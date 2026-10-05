package store

// incident_evidence_test.go — coverage for IncidentEvidenceStore and
// IncidentGrantStore (Release D, U21b).
//
// Pure tests (no database) cover the limit and row validation that runs
// before any SQL, the evidence cursor codec, grantee validation and the
// append-only method set. Env-gated tests (testDB, skipped without
// KUBECENTER_TEST_DATABASE_URL) cover the transactional accounting under the
// incident row lock, dedup, the owner gates, UID-keyed provenance,
// pagination, scopes and grants.
//
// Isolation contract (testdb_test.go): every incident is owned by an id from
// testOwnerID(t), and every read is scoped by that incident.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// ceilingLimits are the SQL CHECK ceilings, the widest limits InsertBatch accepts.
var ceilingLimits = EvidenceLimits{
	MaxItemBytes:     EvidenceMaxItemBytesCeiling,
	MaxIncidentBytes: EvidenceMaxIncidentBytesCeiling,
	MaxItems:         EvidenceMaxItemsCeiling,
	MaxScopes:        EvidenceMaxScopesCeiling,
}

// snapshotRow returns a valid snapshot row whose payload is exactly
// payloadBytes long (at least 8; see payloadOfSize).
func snapshotRow(captureKey string, payloadBytes int) IncidentEvidenceRow {
	return IncidentEvidenceRow{
		EvidenceKind: EvidenceKindObjectSummary,
		Mode:         EvidenceModeSnapshot,
		ClusterID:    "local",
		Resource:     "pods",
		SourceKind:   "Pod",
		Namespace:    "default",
		Name:         "web-1",
		SourceUID:    "uid-web-1",
		Completeness: EvidenceCompletenessComplete,
		Payload:      payloadOfSize(payloadBytes),
		CaptureKey:   captureKey,
	}
}

func liveLinkRow(captureKey string) IncidentEvidenceRow {
	r := snapshotRow(captureKey, 8)
	r.Mode = EvidenceModeLiveLink
	r.Payload = nil
	return r
}

// payloadOfSize returns a JSON object of exactly n bytes (n >= 8):
// {"p":"xxx…"} is 8 bytes plus the padding.
func payloadOfSize(n int) json.RawMessage {
	if n < 8 {
		n = 8
	}
	return json.RawMessage(`{"p":"` + strings.Repeat("x", n-8) + `"}`)
}

func newEvidenceStores(t *testing.T) (*IncidentStore, *IncidentEvidenceStore, *pgxpool.Pool) {
	t.Helper()
	pool := testDB(t)
	return NewIncidentStore(pool), NewIncidentEvidenceStore(pool), pool
}

// evidenceTotals is the incident's counters and the table's own truth.
type evidenceTotals struct {
	bytes, count, scopes          int64
	tableBytes, tableCount, scope int64
}

func readEvidenceTotals(t *testing.T, pool *pgxpool.Pool, incident uuid.UUID) evidenceTotals {
	t.Helper()
	var tot evidenceTotals
	if err := pool.QueryRow(t.Context(), `
		SELECT i.evidence_bytes, i.evidence_count, i.scope_count,
		       COALESCE((SELECT SUM(payload_bytes) FROM incident_evidence WHERE incident_id = i.id), 0),
		       (SELECT COUNT(*) FROM incident_evidence WHERE incident_id = i.id),
		       (SELECT COUNT(DISTINCT (cluster_id, api_group, resource, namespace))
		          FROM incident_evidence WHERE incident_id = i.id)
		  FROM incidents i WHERE i.id = $1`, incident).Scan(
		&tot.bytes, &tot.count, &tot.scopes, &tot.tableBytes, &tot.tableCount, &tot.scope); err != nil {
		t.Fatalf("reading evidence totals: %v", err)
	}
	return tot
}

// requireConsistent asserts the counters equal the table and the expected values.
func requireConsistent(t *testing.T, tot evidenceTotals, wantBytes, wantCount, wantScopes int64) {
	t.Helper()
	if tot.bytes != tot.tableBytes || tot.count != tot.tableCount || tot.scopes != tot.scope {
		t.Errorf("counters (%d B, %d items, %d scopes) disagree with the table (%d B, %d items, %d scopes)",
			tot.bytes, tot.count, tot.scopes, tot.tableBytes, tot.tableCount, tot.scope)
	}
	if tot.bytes != wantBytes || tot.count != wantCount || tot.scopes != wantScopes {
		t.Errorf("totals = (%d B, %d items, %d scopes); want (%d, %d, %d)",
			tot.bytes, tot.count, tot.scopes, wantBytes, wantCount, wantScopes)
	}
}

func mustInsert(t *testing.T, s *IncidentEvidenceStore, incident uuid.UUID, owner string, limits EvidenceLimits, rows ...IncidentEvidenceRow) int {
	t.Helper()
	n, err := s.InsertBatch(t.Context(), incident, owner, rows, limits)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// Pure tests
// ---------------------------------------------------------------------------

func TestEvidenceLimits_Validate(t *testing.T) {
	if err := ceilingLimits.Validate(); err != nil {
		t.Fatalf("the CHECK ceilings themselves were rejected: %v", err)
	}
	if err := (EvidenceLimits{MaxItemBytes: 1, MaxIncidentBytes: 1, MaxItems: 1, MaxScopes: 1}).Validate(); err != nil {
		t.Fatalf("minimal limits were rejected: %v", err)
	}
	mutate := map[string]func(*EvidenceLimits){
		"item bytes zero":         func(l *EvidenceLimits) { l.MaxItemBytes = 0 },
		"item bytes negative":     func(l *EvidenceLimits) { l.MaxItemBytes = -1 },
		"item bytes above 1 MiB":  func(l *EvidenceLimits) { l.MaxItemBytes = 1048577 },
		"incident bytes zero":     func(l *EvidenceLimits) { l.MaxIncidentBytes = 0 },
		"incident bytes > 10 MiB": func(l *EvidenceLimits) { l.MaxIncidentBytes = 10485761 },
		"items zero":              func(l *EvidenceLimits) { l.MaxItems = 0 },
		"items above 500":         func(l *EvidenceLimits) { l.MaxItems = 501 },
		"scopes negative":         func(l *EvidenceLimits) { l.MaxScopes = -5 },
		"scopes above 20":         func(l *EvidenceLimits) { l.MaxScopes = 21 },
	}
	for name, m := range mutate {
		l := ceilingLimits
		m(&l)
		if err := l.Validate(); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("%s: Validate = %v; want ErrIncidentInvalid", name, err)
		}
	}
}

