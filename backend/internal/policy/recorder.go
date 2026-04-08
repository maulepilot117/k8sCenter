package policy

import (
	"context"
	"log/slog"
	"time"

	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	snapshotRetentionDays = 90
	snapshotHourUTC       = 0 // midnight UTC
)

// PolicyFetcher fetches raw policy and violation data for compliance snapshots.
// Implementations should use the service account for full cluster visibility.
type PolicyFetcher interface {
	FetchUnfiltered(ctx context.Context) ([]NormalizedPolicy, []NormalizedViolation, error)
}

// ComplianceRecorder takes daily compliance score snapshots and stores them.
type ComplianceRecorder struct {
	Store     *store.ComplianceStore
	Fetcher   PolicyFetcher
	ClusterID string
	Logger    *slog.Logger
}

// Run starts the snapshot loop: immediate snapshot on startup, then daily at midnight UTC.
func (r *ComplianceRecorder) Run(ctx context.Context) {
	r.takeSnapshot(ctx)

	for {
		timer := time.NewTimer(durationUntilNextUTC(snapshotHourUTC))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			r.takeSnapshot(ctx)
		}
	}
}

func (r *ComplianceRecorder) takeSnapshot(ctx context.Context) {
	policies, violations, err := r.Fetcher.FetchUnfiltered(ctx)
	if err != nil {
		r.Logger.Error("compliance snapshot: fetch failed", "error", err)
		return
	}

	score := computeCompliance(policies, violations, "")

	snap := &store.ComplianceSnapshot{
		Date:         time.Now().UTC().Truncate(24 * time.Hour),
		ClusterID:    r.ClusterID,
		OverallScore: score.Score,
		Pass:         score.Pass,
		Fail:         score.Fail,
		Warn:         score.Warn,
		Total:        score.Total,
	}

	if err := r.Store.Insert(ctx, snap); err != nil {
		r.Logger.Error("compliance snapshot: insert failed", "error", err)
		return
	}

	r.Logger.Info("compliance snapshot recorded",
		"cluster", r.ClusterID, "score", score.Score, "date", snap.Date.Format("2006-01-02"))

	deleted, err := r.Store.Cleanup(ctx, snapshotRetentionDays)
	if err != nil {
		r.Logger.Warn("compliance snapshot: cleanup failed", "error", err)
	} else if deleted > 0 {
		r.Logger.Info("compliance snapshot: cleaned up old snapshots", "deleted", deleted)
	}
}

// durationUntilNextUTC returns the time until the next occurrence of the given hour in UTC.
func durationUntilNextUTC(hour int) time.Duration {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return time.Until(next)
}
