package store

// incident_evidence_test.go — coverage for IncidentEvidenceStore (Release D,
// U21b). Grant tests are in incident_grants_test.go.
//
// Pure tests (no database) cover the limit and row validation that runs
// before any SQL, the evidence cursor codec and the append-only method set. Env-gated tests (testDB, skipped without
// KUBECENTER_TEST_DATABASE_URL) cover the transactional accounting under the
// incident row lock, dedup, the owner gates, UID-keyed provenance,
// pagination and scopes.
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
		"redaction not an object": func(r *IncidentEvidenceRow) { r.Redaction = json.RawMessage(`["secret-values"]`) },
		// The read path's Secret gate keys on the column, so a redaction that
		// says secret-derived must never be stored under secret_derived=false.
		"secretDerived disagrees": func(r *IncidentEvidenceRow) { r.Redaction = json.RawMessage(`{"secretDerived":true}`) },
		"secretDerived any case":  func(r *IncidentEvidenceRow) { r.Redaction = json.RawMessage(`{"SecretDerived":true}`) },
		"lone surrogate payload":  func(r *IncidentEvidenceRow) { r.Payload = json.RawMessage(`{"a":"\ud800"}`) },
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
//
// It is an exact allowlist: any new exported method, whatever its name, fails
// here and has to be argued for against P15.
func TestEvidenceHasNoUpdatePath(t *testing.T) {
	typ := reflect.TypeFor[*IncidentEvidenceStore]()
	got := make([]string, 0, typ.NumMethod())
	for i := range typ.NumMethod() {
		got = append(got, typ.Method(i).Name) // exported methods only, sorted by name
	}
	want := []string{"DistinctScopes", "InsertBatch", "ListAllByIncident", "ListByIncident", "ListBySourceUID", "ListScopeRowsByIncident"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IncidentEvidenceStore exported methods = %v; want exactly %v (evidence is append-only, P15)", got, want)
	}
}

// TestListScopeRowsByIncidentCarriesAuthorizationColumnsOnly: the read path
// counts an incident's evidence through the per-scope filter without loading
// payloads (U23a review: whole-incident counts over up to 500 rows must not
// read 10 MiB of JSONB). Every column the filter needs is present; payload
// and redaction are not.
func TestListScopeRowsByIncidentCarriesAuthorizationColumnsOnly(t *testing.T) {
	is, es, _ := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "scope rows"))

	secret := snapshotRow("secret", 4096)
	secret.SecretDerived = true
	secret.Redaction = json.RawMessage(`{"secretDerived":true}`)
	check := snapshotRow("check", 64)
	check.EvidenceKind = EvidenceKindDiagnosticCheck
	check.SourceKind = "Deployment"
	check.APIGroup = ""
	check.Resource = "pods"
	check.Namespace = "payments"
	mustInsert(t, es, incident, owner, ceilingLimits, secret, check, liveLinkRow("link"))

	rows, err := es.ListScopeRowsByIncident(t.Context(), incident)
	if err != nil || len(rows) != 3 {
		t.Fatalf("ListScopeRowsByIncident = (%d rows, %v); want 3", len(rows), err)
	}
	full, err := es.ListAllByIncident(t.Context(), incident)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if r.Payload != nil || r.Redaction != nil || r.PayloadBytes != 0 || r.Name != "" || r.SourceUID != "" {
			t.Errorf("row %d carries content columns: payload=%d redaction=%d bytes=%d name=%q uid=%q",
				i, len(r.Payload), len(r.Redaction), r.PayloadBytes, r.Name, r.SourceUID)
		}
		if r.ID == uuid.Nil || r.IncidentID != incident || r.ClusterID != "local" || r.CollectedAt.IsZero() || r.EvidenceKind == "" || r.Resource == "" || r.SourceKind == "" {
			t.Errorf("row %d is missing an authorization column: %+v", i, r)
		}
		// Same order and identity as the full listing.
		if r.ID != full[i].ID || r.SecretDerived != full[i].SecretDerived || r.Namespace != full[i].Namespace || r.APIGroup != full[i].APIGroup {
			t.Errorf("row %d = %+v; full row = %+v", i, r, full[i])
		}
	}
	var secretRows int
	for _, r := range rows {
		if r.SecretDerived {
			secretRows++
		}
	}
	if secretRows != 1 {
		t.Errorf("%d secret-derived scope rows; want 1", secretRows)
	}
}