func TestValidateEvidenceRow(t *testing.T) {
	if err := ValidateEvidenceRow(snapshotRow("k", 64)); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if err := ValidateEvidenceRow(liveLinkRow("k")); err != nil {
		t.Fatalf("valid live_link rejected: %v", err)
	}
	ok := snapshotRow("k", 64)
	ok.CompletenessDetail = strings.Repeat("€", 2000) // characters, not bytes
	ok.Redaction = json.RawMessage(`{"applied":true}`)
	if err := ValidateEvidenceRow(ok); err != nil {
		t.Fatalf("2000-character detail rejected: %v", err)
	}

	bad := map[string]func(*IncidentEvidenceRow){
		"live_link with payload":  func(r *IncidentEvidenceRow) { r.Mode = EvidenceModeLiveLink },
		"snapshot without":        func(r *IncidentEvidenceRow) { r.Payload = nil },
		"snapshot empty payload":  func(r *IncidentEvidenceRow) { r.Payload = json.RawMessage{} },
		"snapshot null payload":   func(r *IncidentEvidenceRow) { r.Payload = json.RawMessage(" null ") },
		"payload not JSON":        func(r *IncidentEvidenceRow) { r.Payload = json.RawMessage(`{"a":`) },
		"payload invalid UTF-8":   func(r *IncidentEvidenceRow) { r.Payload = json.RawMessage("{\"a\":\"\xff\"}") },
		"payload NUL escape":      func(r *IncidentEvidenceRow) { r.Payload = json.RawMessage(`{"a":"\u0000"}`) },
		"redaction not JSON":      func(r *IncidentEvidenceRow) { r.Redaction = json.RawMessage(`nope`) },
		"unknown kind":            func(r *IncidentEvidenceRow) { r.EvidenceKind = "loki_excerpt" },
		"unknown mode":            func(r *IncidentEvidenceRow) { r.Mode = "copy" },
		"unknown completeness":    func(r *IncidentEvidenceRow) { r.Completeness = "mostly" },
		"detail too long":         func(r *IncidentEvidenceRow) { r.CompletenessDetail = strings.Repeat("a", 2001) },
		"empty capture key":       func(r *IncidentEvidenceRow) { r.CaptureKey = "" },
		"capture key too long":    func(r *IncidentEvidenceRow) { r.CaptureKey = strings.Repeat("k", 129) },
		"empty cluster":           func(r *IncidentEvidenceRow) { r.ClusterID = "" },
		"empty resource":          func(r *IncidentEvidenceRow) { r.Resource = "" },
		"NUL in name":             func(r *IncidentEvidenceRow) { r.Name = "a\x00b" },
		"invalid UTF-8 namespace": func(r *IncidentEvidenceRow) { r.Namespace = "\xff" },
		"overlong source uid":     func(r *IncidentEvidenceRow) { r.SourceUID = strings.Repeat("u", 1025) },
	}
	for name, m := range bad {
		r := snapshotRow("k", 64)
		m(&r)
		if err := ValidateEvidenceRow(r); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("%s: ValidateEvidenceRow = %v; want ErrIncidentInvalid", name, err)
		}
	}
}

func TestEvidenceStore_RejectsBeforeSQL(t *testing.T) {
	s := NewIncidentEvidenceStore(nil) // any SQL would panic on the nil pool
	ctx := t.Context()
	id := uuid.New()
	limits := EvidenceLimits{MaxItemBytes: 100, MaxIncidentBytes: 1000, MaxItems: 10, MaxScopes: 5}

	// An empty batch is a no-op: no lock, no write.
	if n, err := s.InsertBatch(ctx, id, "o", nil, limits); n != 0 || err != nil {
		t.Errorf("empty batch = (%d, %v); want (0, nil)", n, err)
	}
	if _, err := s.InsertBatch(ctx, id, "", []IncidentEvidenceRow{snapshotRow("k", 10)}, limits); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("empty owner = %v; want ErrIncidentInvalid", err)
	}
	if _, err := s.InsertBatch(ctx, id, "o", []IncidentEvidenceRow{snapshotRow("k", 10)}, EvidenceLimits{}); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("zero limits = %v; want ErrIncidentInvalid", err)
	}
	if _, err := s.InsertBatch(ctx, id, "o", []IncidentEvidenceRow{snapshotRow("a", 10), liveLinkRow("b"), {}}, limits); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("batch with an invalid row = %v; want ErrIncidentInvalid", err)
	}
	tooMany := make([]IncidentEvidenceRow, EvidenceMaxItemsCeiling+1)
	for i := range tooMany {
		tooMany[i] = snapshotRow(strconv.Itoa(i), 10)
	}
	if _, err := s.InsertBatch(ctx, id, "o", tooMany, ceilingLimits); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("a batch above the item ceiling = %v; want ErrIncidentInvalid", err)
	}

	// Per-item size: the second row is one byte over, so nothing is attempted.
	_, err := s.InsertBatch(ctx, id, "o", []IncidentEvidenceRow{snapshotRow("a", 100), snapshotRow("b", 101)}, limits)
	var le *EvidenceLimitError
	if !errors.As(err, &le) || !errors.Is(err, ErrEvidenceLimit) {
		t.Fatalf("oversized item = %v; want an *EvidenceLimitError matching ErrEvidenceLimit", err)
	}
	if le.Limit != EvidenceLimitItemBytes || le.Max != 100 || le.Attempted != 101 {
		t.Errorf("item limit error = %+v; want {item_bytes, max 100, attempted 101}", *le)
	}
	// A caller-supplied PayloadBytes is ignored: the store measures the payload.
	lying := snapshotRow("a", 101)
	lying.PayloadBytes = 1
	if _, err := s.InsertBatch(ctx, id, "o", []IncidentEvidenceRow{lying}, limits); !errors.Is(err, ErrEvidenceLimit) {
		t.Errorf("understated PayloadBytes = %v; want ErrEvidenceLimit from the measured size", err)
	}

	if _, _, err := s.ListByIncident(ctx, id, 10, "!!"); !errors.Is(err, ErrInvalidEvidenceCursor) {
		t.Errorf("malformed cursor = %v; want ErrInvalidEvidenceCursor", err)
	}
	if _, err := s.ListBySourceUID(ctx, id, "local", ""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("empty source uid = %v; want ErrIncidentInvalid", err)
	}
	if _, err := s.ListBySourceUID(ctx, id, "", "u"); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("empty cluster = %v; want ErrIncidentInvalid", err)
	}
}

