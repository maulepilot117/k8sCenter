package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	"github.com/kubecenter/kubecenter/pkg/version"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) registerRoutes() {
	// Health endpoints — no auth required (skipped by auth middleware)
	s.Router.Get("/healthz", s.handleHealthz)
	s.Router.Get("/readyz", s.handleReadyz)

	// API v1
	s.Router.Route("/api/v1", func(r chi.Router) {
		// Auth endpoints — rate limited, no auth required
		r.Route("/auth", func(ar chi.Router) {
			ar.With(middleware.RateLimit(s.RateLimiter)).Post("/login", s.handleLogin)
			ar.Post("/refresh", s.handleRefresh)
			ar.Post("/logout", s.handleLogout)
			ar.Get("/providers", s.handleAuthProviders)
			ar.Get("/me", s.handleAuthMe)
		})

		// Setup endpoint — rate limited, no auth required
		r.With(middleware.RateLimit(s.RateLimiter)).Post("/setup/init", s.handleSetupInit)

		// Authenticated endpoints
		r.Get("/cluster/info", s.handleClusterInfo)
	})
}

// handleHealthz is a trivial liveness check — if the server can respond, it's alive.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "ok"}})
}

// handleReadyz checks whether the server is ready to serve traffic.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.ready() {
		writeJSON(w, http.StatusServiceUnavailable, api.Response{
			Error: &api.APIError{
				Code:    503,
				Message: "server is not ready",
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "ready"}})
}

// handleClusterInfo returns basic cluster information.
func (s *Server) handleClusterInfo(w http.ResponseWriter, r *http.Request) {
	cs := s.K8sClient.BaseClientset()

	serverVersion, err := cs.Discovery().ServerVersion()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to query cluster info"},
		})
		s.Logger.Error("failed to get server version", "error", err)
		return
	}

	nodes, err := s.Informers.Factory().Core().V1().Nodes().Lister().List(labels.Everything())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to list nodes"},
		})
		s.Logger.Error("failed to list nodes from informer", "error", err)
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"clusterID":         s.Config.ClusterID,
			"kubernetesVersion": serverVersion.GitVersion,
			"platform":          serverVersion.Platform,
			"nodeCount":         len(nodes),
			"kubecenter":        version.Get(),
		},
	})
}

