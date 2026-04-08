package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ComplianceSnapshot represents a daily compliance score snapshot.
type ComplianceSnapshot struct {
	Date         time.Time `json:"date"`
	ClusterID    string    `json:"clusterId"`
	OverallScore float64   `json:"score"`
	Pass         int       `json:"pass"`
	Fail         int       `json:"fail"`
	Total        int       `json:"total"`
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
		"total": snap.Total,
	})
	if err != nil {
		return fmt.Errorf("marshal compliance payload: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO compliance_snapshots (snapshot_date, cluster_id, overall_score, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cluster_id, snapshot_date) DO NOTHING`,
		snap.Date, snap.ClusterID, snap.OverallScore, payload)
	if err != nil {
		return fmt.Errorf("insert compliance snapshot: %w", err)
	}
	return nil
}

// QueryHistory returns snapshots for a cluster within the last N days.
// Returns sparse data (only days with snapshots); the frontend fills gaps.
func (s *ComplianceStore) QueryHistory(ctx context.Context, clusterID string, days int) ([]ComplianceSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT snapshot_date, overall_score,
		       COALESCE((payload->>'pass')::int, 0),
		       COALESCE((payload->>'fail')::int, 0),
		       COALESCE((payload->>'total')::int, 0)
		FROM compliance_snapshots
		WHERE cluster_id = $1 AND snapshot_date >= CURRENT_DATE - $2
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
		if err := rows.Scan(&snap.Date, &snap.OverallScore, &snap.Pass, &snap.Fail, &snap.Total); err != nil {
			return nil, fmt.Errorf("scan compliance snapshot: %w", err)
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, rows.Err()
}

// Cleanup deletes snapshots older than retentionDays, returning the count of deleted rows.
func (s *ComplianceStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM compliance_snapshots WHERE snapshot_date < CURRENT_DATE - $1",
		retentionDays)
	if err != nil {
		return 0, fmt.Errorf("cleanup compliance snapshots: %w", err)
	}
	return tag.RowsAffected(), nil
}
