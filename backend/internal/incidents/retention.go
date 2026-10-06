package incidents

// retention.go — incident retention sweep and startup configuration
// resolution (Release D, U25a; Q1 P13/P14).

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	// retentionSweepPeriod is the interval between sweeps after the first.
	retentionSweepPeriod = time.Hour
	// retentionCheckTimeout bounds the startup lookup that feeds the
	// lowered-retention check. The sweep itself is bounded by the store.
	retentionCheckTimeout = 10 * time.Second
	// retentionTask labels every recovered panic from the sweep
	// (task=incidents-retention).
	retentionTask = "incidents-retention"
	// loweringGraceWindow is how long the first sweep is deferred when the
	// configured retention would delete incidents captured under a longer one.
	// It equals retentionSweepPeriod: the deferred first sweep is simply the
	// first tick. That is the operator's window to roll the change back.
	loweringGraceWindow = retentionSweepPeriod
)

// Operator-facing bounds for the values the store does not itself ceiling.
const (
	minTimeout = time.Second
	// maxCaptureTimeout is what is left of captureRequestBudget (27 s, under
	// the frontend proxy's 30 s) after the collector grace, the minimum insert
	// work and the COMMIT bound: 27 - 0.25 - 7 - 5 = 14.75 s. See the budget
	// derivation in handler_capture.go.
	maxCaptureTimeout = captureRequestBudget - captureGrace - captureMinInsertWork - store.IncidentCommitTimeout
	// maxSourceTimeout cannot usefully exceed the capture deadline it runs inside.
	maxSourceTimeout    = maxCaptureTimeout
	maxConcurrencyBound = 16
)

// retentionStore is the slice of the incident store the sweep drives.
// *store.IncidentStore satisfies it.
type retentionStore interface {
	Cleanup(ctx context.Context, retentionDays int) (int64, error)
	RetentionLoweringImpact(ctx context.Context, retentionDays int) (store.RetentionLoweringImpact, error)
	AppliedRetentionDays(ctx context.Context) (days int, found bool, err error)
	RecordAppliedRetention(ctx context.Context, days int) error
}

// Retainer deletes incidents older than the configured retention: one sweep
// at start (or one hour after start, see below), then one per hour.
//
// Lowering the retention deletes incidents created under a longer one, and a
// config rollback cannot bring them back. The Retainer therefore tells a NEW
// lowering from steady state using the retention the last successful sweep
// applied, which it persists (incident_retention_state, migration 000025):
//
//   - New lowering: the configured days are below the persisted applied days,
//     or nothing is persisted yet (first run after upgrade) and some incident
//     was captured under a longer retention (past the configured window, or
//     still inside it). If incidents are past the window
//     (Count above zero), the Retainer logs a Warn with the counts and the
//     affected date range and defers the first sweep by loweringGraceWindow,
//     giving the operator one hour to restore the old value. The deferral is
//     keyed on the past-window count only. If only incidents still inside the
//     window were captured under a longer retention (laterAffectedIncidents),
//     it logs the Warn without deferring.
//   - Steady state: the configured days are at or above the persisted days.
//     There is no deferral and no Warn on restart; the laterAffectedIncidents
//     notice is logged at Info, since those incidents are aging out under a
//     lowering the operator already went through.
//
// The first successful sweep persists the configured days, so the deferral
// happens once per lowering, not on every restart. RetentionLoweringConfirmed
// (KUBECENTER_INCIDENTS_RETENTIONLOWERINGCONFIRMED) skips that one deferral and
// is only a one-shot acknowledgement: remove it after the first sweep. If the
// counts or the persisted value cannot be read, the first sweep is deferred
// too (fail safe). A process that restarts more often than hourly during an
// unconfirmed new lowering never reaches its first sweep; set the flag to
// proceed. A failure to persist is logged and never stops the loop; the next
// start then simply treats the lowering as new again.
//
// Multi-replica skew: every replica sweeps with its OWN configured value, and
// the persisted value is shared. A replica started with a lower value deletes
// for all of them, and restoring the config afterwards cannot restore what it
// deleted. Keep the value identical across replicas (docs:
// migrations/NOTES.txt, 000024 and 000025).
//
// Lifecycle contract: RunLoop is meant to be started with a plain `go`
// statement and owns no sync.WaitGroup and no counted channel. It returns when
// its context is cancelled (promptly, even mid-sweep, because the store honours
// the context), so shutdown never waits on it and nothing waits for it. A
// sweep that is interrupted by shutdown simply does not finish; the next
// process sweeps again. Every sweep, including the first, runs under
// recoverutil.Tick, so a panic is logged and the loop carries on instead of
// crashing the process (the loop runs outside chi's recovery middleware).
type Retainer struct {
	store             retentionStore
	retentionDays     int
	loweringConfirmed bool
	logger            *slog.Logger
	// newTicker is the test seam for the hourly clock.
	newTicker func(d time.Duration) (<-chan time.Time, func())
}

