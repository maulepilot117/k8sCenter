package rubrik

// health.go - records the outcome of the client's RSC contacts on the stored
// connection, so GET /rubrik/status reports R3's health and the last
// successful contact (AE6) from real calls. Every RSC call (API reads,
// writes and the poller's ticks) goes through Client.Query, which reports
// here; Probe checks unsaved credentials and reports nothing.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	// healthWriteInterval: an outcome of the same kind at the same
	// revision as the last one written is written again only after this
	// long, so a busy replica does not write on every request. A change of
	// kind or revision is written at once.
	healthWriteInterval = time.Minute
	// healthWriteTimeout bounds one write. It is detached from the caller,
	// so a request that gives up does not drop the outcome it produced.
	healthWriteTimeout = 5 * time.Second
)

// ContactRecorder receives the outcome of the client's RSC contacts. A
// ConnectionSource that also implements it is told about every contact;
// StoreConnection does. revision is the connection revision the contact was
// made under, and an implementation writes nothing once the connection has
// been saved since. kind is one of the store.RubrikFailure constants: the
// class of the error, never its text.
type ContactRecorder interface {
	RecordContactSuccess(ctx context.Context, revision int64) error
	RecordContactFailure(ctx context.Context, revision int64, kind string) error
}

// contactStore is the part of store.RubrikStore that records contact
// outcomes. It is separate from ConnectionStore so that a read-only store
// stays a valid connection source.
type contactStore interface {
	RecordConnectionSuccess(ctx context.Context, id string, revision int64) (bool, error)
	RecordConnectionFailure(ctx context.Context, id string, revision int64, kind string) (bool, error)
}

var (
	_ contactStore    = (*store.RubrikStore)(nil)
	_ ContactRecorder = StoreConnection{}
)

// RecordContactSuccess implements ContactRecorder. It writes nothing when
// the store cannot record outcomes.
func (c StoreConnection) RecordContactSuccess(ctx context.Context, revision int64) error {
	cs, ok := c.Store.(contactStore)
	if !ok {
		return nil
	}
	_, err := cs.RecordConnectionSuccess(ctx, c.ID, revision)
	return err
}

// RecordContactFailure implements ContactRecorder. It writes nothing when
// the store cannot record outcomes.
func (c StoreConnection) RecordContactFailure(ctx context.Context, revision int64, kind string) error {
	cs, ok := c.Store.(contactStore)
	if !ok {
		return nil
	}
	_, err := cs.RecordConnectionFailure(ctx, c.ID, revision, kind)
	return err
}

// contactFailureKind returns the kind to record for a Query outcome: "" for
// a success, a store.RubrikFailure constant for a failed contact, and
// record false for an outcome that says nothing about the tenant (no
// connection, an unreadable store, a request that could not be encoded, or
// a caller that gave up). A tenant URL that now resolves to a refused
// address is unreachable: k8sCenter will not contact it.
func contactFailureKind(err error) (kind string, record bool) {
	switch {
	case err == nil:
		return "", true
	case errors.Is(err, context.Canceled),
		errors.Is(err, ErrNotConfigured),
		errors.Is(err, ErrConnectionUnavailable),
		errors.Is(err, ErrInvalidRequest):
		return "", false
	case errors.Is(err, ErrAuthFailed):
		return store.RubrikFailureAuth, true
	case errors.Is(err, ErrRateLimited):
		return store.RubrikFailureRateLimited, true
	case errors.Is(err, ErrGraphQL):
		return store.RubrikFailureGraphQL, true
	case errors.Is(err, ErrUnreadable):
		return store.RubrikFailureUnreadable, true
	case errors.Is(err, ErrUnreachable), errors.Is(err, ErrInvalidTenantURL):
		return store.RubrikFailureUnreachable, true
	}
	return "", false
}

// healthMark is the last outcome a tracker wrote.
type healthMark struct {
	revision int64
	kind     string
	at       time.Time
}

// healthTracker throttles a client's outcome writes: at most one per
// healthWriteInterval while the kind and revision stay the same. It is per
// replica, so the stored health can trail this replica's latest outcome by
// up to healthWriteInterval when another replica (or a concurrent call)
// wrote a different kind in between. A failed write is logged and retried
// on the next outcome after the interval or of another kind. A nil tracker
// records nothing.
type healthTracker struct {
	rec    ContactRecorder
	logger *slog.Logger
	now    func() time.Time

	mu   sync.Mutex
	last *healthMark
}

func newHealthTracker(rec ContactRecorder, logger *slog.Logger, now func() time.Time) *healthTracker {
	return &healthTracker{rec: rec, logger: logger, now: now}
}

// observe records the outcome err of a contact made under revision.
func (h *healthTracker) observe(ctx context.Context, revision int64, err error) {
	if h == nil {
		return
	}
	kind, record := contactFailureKind(err)
	if !record {
		return
	}
	now := h.now()
	h.mu.Lock()
	if l := h.last; l != nil && l.revision == revision && l.kind == kind && now.Sub(l.at) < healthWriteInterval {
		h.mu.Unlock()
		return
	}
	h.last = &healthMark{revision: revision, kind: kind, at: now}
	h.mu.Unlock()

	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), healthWriteTimeout)
	defer cancel()
	var werr error
	if kind == "" {
		werr = h.rec.RecordContactSuccess(wctx, revision)
	} else {
		werr = h.rec.RecordContactFailure(wctx, revision, kind)
	}
	if werr != nil {
		h.logger.Warn("rubrik: recording connection health failed", "kind", kind, "error", werr)
	}
}
