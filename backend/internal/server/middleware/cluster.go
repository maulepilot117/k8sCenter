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

// ClusterIDFromContext returns the cluster ID from the request context.
// Returns "local" if no cluster ID is set.
func ClusterIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(clusterIDCtxKey).(string); ok {
		return id
	}
	return "local"
}
