package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// incident_evidence.go — append-only persistence for incident evidence
// (Release D, U21b; table incident_evidence from migration 000024).
//
// Append-only (Q1 P15). The store exposes InsertBatch and reads, nothing
// else: no per-row update and no per-row delete. Evidence leaves the table
// only with its whole incident (owner delete or the retention sweep, both via
// ON DELETE CASCADE). TestEvidenceHasNoUpdatePath pins the method set.
//
// Size and scope ceilings (Q1 P5, P14). The caller passes EvidenceLimits; the
// store never reads configuration. InsertBatch enforces them inside one
// transaction that holds the incident row lock (SELECT ... FOR UPDATE), and it
// checks AFTER inserting with ON CONFLICT DO NOTHING, counting only the rows
// actually inserted. So a re-capture never inflates the running totals, a
// batch that exceeds a limit only through rows that deduplicate is accepted,
// concurrent captures on one incident serialize on the lock and can never
// together exceed a ceiling, and a rejected batch writes nothing. The SQL
// CHECKs on incidents and incident_evidence are the last backstop.
//
// Provenance (Q1 P9) is keyed on (cluster_id, source_uid), never on
// (namespace, name): a deleted-and-recreated object with a reused name has a
// new UID and inherits nothing.
//
// capture_key is opaque here. The collector (U22b) derives it from the
// observation; the store only validates it and deduplicates on
// (incident_id, capture_key).

var (
	// ErrEvidenceLimit matches (errors.Is) every *EvidenceLimitError: a byte,
	// item-count or item-size ceiling (API: 413 evidence_limit_exceeded).
	ErrEvidenceLimit = errors.New("incident evidence limit exceeded")

	// ErrScopeLimit matches every *ScopeLimitError: the distinct-scope cap
	// (Q1 P5; API: 409 scope_limit_exceeded).
	ErrScopeLimit = errors.New("incident evidence scope limit exceeded")

	// ErrIncidentClosed is returned when evidence is captured into a closed
	// incident. A closed investigation is frozen; reopen it first.
	ErrIncidentClosed = errors.New("incident is closed")

	// ErrInvalidEvidenceCursor reports a ListByIncident cursor that does not
	// decode. Callers answer it with a 400, never by restarting at page one.
	ErrInvalidEvidenceCursor = errors.New("invalid evidence cursor")

	// ErrIncidentBusy is returned when a write could not take the incident row
	// lock within its lock timeout because another transaction held it. It
	// is retryable; nothing was written.
	ErrIncidentBusy = errors.New("incident is busy")
)

// incidentLockTimeout bounds how long InsertBatch and AddGrant wait for the
// incident row lock, so a stalled holder cannot pin pool connections.
const incidentLockTimeout = 5 * time.Second

// beginIncidentLockTx begins a transaction whose lock waits give up after
// lockTimeout (SET LOCAL lock_timeout, scoped to this transaction).
func beginIncidentLockTx(ctx context.Context, pool *pgxpool.Pool, lockTimeout time.Duration, what string) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin %s tx: %w", what, err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`,
		strconv.FormatInt(lockTimeout.Milliseconds(), 10)+"ms"); err != nil {
		rollbackDetached(ctx, tx)
		return nil, fmt.Errorf("set %s lock timeout: %w", what, err)
	}
	return tx, nil
}

// busyIfLockTimeout maps PostgreSQL's lock_not_available (55P03, raised when
// lock_timeout expires) to ErrIncidentBusy and passes every other error through.
// InsertBatch and AddGrant apply it once, to the result of their whole
// transaction, so a timeout on any statement in it is reported the same way.
func busyIfLockTimeout(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
		return ErrIncidentBusy
	}
	return err
}

// The SQL CHECK ceilings in migration 000024. EvidenceLimits may never exceed
// them: a limit above a ceiling would turn a clean limit error into a CHECK
// violation.
const (
	EvidenceMaxItemBytesCeiling     = 1 << 20  // incident_evidence.payload_bytes
	EvidenceMaxIncidentBytesCeiling = 10 << 20 // incidents.evidence_bytes
	EvidenceMaxItemsCeiling         = 500      // incidents.evidence_count
	EvidenceMaxScopesCeiling        = 20       // incidents.scope_count
)

// Other evidence bounds. Character counts are runes, matching PostgreSQL
// length().
const (
	EvidenceMaxDetailChars     = 2000 // completeness_detail CHECK
	EvidenceMaxCaptureKeyChars = 128
	// evidenceMaxIdentityChars bounds the unconstrained TEXT identity columns
	// (cluster, group, resource, kind, namespace, name, uid, resourceVersion),
	// far above any Kubernetes limit.
	evidenceMaxIdentityChars = 1024
)

