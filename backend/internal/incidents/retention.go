package incidents

// retention.go — incident retention sweep and startup configuration
// resolution (Release D, U25a; Q1 P13/P14).

import (
	"context"
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
	// lowered-retention warning. The sweep itself is bounded by the store.
	retentionCheckTimeout = 10 * time.Second
	// retentionTask labels every recovered panic from the sweep
	// (task=incidents-retention).
	retentionTask = "incidents-retention"
)

// Operator-facing bounds for the values the store does not itself ceiling.
const (
	minTimeout          = time.Second
	maxCaptureTimeout   = 5 * time.Minute
	maxSourceTimeout    = 2 * time.Minute
	maxConcurrencyBound = 16
)

// retentionStore is the slice of the incident store the sweep drives.
// *store.IncidentStore satisfies it.
type retentionStore interface {
	Cleanup(ctx context.Context, retentionDays int) (int64, error)
	MaxRetentionDaysAtCapture(ctx context.Context) (int, error)
}

// Retainer deletes incidents older than the configured retention: one sweep
// at start, then one per hour.
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
	store         retentionStore
	retentionDays int
	logger        *slog.Logger
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

// RunLoop warns if the retention would retroactively delete existing
// incidents, sweeps immediately, then sweeps hourly until ctx is cancelled.
func (r *Retainer) RunLoop(ctx context.Context) {
	if r == nil || ctx.Err() != nil {
		return
	}
	recoverutil.Tick(ctx, r.logger, retentionTask, r.warnIfLowered)
	r.sweepOnce(ctx)

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
	if err != nil {
		if ctx.Err() != nil {
			return // shutdown interrupted the sweep; not a failure
		}
		r.logger.Error("incident retention sweep failed", "retentionDays", r.retentionDays, "error", err)
		return
	}
	if deleted > 0 {
		r.logger.Info("incident retention sweep deleted expired incidents",
			"deleted", deleted, "retentionDays", r.retentionDays)
	}
}

// warnIfLowered logs a Warn when an existing incident was captured under a
// longer retention than the one now configured: Cleanup applies the current
// value to every row, so lowering it deletes those incidents on the first
// sweep (Q1 R-4). A failed lookup is not fatal.
func (r *Retainer) warnIfLowered(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, retentionCheckTimeout)
	defer cancel()
	longest, err := r.store.MaxRetentionDaysAtCapture(cctx)
	if err != nil {
		r.logger.Warn("incident retention: could not check the longest retention in use", "error", err)
		return
	}
	if longest > r.retentionDays {
		r.logger.Warn("configured incident retention is shorter than some existing incidents were captured under; "+
			"the next sweep deletes incidents older than the configured value",
			"retentionDays", r.retentionDays, "maxRetentionDaysAtCapture", longest)
	}
}

// Settings is the incident configuration after clamping.
type Settings struct {
	RetentionDays int
	Limits        Limits
}

// ResolveSettings turns the raw configuration into effective settings. Each
// value is brought into range and every correction is logged (field,
// configured, effective), never applied silently. A value at or below zero is
// treated as unset and replaced by its default, because clamping it to the
// minimum would turn a typo into "delete after one day" or "capture nothing".
// A value above its ceiling is clamped to the ceiling, and the evidence limits
// never exceed the store's SQL CHECK ceilings.
func ResolveSettings(c config.IncidentsConfig, logger *slog.Logger) Settings {
	if logger == nil {
		logger = slog.Default()
	}
	return Settings{
		RetentionDays: clampInt(logger, "retentionDays", c.RetentionDays,
			config.DefaultIncidentsRetentionDays, store.IncidentMinRetentionDays, store.IncidentMaxRetentionDays),
		Limits: Limits{
			MaxItemBytes: clampInt(logger, "maxItemBytes", c.MaxItemBytes,
				config.DefaultIncidentsMaxItemBytes, MinMaxBytes, store.EvidenceMaxItemBytesCeiling),
			MaxIncidentBytes: clampInt(logger, "maxIncidentBytes", c.MaxIncidentBytes,
				config.DefaultIncidentsMaxIncidentBytes, 1, store.EvidenceMaxIncidentBytesCeiling),
			MaxItems: clampInt(logger, "maxItems", c.MaxItems,
				config.DefaultIncidentsMaxItems, 1, store.EvidenceMaxItemsCeiling),
			MaxScopes: clampInt(logger, "maxScopes", c.MaxScopes,
				config.DefaultIncidentsMaxScopes, 1, store.EvidenceMaxScopesCeiling),
			CaptureTimeout: clampDuration(logger, "captureTimeout", c.CaptureTimeout,
				config.DefaultIncidentsCaptureTimeout, minTimeout, maxCaptureTimeout),
			SourceTimeout: clampDuration(logger, "sourceTimeout", c.SourceTimeout,
				config.DefaultIncidentsSourceTimeout, minTimeout, maxSourceTimeout),
			MaxConcurrency: clampInt(logger, "maxConcurrency", c.MaxConcurrency,
				config.DefaultIncidentsMaxConcurrency, 1, maxConcurrencyBound),
		},
	}
}

func clampInt(logger *slog.Logger, field string, v, def, lo, hi int) int {
	got := v
	switch {
	case v <= 0:
		got = def
	case v < lo:
		got = lo
	case v > hi:
		got = hi
	}
	if got != v {
		logger.Warn("incidents configuration out of range; using the effective value",
			"field", field, "configured", v, "effective", got, "min", lo, "max", hi)
	}
	return got
}

func clampDuration(logger *slog.Logger, field string, v, def, lo, hi time.Duration) time.Duration {
	got := v
	switch {
	case v <= 0:
		got = def
	case v < lo:
		got = lo
	case v > hi:
		got = hi
	}
	if got != v {
		logger.Warn("incidents configuration out of range; using the effective value",
			"field", field, "configured", v.String(), "effective", got.String(), "min", lo.String(), "max", hi.String())
	}
	return got
}
