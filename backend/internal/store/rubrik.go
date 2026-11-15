package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

// RubrikDefaultConnectionID is the id of the one Rubrik Security Cloud
// connection a production install has. Tests use their own ids.
const RubrikDefaultConnectionID = "default"

// pgForeignKeyViolation is PostgreSQL's SQLSTATE for a foreign-key breach.
const pgForeignKeyViolation = "23503"

var (
	// ErrRubrikConnectionNotFound is returned when no connection row exists.
	ErrRubrikConnectionNotFound = errors.New("rubrik connection not found")
	// ErrRubrikSecretRequired is returned by UpsertConnection when the save
	// would keep a stored client secret it may not keep: there is no stored
	// secret, or the tenant URL moves to another origin. Nothing is written.
	ErrRubrikSecretRequired = errors.New("rubrik client secret must be entered")
	// ErrRubrikConnectionChanged is returned by UpsertConnection when another
	// save got in between: it was asked to keep the stored secret but the
	// stored tenant URL is no longer the one the caller read, or two first
	// saves raced. Nothing is written.
	ErrRubrikConnectionChanged = errors.New("rubrik connection changed concurrently")
	// ErrRubrikInvalidTenantURL is returned when the tenant URL has no origin.
	ErrRubrikInvalidTenantURL = errors.New("rubrik tenant URL has no origin")
	// ErrRubrikMappingNotFound is returned when a cluster has no mapping.
	ErrRubrikMappingNotFound = errors.New("rubrik cluster mapping not found")
	// ErrRubrikMappingConflict is returned when the RSC cluster is already
	// mapped to another k8sCenter cluster.
	ErrRubrikMappingConflict = errors.New("rubrik cluster already mapped to another cluster")
	// ErrRubrikClusterUnknown is returned when the k8sCenter cluster is not
	// registered.
	ErrRubrikClusterUnknown = errors.New("cluster not registered")
)

// Rubrik compliance statuses persisted by the poller.
const (
	RubrikComplianceIn      = "in_compliance"
	RubrikComplianceLate    = "late"
	RubrikComplianceOut     = "out_of_compliance"
	RubrikComplianceUnknown = "unknown"
)

// RubrikConnection is the RSC tenant connection without its secret.
// HasSecret reports a usable stored secret: one is stored and it was entered
// for the current tenant URL's origin.
type RubrikConnection struct {
	ID              string
	TenantURL       string
	ClientID        string
	HasSecret       bool
	Revision        int64
	UpdatedBy       string
	UpdatedAt       time.Time
	LastSuccessAt   *time.Time
	LastFailureAt   *time.Time
	LastFailureKind string
}

// RubrikCredentials holds the decrypted client secret. Never log, return or
// audit it.
type RubrikCredentials struct {
	ClientSecret string
}

// RubrikConnectionWrite is one UpsertConnection.
type RubrikConnectionWrite struct {
	TenantURL string
	ClientID  string
	// ClientSecret is nil to keep the stored secret, or the new secret. An
	// empty secret is never stored.
	ClientSecret *string
	// KeptFrom is the tenant URL the caller read when it decided the stored
	// secret may stay ("" meaning no connection). Ignored when ClientSecret
	// is supplied.
	KeptFrom  string
	UpdatedBy string
}

// RubrikClusterMapping maps a k8sCenter cluster to an RSC Kubernetes cluster.
// ClusterGeneration is the cluster's registration generation when the
// mapping was saved; callers compare it with the current one.
type RubrikClusterMapping struct {
	ClusterID         string
	ConnectionID      string
	RSCClusterID      string
	RSCClusterName    string
	ClusterGeneration string
	CreatedBy         string
	CreatedAt         time.Time
	UpdatedBy         string
	UpdatedAt         time.Time
}

// RubrikComplianceState is the poller's persisted compliance judgment for one
// RSC object. An object without a row has unknown compliance.
type RubrikComplianceState struct {
	RSCObjectID string
	Namespace   string
	Status      string
	LateStreak  int
	ChangedAt   time.Time
	EvaluatedAt time.Time
}

// RubrikStore persists the Rubrik connection, cluster mappings and the
// poller's compliance states. The client secret is encrypted at rest with the
// same master secret as cluster credentials.
type RubrikStore struct {
	pool          *pgxpool.Pool
	encryptionKey string
}

// NewRubrikStore creates a Rubrik store backed by PostgreSQL.
func NewRubrikStore(pool *pgxpool.Pool, encryptionKey string) *RubrikStore {
	return &RubrikStore{pool: pool, encryptionKey: encryptionKey}
}

