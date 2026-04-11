package audit

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// asyncBufferSize is the channel buffer for async audit writes.
	// Large enough to absorb bursts without blocking callers.
	asyncBufferSize = 1000

	// insertTimeout is the timeout for each database insert.
	insertTimeout = 5 * time.Second

	// maxRetries is the number of retry attempts for transient errors.
	maxRetries = 3

	// initialBackoff is the starting backoff duration for retries.
	initialBackoff = 100 * time.Millisecond
)

// PostgresLogger implements the Logger interface with persistent PostgreSQL storage.
// It dual-writes to both PostgreSQL (for querying) and slog (for log aggregators).
// Database writes are asynchronous to avoid blocking HTTP handlers.
type PostgresLogger struct {
	store  *PostgresStore
	slog   *SlogLogger
	logger *slog.Logger

	entryCh  chan Entry
	wg       sync.WaitGroup
	stopOnce sync.Once

	// Metrics
	droppedCount atomic.Int64
}

// NewPostgresLogger creates an audit logger that persists entries in PostgreSQL
// and also writes to structured log output. Database writes are asynchronous.
func NewPostgresLogger(store *PostgresStore, logger *slog.Logger) *PostgresLogger {
	l := &PostgresLogger{
		store:   store,
		slog:    NewSlogLogger(logger),
		logger:  logger,
		entryCh: make(chan Entry, asyncBufferSize),
	}

	l.wg.Add(1)
	go l.asyncWriter()

	return l
}

// asyncWriter drains the entry channel and writes to PostgreSQL.
// Uses range over channel — exits cleanly when channel is closed and drained.
func (l *PostgresLogger) asyncWriter() {
	defer l.wg.Done()

	for e := range l.entryCh {
		if err := l.insertWithRetry(e); err != nil {
			l.logger.Error("failed to persist audit entry after retries",
				"error", err, "action", e.Action, "user", e.User)
		}
	}
}

// insertWithRetry attempts to insert an entry with exponential backoff.
func (l *PostgresLogger) insertWithRetry(e Entry) error {
	backoff := initialBackoff

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
		err := l.store.Insert(ctx, e)
		cancel()

		if err == nil {
			return nil
		}

		lastErr = err

		// Only retry on transient errors
		if !isRetryable(err) {
			return err
		}

		// Log retry attempt
		if attempt < maxRetries-1 {
			l.logger.Warn("audit insert failed, retrying",
				"error", err, "attempt", attempt+1, "backoff", backoff)
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	return lastErr
}

// isRetryable returns true for transient errors that warrant a retry.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Retry on timeouts and connection errors
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "reset") ||
		strings.Contains(errStr, "refused")
}

// Log writes an audit entry to slog immediately and queues it for async PostgreSQL persistence.
// This method never blocks on database I/O.
func (l *PostgresLogger) Log(ctx context.Context, e Entry) error {
	// Always write to slog immediately (structured log output for aggregators)
	l.slog.Log(ctx, e)

	// Queue for async PostgreSQL persistence (non-blocking)
	select {
	case l.entryCh <- e:
		// Queued successfully
	default:
		// Buffer full — increment counter and log warning
		dropped := l.droppedCount.Add(1)
		l.logger.Warn("audit buffer full, entry dropped",
			"action", e.Action, "user", e.User, "totalDropped", dropped)
	}

	return nil
}

// DroppedCount returns the number of audit entries dropped due to buffer full.
// Useful for monitoring and alerting.
func (l *PostgresLogger) DroppedCount() int64 {
	return l.droppedCount.Load()
}

// Close stops the async writer and flushes pending entries.
// Blocks until all queued entries are processed or written.
// Call this on application shutdown.
func (l *PostgresLogger) Close() {
	l.stopOnce.Do(func() {
		// Close channel to signal asyncWriter to drain and exit
		close(l.entryCh)

		// Wait for asyncWriter to finish processing all entries
		l.wg.Wait()

		dropped := l.droppedCount.Load()
		if dropped > 0 {
			l.logger.Warn("audit logger shutdown complete", "entriesDropped", dropped)
		} else {
			l.logger.Info("audit logger shutdown complete")
		}
	})
}

// Query delegates to the underlying PostgresStore for audit log queries.
func (l *PostgresLogger) Query(ctx context.Context, params QueryParams) ([]Entry, int, error) {
	return l.store.Query(ctx, params)
}

// Cleanup delegates to the underlying PostgresStore for retention cleanup.
func (l *PostgresLogger) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	return l.store.Cleanup(ctx, retentionDays)
}

// Queryable is implemented by Logger implementations that support audit log queries.
type Queryable interface {
	Query(ctx context.Context, params QueryParams) ([]Entry, int, error)
}
