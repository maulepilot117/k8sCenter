package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// incidents.go — persistence for incident records and their revisioned notes
// (Release D, U21a; migration 000024). Evidence and grant writes are U21b.
//
// Authorization posture. The store enforces the two boundaries that must hold
// even if a handler check is later dropped, in SQL rather than only in Go:
//
//   - Update and Delete carry the acting owner in their WHERE clause, so a
//     non-owner can never mutate an incident.
//   - Note mutations are bound to (incident_id, id) and to the note's author,
//     so an identity authorized on incident A can never reach a note of
//     incident B by guessing its UUID, and nobody but the author can edit or
//     delete a note.
//
// Everything else in the Q1 policy (P1 visibility for reads, P3 collaborator
// ceilings, P4 per-item re-authorization) is the HTTP layer's job. Every
// mutation takes the acting identity as an explicit parameter, never as a
// field a handler might populate from a request body.
//
// Identity columns (owner_id, author_id, grantee_id) hold auth.User.ID strings:
// provider-qualified TEXT, not local_users foreign keys.

var (
	// ErrIncidentInvalid is returned for input the store rejects before any SQL.
	ErrIncidentInvalid = errors.New("invalid incident input")

	// ErrIncidentNotFound is returned when a mutation targets an incident id
	// that does not exist.
	ErrIncidentNotFound = errors.New("incident not found")

	// ErrNotOwner is returned when an incident exists but the acting identity
	// is not its owner. The HTTP layer decides whether to disclose that
	// (Q1 P1 answers a non-visible incident with 404).
	ErrNotOwner = errors.New("not the incident owner")

	// ErrNoteNotFound is returned when no note with that id exists in that
	// incident. A note that exists under a different incident is reported the
	// same way: it is not addressable through this one.
	ErrNoteNotFound = errors.New("incident note not found")

	// ErrNotNoteAuthor is returned when a note exists in the incident but the
	// acting identity did not write it. Edit and delete are author-only.
	ErrNotNoteAuthor = errors.New("not the note author")

	// ErrNoteRevisionConflict matches (errors.Is) every *NoteRevisionConflictError.
	ErrNoteRevisionConflict = errors.New("note revision conflict")

	// ErrInvalidIncidentCursor reports a ListVisible cursor that does not
	// decode. Callers answer it with a 400, never by restarting at page one.
	ErrInvalidIncidentCursor = errors.New("invalid incident cursor")

	// ErrInvalidNoteCursor reports a ListNotes cursor that does not decode.
	ErrInvalidNoteCursor = errors.New("invalid incident note cursor")
)

// NoteRevisionConflictError is returned by UpdateNote when the caller's
// expected revision is stale. Current is the note's revision at the time of
// the attempt, which the API returns as extra.currentRevision with
// 409 note_revision_conflict.
type NoteRevisionConflictError struct {
	Current int
}

func (e *NoteRevisionConflictError) Error() string {
	return fmt.Sprintf("note revision conflict: current revision is %d", e.Current)
}

// Is makes errors.Is(err, ErrNoteRevisionConflict) hold.
func (e *NoteRevisionConflictError) Is(target error) bool {
	return target == ErrNoteRevisionConflict
}

// Incident status values (the incidents.status CHECK set).
const (
	IncidentStatusOpen   = "open"
	IncidentStatusClosed = "closed"
)

// Bounds mirrored from the 000024 CHECK constraints, plus the retention range
// of plan P13. Character counts are runes, matching PostgreSQL length() on a
// UTF-8 database.
const (
	IncidentMaxTitleChars    = 200
	IncidentMaxSummaryChars  = 10000
	IncidentMaxNoteBodyChars = 20000
	IncidentMinRetentionDays = 1
	IncidentMaxRetentionDays = 3650

	// IncidentDefaultPageSize and IncidentMaxPageSize bound ListVisible and
	// ListNotes, the same bounds change_receipts and eso_history use.
	IncidentDefaultPageSize = 50
	IncidentMaxPageSize     = 200
)

// IncidentRow is one row in incidents.
type IncidentRow struct {
	ID                     uuid.UUID
	OwnerID                string
	ClusterID              string
	Title                  string
	Summary                string
	Status                 string
	WindowStart            time.Time
	WindowEnd              *time.Time
	EvidenceBytes          int64
	EvidenceCount          int
	ScopeCount             int
	RetentionDaysAtCapture int
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ClosedAt               *time.Time
}

