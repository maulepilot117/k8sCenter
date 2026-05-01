package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BulkRefreshAction is the eso_bulk_refresh_jobs.action enum.
type BulkRefreshAction string

const (
	BulkRefreshActionStore        BulkRefreshAction = "refresh_store"
	BulkRefreshActionClusterStore BulkRefreshAction = "refresh_cluster_store"
	BulkRefreshActionNamespace    BulkRefreshAction = "refresh_namespace"
)

// BulkRefreshOutcome is one entry in the failed / skipped JSONB arrays.
type BulkRefreshOutcome struct {
	UID    string `json:"uid"`
	Reason string `json:"reason"`
}

// ESOBulkRefreshJob is one row in eso_bulk_refresh_jobs. Mirrors the schema
// added by migration 000013.
type ESOBulkRefreshJob struct {
	ID          uuid.UUID
	ClusterID   string
	RequestedBy string
	Action      BulkRefreshAction
	ScopeTarget string
	TargetUIDs  []string
	CreatedAt   time.Time
	CompletedAt *time.Time
	Succeeded   []string
	Failed      []BulkRefreshOutcome
	Skipped     []BulkRefreshOutcome
}

// ESOBulkJobStore handles CRUD for the eso_bulk_refresh_jobs table.
type ESOBulkJobStore struct {
	pool *pgxpool.Pool
}

// NewESOBulkJobStore creates a bulk-refresh-job store backed by PostgreSQL.
func NewESOBulkJobStore(pool *pgxpool.Pool) *ESOBulkJobStore {
	return &ESOBulkJobStore{pool: pool}
}

// Insert creates a new job row. Caller is responsible for generating the UUID
// (so the response can return jobId synchronously without a follow-up read).
func (s *ESOBulkJobStore) Insert(ctx context.Context, j ESOBulkRefreshJob) error {
	failed, _ := json.Marshal(j.Failed)
	skipped, _ := json.Marshal(j.Skipped)
	if len(failed) == 0 {
		failed = []byte("[]")
	}
	if len(skipped) == 0 {
		skipped = []byte("[]")
	}
	succeeded := j.Succeeded
	if succeeded == nil {
		succeeded = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO eso_bulk_refresh_jobs (
			id, cluster_id, requested_by, action, scope_target,
			target_uids, succeeded, failed, skipped
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		j.ID, j.ClusterID, j.RequestedBy, string(j.Action), j.ScopeTarget,
		j.TargetUIDs, succeeded, failed, skipped)
	if err != nil {
		return fmt.Errorf("insert eso_bulk_refresh_jobs: %w", err)
	}
	return nil
}

// Get returns a single job by id.
func (s *ESOBulkJobStore) Get(ctx context.Context, id uuid.UUID) (*ESOBulkRefreshJob, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, cluster_id, requested_by, action, scope_target,
		       target_uids, created_at, completed_at,
		       succeeded, failed, skipped
		FROM eso_bulk_refresh_jobs
		WHERE id = $1`, id)
	return scanJob(row)
}

// FindActive returns a non-completed job for (cluster_id, action, scope_target),
// or (nil, nil) when none exists. Used to enforce one-job-per-scope semantics.
func (s *ESOBulkJobStore) FindActive(
	ctx context.Context, clusterID string, action BulkRefreshAction, scopeTarget string,
) (*ESOBulkRefreshJob, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, cluster_id, requested_by, action, scope_target,
		       target_uids, created_at, completed_at,
		       succeeded, failed, skipped
		FROM eso_bulk_refresh_jobs
		WHERE cluster_id = $1 AND action = $2 AND scope_target = $3
		  AND completed_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1`,
		clusterID, string(action), scopeTarget)
	j, err := scanJob(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return j, nil
}

// AppendOutcome merges per-target results into the running job. One call per
// patched ES from the worker. Done as separate UPDATEs so the worker's
// progress is visible to GET callers in real time.
func (s *ESOBulkJobStore) AppendOutcome(
	ctx context.Context, id uuid.UUID, succeededUID string, failed *BulkRefreshOutcome, skipped *BulkRefreshOutcome,
) error {
	switch {
	case succeededUID != "":
		_, err := s.pool.Exec(ctx, `
			UPDATE eso_bulk_refresh_jobs
			SET succeeded = array_append(succeeded, $2)
			WHERE id = $1`, id, succeededUID)
		if err != nil {
			return fmt.Errorf("append succeeded: %w", err)
		}
	case failed != nil:
		entry, _ := json.Marshal(failed)
		_, err := s.pool.Exec(ctx, `
			UPDATE eso_bulk_refresh_jobs
			SET failed = failed || $2::jsonb
			WHERE id = $1`, id, "["+string(entry)+"]")
		if err != nil {
			return fmt.Errorf("append failed: %w", err)
		}
	case skipped != nil:
		entry, _ := json.Marshal(skipped)
		_, err := s.pool.Exec(ctx, `
			UPDATE eso_bulk_refresh_jobs
			SET skipped = skipped || $2::jsonb
			WHERE id = $1`, id, "["+string(entry)+"]")
		if err != nil {
			return fmt.Errorf("append skipped: %w", err)
		}
	}
	return nil
}

// Complete sets completed_at = NOW() if not already set. Idempotent. Returns
// the row's completed_at on success.
func (s *ESOBulkJobStore) Complete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE eso_bulk_refresh_jobs
		SET completed_at = NOW()
		WHERE id = $1 AND completed_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("complete bulk job: %w", err)
	}
	return nil
}

// CompleteOrphans marks every IN-PROGRESS job (completed_at IS NULL) as
// completed. Called once on platform startup to prevent zombie jobs from a
// crash. Returns the number of rows affected.
//
// Note that this does NOT distinguish "in-flight at shutdown" from "row left
// over by a worker that died mid-loop" — both end up resolved here. Worker
// progress (succeeded / failed / skipped) is preserved as-is.
func (s *ESOBulkJobStore) CompleteOrphans(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE eso_bulk_refresh_jobs
		SET completed_at = NOW()
		WHERE completed_at IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("complete orphans: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanJob centralizes the row-scan boilerplate shared by Get + FindActive.
func scanJob(row pgx.Row) (*ESOBulkRefreshJob, error) {
	var j ESOBulkRefreshJob
	var actionStr string
	var failedJSON, skippedJSON []byte
	if err := row.Scan(
		&j.ID, &j.ClusterID, &j.RequestedBy, &actionStr, &j.ScopeTarget,
		&j.TargetUIDs, &j.CreatedAt, &j.CompletedAt,
		&j.Succeeded, &failedJSON, &skippedJSON,
	); err != nil {
		return nil, err
	}
	j.Action = BulkRefreshAction(actionStr)
	if len(failedJSON) > 0 {
		_ = json.Unmarshal(failedJSON, &j.Failed)
	}
	if len(skippedJSON) > 0 {
		_ = json.Unmarshal(skippedJSON, &j.Skipped)
	}
	return &j, nil
}
