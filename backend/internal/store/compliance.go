package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ComplianceSnapshot represents a daily compliance score snapshot.
type ComplianceSnapshot struct {
	Date         time.Time `json:"date"`
	ClusterID    string    `json:"clusterId"`
	OverallScore float64   `json:"score"`
	Pass         int       `json:"pass"`
	Fail         int       `json:"fail"`
	Warn         int       `json:"warn"`
	Total        int       `json:"total"`

	// Remote-cluster fields, stored in the payload beside the counts. They are
	// zero on local rows. The JSON tags keep the local response unchanged: the
	// identity fields are never serialised from here, and Partial and
	// Unreadable are omitted when empty.
	Username         string   `json:"-"`
	Groups           []string `json:"-"`
	IdentityRevision int64    `json:"-"`
	Partial          bool     `json:"partial,omitempty"`
	Unreadable       []string `json:"unreadable,omitempty"`
}

// ComplianceGap is a day a remote cluster's snapshot could not be taken.
type ComplianceGap struct {
	Date             time.Time `json:"date"`
	Reason           string    `json:"reason"`
	IdentityRevision int64     `json:"-"`
}

// ComplianceStore handles CRUD for the compliance_snapshots table.
type ComplianceStore struct {
	pool *pgxpool.Pool
}

// NewComplianceStore creates a compliance store backed by PostgreSQL.
func NewComplianceStore(pool *pgxpool.Pool) *ComplianceStore {
	return &ComplianceStore{pool: pool}
}

// Insert stores a compliance snapshot, ignoring duplicates for the same cluster+date.
func (s *ComplianceStore) Insert(ctx context.Context, snap *ComplianceSnapshot) error {
	payload, err := json.Marshal(map[string]int{
		"pass":  snap.Pass,
		"fail":  snap.Fail,
		"warn":  snap.Warn,
		"total": snap.Total,
	})
	if err != nil {
		return fmt.Errorf("marshal compliance payload: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO compliance_snapshots (snapshot_date, cluster_id, overall_score, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cluster_id, snapshot_date) DO UPDATE SET
			overall_score = EXCLUDED.overall_score,
			payload = EXCLUDED.payload,
			updated_at = NOW()`,
		snap.Date, snap.ClusterID, snap.OverallScore, payload)
	if err != nil {
		return fmt.Errorf("insert compliance snapshot: %w", err)
	}
	return nil
}

// lockCurrentIdentity locks the cluster's identity row for the transaction and
// reports whether its revision is still the given one. Holding the row lock
// serialises every guarded write for the cluster and blocks an identity change
// until the write commits. A missing row reports false.
func lockCurrentIdentity(ctx context.Context, tx pgx.Tx, clusterID string, revision int64) (bool, error) {
	var current int64
	err := tx.QueryRow(ctx,
		`SELECT revision FROM cluster_compliance_identity WHERE cluster_id = $1 FOR UPDATE`,
		clusterID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock compliance identity: %w", err)
	}
	return current == revision, nil
}

// InsertRemote stores a remote cluster's daily point under the write rules: it
// lands only while snap.IdentityRevision is the identity row's current
// revision, replaces a same-day point only when its revision is at least the
// stored one (a point without a revision counts as 0), and removes that day's
// gap, all in one transaction. It reports whether the point was stored; a
// stale revision, a missing identity row or an older revision than the stored
// point writes nothing and is not an error.
func (s *ComplianceStore) InsertRemote(ctx context.Context, snap *ComplianceSnapshot) (bool, error) {
	groups := snap.Groups
	if groups == nil {
		groups = []string{}
	}
	unreadable := snap.Unreadable
	if unreadable == nil {
		unreadable = []string{}
	}
	payload, err := json.Marshal(map[string]any{
		"pass":             snap.Pass,
		"fail":             snap.Fail,
		"warn":             snap.Warn,
		"total":            snap.Total,
		"username":         snap.Username,
		"groups":           groups,
		"identityRevision": snap.IdentityRevision,
		"partial":          snap.Partial,
		"unreadable":       unreadable,
	})
	if err != nil {
		return false, fmt.Errorf("marshal compliance payload: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin remote compliance write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := lockCurrentIdentity(ctx, tx, snap.ClusterID, snap.IdentityRevision)
	if err != nil {
		return false, err
	}
	if !current {
		return false, nil
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO compliance_snapshots AS cs (snapshot_date, cluster_id, overall_score, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cluster_id, snapshot_date) DO UPDATE SET
			overall_score = EXCLUDED.overall_score,
			payload = EXCLUDED.payload,
			updated_at = NOW()
		WHERE COALESCE((cs.payload->>'identityRevision')::bigint, 0) <= $5`,
		snap.Date, snap.ClusterID, snap.OverallScore, payload, snap.IdentityRevision)
	if err != nil {
		return false, fmt.Errorf("insert remote compliance snapshot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM compliance_snapshot_gaps WHERE cluster_id = $1 AND snapshot_date = $2`,
		snap.ClusterID, snap.Date); err != nil {
		return false, fmt.Errorf("clear compliance gap: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit remote compliance write: %w", err)
	}
	return true, nil
}

// InsertGap records a day a remote cluster's snapshot could not be taken. It
// lands only while revision is the identity row's current one, never replaces
// a same-day point of the same or a higher revision, deletes a same-day point
// of a strictly lower revision, and otherwise upserts the gap. It reports
// whether the gap was stored.
func (s *ComplianceStore) InsertGap(ctx context.Context, clusterID string, date time.Time, reason string, revision int64) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin compliance gap write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := lockCurrentIdentity(ctx, tx, clusterID, revision)
	if err != nil {
		return false, err
	}
	if !current {
		return false, nil
	}
	var stored int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE((payload->>'identityRevision')::bigint, 0)
		FROM compliance_snapshots WHERE cluster_id = $1 AND snapshot_date = $2`,
		clusterID, date).Scan(&stored)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No same-day point: the gap goes in.
	case err != nil:
		return false, fmt.Errorf("read same-day compliance snapshot: %w", err)
	case stored >= revision:
		return false, nil
	default:
		if _, err := tx.Exec(ctx,
			`DELETE FROM compliance_snapshots WHERE cluster_id = $1 AND snapshot_date = $2`,
			clusterID, date); err != nil {
			return false, fmt.Errorf("replace compliance snapshot with gap: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO compliance_snapshot_gaps (cluster_id, snapshot_date, reason, identity_revision)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cluster_id, snapshot_date) DO UPDATE SET
			reason = EXCLUDED.reason,
			identity_revision = EXCLUDED.identity_revision,
			updated_at = now()`,
		clusterID, date, reason, revision); err != nil {
		return false, fmt.Errorf("insert compliance gap: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit compliance gap write: %w", err)
	}
	return true, nil
}

