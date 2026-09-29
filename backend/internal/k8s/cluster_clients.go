package k8s

import (
	"context"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// ClusterClients is the part of ClusterRouter a feature handler needs to act
// on the cluster a request targets. Handlers depend on this interface rather
// than *ClusterRouter so tests can stand in a fake remote cluster: the SSRF
// dial policy refuses loopback, so an httptest server cannot play one.
type ClusterClients interface {
	// ClientForCluster returns a typed client impersonating the identity on
	// clusterID. A remote failure is an error, never a local fallback.
	ClientForCluster(ctx context.Context, clusterID, username string, groups []string) (kubernetes.Interface, error)
	// DynamicClientForCluster is ClientForCluster for the dynamic client.
	DynamicClientForCluster(ctx context.Context, clusterID, username string, groups []string) (dynamic.Interface, error)
	// TargetSchemaFor returns the discovery client and RESTMapper of
	// clusterID as seen by the identity.
	TargetSchemaFor(ctx context.Context, clusterID, username string, groups []string) (*TargetSchema, error)
}

var _ ClusterClients = (*ClusterRouter)(nil)

// IdentityKey returns the collision-resistant key for an impersonated
// identity, the same one ClusterRouter uses for its client and schema caches.
// Caches of per-identity remote data key on it so no entry is ever served to
// another identity.
func IdentityKey(username string, groups []string) string {
	return cacheKey(username, groups)
}