// Evidence enum values (the incident_evidence CHECK sets).
const (
	EvidenceKindDiagnosticCheck = "diagnostic_check"
	EvidenceKindObjectSummary   = "object_summary"
	EvidenceKindEventList       = "event_list"

	EvidenceModeSnapshot = "snapshot"
	EvidenceModeLiveLink = "live_link"

	EvidenceCompletenessComplete  = "complete"
	EvidenceCompletenessPartial   = "partial"
	EvidenceCompletenessFailed    = "failed"
	EvidenceCompletenessForbidden = "forbidden"
	EvidenceCompletenessTimedOut  = "timed_out"
)

// EvidenceLimit* name the limit an *EvidenceLimitError reports.
const (
	EvidenceLimitItemBytes     = "item_bytes"
	EvidenceLimitIncidentBytes = "incident_bytes"
	EvidenceLimitItems         = "items"
)

// EvidenceLimits are the per-capture ceilings InsertBatch enforces. Every
// field must be in [1, its SQL CHECK ceiling].
type EvidenceLimits struct {
	MaxItemBytes     int
	MaxIncidentBytes int
	MaxItems         int
	MaxScopes        int
}

// Validate rejects a non-positive limit or one above its SQL CHECK ceiling.
func (l EvidenceLimits) Validate() error {
	for _, c := range []struct {
		name       string
		v, ceiling int
	}{
		{"max item bytes", l.MaxItemBytes, EvidenceMaxItemBytesCeiling},
		{"max incident bytes", l.MaxIncidentBytes, EvidenceMaxIncidentBytesCeiling},
		{"max items", l.MaxItems, EvidenceMaxItemsCeiling},
		{"max scopes", l.MaxScopes, EvidenceMaxScopesCeiling},
	} {
		if c.v < 1 || c.v > c.ceiling {
			return fmt.Errorf("%w: evidence %s must be 1 to %d, got %d", ErrIncidentInvalid, c.name, c.ceiling, c.v)
		}
	}
	return nil
}

// EvidenceLimitError reports a byte or item ceiling. Limit is one of the
// EvidenceLimit* names. For incident_bytes and items, Current is the
// incident's committed total before the batch and Attempted the total the
// batch would have produced (counting only rows that were not duplicates).
// For item_bytes, Current is 0 and Attempted is the offending item's size.
type EvidenceLimitError struct {
	Limit     string
	Max       int64
	Current   int64
	Attempted int64
}

func (e *EvidenceLimitError) Error() string {
	return fmt.Sprintf("incident evidence limit %s exceeded: max %d, current %d, attempted %d",
		e.Limit, e.Max, e.Current, e.Attempted)
}

// Is makes errors.Is(err, ErrEvidenceLimit) hold.
func (e *EvidenceLimitError) Is(target error) bool { return target == ErrEvidenceLimit }

// ScopeLimitError reports the distinct-scope cap: Current is the incident's
// scope count before the batch, Attempted the count the batch would produce.
type ScopeLimitError struct {
	Max       int
	Current   int
	Attempted int
}

func (e *ScopeLimitError) Error() string {
	return fmt.Sprintf("incident evidence scope limit exceeded: max %d, current %d, attempted %d",
		e.Max, e.Current, e.Attempted)
}

// Is makes errors.Is(err, ErrScopeLimit) hold.
func (e *ScopeLimitError) Is(target error) bool { return target == ErrScopeLimit }

// IncidentEvidenceRow is one row in incident_evidence. Redaction and Payload
// are raw JSON. On insert, ID, IncidentID (the InsertBatch argument wins),
// CollectedAt (the database clock) and PayloadBytes (len(Payload), measured
// by the store) are ignored. A live_link row has a nil Payload.
type IncidentEvidenceRow struct {
	ID                 uuid.UUID
	IncidentID         uuid.UUID
	EvidenceKind       string
	Mode               string
	ClusterID          string
	APIGroup           string
	Resource           string
	SourceKind         string
	Namespace          string
	Name               string
	SourceUID          string
	ResourceVersion    string
	SecretDerived      bool
	SourceObservedAt   *time.Time
	CollectedAt        time.Time
	Completeness       string
	CompletenessDetail string
	Redaction          json.RawMessage
	Payload            json.RawMessage
	PayloadBytes       int
	CaptureKey         string
}

// EvidenceScope is the authorization scope of an evidence row (Q1
// definitions): the tuple the read path re-checks with CanAccessGroupResource.
type EvidenceScope struct {
	ClusterID string
	APIGroup  string
	Resource  string
	Namespace string
}

// ---------------------------------------------------------------------------
// Pure validation (runs before any SQL)
// ---------------------------------------------------------------------------

