package server

// Live remote Hubble flow streams (#608 slice 5, U11). A remote stream runs
// over a port-forward tunnel to that cluster's relay (networking/
// relay_tunnel.go). Authorization is checked when the tunnel opens, so a
// stream also ends, with a stated reason, when (KTD11):
//   - its cluster is removed: at once on this replica, through the evict
//     hook, and at the next revalidation on the others;
//   - revalidation finds the user's access revoked;
//   - the access token expires, bounding k8sCenter-side revocation to the
//     token's lifetime;
//   - the relay restarts.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/networking"
)

// flowRevalidateInterval is how often a live remote stream rechecks its
// cluster and the user's access. It matches the resource hub's RBAC
// revalidation (websocket/hub.go rbacRevalidateInterval).
var flowRevalidateInterval = 5 * time.Minute

// flowStream is an open remote flow source, as the flows handler uses it.
// *networking.RemoteFlowSource implements it.
type flowStream interface {
	StreamFlows(ctx context.Context, namespace, verdict string, cb func(networking.FlowRecord)) error
	Done() <-chan struct{}
	EndedByRelay() bool
	RelayNamespace() string
	Close()
}

// flowStreamOpener opens a live flow source on a remote cluster for the
// user's view of namespace. A refusal is a *networking.FlowSourceError.
type flowStreamOpener func(ctx context.Context, clusterID string, user *auth.User, namespace string) (flowStream, error)

// remoteFlowOpener returns the opener for live remote streams, or nil when
// remote flows are not wired.
func (s *Server) remoteFlowOpener() flowStreamOpener {
	if s.openRemoteFlows != nil {
		return s.openRemoteFlows
	}
	if s.NetworkingHandler == nil {
		return nil
	}
	nh := s.NetworkingHandler
	return func(ctx context.Context, clusterID string, user *auth.User, namespace string) (flowStream, error) {
		src, err := nh.OpenRemoteFlowSource(ctx, clusterID, user, namespace, networking.FlowSourceLive)
		if err != nil {
			return nil, err
		}
		return src, nil
	}
}

// flowStreamAccess returns the checker revalidation uses, or nil when none
// is wired.
func (s *Server) flowStreamAccess() networking.NamespaceAccess {
	if s.flowAccess != nil {
		return s.flowAccess
	}
	if s.ResourceHandler == nil || s.ResourceHandler.AccessChecker == nil {
		return nil
	}
	return s.ResourceHandler.AccessChecker
}

// flowStreamEnds are the lifecycle ends of a live flow stream
// (stream_lifecycle.go).
var flowStreamEnds = newStreamEnds("the flow stream", "these flows")

// endRelayRestarted ends a remote stream whose relay went away. It is the
// one end flows have that the shared lifecycle does not.
var endRelayRestarted = &streamEnd{reason: networking.ReasonRelayRestarted, message: "Hubble Relay restarted; reconnecting", retryable: true}

// revalidateFlowStream rechecks a live remote stream: the cluster is still
// registered, the user can still list pods in the flow namespace, and still
// holds create pods/portforward where the relay runs, the grant that
// authorized the tunnel. It returns the end to apply, or nil to continue. A
// check that cannot be answered leaves the stream running; the token's
// expiry still bounds it.
func (s *Server) revalidateFlowStream(ctx context.Context, clusterID string, user *auth.User, namespace, relayNamespace string) *streamEnd {
	if records := s.metricsClusterGetter(); records != nil {
		if _, err := records.Get(ctx, clusterID); errors.Is(err, pgx.ErrNoRows) {
			return flowStreamEnds.clusterRemoved
		}
	}
	access := s.flowStreamAccess()
	if access == nil {
		return nil
	}
	checks := []struct{ verb, resource, namespace string }{
		{"list", "pods", namespace},
		{"create", "pods/portforward", relayNamespace},
	}
	for _, c := range checks {
		allowed, err := access.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, c.verb, c.resource, c.namespace)
		if err != nil {
			s.Logger.Warn("flow stream revalidation failed", "cluster", clusterID, "user", user.Username, "error", err)
			continue
		}
		if !allowed {
			return flowStreamEnds.accessRevoked
		}
	}
	return nil
}