// IncidentNoteRow is one row in incident_notes.
type IncidentNoteRow struct {
	ID         uuid.UUID
	IncidentID uuid.UUID
	AuthorID   string
	Body       string
	Revision   int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// IncidentNoteRevisionRow is one prior body in incident_note_revisions.
// Revision is the revision that body had before it was replaced.
type IncidentNoteRevisionRow struct {
	NoteID    uuid.UUID
	Revision  int
	AuthorID  string
	Body      string
	CreatedAt time.Time
}

// ---------------------------------------------------------------------------
// Pure validation (runs before any SQL)
// ---------------------------------------------------------------------------

// validateIncidentText checks a text value against [minChars, maxChars]
// characters. PostgreSQL rejects invalid UTF-8 and NUL in TEXT outright, so
// both are refused here as input errors rather than surfacing as a DB fault.
func validateIncidentText(field, s string, minChars, maxChars int) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrIncidentInvalid, field)
	}
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("%w: %s contains a NUL character", ErrIncidentInvalid, field)
	}
	n := utf8.RuneCountInString(s)
	if n < minChars || n > maxChars {
		return fmt.Errorf("%w: %s must be %d to %d characters, got %d", ErrIncidentInvalid, field, minChars, maxChars, n)
	}
	return nil
}

// ValidateIncidentTitle enforces 1..200 characters.
func ValidateIncidentTitle(title string) error {
	return validateIncidentText("title", title, 1, IncidentMaxTitleChars)
}

// ValidateIncidentSummary enforces at most 10000 characters (empty is allowed).
func ValidateIncidentSummary(summary string) error {
	return validateIncidentText("summary", summary, 0, IncidentMaxSummaryChars)
}

// ValidateIncidentNoteBody enforces 1..20000 characters.
func ValidateIncidentNoteBody(body string) error {
	return validateIncidentText("note body", body, 1, IncidentMaxNoteBodyChars)
}

// ValidateIncidentWindow requires a window start and, when an end is given,
// end >= start.
func ValidateIncidentWindow(start time.Time, end *time.Time) error {
	if start.IsZero() {
		return fmt.Errorf("%w: window start is required", ErrIncidentInvalid)
	}
	if end != nil && end.Before(start) {
		return fmt.Errorf("%w: window end is before window start", ErrIncidentInvalid)
	}
	return nil
}

// ValidateIncidentStatus accepts exactly "open" or "closed".
func ValidateIncidentStatus(status string) error {
	switch status {
	case IncidentStatusOpen, IncidentStatusClosed:
		return nil
	default:
		return fmt.Errorf("%w: unknown status %q", ErrIncidentInvalid, status)
	}
}

// ClampIncidentRetentionDays forces a configured retention into
// [IncidentMinRetentionDays, IncidentMaxRetentionDays]. The store never reads
// configuration; the config layer (U25a) clamps with this and the caller
// passes the result to Create and Cleanup.
func ClampIncidentRetentionDays(days int) int {
	return max(IncidentMinRetentionDays, min(days, IncidentMaxRetentionDays))
}

// ValidateIncidentRetentionDays rejects a retention outside
// [IncidentMinRetentionDays, IncidentMaxRetentionDays].
func ValidateIncidentRetentionDays(days int) error {
	if days < IncidentMinRetentionDays || days > IncidentMaxRetentionDays {
		return fmt.Errorf("%w: retention days must be %d to %d, got %d",
			ErrIncidentInvalid, IncidentMinRetentionDays, IncidentMaxRetentionDays, days)
	}
	return nil
}

func requireIdentity(field, id string) error {
	if id == "" {
		return fmt.Errorf("%w: %s is required", ErrIncidentInvalid, field)
	}
	return nil
}

// clampIncidentPageSize: zero or negative means the default, anything larger
// than the maximum is clamped.
func clampIncidentPageSize(limit int) int {
	switch {
	case limit < 1:
		return IncidentDefaultPageSize
	case limit > IncidentMaxPageSize:
		return IncidentMaxPageSize
	default:
		return limit
	}
}

// ---------------------------------------------------------------------------
// Cursor
// ---------------------------------------------------------------------------

// IncidentCursor is the keyset position after the last row of a ListVisible
// page: that row's (created_at, id). id breaks created_at ties.
type IncidentCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// maxIncidentCursorBytes caps the decoded cursor: the longest valid one
// ("<16 digits>:<36-char uuid>") is well under it. The timestamp bound is the
// shared maxCursorMicros.
const maxIncidentCursorBytes = 80

