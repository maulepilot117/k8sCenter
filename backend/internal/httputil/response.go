// Package httputil provides shared HTTP handler helpers used across
// resource and YAML handler packages.
package httputil

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// WriteError writes a JSON error response using the standard envelope.
// Internal error details are logged server-side but not exposed to the client
// for 5xx errors to prevent information disclosure.
func WriteError(w http.ResponseWriter, status int, message, detail string) {
	if detail != "" && status >= 500 {
		slog.Error("internal error detail", "status", status, "message", message, "detail", detail)
		detail = "" // strip internal details from 5xx responses
	}
	WriteJSON(w, status, api.Response{
		Error: &api.APIError{
			Code:    status,
			Message: message,
			Detail:  detail,
		},
	})
}

// WriteData writes a data response with the standard JSON envelope.
func WriteData(w http.ResponseWriter, data any) {
	WriteJSON(w, http.StatusOK, api.Response{Data: data})
}

// RequireUser extracts the authenticated user from the request context.
// Returns the user and true if found; otherwise writes a 401 and returns false.
func RequireUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, "authentication required", "")
		return nil, false
	}
	return user, true
}