func TestValidateEvidenceRow_RedactionCap(t *testing.T) {
	// {"p":"…"} padded to exactly n bytes.
	redaction := func(n int) json.RawMessage {
		return json.RawMessage(`{"p":"` + strings.Repeat("x", n-8) + `"}`)
	}
	r := snapshotRow("k", 64)
	r.Redaction = redaction(EvidenceMaxRedactionBytes)
	if err := ValidateEvidenceRow(r); err != nil {
		t.Errorf("redaction of exactly %d bytes rejected: %v", EvidenceMaxRedactionBytes, err)
	}
	r.Redaction = redaction(EvidenceMaxRedactionBytes + 1)
	if err := ValidateEvidenceRow(r); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("redaction of %d bytes = %v; want ErrIncidentInvalid", EvidenceMaxRedactionBytes+1, err)
	}
	if EvidenceMaxRedactionBytes != 4096 {
		t.Errorf("EvidenceMaxRedactionBytes = %d; the documented cap is 4096", EvidenceMaxRedactionBytes)
	}
}

func TestValidateEvidenceRow_SecretDerivedAgreement(t *testing.T) {
	r := snapshotRow("k", 64)
	r.SecretDerived = true
	r.Redaction = json.RawMessage(`{"secretDerived":true,"rules":["secret-values"]}`)
	if err := ValidateEvidenceRow(r); err != nil {
		t.Errorf("agreeing secret-derived row rejected: %v", err)
	}
	// The column may be stricter than the redaction metadata.
	r.Redaction = json.RawMessage(`{"secretDerived":false}`)
	if err := ValidateEvidenceRow(r); err != nil {
		t.Errorf("secret_derived=true with secretDerived=false metadata rejected: %v", err)
	}
	r.SecretDerived = false
	for _, ok := range []string{`{}`, `{"secretDerived":false}`, `{"secretDerived":null}`, `{"applied":true}`} {
		r.Redaction = json.RawMessage(ok)
		if err := ValidateEvidenceRow(r); err != nil {
			t.Errorf("redaction %s with secret_derived=false rejected: %v", ok, err)
		}
	}
	r.Redaction = json.RawMessage(`{"secretDerived":"yes"}`)
	if err := ValidateEvidenceRow(r); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("non-boolean secretDerived = %v; want ErrIncidentInvalid", err)
	}

	// encoding/json matches keys case-insensitively with the last one
	// winning, while jsonb keeps every spelling: any ambiguity is refused,
	// whichever value the column carries.
	for _, secret := range []bool{false, true} {
		r.SecretDerived = secret
		for _, bad := range []string{
			`{"secretDerived":true,"SecretDerived":false}`,
			`{"secretDerived":true,"secretderived":false}`,
			`{"secretDerived":false,"secretDerived":true}`,
			`{"SecretDerived":false}`,
			`{"SECRETDERIVED":true}`,
			`{"secretDerived":true,"nested":{"x":1},"secretderived":false}`,
		} {
			r.Redaction = json.RawMessage(bad)
			if err := ValidateEvidenceRow(r); !errors.Is(err, ErrIncidentInvalid) {
				t.Errorf("secret_derived=%v, redaction %s = %v; want ErrIncidentInvalid", secret, bad, err)
			}
		}
	}
	// Keys compare after JSON unescaping. bs is one backslash, assembled so
	// that no escape literal appears in this source.
	bs := string(rune(0x5C))
	escapedExact := `"secret` + bs + `u0044erived"`   // decodes to secretDerived
	escapedVariant := `"secret` + bs + `u0064erived"` // decodes to secretderived
	longS := string(rune(0x17F))                      // folds to "s"
	escapedLongS := `"` + bs + `u017fecretDerived"`   // decodes to the long-s spelling
	for _, c := range []struct {
		name   string
		doc    string
		secret bool
		ok     bool
	}{
		{"escaped exact key, true, column true", `{` + escapedExact + `:true}`, true, true},
		{"escaped exact key, true, column false", `{` + escapedExact + `:true}`, false, false},
		{"escaped exact key, false, column false", `{` + escapedExact + `:false}`, false, true},
		{"escaped exact key duplicates the plain one", `{"secretDerived":false,` + escapedExact + `:true}`, true, false},
		{"escaped case variant", `{` + escapedVariant + `:false}`, true, false},
		{"non-ASCII fold variant", `{"` + longS + `ecretDerived":false}`, true, false},
		{"escaped non-ASCII fold variant", `{` + escapedLongS + `:false}`, true, false},
	} {
		r.SecretDerived = c.secret
		r.Redaction = json.RawMessage(c.doc)
		err := ValidateEvidenceRow(r)
		if c.ok && err != nil {
			t.Errorf("%s (%s): rejected: %v", c.name, c.doc, err)
		}
		if !c.ok && !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("%s (%s): = %v; want ErrIncidentInvalid", c.name, c.doc, err)
		}
	}

	// Only the top level is the metadata contract; a nested object may use
	// any key, and an exact key alongside unrelated keys is fine.
	r.SecretDerived = false
	for _, ok := range []string{
		`{"nested":{"secretDerived":true,"SecretDerived":false}}`,
		`{"rules":["secret-values"],"secretDerived":false,"fieldsRemoved":2}`,
	} {
		r.Redaction = json.RawMessage(ok)
		if err := ValidateEvidenceRow(r); err != nil {
			t.Errorf("redaction %s rejected: %v", ok, err)
		}
	}
}

