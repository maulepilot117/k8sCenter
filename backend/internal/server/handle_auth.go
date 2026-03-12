package server

import (
	"encoding/json"
	"net/http"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
	"k8s.io/apimachinery/pkg/labels"
)

// handleLogin authenticates a user and returns a JWT access token.
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

	user, err := s.LocalAuth.Authenticate(r.Context(), creds)
	if err != nil {
		entry := s.newAuditEntry(r, creds.Username, audit.ActionLogin, audit.ResultFailure)
		entry.Detail = "invalid credentials"
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
		entry := s.newAuditEntry(r, "", audit.ActionRefresh, audit.ResultFailure)
		entry.Detail = "invalid or expired refresh token"
		s.AuditLogger.Log(r.Context(), entry)
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