const rubrikConnectionColumns = `id, tenant_url, client_id, client_secret, secret_origin, revision,
	updated_by, updated_at, last_success_at, last_failure_at, last_failure_kind`

// scanRubrikConnection reads one row of rubrikConnectionColumns. The secret
// is returned only while it was entered for the current URL's origin; the
// store never writes any other row, so a mismatch means a hand edit and it
// fails closed.
func (s *RubrikStore) scanRubrikConnection(row pgx.Row) (RubrikConnection, RubrikCredentials, error) {
	var (
		c      RubrikConnection
		enc    []byte
		origin string
	)
	if err := row.Scan(&c.ID, &c.TenantURL, &c.ClientID, &enc, &origin, &c.Revision,
		&c.UpdatedBy, &c.UpdatedAt, &c.LastSuccessAt, &c.LastFailureAt, &c.LastFailureKind); err != nil {
		return RubrikConnection{}, RubrikCredentials{}, err
	}
	current := urlorigin.Of(c.TenantURL)
	c.HasSecret = len(enc) > 0 && current != "" && origin == current
	var creds RubrikCredentials
	if c.HasSecret {
		plain, err := Decrypt(enc, s.encryptionKey)
		if err != nil {
			return RubrikConnection{}, RubrikCredentials{}, fmt.Errorf("decrypting rubrik client secret: %w", err)
		}
		creds.ClientSecret = string(plain)
	}
	return c, creds, nil
}

