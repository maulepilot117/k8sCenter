package store

// change_receipts_test.go — coverage for ChangeReceiptStore and migration
// 000023 (Release E, U27).
//
// Two kinds of test live here:
//
//   - Pure tests (no database): enum/CHECK drift guards, JSON shape, the
//     reflection guard that no manifest content can be persisted, input
//     validation that must fail before any SQL, and migration file shape.
//   - Env-gated tests (testDB / migrationScratchDB, skipped without
//     KUBECENTER_TEST_DATABASE_URL): the write-ordering and recovery
//     behaviour the tracked-apply service (U28) depends on.
//
// Isolation contract (testdb_test.go): the harness never truncates, so every
// row these tests write is scoped by a unique owner id from testOwnerID(t),
// which doubles as the cluster id (change_receipts has no uniqueness beyond
// its client-supplied UUID primary key, so residue is harmless).
// ReconcileOrphans and Cleanup are table-global by design; their tests
// therefore assert on their own rows only and only age their own rows.
//
// testdb_test.go's header table list is not extended for change_receipts /
// change_receipt_grants: owner_id (change_receipts) and the receipt_id FK
// (change_receipt_grants) are the isolation keys, as stated above.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Pure tests
// ---------------------------------------------------------------------------

func TestReceiptQueryParams_Normalize(t *testing.T) {
	tests := []struct {
		name       string
		in         ReceiptQueryParams
		wantPage   int
		wantSize   int
		wantOffset int
	}{
		{"zero value gets defaults", ReceiptQueryParams{}, 1, ReceiptDefaultPageSize, 0},
		{"negative page clamps to 1", ReceiptQueryParams{Page: -3, PageSize: 10}, 1, 10, 0},
		{"negative size gets default", ReceiptQueryParams{Page: 2, PageSize: -1}, 2, ReceiptDefaultPageSize, ReceiptDefaultPageSize},
		{"oversize clamps to max", ReceiptQueryParams{Page: 1, PageSize: 10_000}, 1, ReceiptMaxPageSize, 0},
		{"page 3 of 25", ReceiptQueryParams{Page: 3, PageSize: 25}, 3, 25, 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.in
			q.Normalize()
			if q.Page != tc.wantPage || q.PageSize != tc.wantSize {
				t.Errorf("Normalize() = page %d size %d; want page %d size %d",
					q.Page, q.PageSize, tc.wantPage, tc.wantSize)
			}
			if got := q.Offset(); got != tc.wantOffset {
				t.Errorf("Offset() = %d; want %d", got, tc.wantOffset)
			}
		})
	}
	if ReceiptDefaultPageSize != 50 || ReceiptMaxPageSize != 200 {
		t.Errorf("page-size bounds drifted from audit.QueryParams: default %d max %d",
			ReceiptDefaultPageSize, ReceiptMaxPageSize)
	}
}

// receiptMigrationUp returns the embedded 000023 up migration text.
func receiptMigrationUp(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(migrationsFS, "migrations/000023_create_change_receipts.up.sql")
	if err != nil {
		t.Fatalf("reading 000023 up migration: %v", err)
	}
	return string(b)
}

