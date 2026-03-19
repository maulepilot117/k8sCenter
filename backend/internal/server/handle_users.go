package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

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

	// Guard: no deleting the last admin
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