// GetConnection returns the connection and its decrypted client secret
// (empty when none is usable).
func (s *RubrikStore) GetConnection(ctx context.Context, id string) (RubrikConnection, RubrikCredentials, error) {
	c, creds, err := s.scanRubrikConnection(s.pool.QueryRow(ctx,
		`SELECT `+rubrikConnectionColumns+` FROM rubrik_connection WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return RubrikConnection{}, RubrikCredentials{}, ErrRubrikConnectionNotFound
	}
	if err != nil {
		return RubrikConnection{}, RubrikCredentials{}, fmt.Errorf("getting rubrik connection: %w", err)
	}
	return c, creds, nil
}

// ConnectionRevision returns the connection's revision without decrypting
// anything. Every save advances it, so a caller that cached a token or client
// built from an older revision rebuilds it, whichever replica saved.
func (s *RubrikStore) ConnectionRevision(ctx context.Context, id string) (int64, error) {
	var rev int64
	err := s.pool.QueryRow(ctx, `SELECT revision FROM rubrik_connection WHERE id = $1`, id).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRubrikConnectionNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("getting rubrik connection revision: %w", err)
	}
	return rev, nil
}

// UpsertConnection creates or replaces the connection and returns it.
//
// A kept secret (ClientSecret nil) survives only when a connection exists,
// its stored secret was entered for the new URL's origin, and its stored URL
// is still KeptFrom; otherwise the result is ErrRubrikSecretRequired or
// ErrRubrikConnectionChanged and nothing is written. Saving a new origin
// with a new secret deletes every mapping of this connection, and every
// poller row with them, in the same transaction. Every save advances the
// revision and clears the recorded contact health, which described the
// previous settings.
func (s *RubrikStore) UpsertConnection(ctx context.Context, id string, w RubrikConnectionWrite) (RubrikConnection, error) {
	newOrigin := urlorigin.Of(w.TenantURL)
	if newOrigin == "" {
		return RubrikConnection{}, ErrRubrikInvalidTenantURL
	}
	var enc []byte
	if w.ClientSecret != nil {
		if *w.ClientSecret == "" {
			return RubrikConnection{}, ErrRubrikSecretRequired
		}
		var err error
		enc, err = Encrypt([]byte(*w.ClientSecret), s.encryptionKey)
		if err != nil {
			return RubrikConnection{}, fmt.Errorf("encrypting rubrik client secret: %w", err)
		}
	}

	var out RubrikConnection
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var storedURL, storedOrigin string
		err := tx.QueryRow(ctx, `SELECT tenant_url, secret_origin FROM rubrik_connection
			WHERE id = $1 FOR UPDATE`, id).Scan(&storedURL, &storedOrigin)
		exists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("reading rubrik connection: %w", err)
		}

		if enc == nil {
			if !exists || storedOrigin != newOrigin {
				return ErrRubrikSecretRequired
			}
			if storedURL != w.KeptFrom {
				return ErrRubrikConnectionChanged
			}
		}
		if exists && storedOrigin != newOrigin {
			// RSC cluster ids belong to the old tenant. The cascades take
			// every poller row with the mappings.
			if _, err := tx.Exec(ctx, `DELETE FROM rubrik_cluster_mappings WHERE connection_id = $1`, id); err != nil {
				return fmt.Errorf("clearing rubrik mappings: %w", err)
			}
		}

		// The row is locked (or known absent) above, so a plain UPDATE or
		// INSERT is enough. A concurrent first insert loses on the primary
		// key and returns an error rather than overwriting.
		var row pgx.Row
		if exists {
			row = tx.QueryRow(ctx, `
				UPDATE rubrik_connection SET
					tenant_url        = $2,
					client_id         = $3,
					client_secret     = COALESCE($4::bytea, client_secret),
					secret_origin     = $5,
					revision          = revision + 1,
					updated_by        = $6,
					updated_at        = now(),
					last_success_at   = NULL,
					last_failure_at   = NULL,
					last_failure_kind = ''
				WHERE id = $1
				RETURNING `+rubrikConnectionColumns,
				id, w.TenantURL, w.ClientID, enc, newOrigin, w.UpdatedBy)
		} else {
			row = tx.QueryRow(ctx, `
				INSERT INTO rubrik_connection (id, tenant_url, client_id, client_secret, secret_origin,
					revision, updated_by, updated_at)
				VALUES ($1, $2, $3, $4, $5, 1, $6, now())
				RETURNING `+rubrikConnectionColumns,
				id, w.TenantURL, w.ClientID, enc, newOrigin, w.UpdatedBy)
		}
		c, _, err := s.scanRubrikConnection(row)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			// Another first save inserted the row after our read.
			return ErrRubrikConnectionChanged
		}
		if err != nil {
			return fmt.Errorf("upserting rubrik connection: %w", err)
		}
		out = c
		return nil
	})
	if err != nil {
		return RubrikConnection{}, err
	}
	return out, nil
}

// RubrikMappingWrite is one SetMapping.
type RubrikMappingWrite struct {
	ClusterID    string
	ConnectionID string
	// ConnectionRevision is the connection revision the caller read when it
	// chose RSCClusterID. The save is refused when the connection has been
	// saved since, because the RSC cluster id may belong to another tenant.
	ConnectionRevision int64
	RSCClusterID       string
	RSCClusterName     string
	ClusterGeneration  string
	UpdatedBy          string
}

const rubrikMappingColumns = `cluster_id, connection_id, rsc_cluster_id, rsc_cluster_name,
	cluster_generation, created_by, created_at, updated_by, updated_at`

func scanRubrikMapping(row pgx.Row) (RubrikClusterMapping, error) {
	var m RubrikClusterMapping
	err := row.Scan(&m.ClusterID, &m.ConnectionID, &m.RSCClusterID, &m.RSCClusterName,
		&m.ClusterGeneration, &m.CreatedBy, &m.CreatedAt, &m.UpdatedBy, &m.UpdatedAt)
	return m, err
}

// SetMapping maps a k8sCenter cluster to an RSC cluster. Mapping it to a
// different RSC cluster, connection or registration generation replaces the
// mapping and drops the poller rows of the old one; saving the same target
// again only updates its name and audit fields.
//
// Saves for one cluster are serialized on its clusters row, so two
// overlapping saves cannot both report success for different targets. The
// connection row is read FOR SHARE and its revision compared with
// ConnectionRevision, which orders the save against UpsertConnection's
// FOR UPDATE: a mapping chosen for the old tenant cannot outlive an origin
// change.
//
// Errors: ErrRubrikMappingConflict when the RSC cluster is mapped to another
// k8sCenter cluster, ErrRubrikClusterUnknown when the cluster is not
// registered, ErrRubrikConnectionNotFound when the connection does not
// exist, ErrRubrikConnectionChanged when the connection was saved after the
// caller read ConnectionRevision.
func (s *RubrikStore) SetMapping(ctx context.Context, w RubrikMappingWrite) (RubrikClusterMapping, error) {
	var out RubrikClusterMapping
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var one int
		err := tx.QueryRow(ctx, `SELECT 1 FROM clusters WHERE id = $1 FOR NO KEY UPDATE`, w.ClusterID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRubrikClusterUnknown
		}
		if err != nil {
			return fmt.Errorf("locking cluster: %w", err)
		}
		var rev int64
		err = tx.QueryRow(ctx, `SELECT revision FROM rubrik_connection WHERE id = $1 FOR SHARE`, w.ConnectionID).Scan(&rev)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRubrikConnectionNotFound
		}
		if err != nil {
			return fmt.Errorf("reading rubrik connection revision: %w", err)
		}
		if rev != w.ConnectionRevision {
			return ErrRubrikConnectionChanged
		}

		var conn, rsc, gen string
		err = tx.QueryRow(ctx, `SELECT connection_id, rsc_cluster_id, cluster_generation
			FROM rubrik_cluster_mappings WHERE cluster_id = $1 FOR UPDATE`, w.ClusterID).Scan(&conn, &rsc, &gen)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("reading rubrik mapping: %w", err)
		case conn != w.ConnectionID || rsc != w.RSCClusterID || gen != w.ClusterGeneration:
			if _, err := tx.Exec(ctx, `DELETE FROM rubrik_cluster_mappings WHERE cluster_id = $1`, w.ClusterID); err != nil {
				return fmt.Errorf("replacing rubrik mapping: %w", err)
			}
		}
		m, err := scanRubrikMapping(tx.QueryRow(ctx, `
			INSERT INTO rubrik_cluster_mappings (cluster_id, connection_id, rsc_cluster_id, rsc_cluster_name,
				cluster_generation, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			ON CONFLICT (cluster_id) DO UPDATE SET
				rsc_cluster_name = EXCLUDED.rsc_cluster_name,
				updated_by       = EXCLUDED.updated_by,
				updated_at       = now()
			RETURNING `+rubrikMappingColumns,
			w.ClusterID, w.ConnectionID, w.RSCClusterID, w.RSCClusterName, w.ClusterGeneration, w.UpdatedBy))
		if err != nil {
			return classifyRubrikMappingError(err)
		}
		out = m
		return nil
	})
	if err != nil {
		return RubrikClusterMapping{}, err
	}
	return out, nil
}

// classifyRubrikMappingError maps constraint violations on a mapping write to
// the store's sentinels.
func classifyRubrikMappingError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == pgUniqueViolation:
			return ErrRubrikMappingConflict
		case pgErr.Code == pgForeignKeyViolation && pgErr.ConstraintName == "rubrik_cluster_mappings_cluster_id_fkey":
			return ErrRubrikClusterUnknown
		case pgErr.Code == pgForeignKeyViolation && pgErr.ConstraintName == "rubrik_cluster_mappings_connection_id_fkey":
			return ErrRubrikConnectionNotFound
		}
	}
	return fmt.Errorf("saving rubrik mapping: %w", err)
}

// GetMapping returns a cluster's mapping.
func (s *RubrikStore) GetMapping(ctx context.Context, clusterID string) (RubrikClusterMapping, error) {
	m, err := scanRubrikMapping(s.pool.QueryRow(ctx,
		`SELECT `+rubrikMappingColumns+` FROM rubrik_cluster_mappings WHERE cluster_id = $1`, clusterID))
	if errors.Is(err, pgx.ErrNoRows) {
		return RubrikClusterMapping{}, ErrRubrikMappingNotFound
	}
	if err != nil {
		return RubrikClusterMapping{}, fmt.Errorf("getting rubrik mapping: %w", err)
	}
	return m, nil
}

// ListMappings returns every mapping of a connection, ordered by cluster id.
func (s *RubrikStore) ListMappings(ctx context.Context, connectionID string) ([]RubrikClusterMapping, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+rubrikMappingColumns+` FROM rubrik_cluster_mappings
		WHERE connection_id = $1 ORDER BY cluster_id`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("listing rubrik mappings: %w", err)
	}
	defer rows.Close()
	var out []RubrikClusterMapping
	for rows.Next() {
		m, err := scanRubrikMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning rubrik mapping: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing rubrik mappings: %w", err)
	}
	return out, nil
}

// DeleteMapping removes a cluster's mapping and, through the cascades, its
// poller rows. It returns ErrRubrikMappingNotFound when there was none.
func (s *RubrikStore) DeleteMapping(ctx context.Context, clusterID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM rubrik_cluster_mappings WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return fmt.Errorf("deleting rubrik mapping: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRubrikMappingNotFound
	}
	return nil
}

// ListComplianceStates returns the poller's persisted compliance judgments
// for a cluster, ordered by RSC object id. An object with no row has unknown
// compliance.
func (s *RubrikStore) ListComplianceStates(ctx context.Context, clusterID string) ([]RubrikComplianceState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT rsc_object_id, namespace, status, late_streak, changed_at, evaluated_at
		FROM rubrik_compliance_states WHERE cluster_id = $1 ORDER BY rsc_object_id`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("listing rubrik compliance states: %w", err)
	}
	defer rows.Close()
	var out []RubrikComplianceState
	for rows.Next() {
		var c RubrikComplianceState
		if err := rows.Scan(&c.RSCObjectID, &c.Namespace, &c.Status, &c.LateStreak, &c.ChangedAt, &c.EvaluatedAt); err != nil {
			return nil, fmt.Errorf("scanning rubrik compliance state: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing rubrik compliance states: %w", err)
	}
	return out, nil
}
