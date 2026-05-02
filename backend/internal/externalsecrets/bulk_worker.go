package externalsecrets

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/store"
)

// interCallDelay is the throttle between per-target patches inside a single
// bulk job. The k8s API server has plenty of headroom (platform QPS=50,
// burst=100), but the ESO controller's reconcile queue is bounded by its own
// --concurrent flag (typically 5-10). 200ms gives the controller breathing
// room without making a 100-target job take more than ~30s wall-clock.
const interCallDelay = 200 * time.Millisecond

// queueDepth is the buffered channel size for pending bulk-refresh jobs.
// Beyond this depth, Enqueue returns an error and the dialog sees 503. With
// a single-worker model and ~30s per typical job, 32 slots covers ~16
// minutes of backlog before pushback — enough for an admin to fan out
// scope-by-scope without hitting the cap.
const queueDepth = 32

// BulkJobReadWriter is the subset of *store.ESOBulkJobStore the handler
// needs. Interface lives here (the consumer) per Go's accepted-interface
// pattern, so tests can swap in a fake without depending on pgxpool.
type BulkJobReadWriter interface {
	Insert(ctx context.Context, j store.ESOBulkRefreshJob) error
	Get(ctx context.Context, id uuid.UUID) (*store.ESOBulkRefreshJob, error)
	FindActive(ctx context.Context, clusterID string, action store.BulkRefreshAction, scopeTarget string) (*store.ESOBulkRefreshJob, error)
	AppendOutcome(ctx context.Context, id uuid.UUID, succeededUID string, failed *store.BulkRefreshOutcome, skipped *store.BulkRefreshOutcome) error
	AppendOutcomes(ctx context.Context, id uuid.UUID, batch store.BulkOutcomeBatch) error
	Complete(ctx context.Context, id uuid.UUID) error
	CompleteOrphans(ctx context.Context) (int64, error)
}

// outcomeBatchSize controls flush cadence for the per-job outcome batcher.
// 50 targets per UPDATE keeps per-job round-trips at O(N/50) (≤100 UPDATEs
// for the 5000-target cap) while preserving real-time progress visibility
// (interCallDelay × 50 ≈ 10s, well under the dialog's 2s polling cadence).
// See todo #342.
const outcomeBatchSize = 50

// BulkWorkerEnqueuer is the subset of *BulkWorker the handler needs.
type BulkWorkerEnqueuer interface {
	Enqueue(msg BulkJobMessage) error
}

// BulkJobMessage is what the HTTP path sends to the worker. The user identity
// is captured at request time so the worker can impersonate later (the worker
// runs in the background, after the request returns).
type BulkJobMessage struct {
	JobID      uuid.UUID
	ClusterID  string
	Action     store.BulkRefreshAction
	ScopeTarget   string
	Targets    []BulkScopeTarget
	Username   string
	Groups     []string
	ActorName  string // user.Username for audit/log
	SourceIP   string
	EnqueuedAt time.Time
}

// BulkWorker processes bulk-refresh jobs serially out of a buffered channel.
// Single-worker design: per-target throttle (interCallDelay) is the dominant
// cost, so concurrency would exceed the ESO controller's reconcile queue
// without speeding up the average user-perceived completion.
type BulkWorker struct {
	jobs    chan BulkJobMessage
	store   BulkJobReadWriter
	k8s     *k8s.ClientFactory
	handler *Handler // for InvalidateCache + auditBulkJob
	logger  *slog.Logger
	// dynForUser is the impersonation factory. Tests inject a closure that
	// returns a fake dynamic.Interface so the real processJob path can run
	// without a live ClientFactory. See todo #352.
	dynForUser func(username string, groups []string) (dynamic.Interface, error)
}

// NewBulkWorker returns a worker bound to the given store + k8s factory.
// Caller must call Start to spawn the goroutine.
func NewBulkWorker(
	jobStore BulkJobReadWriter, k8sClient *k8s.ClientFactory, handler *Handler, logger *slog.Logger,
) *BulkWorker {
	if logger == nil {
		logger = slog.Default()
	}
	w := &BulkWorker{
		jobs:    make(chan BulkJobMessage, queueDepth),
		store:   jobStore,
		k8s:     k8sClient,
		handler: handler,
		logger:  logger,
	}
	w.dynForUser = func(username string, groups []string) (dynamic.Interface, error) {
		if k8sClient == nil {
			return nil, errors.New("no k8s client configured")
		}
		return k8sClient.DynamicClientForUser(username, groups)
	}
	return w
}