// NewRetainer returns the sweeper, or nil when there is no store: a nil
// Retainer's RunLoop returns immediately, so a deployment without a database
// starts no loop. retentionDays below 1 is replaced by the default (a zero or
// negative value must never mean "delete everything" or "keep forever") and
// one above the maximum is clamped, so the value can never reach Cleanup out
// of range. ResolveSettings has normally corrected it already and logged why.
func NewRetainer(st retentionStore, retentionDays int, logger *slog.Logger) *Retainer {
	if st == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if retentionDays < store.IncidentMinRetentionDays {
		retentionDays = config.DefaultIncidentsRetentionDays
	}
	return &Retainer{
		store:         st,
		retentionDays: store.ClampIncidentRetentionDays(retentionDays),
		logger:        logger,
		newTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
	}
}

// WithLoweringConfirmed records the operator's acknowledgement that lowering
// retention deletes existing incidents, so the first sweep is not deferred.
// It returns r for chaining and is safe on a nil Retainer.
func (r *Retainer) WithLoweringConfirmed(confirmed bool) *Retainer {
	if r != nil {
		r.loweringConfirmed = confirmed
	}
	return r
}

// RunLoop checks whether the configured retention would delete incidents
// captured under a longer one, sweeps immediately (or after the grace window
// when it would), then sweeps hourly until ctx is cancelled.
func (r *Retainer) RunLoop(ctx context.Context) {
	if r == nil || ctx.Err() != nil {
		return
	}
	// Fail safe: a panic in the check leaves deferFirst true.
	deferFirst := true
	recoverutil.Tick(ctx, r.logger, retentionTask, func(ctx context.Context) {
		deferFirst = r.shouldDeferFirstSweep(ctx)
	})
	if !deferFirst {
		r.sweepOnce(ctx)
	}

	tick, stop := r.newTicker(retentionSweepPeriod)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			if ctx.Err() != nil {
				return
			}
			r.sweepOnce(ctx)
		}
	}
}

func (r *Retainer) sweepOnce(ctx context.Context) {
	recoverutil.Tick(ctx, r.logger, retentionTask, r.sweep)
}

func (r *Retainer) sweep(ctx context.Context) {
	deleted, err := r.store.Cleanup(ctx, r.retentionDays)
	// Cleanup deletes in batches and returns what it removed even on failure.
	if deleted > 0 {
		r.logger.Info("incident retention sweep deleted expired incidents",
			"deleted", deleted, "retentionDays", r.retentionDays)
	}
	if err != nil {
		if ctx.Err() != nil {
			return // shutdown interrupted the sweep; not a failure
		}
		r.logger.Error("incident retention sweep failed", "retentionDays", r.retentionDays, "error", err)
		return
	}
	// Remember what this sweep applied, so a restart can tell steady state from
	// a new lowering. A failure here is logged and never stops the loop.
	if rerr := r.store.RecordAppliedRetention(ctx, r.retentionDays); rerr != nil && ctx.Err() == nil {
		r.logger.Warn("incident retention: could not record the applied retention; "+
			"the next start will treat a lowering as new again",
			"retentionDays", r.retentionDays, "error", rerr)
	}
}