func TestValidateEvidenceJSON_Escapes(t *testing.T) {
	accept := map[string]string{
		"escaped backslash then u0000 text": `{"a":"\\u0000"}`,
		"surrogate pair":                    `{"a":"\ud83d\ude00"}`,
		"surrogate pair upper hex":          `{"a":"\uD83D\uDE00"}`,
		"ordinary escapes":                  `{"a":"\n\t\"\/\u00e9\u0001"}`,
		"key with escape":                   `{"\u00e9":1}`,
		"raw astral UTF-8":                  `{"a":"😀"}`,
		"many backslashes":                  `{"a":"\\\\\\u0000"}`,
	}
	for name, raw := range accept {
		if err := validateEvidenceJSON("payload", json.RawMessage(raw)); err != nil {
			t.Errorf("%s (%s): rejected: %v", name, raw, err)
		}
	}
	reject := map[string]string{
		"NUL escape":                     `{"a":"\u0000"}`,
		"NUL escape after escaped quote": `{"a":"\"\u0000"}`,
		"NUL escape after 3 backslashes": `{"a":"\\\u0000"}`,
		"NUL escape in key":              `{"\u0000":1}`,
		"lone high surrogate":            `{"a":"\ud800"}`,
		"lone high surrogate then text":  `{"a":"\ud800x"}`,
		"high then non-low escape":       `{"a":"\ud800\u0041"}`,
		"high then high":                 `{"a":"\ud800\ud800"}`,
		"lone low surrogate":             `{"a":"\udc00"}`,
		"reversed pair":                  `{"a":"\ude00\ud83d"}`,
		"high then escaped newline":      `{"a":"\ud83d\n"}`,
	}
	for name, raw := range reject {
		if err := validateEvidenceJSON("payload", json.RawMessage(raw)); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("%s (%s): = %v; want ErrIncidentInvalid", name, raw, err)
		}
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

// incidentLockRendezvous holds the first writer's incident read open until
// the second writer's read completes, or 750ms pass. With FOR UPDATE the
// second read blocks on the row lock, so the first proceeds after the timeout
// and the two writers serialize. Without the lock both read the same state
// and race. It keys on match, a fragment of the SELECT text that excludes
// the FOR UPDATE clause, so it still synchronizes when the lock is removed.
type incidentLockRendezvous struct {
	match  string
	mu     sync.Mutex
	reads  int
	second chan struct{}
}

func newIncidentLockRendezvous(match string) *incidentLockRendezvous {
	return &incidentLockRendezvous{match: match, second: make(chan struct{})}
}

type rendezvousSQLKey struct{}

func (r *incidentLockRendezvous) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, rendezvousSQLKey{}, d.SQL)
}

