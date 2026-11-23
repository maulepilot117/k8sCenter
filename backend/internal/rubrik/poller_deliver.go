package rubrik

// poller_deliver.go — what the poller does for a cluster after its tick
// (plan U10, KTD7, KTD10, KTD11, R24, R26): it sends the recorded events'
// delivery intents to Notification Center, and it sweeps the cluster's
// write operations. Both run under the cluster's lease, in tick() after the
// cluster tick, so a tick that returned early (another replica's lease, an
// unmapped cluster, a refused commit) does not skip them, and each gets a
// deadline of its own.
//
// Delivery. A claimed intent is emitted with a fixed title and message per
// event kind; no title or message carries a namespace, an object name or
// tenant text. The namespace goes in the row's resource namespace, which
// the feed filters on (KTD11) and SuppressResourceFields keeps out of Slack,
// webhook, email and push payloads (R26). ClusterID is the event's cluster,
// so the feed keeps a remote cluster's namespace rows from a same-named
// local namespace's readers. ResourceUID is the event id: sending is at
// least once (store/rubrik_poller.go), and the event id makes a repeat
// inside Notification Center's dedup window collapse into one row while a
// new transition, however soon, is its own row.
//
// Sweep. An operation stuck in applying past a grace period becomes
// unknown: its request died between recording the intent and recording the
// outcome, and only RSC can say what happened. Running and unknown
// operations are refreshed from RSC through the handler's own refresh,
// which follows the job and never resends a mutation, instead of being
// expired by age: a restore can run for hours. Refreshing them is also what
// releases the reads' in-flight flags.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	// pollerAfterTimeout bounds one cluster's delivery and sweep.
	pollerAfterTimeout = 90 * time.Second
	// pollerDeliveryBatch bounds the serial sends per cluster per tick. The
	// steady state is zero; a bad day's backlog drains over a few ticks.
	pollerDeliveryBatch = 50
	// pollerMaxDeliveryAttempts is how many ticks an intent is tried before
	// it is parked as failed. At one attempt per tick the last retry lands
	// ten minutes after the first, inside Notification Center's 15-minute
	// dedup window, so a send whose mark did not commit is suppressed on
	// retry rather than shown twice.
	pollerMaxDeliveryAttempts = 3
	// pollerSweepBatch bounds the operations refreshed from RSC per cluster
	// per tick, oldest first.
	pollerSweepBatch = 20
	// pollerApplyingGrace is how long an operation may sit in applying
	// before the sweep calls it unknown. It is one poll interval, well past
	// the write path's own bound and store.RubrikApplyingMinGrace.
	pollerApplyingGrace = PollInterval
	// pollerResourceKind is the ResourceKind on every Rubrik notification:
	// one fixed string, so no condition or object travels as identity
	// metadata to external channels.
	pollerResourceKind = "RubrikProtection"
	// pollerUnsentReason is what a claimed intent records when the drain
	// stopped before sending it.
	pollerUnsentReason = "not sent: delivery stopped before this notification"
)

// Notifier is the one notification operation the drain needs: the emit that
// reports what happened, so an intent is marked by its outcome.
// *notifications.NotificationService implements it.
type Notifier interface {
	EmitSync(ctx context.Context, n notifications.Notification) (notifications.EmitResult, error)
}

// DeliveryStore is the part of store.RubrikStore the drain uses.
type DeliveryStore interface {
	ClaimPollerDeliveries(ctx context.Context, clusterID string, maxAttempts, limit int) ([]store.RubrikPollerDeliveryJob, error)
	MarkPollerDelivered(ctx context.Context, id string) error
	MarkPollerDeliveryFailed(ctx context.Context, id, errMsg string, maxAttempts int) error
}

// NamespaceGetter reads a local namespace; the informer's namespace lister
// implements it.
type NamespaceGetter interface {
	Get(name string) (*corev1.Namespace, error)
}

// SweepStore is the part of store.RubrikStore the operation sweep uses.
type SweepStore interface {
	ExpireApplyingOperations(ctx context.Context, clusterID string, grace time.Duration) (int64, error)
	OpenOperations(ctx context.Context, clusterID string) ([]store.RubrikOperation, error)
}

