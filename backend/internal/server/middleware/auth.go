package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// Auth returns middleware that validates JWT Bearer tokens.
// Apply this only to route groups that require authentication —
// public routes should be registered outside this middleware group.
func Auth(tm *auth.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			token, found := strings.CutPrefix(header, "Bearer ")
			if !found || token == "" {
				writeAuthError(w, http.StatusUnauthorized, "invalid authorization header format")
				return
			}

			claims, err := tm.ValidateAccessToken(token)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			user := auth.UserFromClaims(claims)
			ctx := auth.ContextWithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CSRF returns middleware that requires the X-Requested-With header on
// state-changing requests (POST, PUT, PATCH, DELETE).
// This prevents CSRF attacks since browsers won't add custom headers in cross-origin requests.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if r.Header.Get("X-Requested-With") == "" {
				writeAuthError(w, http.StatusForbidden, "missing X-Requested-With header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin returns middleware that restricts access to users with the "admin" role.
// Must be applied after Auth middleware (requires user in context).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		if !auth.IsAdmin(user) {
			writeAuthError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := api.Response{
		Error: &api.APIError{
			Code:    status,
			Message: message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}