func TestEvidenceLimitErrors_IsAndMessage(t *testing.T) {
	var err error = &EvidenceLimitError{Limit: EvidenceLimitIncidentBytes, Max: 10, Current: 8, Attempted: 12}
	if !errors.Is(err, ErrEvidenceLimit) || errors.Is(err, ErrScopeLimit) {
		t.Errorf("EvidenceLimitError Is mismatch: %v", err)
	}
	if !strings.Contains(err.Error(), "incident_bytes") {
		t.Errorf("message %q does not name the limit", err.Error())
	}
	err = &ScopeLimitError{Max: 20, Current: 20, Attempted: 21}
	if !errors.Is(err, ErrScopeLimit) || errors.Is(err, ErrEvidenceLimit) {
		t.Errorf("ScopeLimitError Is mismatch: %v", err)
	}
}

func TestEvidenceCursor_RoundTripAndMalformed(t *testing.T) {
	c := EvidenceCursor{CollectedAt: time.Date(2026, 10, 5, 1, 2, 3, 456789000, time.UTC), ID: uuid.New()}
	got, err := DecodeEvidenceCursor(EncodeEvidenceCursor(c))
	if err != nil || !got.CollectedAt.Equal(c.CollectedAt) || got.ID != c.ID {
		t.Fatalf("round trip = (%+v, %v); want %+v", got, err, c)
	}
	for _, s := range []string{"", "!!", "MTIz", encodeMicrosCursor(c.CollectedAt, uuid.Nil.String()),
		encodeMicrosCursor(c.CollectedAt, strings.ToUpper(c.ID.String())), encodeMicrosCursor(c.CollectedAt, "7")} {
		if _, err := DecodeEvidenceCursor(s); !errors.Is(err, ErrInvalidEvidenceCursor) {
			t.Errorf("Decode(%q) = %v; want ErrInvalidEvidenceCursor", s, err)
		}
	}
}

// TestEvidenceHasNoUpdatePath pins P15: evidence is append-only, so the store
// exposes no per-row mutation at all.
func TestEvidenceHasNoUpdatePath(t *testing.T) {
	typ := reflect.TypeFor[*IncidentEvidenceStore]()
	if typ.NumMethod() == 0 {
		t.Fatal("IncidentEvidenceStore has no methods; the reflection probe is looking at the wrong type")
	}
	for i := range typ.NumMethod() {
		name := typ.Method(i).Name
		for _, verb := range []string{"Update", "Delete", "Set", "Remove", "Modify", "Upsert", "Replace", "Patch", "Edit"} {
			if strings.HasPrefix(name, verb) {
				t.Errorf("IncidentEvidenceStore.%s is a mutation path; evidence is append-only (P15)", name)
			}
		}
	}
}

func TestValidateGranteeID(t *testing.T) {
	for _, ok := range []string{"u", "oidc:abc|user@example.com", strings.Repeat("a", 256), "ldap:Zoë"} {
		if err := ValidateGranteeID(ok); err != nil {
			t.Errorf("ValidateGranteeID(%q) = %v; want nil", ok, err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 257), "a\nb", "a\x00b", "a\tb", "\x7f", "a\u0085b", "\xff"} {
		if err := ValidateGranteeID(bad); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("ValidateGranteeID(%q) = %v; want ErrIncidentInvalid", bad, err)
		}
	}
}

func TestGrantStore_RejectsBeforeSQL(t *testing.T) {
	s := NewIncidentGrantStore(nil)
	ctx := t.Context()
	id := uuid.New()
	if err := s.AddGrant(ctx, id, "", "g", true); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("AddGrant empty owner = %v", err)
	}
	if err := s.AddGrant(ctx, id, "o", "bad\nid", true); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("AddGrant bad grantee = %v", err)
	}
	if err := s.RemoveGrant(ctx, id, "", "g"); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("RemoveGrant empty owner = %v", err)
	}
	if err := s.RemoveGrant(ctx, id, "o", ""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("RemoveGrant empty grantee = %v", err)
	}
	if _, err := s.GetGrant(ctx, id, ""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("GetGrant empty user = %v", err)
	}
}

