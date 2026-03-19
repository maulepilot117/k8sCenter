package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
	"k8s.io/apimachinery/pkg/labels"
)

// handleLogin authenticates a user via credential-based providers (local, LDAP).
// The optional "provider" field in the request body selects which provider to use (default: "local").
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)

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

	if len(creds.Username) > maxUsernameLen || len(creds.Password) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid credentials"},
		})
		return
	}

	// Select provider (default: "local")
	providerID := creds.Provider
	if providerID == "" {
		providerID = "local"
	}

	provider, ok := s.AuthRegistry.GetCredential(providerID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "unknown auth provider"},
		})
		return
	}

	user, err := provider.Authenticate(r.Context(), creds)
	if err != nil {
		entry := s.newAuditEntry(r, creds.Username, audit.ActionLogin, audit.ResultFailure)
		entry.Detail = "invalid credentials (provider: " + providerID + ")"
		s.AuditLogger.Log(r.Context(), entry)
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "invalid credentials"},
		})
		return
	}

	accessToken, err := s.issueTokenPair(w, user)
	if err != nil {
		s.Logger.Error("failed to issue tokens", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	s.AuditLogger.Log(r.Context(), s.newAuditEntry(r, user.Username, audit.ActionLogin, audit.ResultSuccess))

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"accessToken": accessToken,
			"expiresIn":   int(auth.AccessTokenLifetime.Seconds()),
		},
	})
}

// handleRefresh exchanges a valid refresh token for a new access token.
// Implements refresh token rotation — the old token is invalidated.
// Works across all provider types (local, OIDC, LDAP).
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "missing refresh token"},
		})
		return
	}

	session, err := s.Sessions.Validate(cookie.Value)
	if err != nil {
		entry := s.newAuditEntry(r, "", audit.ActionRefresh, audit.ResultFailure)
		entry.Detail = "invalid or expired refresh token"
		s.AuditLogger.Log(r.Context(), entry)
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "invalid or expired refresh token"},
		})
		return
	}

	// For OIDC users, use the cached user data from the session (no local store).
	// For local/LDAP users, look up by ID from the provider registry.
	var user *auth.User
	if session.CachedUser != nil {
		user = session.CachedUser
	} else {
		user, err = s.AuthRegistry.GetUserByID(session.Provider, session.UserID)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, api.Response{
				Error: &api.APIError{Code: 401, Message: "user not found"},
			})
			return
		}
	}

	accessToken, err := s.issueTokenPair(w, user)
	if err != nil {
		s.Logger.Error("failed to issue tokens", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	s.AuditLogger.Log(r.Context(), s.newAuditEntry(r, user.Username, audit.ActionRefresh, audit.ResultSuccess))

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

	s.setRefreshCookie(w, "", -1)

	user, ok := auth.UserFromContext(r.Context())
	if ok {
		s.AuditLogger.Log(r.Context(), s.newAuditEntry(r, user.Username, audit.ActionLogout, audit.ResultSuccess))
	}

	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "logged out"}})
}

// handleAuthProviders returns the list of configured auth providers.
func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.Response{Data: s.AuthRegistry.ListProviders()})
}

