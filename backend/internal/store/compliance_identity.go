package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrComplianceIdentityNotFound is returned when a cluster has no stored
// compliance identity.
var ErrComplianceIdentityNotFound = errors.New("compliance identity not found")

// ComplianceIdentity is the background identity a remote cluster's compliance
// snapshot impersonates. It is not a secret. Revision grows whenever the
// username or groups change and is never reused, even across a delete and a
// re-insert.
type ComplianceIdentity struct {
	ClusterID string
	Username  string
	Groups    []string
	Revision  int64
	UpdatedBy string
	UpdatedAt time.Time
}

// ComplianceIdentityStore persists per-cluster compliance identities in the
// cluster_compliance_identity table.
type ComplianceIdentityStore struct {
	pool *pgxpool.Pool
}

// NewComplianceIdentityStore creates a compliance identity store backed by PostgreSQL.
func NewComplianceIdentityStore(pool *pgxpool.Pool) *ComplianceIdentityStore {
	return &ComplianceIdentityStore{pool: pool}
}

const complianceIdentityColumns = `cluster_id, username, groups, revision, COALESCE(updated_by, ''), updated_at`

func scanComplianceIdentity(row pgx.Row) (ComplianceIdentity, error) {
	var id ComplianceIdentity
	err := row.Scan(&id.ClusterID, &id.Username, &id.Groups, &id.Revision, &id.UpdatedBy, &id.UpdatedAt)
	return id, err
}

// Get returns a cluster's identity, or ErrComplianceIdentityNotFound.
func (s *ComplianceIdentityStore) Get(ctx context.Context, clusterID string) (ComplianceIdentity, error) {
	id, err := scanComplianceIdentity(s.pool.QueryRow(ctx,
		`SELECT `+complianceIdentityColumns+` FROM cluster_compliance_identity WHERE cluster_id = $1`, clusterID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ComplianceIdentity{}, ErrComplianceIdentityNotFound
	}
	if err != nil {
		return ComplianceIdentity{}, fmt.Errorf("getting compliance identity: %w", err)
	}
	return id, nil
}

// List returns every stored identity, ordered by cluster id.
func (s *ComplianceIdentityStore) List(ctx context.Context) ([]ComplianceIdentity, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+complianceIdentityColumns+` FROM cluster_compliance_identity ORDER BY cluster_id`)
	if err != nil {
		return nil, fmt.Errorf("listing compliance identities: %w", err)
	}
	defer rows.Close()
	var out []ComplianceIdentity
	for rows.Next() {
		id, err := scanComplianceIdentity(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning compliance identity: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing compliance identities: %w", err)
	}
	return out, nil
}

// Upsert stores the identity for a cluster and returns the stored record. A
// new revision is drawn from the sequence only when the row is new or the
// username or groups differ from the stored ones; identical values keep the
// stored revision (only updated_by and updated_at move). The cluster must be
// registered: an unknown cluster fails the foreign key.
func (s *ComplianceIdentityStore) Upsert(ctx context.Context, clusterID, username string, groups []string, updatedBy string) (ComplianceIdentity, error) {
	if groups == nil {
		groups = []string{}
	}
	// UPDATE first keeps nextval lazy: it is evaluated only in the CASE
	// branch that needs it. The INSERT covers a missing row, and its
	// ON CONFLICT arm covers a concurrent first save.
	id, err := scanComplianceIdentity(s.pool.QueryRow(ctx, `
		UPDATE cluster_compliance_identity SET
			revision   = CASE WHEN username IS DISTINCT FROM $2 OR groups IS DISTINCT FROM $3::text[]
			                  THEN nextval('cluster_compliance_identity_revision_seq') ELSE revision END,
			username   = $2,
			groups     = $3::text[],
			updated_by = NULLIF($4, ''),
			updated_at = now()
		WHERE cluster_id = $1
		RETURNING `+complianceIdentityColumns, clusterID, username, groups, updatedBy))
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ComplianceIdentity{}, fmt.Errorf("saving compliance identity: %w", err)
	}
	id, err = scanComplianceIdentity(s.pool.QueryRow(ctx, `
		INSERT INTO cluster_compliance_identity (cluster_id, username, groups, revision, updated_by, updated_at)
		VALUES ($1, $2, $3::text[], nextval('cluster_compliance_identity_revision_seq'), NULLIF($4, ''), now())
		ON CONFLICT (cluster_id) DO UPDATE SET
			revision   = CASE WHEN cluster_compliance_identity.username IS DISTINCT FROM EXCLUDED.username
			                    OR cluster_compliance_identity.groups IS DISTINCT FROM EXCLUDED.groups
			                  THEN EXCLUDED.revision ELSE cluster_compliance_identity.revision END,
			username   = EXCLUDED.username,
			groups     = EXCLUDED.groups,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
		RETURNING `+complianceIdentityColumns, clusterID, username, groups, updatedBy))
	if err != nil {
		return ComplianceIdentity{}, fmt.Errorf("saving compliance identity: %w", err)
	}
	return id, nil
}

// Delete removes a cluster's identity, or returns ErrComplianceIdentityNotFound.
func (s *ComplianceIdentityStore) Delete(ctx context.Context, clusterID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM cluster_compliance_identity WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return fmt.Errorf("deleting compliance identity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrComplianceIdentityNotFound
	}
	return nil
}