// ---------------------------------------------------------------------------
// DB-backed: accounting, limits and gates
// ---------------------------------------------------------------------------

func TestInsertBatchEnforcesIncidentByteCeiling(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "bytes"))
	limits := EvidenceLimits{MaxItemBytes: 100, MaxIncidentBytes: 100, MaxItems: 50, MaxScopes: 5}

	if n := mustInsert(t, es, incident, owner, limits, snapshotRow("a", 30), snapshotRow("b", 30)); n != 2 {
		t.Fatalf("inserted %d; want 2", n)
	}
	before := readEvidenceTotals(t, pool, incident)
	requireConsistent(t, before, 60, 2, 1)

	// 60 + 25 + 25 = 110 > 100: the whole batch is refused, including the row that would fit.
	_, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{snapshotRow("c", 25), snapshotRow("d", 25)}, limits)
	var le *EvidenceLimitError
	if !errors.As(err, &le) || !errors.Is(err, ErrEvidenceLimit) {
		t.Fatalf("over-ceiling batch = %v; want *EvidenceLimitError", err)
	}
	if *le != (EvidenceLimitError{Limit: EvidenceLimitIncidentBytes, Max: 100, Current: 60, Attempted: 110}) {
		t.Errorf("limit error = %+v; want {incident_bytes, 100, 60, 110}", *le)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 60, 2, 1)

	// Exactly at the ceiling is allowed.
	if n := mustInsert(t, es, incident, owner, limits, snapshotRow("e", 40)); n != 1 {
		t.Fatalf("inserted %d; want 1", n)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 100, 3, 1)
	if got := mustGetIncident(t, is, incident); !got.UpdatedAt.After(got.CreatedAt) {
		t.Errorf("updated_at %s not bumped past created_at %s", got.UpdatedAt, got.CreatedAt)
	}
}

func TestInsertBatchEnforcesItemCountCeiling(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "items"))
	limits := EvidenceLimits{MaxItemBytes: 100, MaxIncidentBytes: 10000, MaxItems: 3, MaxScopes: 5}

	mustInsert(t, es, incident, owner, limits, snapshotRow("a", 10), snapshotRow("b", 10))
	_, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{snapshotRow("c", 10), snapshotRow("d", 10)}, limits)
	var le *EvidenceLimitError
	if !errors.As(err, &le) || *le != (EvidenceLimitError{Limit: EvidenceLimitItems, Max: 3, Current: 2, Attempted: 4}) {
		t.Fatalf("over-count batch = %v; want items limit {3, 2, 4}", err)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 20, 2, 1)
	if n := mustInsert(t, es, incident, owner, limits, snapshotRow("c", 10)); n != 1 {
		t.Fatalf("inserted %d; want 1 (exactly at the ceiling)", n)
	}
}

func TestInsertBatchIsIdempotentOnDuplicateCapture(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "idem"))

	batch := []IncidentEvidenceRow{snapshotRow("a", 20), snapshotRow("b", 30), liveLinkRow("c")}
	if n := mustInsert(t, es, incident, owner, ceilingLimits, batch...); n != 3 {
		t.Fatalf("first capture inserted %d; want 3", n)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 50, 3, 1)

	// A fully deduplicated batch writes nothing, not even updated_at.
	before := mustGetIncident(t, is, incident)
	for range 2 {
		if n := mustInsert(t, es, incident, owner, ceilingLimits, batch...); n != 0 {
			t.Fatalf("re-capture inserted %d; want 0", n)
		}
	}
	if after := mustGetIncident(t, is, incident); !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("a fully deduplicated re-capture moved updated_at from %s to %s", before.UpdatedAt, after.UpdatedAt)
	}
	// A duplicate key inside one batch collapses too.
	if n := mustInsert(t, es, incident, owner, ceilingLimits, snapshotRow("d", 10), snapshotRow("d", 10)); n != 1 {
		t.Fatalf("in-batch duplicate inserted %d; want 1", n)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 60, 4, 1)
}

// TestInsertBatchDedupDoesNotInflateByteCount is D-3: only rows actually
// inserted are counted, so a batch that exceeds a limit only through rows that
// deduplicate is accepted.
func TestInsertBatchDedupDoesNotInflateByteCount(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "dedup"))
	limits := EvidenceLimits{MaxItemBytes: 100, MaxIncidentBytes: 100, MaxItems: 3, MaxScopes: 5}

	mustInsert(t, es, incident, owner, limits, snapshotRow("a", 60), snapshotRow("b", 20))
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 80, 2, 1)

	// Bytes: 80 + 60 (dup a) + 20 (new c) = 160 naively; 100 actually.
	if n := mustInsert(t, es, incident, owner, limits, snapshotRow("a", 60), snapshotRow("c", 20)); n != 1 {
		t.Fatalf("inserted %d; want 1 (a deduplicates)", n)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 100, 3, 1)

	// Items: 3 + 3 duplicates = 6 naively; 3 actually.
	if n := mustInsert(t, es, incident, owner, limits, snapshotRow("a", 60), snapshotRow("b", 20), snapshotRow("c", 20)); n != 0 {
		t.Fatalf("inserted %d; want 0", n)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 100, 3, 1)
}