// checkSetFor extracts the quoted members of `<column> TEXT ... CHECK (<column> IN (...))`.
func checkSetFor(t *testing.T, sql, column string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)` + column + `\s+TEXT[^,]*?CHECK\s*\(\s*` + column + `\s+IN\s*\(([^)]*)\)`)
	m := re.FindStringSubmatch(sql)
	if m == nil {
		t.Fatalf("no CHECK (%s IN (...)) found in the 000023 up migration", column)
	}
	var out []string
	for _, member := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, member[1])
	}
	sort.Strings(out)
	return out
}

func TestReceiptState_CheckConstraintSetsMatchGoConstants(t *testing.T) {
	want := []string{
		string(ReceiptPreviewed), string(ReceiptApplying), string(ReceiptApplied),
		string(ReceiptPartial), string(ReceiptFailed), string(ReceiptUnknown),
	}
	sort.Strings(want)
	if got := checkSetFor(t, receiptMigrationUp(t), "state"); !reflect.DeepEqual(got, want) {
		t.Errorf("state CHECK set = %v; Go constants = %v", got, want)
	}
}

func TestVerificationState_CheckConstraintSetsMatchGoConstants(t *testing.T) {
	want := []string{
		string(VerifyPending), string(VerifyVerifying), string(VerifyVerified),
		string(VerifyInconclusive), string(VerifyFailed),
	}
	sort.Strings(want)
	if got := checkSetFor(t, receiptMigrationUp(t), "verification_state"); !reflect.DeepEqual(got, want) {
		t.Errorf("verification_state CHECK set = %v; Go constants = %v", got, want)
	}
	for _, v := range []VerificationState{VerifyPending, VerifyVerifying, VerifyVerified, VerifyInconclusive, VerifyFailed} {
		if !v.IsValid() {
			t.Errorf("%q.IsValid() = false", v)
		}
	}
	if VerificationState("bogus").IsValid() {
		t.Error(`"bogus".IsValid() = true`)
	}
}

func TestReceiptState_TerminalSet(t *testing.T) {
	terminal := map[ReceiptState]bool{
		ReceiptApplied: true, ReceiptPartial: true, ReceiptFailed: true, ReceiptUnknown: true,
		ReceiptPreviewed: false, ReceiptApplying: false, ReceiptState("bogus"): false,
	}
	for s, want := range terminal {
		if got := s.IsTerminal(); got != want {
			t.Errorf("%q.IsTerminal() = %v; want %v", s, got, want)
		}
	}
}

func TestReceiptObject_MarshalOmitsEmptyOptionalFields(t *testing.T) {
	at := time.Date(2026, 9, 10, 13, 15, 2, 0, time.UTC)
	b, err := json.Marshal(ReceiptObject{Index: 0, Kind: "Namespace", Name: "prod", Action: "created", RecordedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"index": float64(0), "kind": "Namespace", "name": "prod",
		"action": "created", "recordedAt": "2026-09-10T13:15:02Z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("marshalled shape = %v; want %v", got, want)
	}

	full, err := json.Marshal(ReceiptObject{
		Index: 1, Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment",
		Namespace: "prod", Name: "web", UID: "u", Action: "failed", Error: "conflict", RecordedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"group", "version", "resource", "namespace", "uid", "error"} {
		if !strings.Contains(string(full), `"`+key+`"`) {
			t.Errorf("populated object is missing %q: %s", key, full)
		}
	}
}

// manifestFieldPattern matches field names that could carry request content.
var manifestFieldPattern = regexp.MustCompile(`(?i)(data|stringdata|spec|manifest|content|body|yaml)`)

// assertNoManifestFields fails when any exported field of typ could hold
// manifest content. allowed names fields that match the pattern by name but are
// content-free by construction (documented at the call site).
func assertNoManifestFields(t *testing.T, typ reflect.Type, allowed map[string]reflect.Kind) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if manifestFieldPattern.MatchString(f.Name) {
			kind, ok := allowed[f.Name]
			if !ok || f.Type.Kind() != kind {
				t.Errorf("%s.%s (%s) can hold manifest content; a receipt must never persist request content",
					typ.Name(), f.Name, f.Type)
			}
		}
		// A free-form map or interface would let content in under any name.
		switch f.Type.Kind() {
		case reflect.Map, reflect.Interface:
			t.Errorf("%s.%s is a %s; free-form fields can smuggle manifest content", typ.Name(), f.Name, f.Type.Kind())
		}
	}
}

func TestReceiptObject_NeverCarriesManifestFields(t *testing.T) {
	assertNoManifestFields(t, reflect.TypeOf(ReceiptObject{}), nil)
}

func TestChangeReceipt_NeverCarriesManifestFields(t *testing.T) {
	// ContentDigest is a "sha256:<hex>" string: the only trace of the request
	// body, and not reversible to it. Ownership and Verification are opaque
	// JSON (OwnershipResult and CheckResult arrays) and are the only raw-JSON
	// fields; adding a third one must be a conscious edit to this list.
	assertNoManifestFields(t, reflect.TypeOf(ChangeReceipt{}), map[string]reflect.Kind{
		"ContentDigest": reflect.String,
	})

	rawJSON := reflect.TypeOf(json.RawMessage(nil))
	var raw []string
	typ := reflect.TypeOf(ChangeReceipt{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type == rawJSON {
			raw = append(raw, typ.Field(i).Name)
		}
	}
	sort.Strings(raw)
	if want := []string{"Ownership", "Verification"}; !reflect.DeepEqual(raw, want) {
		t.Errorf("raw-JSON fields on ChangeReceipt = %v; want exactly %v", raw, want)
	}
}

func TestCleanup_RejectsInvalidRetention(t *testing.T) {
	// A nil pool would panic if Cleanup reached SQL, so passing proves the
	// validation runs first.
	s := NewChangeReceiptStore(nil)
	for _, days := range []int{0, -1, -30} {
		n, err := s.Cleanup(context.Background(), days)
		if err == nil {
			t.Errorf("Cleanup(%d) returned no error", days)
		}
		if n != 0 {
			t.Errorf("Cleanup(%d) = %d rows; want 0", days, n)
		}
	}
}

func TestNewChangeReceiptStore_NilPoolIsConstructible(t *testing.T) {
	if s := NewChangeReceiptStore(nil); s == nil {
		t.Fatal("NewChangeReceiptStore(nil) returned nil; callers nil-guard the store, not the pool")
	}
}

func TestChangeReceiptStore_RejectsInvalidInputBeforeSQL(t *testing.T) {
	s := NewChangeReceiptStore(nil) // nil pool: reaching SQL would panic
	ctx := context.Background()
	valid := ChangeReceipt{ID: uuid.New(), OwnerID: "o", ContentDigest: "sha256:abc", DocumentCount: 1}

	mutate := func(f func(*ChangeReceipt)) ChangeReceipt { r := valid; f(&r); return r }
	inserts := map[string]ChangeReceipt{
		"nil id":           mutate(func(r *ChangeReceipt) { r.ID = uuid.Nil }),
		"no owner":         mutate(func(r *ChangeReceipt) { r.OwnerID = "" }),
		"no digest":        mutate(func(r *ChangeReceipt) { r.ContentDigest = "" }),
		"negative count":   mutate(func(r *ChangeReceipt) { r.DocumentCount = -1 }),
		"terminal state":   mutate(func(r *ChangeReceipt) { r.State = ReceiptApplied }),
		"bad verification": mutate(func(r *ChangeReceipt) { r.VerificationState = "bogus" }),
		"bad ownership":    mutate(func(r *ChangeReceipt) { r.Ownership = json.RawMessage(`{not json`) }),
	}
	for name, r := range inserts {
		if err := s.Insert(ctx, r); !errors.Is(err, ErrReceiptInvalid) {
			t.Errorf("Insert(%s) = %v; want ErrReceiptInvalid", name, err)
		}
	}
	if err := s.Finalize(ctx, uuid.New(), ReceiptApplying); !errors.Is(err, ErrReceiptInvalid) {
		t.Errorf("Finalize(applying) = %v; want ErrReceiptInvalid", err)
	}
	if err := s.SetVerification(ctx, uuid.New(), "bogus", nil); !errors.Is(err, ErrReceiptInvalid) {
		t.Errorf("SetVerification(bogus) = %v; want ErrReceiptInvalid", err)
	}
	if _, _, err := s.ListForOwner(ctx, ReceiptQueryParams{}); !errors.Is(err, ErrReceiptInvalid) {
		t.Errorf("ListForOwner(no owner) = %v; want ErrReceiptInvalid", err)
	}
	for _, age := range []time.Duration{0, -time.Minute} {
		if _, err := s.ReconcileOrphans(ctx, age); !errors.Is(err, ErrReceiptInvalid) {
			t.Errorf("ReconcileOrphans(%s) = %v; want ErrReceiptInvalid", age, err)
		}
	}
}

func TestVerificationState_IsFinalAndIsValid(t *testing.T) {
	want := map[VerificationState]struct{ valid, final bool }{
		VerifyPending:      {true, false},
		VerifyVerifying:    {true, false},
		VerifyVerified:     {true, true},
		VerifyInconclusive: {true, true},
		VerifyFailed:       {true, true},
		"":                 {false, false},
		"bogus":            {false, false},
	}
	for v, w := range want {
		if v.IsValid() != w.valid || v.IsFinal() != w.final {
			t.Errorf("%q: IsValid=%v IsFinal=%v; want %v/%v", v, v.IsValid(), v.IsFinal(), w.valid, w.final)
		}
	}
}

func TestJSONArrayOrEmpty(t *testing.T) {
	accepted := map[string]string{
		"":           "[]",
		"   \n\t":    "[]",
		"null":       "[]",
		"  null  ":   "[]",
		"[]":         "[]",
		"  [ ]  ":    "[ ]",
		`[{"a":1}]`:  `[{"a":1}]`,
		"\n[1, 2]\n": "[1, 2]",
	}
	for in, want := range accepted {
		got, err := jsonArrayOrEmpty(json.RawMessage(in), "f")
		if err != nil || string(got) != want {
			t.Errorf("jsonArrayOrEmpty(%q) = (%q, %v); want (%q, nil)", in, got, err, want)
		}
	}
	for _, in := range []string{`{}`, `{"a":1}`, `"x"`, `1`, `true`, `[1,`, `[1] x`, `nul`} {
		if _, err := jsonArrayOrEmpty(json.RawMessage(in), "f"); !errors.Is(err, ErrReceiptInvalid) {
			t.Errorf("jsonArrayOrEmpty(%q) = %v; want ErrReceiptInvalid", in, err)
		}
	}
}

func TestMigration000023_UpAndDownAreWellFormed(t *testing.T) {
	up := receiptMigrationUp(t)
	down, err := fs.ReadFile(migrationsFS, "migrations/000023_create_change_receipts.down.sql")
	if err != nil {
		t.Fatalf("reading 000023 down migration: %v", err)
	}

	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS change_receipts",
		"CREATE TABLE IF NOT EXISTS change_receipt_grants",
		"idx_change_receipts_owner_created",
		"idx_change_receipts_cluster_created",
		"idx_change_receipts_unfinished",
		"idx_change_receipt_grants_grantee",
		"REFERENCES change_receipts(id) ON DELETE CASCADE",
		"COMMENT ON TABLE change_receipts",
		"COMMENT ON TABLE change_receipt_grants",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("000023 up migration is missing %q", want)
		}
	}
	// The generation comment must describe the YAML target pin, not
	// clusters.updated_at (the plan's original, superseded wording).
	if strings.Contains(up, "clusters.updated_at") {
		t.Error("000023 describes cluster_generation as clusters.updated_at; it is the YAML target pin generation")
	}
	// document_count deliberately has no default.
	if regexp.MustCompile(`document_count\s+INTEGER NOT NULL DEFAULT`).MatchString(up) {
		t.Error("document_count must not have a default: a receipt without a count is meaningless")
	}

	for _, want := range []string{"DROP TABLE IF EXISTS change_receipts", "DROP TABLE IF EXISTS change_receipt_grants"} {
		if !strings.Contains(string(down), want) {
			t.Errorf("000023 down migration is missing %q", want)
		}
	}

	// 000023 is the only migration that introduces these tables.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	var creators []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		b, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "CREATE TABLE IF NOT EXISTS change_receipts") {
			creators = append(creators, e.Name())
		}
	}
	if want := []string{"000023_create_change_receipts.up.sql"}; !reflect.DeepEqual(creators, want) {
		t.Errorf("migrations creating change_receipts = %v; want %v", creators, want)
	}
}

func TestMigration000023_DownDropsGrantsBeforeReceipts(t *testing.T) {
	down, err := fs.ReadFile(migrationsFS, "migrations/000023_create_change_receipts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(down)
	grants := strings.Index(s, "DROP TABLE IF EXISTS change_receipt_grants")
	receipts := strings.Index(s, "DROP TABLE IF EXISTS change_receipts")
	if grants < 0 || receipts < 0 || grants > receipts {
		t.Errorf("the grants table references change_receipts, so it must be dropped first (grants at %d, receipts at %d)",
			grants, receipts)
	}
}

// ---------------------------------------------------------------------------
// Env-gated PostgreSQL tests
// ---------------------------------------------------------------------------

func newReceiptStore(t *testing.T) (*ChangeReceiptStore, *pgxpool.Pool) {
	t.Helper()
	pool := testDB(t)
	return NewChangeReceiptStore(pool), pool
}

// newReceipt returns an insertable receipt scoped to owner (and to a cluster id
// equal to owner, so cluster filters stay private to the test).
func newReceipt(owner string) ChangeReceipt {
	return ChangeReceipt{
		ID:                uuid.New(),
		OwnerID:           owner,
		OwnerUsername:     "alice",
		ClusterID:         owner,
		ClusterGeneration: "local",
		ContentDigest:     "sha256:" + strings.Repeat("ab", 32),
		DocumentCount:     2,
	}
}

func mustInsertReceipt(t *testing.T, s *ChangeReceiptStore, r ChangeReceipt) ChangeReceipt {
	t.Helper()
	if err := s.Insert(t.Context(), r); err != nil {
		t.Fatalf("Insert(%s): %v", r.ID, err)
	}
	return r
}

func mustGetReceipt(t *testing.T, s *ChangeReceiptStore, id uuid.UUID) *ChangeReceipt {
	t.Helper()
	got, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	if got == nil {
		t.Fatalf("Get(%s) = nil; want the row", id)
	}
	return got
}

func sampleObject(index int, name, action string) ReceiptObject {
	return ReceiptObject{
		Index: index, Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment",
		Namespace: "prod", Name: name, UID: "uid-" + name, Action: action,
	}
}

func jsonEqual(t *testing.T, got json.RawMessage, wantJSON string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("decoding stored JSON %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &w); err != nil {
		t.Fatalf("decoding expected JSON %q: %v", wantJSON, err)
	}
	return reflect.DeepEqual(g, w)
}

func TestChangeReceiptStore_InsertAndGet_RoundTrips(t *testing.T) {
	s, _ := newReceiptStore(t)
	owner := testOwnerID(t)
	repairOf := uuid.New()

	in := newReceipt(owner)
	in.Force = true
	in.ContainsSecret = true
	in.RepairOf = &repairOf
	in.Ownership = json.RawMessage(`[{"kind":"Deployment","owner":"argocd"}]`)
	mustInsertReceipt(t, s, in)

	got := mustGetReceipt(t, s, in.ID)
	if got.OwnerID != owner || got.OwnerUsername != "alice" || got.ClusterID != owner ||
		got.ClusterGeneration != "local" || got.ContentDigest != in.ContentDigest ||
		got.DocumentCount != 2 || !got.Force || !got.ContainsSecret {
		t.Errorf("round-tripped scalars wrong: %+v", got)
	}
	if got.RepairOf == nil || *got.RepairOf != repairOf {
		t.Errorf("RepairOf = %v; want %v", got.RepairOf, repairOf)
	}
	if got.State != ReceiptApplying || got.VerificationState != VerifyPending {
		t.Errorf("defaults: state %q verification %q; want applying/pending", got.State, got.VerificationState)
	}
	if got.MutationStartedAt != nil || got.CompletedAt != nil || got.VerifiedAt != nil {
		t.Errorf("a fresh receipt must have no mutation/completion/verification stamp: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at was not stamped")
	}
	if len(got.Objects) != 0 {
		t.Errorf("Objects = %v; want empty", got.Objects)
	}
	if !jsonEqual(t, got.Verification, `[]`) {
		t.Errorf("Verification = %s; want []", got.Verification)
	}
	if !jsonEqual(t, got.Ownership, `[{"kind":"Deployment","owner":"argocd"}]`) {
		t.Errorf("Ownership = %s; round-trip lost data", got.Ownership)
	}
}

func TestChangeReceiptStore_InsertDefaultsClusterToLocal(t *testing.T) {
	s, _ := newReceiptStore(t)
	r := newReceipt(testOwnerID(t))
	r.ClusterID = ""
	mustInsertReceipt(t, s, r)
	if got := mustGetReceipt(t, s, r.ID); got.ClusterID != "local" {
		t.Errorf("ClusterID = %q; want local", got.ClusterID)
	}
}

func TestChangeReceiptStore_GetAbsentReturnsNilNil(t *testing.T) {
	s, _ := newReceiptStore(t)
	got, err := s.Get(t.Context(), uuid.New())
	if err != nil || got != nil {
		t.Errorf("Get(absent) = (%v, %v); want (nil, nil)", got, err)
	}
}

func TestChangeReceiptStore_DuplicateOperationID_ReturnsErrReceiptExists(t *testing.T) {
	s, _ := newReceiptStore(t)
	owner := testOwnerID(t)
	first := mustInsertReceipt(t, s, newReceipt(owner))

	// Same id, different owner and digest: the primary key alone decides, and
	// the first row must be left exactly as it was.
	dup := newReceipt("someone-else-" + owner)
	dup.ID = first.ID
	dup.ContentDigest = "sha256:" + strings.Repeat("cd", 32)
	if err := s.Insert(t.Context(), dup); !errors.Is(err, ErrReceiptExists) {
		t.Fatalf("duplicate Insert = %v; want ErrReceiptExists", err)
	}
	got := mustGetReceipt(t, s, first.ID)
	if got.OwnerID != owner || got.ContentDigest != first.ContentDigest {
		t.Errorf("the original row was altered by a rejected duplicate: %+v", got)
	}
}

func TestChangeReceiptStore_MarkMutationStarted_GuardedAndIdempotent(t *testing.T) {
	s, _ := newReceiptStore(t)
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))

	if err := s.MarkMutationStarted(t.Context(), r.ID); err != nil {
		t.Fatalf("first MarkMutationStarted: %v", err)
	}
	first := mustGetReceipt(t, s, r.ID).MutationStartedAt
	if first == nil {
		t.Fatal("mutation_started_at not stamped")
	}

	time.Sleep(20 * time.Millisecond)
	if err := s.MarkMutationStarted(t.Context(), r.ID); err != nil {
		t.Fatalf("second MarkMutationStarted (idempotent): %v", err)
	}
	second := mustGetReceipt(t, s, r.ID).MutationStartedAt
	if second == nil || !second.Equal(*first) {
		t.Errorf("the stamp moved on a repeat call: %v -> %v", first, second)
	}

	if err := s.MarkMutationStarted(t.Context(), uuid.New()); !errors.Is(err, ErrReceiptNotFound) {
		t.Errorf("MarkMutationStarted(unknown id) = %v; want ErrReceiptNotFound", err)
	}
}

// backdateReceipt moves a receipt's created_at into the past so the age-bounded
// reconciler considers it orphaned. Only ever called on the test's own rows.
func backdateReceipt(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, age time.Duration) {
	t.Helper()
	if _, err := pool.Exec(t.Context(),
		`UPDATE change_receipts SET created_at = NOW() - make_interval(secs => $2) WHERE id = $1`,
		id, age.Seconds()); err != nil {
		t.Fatalf("backdating receipt %s: %v", id, err)
	}
}

func TestChangeReceiptStore_MarkMutationStarted_RefusesReconciledRow(t *testing.T) {
	s, pool := newReceiptStore(t)
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))

	backdateReceipt(t, pool, r.ID, 2*ReceiptOrphanGrace)
	if _, err := s.ReconcileOrphans(t.Context(), ReceiptOrphanGrace); err != nil {
		t.Fatal(err)
	}
	if got := mustGetReceipt(t, s, r.ID); got.State != ReceiptFailed {
		t.Fatalf("setup: state = %q; want failed", got.State)
	}

	// A late caller must not be able to open the mutation window on a row that
	// was already declared "never started".
	if err := s.MarkMutationStarted(t.Context(), r.ID); !errors.Is(err, ErrReceiptAlreadyFinal) {
		t.Fatalf("MarkMutationStarted on a reconciled row = %v; want ErrReceiptAlreadyFinal", err)
	}
	if got := mustGetReceipt(t, s, r.ID); got.MutationStartedAt != nil {
		t.Errorf("a reconciled row was stamped mutation_started_at = %v", got.MutationStartedAt)
	}
}

func TestChangeReceiptStore_AppendObject_BuildsOrderedPrefix(t *testing.T) {
	s, _ := newReceiptStore(t)
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	ctx := t.Context()

	for i, name := range []string{"a", "b", "c"} {
		action := "configured"
		if i == 2 {
			action = "failed"
		}
		o := sampleObject(i, name, action)
		if i == 2 {
			o.Error = "conflict"
		}
		if err := s.AppendObject(ctx, r.ID, o); err != nil {
			t.Fatalf("AppendObject(%d): %v", i, err)
		}
		// After every append the stored list is exactly the prefix so far,
		// which is what a crash at that point would leave behind.
		got := mustGetReceipt(t, s, r.ID).Objects
		if len(got) != i+1 {
			t.Fatalf("after append %d: %d objects; want %d", i, len(got), i+1)
		}
		for j := range got {
			if got[j].Index != j {
				t.Errorf("after append %d: objects[%d].Index = %d", i, j, got[j].Index)
			}
		}
	}

	got := mustGetReceipt(t, s, r.ID).Objects
	if got[0].Name != "a" || got[1].Name != "b" || got[2].Name != "c" {
		t.Errorf("order not preserved: %v", got)
	}
	if got[2].Error != "conflict" || got[2].Action != "failed" {
		t.Errorf("failure outcome lost: %+v", got[2])
	}
	if got[0].UID != "uid-a" || got[0].Resource != "deployments" || got[0].RecordedAt.IsZero() {
		t.Errorf("object fields not round-tripped (RecordedAt defaults to now): %+v", got[0])
	}
}

func TestChangeReceiptStore_AppendObject_RefusesTerminalAndUnknownRows(t *testing.T) {
	s, _ := newReceiptStore(t)
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	ctx := t.Context()

	if err := s.AppendObject(ctx, r.ID, sampleObject(0, "a", "created")); err != nil {
		t.Fatal(err)
	}
	if err := s.Finalize(ctx, r.ID, ReceiptApplied); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendObject(ctx, r.ID, sampleObject(1, "b", "created")); !errors.Is(err, ErrReceiptAlreadyFinal) {
		t.Errorf("AppendObject after Finalize = %v; want ErrReceiptAlreadyFinal", err)
	}
	if got := mustGetReceipt(t, s, r.ID).Objects; len(got) != 1 {
		t.Errorf("a finalized receipt was modified: %d objects", len(got))
	}
	if err := s.AppendObject(ctx, uuid.New(), sampleObject(0, "a", "created")); !errors.Is(err, ErrReceiptNotFound) {
		t.Errorf("AppendObject(unknown id) = %v; want ErrReceiptNotFound", err)
	}
}

func TestChangeReceiptStore_Finalize_GuardedAgainstRefinalize(t *testing.T) {
	s, _ := newReceiptStore(t)
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	ctx := t.Context()

	if err := s.Finalize(ctx, r.ID, ReceiptPartial); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	got := mustGetReceipt(t, s, r.ID)
	if got.State != ReceiptPartial || got.CompletedAt == nil {
		t.Fatalf("after Finalize: state %q completed_at %v", got.State, got.CompletedAt)
	}
	completed := *got.CompletedAt

	if err := s.Finalize(ctx, r.ID, ReceiptApplied); !errors.Is(err, ErrReceiptAlreadyFinal) {
		t.Errorf("second Finalize = %v; want ErrReceiptAlreadyFinal", err)
	}
	got = mustGetReceipt(t, s, r.ID)
	if got.State != ReceiptPartial || !got.CompletedAt.Equal(completed) {
		t.Errorf("a finalized row was rewritten: state %q completed_at %v", got.State, got.CompletedAt)
	}
	if err := s.Finalize(ctx, uuid.New(), ReceiptApplied); !errors.Is(err, ErrReceiptNotFound) {
		t.Errorf("Finalize(unknown id) = %v; want ErrReceiptNotFound", err)
	}
}

func TestChangeReceiptStore_Finalize_DoesNotResurrectReconciledRow(t *testing.T) {
	s, pool := newReceiptStore(t)
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	if err := s.MarkMutationStarted(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	backdateReceipt(t, pool, r.ID, 2*ReceiptOrphanGrace)
	if _, err := s.ReconcileOrphans(t.Context(), ReceiptOrphanGrace); err != nil {
		t.Fatal(err)
	}
	// The process that owned this apply is gone; a late Finalize must not
	// overwrite the honest "unknown".
	if err := s.Finalize(t.Context(), r.ID, ReceiptApplied); !errors.Is(err, ErrReceiptAlreadyFinal) {
		t.Fatalf("Finalize on a reconciled row = %v; want ErrReceiptAlreadyFinal", err)
	}
	if got := mustGetReceipt(t, s, r.ID); got.State != ReceiptUnknown {
		t.Errorf("state = %q; want unknown", got.State)
	}
}

func TestChangeReceiptStore_SetVerificationAndOwnership(t *testing.T) {
	s, _ := newReceiptStore(t)
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	ctx := t.Context()

	// verifying is not an outcome: verified_at stays unset.
	if err := s.SetVerification(ctx, r.ID, VerifyVerifying, nil); err != nil {
		t.Fatal(err)
	}
	got := mustGetReceipt(t, s, r.ID)
	if got.VerificationState != VerifyVerifying || got.VerifiedAt != nil {
		t.Errorf("verifying: state %q verified_at %v", got.VerificationState, got.VerifiedAt)
	}
	if !jsonEqual(t, got.Verification, `[]`) {
		t.Errorf("nil payload stored %s; want []", got.Verification)
	}

	payload := json.RawMessage(`[{"checkId":"workload.rollout-complete","status":"inconclusive","reason":"kind_not_supported"}]`)
	if err := s.SetVerification(ctx, r.ID, VerifyInconclusive, payload); err != nil {
		t.Fatal(err)
	}
	got = mustGetReceipt(t, s, r.ID)
	if got.VerificationState != VerifyInconclusive || got.VerifiedAt == nil {
		t.Errorf("inconclusive: state %q verified_at %v; want stamped", got.VerificationState, got.VerifiedAt)
	}
	if !jsonEqual(t, got.Verification, string(payload)) {
		t.Errorf("verification payload = %s; want %s", got.Verification, payload)
	}
	// Verification is independent of apply state.
	if got.State != ReceiptApplying {
		t.Errorf("SetVerification changed state to %q", got.State)
	}

	if err := s.SetOwnership(ctx, r.ID, json.RawMessage(`[{"owner":"flux"}]`)); err != nil {
		t.Fatal(err)
	}
	if got = mustGetReceipt(t, s, r.ID); !jsonEqual(t, got.Ownership, `[{"owner":"flux"}]`) {
		t.Errorf("ownership = %s", got.Ownership)
	}

	if err := s.SetVerification(ctx, uuid.New(), VerifyVerified, nil); !errors.Is(err, ErrReceiptNotFound) {
		t.Errorf("SetVerification(unknown id) = %v; want ErrReceiptNotFound", err)
	}
	if err := s.SetOwnership(ctx, uuid.New(), nil); !errors.Is(err, ErrReceiptNotFound) {
		t.Errorf("SetOwnership(unknown id) = %v; want ErrReceiptNotFound", err)
	}
}

func TestChangeReceiptStore_ReconcileOrphans_SplitsFailedFromUnknown(t *testing.T) {
	s, pool := newReceiptStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	neverStarted := mustInsertReceipt(t, s, newReceipt(owner))

	midApply := mustInsertReceipt(t, s, newReceipt(owner))
	if err := s.MarkMutationStarted(ctx, midApply.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendObject(ctx, midApply.ID, sampleObject(0, "a", "created")); err != nil {
		t.Fatal(err)
	}

	done := mustInsertReceipt(t, s, newReceipt(owner))
	if err := s.MarkMutationStarted(ctx, done.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Finalize(ctx, done.ID, ReceiptApplied); err != nil {
		t.Fatal(err)
	}
	doneBefore := mustGetReceipt(t, s, done.ID)

	// Every row is old enough to be reaped, so only the state filter decides.
	for _, r := range []ChangeReceipt{neverStarted, midApply, done} {
		backdateReceipt(t, pool, r.ID, 2*ReceiptOrphanGrace)
	}

	n, err := s.ReconcileOrphans(ctx, ReceiptOrphanGrace)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}
	// Table-global: other suites' in-flight rows may also be reaped, so only
	// a lower bound is meaningful.
	if n < 2 {
		t.Errorf("ReconcileOrphans touched %d rows; want at least our 2", n)
	}

	nsGot := mustGetReceipt(t, s, neverStarted.ID)
	if nsGot.State != ReceiptFailed || nsGot.VerificationState != VerifyInconclusive ||
		nsGot.CompletedAt == nil || nsGot.MutationStartedAt != nil || nsGot.VerifiedAt == nil {
		t.Errorf("never-started row: %+v; want failed/inconclusive, completed, still no mutation stamp", nsGot)
	}

	maGot := mustGetReceipt(t, s, midApply.ID)
	if maGot.State != ReceiptUnknown || maGot.VerificationState != VerifyInconclusive ||
		maGot.CompletedAt == nil || maGot.VerifiedAt == nil {
		t.Errorf("mid-apply row: %+v; want unknown/inconclusive, completed", maGot)
	}
	if len(maGot.Objects) != 1 {
		t.Errorf("reconciliation lost the recorded prefix: %v", maGot.Objects)
	}

	doneAfter := mustGetReceipt(t, s, done.ID)
	if doneAfter.State != ReceiptApplied || doneAfter.VerificationState != VerifyPending ||
		doneAfter.VerifiedAt != nil || !doneAfter.CompletedAt.Equal(*doneBefore.CompletedAt) {
		t.Errorf("a terminal row was touched: before %+v after %+v", doneBefore, doneAfter)
	}

	// Idempotent for our rows: a second sweep must not move them again.
	if _, err := s.ReconcileOrphans(ctx, ReceiptOrphanGrace); err != nil {
		t.Fatal(err)
	}
	if again := mustGetReceipt(t, s, midApply.ID); !again.CompletedAt.Equal(*maGot.CompletedAt) {
		t.Errorf("second sweep moved completed_at: %v -> %v", maGot.CompletedAt, again.CompletedAt)
	}

	// And the unfinished-row recovery index is what makes the scan cheap.
	var unfinished int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM change_receipts WHERE owner_id = $1 AND completed_at IS NULL`, owner).Scan(&unfinished); err != nil {
		t.Fatal(err)
	}
	if unfinished != 0 {
		t.Errorf("%d of our rows are still unfinished after reconciliation", unfinished)
	}
}

