package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// asyncBufferSize is the channel buffer for async audit writes.
	// Large enough to absorb bursts without blocking callers.
	asyncBufferSize = 1000

	// flushTimeout is how long to wait for pending writes on shutdown.
	flushTimeout = 5 * time.Second
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
	stopCh   chan struct{}
}

// NewPostgresLogger creates an audit logger that persists entries in PostgreSQL
// and also writes to structured log output. Database writes are asynchronous.
func NewPostgresLogger(store *PostgresStore, logger *slog.Logger) *PostgresLogger {
	l := &PostgresLogger{
		store:   store,
		slog:    NewSlogLogger(logger),
		logger:  logger,
		entryCh: make(chan Entry, asyncBufferSize),
		stopCh:  make(chan struct{}),
	}

	l.wg.Add(1)
	go l.asyncWriter()

	return l
}

// asyncWriter drains the entry channel and writes to PostgreSQL.
func (l *PostgresLogger) asyncWriter() {
	defer l.wg.Done()

	for {
		select {
		case e := <-l.entryCh:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := l.store.Insert(ctx, e); err != nil {
				l.logger.Error("failed to persist audit entry", "error", err, "action", e.Action, "user", e.User)
			}
			cancel()

		case <-l.stopCh:
			// Drain remaining entries before exiting
			for {
				select {
				case e := <-l.entryCh:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					if err := l.store.Insert(ctx, e); err != nil {
						l.logger.Error("failed to persist audit entry during shutdown", "error", err, "action", e.Action)
					}
					cancel()
				default:
					return
				}
			}
		}
	}
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
		// Buffer full — log warning but don't block the caller
		l.logger.Warn("audit buffer full, entry dropped", "action", e.Action, "user", e.User)
	}

	return nil
}

// Close stops the async writer and flushes pending entries.
// Call this on application shutdown.
func (l *PostgresLogger) Close() {
	l.stopOnce.Do(func() {
		close(l.stopCh)

		// Wait for writer to drain with timeout
		done := make(chan struct{})
		go func() {
			l.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			l.logger.Info("audit logger shutdown complete")
		case <-time.After(flushTimeout):
			l.logger.Warn("audit logger shutdown timed out, some entries may be lost")
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