func (r *incidentLockRendezvous) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(rendezvousSQLKey{}).(string)
	if d.Err != nil || !strings.Contains(sql, r.match) {
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

	rv := newIncidentLockRendezvous("evidence_bytes, evidence_count, scope_count")
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

// TestInsertBatchErrorPrecedence pins the documented order, which U23b maps to
// distinct statuses: pre-SQL validation, then (under the lock) not found, not
// owner, closed, then (after the insert) items, incident bytes, scopes. Each
// case violates two adjacent rules at once.
func TestInsertBatchErrorPrecedence(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	intruder := owner + "-intruder"
	open := mustCreateIncident(t, is, newIncident(owner, "open"))
	closed := mustCreateIncident(t, is, newIncident(owner, "closed"))
	if err := is.Update(t.Context(), closed, owner, "closed", "", IncidentStatusClosed); err != nil {
		t.Fatal(err)
	}
	small := EvidenceLimits{MaxItemBytes: 50, MaxIncidentBytes: 30, MaxItems: 1, MaxScopes: 1}
	twoScopes := func() []IncidentEvidenceRow {
		a, b := snapshotRow("p-a", 20), snapshotRow("p-b", 20)
		b.Namespace = "elsewhere"
		return []IncidentEvidenceRow{a, b}
	}
	invalid := snapshotRow("p-x", 10)
	invalid.Mode = "copy"

	cases := []struct {
		name     string
		incident uuid.UUID
		actor    string
		rows     []IncidentEvidenceRow
		limits   EvidenceLimits
		check    func(error) bool
	}{
		{"invalid row beats missing incident", uuid.New(), owner, []IncidentEvidenceRow{invalid}, small,
			func(err error) bool { return errors.Is(err, ErrIncidentInvalid) }},
		// Validation covers every row before any size check: an oversized
		// valid row 0 does not mask an invalid row 1.
		{"invalid later row beats oversized earlier row", open, owner,
			[]IncidentEvidenceRow{snapshotRow("p-big", 51), invalid}, small,
			func(err error) bool {
				return errors.Is(err, ErrIncidentInvalid) && !errors.Is(err, ErrEvidenceLimit) &&
					strings.Contains(err.Error(), "evidence item 1")
			}},
		{"item size beats not owner", open, intruder, []IncidentEvidenceRow{snapshotRow("p-big", 51)}, small,
			func(err error) bool { return isLimit(err, EvidenceLimitItemBytes) }},
		{"not owner beats closed", closed, intruder, []IncidentEvidenceRow{snapshotRow("p-1", 10)}, small,
			func(err error) bool { return errors.Is(err, ErrNotOwner) }},
		{"closed beats item count", closed, owner, twoScopes(), small,
			func(err error) bool { return errors.Is(err, ErrIncidentClosed) }},
		{"item count beats incident bytes", open, owner, twoScopes(), small,
			func(err error) bool { return isLimit(err, EvidenceLimitItems) }},
		{"incident bytes beats scopes", open, owner, twoScopes(),
			EvidenceLimits{MaxItemBytes: 50, MaxIncidentBytes: 30, MaxItems: 10, MaxScopes: 1},
			func(err error) bool { return isLimit(err, EvidenceLimitIncidentBytes) }},
	}
	for _, c := range cases {
		_, err := es.InsertBatch(t.Context(), c.incident, c.actor, c.rows, c.limits)
		if !c.check(err) {
			t.Errorf("%s: got %v", c.name, err)
		}
	}
	requireConsistent(t, readEvidenceTotals(t, pool, open), 0, 0, 0)
	requireConsistent(t, readEvidenceTotals(t, pool, closed), 0, 0, 0)
}

func isLimit(err error, limit string) bool {
	var le *EvidenceLimitError
	return errors.As(err, &le) && le.Limit == limit
}

// TestInsertBatchLoweredLimits: a scope limit lowered below an incident's
// current scope count still admits evidence in scopes it already has and
// rejects only growth of the scope set. Item and byte limits are cumulative
// totals, so a lowered one blocks any growth.
func TestInsertBatchLoweredLimits(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "lowered"))
	seed := make([]IncidentEvidenceRow, 0, 3)
	for i := range 3 {
		r := snapshotRow("seed-"+strconv.Itoa(i), 10)
		r.Namespace = "ns-" + strconv.Itoa(i)
		seed = append(seed, r)
	}
	mustInsert(t, es, incident, owner, ceilingLimits, seed...)
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 30, 3, 3)

	lowered := ceilingLimits
	lowered.MaxScopes = 2
	existing := snapshotRow("existing-scope", 10)
	existing.Namespace = "ns-1"
	if n, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{existing}, lowered); err != nil || n != 1 {
		t.Fatalf("row in an existing scope under a lowered scope limit = (%d, %v); want (1, nil)", n, err)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 40, 4, 3)
	fresh := snapshotRow("new-scope", 10)
	fresh.Namespace = "ns-9"
	_, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{fresh}, lowered)
	var se *ScopeLimitError
	if !errors.As(err, &se) || *se != (ScopeLimitError{Max: 2, Current: 3, Attempted: 4}) {
		t.Fatalf("row in a new scope = %v; want ScopeLimitError{2, 3, 4}", err)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 40, 4, 3)

	// A batch mixing existing and new scopes grows the set: rejected whole.
	again := snapshotRow("existing-again", 10)
	again.Namespace = "ns-2"
	_, err = es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{again, fresh}, lowered)
	if !errors.As(err, &se) || *se != (ScopeLimitError{Max: 2, Current: 3, Attempted: 4}) {
		t.Fatalf("mixed existing+new scopes = %v; want ScopeLimitError{2, 3, 4}", err)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 40, 4, 3)
	// Several rows, all in existing scopes: accepted.
	other := snapshotRow("existing-other", 10)
	other.Namespace = "ns-0"
	if n, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{again, other}, lowered); err != nil || n != 2 {
		t.Fatalf("batch of existing scopes only = (%d, %v); want (2, nil)", n, err)
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 60, 6, 3)

	loweredItems := ceilingLimits
	loweredItems.MaxItems = 2
	if _, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{snapshotRow("more", 10)}, loweredItems); !isLimit(err, EvidenceLimitItems) {
		t.Errorf("growth under a lowered item limit = %v; want an items limit error", err)
	}
	// A fully deduplicated batch is still a no-op under a lowered limit.
	if n, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{existing, again}, loweredItems); err != nil || n != 0 {
		t.Errorf("deduplicated batch under a lowered item limit = (%d, %v); want (0, nil)", n, err)
	}
}