// EncodeIncidentCursor renders a cursor in the shared keyset form
// (encodeMicrosCursor) over "<created_at unix microseconds>:<id>".
//
// The cursor is deliberately not signed: it carries no authority. ListVisible
// pins the caller's identity in its WHERE clause, so a forged cursor can only
// move the caller's window among incidents they can already list.
func EncodeIncidentCursor(c IncidentCursor) string {
	return encodeMicrosCursor(c.CreatedAt, c.ID.String())
}

// DecodeIncidentCursor parses a cursor produced by EncodeIncidentCursor. The
// id must be a non-nil UUID in canonical lowercase form. Every malformed input
// returns ErrInvalidIncidentCursor.
func DecodeIncidentCursor(s string) (IncidentCursor, error) {
	at, idText, ok := decodeMicrosCursor(s, maxIncidentCursorBytes)
	if !ok {
		return IncidentCursor{}, ErrInvalidIncidentCursor
	}
	id, err := uuid.Parse(idText)
	if err != nil || id == uuid.Nil || id.String() != idText {
		return IncidentCursor{}, ErrInvalidIncidentCursor
	}
	return IncidentCursor{CreatedAt: at, ID: id}, nil
}

// IncidentNoteCursor is the keyset position after the last row of a
// ListNotes page: that row's (created_at, id). Notes page oldest first, so
// the next page starts strictly after it.
type IncidentNoteCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// EncodeIncidentNoteCursor renders a note cursor in the shared keyset form.
// Unsigned, like the incident cursor: ListNotes pins the incident in its
// WHERE clause and the caller has already passed the visibility gate.
func EncodeIncidentNoteCursor(c IncidentNoteCursor) string {
	return encodeMicrosCursor(c.CreatedAt, c.ID.String())
}

// DecodeIncidentNoteCursor parses a cursor produced by
// EncodeIncidentNoteCursor; every malformed input returns ErrInvalidNoteCursor.
func DecodeIncidentNoteCursor(s string) (IncidentNoteCursor, error) {
	at, idText, ok := decodeMicrosCursor(s, maxIncidentCursorBytes)
	if !ok {
		return IncidentNoteCursor{}, ErrInvalidNoteCursor
	}
	id, err := uuid.Parse(idText)
	if err != nil || id == uuid.Nil || id.String() != idText {
		return IncidentNoteCursor{}, ErrInvalidNoteCursor
	}
	return IncidentNoteCursor{CreatedAt: at, ID: id}, nil
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// IncidentStore handles persistence for incidents, incident_notes and
// incident_note_revisions.
type IncidentStore struct {
	pool *pgxpool.Pool
}

// NewIncidentStore creates an incident store backed by PostgreSQL. It never
// touches the pool, so it is safe to construct with a nil pool; callers
// nil-guard the store itself.
func NewIncidentStore(pool *pgxpool.Pool) *IncidentStore {
	return &IncidentStore{pool: pool}
}

// incidentRollbackTimeout bounds rollbackDetached.
const incidentRollbackTimeout = 5 * time.Second

// rollbackDetached rolls tx back for a deferred cleanup; after Commit it is a
// no-op. It runs detached from ctx's cancellation, because a cancelled caller
// must still release its row locks, but bounded by incidentRollbackTimeout so
// a wedged connection cannot hold the goroutine. Every incident-store
// transaction defers it.
func rollbackDetached(ctx context.Context, tx pgx.Tx) {
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), incidentRollbackTimeout)
	defer cancel()
	_ = tx.Rollback(rbCtx)
}

// incidentColumns is the SELECT list every incident reader scans with
// scanIncident, in scan order.
const incidentColumns = `
	id, owner_id, cluster_id, title, summary, status, window_start, window_end,
	evidence_bytes, evidence_count, scope_count, retention_days_at_capture,
	created_at, updated_at, closed_at`

func scanIncident(row pgx.Row) (IncidentRow, error) {
	var r IncidentRow
	err := row.Scan(
		&r.ID, &r.OwnerID, &r.ClusterID, &r.Title, &r.Summary, &r.Status, &r.WindowStart, &r.WindowEnd,
		&r.EvidenceBytes, &r.EvidenceCount, &r.ScopeCount, &r.RetentionDaysAtCapture,
		&r.CreatedAt, &r.UpdatedAt, &r.ClosedAt,
	)
	return r, err
}