// operationRefresher follows an operation's job in RSC and records a change;
// *Handler implements it with the refresh the operation reads use.
type operationRefresher interface {
	refreshOperation(ctx context.Context, op store.RubrikOperation) (store.RubrikOperation, string)
}

var (
	_ Notifier           = (*notifications.NotificationService)(nil)
	_ DeliveryStore      = (*store.RubrikStore)(nil)
	_ SweepStore         = (*store.RubrikStore)(nil)
	_ operationRefresher = (*Handler)(nil)
)

// WithDelivery makes the poller send its events' delivery intents through n,
// reading the local cluster's namespaces through namespaces. All three
// arguments must be non-nil; the poller sends nothing until this is called.
// It returns p.
func (p *Poller) WithDelivery(st DeliveryStore, n Notifier, namespaces NamespaceGetter) *Poller {
	p.deliveries, p.notifier, p.namespaces = st, n, namespaces
	return p
}

// WithSweep makes the poller sweep each cluster's write operations, refreshing
// running and unknown ones through h. Both arguments must be non-nil. It
// returns p.
func (p *Poller) WithSweep(st SweepStore, h *Handler) *Poller {
	p.sweepStore, p.refresher = st, h
	return p
}

// afterTick delivers the cluster's pending intents and sweeps its operations,
// if the poller was given them, while this replica holds the cluster's
// lease. Renewing the lease is how it learns that; another replica's lease
// means that replica does this work.
func (p *Poller) afterTick(ctx context.Context, clusterID string) {
	if p.notifier == nil && p.refresher == nil {
		return
	}
	if _, err := p.store.AcquireOrRenewPollerLease(ctx, clusterID, p.holder, pollerLeaseTTL); err != nil {
		if !errors.Is(err, store.ErrRubrikPollerLeaseHeld) {
			p.logger.Warn("rubrik poller: lease renewal before delivery failed", "cluster", clusterID, "error", err)
		}
		return
	}
	p.mu.Lock()
	p.held[clusterID] = true
	p.mu.Unlock()
	if p.notifier != nil {
		if err := p.drain(ctx, clusterID); err != nil {
			p.logger.Warn("rubrik poller: delivery stopped", "cluster", clusterID, "error", err)
		}
	}
	if p.refresher != nil {
		p.sweep(ctx, clusterID)
	}
}

// drain claims the cluster's pending intents and emits each one, marking it
// by the outcome.
//
// A claim spends an attempt on every claimed intent, and the store never
// claims an intent again once its attempts reach the limit. So every
// claimed intent is marked before drain returns: a mark runs on a commit
// context that outlives the drain's deadline, and when the drain stops
// early the intents it did not send are marked failed, which keeps them
// pending for a later claim or, on their last attempt, parks them as
// failed. Left unmarked, an intent on its last attempt would stay pending
// and unclaimable for good.
func (p *Poller) drain(ctx context.Context, clusterID string) error {
	// Claiming spends an attempt; do not spend one on a deadline that has
	// already passed.
	if err := ctx.Err(); err != nil {
		return err
	}
	jobs, err := p.deliveries.ClaimPollerDeliveries(ctx, clusterID, pollerMaxDeliveryAttempts, pollerDeliveryBatch)
	if err != nil {
		return err
	}
	for i, j := range jobs {
		if err := ctx.Err(); err != nil {
			p.releaseUnsent(ctx, jobs[i:])
			return err
		}
		res, emitErr := p.notifier.EmitSync(ctx, p.scoped(notificationFor(j.Event), j.Event))
		reason := ""
		switch {
		case emitErr != nil:
			p.logger.Warn("rubrik poller: notification emit failed",
				"delivery", j.Delivery.ID, "attempt", j.Delivery.Attempts, "error", emitErr)
			reason = deliveryFailure(emitErr)
		case !res.Delivered():
			// EmitSync never reports a failure without an error; count a
			// breach as a failed attempt, never as a delivery.
			reason = "notification not delivered: " + res.String()
		}
		markErr := p.mark(ctx, j.Delivery.ID, reason)
		if errors.Is(markErr, store.ErrRubrikDeliveryNotPending) {
			continue // another replica's drain finished it
		}
		if markErr != nil {
			p.releaseUnsent(ctx, jobs[i+1:])
			return markErr
		}
	}
	return nil
}

