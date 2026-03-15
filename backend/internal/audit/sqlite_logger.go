package audit

import (
	"context"
	"log/slog"
)

// SQLiteLogger implements the Logger interface with persistent SQLite storage.
// It dual-writes to both SQLite (for querying) and slog (for log aggregators).
type SQLiteLogger struct {
	store  *SQLiteStore
	slog   *SlogLogger
	logger *slog.Logger
}

// NewSQLiteLogger creates an audit logger that persists entries in SQLite
// and also writes to structured log output.
func NewSQLiteLogger(store *SQLiteStore, logger *slog.Logger) *SQLiteLogger {
	return &SQLiteLogger{
		store:  store,
		slog:   NewSlogLogger(logger),
		logger: logger,
	}
}

// Log writes an audit entry to both SQLite and slog.
// SQLite errors are logged but do not fail the caller — the operation
// being audited should not be blocked by audit storage failures.
func (l *SQLiteLogger) Log(ctx context.Context, e Entry) error {
	// Always write to slog (structured log output for aggregators)
	l.slog.Log(ctx, e)

	// Persist to SQLite
	if err := l.store.Insert(ctx, e); err != nil {
		l.logger.Error("failed to persist audit entry", "error", err, "action", e.Action, "user", e.User)
		// Don't return the error — audit storage failure should not block the operation
	}

	return nil
}

// Query delegates to the underlying SQLiteStore for audit log queries.
func (l *SQLiteLogger) Query(ctx context.Context, params QueryParams) ([]Entry, int, error) {
	return l.store.Query(ctx, params)
}

// Cleanup delegates to the underlying SQLiteStore for retention cleanup.
func (l *SQLiteLogger) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	return l.store.Cleanup(ctx, retentionDays)
}

// Queryable is implemented by Logger implementations that support audit log queries.
type Queryable interface {
	Query(ctx context.Context, params QueryParams) ([]Entry, int, error)
}
