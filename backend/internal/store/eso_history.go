package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cleanupTimeout caps the retention DELETE so a wedged connection or
// runaway predicate can't pin a pgxpool slot indefinitely. 5 minutes is
// generous: a 10M-row delete on an indexed column should finish in under
// a minute under normal load. If retention legitimately needs longer,
// the next 1h tick will pick up the residue.
const cleanupTimeout = 5 * time.Minute

// ESOSyncHistoryEntry is one row in eso_sync_history. Mirrors the schema
// added by migration 000011. UID is the ExternalSecret's metadata.uid; the
// (uid, attempt_at) pair is the dedup key.
type ESOSyncHistoryEntry struct {
	ID                    int64
	ClusterID             string
	UID                   string
	Namespace             string
	Name                  string
	AttemptAt             time.Time
	Outcome               string
	Reason                string
	Message               string
	DiffKeysAdded         []string
	DiffKeysRemoved       []string
	DiffKeysChanged       []string
	SyncedResourceVersion string
}

// ESOHistoryStore handles CRUD for the eso_sync_history table.
type ESOHistoryStore struct {
	pool *pgxpool.Pool
}

// NewESOHistoryStore creates a sync-history store backed by PostgreSQL.
func NewESOHistoryStore(pool *pgxpool.Pool) *ESOHistoryStore {
	return &ESOHistoryStore{pool: pool}
}

// Insert appends a single sync attempt. Idempotent via the
// (uid, attempt_at) unique index — ON CONFLICT DO NOTHING absorbs duplicate
// inserts under poller restart, where the same lastRefreshTime would be
// re-observed for an ES whose row was already persisted.
func (s *ESOHistoryStore) Insert(ctx context.Context, e ESOSyncHistoryEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO eso_sync_history (
			cluster_id, uid, namespace, name, attempt_at, outcome,
			reason, message,
			diff_keys_added, diff_keys_removed, diff_keys_changed,
			synced_resource_version
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (uid, attempt_at) DO NOTHING`,
		e.ClusterID, e.UID, e.Namespace, e.Name, e.AttemptAt, e.Outcome,
		e.Reason, e.Message,
		e.DiffKeysAdded, e.DiffKeysRemoved, e.DiffKeysChanged,
		e.SyncedResourceVersion)
	if err != nil {
		return fmt.Errorf("insert eso_sync_history: %w", err)
	}
	return nil
}

// QueryByUID returns up to limit history entries for an ExternalSecret UID,
// most recent first.
//
// RBAC contract for callers: the diff_keys_* columns leak k8s Secret KEY
// NAMES (per the Phase C scope boundary that values are never persisted).
// Key names alone can be operationally sensitive — a key named
// "PROD_DB_PASSWORD" reveals credential structure. Future history
// endpoints MUST verify the requesting user holds `get` on `core/secrets`
// in the ES namespace, not merely `get externalsecret`. A user with ES
// read but not Secret read should receive a response with diff_keys_*
// stripped (or be denied entirely).
func (s *ESOHistoryStore) QueryByUID(ctx context.Context, uid string, limit int) ([]ESOSyncHistoryEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, cluster_id, uid, namespace, name, attempt_at, outcome,
		       reason, message,
		       diff_keys_added, diff_keys_removed, diff_keys_changed,
		       synced_resource_version
		FROM eso_sync_history
		WHERE uid = $1
		ORDER BY attempt_at DESC
		LIMIT $2`,
		uid, limit)
	if err != nil {
		return nil, fmt.Errorf("query eso_sync_history: %w", err)
	}
	defer rows.Close()

	var entries []ESOSyncHistoryEntry
	for rows.Next() {
		var e ESOSyncHistoryEntry
		if err := rows.Scan(
			&e.ID, &e.ClusterID, &e.UID, &e.Namespace, &e.Name, &e.AttemptAt, &e.Outcome,
			&e.Reason, &e.Message,
			&e.DiffKeysAdded, &e.DiffKeysRemoved, &e.DiffKeysChanged,
			&e.SyncedResourceVersion,
		); err != nil {
			return nil, fmt.Errorf("scan eso_sync_history: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// LatestByUID returns the most-recent history entry for a UID. Returns
// (nil, nil) when no rows exist for the UID; returns (nil, err) for any
// real query or scan failure. Callers must distinguish these cases so a
// transient DB fault doesn't render as "no history yet" — a subtle
// silent-failure mode caught by the Phase C review.
//
// Currently no caller is wired in this branch. Phase E or a follow-up
// will use this for cold-start drift hints (when the in-memory
// observedDrift map on Handler doesn't yet have an entry).
func (s *ESOHistoryStore) LatestByUID(ctx context.Context, uid string) (*ESOSyncHistoryEntry, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, cluster_id, uid, namespace, name, attempt_at, outcome,
		       reason, message,
		       diff_keys_added, diff_keys_removed, diff_keys_changed,
		       synced_resource_version
		FROM eso_sync_history
		WHERE uid = $1
		ORDER BY attempt_at DESC
		LIMIT 1`,
		uid)
	var e ESOSyncHistoryEntry
	if err := row.Scan(
		&e.ID, &e.ClusterID, &e.UID, &e.Namespace, &e.Name, &e.AttemptAt, &e.Outcome,
		&e.Reason, &e.Message,
		&e.DiffKeysAdded, &e.DiffKeysRemoved, &e.DiffKeysChanged,
		&e.SyncedResourceVersion,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("latest eso_sync_history for uid %s: %w", uid, err)
	}
	return &e, nil
}

// Cleanup deletes entries older than retentionDays. Returns the row count
// for logging / metrics. The poller's retention goroutine calls this on a
// 1h tick.
//
// A bounded ctx (cleanupTimeout) is wrapped around the parent ctx so a
// runaway DELETE on a multi-million-row table can't pin a pgxpool slot
// for the platform's lifetime. If the operation is genuinely longer
// than that, the next 1h tick picks up the residue.
func (s *ESOHistoryStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	tag, err := s.pool.Exec(cleanupCtx,
		"DELETE FROM eso_sync_history WHERE attempt_at < NOW() - $1 * INTERVAL '1 day'",
		retentionDays)
	if err != nil {
		return 0, fmt.Errorf("cleanup eso_sync_history: %w", err)
	}
	return tag.RowsAffected(), nil
}
