package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const (
	maxAuthBodySize = 1 << 16 // 64 KB — more than enough for auth payloads
	maxPasswordLen  = 128
	maxUsernameLen  = 253 // k8s username limit
)

var validUsername = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]*$`)

// handleSetupStatus returns whether the instance needs initial setup.
// Public endpoint — only returns a boolean, never leaks user count or settings state.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.LocalAuth.Store().Count(r.Context())
	if err != nil {
		s.Logger.Error("failed to count users for setup status", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "internal error"},
		})
		return
	}
	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"needsSetup": count == 0,
		},
	})
}

// handleSetupInit creates the first admin user. Returns 410 Gone if any user exists.
func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)

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

	if len(req.Username) > maxUsernameLen || !validUsername.MatchString(req.Username) {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid username format"},
		})
		return
	}

	if len(req.Password) < 8 || len(req.Password) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "password must be 8-128 characters"},
		})
		return
	}

	// Verify setup token if configured — constant-time comparison
	if s.Config.Auth.SetupToken != "" {
		if subtle.ConstantTimeCompare([]byte(req.SetupToken), []byte(s.Config.Auth.SetupToken)) != 1 {
			s.Logger.Warn("setup init rejected: invalid setup token", "remoteAddr", r.RemoteAddr)
			writeJSON(w, http.StatusForbidden, api.Response{
				Error: &api.APIError{Code: 403, Message: "invalid setup token"},
			})
			return
		}
	}

	// Atomic: checks no users exist AND creates under the same lock.
	user, err := s.LocalAuth.CreateFirstUser(r.Context(), req.Username, req.Password, []string{"admin"})
	if err != nil {
		if errors.Is(err, auth.ErrSetupCompleted) {
			writeJSON(w, http.StatusGone, api.Response{
				Error: &api.APIError{Code: 410, Message: "setup already completed"},
			})
			return
		}
		s.Logger.Error("failed to create admin user", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to create admin user"},
		})
		return
	}

	entry := s.newAuditEntry(r, user.Username, audit.ActionSetup, audit.ResultSuccess)
	entry.Detail = "initial admin account created"
	s.AuditLogger.Log(r.Context(), entry)

	writeJSON(w, http.StatusCreated, api.Response{
		Data: map[string]any{
			"username": user.Username,
			"created":  true,
		},
	})
}
