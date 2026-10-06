package store

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// incident_grants.go — explicit, revocable collaborator grants (Release D,
// U21b; table incident_grants from migration 000024).
//
// A grant conveys standing to ask, never Kubernetes authority (Q1 P2): every
// evidence read still re-checks the caller's current authorization per item.
// Granting and revoking are owner-only (P3) and the owner is asserted in the
// SQL of every write, so a non-owner cannot grant or revoke even if a handler
// check is dropped.

var (
	// ErrGrantNotFound is returned by RemoveGrant when the grantee holds no
	// grant on the incident.
	ErrGrantNotFound = errors.New("incident grant not found")

	// ErrGrantLimit is returned by AddGrant when the incident already has
	// IncidentMaxGrants grantees and the grantee is a new one.
	ErrGrantLimit = errors.New("incident grant limit reached")
)

const (
	// IncidentMaxGrants caps the grantees per incident.
	IncidentMaxGrants = 50
	// IncidentMaxGranteeIDBytes bounds a grantee id (an auth.User.ID).
	IncidentMaxGranteeIDBytes = 256
)

// IncidentGrantRow is one row in incident_grants.
type IncidentGrantRow struct {
	IncidentID  uuid.UUID
	GranteeID   string
	GrantedBy   string
	CanAnnotate bool
	CreatedAt   time.Time
}

// ValidateGranteeID requires a non-empty, valid UTF-8 id of at most
// IncidentMaxGranteeIDBytes bytes with no control characters.
func ValidateGranteeID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: grantee id is required", ErrIncidentInvalid)
	}
	// "." and ".." are URL dot-segments: as the {granteeId} path segment of
	// a revoke they would be normalized into a different resource (".."
	// makes it DELETE /incidents/{id}), so no grant may carry one.
	if id == "." || id == ".." {
		return fmt.Errorf("%w: grantee id must not be %q", ErrIncidentInvalid, id)
	}
	if len(id) > IncidentMaxGranteeIDBytes {
		return fmt.Errorf("%w: grantee id exceeds %d bytes", ErrIncidentInvalid, IncidentMaxGranteeIDBytes)
	}
	if !utf8.ValidString(id) {
		return fmt.Errorf("%w: grantee id is not valid UTF-8", ErrIncidentInvalid)
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: grantee id contains a control character", ErrIncidentInvalid)
		}
	}
	return nil
}

// IncidentGrantStore handles persistence for incident_grants.
type IncidentGrantStore struct {
	pool        *pgxpool.Pool
	lockTimeout time.Duration // incidentLockTimeout; tests shorten it
}

// NewIncidentGrantStore creates a grant store. It never touches the pool, so
// it is safe to construct with a nil pool.
func NewIncidentGrantStore(pool *pgxpool.Pool) *IncidentGrantStore {
	return &IncidentGrantStore{pool: pool, lockTimeout: incidentLockTimeout}
}

// AddGrant gives granteeID a grant on an incident owned by ownerID, or updates
// can_annotate when the grantee already holds one. In one transaction holding
// the incident row lock (so concurrent grants cannot overshoot the cap):
// ErrIncidentNotFound or ErrNotOwner; a self-grant (granteeID == ownerID) is
// then a no-op returning nil, since the owner already holds every right;
// ErrGrantLimit when the grantee is new and the incident already has
// IncidentMaxGrants grantees. The INSERT itself selects from incidents with
// the owner in its WHERE clause.
//
// Any lock wait in the transaction past incidentLockTimeout (the incident row,
// or the grant row a concurrent RemoveGrant holds) returns the retryable
// ErrIncidentBusy, and nothing is written.
func (s *IncidentGrantStore) AddGrant(ctx context.Context, incidentID uuid.UUID, ownerID, granteeID string, canAnnotate bool) error {
	if err := requireIdentity("owner id", ownerID); err != nil {
		return err
	}
	if err := ValidateGranteeID(granteeID); err != nil {
		return err
	}
	return busyIfLockTimeout(s.addGrant(ctx, incidentID, ownerID, granteeID, canAnnotate))
}