// ValidateEvidenceRow checks one row against the incident_evidence CHECKs and
// against what PostgreSQL would otherwise reject as a driver error (invalid
// UTF-8, NUL, malformed JSON). It does not check sizes against limits; that
// is InsertBatch's job.
func ValidateEvidenceRow(r IncidentEvidenceRow) error {
	switch r.EvidenceKind {
	case EvidenceKindDiagnosticCheck, EvidenceKindObjectSummary, EvidenceKindEventList:
	default:
		return fmt.Errorf("%w: unknown evidence kind %q", ErrIncidentInvalid, r.EvidenceKind)
	}
	switch r.Completeness {
	case EvidenceCompletenessComplete, EvidenceCompletenessPartial, EvidenceCompletenessFailed,
		EvidenceCompletenessForbidden, EvidenceCompletenessTimedOut:
	default:
		return fmt.Errorf("%w: unknown completeness %q", ErrIncidentInvalid, r.Completeness)
	}
	switch r.Mode {
	case EvidenceModeSnapshot:
		if err := validateEvidenceJSON("payload", r.Payload); err != nil {
			return err
		}
		if len(r.Payload) == 0 || string(bytes.TrimSpace(r.Payload)) == "null" {
			return fmt.Errorf("%w: a snapshot requires a payload", ErrIncidentInvalid)
		}
	case EvidenceModeLiveLink:
		if len(r.Payload) != 0 {
			return fmt.Errorf("%w: a live_link carries no payload", ErrIncidentInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown evidence mode %q", ErrIncidentInvalid, r.Mode)
	}
	if err := validateEvidenceRedaction(r.Redaction, r.SecretDerived); err != nil {
		return err
	}
	if err := validateIncidentText("completeness detail", r.CompletenessDetail, 0, EvidenceMaxDetailChars); err != nil {
		return err
	}
	if err := validateIncidentText("capture key", r.CaptureKey, 1, EvidenceMaxCaptureKeyChars); err != nil {
		return err
	}
	for _, f := range []struct {
		name     string
		v        string
		required bool
	}{
		{"cluster id", r.ClusterID, true},
		{"resource", r.Resource, true},
		{"api group", r.APIGroup, false},
		{"source kind", r.SourceKind, false},
		{"namespace", r.Namespace, false},
		{"name", r.Name, false},
		{"source uid", r.SourceUID, false},
		{"resource version", r.ResourceVersion, false},
	} {
		minChars := 0
		if f.required {
			minChars = 1
		}
		if err := validateIncidentText(f.name, f.v, minChars, evidenceMaxIdentityChars); err != nil {
			return err
		}
	}
	return nil
}

// redactionSecretDerivedKey is the one accepted spelling of the redaction
// metadata's secret flag.
const redactionSecretDerivedKey = "secretDerived"

// validateEvidenceRedaction checks the redaction metadata: empty (stored as
// {}) or a JSON object. The read path's Secret gate (Q1 P11) keys on the
// secret_derived column, so metadata that says "secretDerived": true under a
// false column is refused rather than stored ungated. The column may be
// stricter than the metadata, never laxer.
func validateEvidenceRedaction(raw json.RawMessage, secretDerived bool) error {
	if err := validateEvidenceJSON("redaction", raw); err != nil || len(raw) == 0 {
		return err
	}
	marked, err := redactionMarksSecretDerived(raw)
	if err != nil {
		return fmt.Errorf("%w: redaction %v", ErrIncidentInvalid, err)
	}
	if marked && !secretDerived {
		return fmt.Errorf("%w: redaction marks the item secret-derived but secret_derived is false", ErrIncidentInvalid)
	}
	return nil
}

// redactionMarksSecretDerived reads the top-level secretDerived flag of valid
// JSON with a token scan. Readers disagree on ambiguous keys: encoding/json
// matches keys case-insensitively and keeps the last, jsonb keeps every
// spelling and the last duplicate. So any top-level key that case-folds to
// secretDerived must be spelled exactly that way, appear once, and hold a
// boolean or null; anything else is an error. Nested objects are not part of
// the metadata contract and are skipped.
func redactionMarksSecretDerived(raw []byte) (bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false, errors.New("must be a JSON object")
	}
	var seen, marked bool
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return false, err
		}
		key, _ := tok.(string)
		if !strings.EqualFold(key, redactionSecretDerivedKey) {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return false, err
			}
			continue
		}
		if key != redactionSecretDerivedKey {
			return false, fmt.Errorf("spells the %s key as %q", redactionSecretDerivedKey, key)
		}
		if seen {
			return false, fmt.Errorf("repeats the %s key", redactionSecretDerivedKey)
		}
		seen = true
		var v *bool
		if err := dec.Decode(&v); err != nil {
			return false, fmt.Errorf("%s must be a boolean", redactionSecretDerivedKey)
		}
		marked = v != nil && *v
	}
	return marked, nil
}

