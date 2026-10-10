package topology

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// RemoteReadTimeout bounds one request's reads on a remote cluster: client
// resolution, every list and the graph build. Shared by the topology and
// diagnostics handlers. A var so tests can shorten it.
var RemoteReadTimeout = 10 * time.Second

// ErrNoClusterClients is returned by NewRemoteListerFor when no
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

// NewRemoteListerFor resolves a client for clusterID impersonating user and
// returns a per-request RemoteLister over it. A graph over it is built with
// NewBuilder(lister, nil, logger): the overlay providers read local-cluster
// inventories. There is never a local fallback. A nil clients is
// ErrNoClusterClients; a resolution failure is returned as is, for
// httputil.WriteTargetError.
func NewRemoteListerFor(ctx context.Context, clients k8s.ClusterClients, clusterID string, user *auth.User, logger *slog.Logger) (*RemoteLister, error) {
	if clients == nil {
		return nil, ErrNoClusterClients
	}
	cs, err := clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, err
	}
	return NewRemoteLister(cs, logger), nil
}

// Prefetcher is an optional ResourceLister capability: Prefetch reads the
// given kinds (Kind* constants) in namespace ahead of the List calls, which
// then return the remembered results. RemoteLister implements it so a remote
// graph's lists run concurrently instead of one round trip after another.
type Prefetcher interface {
	Prefetch(ctx context.Context, namespace string, kinds ...string)
}

// graphKinds are the kinds a namespace graph lists, in build order.
var graphKinds = []string{
	KindPods, KindServices, KindDeployments, KindReplicaSets,
	KindStatefulSets, KindDaemonSets, KindJobs, KindCronJobs,
	KindIngresses, KindConfigMaps, KindPVCs, KindHPAs,
}

// The failure policy of a graph build depends on where its lister reads.
//
// Local (the informers): a permission check that errors counts as a denial,
// and a list error leaves the kind out.
//
// Remote (a *RemoteLister): an unanswered permission check, or a list that
// failed for any reason but these three, fails the whole build, so a dead,
// slow or credential-broken cluster never reads as a healthy namespace:
//   - forbidden: the kind is left out, as a denial is;
//   - over the read cap (*TruncatedError), or out of time (the request's
//     deadline or cancellation): the kind is left out and the graph is
//     marked Truncated, with the kind named in Errors.
//
// The local path marks the last two the same way.

// canListKind is canAccess for one of a graph's own kinds. On a remote
// cluster an error is returned rather than read as a denial.
func canListKind(ctx context.Context, user *auth.User, checker *resources.AccessChecker, kind, namespace string, remote bool) (bool, error) {
	if !remote {
		return canAccess(ctx, user, checker, kind, namespace), nil
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	allowed, err := checker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", kind, namespace)
	if err != nil {
		return false, fmt.Errorf("checking list access to %s: %w", kind, err)
	}
	return allowed, nil
}

// listFailures records the list failures of one graph build.
type listFailures struct {
	b         *Builder
	graph     *Graph
	namespace string
	remote    bool
	// err is the first remote list failure that fails the build.
	err error
}

// add records that resource's list failed with err. Each message put in
// graph.Errors is a fixed sentence built from the resource name and a count,
// never err's text, which can carry a remote API server's address.
func (f *listFailures) add(resource string, err error) {
	f.b.logger.Warn("failed to list "+resource, "namespace", f.namespace, "error", err)
	var msg string
	var te *TruncatedError
	switch {
	case errors.As(err, &te):
		msg = fmt.Sprintf("more than %d %s in this namespace on the selected cluster; they are left out of this graph", te.Read, resource)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		msg = fmt.Sprintf("%s in this namespace could not be read in time; they are left out of this graph", resource)
	case f.remote && !apierrors.IsForbidden(err):
		if f.err == nil {
			f.err = fmt.Errorf("listing %s: %w", resource, err)
		}
		return
	default:
		return
	}
	f.graph.Truncated = true
	if f.graph.Errors == nil {
		f.graph.Errors = map[string]string{}
	}
	f.graph.Errors[resource] = msg
}