func TestInsertBatchScopeCapAtTwenty(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "scopes"))

	rows := make([]IncidentEvidenceRow, 0, 20)
	for i := range 20 {
		r := snapshotRow("ns-"+strconv.Itoa(i), 10)
		r.Namespace = "ns-" + strconv.Itoa(i)
		rows = append(rows, r)
	}
	mustInsert(t, es, incident, owner, ceilingLimits, rows...)
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 200, 20, 20)

	// A new row in an existing scope is fine; one new scope is not.
	sameScope := snapshotRow("again", 10)
	sameScope.Namespace = "ns-3"
	sameScope.Name = "other"
	newScope := snapshotRow("new-scope", 10)
	newScope.Namespace = "ns-20"
	_, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{sameScope, newScope}, ceilingLimits)
	var se *ScopeLimitError
	if !errors.As(err, &se) || !errors.Is(err, ErrScopeLimit) || errors.Is(err, ErrEvidenceLimit) {
		t.Fatalf("21st scope = %v; want *ScopeLimitError", err)
	}
	if *se != (ScopeLimitError{Max: 20, Current: 20, Attempted: 21}) {
		t.Errorf("scope error = %+v; want {20, 20, 21}", *se)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 200, 20, 20)
	// The api_group is part of the scope: same resource name, other group.
	grouped := snapshotRow("grouped", 10)
	grouped.Namespace = "ns-0"
	grouped.APIGroup = "metrics.k8s.io"
	if _, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{grouped}, ceilingLimits); !errors.Is(err, ErrScopeLimit) {
		t.Errorf("a new api_group = %v; want ErrScopeLimit", err)
	}
	if n := mustInsert(t, es, incident, owner, ceilingLimits, sameScope); n != 1 {
		t.Fatalf("same-scope row inserted %d; want 1", n)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 210, 21, 20)
}

func TestInsertBatchOwnerStatusAndExistenceGates(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "gates"))
	collaborator := owner + "-collab"
	insertGrant(t, pool, incident, collaborator, owner)
	row := []IncidentEvidenceRow{snapshotRow("a", 10)}

	if _, err := es.InsertBatch(t.Context(), incident, collaborator, row, ceilingLimits); !errors.Is(err, ErrNotOwner) {
		t.Errorf("collaborator capture = %v; want ErrNotOwner (capture is owner-only, P3)", err)
	}
	if _, err := es.InsertBatch(t.Context(), uuid.New(), owner, row, ceilingLimits); !errors.Is(err, ErrIncidentNotFound) {
		t.Errorf("missing incident = %v; want ErrIncidentNotFound", err)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 0, 0, 0)

	if err := is.Update(t.Context(), incident, owner, "gates", "", IncidentStatusClosed); err != nil {
		t.Fatal(err)
	}
	if _, err := es.InsertBatch(t.Context(), incident, owner, row, ceilingLimits); !errors.Is(err, ErrIncidentClosed) {
		t.Errorf("closed incident = %v; want ErrIncidentClosed", err)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 0, 0, 0)
	if err := is.Update(t.Context(), incident, owner, "gates", "", IncidentStatusOpen); err != nil {
		t.Fatal(err)
	}
	if n := mustInsert(t, es, incident, owner, ceilingLimits, row...); n != 1 {
		t.Errorf("reopened incident inserted %d; want 1", n)
	}
}

// TestInsertBatchStoresWhatWasCaptured checks the round trip through the
// store, the server-computed byte count and the live_link NULL payload.
func TestInsertBatchStoresWhatWasCaptured(t *testing.T) {
	is, es, _ := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "roundtrip"))

	observed := time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC)
	snap := snapshotRow("snap", 40)
	snap.APIGroup = "apps"
	snap.Resource = "deployments"
	snap.SourceKind = "Deployment"
	snap.ResourceVersion = "123"
	snap.SecretDerived = true
	snap.SourceObservedAt = &observed
	snap.Completeness = EvidenceCompletenessPartial
	snap.CompletenessDetail = "events truncated"
	snap.Redaction = json.RawMessage(`{"applied":true,"rules":["secret-values"]}`)
	snap.PayloadBytes = 999999 // ignored
	link := liveLinkRow("link")
	mustInsert(t, es, incident, owner, ceilingLimits, snap, link)

	all, err := es.ListAllByIncident(t.Context(), incident)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListAllByIncident = (%d rows, %v); want 2", len(all), err)
	}
	byKey := map[string]IncidentEvidenceRow{}
	for _, r := range all {
		byKey[r.CaptureKey] = r
	}
	got := byKey["snap"]
	if got.IncidentID != incident || got.ID == uuid.Nil || got.CollectedAt.IsZero() ||
		got.APIGroup != "apps" || got.Resource != "deployments" || got.SourceKind != "Deployment" ||
		got.ResourceVersion != "123" || !got.SecretDerived || got.SourceObservedAt == nil ||
		!got.SourceObservedAt.Equal(observed) || got.Completeness != EvidenceCompletenessPartial ||
		got.CompletenessDetail != "events truncated" || got.PayloadBytes != 40 {
		t.Errorf("snapshot round trip = %+v", got)
	}
	var payload, redaction map[string]any
	if json.Unmarshal(got.Payload, &payload) != nil || json.Unmarshal(got.Redaction, &redaction) != nil ||
		redaction["applied"] != true || len(payload["p"].(string)) != 32 {
		t.Errorf("payload %s / redaction %s did not round-trip", got.Payload, got.Redaction)
	}
	l := byKey["link"]
	if l.Mode != EvidenceModeLiveLink || l.Payload != nil || l.PayloadBytes != 0 || string(l.Redaction) != "{}" {
		t.Errorf("live_link round trip = %+v (payload %q, redaction %q)", l, l.Payload, l.Redaction)
	}
}