// validateEvidenceJSON accepts an empty value (nothing to store) or valid
// UTF-8 JSON that jsonb can hold. encoding/json accepts two escapes jsonb
// rejects: \u0000, and a UTF-16 surrogate escape that is not a high surrogate
// immediately followed by a low one. Both are refused here, so the capture
// fails as a validation error instead of a driver error mid-transaction.
func validateEvidenceJSON(field string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return fmt.Errorf("%w: %s is not valid UTF-8 JSON", ErrIncidentInvalid, field)
	}
	if reason := jsonbEscapeProblem(raw); reason != "" {
		return fmt.Errorf("%w: %s contains %s", ErrIncidentInvalid, field, reason)
	}
	// encoding/json also accepts numbers PostgreSQL's numeric cannot hold.
	if reason := jsonbNumbersProblem(raw); reason != "" {
		return fmt.Errorf("%w: %s contains %s", ErrIncidentInvalid, field, reason)
	}
	return nil
}

// jsonbNumbersProblem walks valid JSON, skipping strings (a backslash inside
// one escapes the next byte), and checks every number token with
// jsonbNumberProblem. It returns the first problem, or "".
func jsonbNumbersProblem(raw []byte) string {
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(raw) && isJSONNumberByte(raw[j]) {
				j++
			}
			if reason := jsonbNumberProblem(raw[i:j]); reason != "" {
				return reason
			}
			i = j - 1
		}
	}
	return ""
}

func isJSONNumberByte(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

// PostgreSQL numeric limits, as numeric_in (PostgreSQL 17) applies them to a
// jsonb number. TestJSONBNumberCasesMatchPostgreSQL checks the model against
// a real server.
const (
	jsonbMaxExponent  = 1<<30 - 1 // INT_MAX/2: a larger exponent overflows outright
	jsonbMaxScale     = 16383     // NUMERIC_DSCALE_MAX: digits after the point
	jsonbMaxIntDigits = 131072    // (NUMERIC_WEIGHT_MAX+1) * 4 base-10000 digits
)

// jsonbNumberProblem describes why numeric cannot hold the valid JSON number
// tok, or returns "":
//   - an exponent above jsonbMaxExponent (or outside int64) overflows;
//   - the display scale, fraction digits minus the exponent (floored at 0),
//     may not exceed jsonbMaxScale, even for zero;
//   - the most significant nonzero digit's decimal position may not reach
//     jsonbMaxIntDigits.
func jsonbNumberProblem(tok []byte) string {
	s := bytes.TrimPrefix(tok, []byte("-"))
	mant, expText := s, []byte(nil)
	if i := bytes.IndexAny(s, "eE"); i >= 0 {
		mant, expText = s[:i], s[i+1:]
	}
	intPart, frac := mant, []byte(nil)
	if i := bytes.IndexByte(mant, '.'); i >= 0 {
		intPart, frac = mant[:i], mant[i+1:]
	}
	var exp int64
	if len(expText) > 0 {
		e, err := strconv.ParseInt(string(expText), 10, 64)
		if err != nil { // json.Valid leaves only out-of-range exponents here
			return "a number whose exponent is out of range"
		}
		exp = e
	}
	if exp > jsonbMaxExponent {
		return "a number whose exponent is out of range"
	}
	// exp < -jsonbMaxScale already forces the scale past the limit; checking
	// it first also keeps the subtraction below from overflowing.
	if exp < -jsonbMaxScale || int64(len(frac))-exp > jsonbMaxScale {
		return "a number with too many digits after the decimal point"
	}
	// The first nonzero digit, k digits into intPart followed by frac, sits at
	// decimal position len(intPart)-1-k+exp. Zero has no such digit.
	for k := range len(intPart) + len(frac) {
		var d byte
		if k < len(intPart) {
			d = intPart[k]
		} else {
			d = frac[k-len(intPart)]
		}
		if d != '0' {
			if int64(len(intPart)-1-k)+exp >= jsonbMaxIntDigits {
				return "a number too large for numeric"
			}
			break
		}
	}
	return ""
}

// jsonbEscapeProblem scans the escapes of valid JSON (a backslash only occurs
// inside a string, and json.Valid guarantees each escape is complete) and
// describes the first one jsonb would reject, or returns "". A "\\" escape
// consumes both bytes, so the literal text \u0000 inside a string (written
// "\\u0000") is not mistaken for the escape.
func jsonbEscapeProblem(raw []byte) string {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if raw[i+1] != 'u' {
			i++ // a two-byte escape such as \\ or \"
			continue
		}
		r := hexRune(raw[i+2 : i+6])
		switch {
		case r == 0:
			return `a \u0000 escape`
		case r >= 0xDC00 && r <= 0xDFFF:
			return "an unpaired low surrogate escape"
		case r >= 0xD800 && r <= 0xDBFF:
			next := i + 6
			if next+6 > len(raw) || raw[next] != '\\' || raw[next+1] != 'u' {
				return "an unpaired high surrogate escape"
			}
			if lo := hexRune(raw[next+2 : next+6]); lo < 0xDC00 || lo > 0xDFFF {
				return "an unpaired high surrogate escape"
			}
			i = next + 5 // past the low surrogate
		default:
			i += 5
		}
	}
	return ""
}

