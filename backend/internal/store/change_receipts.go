package store

import (
	"bytes"
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

// change_receipts.go — durable operation intent and receipts for tracked
// applies (Release E, U27; migration 000023).
//
// The write ordering the tracked-apply service depends on (plan D3):
//
//	Insert               -> intent recorded, no mutation yet
//	MarkMutationStarted  -> the cluster may now be touched
//	AppendObject (xN)    -> one outcome per document, append-only
//	Finalize             -> terminal state + completed_at
//
// A crash between any two of these leaves a row ReconcileOrphans can classify
// truthfully: failed when mutation_started_at is NULL, unknown otherwise.
//
// STRUCTURAL GUARANTEE: nothing in this file can hold manifest content. The
// receipt carries a sha256 digest and per-object references/outcomes only.
// TestReceiptObject_NeverCarriesManifestFields and
// TestChangeReceipt_NeverCarriesManifestFields enforce that by reflection.

var (
	// ErrReceiptExists is returned by Insert when the client-supplied operation
	// id is already present. The caller inspects the existing row to decide
	// between replay, in-flight, reuse, and cross-owner conflict.
	ErrReceiptExists = errors.New("change receipt already exists for this operation id")

	// ErrReceiptNotFound is returned when a write targets an id with no row.
	ErrReceiptNotFound = errors.New("change receipt not found")

	// ErrReceiptAlreadyFinal is returned when a write that is only legal on an
	// in-flight receipt (MarkMutationStarted, AppendObject, Finalize) targets a
	// row that already reached a terminal state, typically because
	// ReconcileOrphans reaped it. The caller must stop: continuing would let a
	// reconciled row be resurrected or mutated after being declared finished.
	ErrReceiptAlreadyFinal = errors.New("change receipt is already finalized")

	// ErrReceiptInvalid is returned for input the store rejects before any SQL.
	ErrReceiptInvalid = errors.New("invalid change receipt")
)

// ReceiptState is the change_receipts.state enum.
type ReceiptState string

const (
	ReceiptPreviewed ReceiptState = "previewed"
	ReceiptApplying  ReceiptState = "applying"
	ReceiptApplied   ReceiptState = "applied"
	ReceiptPartial   ReceiptState = "partial"
	ReceiptFailed    ReceiptState = "failed"
	ReceiptUnknown   ReceiptState = "unknown"
)

// IsTerminal reports whether s is a state a finished apply may end in.
func (s ReceiptState) IsTerminal() bool {
	switch s {
	case ReceiptApplied, ReceiptPartial, ReceiptFailed, ReceiptUnknown:
		return true
	default:
		return false
	}
}

// VerificationState is the change_receipts.verification_state enum. It
// advances independently of ReceiptState: applied + pending is legal.
type VerificationState string

const (
	VerifyPending      VerificationState = "pending"
	VerifyVerifying    VerificationState = "verifying"
	VerifyVerified     VerificationState = "verified"
	VerifyInconclusive VerificationState = "inconclusive"
	VerifyFailed       VerificationState = "verification_failed"
)

// IsValid reports whether v is a member of the CHECK set.
func (v VerificationState) IsValid() bool {
	switch v {
	case VerifyPending, VerifyVerifying, VerifyVerified, VerifyInconclusive, VerifyFailed:
		return true
	default:
		return false
	}
}

// IsFinal reports whether v is an outcome (no further verification pending),
// which is what stamps verified_at.
func (v VerificationState) IsFinal() bool {
	switch v {
	case VerifyVerified, VerifyInconclusive, VerifyFailed:
		return true
	default:
		return false
	}
}

// ReceiptObject is one per-document outcome in change_receipts.objects. It
// identifies an object and says what happened to it; it never carries content.
type ReceiptObject struct {
	Index     int    `json:"index"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
	Action    string `json:"action"`
	Error     string `json:"error,omitempty"`
	// ErrorClass is the Kubernetes reason class of a failed action
	// (conflict, forbidden, invalid, not_found, indeterminate, other). It is
	// what a Secret-bearing receipt keeps instead of Error, and what the
	// verifier reads to tell "the API server rejected it" from "the request
	// was cut off and the server may have committed it".
	ErrorClass string    `json:"errorClass,omitempty"`
	RecordedAt time.Time `json:"recordedAt"`
}

// ChangeReceipt is one row in change_receipts.
//
// Ownership and Verification are opaque JSON arrays ([]OwnershipResult and
// []diagnostics.CheckResult respectively); the store stays agnostic so it
// imports neither package. ContentDigest is "sha256:" + hex of the submitted
// bundle bytes and is the only trace of the request body.
type ChangeReceipt struct {
	ID                uuid.UUID
	OwnerID           string
	OwnerUsername     string
	ClusterID         string
	ClusterGeneration string
	ContentDigest     string
	DocumentCount     int
	Force             bool
	ContainsSecret    bool
	RepairOf          *uuid.UUID
	State             ReceiptState
	Objects           []ReceiptObject
	Ownership         json.RawMessage
	VerificationState VerificationState
	Verification      json.RawMessage
	CreatedAt         time.Time
	MutationStartedAt *time.Time
	CompletedAt       *time.Time
	VerifiedAt        *time.Time
}

// Pagination bounds for ListForOwner, mirroring audit.QueryParams. Offset
// pagination on purpose: no cursor idiom exists in this repo.
const (
	ReceiptDefaultPageSize = 50
	ReceiptMaxPageSize     = 200
)

// ReceiptQueryParams filters and paginates ListForOwner. OwnerID is required:
// the store has no unscoped list, so a caller cannot forget the owner filter.
type ReceiptQueryParams struct {
	OwnerID   string
	ClusterID string // optional exact match
	Page      int
	PageSize  int
}

// Normalize applies defaults and clamps values.
func (q *ReceiptQueryParams) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = ReceiptDefaultPageSize
	}
	if q.PageSize > ReceiptMaxPageSize {
		q.PageSize = ReceiptMaxPageSize
	}
}

// Offset returns the SQL offset for the current page.
func (q *ReceiptQueryParams) Offset() int {
	return (q.Page - 1) * q.PageSize
}

// ChangeReceiptStore handles persistence for change_receipts and reads
// change_receipt_grants.
type ChangeReceiptStore struct {
	pool *pgxpool.Pool
}

// NewChangeReceiptStore creates a receipt store backed by PostgreSQL. It never
// touches the pool, so it is safe to construct with a nil pool; callers
// nil-guard the store itself.
func NewChangeReceiptStore(pool *pgxpool.Pool) *ChangeReceiptStore {
	return &ChangeReceiptStore{pool: pool}
}

// receiptColumns is the column list shared by every read, in scan order.
const receiptColumns = `id, owner_id, owner_username, cluster_id, cluster_generation,
		content_digest, document_count, force, contains_secret, repair_of,
		state, objects, ownership, verification_state, verification,
		created_at, mutation_started_at, completed_at, verified_at`

// Insert records the operation intent. It returns ErrReceiptExists when the
// operation id is already present (PostgreSQL 23505), which is how a retry of
// a dropped response is told apart from a new apply.
//
// The row starts as ReceiptApplying (or ReceiptPreviewed when set explicitly)
// with mutation_started_at NULL. CreatedAt, MutationStartedAt, CompletedAt and
// VerifiedAt on r are ignored: the database stamps them.
func (s *ChangeReceiptStore) Insert(ctx context.Context, r ChangeReceipt) error {
	if r.ID == uuid.Nil {
		return fmt.Errorf("%w: id is required", ErrReceiptInvalid)
	}
	if r.OwnerID == "" {
		return fmt.Errorf("%w: owner id is required", ErrReceiptInvalid)
	}
	if r.ContentDigest == "" {
		return fmt.Errorf("%w: content digest is required", ErrReceiptInvalid)
	}
	if r.DocumentCount < 0 {
		return fmt.Errorf("%w: document count must not be negative", ErrReceiptInvalid)
	}
	state := r.State
	if state == "" {
		state = ReceiptApplying
	}
	if state != ReceiptApplying && state != ReceiptPreviewed {
		return fmt.Errorf("%w: a receipt is inserted as applying or previewed, got %q", ErrReceiptInvalid, state)
	}
	verifyState := r.VerificationState
	if verifyState == "" {
		verifyState = VerifyPending
	}
	if !verifyState.IsValid() {
		return fmt.Errorf("%w: unknown verification state %q", ErrReceiptInvalid, verifyState)
	}
	clusterID := r.ClusterID
	if clusterID == "" {
		clusterID = "local"
	}

	objects := r.Objects
	if objects == nil {
		objects = []ReceiptObject{}
	}
	objectsJSON, err := json.Marshal(objects)
	if err != nil {
		return fmt.Errorf("marshal receipt objects: %w", err)
	}
	ownership, err := jsonArrayOrEmpty(r.Ownership, "ownership")
	if err != nil {
		return err
	}
	verification, err := jsonArrayOrEmpty(r.Verification, "verification")
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO change_receipts (
			id, owner_id, owner_username, cluster_id, cluster_generation,
			content_digest, document_count, force, contains_secret, repair_of,
			state, objects, ownership, verification_state, verification
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		r.ID, r.OwnerID, r.OwnerUsername, clusterID, r.ClusterGeneration,
		r.ContentDigest, r.DocumentCount, r.Force, r.ContainsSecret, r.RepairOf,
		string(state), objectsJSON, ownership, string(verifyState), verification)
	if err != nil {
		var pgErr *pgconn.PgError
		// 23505 = unique_violation. The primary key is the only unique
		// constraint on the table, so this is exactly "operation id reused".
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrReceiptExists
		}
		return fmt.Errorf("insert change_receipts: %w", err)
	}
	return nil
}

// MarkMutationStarted stamps mutation_started_at exactly once, and only on an
// in-flight row. It is idempotent: a second call on a row that is already
// stamped and still in flight returns nil without moving the timestamp.
//
// It returns ErrReceiptNotFound when no such row exists and
// ErrReceiptAlreadyFinal when the row is terminal. The latter is what stops a
// reconciled-to-failed row ("never started") from being stamped and then
// applied: the caller must abort.
func (s *ChangeReceiptStore) MarkMutationStarted(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE change_receipts
		   SET mutation_started_at = NOW()
		 WHERE id = $1 AND mutation_started_at IS NULL AND completed_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("mark change receipt mutation started: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	exists, final, err := s.receiptStatus(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case !exists:
		return ErrReceiptNotFound
	case final:
		return ErrReceiptAlreadyFinal
	default:
		return nil // already stamped and still in flight: idempotent
	}
}

// AppendObject appends one per-document outcome to the receipt. Append-only:
// an outcome is never rewritten, so a crash leaves a truthful ordered prefix.
// Callers supply Index; RecordedAt defaults to now when zero.
//
// It returns ErrReceiptNotFound when no such row exists and
// ErrReceiptAlreadyFinal when the row is terminal, so a receipt that was
// reaped or finalized is never modified afterwards and the applier stops.
func (s *ChangeReceiptStore) AppendObject(ctx context.Context, id uuid.UUID, o ReceiptObject) error {
	if o.RecordedAt.IsZero() {
		o.RecordedAt = time.Now().UTC()
	}
	payload, err := json.Marshal([]ReceiptObject{o})
	if err != nil {
		return fmt.Errorf("marshal receipt object: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE change_receipts
		   SET objects = objects || $2::jsonb
		 WHERE id = $1 AND completed_at IS NULL`, id, payload)
	if err != nil {
		return fmt.Errorf("append change receipt object: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return s.noRowsError(ctx, id)
}

// Finalize sets the terminal state and completed_at. Guarded on
// completed_at IS NULL so a row already reconciled (or finalized) is never
// finalized again; that case returns ErrReceiptAlreadyFinal. state must be
// terminal (applied, partial, failed or unknown).
func (s *ChangeReceiptStore) Finalize(ctx context.Context, id uuid.UUID, state ReceiptState) error {
	if !state.IsTerminal() {
		return fmt.Errorf("%w: %q is not a terminal state", ErrReceiptInvalid, state)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE change_receipts
		   SET state = $2, completed_at = NOW()
		 WHERE id = $1 AND completed_at IS NULL`, id, string(state))
	if err != nil {
		return fmt.Errorf("finalize change receipt: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return s.noRowsError(ctx, id)
}

// SetVerification records the verification state and its evidence. payload is
// a JSON array (nil, empty and null store []). verified_at is stamped when
// state is an outcome (verified, inconclusive or verification_failed) and left
// alone for pending/verifying.
//
// A final verdict is never replaced: ANY write (final or not) against a row
// whose verification_state is already final returns ErrReceiptAlreadyFinal
// and changes nothing. The guard is in the UPDATE's WHERE clause, so two
// pollers that both compute a final verdict cannot race it: exactly one write
// lands and the other is told the row was already final. That is also what
// stops a slow verifier from overwriting the "inconclusive" that
// reconciliation recorded. The caller treats ErrReceiptAlreadyFinal as "read
// the stored verdict back", not as a failure. Returns ErrReceiptNotFound for
// an unknown id.
func (s *ChangeReceiptStore) SetVerification(
	ctx context.Context, id uuid.UUID, state VerificationState, payload json.RawMessage,
) error {
	if !state.IsValid() {
		return fmt.Errorf("%w: unknown verification state %q", ErrReceiptInvalid, state)
	}
	verification, err := jsonArrayOrEmpty(payload, "verification")
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE change_receipts
		   SET verification_state = $2,
		       verification       = $3::jsonb,
		       verified_at        = CASE WHEN $4::boolean THEN NOW() ELSE verified_at END
		 WHERE id = $1
		   AND verification_state NOT IN ('verified', 'inconclusive', 'verification_failed')`,
		id, string(state), verification, state.IsFinal())
	if err != nil {
		return fmt.Errorf("set change receipt verification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.noRowsError(ctx, id)
	}
	return nil
}

// SetOwnership stores the preview-time ownership snapshot (a JSON array).
// Returns ErrReceiptNotFound for an unknown id.
func (s *ChangeReceiptStore) SetOwnership(ctx context.Context, id uuid.UUID, payload json.RawMessage) error {
	ownership, err := jsonArrayOrEmpty(payload, "ownership")
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE change_receipts SET ownership = $2::jsonb WHERE id = $1`, id, ownership)
	if err != nil {
		return fmt.Errorf("set change receipt ownership: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrReceiptNotFound
	}
	return nil
}

// Get returns one receipt by id, or (nil, nil) when it does not exist.
// Authorization (owner, grant, admin, per-object RBAC) is the caller's job.
func (s *ChangeReceiptStore) Get(ctx context.Context, id uuid.UUID) (*ChangeReceipt, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+receiptColumns+` FROM change_receipts WHERE id = $1`, id)
	r, err := scanReceipt(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// ListForOwner returns the receipts OWNED by p.OwnerID, newest first, plus the
// total number of matching rows. It does not include receipts shared through
// change_receipt_grants; use GrantsFor to inspect a single receipt's grants.
// p.OwnerID is required.
func (s *ChangeReceiptStore) ListForOwner(
	ctx context.Context, p ReceiptQueryParams,
) ([]ChangeReceipt, int, error) {
	if p.OwnerID == "" {
		return nil, 0, fmt.Errorf("%w: owner id is required", ErrReceiptInvalid)
	}
	p.Normalize()

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM change_receipts
		 WHERE owner_id = $1 AND ($2 = '' OR cluster_id = $2)`,
		p.OwnerID, p.ClusterID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count change_receipts: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+receiptColumns+`
		  FROM change_receipts
		 WHERE owner_id = $1 AND ($2 = '' OR cluster_id = $2)
		 ORDER BY created_at DESC, id DESC
		 LIMIT $3 OFFSET $4`,
		p.OwnerID, p.ClusterID, p.PageSize, p.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("list change_receipts: %w", err)
	}
	defer rows.Close()

	// No capacity hint: a page is at most ReceiptMaxPageSize rows, so
	// preallocation buys nothing, and deriving a size from caller input is what
	// CodeQL go/uncontrolled-allocation-size flags even after a clamp.
	out := make([]ChangeReceipt, 0)
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list change_receipts: %w", err)
	}
	return out, total, nil
}

// GrantsFor returns the grantee ids that hold an explicit read grant on the
// receipt, in grant order. Release E ships no grant-creation API; this honours
// rows created elsewhere.
func (s *ChangeReceiptStore) GrantsFor(ctx context.Context, id uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT grantee_id FROM change_receipt_grants
		 WHERE receipt_id = $1
		 ORDER BY granted_at, grantee_id`, id)
	if err != nil {
		return nil, fmt.Errorf("list change receipt grants: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, fmt.Errorf("scan change receipt grant: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list change receipt grants: %w", err)
	}
	return out, nil
}

// ReceiptOrphanGrace is the recommended olderThan for ReconcileOrphans. A
// tracked apply is one live request bounded by the 30s BFF proxy cap, so a row
// still in flight after ten minutes belongs to a process that is gone, while a
// row younger than that may belong to a live request on another pod (a rolling
// update overlaps old and new pods). The margin is deliberately generous: the
// cost of reaping late is a stale "applying" row for a few minutes, the cost of
// reaping early is aborting a live apply mid-bundle.
const ReceiptOrphanGrace = 10 * time.Minute

// ReconcileOrphans closes receipts whose owning process died: rows with
// state = 'applying', completed_at IS NULL and created_at older than
// olderThan. They become failed when the mutation never started
// (mutation_started_at IS NULL) or unknown when it had, with
// verification_state = 'inconclusive' and verified_at stamped (the same rule
// SetVerification applies to a final verification state). It NEVER replays
// anything. Returns the number of rows touched. olderThan must be positive;
// pass ReceiptOrphanGrace.
//
// Only 'applying' rows are reaped. A 'previewed' row is not an orphaned
// mutation (nothing was ever going to be applied by it); the retention sweep
// (Cleanup) removes those. Rows that already have completed_at are never
// touched.
//
// Safety under rolling updates: because a row younger than olderThan is never
// reaped, a new pod's boot-time call cannot kill the old pod's in-flight apply.
// The remaining assumption is that no apply legitimately runs longer than
// olderThan. The caller should run this at boot AND on the periodic retention
// tick, so a row too young to reap at boot is still reaped on a later tick.
func (s *ChangeReceiptStore) ReconcileOrphans(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		return 0, fmt.Errorf("%w: reconcile age bound must be positive, got %s", ErrReceiptInvalid, olderThan)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE change_receipts
		   SET state = CASE WHEN mutation_started_at IS NULL THEN 'failed' ELSE 'unknown' END,
		       verification_state = 'inconclusive',
		       verified_at = NOW(),
		       completed_at = NOW()
		 WHERE completed_at IS NULL
		   AND state = 'applying'
		   AND created_at < NOW() - make_interval(secs => $1)`, olderThan.Seconds())
	if err != nil {
		return 0, fmt.Errorf("reconcile change receipt orphans: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Cleanup deletes receipts created more than retentionDays ago, bounded by
// cleanupTimeout. change_receipt_grants rows go with them (ON DELETE CASCADE).
// Retention is on created_at, so an old row that never finalized is also
// removed. It rejects retentionDays < 1 before issuing any SQL.
func (s *ChangeReceiptStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	tag, err := s.pool.Exec(cleanupCtx, `
		DELETE FROM change_receipts
		 WHERE created_at < NOW() - $1 * INTERVAL '1 day'`, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("cleanup change_receipts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// receiptStatus reports whether a row exists and whether it is terminal.
func (s *ChangeReceiptStore) receiptStatus(ctx context.Context, id uuid.UUID) (exists, final bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT completed_at IS NOT NULL FROM change_receipts WHERE id = $1`, id).Scan(&final)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read change receipt status: %w", err)
	}
	return true, final, nil
}

// noRowsError explains a guarded UPDATE that matched nothing.
func (s *ChangeReceiptStore) noRowsError(ctx context.Context, id uuid.UUID) error {
	exists, _, err := s.receiptStatus(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrReceiptNotFound
	}
	return ErrReceiptAlreadyFinal
}

// jsonArrayOrEmpty normalizes an opaque JSON array column value. Surrounding
// whitespace is trimmed; empty input and the JSON literal null become "[]".
// Anything else must be a valid JSON array (first byte '['); objects, strings
// and scalars are rejected with ErrReceiptInvalid so the column's shape cannot
// drift.
func jsonArrayOrEmpty(raw json.RawMessage, field string) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return []byte("[]"), nil
	}
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("%w: %s must be a JSON array", ErrReceiptInvalid, field)
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("%w: %s is not valid JSON", ErrReceiptInvalid, field)
	}
	return trimmed, nil
}

// scanReceipt centralizes the row-scan boilerplate shared by Get and
// ListForOwner. It satisfies both pgx.Row and pgx.Rows.
func scanReceipt(row pgx.Row) (*ChangeReceipt, error) {
	var (
		r                          ChangeReceipt
		state, verifyState         string
		objectsJSON, ownershipJSON []byte
		verificationJSON           []byte
	)
	if err := row.Scan(
		&r.ID, &r.OwnerID, &r.OwnerUsername, &r.ClusterID, &r.ClusterGeneration,
		&r.ContentDigest, &r.DocumentCount, &r.Force, &r.ContainsSecret, &r.RepairOf,
		&state, &objectsJSON, &ownershipJSON, &verifyState, &verificationJSON,
		&r.CreatedAt, &r.MutationStartedAt, &r.CompletedAt, &r.VerifiedAt,
	); err != nil {
		return nil, err
	}
	r.State = ReceiptState(state)
	r.VerificationState = VerificationState(verifyState)
	r.Ownership = json.RawMessage(ownershipJSON)
	r.Verification = json.RawMessage(verificationJSON)
	r.Objects = []ReceiptObject{}
	if len(objectsJSON) > 0 {
		if err := json.Unmarshal(objectsJSON, &r.Objects); err != nil {
			return nil, fmt.Errorf("decode change receipt objects: %w", err)
		}
	}
	return &r, nil
}
