package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
// (cluster_id, uid, attempt_at) triple is the dedup key (migration 000020).
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
// (cluster_id, uid, attempt_at) unique index — ON CONFLICT DO NOTHING absorbs
// duplicate inserts under poller restart, where the same lastRefreshTime
// would be re-observed for an ES whose row was already persisted. The
// conflict target must name that index's columns exactly (migration 000020);
// PostgreSQL rejects a target no unique index matches.
func (s *ESOHistoryStore) Insert(ctx context.Context, e ESOSyncHistoryEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO eso_sync_history (
			cluster_id, uid, namespace, name, attempt_at, outcome,
			reason, message,
			diff_keys_added, diff_keys_removed, diff_keys_changed,
			synced_resource_version
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (cluster_id, uid, attempt_at) DO NOTHING`,
		e.ClusterID, e.UID, e.Namespace, e.Name, e.AttemptAt, e.Outcome,
		e.Reason, e.Message,
		e.DiffKeysAdded, e.DiffKeysRemoved, e.DiffKeysChanged,
		e.SyncedResourceVersion)
	if err != nil {
		return fmt.Errorf("insert eso_sync_history: %w", err)
	}
	return nil
}

// esoHistoryColumns is the SELECT list every reader scans with scanESOHistory.
// Kept in one place so a column added to the table cannot be read by one
// method and silently dropped by another.
const esoHistoryColumns = `
	id, cluster_id, uid, namespace, name, attempt_at, outcome,
	reason, message,
	diff_keys_added, diff_keys_removed, diff_keys_changed,
	synced_resource_version`

// scanESOHistory scans one row selected with esoHistoryColumns.
func scanESOHistory(row pgx.Row) (ESOSyncHistoryEntry, error) {
	var e ESOSyncHistoryEntry
	err := row.Scan(
		&e.ID, &e.ClusterID, &e.UID, &e.Namespace, &e.Name, &e.AttemptAt, &e.Outcome,
		&e.Reason, &e.Message,
		&e.DiffKeysAdded, &e.DiffKeysRemoved, &e.DiffKeysChanged,
		&e.SyncedResourceVersion,
	)
	return e, err
}

// History page-size bounds. A limit outside [1, maxESOHistoryLimit] is not an
// error: zero or negative means "the default", anything larger is clamped.
const (
	defaultESOHistoryLimit = 50
	maxESOHistoryLimit     = 200
)

// clampESOHistoryLimit applies the page-size bounds.
func clampESOHistoryLimit(limit int) int {
	switch {
	case limit < 1:
		return defaultESOHistoryLimit
	case limit > maxESOHistoryLimit:
		return maxESOHistoryLimit
	default:
		return limit
	}
}

// ErrInvalidESOHistoryCursor reports a cursor that does not decode. Callers
// answer it with a 400, never by silently restarting at page one: a reset
// would send a paginating client round the same pages forever.
var ErrInvalidESOHistoryCursor = errors.New("invalid eso history cursor")

// ESOHistoryCursor is the keyset position after the last row of a page: the
// (attempt_at, id) tuple of that row. id breaks ties between attempts
// recorded at the same instant, so paging is exact even then.
type ESOHistoryCursor struct {
	AttemptAt time.Time
	ID        int64
}

// ESOHistoryPage is one page of history, newest first. NextCursor is empty
// when there is no further page.
type ESOHistoryPage struct {
	Entries    []ESOSyncHistoryEntry
	NextCursor string
}

// Cursor decode bounds. The encoded form of the largest valid cursor is well
// under maxESOHistoryCursorBytes decoded; anything longer is rejected before
// parsing. The upper timestamp bound (2100-01-01T00:00:00Z) keeps a forged
// value from overflowing time.UnixMicro into a nonsense instant.
const (
	maxESOHistoryCursorBytes  = 64
	maxESOHistoryCursorMicros = int64(4102444800000000)
)

// EncodeESOHistoryCursor renders a cursor as unpadded base64url over
// "<attempt_at unix microseconds>:<id>". Microseconds match PostgreSQL's
// TIMESTAMPTZ resolution, so a round trip reproduces the stored value exactly.
//
// The cursor is deliberately NOT signed. It carries no authority: QueryPage
// takes cluster_id from server configuration and uid from the caller's live
// read of the object, and pins both in its WHERE clause. A forged cursor can
// only move the caller's window among rows they are already allowed to read;
// it cannot reach another cluster or another object. Signing it would add key
// management without moving any boundary.
func EncodeESOHistoryCursor(c ESOHistoryCursor) string {
	raw := strconv.FormatInt(c.AttemptAt.UnixMicro(), 10) + ":" + strconv.FormatInt(c.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeESOHistoryCursor parses a cursor produced by EncodeESOHistoryCursor.
// Every malformed input returns ErrInvalidESOHistoryCursor.
func DecodeESOHistoryCursor(s string) (ESOHistoryCursor, error) {
	if s == "" || base64.RawURLEncoding.DecodedLen(len(s)) > maxESOHistoryCursorBytes {
		return ESOHistoryCursor{}, ErrInvalidESOHistoryCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || !utf8.Valid(raw) {
		return ESOHistoryCursor{}, ErrInvalidESOHistoryCursor
	}
	microsText, idText, ok := strings.Cut(string(raw), ":")
	if !ok || strings.Contains(idText, ":") {
		return ESOHistoryCursor{}, ErrInvalidESOHistoryCursor
	}
	micros, err := strconv.ParseInt(microsText, 10, 64)
	if err != nil || micros < 0 || micros > maxESOHistoryCursorMicros {
		return ESOHistoryCursor{}, ErrInvalidESOHistoryCursor
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id < 1 {
		return ESOHistoryCursor{}, ErrInvalidESOHistoryCursor
	}
	return ESOHistoryCursor{AttemptAt: time.UnixMicro(micros).UTC(), ID: id}, nil
}

// The two page queries QueryPage chooses between. They are two fixed SQL texts
// rather than one text with "$3 IS NULL OR (attempt_at, id) < (...)", because
// pgx prepares each text once per pooled connection and PostgreSQL switches a
// prepared statement to a generic plan after five executions. A generic plan
// cannot know whether $3 is NULL, so the OR form degrades to scanning every
// row from the newest down to the cursor and filtering: O(depth) per page.
// With the predicate fixed in the text, the row-value comparison stays an
// index condition on idx_eso_sync_history_keyset under either plan type
// (TestESOHistory_NextPageSQL_KeysetUnderGenericPlan pins this).
const (
	esoHistoryFirstPageSQL = `
		SELECT` + esoHistoryColumns + `
		FROM eso_sync_history
		WHERE cluster_id = $1 AND uid = $2
		ORDER BY attempt_at DESC, id DESC
		LIMIT $3`

	esoHistoryNextPageSQL = `
		SELECT` + esoHistoryColumns + `
		FROM eso_sync_history
		WHERE cluster_id = $1 AND uid = $2
		  AND (attempt_at, id) < ($3::timestamptz, $4::bigint)
		ORDER BY attempt_at DESC, id DESC
		LIMIT $5`
)

// QueryPage returns one page of history for an ExternalSecret, newest first,
// starting after the cursor (or at the newest row when after is nil). limit
// is clamped by clampESOHistoryLimit.
//
// Both clusterID and uid are required and both are pinned in the WHERE
// clause: a UID alone is not an identity across clusters, because a cluster
// restored from backup into a second registration reproduces the same UIDs.
//
// A database fault is (ESOHistoryPage{}, err), never an empty page with a nil
// error, so a caller can tell "no history yet" from "history unavailable".
//
// The diff_keys_* columns hold Secret KEY NAMES (never values), and key names
// alone can be sensitive — "PROD_DB_PASSWORD" describes credential structure.
// This method returns them unfiltered; the HTTP layer projects them away for
// a caller who cannot read Secrets in the ExternalSecret's namespace (Release
// B, U14a). Any new caller must apply that projection too.
func (s *ESOHistoryStore) QueryPage(ctx context.Context, clusterID, uid string, after *ESOHistoryCursor, limit int) (ESOHistoryPage, error) {
	limit = clampESOHistoryLimit(limit)

	var (
		rows pgx.Rows
		err  error
	)
	if after == nil {
		rows, err = s.pool.Query(ctx, esoHistoryFirstPageSQL, clusterID, uid, limit)
	} else {
		rows, err = s.pool.Query(ctx, esoHistoryNextPageSQL, clusterID, uid, after.AttemptAt, after.ID, limit)
	}
	if err != nil {
		return ESOHistoryPage{}, fmt.Errorf("query eso_sync_history page: %w", err)
	}
	defer rows.Close()

	entries := make([]ESOSyncHistoryEntry, 0, limit)
	for rows.Next() {
		e, err := scanESOHistory(rows)
		if err != nil {
			return ESOHistoryPage{}, fmt.Errorf("scan eso_sync_history: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return ESOHistoryPage{}, fmt.Errorf("iterate eso_sync_history: %w", err)
	}

	page := ESOHistoryPage{Entries: entries}
	// A full page may be followed by more; a short one cannot. A full page
	// that happens to be the last costs the client one extra, empty request.
	if len(entries) == limit {
		last := entries[len(entries)-1]
		page.NextCursor = EncodeESOHistoryCursor(ESOHistoryCursor{AttemptAt: last.AttemptAt, ID: last.ID})
	}
	return page, nil
}

// LatestByClusterUID returns the most recent history entry for an
// ExternalSecret in one cluster. It returns (nil, nil) when there is no row
// and (nil, err) on any query or scan failure, so a transient fault never
// reads as "no history yet".
func (s *ESOHistoryStore) LatestByClusterUID(ctx context.Context, clusterID, uid string) (*ESOSyncHistoryEntry, error) {
	e, err := scanESOHistory(s.pool.QueryRow(ctx, `
		SELECT`+esoHistoryColumns+`
		FROM eso_sync_history
		WHERE cluster_id = $1 AND uid = $2
		ORDER BY attempt_at DESC, id DESC
		LIMIT 1`,
		clusterID, uid))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("latest eso_sync_history for cluster %s uid %s: %w", clusterID, uid, err)
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