// hexRune decodes four hex digits that json.Valid has already checked.
func hexRune(h []byte) rune {
	var r rune
	for _, c := range h {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c - 'a' + 10)
		default:
			r |= rune(c - 'A' + 10)
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// Cursor
// ---------------------------------------------------------------------------

// EvidenceCursor is the keyset position after the last row of a
// ListByIncident page: that row's (collected_at, id).
type EvidenceCursor struct {
	CollectedAt time.Time
	ID          uuid.UUID
}

// maxEvidenceCursorBytes caps the decoded cursor ("<16 digits>:<36-char uuid>").
const maxEvidenceCursorBytes = 80

// EncodeEvidenceCursor renders a cursor in the shared keyset form
// (encodeMicrosCursor). It is unsigned and carries no authority:
// ListByIncident pins the incident in its WHERE clause, and visibility of that
// incident is the caller's check.
func EncodeEvidenceCursor(c EvidenceCursor) string {
	return encodeMicrosCursor(c.CollectedAt, c.ID.String())
}

// DecodeEvidenceCursor parses a cursor produced by EncodeEvidenceCursor. The id
// must be a non-nil UUID in canonical lowercase form. Every malformed input
// returns ErrInvalidEvidenceCursor.
func DecodeEvidenceCursor(s string) (EvidenceCursor, error) {
	at, idText, ok := decodeMicrosCursor(s, maxEvidenceCursorBytes)
	if !ok {
		return EvidenceCursor{}, ErrInvalidEvidenceCursor
	}
	id, err := uuid.Parse(idText)
	if err != nil || id == uuid.Nil || id.String() != idText {
		return EvidenceCursor{}, ErrInvalidEvidenceCursor
	}
	return EvidenceCursor{CollectedAt: at, ID: id}, nil
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// IncidentEvidenceStore handles append-only persistence for incident_evidence.
type IncidentEvidenceStore struct {
	pool        *pgxpool.Pool
	lockTimeout time.Duration // incidentLockTimeout; tests shorten it
}

// NewIncidentEvidenceStore creates an evidence store. It never touches the
// pool, so it is safe to construct with a nil pool.
func NewIncidentEvidenceStore(pool *pgxpool.Pool) *IncidentEvidenceStore {
	return &IncidentEvidenceStore{pool: pool, lockTimeout: incidentLockTimeout}
}

const (
	// evidenceLockSQL reads the incident's owner, status and running totals
	// under the row lock every capture of that incident serializes on.
	evidenceLockSQL = `
		SELECT owner_id, status, evidence_bytes, evidence_count, scope_count
		  FROM incidents
		 WHERE id = $1
		   FOR UPDATE`

	evidenceInsertSQL = `
		INSERT INTO incident_evidence (
			incident_id, evidence_kind, mode, cluster_id, api_group, resource, source_kind,
			namespace, name, source_uid, resource_version, secret_derived, source_observed_at,
			completeness, completeness_detail, redaction, payload, payload_bytes, capture_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16::jsonb, $17::jsonb, $18, $19)
		ON CONFLICT (incident_id, capture_key) DO NOTHING
		RETURNING payload_bytes`

	evidenceScopeCountSQL = `
		SELECT COUNT(DISTINCT (cluster_id, api_group, resource, namespace))
		  FROM incident_evidence
		 WHERE incident_id = $1`

	evidenceTotalsUpdateSQL = `
		UPDATE incidents
		   SET evidence_bytes = $2, evidence_count = $3, scope_count = $4, updated_at = NOW()
		 WHERE id = $1`
)

// InsertBatch appends one capture's evidence to an incident owned by ownerID
// and returns how many rows were inserted (rows whose capture_key already
// exists for the incident are skipped, not counted, and not an error).
//
// Before any SQL it validates ownerID, limits, the batch length (at most
// EvidenceMaxItemsCeiling rows) and every row, then rejects any row whose
// payload exceeds limits.MaxItemBytes with an item_bytes *EvidenceLimitError.
// The two passes are separate, so an invalid row is reported even when an
// earlier row is oversized.
//
// An empty batch returns (0, nil) without taking the lock, reading the
// incident or checking ownership. It is not an access check: callers (U23b)
// authorize the caller against the incident before capturing.
//
// payload_bytes, and so evidence_bytes, count the payload bytes as submitted
// (the collector's compact JSON), not the text jsonb renders on read, which
// can differ (jsonb normalizes whitespace and key order).
//
// Then, in one transaction holding the incident row lock (waiting at most
// incidentLockTimeout for any lock, else ErrIncidentBusy): ErrIncidentNotFound,
// ErrNotOwner (capture is owner-only, Q1 P3) or ErrIncidentClosed; insert with
// ON CONFLICT DO NOTHING; recompute distinct scopes from the table; reject
// when the new totals exceed limits, which rolls back every row; otherwise
// write the new totals and commit. Any failure leaves the incident unchanged.
//
// Error precedence, first match wins (U23b maps each to its own status):
//  1. before SQL: ErrIncidentInvalid (owner id, limits, batch length, rows),
//     then the item_bytes *EvidenceLimitError;
//  2. under the lock: ErrIncidentBusy, ErrIncidentNotFound, ErrNotOwner,
//     ErrIncidentClosed;
//  3. after the insert: items, then incident_bytes *EvidenceLimitError, then
//     *ScopeLimitError.
//
// Items and bytes are cumulative totals, so a limit lowered below the
// incident's current total blocks any batch that adds a row. The scope limit
// only blocks growth of the scope set: a batch whose rows all fall in scopes
// the incident already has is accepted even when a lowered limit is below the
// current scope count.
func (s *IncidentEvidenceStore) InsertBatch(
	ctx context.Context, incidentID uuid.UUID, ownerID string, rows []IncidentEvidenceRow, limits EvidenceLimits,
) (int, error) {
	if err := requireIdentity("owner id", ownerID); err != nil {
		return 0, err
	}
	if err := limits.Validate(); err != nil {
		return 0, err
	}
	if len(rows) > EvidenceMaxItemsCeiling {
		return 0, fmt.Errorf("%w: a capture holds at most %d items, got %d", ErrIncidentInvalid, EvidenceMaxItemsCeiling, len(rows))
	}
	for i, r := range rows {
		if err := ValidateEvidenceRow(r); err != nil {
			return 0, fmt.Errorf("evidence item %d: %w", i, err)
		}
	}
	prepared := make([]IncidentEvidenceRow, 0, len(rows))
	for _, r := range rows {
		if len(r.Payload) > limits.MaxItemBytes {
			return 0, &EvidenceLimitError{
				Limit: EvidenceLimitItemBytes, Max: int64(limits.MaxItemBytes), Attempted: int64(len(r.Payload)),
			}
		}
		r.PayloadBytes = len(r.Payload)
		if len(r.Redaction) == 0 {
			r.Redaction = json.RawMessage(`{}`)
		}
		prepared = append(prepared, r)
	}
	if len(prepared) == 0 {
		return 0, nil
	}
	n, err := s.insertValidated(ctx, incidentID, ownerID, prepared, limits)
	return n, busyIfLockTimeout(err)
}

// insertValidated is InsertBatch's transaction. rows must already be
// validated and carry PayloadBytes and a non-empty Redaction.
func (s *IncidentEvidenceStore) insertValidated(
	ctx context.Context, incidentID uuid.UUID, ownerID string, rows []IncidentEvidenceRow, limits EvidenceLimits,
) (int, error) {
	tx, err := beginIncidentLockTx(ctx, s.pool, s.lockTimeout, "incident evidence")
	if err != nil {
		return 0, err
	}
	defer rollbackDetached(ctx, tx)

	var (
		owner, status       string
		curBytes            int64
		curCount, curScopes int
	)
	err = tx.QueryRow(ctx, evidenceLockSQL, incidentID).Scan(&owner, &status, &curBytes, &curCount, &curScopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrIncidentNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lock incidents: %w", err)
	}
	if owner != ownerID {
		return 0, ErrNotOwner
	}
	if status == IncidentStatusClosed {
		return 0, ErrIncidentClosed
	}

	inserted, addedBytes, err := insertEvidenceRows(ctx, tx, incidentID, rows)
	if err != nil {
		return 0, err
	}
	if inserted == 0 {
		return 0, nil // every row was a duplicate: nothing to account
	}

	// Items and bytes are cumulative: any growth past the limit is refused,
	// even when the limit was lowered below the existing total.
	newCount := curCount + inserted
	if newCount > limits.MaxItems {
		return 0, &EvidenceLimitError{
			Limit: EvidenceLimitItems, Max: int64(limits.MaxItems), Current: int64(curCount), Attempted: int64(newCount),
		}
	}
	newBytes := curBytes + addedBytes
	if newBytes > int64(limits.MaxIncidentBytes) {
		return 0, &EvidenceLimitError{
			Limit: EvidenceLimitIncidentBytes, Max: int64(limits.MaxIncidentBytes), Current: curBytes, Attempted: newBytes,
		}
	}
	var newScopes int
	if err := tx.QueryRow(ctx, evidenceScopeCountSQL, incidentID).Scan(&newScopes); err != nil {
		return 0, fmt.Errorf("count incident_evidence scopes: %w", err)
	}
	// The scope cap bounds the access checks one read costs (Q1 P5). A batch
	// that adds no new scope adds no check, so it is refused only when it grows
	// the scope set past the limit. curScopes is the scope count before this
	// insert (incidents.scope_count, maintained under this same lock).
	if newScopes > limits.MaxScopes && newScopes > curScopes {
		return 0, &ScopeLimitError{Max: limits.MaxScopes, Current: curScopes, Attempted: newScopes}
	}

	if _, err := tx.Exec(ctx, evidenceTotalsUpdateSQL, incidentID, newBytes, newCount, newScopes); err != nil {
		return 0, fmt.Errorf("update incidents evidence totals: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit incident evidence tx: %w", err)
	}
	return inserted, nil
}

// insertEvidenceRows pipelines the inserts in one round trip and sums the rows
// that were actually inserted (a duplicate capture_key returns no row).
func insertEvidenceRows(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID, rows []IncidentEvidenceRow) (int, int64, error) {
	batch := &pgx.Batch{}
	for _, r := range rows {
		var payload any // a nil payload must reach the column as SQL NULL
		if len(r.Payload) > 0 {
			payload = string(r.Payload)
		}
		batch.Queue(evidenceInsertSQL,
			incidentID, r.EvidenceKind, r.Mode, r.ClusterID, r.APIGroup, r.Resource, r.SourceKind,
			r.Namespace, r.Name, r.SourceUID, r.ResourceVersion, r.SecretDerived, r.SourceObservedAt,
			r.Completeness, r.CompletenessDetail, string(r.Redaction), payload, r.PayloadBytes, r.CaptureKey)
	}
	br := tx.SendBatch(ctx, batch)
	var (
		inserted int
		added    int64
	)
	for range rows {
		var n int
		err := br.QueryRow().Scan(&n)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			_ = br.Close()
			return 0, 0, fmt.Errorf("insert incident_evidence: %w", err)
		}
		inserted++
		added += int64(n)
	}
	if err := br.Close(); err != nil {
		return 0, 0, fmt.Errorf("insert incident_evidence: %w", err)
	}
	return inserted, added, nil
}

// evidenceColumns is the SELECT list scanEvidence reads, in scan order.
const evidenceColumns = `
	id, incident_id, evidence_kind, mode, cluster_id, api_group, resource, source_kind,
	namespace, name, source_uid, resource_version, secret_derived, source_observed_at,
	collected_at, completeness, completeness_detail, redaction, payload, payload_bytes, capture_key`

func scanEvidence(row pgx.Row) (IncidentEvidenceRow, error) {
	var (
		r                  IncidentEvidenceRow
		redaction, payload []byte
	)
	err := row.Scan(
		&r.ID, &r.IncidentID, &r.EvidenceKind, &r.Mode, &r.ClusterID, &r.APIGroup, &r.Resource, &r.SourceKind,
		&r.Namespace, &r.Name, &r.SourceUID, &r.ResourceVersion, &r.SecretDerived, &r.SourceObservedAt,
		&r.CollectedAt, &r.Completeness, &r.CompletenessDetail, &redaction, &payload, &r.PayloadBytes, &r.CaptureKey,
	)
	if redaction != nil {
		r.Redaction = json.RawMessage(redaction)
	}
	if payload != nil {
		r.Payload = json.RawMessage(payload)
	}
	return r, err
}

func collectEvidence(rows pgx.Rows, what string) ([]IncidentEvidenceRow, error) {
	defer rows.Close()
	// No capacity hint from caller input (CodeQL go/uncontrolled-allocation-size).
	out := make([]IncidentEvidenceRow, 0)
	for rows.Next() {
		r, err := scanEvidence(rows)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", what, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", what, err)
	}
	return out, nil
}