// scoped drops a notification's namespace when the local namespace of that
// name was created after the event was recorded, or cannot be read. The
// feed shows a Rubrik row to a namespace's readers only when the row is
// newer than the namespace (R6, KTD11), and the row is created when it is
// delivered, not when the event was recorded: without this, an event
// delivered late would reach the readers of a namespace recreated in
// between. Without the namespace the row is admin-only. A namespace that no
// longer exists keeps the row's namespace: no reader holds a grant in it,
// and the feed hides the row from a later namespace of that name.
func (p *Poller) scoped(n notifications.Notification, e store.RubrikPollerEvent) notifications.Notification {
	if n.ResourceNS == "" || !k8s.IsLocalClusterID(e.ClusterID) {
		return n
	}
	ns, err := p.namespaces.Get(n.ResourceNS)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		p.logger.Warn("rubrik poller: reading the event's namespace failed; delivering it to admins only", "event", e.ID, "error", err)
		n.ResourceNS = ""
	case ns.CreationTimestamp.After(e.CreatedAt):
		n.ResourceNS = ""
	}
	return n
}

// mark records one claimed intent's outcome: delivered when reason is
// empty, else a failed attempt with that reason. It commits on a context
// that outlives ctx's deadline, so an emit cut off by the deadline is still
// recorded.
func (p *Poller) mark(ctx context.Context, id, reason string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pollerCommitTimeout)
	defer cancel()
	if reason == "" {
		return p.deliveries.MarkPollerDelivered(ctx, id)
	}
	return p.deliveries.MarkPollerDeliveryFailed(ctx, id, reason, pollerMaxDeliveryAttempts)
}

// releaseUnsent marks the claimed intents a stopped drain did not send as
// failed attempts, under one commit timeout. It stops at the first store
// error other than an intent another replica already finished.
func (p *Poller) releaseUnsent(ctx context.Context, jobs []store.RubrikPollerDeliveryJob) {
	if len(jobs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pollerCommitTimeout)
	defer cancel()
	for _, j := range jobs {
		err := p.deliveries.MarkPollerDeliveryFailed(ctx, j.Delivery.ID, pollerUnsentReason, pollerMaxDeliveryAttempts)
		if err != nil && !errors.Is(err, store.ErrRubrikDeliveryNotPending) {
			p.logger.Warn("rubrik poller: releasing unsent deliveries failed", "delivery", j.Delivery.ID, "error", err)
			return
		}
	}
}

// deliveryFailure is what a failed intent records. The emit's error is
// logged, not stored: it can carry driver and host detail.
func deliveryFailure(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "notification emit cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "notification emit timed out"
	}
	return "notification service error (see server log)"
}

// sweep moves the cluster's operations stuck in applying to unknown, then
// refreshes its running and unknown operations from RSC, oldest first and
// at most pollerSweepBatch of them. A cluster whose mapping no longer
// resolves is not refreshed: RSC cannot answer for it.
func (p *Poller) sweep(ctx context.Context, clusterID string) {
	if n, err := p.sweepStore.ExpireApplyingOperations(ctx, clusterID, pollerApplyingGrace); err != nil {
		p.logger.Warn("rubrik poller: expiring stuck operations failed", "cluster", clusterID, "error", err)
	} else if n > 0 {
		p.logger.Info("rubrik poller: stuck operations marked unknown", "cluster", clusterID, "operations", n)
	}
	if _, err := p.svc.Resolve(ctx, clusterID); err != nil {
		return
	}
	ops, err := p.sweepStore.OpenOperations(ctx, clusterID)
	if err != nil && !errors.Is(err, store.ErrRubrikOperationsTruncated) {
		p.logger.Warn("rubrik poller: listing open operations failed", "cluster", clusterID, "error", err)
		return
	}
	var due []store.RubrikOperation
	for _, op := range ops {
		if op.State == store.RubrikOpRunning || op.State == store.RubrikOpUnknown {
			due = append(due, op)
		}
	}
	if len(due) == 0 {
		return
	}
	// Start after the operation the last sweep ended on and wrap around, so
	// operations that stay running for hours at the head of the list cannot
	// keep the ones behind them from ever being refreshed.
	p.mu.Lock()
	last, swept := p.sweptTo[clusterID]
	p.mu.Unlock()
	start := 0
	if swept {
		start = sweepStart(due, last)
	}
	for i := range min(len(due), pollerSweepBatch) {
		if ctx.Err() != nil {
			return
		}
		op := due[(start+i)%len(due)]
		p.refresher.refreshOperation(ctx, op)
		p.mu.Lock()
		p.sweptTo[clusterID] = op
		p.mu.Unlock()
	}
}