// shouldDeferFirstSweep reports whether the first sweep must wait one grace
// window, logging why.
func (r *Retainer) shouldDeferFirstSweep(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, retentionCheckTimeout)
	defer cancel()
	impact, err := r.store.RetentionLoweringImpact(cctx, r.retentionDays)
	var applied int
	var found bool
	if err == nil {
		applied, found, err = r.store.AppliedRetentionDays(cctx)
	}
	if err != nil {
		if r.loweringConfirmed {
			r.logger.Warn("incident retention: could not check whether the configured retention deletes existing incidents; "+
				"deletion is confirmed by configuration and proceeds now",
				"retentionDays", r.retentionDays, "error", err)
			return false
		}
		r.logger.Warn("incident retention: could not check whether the configured retention deletes existing incidents; "+
			"deferring the first sweep",
			"retentionDays", r.retentionDays, "firstSweepDelay", loweringGraceWindow.String(), "error", err)
		return true
	}
	// A lowering is new when the configured days are below what the last sweep
	// applied, or, with nothing recorded yet (first run after upgrade), when
	// any incident was captured under a longer retention (past the window,
	// or still inside it).
	newLowering := (found && r.retentionDays < applied) || (!found && (impact.Count > 0 || impact.LaterCount > 0))
	if !newLowering {
		if impact.LaterCount > 0 {
			// Steady state: these incidents are aging out under a lowering that
			// already went through. Informational only.
			r.logger.Info("some live incidents were captured under a longer retention than the configured one; "+
				"they will be deleted earlier than they were captured to be",
				"retentionDays", r.retentionDays, "laterAffectedIncidents", impact.LaterCount)
		}
		return false
	}
	if impact.Count == 0 {
		if impact.LaterCount > 0 {
			// Nothing is deleted early yet, so there is nothing to defer; say
			// what will happen as these incidents age past the new window.
			r.logger.Warn("configured incident retention is shorter than the retention some live incidents were captured under; "+
				"they will be deleted earlier than they were captured to be",
				"retentionDays", r.retentionDays, "laterAffectedIncidents", impact.LaterCount)
		}
		return false
	}
	if r.loweringConfirmed {
		r.logger.Warn("configured incident retention deletes incidents captured under a longer retention; "+
			"deletion is confirmed by configuration and proceeds now",
			"retentionDays", r.retentionDays, "affectedIncidents", impact.Count, "laterAffectedIncidents", impact.LaterCount,
			"oldestCreatedAt", impact.Oldest.UTC().Format(time.RFC3339), "newestCreatedAt", impact.Newest.UTC().Format(time.RFC3339))
		return false
	}
	r.logger.Warn("configured incident retention is shorter than the retention these incidents were captured under; "+
		"the first sweep is deferred so the change can be rolled back, then deletes them. "+
		"Set KUBECENTER_INCIDENTS_RETENTIONLOWERINGCONFIRMED=true to delete immediately",
		"retentionDays", r.retentionDays, "affectedIncidents", impact.Count, "laterAffectedIncidents", impact.LaterCount,
		"oldestCreatedAt", impact.Oldest.UTC().Format(time.RFC3339), "newestCreatedAt", impact.Newest.UTC().Format(time.RFC3339),
		"firstSweepDelay", loweringGraceWindow.String())
	return true
}

// Settings is the incident configuration after clamping.
type Settings struct {
	RetentionDays int
	Limits        Limits
	// RetentionLoweringConfirmed is passed through unchanged.
	RetentionLoweringConfirmed bool
}