// The two ListByIncident page queries: fixed texts rather than one with
// "$2 IS NULL OR (...)", so the keyset predicate stays an index condition on
// idx_incident_evidence_timeline under generic plans (see eso_history.go).
const (
	evidenceFirstPageSQL = `
		SELECT` + evidenceColumns + `
		  FROM incident_evidence
		 WHERE incident_id = $1
		 ORDER BY collected_at DESC, id DESC
		 LIMIT $2`

	evidenceNextPageSQL = `
		SELECT` + evidenceColumns + `
		  FROM incident_evidence
		 WHERE incident_id = $1
		   AND (collected_at, id) < ($2::timestamptz, $3::uuid)
		 ORDER BY collected_at DESC, id DESC
		 LIMIT $4`
)

// ListByIncident returns one page of an incident's evidence, newest first
// ((collected_at DESC, id DESC)), starting after cursor ("" for the first
// page). limit is clamped like ListVisible. The returned cursor is "" when
// there is no further page. Visibility of the incident (Q1 P1) and per-item
// re-authorization (P4) are the caller's job.
func (s *IncidentEvidenceStore) ListByIncident(
	ctx context.Context, incidentID uuid.UUID, limit int, cursor string,
) ([]IncidentEvidenceRow, string, error) {
	limit = clampIncidentPageSize(limit)
	var (
		rows pgx.Rows
		err  error
	)
	if cursor == "" {
		rows, err = s.pool.Query(ctx, evidenceFirstPageSQL, incidentID, limit)
	} else {
		after, decodeErr := DecodeEvidenceCursor(cursor)
		if decodeErr != nil {
			return nil, "", decodeErr
		}
		rows, err = s.pool.Query(ctx, evidenceNextPageSQL, incidentID, after.CollectedAt, after.ID, limit)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list incident_evidence: %w", err)
	}
	out, err := collectEvidence(rows, "incident_evidence")
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = EncodeEvidenceCursor(EvidenceCursor{CollectedAt: last.CollectedAt, ID: last.ID})
	}
	return out, next, nil
}

