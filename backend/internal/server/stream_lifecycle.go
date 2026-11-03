package server

// The shared lifecycle of long-lived streams (remote logs and shells plan,
// KTD4). Every live stream, whatever it carries, ends for the same reasons
// and the same way: its context is cancelled with a *streamEnd cause, and
// the cause becomes the stream's final error frame. This file owns those
// ends, the per-cluster registry that ends a removed cluster's streams at
// once, and the watcher that ends a stream at token expiry or when
// revalidation fails. Flows use it today (flow_streams.go).

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kubecenter/kubecenter/internal/recoverutil"
)

// streamEnd is why a live stream ended. It is the cancel cause of the
// stream's context, and becomes the stream's final error frame.
type streamEnd struct {
	reason    string
	message   string
	retryable bool
}

func (e *streamEnd) Error() string { return e.reason }

func (e *streamEnd) frame() map[string]any {
	f := map[string]any{"type": "error", "message": e.message, "reason": e.reason}
	if e.retryable {
		f["retryable"] = true
	}
	return f
}

// streamEnds is one stream kind's set of lifecycle ends. The reason codes
// are stable, because clients key on them; the messages name the stream.
// No message may contain "auth", "token" or "permission": the mobile client
// treats an error frame carrying any of them as fatal and stops retrying.
type streamEnds struct {
	clusterRemoved       *streamEnd
	accessRevoked        *streamEnd
	tokenExpired         *streamEnd
	signedOut            *streamEnd
	idleTimeout          *streamEnd
	serverRestarting     *streamEnd
	recordingUnavailable *streamEnd
	recordingLimit       *streamEnd
}

// newStreamEnds builds the ends of one stream kind. stream names the stream
// ("the flow stream") and subject what it shows ("these flows").
func newStreamEnds(stream, subject string) *streamEnds {
	return &streamEnds{
		clusterRemoved:       &streamEnd{reason: "cluster_removed", message: "the cluster was removed from k8sCenter; " + stream + " ended"},
		accessRevoked:        &streamEnd{reason: "access_revoked", message: "your access to " + subject + " was revoked; " + stream + " ended"},
		tokenExpired:         &streamEnd{reason: "token_expired", message: "your session needs refreshing; reconnecting", retryable: true},
		signedOut:            &streamEnd{reason: "signed_out", message: "you signed out; " + stream + " ended"},
		idleTimeout:          &streamEnd{reason: "idle_timeout", message: stream + " was idle too long and ended"},
		serverRestarting:     &streamEnd{reason: "server_restarting", message: "k8sCenter is restarting; reconnecting", retryable: true},
		recordingUnavailable: &streamEnd{reason: "recording_unavailable", message: "session recording is unavailable; " + stream + " ended"},
		recordingLimit:       &streamEnd{reason: "recording_limit", message: "the session recording reached its size limit; " + stream + " ended"},
	}
}

// streamRegistry tracks one kind of live stream per cluster, so removing a
// cluster ends its streams at once and shutdown can end them all. Build it
// with newStreamRegistry.
type streamRegistry struct {
	ends    *streamEnds
	mu      sync.Mutex
	next    uint64
	streams map[string]map[uint64]context.CancelCauseFunc
}

func newStreamRegistry(ends *streamEnds) *streamRegistry {
	return &streamRegistry{ends: ends}
}

// add registers a stream's cancel under clusterID. The returned remove
// unregisters it.
func (r *streamRegistry) add(clusterID string, cancel context.CancelCauseFunc) (remove func()) {
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
func (r *streamRegistry) CancelCluster(clusterID string) {
	r.mu.Lock()
	cancels := make([]context.CancelCauseFunc, 0, len(r.streams[clusterID]))
	for _, cancel := range r.streams[clusterID] {
		cancels = append(cancels, cancel)
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel(r.ends.clusterRemoved)
	}
}

// CancelAll ends every registered stream, on every cluster, with end. It
// never blocks on a stream.
func (r *streamRegistry) CancelAll(end *streamEnd) {
	r.mu.Lock()
	var cancels []context.CancelCauseFunc
	for _, byID := range r.streams {
		for _, cancel := range byID {
			cancels = append(cancels, cancel)
		}
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel(end)
	}
}

// streamRegistries lists the registry of every stream kind, so cluster
// eviction and shutdown reach them all.
func (s *Server) streamRegistries() []*streamRegistry {
	return []*streamRegistry{s.flowStreams}
}

// watchStream ends a live stream with its tokenExpired end when the access
// token it authenticated with expires, and with whatever end revalidate
// returns, checked every interval. A nil from revalidate leaves the stream
// running, and so does a revalidate that panics. Both stop when ctx ends.
func (s *Server) watchStream(ctx context.Context, cancel context.CancelCauseFunc, ends *streamEnds, label string, interval time.Duration, revalidate func(context.Context) *streamEnd) {
	if exp, ok := wsTokenExpiry(ctx); ok {
		expiry := time.AfterFunc(time.Until(exp), func() { cancel(ends.tokenExpired) })
		context.AfterFunc(ctx, func() { expiry.Stop() })
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var end *streamEnd
			recoverutil.Tick(ctx, s.Logger, label, func(ctx context.Context) {
				end = revalidate(ctx)
			})
			if end != nil {
				cancel(end)
				return
			}
		}
	}()
}

// writeStreamEnd sends the final frame of a stream whose context ended. A
// stream the client closed gets none.
func writeStreamEnd(conn *websocket.Conn, ctx context.Context) {
	var end *streamEnd
	if errors.As(context.Cause(ctx), &end) {
		conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
		conn.WriteJSON(end.frame())
	}
}
