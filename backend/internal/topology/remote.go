package topology

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// RemoteReadTimeout bounds one request's reads on a remote cluster: client
// resolution, every list and the graph build. Shared by the topology and
// diagnostics handlers. A var so tests can shorten it.
var RemoteReadTimeout = 10 * time.Second

// ErrNoClusterClients is returned by NewRemoteBuilder when no
// k8s.ClusterClients are wired. It only happens on a wiring mistake; callers
// answer it with their own 500.
var ErrNoClusterClients = errors.New("remote cluster requested but no cluster clients are wired")

// WithRemoteTimeout bounds ctx by RemoteReadTimeout when clusterID names a
// remote cluster. For the local cluster it returns ctx unchanged and a no-op
// cancel, so callers can always defer the cancel.
func WithRemoteTimeout(ctx context.Context, clusterID string) (context.Context, context.CancelFunc) {
	if k8s.IsLocalClusterID(clusterID) {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, RemoteReadTimeout)
}

// NewRemoteBuilder resolves a client for clusterID impersonating user and
// returns a per-request RemoteLister over it, and a Builder over that lister
// with no overlay providers (those read local-cluster inventories). There is
// never a local fallback. A nil clients is ErrNoClusterClients; a resolution
// failure is returned as is, for httputil.WriteTargetError.
func NewRemoteBuilder(ctx context.Context, clients k8s.ClusterClients, clusterID string, user *auth.User, logger *slog.Logger) (*Builder, *RemoteLister, error) {
	if clients == nil {
		return nil, nil, ErrNoClusterClients
	}
	cs, err := clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, nil, err
	}
	lister := NewRemoteLister(cs, logger)
	return NewBuilder(lister, nil, logger), lister, nil
}
