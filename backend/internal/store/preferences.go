package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PreferenceKind is the user_preferences.kind enum.
type PreferenceKind string

const (
	PreferenceKindSavedView PreferenceKind = "saved_view"
	PreferenceKindPin       PreferenceKind = "pin"
)

// Sentinel errors. Handlers map these onto HTTP status + reason codes;
// they must never be returned to a caller verbatim.
var (
	ErrPreferenceNotFound  = errors.New("preference not found")
	ErrPreferenceConflict  = errors.New("preference revision conflict")
	ErrPreferenceDuplicate = errors.New("preference already exists")
	ErrPreferenceLimit     = errors.New("preference limit reached")
)

// pgUniqueViolation is PostgreSQL's SQLSTATE for a unique-constraint breach.
const pgUniqueViolation = "23505"

// PreferenceRecord is one row of user_preferences. OwnerID and DedupKey are
// server-derived and never serialized to a client.
type PreferenceRecord struct {
	ID            uuid.UUID       `json:"id"`
	OwnerID       string          `json:"-"`
	Kind          PreferenceKind  `json:"kind"`
	Name          string          `json:"name"`
	ClusterID     string          `json:"clusterId"`
	DedupKey      string          `json:"-"`
	SchemaVersion int             `json:"schemaVersion"`
	Revision      int64           `json:"revision"`
	Config        json.RawMessage `json:"config"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// PreferenceStore handles owner-scoped CRUD for user_preferences.
//
// Authorization contract for callers: every method takes ownerID and every
// statement constrains on it. A record id belonging to another owner is
// indistinguishable from a nonexistent one — callers MUST NOT probe for
// existence outside this store.
//
// The store performs no field-level validation of Config: the allowlist that
// decides which keys may be stored lives in the handler layer, which runs
// before any write reaches here. The DDL's CHECK constraints (kind enum,
// length bounds, 8 KiB config ceiling) are the database's own backstop and
// surface as ordinary wrapped errors, not as one of the sentinels above.
type PreferenceStore struct {
	pool *pgxpool.Pool
}

// NewPreferenceStore creates a preference store backed by PostgreSQL.
func NewPreferenceStore(pool *pgxpool.Pool) *PreferenceStore {
	return &PreferenceStore{pool: pool}
}

// preferenceColumns is the column list every read and RETURNING clause
// shares, in the order scanPreference expects.
const preferenceColumns = `id, owner_id, kind, name, cluster_id, dedup_key,
	       schema_version, revision, config, created_at, updated_at`

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanPreference reads one row in preferenceColumns order. config is scanned
// through []byte because pgx hands JSONB back as raw bytes; the copy becomes
// the record's json.RawMessage.
func scanPreference(row rowScanner) (PreferenceRecord, error) {
	var (
		rec PreferenceRecord
		raw []byte
	)
	err := row.Scan(
		&rec.ID, &rec.OwnerID, &rec.Kind, &rec.Name, &rec.ClusterID, &rec.DedupKey,
		&rec.SchemaVersion, &rec.Revision, &raw, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		return PreferenceRecord{}, err
	}
	rec.Config = json.RawMessage(raw)
	return rec, nil
}

// configOrEmpty normalises a nil or empty config to an empty JSON object so
// the NOT NULL column always receives valid JSONB.
func configOrEmpty(config json.RawMessage) []byte {
	if len(config) == 0 {
		return []byte(`{}`)
	}
	return config
}

// isUniqueViolation reports whether err is PostgreSQL's unique-constraint
// breach — for this table, always the (owner_id, kind, cluster_id, dedup_key)
// index.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// List returns every record of one kind owned by ownerID, across all
// clusters, most recently updated first. An owner with no records gets an
// empty slice and a nil error.
func (s *PreferenceStore) List(ctx context.Context, ownerID string, kind PreferenceKind) ([]PreferenceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+preferenceColumns+`
		FROM user_preferences
		WHERE owner_id = $1 AND kind = $2
		ORDER BY updated_at DESC`,
		ownerID, kind)
	if err != nil {
		return nil, fmt.Errorf("query user_preferences: %w", err)
	}
	defer rows.Close()

	var records []PreferenceRecord
	for rows.Next() {
		rec, err := scanPreference(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user_preferences: %w", err)
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query user_preferences: %w", err)
	}
	return records, nil
}

// Get returns one record by id. A record owned by somebody else returns
// ErrPreferenceNotFound, exactly as a nonexistent id does.
func (s *PreferenceStore) Get(ctx context.Context, ownerID string, id uuid.UUID) (*PreferenceRecord, error) {
	rec, err := scanPreference(s.pool.QueryRow(ctx, `
		SELECT `+preferenceColumns+`
		FROM user_preferences
		WHERE owner_id = $1 AND id = $2`,
		ownerID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPreferenceNotFound
		}
		return nil, fmt.Errorf("get user_preference: %w", err)
	}
	return &rec, nil
}

// Create inserts a new record for rec.OwnerID and returns the stored row.
//
// The per-user ceiling is enforced inside the INSERT itself: the row is
// selected for insertion only while the owner holds fewer than maxPerKind
// records of that kind, so two concurrent creates cannot both slip past a
// read-then-write check. A filtered-out row surfaces as ErrPreferenceLimit;
// a dedup-key collision as ErrPreferenceDuplicate.
func (s *PreferenceStore) Create(ctx context.Context, rec PreferenceRecord, maxPerKind int) (*PreferenceRecord, error) {
	created, err := scanPreference(s.pool.QueryRow(ctx, `
		INSERT INTO user_preferences (owner_id, kind, name, cluster_id, dedup_key, schema_version, config)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE (SELECT count(*) FROM user_preferences WHERE owner_id = $1 AND kind = $2) < $8
		RETURNING `+preferenceColumns,
		rec.OwnerID, rec.Kind, rec.Name, rec.ClusterID, rec.DedupKey,
		rec.SchemaVersion, configOrEmpty(rec.Config), maxPerKind))
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// The WHERE clause filtered the row out: the owner is at the
			// ceiling for this kind.
			return nil, ErrPreferenceLimit
		case isUniqueViolation(err):
			return nil, ErrPreferenceDuplicate
		default:
			return nil, fmt.Errorf("create user_preference: %w", err)
		}
	}
	return &created, nil
}

// Update rewrites a record's mutable fields if it still carries
// expectedRevision, bumping revision and updated_at.
//
// Zero rows updated is ambiguous — wrong revision, wrong owner, or no such
// record — so it is disambiguated by an owner-scoped existence probe:
// ErrPreferenceConflict when the caller owns a record at a different
// revision, ErrPreferenceNotFound otherwise. The probe carries owner_id for
// the same reason every other statement does: without it, a guessed id
// belonging to another user would answer 409 instead of 404 and confirm that
// the record exists.
func (s *PreferenceStore) Update(ctx context.Context, ownerID string, id uuid.UUID, expectedRevision int64,
	name, dedupKey string, schemaVersion int, config json.RawMessage,
) (*PreferenceRecord, error) {
	updated, err := scanPreference(s.pool.QueryRow(ctx, `
		UPDATE user_preferences
		SET name = $4, dedup_key = $5, schema_version = $6, config = $7,
		    revision = revision + 1, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND revision = $3
		RETURNING `+preferenceColumns,
		id, ownerID, expectedRevision, name, dedupKey, schemaVersion, configOrEmpty(config)))
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, s.classifyMissingUpdate(ctx, ownerID, id)
		case isUniqueViolation(err):
			return nil, ErrPreferenceDuplicate
		default:
			return nil, fmt.Errorf("update user_preference: %w", err)
		}
	}
	return &updated, nil
}

// classifyMissingUpdate decides whether an update that matched no row was a
// revision conflict or a missing record, without disclosing anything about
// records owned by other users.
func (s *PreferenceStore) classifyMissingUpdate(ctx context.Context, ownerID string, id uuid.UUID) error {
	var exists int
	err := s.pool.QueryRow(ctx,
		`SELECT 1 FROM user_preferences WHERE id = $1 AND owner_id = $2`,
		id, ownerID).Scan(&exists)
	switch {
	case err == nil:
		return ErrPreferenceConflict
	case errors.Is(err, pgx.ErrNoRows):
		return ErrPreferenceNotFound
	default:
		return fmt.Errorf("probe user_preference: %w", err)
	}
}

// Delete removes one record. A record owned by somebody else returns
// ErrPreferenceNotFound and is left untouched.
func (s *PreferenceStore) Delete(ctx context.Context, ownerID string, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM user_preferences WHERE id = $1 AND owner_id = $2`,
		id, ownerID)
	if err != nil {
		return fmt.Errorf("delete user_preference: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPreferenceNotFound
	}
	return nil
}

// CountByKind returns how many records of one kind the owner holds, across
// all clusters. Handlers use it to report remaining quota; Create does not
// depend on it, because the ceiling is enforced inside the INSERT.
func (s *PreferenceStore) CountByKind(ctx context.Context, ownerID string, kind PreferenceKind) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM user_preferences WHERE owner_id = $1 AND kind = $2`,
		ownerID, kind).Scan(&n); err != nil {
		return 0, fmt.Errorf("count user_preferences: %w", err)
	}
	return n, nil
}
