package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// skipAuthPaths are endpoints that do not require authentication.
var skipAuthPaths = map[string]bool{
	"/healthz":                true,
	"/readyz":                 true,
	"/api/v1/auth/login":     true,
	"/api/v1/auth/refresh":   true,
	"/api/v1/auth/providers": true,
	"/api/v1/setup/init":     true,
}

// Auth returns middleware that validates JWT Bearer tokens.
// Requests to skip-listed paths pass through without auth.
// Authenticated user is injected into the request context.
func Auth(tm *auth.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipAuthPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

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

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := api.Response{
		Error: &api.APIError{
			Code:    status,
			Message: message,
		},
	}
	// Best-effort JSON encoding — if this fails, the status code is already sent
	json.NewEncoder(w).Encode(resp)
}