// noteColumns is the SELECT/RETURNING list scanNote reads, in scan order.
const noteColumns = `id, incident_id, author_id, body, revision, created_at, updated_at`

func scanNote(row pgx.Row) (IncidentNoteRow, error) {
	var n IncidentNoteRow
	err := row.Scan(&n.ID, &n.IncidentID, &n.AuthorID, &n.Body, &n.Revision, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

// Create inserts a new open incident and returns its id. r.OwnerID must be
// the authenticated caller's auth.User.ID, set by the caller from the auth
// context. ID, Status (must be empty or "open"), the evidence counters and the
// timestamps on r are ignored or defaulted: the database assigns them. An empty
// ClusterID defaults to "local". RetentionDaysAtCapture is the retention in
// force at creation, supplied by the caller; it must be within [1, 3650].
func (s *IncidentStore) Create(ctx context.Context, r IncidentRow) (uuid.UUID, error) {
	if err := requireIdentity("owner id", r.OwnerID); err != nil {
		return uuid.Nil, err
	}
	if err := ValidateIncidentTitle(r.Title); err != nil {
		return uuid.Nil, err
	}
	if err := ValidateIncidentSummary(r.Summary); err != nil {
		return uuid.Nil, err
	}
	if err := ValidateIncidentWindow(r.WindowStart, r.WindowEnd); err != nil {
		return uuid.Nil, err
	}
	if err := ValidateIncidentRetentionDays(r.RetentionDaysAtCapture); err != nil {
		return uuid.Nil, err
	}
	if r.Status != "" && r.Status != IncidentStatusOpen {
		return uuid.Nil, fmt.Errorf("%w: an incident is created open, got status %q", ErrIncidentInvalid, r.Status)
	}
	clusterID := r.ClusterID
	if clusterID == "" {
		clusterID = "local"
	}

	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO incidents (owner_id, cluster_id, title, summary, window_start, window_end, retention_days_at_capture)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		r.OwnerID, clusterID, r.Title, r.Summary, r.WindowStart, r.WindowEnd, r.RetentionDaysAtCapture,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert incidents: %w", err)
	}
	return id, nil
}

// Get returns one incident by id, or (nil, nil) when it does not exist. A
// database fault is (nil, err), never "not found". Visibility (owner or grant,
// Q1 P1) is the caller's job.
func (s *IncidentStore) Get(ctx context.Context, id uuid.UUID) (*IncidentRow, error) {
	r, err := scanIncident(s.pool.QueryRow(ctx, `SELECT`+incidentColumns+` FROM incidents WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get incidents: %w", err)
	}
	return &r, nil
}

// The two ListVisible page queries. As in eso_history.go they are two fixed
// SQL texts rather than one with "$2 IS NULL OR (...)", so the keyset
// predicate stays an index condition under PostgreSQL's generic plans.
const (
	incidentVisibleWhere = `
		WHERE (owner_id = $1
		       OR EXISTS (SELECT 1 FROM incident_grants g
		                   WHERE g.incident_id = incidents.id AND g.grantee_id = $1))`

	incidentFirstPageSQL = `
		SELECT` + incidentColumns + `
		FROM incidents` + incidentVisibleWhere + `
		ORDER BY created_at DESC, id DESC
		LIMIT $2`

	incidentNextPageSQL = `
		SELECT` + incidentColumns + `
		FROM incidents` + incidentVisibleWhere + `
		  AND (created_at, id) < ($2::timestamptz, $3::uuid)
		ORDER BY created_at DESC, id DESC
		LIMIT $4`
)

// ListVisible returns the incidents userID owns or holds a grant on, newest
// first ((created_at DESC, id DESC)), starting after cursor ("" for the first
// page). limit is clamped to [1, IncidentMaxPageSize] (non-positive means
// IncidentDefaultPageSize). The returned cursor is "" when there is no further
// page; a full final page costs the client one extra, empty request.
//
// This is record visibility only (Q1 P1). Per-item evidence filtering (P4) and
// visible counts (P10) are the HTTP layer's job.
func (s *IncidentStore) ListVisible(ctx context.Context, userID string, limit int, cursor string) ([]IncidentRow, string, error) {
	if err := requireIdentity("user id", userID); err != nil {
		return nil, "", err
	}
	limit = clampIncidentPageSize(limit)

	var (
		rows pgx.Rows
		err  error
	)
	if cursor == "" {
		rows, err = s.pool.Query(ctx, incidentFirstPageSQL, userID, limit)
	} else {
		after, decodeErr := DecodeIncidentCursor(cursor)
		if decodeErr != nil {
			return nil, "", decodeErr
		}
		rows, err = s.pool.Query(ctx, incidentNextPageSQL, userID, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	// No capacity hint from caller input (CodeQL go/uncontrolled-allocation-size).
	out := make([]IncidentRow, 0)
	for rows.Next() {
		r, err := scanIncident(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan incidents: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate incidents: %w", err)
	}

	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = EncodeIncidentCursor(IncidentCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return out, next, nil
}

// Update sets the title, summary and/or status of an incident owned by
// ownerID and bumps updated_at. A nil field keeps its column: the merge is
// COALESCE in the UPDATE itself, so two partial updates that interleave
// (one editor's title, another's summary) never revert each other the way a
// handler-side read-modify-write would. closed_at follows the RESULTING
// status: closing stamps it (an edit to an already-closed incident keeps its
// original close time); reopening clears it. A non-nil field is validated; a
// nil one is not. The owner is in the WHERE clause, so a non-owner can never
// write. Returns ErrIncidentNotFound for a missing id and ErrNotOwner when
// it exists under another owner.
func (s *IncidentStore) Update(ctx context.Context, id uuid.UUID, ownerID string, title, summary, status *string) error {
	if err := requireIdentity("owner id", ownerID); err != nil {
		return err
	}
	if title != nil {
		if err := ValidateIncidentTitle(*title); err != nil {
			return err
		}
	}
	if summary != nil {
		if err := ValidateIncidentSummary(*summary); err != nil {
			return err
		}
	}
	if status != nil {
		if err := ValidateIncidentStatus(*status); err != nil {
			return err
		}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE incidents
		   SET title      = COALESCE($3, title),
		       summary    = COALESCE($4, summary),
		       status     = COALESCE($5, status),
		       closed_at  = CASE WHEN COALESCE($5, status) = 'closed' THEN COALESCE(closed_at, NOW()) ELSE NULL END,
		       updated_at = NOW()
		 WHERE id = $1 AND owner_id = $2`,
		id, ownerID, title, summary, status)
	if err != nil {
		return fmt.Errorf("update incidents: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return s.ownershipMiss(ctx, id)
}

// Delete removes an incident owned by ownerID; evidence, notes, note revisions
// and grants go with it (ON DELETE CASCADE). Returns ErrIncidentNotFound for a
// missing id and ErrNotOwner when it exists under another owner.
func (s *IncidentStore) Delete(ctx context.Context, id uuid.UUID, ownerID string) error {
	if err := requireIdentity("owner id", ownerID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM incidents WHERE id = $1 AND owner_id = $2`, id, ownerID)
	if err != nil {
		return fmt.Errorf("delete incidents: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return s.ownershipMiss(ctx, id)
}

// ownershipMiss explains an owner-scoped write that matched nothing.
func (s *IncidentStore) ownershipMiss(ctx context.Context, id uuid.UUID) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM incidents WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("read incidents ownership: %w", err)
	}
	if !exists {
		return ErrIncidentNotFound
	}
	return ErrNotOwner
}

// Cleanup deletes incidents created more than retentionDays ago, using the
// value the caller passes (the currently configured retention, Q1 P13), and
// returns the number deleted. Evidence, notes, revisions and grants cascade.
// Lowering the configured value therefore deletes, on the next sweep,
// incidents that were inside the old window. It deletes in batches of
// incidentCleanupBatch, each its own statement, so a large backlog never holds
// one long transaction or lock; the whole call is bounded by cleanupTimeout, and
// a timeout or failure part-way returns the count already deleted along with the
// error (progress is kept). It rejects retentionDays < 1 before any SQL.
func (s *IncidentStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	return s.cleanupBatched(ctx, retentionDays, incidentCleanupBatch, nil)
}

// incidentCleanupBatch is the number of incidents one Cleanup statement deletes.
const incidentCleanupBatch = 500

// cleanupBatched is Cleanup with an injectable batch size. afterBatch, when
// non-nil, is called with each batch's deleted count and may return false to
// stop early (test seam; production passes nil).
func (s *IncidentStore) cleanupBatched(ctx context.Context, retentionDays, batchSize int, afterBatch func(deleted int64) bool) (int64, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
	}
	if batchSize < 1 {
		return 0, fmt.Errorf("cleanup batch size must be at least 1, got %d", batchSize)
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	var total int64
	for {
		tag, err := s.pool.Exec(cleanupCtx,
			`DELETE FROM incidents WHERE id IN (
			     SELECT id FROM incidents
			      WHERE created_at < NOW() - $1 * INTERVAL '1 day'
			      ORDER BY created_at
			      LIMIT $2)`, retentionDays, batchSize)
		if err != nil {
			return total, fmt.Errorf("cleanup incidents: %w", err)
		}
		n := tag.RowsAffected()
		total += n
		if afterBatch != nil && !afterBatch(n) {
			return total, nil
		}
		if n < int64(batchSize) {
			return total, nil
		}
	}
}

// RetentionLoweringImpact describes incidents a configured retention would
// delete although the retention in force when they were created would have
// kept them.
type RetentionLoweringImpact struct {
	// Count is how many such incidents exist.
	Count int64
	// Oldest and Newest bound their created_at; both are zero when Count is 0.
	Oldest, Newest time.Time
	// LaterCount is how many incidents are still inside the configured window
	// but were captured under a longer retention: they will be deleted earlier
	// than their own policy promised, once they age past the configured value.
	LaterCount int64
}

// RetentionLoweringImpact reports how a configured retention departs from the
// retention_days_at_capture of existing incidents. Count (with Oldest and
// Newest) is the incidents the next Cleanup(retentionDays) deletes only
// because the configured value is lower than the one they were created under:
// created_at is older than the configured window but still inside their own.
// LaterCount is those still inside the configured window that were captured
// under a longer retention. Incidents simply expired under their own policy
// are in neither, so both are zero in normal operation. LaterCount stays above
// zero until every such incident has aged past the configured window, so a
// caller that warns on it will warn again on later restarts. It reads
// aggregates only, no incident content.
func (s *IncidentStore) RetentionLoweringImpact(ctx context.Context, retentionDays int) (RetentionLoweringImpact, error) {
	if retentionDays < 1 {
		return RetentionLoweringImpact{}, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
	}
	var (
		impact         RetentionLoweringImpact
		oldest, newest *time.Time
	)
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE past),
		       MIN(created_at) FILTER (WHERE past),
		       MAX(created_at) FILTER (WHERE past),
		       COUNT(*) FILTER (WHERE later)
		  FROM (
		    SELECT created_at,
		           created_at <  NOW() - $1::int * INTERVAL '1 day'
		             AND created_at >= NOW() - retention_days_at_capture * INTERVAL '1 day' AS past,
		           created_at >= NOW() - $1::int * INTERVAL '1 day'
		             AND retention_days_at_capture > $1::int AS later
		      FROM incidents) AS t`,
		retentionDays).Scan(&impact.Count, &oldest, &newest, &impact.LaterCount); err != nil {
		return RetentionLoweringImpact{}, fmt.Errorf("retention lowering impact: %w", err)
	}
	if oldest != nil {
		impact.Oldest = *oldest
	}
	if newest != nil {
		impact.Newest = *newest
	}
	return impact, nil
}

// AppliedRetentionDays returns the retention (days) the last successful
// retention sweep applied, from the single-row incident_retention_state table
// (migration 000025). found is false when no sweep has recorded one yet.
func (s *IncidentStore) AppliedRetentionDays(ctx context.Context) (days int, found bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT applied_retention_days FROM incident_retention_state`).Scan(&days)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read applied retention: %w", err)
	}
	return days, true, nil
}

// RecordAppliedRetention upserts the retention a successful sweep applied. It
// rejects a value outside [IncidentMinRetentionDays, IncidentMaxRetentionDays]
// before any SQL.
func (s *IncidentStore) RecordAppliedRetention(ctx context.Context, days int) error {
	if err := ValidateIncidentRetentionDays(days); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO incident_retention_state (id, applied_retention_days, updated_at)
		VALUES (true, $1, now())
		ON CONFLICT (id) DO UPDATE
		   SET applied_retention_days = EXCLUDED.applied_retention_days, updated_at = now()`, days); err != nil {
		return fmt.Errorf("record applied retention: %w", err)
	}
	return nil
}

// CreateNote adds a note at revision 1. authorID is the authenticated caller.
// Whether the caller may annotate this incident (owner, or a grant with
// can_annotate, Q1 P3) is the caller's check. Returns ErrIncidentNotFound when
// the incident does not exist.
func (s *IncidentStore) CreateNote(ctx context.Context, incidentID uuid.UUID, authorID, body string) (IncidentNoteRow, error) {
	if err := requireIdentity("author id", authorID); err != nil {
		return IncidentNoteRow{}, err
	}
	if err := ValidateIncidentNoteBody(body); err != nil {
		return IncidentNoteRow{}, err
	}
	n, err := scanNote(s.pool.QueryRow(ctx, `
		INSERT INTO incident_notes (incident_id, author_id, body)
		VALUES ($1, $2, $3)
		RETURNING `+noteColumns, incidentID, authorID, body))
	if err != nil {
		var pgErr *pgconn.PgError
		// 23503 = foreign_key_violation: the only FK is incident_id.
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return IncidentNoteRow{}, ErrIncidentNotFound
		}
		return IncidentNoteRow{}, fmt.Errorf("insert incident_notes: %w", err)
	}
	return n, nil
}

// The two ListNotes page queries (fixed texts, as for ListVisible). Both
// fetch one row past the page so the returned cursor is exact: it is issued
// only when a further note exists.
const (
	noteFirstPageSQL = `
		SELECT ` + noteColumns + `
		  FROM incident_notes
		 WHERE incident_id = $1
		 ORDER BY created_at, id
		 LIMIT $2`

	noteNextPageSQL = `
		SELECT ` + noteColumns + `
		  FROM incident_notes
		 WHERE incident_id = $1
		   AND (created_at, id) > ($2::timestamptz, $3::uuid)
		 ORDER BY created_at, id
		 LIMIT $4`
)

// ListNotes returns one page of an incident's notes, oldest first
// ((created_at, id)), starting after cursor ("" for the first page). limit is
// clamped like ListVisible. The returned cursor is "" when no further note
// exists, so a page that ends on the last note never costs an extra request.
// The schema does not bound notes per incident; every note is reachable by
// paging.
//
// Ordering caveat: created_at is the INSERT's NOW(), which is the start of
// its transaction. A note whose transaction commits late (it waited on a
// lock) can therefore land BEHIND a cursor a forward pager has already
// passed, and that pager will not see it until it restarts from the first
// page. Accepted for Release D (notes are rare, and a reload shows them); a
// monotonic BIGSERIAL ordering column would close it and needs a migration.
func (s *IncidentStore) ListNotes(ctx context.Context, incidentID uuid.UUID, limit int, cursor string) ([]IncidentNoteRow, string, error) {
	limit = clampIncidentPageSize(limit)
	var (
		rows pgx.Rows
		err  error
	)
	if cursor == "" {
		rows, err = s.pool.Query(ctx, noteFirstPageSQL, incidentID, limit+1)
	} else {
		after, decodeErr := DecodeIncidentNoteCursor(cursor)
		if decodeErr != nil {
			return nil, "", decodeErr
		}
		rows, err = s.pool.Query(ctx, noteNextPageSQL, incidentID, after.CreatedAt, after.ID, limit+1)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list incident_notes: %w", err)
	}
	defer rows.Close()

	out := make([]IncidentNoteRow, 0)
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan incident_notes: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate incident_notes: %w", err)
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = EncodeIncidentNoteCursor(IncidentNoteCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return out, next, nil
}

// ListNoteRevisions returns the prior bodies of a note, oldest revision first.
// The note must belong to incidentID; a note from another incident yields an
// empty list, the same as a note with no edits.
func (s *IncidentStore) ListNoteRevisions(ctx context.Context, incidentID, noteID uuid.UUID) ([]IncidentNoteRevisionRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.note_id, r.revision, r.author_id, r.body, r.created_at
		  FROM incident_note_revisions r
		  JOIN incident_notes n ON n.id = r.note_id
		 WHERE n.incident_id = $1 AND r.note_id = $2
		 ORDER BY r.revision`, incidentID, noteID)
	if err != nil {
		return nil, fmt.Errorf("list incident_note_revisions: %w", err)
	}
	defer rows.Close()

	out := make([]IncidentNoteRevisionRow, 0)
	for rows.Next() {
		var r IncidentNoteRevisionRow
		if err := rows.Scan(&r.NoteID, &r.Revision, &r.AuthorID, &r.Body, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan incident_note_revisions: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident_note_revisions: %w", err)
	}
	return out, nil
}

// UpdateNote replaces a note's body when authorID wrote it and
// expectedRevision is still current, in one transaction: lock the note row
// (scoped by incident), check author then revision, append the prior body to
// incident_note_revisions, and write the new body at revision+1.
//
// Errors: ErrNoteNotFound when no such note exists in this incident;
// ErrNotNoteAuthor when someone else wrote it (checked before the revision,
// so a non-author never learns it); *NoteRevisionConflictError (matching
// ErrNoteRevisionConflict) carrying the current revision when expectedRevision
// is stale. Any failure leaves the note and its history unchanged.
func (s *IncidentStore) UpdateNote(
	ctx context.Context, incidentID, noteID uuid.UUID, authorID, body string, expectedRevision int,
) (IncidentNoteRow, error) {
	if err := requireIdentity("author id", authorID); err != nil {
		return IncidentNoteRow{}, err
	}
	if err := ValidateIncidentNoteBody(body); err != nil {
		return IncidentNoteRow{}, err
	}
	if expectedRevision < 1 {
		return IncidentNoteRow{}, fmt.Errorf("%w: expected revision must be at least 1, got %d", ErrIncidentInvalid, expectedRevision)
	}

	var out IncidentNoteRow
	err := s.withNoteLock(ctx, incidentID, noteID, authorID, func(tx pgx.Tx, current lockedNote) error {
		if current.revision != expectedRevision {
			return &NoteRevisionConflictError{Current: current.revision}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO incident_note_revisions (note_id, revision, author_id, body)
			VALUES ($1, $2, $3, $4)`, noteID, current.revision, current.authorID, current.body); err != nil {
			return fmt.Errorf("insert incident_note_revisions: %w", err)
		}
		n, err := scanNote(tx.QueryRow(ctx, `
			UPDATE incident_notes
			   SET body = $4, revision = revision + 1, updated_at = NOW()
			 WHERE id = $1 AND incident_id = $2 AND author_id = $3
			RETURNING `+noteColumns, noteID, incidentID, authorID, body))
		if err != nil {
			return fmt.Errorf("update incident_notes: %w", err)
		}
		out = n
		return nil
	})
	if err != nil {
		return IncidentNoteRow{}, err
	}
	return out, nil
}