// QueryHistory returns snapshots for a cluster within the last N days.
// Returns sparse data (only days with snapshots); the frontend fills gaps.
// Remote rows also carry the identity, revision and coverage from the payload;
// those fields stay zero on local rows.
func (s *ComplianceStore) QueryHistory(ctx context.Context, clusterID string, days int) ([]ComplianceSnapshot, error) {
	if days < 1 {
		days = 1
	}
	rows, err := s.pool.Query(ctx, `
		SELECT snapshot_date, overall_score,
		       COALESCE((payload->>'pass')::int, 0),
		       COALESCE((payload->>'fail')::int, 0),
		       COALESCE((payload->>'warn')::int, 0),
		       COALESCE((payload->>'total')::int, 0),
		       COALESCE(payload->>'username', ''),
		       COALESCE(payload->'groups', '[]'::jsonb),
		       COALESCE((payload->>'identityRevision')::bigint, 0),
		       COALESCE((payload->>'partial')::boolean, false),
		       COALESCE(payload->'unreadable', '[]'::jsonb)
		FROM compliance_snapshots
		WHERE cluster_id = $1 AND snapshot_date >= CURRENT_DATE - $2 * INTERVAL '1 day'
		ORDER BY snapshot_date`,
		clusterID, days)
	if err != nil {
		return nil, fmt.Errorf("query compliance history: %w", err)
	}
	defer rows.Close()

	var snapshots []ComplianceSnapshot
	for rows.Next() {
		var snap ComplianceSnapshot
		snap.ClusterID = clusterID
		if err := rows.Scan(&snap.Date, &snap.OverallScore, &snap.Pass, &snap.Fail, &snap.Warn, &snap.Total,
			&snap.Username, &snap.Groups, &snap.IdentityRevision, &snap.Partial, &snap.Unreadable); err != nil {
			return nil, fmt.Errorf("scan compliance snapshot: %w", err)
		}
		if len(snap.Groups) == 0 {
			snap.Groups = nil
		}
		if len(snap.Unreadable) == 0 {
			snap.Unreadable = nil
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, rows.Err()
}

// QueryGaps returns the gap days for a cluster within the last N days, oldest first.
func (s *ComplianceStore) QueryGaps(ctx context.Context, clusterID string, days int) ([]ComplianceGap, error) {
	if days < 1 {
		days = 1
	}
	rows, err := s.pool.Query(ctx, `
		SELECT snapshot_date, reason, identity_revision
		FROM compliance_snapshot_gaps
		WHERE cluster_id = $1 AND snapshot_date >= CURRENT_DATE - $2 * INTERVAL '1 day'
		ORDER BY snapshot_date`,
		clusterID, days)
	if err != nil {
		return nil, fmt.Errorf("query compliance gaps: %w", err)
	}
	defer rows.Close()

	var gaps []ComplianceGap
	for rows.Next() {
		var g ComplianceGap
		if err := rows.Scan(&g.Date, &g.Reason, &g.IdentityRevision); err != nil {
			return nil, fmt.Errorf("scan compliance gap: %w", err)
		}
		gaps = append(gaps, g)
	}
	return gaps, rows.Err()
}

// Cleanup deletes snapshots and gaps older than retentionDays for every
// cluster, returning the combined count of deleted rows.
func (s *ComplianceStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM compliance_snapshots WHERE snapshot_date < CURRENT_DATE - $1 * INTERVAL '1 day'",
		retentionDays)
	if err != nil {
		return 0, fmt.Errorf("cleanup compliance snapshots: %w", err)
	}
	gapTag, err := s.pool.Exec(ctx,
		"DELETE FROM compliance_snapshot_gaps WHERE snapshot_date < CURRENT_DATE - $1 * INTERVAL '1 day'",
		retentionDays)
	if err != nil {
		return 0, fmt.Errorf("cleanup compliance snapshot gaps: %w", err)
	}
	return tag.RowsAffected() + gapTag.RowsAffected(), nil
}
