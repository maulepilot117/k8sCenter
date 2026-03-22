package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// handleCreateUser creates a new local user with optional k8s identity. Admin only.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "not authenticated"},
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)
	var req struct {
		Username    string   `json:"username"`
		Password    string   `json:"password"`
		K8sUsername string   `json:"k8sUsername"`
		K8sGroups   []string `json:"k8sGroups"`
		Roles       []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	// Validate username
	if req.Username == "" || len(req.Username) > maxUsernameLen || !validUsername.MatchString(req.Username) {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid username format"},
		})
		return
	}

	// Validate password
	if len(req.Password) < 8 || len(req.Password) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "password must be 8-128 characters"},
		})
		return
	}

	// Validate k8sUsername: reject system: prefix
	k8sUser := req.K8sUsername
	if k8sUser == "" {
		k8sUser = req.Username
	}
	if strings.HasPrefix(k8sUser, "system:") {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "k8sUsername cannot start with 'system:' (reserved by Kubernetes)"},
		})
		return
	}
	if len(k8sUser) > maxUsernameLen || !validUsername.MatchString(k8sUser) {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid k8sUsername format"},
		})
		return
	}

	// Validate k8sGroups: reject system:masters
	k8sGroups := req.K8sGroups
	if len(k8sGroups) == 0 {
		k8sGroups = []string{"system:authenticated"}
	}
	for _, g := range k8sGroups {
		if g == "system:masters" {
			writeJSON(w, http.StatusBadRequest, api.Response{
				Error: &api.APIError{Code: 400, Message: "group 'system:masters' cannot be assigned (bypasses all RBAC)"},
			})
			return
		}
	}

	// Default roles to empty if not provided
	roles := req.Roles
	if roles == nil {
		roles = []string{}
	}

	opts := &auth.CreateUserOpts{
		K8sUsername: k8sUser,
		K8sGroups:   k8sGroups,
	}

	user, err := s.LocalAuth.CreateUser(r.Context(), req.Username, req.Password, roles, opts)
	if err != nil {
		if errors.Is(err, auth.ErrDuplicateUser) {
			writeJSON(w, http.StatusConflict, api.Response{
				Error: &api.APIError{Code: 409, Message: "username already exists"},
			})
			return
		}
		s.Logger.Error("failed to create user", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to create user"},
		})
		return
	}

	entry := s.newAuditEntry(r, caller.Username, audit.ActionCreate, audit.ResultSuccess)
	entry.ResourceKind = "User"
	entry.ResourceName = user.Username
	s.AuditLogger.Log(r.Context(), entry)

	writeJSON(w, http.StatusCreated, api.Response{
		Data: map[string]any{
			"username":    user.Username,
			"k8sUsername": user.KubernetesUsername,
			"roles":       user.Roles,
		},
	})
}

// handleListUsers returns all local users. Admin only.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.LocalAuth.Store().List(r.Context())
	if err != nil {
		s.Logger.Error("failed to list users", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to list users"},
		})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data:     users,
		Metadata: &api.Metadata{Total: len(users)},
	})
}

// handleDeleteUser deletes a local user by ID. Admin only.
// Guards: cannot delete yourself, cannot delete the last admin.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "not authenticated"},
		})
		return
	}

	id := chi.URLParam(r, "id")

	// Guard: no self-deletion
	if caller.ID == id {
		writeJSON(w, http.StatusConflict, api.Response{
			Error: &api.APIError{Code: 409, Message: auth.ErrSelfDelete.Error()},
		})
		return
	}

	// Guard: no deleting the last admin.
	// NOTE: This list-then-delete is not transactional. Under concurrent admin
	// sessions, a TOCTOU race could allow deleting the last admin. Acceptable
	// for current single-admin usage; use SELECT ... FOR UPDATE if this becomes
	// a concern (see CreateFirstUser in store/users.go for the pattern).
	users, err := s.LocalAuth.Store().List(r.Context())
	if err != nil {
		s.Logger.Error("failed to list users for admin guard", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to check admin count"},
		})
		return
	}
	adminCount := 0
	isTargetAdmin := false
	for _, u := range users {
		for _, role := range u.Roles {
			if role == "admin" {
				adminCount++
				if u.ID == id {
					isTargetAdmin = true
				}
				break
			}
		}
	}
	if isTargetAdmin && adminCount <= 1 {
		writeJSON(w, http.StatusConflict, api.Response{
			Error: &api.APIError{Code: 409, Message: auth.ErrLastAdmin.Error()},
		})
		return
	}

	if err := s.LocalAuth.Store().Delete(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, api.Response{
				Error: &api.APIError{Code: 404, Message: "user not found"},
			})
			return
		}
		s.Logger.Error("failed to delete user", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to delete user"},
		})
		return
	}

	entry := s.newAuditEntry(r, caller.Username, audit.ActionDelete, audit.ResultSuccess)
	entry.ResourceKind = "User"
	entry.ResourceName = id
	s.AuditLogger.Log(r.Context(), entry)

	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateUserPassword changes a local user's password. Admin only.
func (s *Server) handleUpdateUserPassword(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "not authenticated"},
		})
		return
	}

	id := chi.URLParam(r, "id")

	r.Body = http.MaxBytesReader(w, r.Body, 1024) // 1 KB — generous for a password change
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	if err := s.LocalAuth.UpdatePassword(r.Context(), id, req.Password); err != nil {
		if errors.Is(err, auth.ErrPasswordInvalid) {
			writeJSON(w, http.StatusBadRequest, api.Response{
				Error: &api.APIError{Code: 400, Message: err.Error()},
			})
			return
		}
		if errors.Is(err, auth.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, api.Response{
				Error: &api.APIError{Code: 404, Message: "user not found"},
			})
			return
		}
		s.Logger.Error("failed to update password", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to update password"},
		})
		return
	}

	entry := s.newAuditEntry(r, caller.Username, audit.ActionUpdate, audit.ResultSuccess)
	entry.ResourceKind = "User"
	entry.ResourceName = id
	s.AuditLogger.Log(r.Context(), entry)

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]string{"message": "password updated"},
	})
}
