package externalsecrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

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
	Complete(ctx context.Context, id uuid.UUID) error
	CompleteOrphans(ctx context.Context) (int64, error)
}

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
	ScopeTgt   string
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
}

// NewBulkWorker returns a worker bound to the given store + k8s factory.
// Caller must call Start to spawn the goroutine.
func NewBulkWorker(
	jobStore BulkJobReadWriter, k8sClient *k8s.ClientFactory, handler *Handler, logger *slog.Logger,
) *BulkWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &BulkWorker{
		jobs:    make(chan BulkJobMessage, queueDepth),
		store:   jobStore,
		k8s:     k8sClient,
		handler: handler,
		logger:  logger,
	}
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

// run is the worker loop. defer recover() catches dispatch panics so a
// transient driver fault doesn't kill the goroutine — the loop continues with
// the next job.
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
					}
				}()
				w.processJob(ctx, msg)
			}()
		}
	}
}

// processJob executes one bulk job. Each target gets a force-sync patch
// reusing patchForceSync (single-resource semantics), with results appended
// to the job row as they complete. interCallDelay throttles between
// patches.
func (w *BulkWorker) processJob(ctx context.Context, msg BulkJobMessage) {
	w.logger.Info("eso bulk refresh: starting",
		"jobId", msg.JobID, "action", msg.Action, "scope", msg.ScopeTgt,
		"targetCount", len(msg.Targets))

	dynClient, err := w.k8s.DynamicClientForUser(msg.Username, msg.Groups)
	if err != nil {
		w.logger.Error("eso bulk refresh: impersonating client",
			"jobId", msg.JobID, "error", err)
		_ = w.store.Complete(ctx, msg.JobID)
		return
	}

	for i, target := range msg.Targets {
		select {
		case <-ctx.Done():
			w.logger.Info("eso bulk refresh: ctx cancelled mid-job",
				"jobId", msg.JobID, "remaining", len(msg.Targets)-i)
			// Don't Complete — leave it as IN-PROGRESS so startup orphans
			// reaper closes it on next boot.
			return
		default:
		}

		uid, err := w.handler.patchForceSync(ctx, dynClient, target.Namespace, target.Name)
		switch {
		case errors.Is(err, errAlreadyRefreshing):
			_ = w.store.AppendOutcome(ctx, msg.JobID, "", nil, &store.BulkRefreshOutcome{
				UID: target.UID, Reason: "already_refreshing",
			})
		case apierrors.IsForbidden(err):
			_ = w.store.AppendOutcome(ctx, msg.JobID, "", &store.BulkRefreshOutcome{
				UID: target.UID, Reason: "rbac_denied",
			}, nil)
		case apierrors.IsNotFound(err):
			_ = w.store.AppendOutcome(ctx, msg.JobID, "", &store.BulkRefreshOutcome{
				UID: target.UID, Reason: "not_found",
			}, nil)
		case apierrors.IsConflict(err):
			_ = w.store.AppendOutcome(ctx, msg.JobID, "", &store.BulkRefreshOutcome{
				UID: target.UID, Reason: "optimistic_lock",
			}, nil)
		case err != nil:
			w.logger.Warn("eso bulk refresh: patch failed",
				"jobId", msg.JobID, "namespace", target.Namespace, "name", target.Name,
				"error", err)
			_ = w.store.AppendOutcome(ctx, msg.JobID, "", &store.BulkRefreshOutcome{
				UID: target.UID, Reason: classifyPatchError(err),
			}, nil)
		default:
			_ = w.store.AppendOutcome(ctx, msg.JobID, target.UID, nil, nil)
			_ = uid // patchForceSync returns the live UID; we already pinned it at scope-resolve
		}

		if i < len(msg.Targets)-1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(interCallDelay):
			}
		}
	}

	_ = w.store.Complete(ctx, msg.JobID)

	// Re-fetch the row to pick up the final state (succeeded/failed/skipped
	// arrays were updated incrementally) for the audit row + cache
	// invalidation.
	final, err := w.store.Get(ctx, msg.JobID)
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
		"jobId", msg.JobID, "scope", msg.ScopeTgt)
}

// classifyPatchError maps errors not covered by the apierrors helpers to a
// short reason string for the failed[] entry. Generic enough to render in
// the audit-log viewer without leaking API server details.
func classifyPatchError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("patch_error:%s", apierrorReason(err))
}

func apierrorReason(err error) string {
	se, ok := err.(interface{ Status() any })
	if !ok {
		return "unknown"
	}
	_ = se
	return "unknown"
}