// TestRecreatedNameDoesNotInheritEvidence is P9: two objects with the same
// namespace/name and different UIDs are different subjects.
func TestRecreatedNameDoesNotInheritEvidence(t *testing.T) {
	is, es, _ := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "recreated"))
	other := mustCreateIncident(t, is, newIncident(owner, "other"))

	old := snapshotRow("old", 10)
	old.SourceUID = "uid-old"
	recreated := snapshotRow("new", 10)
	recreated.SourceUID = "uid-new"
	mustInsert(t, es, incident, owner, ceilingLimits, old, recreated)
	elsewhere := snapshotRow("elsewhere", 10)
	elsewhere.SourceUID = "uid-new"
	mustInsert(t, es, other, owner, ceilingLimits, elsewhere)

	for uid, want := range map[string]string{"uid-old": "old", "uid-new": "new"} {
		rows, err := es.ListBySourceUID(t.Context(), incident, "local", uid)
		if err != nil || len(rows) != 1 || rows[0].CaptureKey != want {
			t.Errorf("ListBySourceUID(%s) = (%v, %v); want only %q", uid, captureKeys(rows), err, want)
		}
	}
	if rows, err := es.ListBySourceUID(t.Context(), incident, "other-cluster", "uid-new"); err != nil || len(rows) != 0 {
		t.Errorf("another cluster's uid crossed: (%v, %v)", captureKeys(rows), err)
	}
	if rows, err := es.ListBySourceUID(t.Context(), incident, "local", "uid-gone"); err != nil || len(rows) != 0 {
		t.Errorf("a recreated object with a fresh uid inherited (%v, %v)", captureKeys(rows), err)
	}
}

func captureKeys(rows []IncidentEvidenceRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.CaptureKey)
	}
	return out
}

// TestInsertBatchRollsBackOnConstraintViolation drives the transactional
// path past Go validation (insertValidated) with a row the CHECK rejects in
// the middle of the batch: the rows before it must not survive.
func TestInsertBatchRollsBackOnConstraintViolation(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "rollback"))
	mustInsert(t, es, incident, owner, ceilingLimits, snapshotRow("seed", 10))

	prep := func(r IncidentEvidenceRow) IncidentEvidenceRow {
		r.PayloadBytes = len(r.Payload)
		r.Redaction = json.RawMessage(`{}`)
		return r
	}
	liveWithPayload := prep(snapshotRow("bad-live", 10))
	liveWithPayload.Mode = EvidenceModeLiveLink // CHECK: live_link must have no payload
	snapNoPayload := prep(liveLinkRow("bad-snap"))
	snapNoPayload.Mode = EvidenceModeSnapshot // CHECK: snapshot must have a payload

	for name, bad := range map[string]IncidentEvidenceRow{"live_link with payload": liveWithPayload, "snapshot without payload": snapNoPayload} {
		if err := ValidateEvidenceRow(bad); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("%s passed Go validation: %v", name, err)
		}
		_, err := es.insertValidated(t.Context(), incident, owner,
			[]IncidentEvidenceRow{prep(snapshotRow("ok-1", 10)), bad, prep(snapshotRow("ok-2", 10))}, ceilingLimits)
		if err == nil {
			t.Errorf("%s: the CHECK did not reject the batch", name)
		}
		requireConsistent(t, readEvidenceTotals(t, pool, incident), 10, 1, 1)
	}
}

// evidenceBatchCanceller cancels the capture context once the first INSERT
// of the batch has run, so earlier rows exist inside the transaction.
type evidenceBatchCanceller struct {
	cancel context.CancelFunc
	seen   int
}

func (c *evidenceBatchCanceller) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}

func (c *evidenceBatchCanceller) TraceBatchQuery(_ context.Context, _ *pgx.Conn, d pgx.TraceBatchQueryData) {
	if strings.Contains(d.SQL, "INSERT INTO incident_evidence") {
		c.seen++
		if c.seen == 1 && c.cancel != nil {
			c.cancel()
		}
	}
}

func (c *evidenceBatchCanceller) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func (c *evidenceBatchCanceller) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (c *evidenceBatchCanceller) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCancellationLeavesNoPartialBatch(t *testing.T) {
	tracer := &evidenceBatchCanceller{}
	pool := testDBWithOptions(t, 2, func(c *pgxpool.Config) { c.ConnConfig.Tracer = tracer })
	is, es := NewIncidentStore(pool), NewIncidentEvidenceStore(pool)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "cancel"))

	ctx, cancel := context.WithCancel(t.Context())
	tracer.cancel = cancel
	rows := make([]IncidentEvidenceRow, 0, 50)
	for i := range 50 {
		rows = append(rows, snapshotRow("c"+strconv.Itoa(i), 2000))
	}
	_, err := es.InsertBatch(ctx, incident, owner, rows, ceilingLimits)
	tracer.cancel = nil
	cancel()
	if err == nil {
		t.Fatal("InsertBatch succeeded although its context was cancelled mid-batch")
	}
	if tracer.seen == 0 {
		t.Fatal("the tracer never saw an INSERT; the test did not cancel mid-batch")
	}
	reader := testDB(t)
	requireConsistent(t, readEvidenceTotals(t, reader, incident), 0, 0, 0)
	// The incident row lock was released: a fresh capture succeeds at once.
	ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel2()
	if n, err := NewIncidentEvidenceStore(reader).InsertBatch(ctx2, incident, owner, rows[:2], ceilingLimits); err != nil || n != 2 {
		t.Errorf("capture after the cancelled one = (%d, %v); want (2, nil)", n, err)
	}
}

// evidenceLockRendezvous holds the first capture's incident read open until
// the second capture's read completes, or 750ms pass. With FOR UPDATE the
// second read blocks on the row lock, so the first proceeds after the timeout
// and the two captures serialize. Without the lock both read the same totals
// and race. It keys on the SELECT text (not the FOR UPDATE clause), so it
// still synchronizes when the lock clause is removed.
type evidenceLockRendezvous struct {
	mu     sync.Mutex
	reads  int
	second chan struct{}
}

type evidenceSQLKey struct{}

func (r *evidenceLockRendezvous) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, evidenceSQLKey{}, d.SQL)
}

