package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	appstore "github.com/kubecenter/kubecenter/internal/store"
)

// receiptSweepCallTimeout bounds the reconcile call, including the boot
// reconcile that runs before the HTTP listener, which must finish well inside
// the backend startupProbe budget (about a minute). Cleanup is deliberately not
// given this bound: it runs only in the background, and a large retention
// DELETE on a big table legitimately needs the store's own five-minute bound.
const receiptSweepCallTimeout = 30 * time.Second

// receiptSweepStore is the two store methods the sweeper drives, so a test can
// substitute a fake. *appstore.ChangeReceiptStore satisfies it.
type receiptSweepStore interface {
	ReconcileOrphans(ctx context.Context, olderThan time.Duration) (int64, error)
	Cleanup(ctx context.Context, retentionDays int) (int64, error)
}

// receiptSweeper closes tracked applies whose process died (ReconcileOrphans,
// which never replays anything) and prunes receipts past retention (Cleanup).
//
// Startup runs only reconcileOnBoot, which is one bounded call, so the listener
// is never delayed by retention work. The first retention pass and every later
// pass run in run's background loop.
type receiptSweeper struct {
	store         receiptSweepStore
	retentionDays int
	grace         time.Duration
	period        time.Duration
	callTimeout   time.Duration
	logger        *slog.Logger
}

// newReceiptSweeper builds a sweeper with the production grace, period and
// call bound. A retentionDays below 1 is not usable (the store refuses it and
// would never prune), so it is replaced with the default and the operator is
// told, rather than silently disabling retention.
func newReceiptSweeper(store receiptSweepStore, retentionDays int, logger *slog.Logger) *receiptSweeper {
	if retentionDays < 1 {
		logger.Warn("changes: receipt retention days must be at least 1; using the default",
			"configured", retentionDays, "default", config.DefaultChangesReceiptRetentionDays)
		retentionDays = config.DefaultChangesReceiptRetentionDays
	}
	return &receiptSweeper{
		store:         store,
		retentionDays: retentionDays,
		grace:         appstore.ReceiptOrphanGrace,
		// The period equals the orphan grace, so a row too young to reap on one
		// pass is reaped on the next.
		period:      appstore.ReceiptOrphanGrace,
		callTimeout: receiptSweepCallTimeout,
		logger:      logger,
	}
}

// reconcile closes orphaned 'applying' rows under the per-call bound. Only rows
// older than the grace are touched, so it is safe while an old pod is still
// finishing a live apply during a rolling update.
func (w *receiptSweeper) reconcile(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, w.callTimeout)
	defer cancel()
	if n, err := w.store.ReconcileOrphans(cctx, w.grace); err != nil {
		w.logger.Warn("changes: receipt reconcile failed", "error", err)
	} else if n > 0 {
		w.logger.Info("changes: reconciled interrupted receipts", "count", n)
	}
}

// cleanup prunes receipts past retention. Background only; see receiptSweepCallTimeout.
func (w *receiptSweeper) cleanup(ctx context.Context) {
	// No caller-side timeout: Cleanup bounds itself (cleanupTimeout), and a
	// shorter cut here would abort a large DELETE every pass and never prune.
	if n, err := w.store.Cleanup(ctx, w.retentionDays); err != nil {
		w.logger.Warn("changes: receipt retention sweep failed", "error", err)
	} else if n > 0 {
		w.logger.Info("changes: receipts pruned", "count", n, "retentionDays", w.retentionDays)
	}
}

// sweep is one full pass. A failed reconcile does not skip the cleanup.
func (w *receiptSweeper) sweep(ctx context.Context) {
	w.reconcile(ctx)
	w.cleanup(ctx)
}

// reconcileOnBoot is the only receipt work on the startup path: one reconcile
// bounded by callTimeout, panic-recovered. Synchronous so interrupted rows from
// the prior run are closed before the first request.
func (w *receiptSweeper) reconcileOnBoot(ctx context.Context) {
	recoverutil.Tick(ctx, w.logger, "changes receipt reconcile (boot)", w.reconcile)
}

// run is the background loop: a first retention pass straight away (reconcile
// already ran at boot), then a full sweep every period until ctx ends. It runs
// outside chi's recovery middleware, so each pass goes through recoverutil.Tick;
// close(done) is outside the wrapped closure so shutdown coordination always
// completes.
func (w *receiptSweeper) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	recoverutil.Tick(ctx, w.logger, "changes receipt retention (first)", w.cleanup)

	ticker := time.NewTicker(w.period)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recoverutil.Tick(ctx, w.logger, "changes receipt sweep", w.sweep)
		}
	}
}