// Enqueue places a job on the worker's channel without blocking. Returns an
// error when the queue is at depth — the handler converts this to 503.
func (w *BulkWorker) Enqueue(msg BulkJobMessage) error {
	select {
	case w.jobs <- msg:
		return nil
	default:
		return errors.New("bulk-refresh queue full")
	}
}

// Start spawns the worker goroutine. Returns immediately. On ctx cancel, the
// goroutine returns; any in-flight job's row is left as-is (completed_at
// nil), and the next platform startup's CompleteOrphans pass closes it.
func (w *BulkWorker) Start(ctx context.Context) {
	go w.run(ctx)
}

// dbWriteTimeout bounds each per-target DB write so a hung pgxpool can't
// pin the worker indefinitely. See todo #345.
const dbWriteTimeout = 5 * time.Second

// dbWriteCtx returns a context that ignores parent cancellation but carries
// a bounded timeout. A successful patch must always be recorded — losing the
// outcome row when the parent ctx cancels would silently desync audit from
// reality. See todo #345.
func dbWriteCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), dbWriteTimeout)
}

// run is the worker loop. defer recover() catches dispatch panics so a
// transient driver fault doesn't kill the goroutine — the loop continues with
// the next job. On panic, the row is force-completed with a synthetic
// `worker_panic` outcome so the dialog's polling loop terminates and
// FindActive does not block subsequent same-scope POSTs. See todo #344.
func (w *BulkWorker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("eso bulk worker stopping", "reason", ctx.Err())
			return
		case msg := <-w.jobs:
			func() {
				defer func() {
					if r := recover(); r != nil {
						w.logger.Error("eso bulk worker panic recovered",
							"jobId", msg.JobID, "panic", r)
						cleanCtx, cancel := dbWriteCtx(ctx)
						defer cancel()
						if err := w.store.AppendOutcome(cleanCtx, msg.JobID, "",
							&store.BulkRefreshOutcome{Reason: "worker_panic"}, nil); err != nil {
							w.logger.Warn("eso bulk refresh: append panic outcome failed",
								"jobId", msg.JobID, "error", err)
						}
						if err := w.store.Complete(cleanCtx, msg.JobID); err != nil {
							w.logger.Warn("eso bulk refresh: complete after panic failed",
								"jobId", msg.JobID, "error", err)
						}
					}
				}()
				w.processJob(ctx, msg)
			}()
		}
	}
}

// completeWithLog calls Complete on a non-cancellable ctx and logs failures
// instead of swallowing them. Used by early-exit paths (cluster guard,
// impersonating-client failure).
func (w *BulkWorker) completeWithLog(parent context.Context, jobID uuid.UUID) {
	ctx, cancel := dbWriteCtx(parent)
	defer cancel()
	if err := w.store.Complete(ctx, jobID); err != nil {
		w.logger.Warn("eso bulk refresh: complete failed",
			"jobId", jobID, "error", err)
	}
}