func (r *evidenceLockRendezvous) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(evidenceSQLKey{}).(string)
	if d.Err != nil || !strings.Contains(sql, "evidence_bytes, evidence_count, scope_count") {
		return
	}
	r.mu.Lock()
	r.reads++
	n := r.reads
	r.mu.Unlock()
	switch n {
	case 1:
		select {
		case <-r.second:
		case <-time.After(750 * time.Millisecond):
		}
	case 2:
		close(r.second)
	}
}

func TestConcurrentInsertBatchNeverExceedsCeiling(t *testing.T) {
	setup := testDB(t)
	is := NewIncidentStore(setup)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "race"))
	limits := EvidenceLimits{MaxItemBytes: 100, MaxIncidentBytes: 150, MaxItems: 50, MaxScopes: 5}

	rv := &evidenceLockRendezvous{second: make(chan struct{})}
	raced := NewIncidentEvidenceStore(testDBWithOptions(t, 4, func(c *pgxpool.Config) { c.ConnConfig.Tracer = rv }))

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		results = make([]error, 2)
	)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Each batch (100 bytes) fits alone; together they exceed 150.
			_, results[i] = raced.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{
				snapshotRow("g"+strconv.Itoa(i)+"-a", 50), snapshotRow("g"+strconv.Itoa(i)+"-b", 50),
			}, limits)
		}()
	}
	close(start)
	wg.Wait()

	var wins, limited int
	for _, err := range results {
		var le *EvidenceLimitError
		switch {
		case err == nil:
			wins++
		case errors.As(err, &le) && le.Current == 100 && le.Attempted == 200:
			limited++
		default:
			t.Errorf("racing InsertBatch returned %v; want success or an incident_bytes limit at current 100", err)
		}
	}
	if wins != 1 || limited != 1 {
		t.Fatalf("wins=%d limited=%d; want exactly one of each", wins, limited)
	}
	requireConsistent(t, readEvidenceTotals(t, setup, incident), 100, 2, 1)
}

// ---------------------------------------------------------------------------
// DB-backed: reads
// ---------------------------------------------------------------------------

func TestListByIncidentPaginatesStably(t *testing.T) {
	is, es, _ := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "pages"))
	other := mustCreateIncident(t, is, newIncident(owner, "noise"))
	// Rows within one batch share collected_at (one transaction), so the id
	// tie-break carries the ordering.
	mustInsert(t, es, incident, owner, ceilingLimits, snapshotRow("a", 10), snapshotRow("b", 10), snapshotRow("c", 10))
	mustInsert(t, es, incident, owner, ceilingLimits, snapshotRow("d", 10), liveLinkRow("e"))
	mustInsert(t, es, other, owner, ceilingLimits, snapshotRow("x", 10))

	all, err := es.ListAllByIncident(t.Context(), incident)
	if err != nil || len(all) != 5 {
		t.Fatalf("ListAllByIncident = (%d, %v); want 5", len(all), err)
	}
	for i := 1; i < len(all); i++ {
		p, c := all[i-1], all[i]
		if c.CollectedAt.After(p.CollectedAt) || (c.CollectedAt.Equal(p.CollectedAt) && c.ID.String() > p.ID.String()) {
			t.Fatalf("ListAllByIncident not ordered (collected_at DESC, id DESC) at %d", i)
		}
	}

	var paged []IncidentEvidenceRow
	cursor := ""
	for range 10 {
		page, next, err := es.ListByIncident(t.Context(), incident, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(paged) != len(all) {
		t.Fatalf("paged %d rows; want %d", len(paged), len(all))
	}
	for i := range all {
		if paged[i].ID != all[i].ID {
			t.Fatalf("page order diverges from the full order at %d", i)
		}
	}
	if _, _, err := es.ListByIncident(t.Context(), incident, 2, "!!"); !errors.Is(err, ErrInvalidEvidenceCursor) {
		t.Errorf("malformed cursor = %v", err)
	}
}

func TestDistinctScopesAreDeduplicatedAndSorted(t *testing.T) {
	is, es, _ := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "scopes"))
	mk := func(key, cluster, group, resource, ns string) IncidentEvidenceRow {
		r := snapshotRow(key, 10)
		r.ClusterID, r.APIGroup, r.Resource, r.Namespace = cluster, group, resource, ns
		return r
	}
	mustInsert(t, es, incident, owner, ceilingLimits,
		mk("1", "local", "apps", "deployments", "web"),
		mk("2", "local", "", "pods", "web"),
		mk("3", "local", "", "pods", "web"), // duplicate scope
		mk("4", "local", "", "pods", "api"),
		mk("5", "b-cluster", "", "events", ""),
	)
	got, err := es.DistinctScopes(t.Context(), incident)
	if err != nil {
		t.Fatal(err)
	}
	want := []EvidenceScope{
		{ClusterID: "b-cluster", APIGroup: "", Resource: "events", Namespace: ""},
		{ClusterID: "local", APIGroup: "", Resource: "pods", Namespace: "api"},
		{ClusterID: "local", APIGroup: "", Resource: "pods", Namespace: "web"},
		{ClusterID: "local", APIGroup: "apps", Resource: "deployments", Namespace: "web"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DistinctScopes = %+v; want %+v", got, want)
	}
	if empty, err := es.DistinctScopes(t.Context(), uuid.New()); err != nil || len(empty) != 0 {
		t.Errorf("DistinctScopes of an unknown incident = (%v, %v); want empty", empty, err)
	}
}

// ---------------------------------------------------------------------------
// DB-backed: grants
// ---------------------------------------------------------------------------

func newGrantStores(t *testing.T) (*IncidentStore, *IncidentGrantStore, *pgxpool.Pool) {
	t.Helper()
	pool := testDB(t)
	return NewIncidentStore(pool), NewIncidentGrantStore(pool), pool
}