// TestChangeReceiptStore_ReconcileOrphans_LeavesLiveRowAlone is the rolling
// update case: a new pod reconciles at boot while the old pod is still
// applying. A row younger than the grace must be left exactly as it is, and
// the old pod's remaining writes must still succeed.
func TestChangeReceiptStore_ReconcileOrphans_LeavesLiveRowAlone(t *testing.T) {
	s, pool := newReceiptStore(t)
	ctx := t.Context()
	live := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	if err := s.MarkMutationStarted(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendObject(ctx, live.ID, sampleObject(0, "a", "created")); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReconcileOrphans(ctx, ReceiptOrphanGrace); err != nil {
		t.Fatal(err)
	}
	got := mustGetReceipt(t, s, live.ID)
	if got.State != ReceiptApplying || got.CompletedAt != nil || got.VerifiedAt != nil ||
		got.VerificationState != VerifyPending || len(got.Objects) != 1 {
		t.Fatalf("a live in-flight row was reaped or altered: %+v", got)
	}

	// The old pod carries on and finishes normally.
	if err := s.AppendObject(ctx, live.ID, sampleObject(1, "b", "created")); err != nil {
		t.Errorf("AppendObject after a boot-time reconcile: %v", err)
	}
	if err := s.Finalize(ctx, live.ID, ReceiptApplied); err != nil {
		t.Errorf("Finalize after a boot-time reconcile: %v", err)
	}
	if got = mustGetReceipt(t, s, live.ID); got.State != ReceiptApplied || len(got.Objects) != 2 {
		t.Errorf("after finishing: state %q, %d objects; want applied, 2", got.State, len(got.Objects))
	}

	// A second live row, reaped once it ages past the grace: unknown, and the
	// owning process's late writes are refused rather than resurrecting it.
	late := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	if err := s.MarkMutationStarted(ctx, late.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendObject(ctx, late.ID, sampleObject(0, "a", "created")); err != nil {
		t.Fatal(err)
	}
	backdateReceipt(t, pool, late.ID, 2*ReceiptOrphanGrace)
	if _, err := s.ReconcileOrphans(ctx, ReceiptOrphanGrace); err != nil {
		t.Fatal(err)
	}
	lateGot := mustGetReceipt(t, s, late.ID)
	if lateGot.State != ReceiptUnknown || lateGot.CompletedAt == nil || len(lateGot.Objects) != 1 {
		t.Fatalf("an aged-out in-flight row: %+v; want unknown, completed, prefix kept", lateGot)
	}
	if err := s.AppendObject(ctx, late.ID, sampleObject(1, "b", "created")); !errors.Is(err, ErrReceiptAlreadyFinal) {
		t.Errorf("AppendObject on a reaped row = %v; want ErrReceiptAlreadyFinal", err)
	}
	if err := s.Finalize(ctx, late.ID, ReceiptApplied); !errors.Is(err, ErrReceiptAlreadyFinal) {
		t.Errorf("Finalize on a reaped row = %v; want ErrReceiptAlreadyFinal", err)
	}
}

func TestChangeReceiptStore_ReconcileOrphans_SkipsPreviewedRows(t *testing.T) {
	s, pool := newReceiptStore(t)
	ctx := t.Context()
	r := newReceipt(testOwnerID(t))
	r.State = ReceiptPreviewed
	mustInsertReceipt(t, s, r)
	backdateReceipt(t, pool, r.ID, 2*ReceiptOrphanGrace)

	if _, err := s.ReconcileOrphans(ctx, ReceiptOrphanGrace); err != nil {
		t.Fatal(err)
	}
	got := mustGetReceipt(t, s, r.ID)
	if got.State != ReceiptPreviewed || got.CompletedAt != nil || got.VerificationState != VerifyPending {
		t.Errorf("a previewed row is not an orphaned mutation and must be left alone: %+v", got)
	}
}

func TestChangeReceiptStore_SetVerification_DoesNotReopenFinalOutcome(t *testing.T) {
	s, pool := newReceiptStore(t)
	ctx := t.Context()

	for _, final := range []VerificationState{VerifyVerified, VerifyInconclusive, VerifyFailed} {
		t.Run(string(final), func(t *testing.T) {
			r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
			payload := json.RawMessage(`[{"checkId":"c","status":"pass"}]`)
			if err := s.SetVerification(ctx, r.ID, final, payload); err != nil {
				t.Fatalf("SetVerification(%s): %v", final, err)
			}
			got := mustGetReceipt(t, s, r.ID)
			if got.VerificationState != final || got.VerifiedAt == nil || !jsonEqual(t, got.Verification, string(payload)) {
				t.Fatalf("after %s: %+v", final, got)
			}
			stamped := *got.VerifiedAt

			for _, reopen := range []VerificationState{VerifyPending, VerifyVerifying} {
				if err := s.SetVerification(ctx, r.ID, reopen, json.RawMessage(`[]`)); !errors.Is(err, ErrReceiptAlreadyFinal) {
					t.Errorf("SetVerification(%s) over %s = %v; want ErrReceiptAlreadyFinal", reopen, final, err)
				}
			}
			got = mustGetReceipt(t, s, r.ID)
			if got.VerificationState != final || !got.VerifiedAt.Equal(stamped) || !jsonEqual(t, got.Verification, string(payload)) {
				t.Errorf("a final verification was reopened or altered: %+v", got)
			}

			// Final to final replaces: a re-verification supersedes the result.
			if err := s.SetVerification(ctx, r.ID, VerifyVerified, json.RawMessage(`[{"checkId":"c2","status":"pass"}]`)); err != nil {
				t.Errorf("final-to-final SetVerification: %v", err)
			}
			if got = mustGetReceipt(t, s, r.ID); got.VerificationState != VerifyVerified ||
				!jsonEqual(t, got.Verification, `[{"checkId":"c2","status":"pass"}]`) {
				t.Errorf("final-to-final did not replace the outcome: %+v", got)
			}
		})
	}

	// Reconciliation records inconclusive; a slow verifier must not reopen it.
	r := mustInsertReceipt(t, s, newReceipt(testOwnerID(t)))
	backdateReceipt(t, pool, r.ID, 2*ReceiptOrphanGrace)
	if _, err := s.ReconcileOrphans(ctx, ReceiptOrphanGrace); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVerification(ctx, r.ID, VerifyVerifying, nil); !errors.Is(err, ErrReceiptAlreadyFinal) {
		t.Errorf("SetVerification(verifying) over a reconciled row = %v; want ErrReceiptAlreadyFinal", err)
	}
}

func TestChangeReceiptStore_Insert_PreviewedStateAndSeededObjects(t *testing.T) {
	s, _ := newReceiptStore(t)
	r := newReceipt(testOwnerID(t))
	r.State = ReceiptPreviewed
	r.Objects = []ReceiptObject{
		{Index: 0, Kind: "Namespace", Name: "prod", Action: "created", RecordedAt: time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)},
		sampleObject(1, "web", "configured"),
	}
	mustInsertReceipt(t, s, r)

	got := mustGetReceipt(t, s, r.ID)
	if got.State != ReceiptPreviewed {
		t.Errorf("state = %q; want previewed", got.State)
	}
	if len(got.Objects) != 2 || got.Objects[0].Kind != "Namespace" || got.Objects[0].Name != "prod" ||
		!got.Objects[0].RecordedAt.Equal(r.Objects[0].RecordedAt) ||
		got.Objects[1].Name != "web" || got.Objects[1].UID != "uid-web" || got.Objects[1].Resource != "deployments" {
		t.Errorf("seeded objects did not round-trip: %+v", got.Objects)
	}
}

func TestChangeReceiptStore_Cleanup_DeletesOnlyOldRows(t *testing.T) {
	s, pool := newReceiptStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	old := mustInsertReceipt(t, s, newReceipt(owner))
	fresh := mustInsertReceipt(t, s, newReceipt(owner))
	if _, err := pool.Exec(ctx,
		`UPDATE change_receipts SET created_at = NOW() - INTERVAL '40 days' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipt_grants (receipt_id, grantee_id, granted_by) VALUES ($1, 'g1', 'o')`, old.ID); err != nil {
		t.Fatal(err)
	}

	n, err := s.Cleanup(ctx, 30)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if n < 1 {
		t.Errorf("Cleanup deleted %d rows; want at least our old one", n)
	}
	if got, _ := s.Get(ctx, old.ID); got != nil {
		t.Error("the 40-day-old receipt survived a 30-day sweep")
	}
	if got, _ := s.Get(ctx, fresh.ID); got == nil {
		t.Error("a fresh receipt was deleted by the sweep")
	}
	var grants int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM change_receipt_grants WHERE receipt_id = $1`, old.ID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Errorf("%d grant rows survived their receipt's deletion", grants)
	}
}

func TestChangeReceiptStore_GrantsFor_AndCascade(t *testing.T) {
	s, pool := newReceiptStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()
	r := mustInsertReceipt(t, s, newReceipt(owner))
	other := mustInsertReceipt(t, s, newReceipt(owner))

	none, err := s.GrantsFor(ctx, r.ID)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("GrantsFor(no grants) = (%v, %v); want empty non-nil slice", none, err)
	}

	for _, g := range []string{"oidc:bob", "local:carol"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO change_receipt_grants (receipt_id, grantee_id, granted_by) VALUES ($1, $2, $3)`,
			r.ID, g, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipt_grants (receipt_id, grantee_id, granted_by) VALUES ($1, 'ldap:dave', $2)`,
		other.ID, owner); err != nil {
		t.Fatal(err)
	}

	got, err := s.GrantsFor(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if want := []string{"local:carol", "oidc:bob"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GrantsFor = %v; want %v (grants of another receipt must not leak)", got, want)
	}

	// A grant is unique per (receipt, grantee).
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipt_grants (receipt_id, grantee_id, granted_by) VALUES ($1, 'oidc:bob', $2)`,
		r.ID, owner); err == nil {
		t.Error("a duplicate (receipt, grantee) grant was accepted")
	}

	// Deleting the receipt takes its grants with it.
	if _, err := pool.Exec(ctx, `DELETE FROM change_receipts WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM change_receipt_grants WHERE receipt_id = $1`, r.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d grants survived the receipt delete", left)
	}
}