// addGrant is AddGrant's transaction; AddGrant maps its lock timeouts.
func (s *IncidentGrantStore) addGrant(ctx context.Context, incidentID uuid.UUID, ownerID, granteeID string, canAnnotate bool) error {
	tx, err := beginIncidentLockTx(ctx, s.pool, s.lockTimeout, "incident grant")
	if err != nil {
		return err
	}
	defer rollbackDetached(ctx, tx)

	var owner string
	err = tx.QueryRow(ctx, `SELECT owner_id FROM incidents WHERE id = $1 FOR UPDATE`, incidentID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIncidentNotFound
	}
	if err != nil {
		return fmt.Errorf("lock incidents: %w", err)
	}
	if owner != ownerID {
		return ErrNotOwner
	}
	if granteeID == ownerID {
		return nil
	}

	var count int
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(bool_or(grantee_id = $2), false)
		  FROM incident_grants
		 WHERE incident_id = $1`, incidentID, granteeID).Scan(&count, &exists); err != nil {
		return fmt.Errorf("count incident_grants: %w", err)
	}
	if !exists && count >= IncidentMaxGrants {
		return ErrGrantLimit
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO incident_grants (incident_id, grantee_id, granted_by, can_annotate)
		SELECT i.id, $3, i.owner_id, $4
		  FROM incidents i
		 WHERE i.id = $1 AND i.owner_id = $2
		ON CONFLICT (incident_id, grantee_id) DO UPDATE SET can_annotate = EXCLUDED.can_annotate`,
		incidentID, ownerID, granteeID, canAnnotate)
	if err != nil {
		return fmt.Errorf("insert incident_grants: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotOwner
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit incident grant tx: %w", err)
	}
	return nil
}

// RemoveGrant revokes granteeID's grant on an incident owned by ownerID. The
// DELETE joins incidents with the owner in its WHERE clause. Returns
// ErrIncidentNotFound or ErrNotOwner before disclosing whether the grant
// exists, then ErrGrantNotFound when there is no such grant.
func (s *IncidentGrantStore) RemoveGrant(ctx context.Context, incidentID uuid.UUID, ownerID, granteeID string) error {
	if err := requireIdentity("owner id", ownerID); err != nil {
		return err
	}
	if err := ValidateGranteeID(granteeID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM incident_grants g
		 USING incidents i
		 WHERE g.incident_id = $1 AND g.grantee_id = $3
		   AND i.id = g.incident_id AND i.owner_id = $2`,
		incidentID, ownerID, granteeID)
	if err != nil {
		return fmt.Errorf("delete incident_grants: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var owner string
	err = s.pool.QueryRow(ctx, `SELECT owner_id FROM incidents WHERE id = $1`, incidentID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIncidentNotFound
	}
	if err != nil {
		return fmt.Errorf("read incidents ownership: %w", err)
	}
	if owner != ownerID {
		return ErrNotOwner
	}
	return ErrGrantNotFound
}

const grantColumns = `incident_id, grantee_id, granted_by, can_annotate, created_at`

func scanGrant(row pgx.Row) (IncidentGrantRow, error) {
	var g IncidentGrantRow
	err := row.Scan(&g.IncidentID, &g.GranteeID, &g.GrantedBy, &g.CanAnnotate, &g.CreatedAt)
	return g, err
}

// ListGrants returns an incident's grants, oldest first. AddGrant caps them
// at IncidentMaxGrants. Visibility of the incident is the caller's check.
func (s *IncidentGrantStore) ListGrants(ctx context.Context, incidentID uuid.UUID) ([]IncidentGrantRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+grantColumns+`
		  FROM incident_grants
		 WHERE incident_id = $1
		 ORDER BY created_at, grantee_id`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("list incident_grants: %w", err)
	}
	defer rows.Close()
	out := make([]IncidentGrantRow, 0)
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan incident_grants: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident_grants: %w", err)
	}
	return out, nil
}

// GrantsFor returns userID's grants on the named incidents, keyed by incident
// id, in one query; incidents without a grant for userID are absent. An
// empty id list returns an empty map without a query. The list endpoint
// resolves a page's collaborator roles with this rather than one GetGrant
// per row.
func (s *IncidentGrantStore) GrantsFor(ctx context.Context, userID string, incidentIDs []uuid.UUID) (map[uuid.UUID]IncidentGrantRow, error) {
	if err := requireIdentity("user id", userID); err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]IncidentGrantRow, len(incidentIDs))
	if len(incidentIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+grantColumns+`
		  FROM incident_grants
		 WHERE grantee_id = $1 AND incident_id = ANY($2)`, userID, incidentIDs)
	if err != nil {
		return nil, fmt.Errorf("list incident_grants for grantee: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan incident_grants: %w", err)
		}
		out[g.IncidentID] = g
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident_grants: %w", err)
	}
	return out, nil
}

// GetGrant returns userID's grant on an incident, or (nil, nil) when there is
// none. A database fault is (nil, err), never "no grant".
func (s *IncidentGrantStore) GetGrant(ctx context.Context, incidentID uuid.UUID, userID string) (*IncidentGrantRow, error) {
	if err := requireIdentity("user id", userID); err != nil {
		return nil, err
	}
	g, err := scanGrant(s.pool.QueryRow(ctx, `
		SELECT `+grantColumns+`
		  FROM incident_grants
		 WHERE incident_id = $1 AND grantee_id = $2`, incidentID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get incident_grants: %w", err)
	}
	return &g, nil
}
