package middleware

import (
	"context"
	"net/http"

	"github.com/kubecenter/kubecenter/internal/auth"
)

type clusterContextKey int

const clusterIDCtxKey clusterContextKey = 0

// ClusterContext extracts the X-Cluster-ID header and stores it in the request context.
// Non-local cluster access requires the admin role.
func ClusterContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clusterID := r.Header.Get("X-Cluster-ID")
		if clusterID == "" || clusterID == "local" {
			clusterID = "local"
		} else {
			// Validate header length (cluster IDs are 32-char hex strings)
			if len(clusterID) > 64 {
				writeAuthError(w, http.StatusBadRequest, "invalid X-Cluster-ID")
				return
			}
			// Non-local cluster access requires admin role
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !auth.IsAdmin(user) {
				writeAuthError(w, http.StatusForbidden, "admin role required for remote cluster access")
				return
			}
		}
		ctx := context.WithValue(r.Context(), clusterIDCtxKey, clusterID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WSClusterContext is a WebSocket-friendly variant of ClusterContext that
// reads the X-Cluster-ID header before the connection is upgraded and stores
// the resolved value in the request context. Unlike ClusterContext, it does
// NOT enforce the admin-required check — WS handlers do authentication in-
// band after upgrade (see ws_helpers.wsAuthAndUpgrade), so user identity
// isn't in the request context yet at middleware time.
//
// Authorization for non-local clusters is enforced downstream by the
// per-handler gates (e.g. handle_ws_logs returns "remote clusters not
// supported" when the resolved clusterID is non-local). Without this
// middleware, those gates always saw "local" because ClusterContext only
// ran inside the /api/v1 REST group, leaving the WS routes blind to
// X-Cluster-ID — F#6 of the security audit re-review.
func WSClusterContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clusterID := r.Header.Get("X-Cluster-ID")
		if clusterID == "" {
			clusterID = "local"
		}
		// Same length cap as ClusterContext — guards downstream handlers
		// that log the cluster ID against unbounded header abuse.
		if len(clusterID) > 64 {
			writeAuthError(w, http.StatusBadRequest, "invalid X-Cluster-ID")
			return
		}
		ctx := context.WithValue(r.Context(), clusterIDCtxKey, clusterID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ClusterIDFromContext returns the cluster ID from the request context.
// Returns "local" if no cluster ID is set.
func ClusterIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(clusterIDCtxKey).(string); ok {
		return id
	}
	return "local"
}

// WithClusterID returns a derived context carrying the given cluster ID.
// Intended for tests and code paths that bypass the ClusterContext HTTP
// middleware (e.g. background goroutines reconstructing a request scope).
func WithClusterID(ctx context.Context, clusterID string) context.Context {
	return context.WithValue(ctx, clusterIDCtxKey, clusterID)
}