// sweepStart is the index in due, which is in the store's order (created_at,
// then id), of the first operation after last; 0 when none is.
func sweepStart(due []store.RubrikOperation, last store.RubrikOperation) int {
	if i := slices.IndexFunc(due, func(o store.RubrikOperation) bool { return o.ID == last.ID }); i >= 0 {
		return (i + 1) % len(due)
	}
	// The last one settled and left the list: start at the first one after
	// where it stood.
	if i := slices.IndexFunc(due, func(o store.RubrikOperation) bool {
		return o.CreatedAt.After(last.CreatedAt) || (o.CreatedAt.Equal(last.CreatedAt) && o.ID > last.ID)
	}); i >= 0 {
		return i
	}
	return 0
}

// pollerNotice is the fixed wording of one event kind.
type pollerNotice struct {
	severity notifications.Severity
	title    string
	message  string
}

// pollerNotices holds every event kind's wording. None names a namespace, an
// object or a tenant: the feed shows the namespace from the row, to the
// readers allowed to see it.
var pollerNotices = map[string]pollerNotice{
	store.RubrikEventSnapshotFailed: {notifications.SeverityWarning, "Rubrik snapshot failed",
		"A Rubrik snapshot job failed. Open the namespace's Rubrik page for the job history."},
	store.RubrikEventComplianceLost: {notifications.SeverityWarning, "Rubrik SLA compliance lost",
		"A Rubrik-protected object missed its SLA domain's snapshot schedule on two consecutive checks."},
	store.RubrikEventComplianceRestored: {notifications.SeverityInfo, "Rubrik SLA compliance restored",
		"A Rubrik-protected object is back within its SLA domain's snapshot schedule."},
	store.RubrikEventConnectionFailing: {notifications.SeverityCritical, "Rubrik connection failing",
		"k8sCenter could not read from Rubrik Security Cloud on three consecutive polls. Rubrik pages show the last data read."},
	store.RubrikEventConnectionRestored: {notifications.SeverityInfo, "Rubrik connection restored",
		"k8sCenter is reading from Rubrik Security Cloud again."},
	store.RubrikEventMonitoringGap: {notifications.SeverityWarning, "Rubrik monitoring gap",
		"Rubrik monitoring was interrupted for more than 24 hours. Check Rubrik Security Cloud for failures during the gap."},
}

// notificationFor builds the Notification Center entry for one event.
func notificationFor(e store.RubrikPollerEvent) notifications.Notification {
	notice, ok := pollerNotices[e.Kind]
	if !ok {
		notice = pollerNotice{notifications.SeverityWarning, "Rubrik event", "Rubrik reported a change. Open the Rubrik pages for details."}
	}
	message := notice.message
	if e.Kind == store.RubrikEventMonitoringGap {
		var gap gapDetail
		if err := json.Unmarshal(e.Detail, &gap); err == nil && !gap.GapStart.IsZero() && !gap.GapEnd.IsZero() {
			// R24: the gap's length, and the instant before which failures
			// were not raised one by one.
			until := "it resumed"
			if !e.OccurredAt.IsZero() && e.OccurredAt.After(gap.GapStart) {
				until = fmt.Sprintf("%s (about %d hours)", e.OccurredAt.UTC().Format(time.RFC3339),
					int(e.OccurredAt.Sub(gap.GapStart).Round(time.Hour).Hours()))
			}
			message = "Rubrik monitoring was down from " + gap.GapStart.UTC().Format(time.RFC3339) +
				" until " + until + "; failures before " + gap.GapEnd.UTC().Format(time.RFC3339) +
				" were not raised individually. Check Rubrik Security Cloud for failures before that time."
		}
	}
	return notifications.Notification{
		Source:                 notifications.SourceRubrik,
		Severity:               notice.severity,
		Title:                  notice.title,
		Message:                message,
		ResourceKind:           pollerResourceKind,
		ResourceNS:             e.Namespace,
		ResourceUID:            e.ID,
		ClusterID:              e.ClusterID,
		SuppressResourceFields: true,
	}
}