// handleSetupInit creates the first admin user. Returns 410 Gone if any user exists.
func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	if s.LocalAuth.UserCount() > 0 {
		writeJSON(w, http.StatusGone, api.Response{
			Error: &api.APIError{Code: 410, Message: "setup already completed"},
		})
		return
	}

	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		SetupToken string `json:"setupToken,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "username and password are required"},
		})
		return
	}

	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "password must be at least 8 characters"},
		})
		return
	}

	// Verify setup token if configured
	if s.Config.Auth.SetupToken != "" && req.SetupToken != s.Config.Auth.SetupToken {
		s.Logger.Warn("setup init rejected: invalid setup token", "remoteAddr", r.RemoteAddr)
		writeJSON(w, http.StatusForbidden, api.Response{
			Error: &api.APIError{Code: 403, Message: "invalid setup token"},
		})
		return
	}

	user, err := s.LocalAuth.CreateUser(req.Username, req.Password, []string{"admin"})
	if err != nil {
		s.Logger.Error("failed to create admin user", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to create admin user"},
		})
		return
	}

	s.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp: time.Now(),
		ClusterID: s.Config.ClusterID,
		User:      user.Username,
		SourceIP:  r.RemoteAddr,
		Action:    audit.ActionSetup,
		Result:    audit.ResultSuccess,
		Detail:    "initial admin account created",
	})

	writeJSON(w, http.StatusCreated, api.Response{
		Data: map[string]any{
			"user": user,
		},
	})
}

// handleLogin authenticates a user and returns a JWT access token.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var creds auth.Credentials
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	if creds.Username == "" || creds.Password == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "username and password are required"},
		})
		return
	}

	user, err := s.LocalAuth.Authenticate(r.Context(), creds)
	if err != nil {
		s.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp: time.Now(),
			ClusterID: s.Config.ClusterID,
			User:      creds.Username,
			SourceIP:  r.RemoteAddr,
			Action:    audit.ActionLogin,
			Result:    audit.ResultFailure,
			Detail:    "invalid credentials",
		})
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "invalid credentials"},
		})
		return
	}

	accessToken, err := s.TokenManager.IssueAccessToken(user)
	if err != nil {
		s.Logger.Error("failed to issue access token", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	refreshToken, err := auth.GenerateRefreshToken()
	if err != nil {
		s.Logger.Error("failed to generate refresh token", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	s.Sessions.Store(auth.RefreshSession{
		Token:     refreshToken,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(auth.RefreshTokenLifetime),
	})

	// Set refresh token as httpOnly cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   !s.Config.Dev,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.RefreshTokenLifetime.Seconds()),
	})

	s.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp: time.Now(),
		ClusterID: s.Config.ClusterID,
		User:      user.Username,
		SourceIP:  r.RemoteAddr,
		Action:    audit.ActionLogin,
		Result:    audit.ResultSuccess,
	})

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"accessToken": accessToken,
			"expiresIn":   int(auth.AccessTokenLifetime.Seconds()),
		},
	})
}

// handleRefresh exchanges a valid refresh token for a new access token.
// Implements refresh token rotation — the old token is invalidated.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "missing refresh token"},
		})
		return
	}

	userID, err := s.Sessions.Validate(cookie.Value)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "invalid or expired refresh token"},
		})
		return
	}

	user, err := s.LocalAuth.GetUserByID(userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "user not found"},
		})
		return
	}

	accessToken, err := s.TokenManager.IssueAccessToken(user)
	if err != nil {
		s.Logger.Error("failed to issue access token", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	// Rotation: issue new refresh token
	newRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		s.Logger.Error("failed to generate refresh token", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	s.Sessions.Store(auth.RefreshSession{
		Token:     newRefresh,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(auth.RefreshTokenLifetime),
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    newRefresh,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   !s.Config.Dev,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.RefreshTokenLifetime.Seconds()),
	})

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"accessToken": accessToken,
			"expiresIn":   int(auth.AccessTokenLifetime.Seconds()),
		},
	})
}

// handleLogout invalidates the user's refresh token.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err == nil {
		s.Sessions.Revoke(cookie.Value)
	}

	// Clear the cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   !s.Config.Dev,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})

	user, ok := auth.UserFromContext(r.Context())
	if ok {
		s.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp: time.Now(),
			ClusterID: s.Config.ClusterID,
			User:      user.Username,
			SourceIP:  r.RemoteAddr,
			Action:    audit.ActionLogout,
			Result:    audit.ResultSuccess,
		})
	}

	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "logged out"}})
}

// handleAuthProviders returns the list of configured auth providers.
func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	providers := []map[string]string{
		{"type": "local", "name": "Local Accounts"},
	}
	writeJSON(w, http.StatusOK, api.Response{Data: providers})
}

// handleAuthMe returns the authenticated user's info and RBAC summary.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "not authenticated"},
		})
		return
	}

	// Get list of namespaces from informer
	nsList, err := s.Informers.Factory().Core().V1().Namespaces().Lister().List(labels.Everything())
	if err != nil {
		s.Logger.Error("failed to list namespaces for RBAC summary", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to get RBAC summary"},
		})
		return
	}

	namespaces := make([]string, len(nsList))
	for i, ns := range nsList {
		namespaces[i] = ns.Name
	}

	summary, err := s.RBACChecker.GetSummary(r.Context(), user, namespaces)
	if err != nil {
		s.Logger.Error("failed to get RBAC summary", "error", err, "user", user.Username)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to get RBAC summary"},
		})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"user": user,
			"rbac": summary,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