// handleOIDCLogin initiates an OIDC authorization flow by redirecting to the provider.
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")

	provider, ok := s.AuthRegistry.GetOIDC(providerID)
	if !ok {
		writeJSON(w, http.StatusNotFound, api.Response{
			Error: &api.APIError{Code: 404, Message: "unknown OIDC provider"},
		})
		return
	}

	redirectURL, err := provider.LoginRedirect()
	if err != nil {
		s.Logger.Error("failed to generate OIDC login redirect", "error", err, "provider", providerID)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to initiate login"},
		})
		return
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// handleOIDCCallback handles the OIDC authorization code callback.
// It exchanges the code for tokens, verifies the ID token, maps claims,
// issues a k8sCenter JWT, and redirects to the frontend.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")

	// Check for OIDC error response
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		desc := r.URL.Query().Get("error_description")
		s.Logger.Warn("OIDC callback error", "provider", providerID, "error", errMsg, "description", desc)
		entry := s.newAuditEntry(r, "", audit.ActionLogin, audit.ResultFailure)
		entry.Detail = "OIDC error: " + errMsg
		s.AuditLogger.Log(r.Context(), entry)
		// Redirect to login with error
		http.Redirect(w, r, "/login?error=oidc_failed", http.StatusFound)
		return
	}

	stateParam := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if stateParam == "" || code == "" {
		http.Redirect(w, r, "/login?error=invalid_request", http.StatusFound)
		return
	}

	// Consume flow state (validates and deletes — single use)
	flowState, err := s.OIDCStateStore.Consume(stateParam)
	if err != nil {
		entry := s.newAuditEntry(r, "", audit.ActionLogin, audit.ResultFailure)
		entry.Detail = "OIDC state validation failed"
		s.AuditLogger.Log(r.Context(), entry)
		http.Redirect(w, r, "/login?error=invalid_state", http.StatusFound)
		return
	}

	// Verify the provider matches
	if flowState.ProviderID != providerID {
		entry := s.newAuditEntry(r, "", audit.ActionLogin, audit.ResultFailure)
		entry.Detail = "OIDC provider mismatch"
		s.AuditLogger.Log(r.Context(), entry)
		http.Redirect(w, r, "/login?error=provider_mismatch", http.StatusFound)
		return
	}

	provider, ok := s.AuthRegistry.GetOIDC(providerID)
	if !ok {
		http.Redirect(w, r, "/login?error=unknown_provider", http.StatusFound)
		return
	}

	user, err := provider.HandleCallback(r.Context(), code, flowState)
	if err != nil {
		s.Logger.Error("OIDC callback failed", "error", err, "provider", providerID)
		entry := s.newAuditEntry(r, "", audit.ActionLogin, audit.ResultFailure)
		entry.Detail = "OIDC authentication failed"
		s.AuditLogger.Log(r.Context(), entry)
		http.Redirect(w, r, "/login?error=auth_failed", http.StatusFound)
		return
	}

	// Issue k8sCenter JWT + refresh cookie
	accessToken, err := s.issueTokenPair(w, user)
	if err != nil {
		s.Logger.Error("failed to issue tokens after OIDC callback", "error", err)
		http.Redirect(w, r, "/login?error=token_failed", http.StatusFound)
		return
	}

	s.AuditLogger.Log(r.Context(), s.newAuditEntry(r, user.Username, audit.ActionLogin, audit.ResultSuccess))

	// Set the access token in a short-lived httpOnly cookie for the frontend to pick up.
	// The frontend callback page reads this cookie via a BFF endpoint, stores the token
	// in memory, and clears the cookie. This avoids exposing the token in URL fragments.
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_access_token",
		Value:    accessToken,
		Path:     "/api/auth/oidc-token-exchange",
		HttpOnly: true,
		Secure:   !s.Config.Dev,
		SameSite: http.SameSiteLaxMode, // Lax to allow cross-origin OIDC redirect
		MaxAge:   60,                   // 60 seconds — just long enough for the redirect
	})

	// Redirect to the frontend OIDC callback page
	http.Redirect(w, r, "/auth/callback", http.StatusFound)
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

	// If ?namespace= is specified, return permissions for that namespace only (+ cluster-scoped).
	// This is the fast path for frontend permission gating (2 API calls instead of N).
	var summary *auth.RBACSummary
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		// Validate namespace — prevent arbitrary strings hitting the k8s API
		if len(ns) > 63 || !isValidDNSLabel(ns) {
			writeJSON(w, http.StatusBadRequest, api.Response{
				Error: &api.APIError{Code: 400, Message: "invalid namespace"},
			})
			return
		}
		var err error
		summary, err = s.RBACChecker.GetNamespacePermissions(r.Context(), user, ns)
		if err != nil {
			s.Logger.Error("failed to get namespace RBAC permissions", "error", err, "user", user.Username, "namespace", ns)
			writeJSON(w, http.StatusInternalServerError, api.Response{
				Error: &api.APIError{Code: 500, Message: "failed to get RBAC summary"},
			})
			return
		}
	} else {
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

		summary, err = s.RBACChecker.GetSummary(r.Context(), user, namespaces)
		if err != nil {
			s.Logger.Error("failed to get RBAC summary", "error", err, "user", user.Username)
			writeJSON(w, http.StatusInternalServerError, api.Response{
				Error: &api.APIError{Code: 500, Message: "failed to get RBAC summary"},
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"user": user,
			"rbac": summary,
		},
	})
}