// ResolveSettings turns the raw configuration into effective settings. Each
// value is brought into range and every correction is logged (field,
// configured, effective), never applied silently. A value at or below zero is
// treated as unset and replaced by its default, because clamping it to the
// minimum would turn a typo into "delete after one day" or "capture nothing".
// A value above its ceiling is clamped to the ceiling, and the evidence limits
// never exceed the store's SQL CHECK ceilings.
//
// After clamping, two cross-field rules hold: MaxIncidentBytes is at least
// MaxItemBytes (otherwise one maximal item could never be stored), and
// SourceTimeout is at most CaptureTimeout (a source cannot outlive its
// capture). A violation is corrected and logged.
func ResolveSettings(c config.IncidentsConfig, logger *slog.Logger) Settings {
	if logger == nil {
		logger = slog.Default()
	}
	l := Limits{
		MaxItemBytes: clamp(logger, "maxItemBytes", c.MaxItemBytes,
			config.DefaultIncidentsMaxItemBytes, MinMaxBytes, store.EvidenceMaxItemBytesCeiling),
		MaxIncidentBytes: clamp(logger, "maxIncidentBytes", c.MaxIncidentBytes,
			config.DefaultIncidentsMaxIncidentBytes, 1, store.EvidenceMaxIncidentBytesCeiling),
		MaxItems: clamp(logger, "maxItems", c.MaxItems,
			config.DefaultIncidentsMaxItems, 1, store.EvidenceMaxItemsCeiling),
		MaxScopes: clamp(logger, "maxScopes", c.MaxScopes,
			config.DefaultIncidentsMaxScopes, 1, store.EvidenceMaxScopesCeiling),
		CaptureTimeout: clamp(logger, "captureTimeout", c.CaptureTimeout,
			config.DefaultIncidentsCaptureTimeout, minTimeout, maxCaptureTimeout),
		SourceTimeout: clamp(logger, "sourceTimeout", c.SourceTimeout,
			config.DefaultIncidentsSourceTimeout, minTimeout, maxSourceTimeout),
		MaxConcurrency: clamp(logger, "maxConcurrency", c.MaxConcurrency,
			config.DefaultIncidentsMaxConcurrency, 1, maxConcurrencyBound),
	}
	if l.MaxIncidentBytes < l.MaxItemBytes {
		logger.Warn("incidents configuration inconsistent; raising maxIncidentBytes to maxItemBytes",
			"field", "maxIncidentBytes", "configured", l.MaxIncidentBytes, "effective", l.MaxItemBytes)
		l.MaxIncidentBytes = l.MaxItemBytes
	}
	if l.SourceTimeout > l.CaptureTimeout {
		logger.Warn("incidents configuration inconsistent; capping sourceTimeout at captureTimeout",
			"field", "sourceTimeout", "configured", l.SourceTimeout.String(), "effective", l.CaptureTimeout.String())
		l.SourceTimeout = l.CaptureTimeout
	}
	return Settings{
		RetentionDays: clamp(logger, "retentionDays", c.RetentionDays,
			config.DefaultIncidentsRetentionDays, store.IncidentMinRetentionDays, store.IncidentMaxRetentionDays),
		Limits:                     l,
		RetentionLoweringConfirmed: c.RetentionLoweringConfirmed,
	}
}

// clamp returns v when it is within [lo, hi]; a value at or below zero becomes
// def, one below lo becomes lo and one above hi becomes hi. Every change is
// logged with the field name.
func clamp[T cmp.Ordered](logger *slog.Logger, field string, v, def, lo, hi T) T {
	var zero T
	got := v
	switch {
	case v <= zero:
		got = def
	case v < lo:
		got = lo
	case v > hi:
		got = hi
	}
	if got != v {
		logger.Warn("incidents configuration out of range; using the effective value",
			"field", field, "configured", logValue(v), "effective", logValue(got), "min", logValue(lo), "max", logValue(hi))
	}
	return got
}

// logValue renders v for a log record: durations (anything with String())
// read as "10m0s" rather than raw nanoseconds, everything else is unchanged.
func logValue[T any](v T) any {
	if s, ok := any(v).(fmt.Stringer); ok {
		return s.String()
	}
	return v
}