// ListAllByIncident returns all of an incident's evidence for export, in
// ListByIncident order. It is bounded by EvidenceMaxItemsCeiling, the most
// one incident can hold.
func (s *IncidentEvidenceStore) ListAllByIncident(ctx context.Context, incidentID uuid.UUID) ([]IncidentEvidenceRow, error) {
	rows, err := s.pool.Query(ctx, evidenceFirstPageSQL, incidentID, EvidenceMaxItemsCeiling)
	if err != nil {
		return nil, fmt.Errorf("list all incident_evidence: %w", err)
	}
	return collectEvidence(rows, "incident_evidence")
}

// ListBySourceUID returns an incident's evidence captured from one object,
// identified by (clusterID, sourceUID) — never by namespace and name (Q1 P9),
// so a recreated object with a reused name matches none of its
// predecessor's evidence. Newest first, bounded like ListAllByIncident. An
// empty sourceUID (weak identity) cannot be looked up.
func (s *IncidentEvidenceStore) ListBySourceUID(
	ctx context.Context, incidentID uuid.UUID, clusterID, sourceUID string,
) ([]IncidentEvidenceRow, error) {
	if err := requireIdentity("cluster id", clusterID); err != nil {
		return nil, err
	}
	if err := requireIdentity("source uid", sourceUID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT`+evidenceColumns+`
		  FROM incident_evidence
		 WHERE incident_id = $1 AND cluster_id = $2 AND source_uid = $3
		 ORDER BY collected_at DESC, id DESC
		 LIMIT $4`, incidentID, clusterID, sourceUID, EvidenceMaxItemsCeiling)
	if err != nil {
		return nil, fmt.Errorf("list incident_evidence by source: %w", err)
	}
	return collectEvidence(rows, "incident_evidence")
}

// DistinctScopes returns the deduplicated (cluster_id, api_group, resource,
// namespace) tuples of one incident's evidence, sorted by those fields in
// byte order (COLLATE "C", independent of the database locale). The read path
// runs one access check per scope (Q1 P5); capture caps them at 20.
func (s *IncidentEvidenceStore) DistinctScopes(ctx context.Context, incidentID uuid.UUID) ([]EvidenceScope, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT cluster_id COLLATE "C", api_group COLLATE "C", resource COLLATE "C", namespace COLLATE "C"
		  FROM incident_evidence
		 WHERE incident_id = $1
		 ORDER BY 1, 2, 3, 4`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("list incident_evidence scopes: %w", err)
	}
	defer rows.Close()
	out := make([]EvidenceScope, 0)
	for rows.Next() {
		var sc EvidenceScope
		if err := rows.Scan(&sc.ClusterID, &sc.APIGroup, &sc.Resource, &sc.Namespace); err != nil {
			return nil, fmt.Errorf("scan incident_evidence scopes: %w", err)
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident_evidence scopes: %w", err)
	}
	return out, nil
}