func TestGrantAddRemoveByNonOwnerFails(t *testing.T) {
	is, gs, pool := newGrantStores(t)
	owner := testOwnerID(t)
	intruder := owner + "-intruder"
	incident := mustCreateIncident(t, is, newIncident(owner, "grants"))
	if err := gs.AddGrant(t.Context(), incident, owner, owner+"-g", true); err != nil {
		t.Fatal(err)
	}

	if err := gs.AddGrant(t.Context(), incident, intruder, intruder, true); !errors.Is(err, ErrNotOwner) {
		t.Errorf("intruder self-grant = %v; want ErrNotOwner (never a silent no-op)", err)
	}
	if err := gs.AddGrant(t.Context(), incident, intruder, owner+"-x", true); !errors.Is(err, ErrNotOwner) {
		t.Errorf("intruder grant = %v; want ErrNotOwner", err)
	}
	if err := gs.RemoveGrant(t.Context(), incident, intruder, owner+"-g"); !errors.Is(err, ErrNotOwner) {
		t.Errorf("intruder remove = %v; want ErrNotOwner", err)
	}
	// A collaborator is not an owner either.
	if err := gs.AddGrant(t.Context(), incident, owner+"-g", owner+"-y", true); !errors.Is(err, ErrNotOwner) {
		t.Errorf("collaborator grant = %v; want ErrNotOwner", err)
	}
	if err := gs.AddGrant(t.Context(), uuid.New(), owner, owner+"-g", true); !errors.Is(err, ErrIncidentNotFound) {
		t.Errorf("grant on missing incident = %v; want ErrIncidentNotFound", err)
	}
	if err := gs.RemoveGrant(t.Context(), uuid.New(), owner, owner+"-g"); !errors.Is(err, ErrIncidentNotFound) {
		t.Errorf("remove on missing incident = %v; want ErrIncidentNotFound", err)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_grants WHERE incident_id = $1`, incident); n != 1 {
		t.Errorf("%d grants; want only the owner's one", n)
	}
}

func TestGrantSelfGrantUpsertAndImmediateRemoval(t *testing.T) {
	is, gs, pool := newGrantStores(t)
	owner := testOwnerID(t)
	grantee := owner + "-g"
	incident := mustCreateIncident(t, is, newIncident(owner, "lifecycle"))

	if err := gs.AddGrant(t.Context(), incident, owner, owner, true); err != nil {
		t.Errorf("owner self-grant = %v; want nil", err)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_grants WHERE incident_id = $1`, incident); n != 0 {
		t.Errorf("self-grant wrote %d rows; want 0", n)
	}
	if g, err := gs.GetGrant(t.Context(), incident, grantee); g != nil || err != nil {
		t.Errorf("GetGrant before any grant = (%+v, %v); want (nil, nil)", g, err)
	}

	if err := gs.AddGrant(t.Context(), incident, owner, grantee, false); err != nil {
		t.Fatal(err)
	}
	g, err := gs.GetGrant(t.Context(), incident, grantee)
	if err != nil || g == nil || g.CanAnnotate || g.GrantedBy != owner || g.IncidentID != incident || g.GranteeID != grantee {
		t.Fatalf("GetGrant = (%+v, %v); want a read-only grant by the owner", g, err)
	}
	if err := gs.AddGrant(t.Context(), incident, owner, grantee, true); err != nil {
		t.Fatal(err)
	}
	if g, _ := gs.GetGrant(t.Context(), incident, grantee); g == nil || !g.CanAnnotate {
		t.Errorf("re-grant did not raise can_annotate: %+v", g)
	}
	list, err := gs.ListGrants(t.Context(), incident)
	if err != nil || len(list) != 1 || list[0].GranteeID != grantee {
		t.Errorf("ListGrants = (%+v, %v); want the one grantee", list, err)
	}

	if err := gs.RemoveGrant(t.Context(), incident, owner, grantee); err != nil {
		t.Fatal(err)
	}
	if g, err := gs.GetGrant(t.Context(), incident, grantee); g != nil || err != nil {
		t.Errorf("GetGrant right after removal = (%+v, %v); want (nil, nil)", g, err)
	}
	if err := gs.RemoveGrant(t.Context(), incident, owner, grantee); !errors.Is(err, ErrGrantNotFound) {
		t.Errorf("second removal = %v; want ErrGrantNotFound", err)
	}
}

func TestGrantCapAndCascade(t *testing.T) {
	is, gs, pool := newGrantStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "cap"))
	for i := range IncidentMaxGrants {
		if err := gs.AddGrant(t.Context(), incident, owner, owner+"-g"+strconv.Itoa(i), true); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}
	if err := gs.AddGrant(t.Context(), incident, owner, owner+"-one-too-many", true); !errors.Is(err, ErrGrantLimit) {
		t.Errorf("grant past the cap = %v; want ErrGrantLimit", err)
	}
	// Updating an existing grantee at the cap is not a new grant.
	if err := gs.AddGrant(t.Context(), incident, owner, owner+"-g0", false); err != nil {
		t.Errorf("re-grant at the cap = %v; want nil", err)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_grants WHERE incident_id = $1`, incident); n != IncidentMaxGrants {
		t.Errorf("%d grants; want %d", n, IncidentMaxGrants)
	}

	if err := is.Delete(t.Context(), incident, owner); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_grants WHERE incident_id = $1`, incident); n != 0 {
		t.Errorf("%d grants survived the incident delete; want 0", n)
	}
	if list, err := gs.ListGrants(t.Context(), incident); err != nil || len(list) != 0 {
		t.Errorf("ListGrants after delete = (%v, %v)", list, err)
	}
}
