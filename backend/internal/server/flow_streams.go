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
	"sync"
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

// flowStreamEnd is why a live stream ended. It is the cancel cause of the
// stream's context, and becomes the stream's final error frame.
type flowStreamEnd struct {
	reason    string
	message   string
	retryable bool
}

func (e *flowStreamEnd) Error() string { return e.reason }

func (e *flowStreamEnd) frame() map[string]any {
	f := map[string]any{"type": "error", "message": e.message, "reason": e.reason}
	if e.retryable {
		f["retryable"] = true
	}
	return f
}

var (
	endClusterRemoved = &flowStreamEnd{reason: "cluster_removed", message: "the cluster was removed from k8sCenter; the flow stream ended"}
	endAccessRevoked  = &flowStreamEnd{reason: "access_revoked", message: "your access to these flows was revoked; the flow stream ended"}
	endTokenExpired   = &flowStreamEnd{reason: "token_expired", message: "your session needs refreshing; reconnecting", retryable: true}
	endRelayRestarted = &flowStreamEnd{reason: networking.ReasonRelayRestarted, message: "Hubble Relay restarted; reconnecting", retryable: true}
)

// flowStreamRegistry tracks active remote streams per cluster, so removing
// a cluster ends its streams at once. The zero value is ready to use.
type flowStreamRegistry struct {
	mu      sync.Mutex
	next    uint64
	streams map[string]map[uint64]context.CancelCauseFunc
}

// add registers a stream's cancel under clusterID. The returned remove
// unregisters it.
func (r *flowStreamRegistry) add(clusterID string, cancel context.CancelCauseFunc) (remove func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streams == nil {
		r.streams = map[string]map[uint64]context.CancelCauseFunc{}
	}
	if r.streams[clusterID] == nil {
		r.streams[clusterID] = map[uint64]context.CancelCauseFunc{}
	}
	r.next++
	id := r.next
	r.streams[clusterID][id] = cancel
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.streams[clusterID], id)
		if len(r.streams[clusterID]) == 0 {
			delete(r.streams, clusterID)
		}
	}
}

// CancelCluster ends every live stream on clusterID with cluster_removed.
// Registered as a ClusterRouter evict hook; it never blocks on a stream.
func (r *flowStreamRegistry) CancelCluster(clusterID string) {
	r.mu.Lock()
	cancels := make([]context.CancelCauseFunc, 0, len(r.streams[clusterID]))
	for _, cancel := range r.streams[clusterID] {
		cancels = append(cancels, cancel)
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel(endClusterRemoved)
	}
}

// revalidateFlowStream rechecks a live remote stream: the cluster is still
// registered, the user can still list pods in the flow namespace, and still
// holds create pods/portforward where the relay runs, the grant that
// authorized the tunnel. It returns the end to apply, or nil to continue. A
// check that cannot be answered leaves the stream running; the token's
// expiry still bounds it.
func (s *Server) revalidateFlowStream(ctx context.Context, clusterID string, user *auth.User, namespace, relayNamespace string) *flowStreamEnd {
	if records := s.metricsClusterGetter(); records != nil {
		if _, err := records.Get(ctx, clusterID); errors.Is(err, pgx.ErrNoRows) {
			return endClusterRemoved
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
			return endAccessRevoked
		}
	}
	return nil
}