// DeleteNote removes a note authorID wrote in incidentID; its revisions go
// with it (ON DELETE CASCADE). Returns ErrNoteNotFound when no such note
// exists in this incident and ErrNotNoteAuthor when someone else wrote it.
func (s *IncidentStore) DeleteNote(ctx context.Context, incidentID, noteID uuid.UUID, authorID string) error {
	if err := requireIdentity("author id", authorID); err != nil {
		return err
	}
	return s.withNoteLock(ctx, incidentID, noteID, authorID, func(tx pgx.Tx, _ lockedNote) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM incident_notes WHERE id = $1 AND incident_id = $2 AND author_id = $3`,
			noteID, incidentID, authorID); err != nil {
			return fmt.Errorf("delete incident_notes: %w", err)
		}
		return nil
	})
}

// lockedNote is the state of a note read under FOR UPDATE.
type lockedNote struct {
	authorID string
	body     string
	revision int
}

// withNoteLock runs fn in a transaction holding a row lock on the note, after
// confirming the note exists in incidentID and was written by authorID. fn's
// error rolls everything back; a nil return commits.
func (s *IncidentStore) withNoteLock(
	ctx context.Context, incidentID, noteID uuid.UUID, authorID string,
	fn func(pgx.Tx, lockedNote) error,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin incident note tx: %w", err)
	}
	defer rollbackDetached(ctx, tx)

	var current lockedNote
	err = tx.QueryRow(ctx, `
		SELECT author_id, body, revision
		  FROM incident_notes
		 WHERE id = $1 AND incident_id = $2
		   FOR UPDATE`, noteID, incidentID).Scan(&current.authorID, &current.body, &current.revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoteNotFound
	}
	if err != nil {
		return fmt.Errorf("lock incident_notes: %w", err)
	}
	if current.authorID != authorID {
		return ErrNotNoteAuthor
	}
	if err := fn(tx, current); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit incident note tx: %w", err)
	}
	return nil
}