// holdIncidentLock takes the incident row lock in a transaction on its own
// pool and returns a release func; release also runs at cleanup.
func holdIncidentLock(t *testing.T, incident uuid.UUID) func() {
	t.Helper()
	holder := testDB(t)
	tx, err := holder.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM incidents WHERE id = $1 FOR UPDATE`, incident); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }) }
	t.Cleanup(release)
	return release
}

func TestInsertBatchLockTimeoutIsBusy(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "busy"))
	if es.lockTimeout != incidentLockTimeout {
		t.Fatalf("default lock timeout = %s; want incidentLockTimeout", es.lockTimeout)
	}
	es.lockTimeout = 200 * time.Millisecond
	release := holdIncidentLock(t, incident)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := es.InsertBatch(ctx, incident, owner, []IncidentEvidenceRow{snapshotRow("a", 10)}, ceilingLimits)
	if !errors.Is(err, ErrIncidentBusy) {
		t.Fatalf("capture behind a held lock = %v after %s; want ErrIncidentBusy", err, time.Since(start))
	}
	release()
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 0, 0, 0)
	if n := mustInsert(t, es, incident, owner, ceilingLimits, snapshotRow("a", 10)); n != 1 {
		t.Errorf("capture after release inserted %d; want 1", n)
	}
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
// JSON numbers jsonb cannot hold
// ---------------------------------------------------------------------------

// jsonbNumberCases are JSON numbers on both sides of the evidence rendering
// bounds (position <= 308, scale <= 340, |exponent| <= 400). ok is the
// validator's verdict. TestJSONBNumberAcceptsArePostgreSQLAccepts checks that
// the real server stores every number the validator accepts.
var jsonbNumberCases = []struct {
	name string
	num  string
	ok   bool
}{
	{"small", "123.456", true},
	{"zero", "0", true},
	{"negative zero scaled", "-0.0e-5", true},
	{"largest float64", "1.7976931348623157e308", true},
	{"most negative float64", "-1.7976931348623157e308", true},
	{"smallest float64, shortest form", "5e-324", true},
	{"smallest float64, 17 digits", "4.9406564584124654e-324", true},
	{"smallest normal float64", "2.2250738585072014e-308", true},
	{"largest int64", "9223372036854775807", true},
	{"smallest int64", "-9223372036854775808", true},
	{"largest position", "1e308", true},
	{"largest position, digit 9", "9E+308", true},
	{"largest position from a fraction", "0.1e309", true},
	{"largest scale", "1e-340", true},
	{"largest scale from fraction digits", "0." + strings.Repeat("1", 340), true},
	{"309 integer digits", "1" + strings.Repeat("0", 308), true},
	{"exponent at the bound, zero", "0e400", true},
	{"large positive exponent offset by fraction digits", "0." + strings.Repeat("0", 200) + "1e300", true},
	{"position past the bound", "1e309", false},
	{"position past the bound, wide mantissa", "12345e305", false},
	{"position past the bound from a fraction", "0.0001e313", false},
	{"310 integer digits", "1" + strings.Repeat("0", 309), false},
	{"scale past the bound", "1e-341", false},
	{"scale past the bound from a fraction", "0.000001e-335", false},
	{"341 fraction digits", "0." + strings.Repeat("1", 341), false},
	{"scale counts trailing zeros", "1." + strings.Repeat("0", 341), false},
	{"zero past the scale bound", "0.0e-340", false},
	{"1e400", "1e400", false},
	{"1e-400", "1e-400", false},
	{"exponent past the bound, zero", "0e401", false},
	{"exponent past the negative bound", "1" + strings.Repeat("0", 200) + "e-401", false},
	{"PostgreSQL storage limit", "1e131071", false},
	{"zero PostgreSQL rejects", "0e1073741824", false},
	{"exponent beyond int64", "1e99999999999999999999", false},
	{"negative exponent beyond int64", "1e-99999999999999999999", false},
}

func TestValidateEvidenceJSON_Numbers(t *testing.T) {
	for _, c := range jsonbNumberCases {
		for _, doc := range []string{`{"n":` + c.num + `}`, `[1,` + c.num + `]`, c.num} {
			err := validateEvidenceJSON("payload", json.RawMessage(doc))
			if c.ok && err != nil {
				t.Errorf("%s: %.60s rejected: %v", c.name, doc, err)
			}
			if !c.ok && !errors.Is(err, ErrIncidentInvalid) {
				t.Errorf("%s: %.60s = %v; want ErrIncidentInvalid", c.name, doc, err)
			}
		}
	}
	// Digits inside a string are text, not a number, including after an
	// escaped quote (the walk must skip the escape, not end the string).
	for _, doc := range []string{`{"n":"1e131072"}`, `{"m":"x\"1e131072"}`, `["\\",1,"\"1e400\""]`} {
		if err := validateEvidenceJSON("payload", json.RawMessage(doc)); err != nil {
			t.Errorf("number-like text inside a string %s was rejected: %v", doc, err)
		}
	}
}

// TestJSONBNumberAcceptsArePostgreSQLAccepts: the real server stores every
// number the validator accepts (so an accepted capture never fails with a
// driver error), and a capture carrying a rejected number fails as a clean
// validation error with nothing written, including one PostgreSQL would have
// stored (1e131071).
func TestJSONBNumberAcceptsArePostgreSQLAccepts(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	for _, c := range jsonbNumberCases {
		if !c.ok {
			continue
		}
		var ok bool
		if err := pool.QueryRow(t.Context(), `SELECT ('{"n":' || $1 || '}')::jsonb IS NOT NULL`, c.num).Scan(&ok); err != nil {
			t.Errorf("%s: the validator accepts %.40s but PostgreSQL rejects it: %v", c.name, c.num, err)
		}
	}

	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "numbers"))
	for _, num := range []string{"1e131072", "1e131071", "1e309"} {
		r := snapshotRow("number-"+num, 8)
		r.Payload = json.RawMessage(`{"n":` + num + `}`)
		if _, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{r}, ceilingLimits); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("capture with %s = %v; want ErrIncidentInvalid", num, err)
		}
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 0, 0, 0)
}

// TestJSONBNumberRenderingIsBounded stores the worst-case numbers the
// validator admits and measures what jsonb renders back: the readback text
// must stay within 64x payload_bytes, the factor InsertBatch documents.
func TestJSONBNumberRenderingIsBounded(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "rendering"))
	repeat := func(item string, n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(item+",", n), ",") + "]"
	}
	payloads := map[string]string{
		"lone largest position": "1e308",
		"lone negative":         "-1e308",
		"lone largest scale":    "1e-340",
		"array of 1e308":        repeat("1e308", 500),
		"array of 1e-340":       repeat("1e-340", 500),
		"object":                `{"a":1e308,"b":1e-340,"c":-9E+308}`,
		"float64 extremes":      repeat("1.7976931348623157e308,5e-324", 100),
	}
	rows := make([]IncidentEvidenceRow, 0, len(payloads))
	for name, p := range payloads {
		r := snapshotRow(name, 8)
		r.Payload = json.RawMessage(p)
		rows = append(rows, r)
	}
	mustInsert(t, es, incident, owner, ceilingLimits, rows...)

	got, err := pool.Query(t.Context(), `
		SELECT capture_key, payload_bytes, length(payload::text)
		  FROM incident_evidence WHERE incident_id = $1`, incident)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	seen := 0
	for got.Next() {
		var (
			key             string
			stored, readout int
		)
		if err := got.Scan(&key, &stored, &readout); err != nil {
			t.Fatal(err)
		}
		seen++
		if ratio := float64(readout) / float64(stored); ratio > 64 {
			t.Errorf("%s: %d submitted bytes read back as %d (%.1fx); the documented bound is 64x", key, stored, readout, ratio)
		} else {
			t.Logf("%s: %d -> %d bytes (%.1fx)", key, stored, readout, ratio)
		}
	}
	if seen != len(payloads) {
		t.Fatalf("read %d rows; want %d", seen, len(payloads))
	}
}

// TestInsertBatchLockTimeoutIsTransactionLocal: the lock_timeout InsertBatch
// sets must not leak onto the pooled connection. One connection, so every
// query reuses the one InsertBatch ran on.
func TestInsertBatchLockTimeoutIsTransactionLocal(t *testing.T) {
	pool := testDBWithMaxConns(t, 1)
	is, es := NewIncidentStore(pool), NewIncidentEvidenceStore(pool)
	es.lockTimeout = 200 * time.Millisecond
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "local"))

	show := func() string {
		t.Helper()
		var v string
		if err := pool.QueryRow(t.Context(), `SELECT current_setting('lock_timeout')`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	baseline := show()
	if baseline == "200ms" {
		t.Fatalf("baseline lock_timeout is already the test value %q", baseline)
	}
	mustInsert(t, es, incident, owner, ceilingLimits, snapshotRow("a", 10))
	if got := show(); got != baseline {
		t.Errorf("after a capture, lock_timeout = %q; want the session default %q", got, baseline)
	}

	release := holdIncidentLock(t, incident)
	if _, err := es.InsertBatch(t.Context(), incident, owner, []IncidentEvidenceRow{snapshotRow("b", 10)}, ceilingLimits); !errors.Is(err, ErrIncidentBusy) {
		t.Fatalf("capture behind a held lock = %v; want ErrIncidentBusy", err)
	}
	release()
	if got := show(); got != baseline {
		t.Errorf("after a busy capture, lock_timeout = %q; want the session default %q", got, baseline)
	}
}