// processJob executes one bulk job. Each target gets a force-sync patch
// reusing patchForceSync (single-resource semantics), with results appended
// to the job row as they complete. interCallDelay throttles between
// patches.
func (w *BulkWorker) processJob(ctx context.Context, msg BulkJobMessage) {
	w.logger.Info("eso bulk refresh: starting",
		"jobId", msg.JobID, "action", msg.Action, "scope", msg.ScopeTarget,
		"targetCount", len(msg.Targets))

	// Defense in depth: if a job row was inserted before the handler-side
	// guard landed (or via direct DB write), refuse to patch a non-local
	// cluster from this local-bound dynamic client. See todo #339.
	if msg.ClusterID != "" && msg.ClusterID != "local" {
		w.logger.Error("eso bulk refresh: refusing non-local cluster job",
			"jobId", msg.JobID, "clusterID", msg.ClusterID)
		w.completeWithLog(ctx, msg.JobID)
		return
	}

	dynClient, err := w.dynForUser(msg.Username, msg.Groups)
	if err != nil {
		w.logger.Error("eso bulk refresh: impersonating client",
			"jobId", msg.JobID, "error", err)
		w.completeWithLog(ctx, msg.JobID)
		return
	}

	var batch store.BulkOutcomeBatch
	flush := func() {
		if batch.Empty() {
			return
		}
		dbCtx, cancel := dbWriteCtx(ctx)
		defer cancel()
		if err := w.store.AppendOutcomes(dbCtx, msg.JobID, batch); err != nil {
			w.logger.Warn("eso bulk refresh: append outcomes failed",
				"jobId", msg.JobID, "succeeded", len(batch.Succeeded),
				"failed", len(batch.Failed), "skipped", len(batch.Skipped),
				"error", err)
		}
		batch = store.BulkOutcomeBatch{}
	}

	for i, target := range msg.Targets {
		select {
		case <-ctx.Done():
			w.logger.Info("eso bulk refresh: ctx cancelled mid-job",
				"jobId", msg.JobID, "remaining", len(msg.Targets)-i)
			// Flush any pending outcomes from completed patches before
			// bailing out — orphan reaper closes the row on next boot.
			flush()
			return
		default:
		}

		_, patchErr := w.handler.patchForceSyncPinned(ctx, dynClient, target.Namespace, target.Name, target.UID)
		switch {
		case errors.Is(patchErr, errAlreadyRefreshing):
			batch.Skipped = append(batch.Skipped, store.BulkRefreshOutcome{UID: target.UID, Reason: "already_refreshing"})
		case errors.Is(patchErr, errUIDDrifted):
			batch.Failed = append(batch.Failed, store.BulkRefreshOutcome{UID: target.UID, Reason: "uid_drifted"})
		case apierrors.IsForbidden(patchErr):
			batch.Failed = append(batch.Failed, store.BulkRefreshOutcome{UID: target.UID, Reason: "rbac_denied"})
		case apierrors.IsNotFound(patchErr):
			batch.Failed = append(batch.Failed, store.BulkRefreshOutcome{UID: target.UID, Reason: "not_found"})
		case apierrors.IsConflict(patchErr):
			batch.Failed = append(batch.Failed, store.BulkRefreshOutcome{UID: target.UID, Reason: "optimistic_lock"})
		case patchErr != nil:
			w.logger.Warn("eso bulk refresh: patch failed",
				"jobId", msg.JobID, "namespace", target.Namespace, "name", target.Name,
				"error", patchErr)
			batch.Failed = append(batch.Failed, store.BulkRefreshOutcome{UID: target.UID, Reason: classifyPatchError(patchErr)})
		default:
			batch.Succeeded = append(batch.Succeeded, target.UID)
		}

		// Flush every outcomeBatchSize targets so the dialog's 2s poll
		// cadence still surfaces progress.
		if len(batch.Succeeded)+len(batch.Failed)+len(batch.Skipped) >= outcomeBatchSize {
			flush()
		}

		if i < len(msg.Targets)-1 {
			select {
			case <-ctx.Done():
				flush()
				return
			case <-time.After(interCallDelay):
			}
		}
	}
	flush()

	completeCtx, cancelComplete := dbWriteCtx(ctx)
	if err := w.store.Complete(completeCtx, msg.JobID); err != nil {
		w.logger.Warn("eso bulk refresh: complete failed",
			"jobId", msg.JobID, "error", err)
	}
	cancelComplete()

	// Re-fetch the row to pick up the final state (succeeded/failed/skipped
	// arrays were updated incrementally) for the audit row + cache
	// invalidation.
	getCtx, cancelGet := dbWriteCtx(ctx)
	final, err := w.store.Get(getCtx, msg.JobID)
	cancelGet()
	if err != nil {
		w.logger.Warn("eso bulk refresh: get final state for audit",
			"jobId", msg.JobID, "error", err)
	} else if w.handler != nil {
		w.handler.auditBulkJob(ctx, msg, final)
	}
	if w.handler != nil {
		w.handler.InvalidateCache()
	}

	w.logger.Info("eso bulk refresh: completed",
		"jobId", msg.JobID, "scope", msg.ScopeTarget)
}

// classifyPatchError maps errors not covered by the apierrors helpers to a
// short reason string for the failed[] entry. Generic enough to render in
// the audit-log viewer without leaking API server details.
//
// Transient classes (timeouts, throttling, service unavailable) are prefixed
// `transient_` so retry tooling (#343) can branch on them without re-parsing
// the underlying error.
func classifyPatchError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return "transient_timeout"
	case apierrors.IsTooManyRequests(err):
		return "transient_throttled"
	case apierrors.IsServiceUnavailable(err):
		return "transient_unavailable"
	}
	reason := apierrors.ReasonForError(err)
	if reason == metav1.StatusReasonUnknown {
		return "patch_error"
	}
	return "patch_error:" + strings.ToLower(string(reason))
}