func TestChangeReceiptStore_ListForOwner_ScopesAndPaginates(t *testing.T) {
	s, pool := newReceiptStore(t)
	owner := testOwnerID(t)
	stranger := "stranger-" + owner
	ctx := t.Context()

	// Five receipts for owner on two clusters, newest first by created_at.
	var ids []uuid.UUID
	for i := 0; i < 5; i++ {
		r := newReceipt(owner)
		if i >= 3 {
			r.ClusterID = "other-" + owner
		}
		mustInsertReceipt(t, s, r)
		if _, err := pool.Exec(ctx,
			`UPDATE change_receipts SET created_at = NOW() - make_interval(mins => $2) WHERE id = $1`,
			r.ID, 10-i); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID) // ids[0] is the oldest
	}
	mustInsertReceipt(t, s, newReceipt(stranger))
	mustInsertReceipt(t, s, newReceipt(stranger))

	page1, total, err := s.ListForOwner(ctx, ReceiptQueryParams{OwnerID: owner, Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (the stranger's rows must not count)", total)
	}
	if len(page1) != 2 || page1[0].ID != ids[4] || page1[1].ID != ids[3] {
		t.Errorf("page 1 = %v; want newest two [%s %s]", receiptIDs(page1), ids[4], ids[3])
	}

	page3, _, err := s.ListForOwner(ctx, ReceiptQueryParams{OwnerID: owner, Page: 3, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 1 || page3[0].ID != ids[0] {
		t.Errorf("page 3 = %v; want the single oldest [%s]", receiptIDs(page3), ids[0])
	}

	beyond, total, err := s.ListForOwner(ctx, ReceiptQueryParams{OwnerID: owner, Page: 9, PageSize: 2})
	if err != nil || len(beyond) != 0 || total != 5 {
		t.Errorf("page past the end = (%d rows, total %d, %v); want (0, 5, nil)", len(beyond), total, err)
	}

	for _, r := range page1 {
		if r.OwnerID != owner {
			t.Errorf("ListForOwner returned a row owned by %q", r.OwnerID)
		}
	}

	byCluster, total, err := s.ListForOwner(ctx, ReceiptQueryParams{OwnerID: owner, ClusterID: "other-" + owner})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(byCluster) != 2 {
		t.Errorf("cluster filter = %d rows total %d; want 2", len(byCluster), total)
	}

	// An owner with no rows gets an empty, non-nil slice.
	none, total, err := s.ListForOwner(ctx, ReceiptQueryParams{OwnerID: "nobody-" + owner})
	if err != nil || none == nil || len(none) != 0 || total != 0 {
		t.Errorf("empty owner = (%v, %d, %v); want (empty non-nil, 0, nil)", none, total, err)
	}
}

func receiptIDs(rs []ChangeReceipt) []uuid.UUID {
	out := make([]uuid.UUID, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// TestMigration000023_RoundTripLeavesOtherTablesIntact applies 000023 on a
// throwaway database that already holds the 000022 schema, checks the shape and
// CHECK constraints, then rolls it back and confirms exactly the two new
// tables disappeared.
func TestMigration000023_RoundTripLeavesOtherTablesIntact(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()

	if err := m.Migrate(22); err != nil {
		t.Fatalf("migrating to 000022: %v", err)
	}
	before := publicTables(t, pool)
	if slices.Contains(before, "change_receipts") {
		t.Fatal("change_receipts exists before 000023")
	}

	if err := m.Migrate(23); err != nil {
		t.Fatalf("applying 000023: %v", err)
	}
	for _, table := range []string{"change_receipts", "change_receipt_grants"} {
		if !tableExists(t, pool, table) {
			t.Errorf("table %s missing after 000023", table)
		}
	}
	for _, idx := range []string{
		"idx_change_receipts_owner_created", "idx_change_receipts_cluster_created",
		"idx_change_receipts_unfinished", "idx_change_receipt_grants_grantee",
	} {
		if !indexExists(t, pool, idx) {
			t.Errorf("index %s missing after 000023", idx)
		}
	}
	if got, want := slices.Sorted(slices.Values(publicTables(t, pool))),
		slices.Sorted(slices.Values(append(slices.Clone(before), "change_receipt_grants", "change_receipts"))); !slices.Equal(got, want) {
		t.Errorf("tables after up = %v; want %v", got, want)
	}

	id := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipts (id, owner_id, content_digest, document_count) VALUES ($1, 'o', 'sha256:x', 1)`, id); err != nil {
		t.Fatalf("inserting a minimal receipt: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipts (id, owner_id, content_digest, document_count, state) VALUES ($1, 'o', 'sha256:x', 1, 'bogus')`,
		uuid.New()); err == nil {
		t.Error("the state CHECK accepted 'bogus'")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipts (id, owner_id, content_digest, document_count, verification_state) VALUES ($1, 'o', 'sha256:x', 1, 'bogus')`,
		uuid.New()); err == nil {
		t.Error("the verification_state CHECK accepted 'bogus'")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipts (id, owner_id, content_digest) VALUES ($1, 'o', 'sha256:x')`, uuid.New()); err == nil {
		t.Error("document_count is NOT NULL with no default; the insert should have failed")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipt_grants (receipt_id, grantee_id, granted_by) VALUES ($1, 'g', 'o')`, uuid.New()); err == nil {
		t.Error("a grant for a nonexistent receipt was accepted")
	}

	if err := m.Migrate(22); err != nil {
		t.Fatalf("rolling back 000023: %v", err)
	}
	after := publicTables(t, pool)
	if !slices.Equal(slices.Sorted(slices.Values(after)), slices.Sorted(slices.Values(before))) {
		t.Errorf("tables after down = %v; want exactly the pre-000023 set %v", after, before)
	}

	// Re-applying after a rollback works (the down leaves nothing behind).
	if err := m.Migrate(23); err != nil {
		t.Fatalf("re-applying 000023 after rollback: %v", err)
	}
}

func publicTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'schema_migrations' ORDER BY tablename`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
