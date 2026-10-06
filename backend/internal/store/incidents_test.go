package store

// incidents_test.go — coverage for IncidentStore and migration 000024
// (Release D, U21a).
//
// Two kinds of test live here:
//
//   - Pure tests (no database): input bounds that must hold before any SQL,
//     the retention clamp, the cursor codec, status validation, and the
//     migration file shape.
//   - Env-gated tests (testDB / migrationScratchDB, skipped without
//     KUBECENTER_TEST_DATABASE_URL): the owner boundary, grant visibility,
//     keyset pagination, revisioned notes, cascades, retention, and the
//     CHECK constraints that back the Go validation.
//
// Isolation contract (testdb_test.go): the harness never truncates, so every
// incident these tests write is owned by an id from testOwnerID(t) (and uses
// it as its cluster id), and every read is scoped by that owner. Cleanup is
// table-global by design; its tests only age their own rows and assert on
// their own rows only.

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Pure tests
// ---------------------------------------------------------------------------

func TestValidateIncidentTitle_Bounds(t *testing.T) {
	cases := []struct {
		name  string
		title string
		ok    bool
	}{
		{"empty", "", false},
		{"one char", "x", true},
		{"200 ASCII", strings.Repeat("a", 200), true},
		{"201 ASCII", strings.Repeat("a", 201), false},
		// PostgreSQL length() counts characters, not bytes: 200 three-byte
		// runes (600 bytes) are within the CHECK, 201 are not.
		{"200 multi-byte runes", strings.Repeat("€", 200), true},
		{"201 multi-byte runes", strings.Repeat("€", 201), false},
		{"invalid UTF-8", "bad\xff", false},
		{"NUL byte", "a\x00b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateIncidentTitle(tc.title)
			if tc.ok && err != nil {
				t.Fatalf("ValidateIncidentTitle(%q) = %v; want nil", tc.title, err)
			}
			if !tc.ok && !errors.Is(err, ErrIncidentInvalid) {
				t.Fatalf("ValidateIncidentTitle(%q) = %v; want ErrIncidentInvalid", tc.title, err)
			}
		})
	}
}

func TestValidateIncidentSummary_Bounds(t *testing.T) {
	if err := ValidateIncidentSummary(""); err != nil {
		t.Errorf("empty summary rejected: %v", err)
	}
	if err := ValidateIncidentSummary(strings.Repeat("日", 10000)); err != nil {
		t.Errorf("10000 multi-byte runes rejected: %v", err)
	}
	if err := ValidateIncidentSummary(strings.Repeat("日", 10001)); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("10001 runes = %v; want ErrIncidentInvalid", err)
	}
}

func TestValidateIncidentNoteBody_Bounds(t *testing.T) {
	if err := ValidateIncidentNoteBody(""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("empty body = %v; want ErrIncidentInvalid", err)
	}
	if err := ValidateIncidentNoteBody(strings.Repeat("ü", 20000)); err != nil {
		t.Errorf("20000 multi-byte runes rejected: %v", err)
	}
	if err := ValidateIncidentNoteBody(strings.Repeat("ü", 20001)); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("20001 runes = %v; want ErrIncidentInvalid", err)
	}
}

func TestValidateIncidentWindow(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	before := start.Add(-time.Second)
	after := start.Add(time.Hour)
	if err := ValidateIncidentWindow(start, nil); err != nil {
		t.Errorf("open window rejected: %v", err)
	}
	if err := ValidateIncidentWindow(start, &start); err != nil {
		t.Errorf("zero-length window rejected: %v", err)
	}
	if err := ValidateIncidentWindow(start, &after); err != nil {
		t.Errorf("forward window rejected: %v", err)
	}
	if err := ValidateIncidentWindow(start, &before); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("inverted window = %v; want ErrIncidentInvalid", err)
	}
	if err := ValidateIncidentWindow(time.Time{}, nil); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("zero window start = %v; want ErrIncidentInvalid", err)
	}
}

func TestIncidentRetentionDays_ClampAndValidate(t *testing.T) {
	clamp := map[int]int{-5: 1, 0: 1, 1: 1, 30: 30, 3650: 3650, 3651: 3650, 1 << 30: 3650}
	for in, want := range clamp {
		if got := ClampIncidentRetentionDays(in); got != want {
			t.Errorf("ClampIncidentRetentionDays(%d) = %d; want %d", in, got, want)
		}
	}
	for _, ok := range []int{1, 30, 3650} {
		if err := ValidateIncidentRetentionDays(ok); err != nil {
			t.Errorf("ValidateIncidentRetentionDays(%d) = %v; want nil", ok, err)
		}
	}
	for _, bad := range []int{-1, 0, 3651} {
		if err := ValidateIncidentRetentionDays(bad); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("ValidateIncidentRetentionDays(%d) = %v; want ErrIncidentInvalid", bad, err)
		}
	}
}

func TestValidateIncidentStatus(t *testing.T) {
	for _, ok := range []string{IncidentStatusOpen, IncidentStatusClosed} {
		if err := ValidateIncidentStatus(ok); err != nil {
			t.Errorf("ValidateIncidentStatus(%q) = %v; want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "Open", "resolved", "closed "} {
		if err := ValidateIncidentStatus(bad); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("ValidateIncidentStatus(%q) = %v; want ErrIncidentInvalid", bad, err)
		}
	}
}

func TestIncidentCleanup_RejectsNonPositiveRetention(t *testing.T) {
	// A nil pool proves the guard runs before any SQL.
	s := NewIncidentStore(nil)
	for _, days := range []int{0, -1} {
		if _, err := s.Cleanup(t.Context(), days); err == nil {
			t.Errorf("Cleanup(%d) succeeded; want an error before any SQL", days)
		}
	}
}

func TestIncidentStore_RejectsInvalidInputBeforeSQL(t *testing.T) {
	s := NewIncidentStore(nil) // any SQL would panic on the nil pool
	ctx := t.Context()
	good := IncidentRow{OwnerID: "o", Title: "t", WindowStart: time.Now(), RetentionDaysAtCapture: 30}

	bad := map[string]IncidentRow{}
	r := good
	r.OwnerID = ""
	bad["empty owner"] = r
	r = good
	r.Title = strings.Repeat("a", 201)
	bad["overlong title"] = r
	r = good
	r.Summary = strings.Repeat("a", 10001)
	bad["overlong summary"] = r
	r = good
	end := good.WindowStart.Add(-time.Minute)
	r.WindowEnd = &end
	bad["inverted window"] = r
	r = good
	r.RetentionDaysAtCapture = 0
	bad["retention below range"] = r
	r = good
	r.RetentionDaysAtCapture = 3651
	bad["retention above range"] = r
	r = good
	r.Status = IncidentStatusClosed
	bad["created closed"] = r
	for name, row := range bad {
		if _, err := s.Create(ctx, row); !errors.Is(err, ErrIncidentInvalid) {
			t.Errorf("Create(%s) = %v; want ErrIncidentInvalid", name, err)
		}
	}

	id := uuid.New()
	if err := s.Update(ctx, id, "o", ptr("t"), ptr(""), ptr("resolved")); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("Update with bad status = %v; want ErrIncidentInvalid", err)
	}
	if err := s.Update(ctx, id, "o", ptr(""), ptr(""), ptr(IncidentStatusOpen)); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("Update with empty title = %v; want ErrIncidentInvalid", err)
	}
	if err := s.Update(ctx, id, "", ptr("t"), ptr(""), ptr(IncidentStatusOpen)); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("Update with empty owner = %v; want ErrIncidentInvalid", err)
	}
	if err := s.Delete(ctx, id, ""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("Delete with empty owner = %v; want ErrIncidentInvalid", err)
	}
	if _, _, err := s.ListVisible(ctx, "", 10, ""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("ListVisible with empty user = %v; want ErrIncidentInvalid", err)
	}
	if _, _, err := s.ListVisible(ctx, "u", 10, "!!not-a-cursor"); !errors.Is(err, ErrInvalidIncidentCursor) {
		t.Errorf("ListVisible with malformed cursor = %v; want ErrInvalidIncidentCursor", err)
	}
	if _, err := s.CreateNote(ctx, id, "a", ""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("CreateNote with empty body = %v; want ErrIncidentInvalid", err)
	}
	if _, err := s.CreateNote(ctx, id, "", "b"); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("CreateNote with empty author = %v; want ErrIncidentInvalid", err)
	}
	if _, err := s.UpdateNote(ctx, id, id, "a", strings.Repeat("x", 20001), 1); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("UpdateNote with overlong body = %v; want ErrIncidentInvalid", err)
	}
	if _, err := s.UpdateNote(ctx, id, id, "a", "b", 0); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("UpdateNote with revision 0 = %v; want ErrIncidentInvalid", err)
	}
	if err := s.DeleteNote(ctx, id, id, ""); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("DeleteNote with empty author = %v; want ErrIncidentInvalid", err)
	}
}

func TestIncidentPageSize_Clamp(t *testing.T) {
	cases := map[int]int{-1: IncidentDefaultPageSize, 0: IncidentDefaultPageSize, 1: 1, 200: 200, 201: IncidentMaxPageSize}
	for in, want := range cases {
		if got := clampIncidentPageSize(in); got != want {
			t.Errorf("clampIncidentPageSize(%d) = %d; want %d", in, got, want)
		}
	}
}

func TestIncidentCursor_RoundTrip(t *testing.T) {
	c := IncidentCursor{
		CreatedAt: time.Date(2026, 10, 5, 9, 30, 15, 123456000, time.UTC),
		ID:        uuid.New(),
	}
	got, err := DecodeIncidentCursor(EncodeIncidentCursor(c))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) || got.ID != c.ID {
		t.Fatalf("round trip = %+v; want %+v", got, c)
	}
}

func TestIncidentCursor_RejectsMalformed(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	bad := []string{
		"",
		"%%%",
		enc("no-separator"),
		enc("123:" + "not-a-uuid"),
		enc("-1:" + uuid.NewString()),
		enc("abc:" + uuid.NewString()),
		enc("99999999999999999:" + uuid.NewString()), // past the 2100 bound
		enc("1:" + uuid.Nil.String()),
		enc("1:" + uuid.NewString() + ":extra"),
		strings.Repeat("A", 200),
	}
	for _, s := range bad {
		if _, err := DecodeIncidentCursor(s); !errors.Is(err, ErrInvalidIncidentCursor) {
			t.Errorf("DecodeIncidentCursor(%q) = %v; want ErrInvalidIncidentCursor", s, err)
		}
	}
}

func TestNoteRevisionConflictError_IsAndCarriesCurrent(t *testing.T) {
	var err error = &NoteRevisionConflictError{Current: 7}
	if !errors.Is(err, ErrNoteRevisionConflict) {
		t.Fatal("NoteRevisionConflictError does not match ErrNoteRevisionConflict")
	}
	var conflict *NoteRevisionConflictError
	if !errors.As(err, &conflict) || conflict.Current != 7 {
		t.Fatalf("errors.As = %+v; want Current 7", conflict)
	}
	if errors.Is(err, ErrNoteNotFound) || errors.Is(err, ErrNotNoteAuthor) {
		t.Fatal("a revision conflict must not match the not-found or not-author sentinels")
	}
}

// incidentMigration reads one 000024 migration file.
func incidentMigration(t *testing.T, direction string) string {
	t.Helper()
	b, err := fs.ReadFile(migrationsFS, "migrations/000024_create_incidents."+direction+".sql")
	if err != nil {
		t.Fatalf("reading 000024 %s migration: %v", direction, err)
	}
	return string(b)
}

// incidentTables is every table 000024 creates, in creation order.
var incidentTables = []string{"incidents", "incident_evidence", "incident_notes", "incident_note_revisions", "incident_grants"}

// incidentIndexes is every named index 000024 creates.
var incidentIndexes = []string{
	"idx_incidents_owner_created", "idx_incidents_retention", "idx_incidents_cluster_status",
	"idx_incident_evidence_dedup", "idx_incident_evidence_timeline", "idx_incident_evidence_scope",
	"idx_incident_evidence_provenance", "idx_incident_evidence_secret",
	"idx_incident_notes_incident", "idx_incident_grants_grantee",
}

func TestMigration000024_UpAndDownAreWellFormed(t *testing.T) {
	up := incidentMigration(t, "up")
	down := incidentMigration(t, "down")

	for _, table := range incidentTables {
		if !strings.Contains(up, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Errorf("000024 up migration does not create %s", table)
		}
		if !strings.Contains(up, "COMMENT ON TABLE "+table+" IS") {
			t.Errorf("000024 up migration has no COMMENT ON TABLE %s", table)
		}
		if !strings.Contains(down, "DROP TABLE IF EXISTS "+table+";") {
			t.Errorf("000024 down migration does not drop %s", table)
		}
	}
	for _, idx := range incidentIndexes {
		if !strings.Contains(up, idx) {
			t.Errorf("000024 up migration is missing index %s", idx)
		}
	}
	if got := strings.Count(up, "REFERENCES incidents(id) ON DELETE CASCADE"); got != 3 {
		t.Errorf("%d tables cascade from incidents; want 3 (evidence, notes, grants)", got)
	}
	if !strings.Contains(up, "REFERENCES incident_notes(id) ON DELETE CASCADE") {
		t.Error("incident_note_revisions does not cascade from incident_notes")
	}
	for _, want := range []string{
		"CHECK (length(title) BETWEEN 1 AND 200)",
		"CHECK (length(summary) <= 10000)",
		"CHECK (status IN ('open', 'closed'))",
		"CHECK (retention_days_at_capture BETWEEN 1 AND 3650)",
		"CHECK (window_end IS NULL OR window_end >= window_start)",
		"CHECK (status <> 'closed' OR closed_at IS NOT NULL)",
		"CHECK (length(body) BETWEEN 1 AND 20000)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("000024 up migration is missing %q", want)
		}
	}
	// History is keyed by stable identity: nothing may key evidence on
	// (namespace, name) alone (plan §3.1, R1).
	if strings.Contains(up, "(cluster_id, namespace, name)") {
		t.Error("000024 keys evidence on (cluster_id, namespace, name); provenance must be (cluster_id, source_uid)")
	}
	for _, want := range []string{"DESTRUCTIVE", "NOTES.txt (000024)"} {
		if !strings.Contains(down, want) {
			t.Errorf("000024 down migration is missing %q", want)
		}
	}
	if strings.Contains(up+down, "000020") {
		t.Error("000024 still refers to the plan's superseded number 000020")
	}

	// 000024 is the only migration that introduces these tables.
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
		if strings.Contains(string(b), "CREATE TABLE IF NOT EXISTS incidents") {
			creators = append(creators, e.Name())
		}
	}
	if want := []string{"000024_create_incidents.up.sql"}; !reflect.DeepEqual(creators, want) {
		t.Errorf("migrations creating incidents = %v; want %v", creators, want)
	}
}

// TestMigration000024_EveryUpStatementIsRerunnable pins what the NOTES.txt
// (000024) rollback procedure relies on: after the recorded version is reset
// to 23, a roll-forward re-runs this file over the existing tables, so each
// statement must be a no-op the second time.
func TestMigration000024_EveryUpStatementIsRerunnable(t *testing.T) {
	var body []string
	for _, line := range strings.Split(incidentMigration(t, "up"), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			body = append(body, line)
		}
	}
	allowed := []string{
		"CREATE TABLE IF NOT EXISTS ",
		"CREATE INDEX IF NOT EXISTS ",
		"CREATE UNIQUE INDEX IF NOT EXISTS ",
		"COMMENT ON TABLE ",
	}
	statements := 0
	for _, stmt := range strings.Split(strings.Join(body, "\n"), ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		statements++
		if !slices.ContainsFunc(allowed, func(p string) bool { return strings.HasPrefix(stmt, p) }) {
			t.Errorf("000024 statement is not re-runnable: %.80q", stmt)
		}
	}
	if statements < len(incidentTables)*2 {
		t.Fatalf("parsed only %d statements from the 000024 up file; the splitter is broken", statements)
	}
}

func TestMigration000024_DownDropsInReverseDependencyOrder(t *testing.T) {
	down := incidentMigration(t, "down")
	want := []string{"incident_grants", "incident_note_revisions", "incident_notes", "incident_evidence", "incidents"}
	last := -1
	for _, table := range want {
		at := strings.Index(down, "DROP TABLE IF EXISTS "+table+";")
		if at < 0 {
			t.Fatalf("down migration does not drop %s", table)
		}
		if at < last {
			t.Errorf("%s is dropped before a table that references it; want order %v", table, want)
		}
		last = at
	}
}

// ---------------------------------------------------------------------------
// Env-gated PostgreSQL tests
// ---------------------------------------------------------------------------

func newIncidentStore(t *testing.T) (*IncidentStore, *pgxpool.Pool) {
	t.Helper()
	pool := testDB(t)
	return NewIncidentStore(pool), pool
}

// newIncident returns a creatable incident owned by owner, using owner as its
// cluster id too so cluster-scoped residue stays private to the test.
func newIncident(owner, title string) IncidentRow {
	return IncidentRow{
		OwnerID:                owner,
		ClusterID:              owner,
		Title:                  title,
		Summary:                "summary of " + title,
		WindowStart:            time.Now().Add(-time.Hour).UTC(),
		RetentionDaysAtCapture: 30,
	}
}

func mustCreateIncident(t *testing.T, s *IncidentStore, r IncidentRow) uuid.UUID {
	t.Helper()
	id, err := s.Create(t.Context(), r)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return id
}

func mustGetIncident(t *testing.T, s *IncidentStore, id uuid.UUID) *IncidentRow {
	t.Helper()
	got, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatalf("Get(%s) = nil; want the incident", id)
	}
	return got
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func insertGrant(t *testing.T, pool *pgxpool.Pool, incidentID uuid.UUID, grantee, grantedBy string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO incident_grants (incident_id, grantee_id, granted_by) VALUES ($1, $2, $3)`,
		incidentID, grantee, grantedBy); err != nil {
		t.Fatalf("inserting grant: %v", err)
	}
}

func insertEvidence(t *testing.T, pool *pgxpool.Pool, incidentID uuid.UUID, captureKey string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO incident_evidence (incident_id, evidence_kind, mode, cluster_id, resource,
			source_kind, name, completeness, payload, payload_bytes, capture_key)
		VALUES ($1, 'object_summary', 'snapshot', 'local', 'pods', 'Pod', 'p', 'complete', '{}'::jsonb, 2, $2)`,
		incidentID, captureKey); err != nil {
		t.Fatalf("inserting evidence: %v", err)
	}
}

func TestIncidentStore_CreateAndGet_RoundTrips(t *testing.T) {
	s, _ := newIncidentStore(t)
	owner := testOwnerID(t)
	r := newIncident(owner, "api latency")
	end := r.WindowStart.Add(30 * time.Minute)
	r.WindowEnd = &end
	r.Title = strings.Repeat("€", 200) // 200 characters, 600 bytes: within the CHECK
	id := mustCreateIncident(t, s, r)

	got := mustGetIncident(t, s, id)
	if got.ID != id || got.OwnerID != owner || got.ClusterID != owner || got.Title != r.Title ||
		got.Summary != r.Summary || got.Status != IncidentStatusOpen || got.RetentionDaysAtCapture != 30 {
		t.Errorf("Get = %+v; want the created fields with status open", got)
	}
	if !got.WindowStart.Equal(r.WindowStart.Truncate(time.Microsecond)) ||
		got.WindowEnd == nil || !got.WindowEnd.Equal(end.Truncate(time.Microsecond)) {
		t.Errorf("window = %v..%v; want %v..%v", got.WindowStart, got.WindowEnd, r.WindowStart, end)
	}
	if got.ClosedAt != nil || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps = created %v updated %v closed %v; want created/updated set, closed nil",
			got.CreatedAt, got.UpdatedAt, got.ClosedAt)
	}
	if got.EvidenceBytes != 0 || got.EvidenceCount != 0 || got.ScopeCount != 0 {
		t.Errorf("counters = %d/%d/%d; want zero on a new incident", got.EvidenceBytes, got.EvidenceCount, got.ScopeCount)
	}
}

func TestIncidentStore_CreateDefaultsClusterToLocal(t *testing.T) {
	s, _ := newIncidentStore(t)
	r := newIncident(testOwnerID(t), "t")
	r.ClusterID = ""
	if got := mustGetIncident(t, s, mustCreateIncident(t, s, r)); got.ClusterID != "local" {
		t.Errorf("cluster id = %q; want local", got.ClusterID)
	}
}

func TestIncidentStore_GetMissingReturnsNilNil(t *testing.T) {
	s, _ := newIncidentStore(t)
	got, err := s.Get(t.Context(), uuid.New())
	if got != nil || err != nil {
		t.Fatalf("Get(missing) = (%v, %v); want (nil, nil)", got, err)
	}
}

func TestIncidentStore_GetDatabaseFaultIsAnError(t *testing.T) {
	s, pool := newIncidentStore(t)
	pool.Close() // a closed pool is an unavailable database, not a missing row
	got, err := s.Get(context.Background(), uuid.New())
	if err == nil || got != nil {
		t.Fatalf("Get on a closed pool = (%v, %v); want (nil, error)", got, err)
	}
}

func TestIncidentStore_OwnerBoundary(t *testing.T) {
	s, _ := newIncidentStore(t)
	ctx := t.Context()
	a, b := testOwnerID(t), testOwnerID(t)
	id := mustCreateIncident(t, s, newIncident(a, "a's incident"))

	if err := s.Update(ctx, id, b, ptr("hijacked"), ptr(""), ptr(IncidentStatusClosed)); !errors.Is(err, ErrNotOwner) {
		t.Errorf("B's Update = %v; want ErrNotOwner", err)
	}
	if err := s.Delete(ctx, id, b); !errors.Is(err, ErrNotOwner) {
		t.Errorf("B's Delete = %v; want ErrNotOwner", err)
	}
	got := mustGetIncident(t, s, id)
	if got.Title != "a's incident" || got.Status != IncidentStatusOpen {
		t.Errorf("after B's attempts the incident is %+v; want it untouched", got)
	}

	rows, _, err := s.ListVisible(ctx, b, 50, "")
	if err != nil {
		t.Fatalf("ListVisible(B): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("B sees %d incidents; want none", len(rows))
	}
	rows, _, err = s.ListVisible(ctx, a, 50, "")
	if err != nil {
		t.Fatalf("ListVisible(A): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Errorf("A sees %v; want exactly %s", incidentIDs(rows), id)
	}

	missing := uuid.New()
	if err := s.Update(ctx, missing, a, ptr("t"), ptr(""), ptr(IncidentStatusOpen)); !errors.Is(err, ErrIncidentNotFound) {
		t.Errorf("Update(missing) = %v; want ErrIncidentNotFound", err)
	}
	if err := s.Delete(ctx, missing, a); !errors.Is(err, ErrIncidentNotFound) {
		t.Errorf("Delete(missing) = %v; want ErrIncidentNotFound", err)
	}

	if err := s.Delete(ctx, id, a); err != nil {
		t.Fatalf("owner Delete: %v", err)
	}
	if got, err := s.Get(ctx, id); got != nil || err != nil {
		t.Errorf("Get after delete = (%v, %v); want (nil, nil)", got, err)
	}
}

func TestIncidentStore_UpdateClosesAndReopens(t *testing.T) {
	s, _ := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	id := mustCreateIncident(t, s, newIncident(owner, "t"))
	before := mustGetIncident(t, s, id)

	if err := s.Update(ctx, id, owner, ptr("renamed"), ptr("new summary"), ptr(IncidentStatusClosed)); err != nil {
		t.Fatalf("close: %v", err)
	}
	closed := mustGetIncident(t, s, id)
	if closed.Title != "renamed" || closed.Summary != "new summary" || closed.Status != IncidentStatusClosed {
		t.Errorf("after close = %+v", closed)
	}
	if closed.ClosedAt == nil {
		t.Fatal("closed_at not stamped on close")
	}
	if !closed.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("updated_at %v did not advance past %v", closed.UpdatedAt, before.UpdatedAt)
	}

	// Editing a closed incident keeps the original close time.
	if err := s.Update(ctx, id, owner, ptr("renamed again"), ptr(""), ptr(IncidentStatusClosed)); err != nil {
		t.Fatalf("edit while closed: %v", err)
	}
	if again := mustGetIncident(t, s, id); again.ClosedAt == nil || !again.ClosedAt.Equal(*closed.ClosedAt) {
		t.Errorf("closed_at moved from %v to %v on an edit that kept it closed", closed.ClosedAt, again.ClosedAt)
	}

	if err := s.Update(ctx, id, owner, ptr("renamed"), ptr(""), ptr(IncidentStatusOpen)); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened := mustGetIncident(t, s, id); reopened.Status != IncidentStatusOpen || reopened.ClosedAt != nil {
		t.Errorf("after reopen status=%q closed_at=%v; want open and nil", reopened.Status, reopened.ClosedAt)
	}
}

// ptr is a literal's address, for the nullable Update fields.
func ptr(s string) *string { return &s }

// TestIncidentStore_PartialUpdatesMergeInSQL: a nil field keeps the column
// (COALESCE in the UPDATE), so two partial updates that interleave never
// revert each other, and the closed_at rule follows the RESULTING status.
func TestIncidentStore_PartialUpdatesMergeInSQL(t *testing.T) {
	s, _ := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	id := mustCreateIncident(t, s, newIncident(owner, "original"))

	// Two editors each read the same row, then write one field each.
	if err := s.Update(ctx, id, owner, ptr("renamed"), nil, nil); err != nil {
		t.Fatalf("title-only: %v", err)
	}
	if err := s.Update(ctx, id, owner, nil, ptr("new summary"), nil); err != nil {
		t.Fatalf("summary-only: %v", err)
	}
	got := mustGetIncident(t, s, id)
	if got.Title != "renamed" || got.Summary != "new summary" || got.Status != IncidentStatusOpen || got.ClosedAt != nil {
		t.Fatalf("after interleaved partial updates = %+v; want both fields kept", got)
	}

	// Closing with status only keeps title and summary; a later nil-status
	// edit keeps it closed with the original close time.
	if err := s.Update(ctx, id, owner, nil, nil, ptr(IncidentStatusClosed)); err != nil {
		t.Fatalf("close: %v", err)
	}
	closed := mustGetIncident(t, s, id)
	if closed.Status != IncidentStatusClosed || closed.ClosedAt == nil || closed.Title != "renamed" || closed.Summary != "new summary" {
		t.Fatalf("after close = %+v", closed)
	}
	if err := s.Update(ctx, id, owner, ptr("renamed twice"), nil, nil); err != nil {
		t.Fatalf("edit while closed: %v", err)
	}
	again := mustGetIncident(t, s, id)
	if again.Status != IncidentStatusClosed || again.ClosedAt == nil || !again.ClosedAt.Equal(*closed.ClosedAt) || again.Title != "renamed twice" {
		t.Fatalf("nil-status edit on a closed incident = %+v; want still closed at %v", again, closed.ClosedAt)
	}
	// Reopening with status only clears closed_at.
	if err := s.Update(ctx, id, owner, nil, nil, ptr(IncidentStatusOpen)); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if re := mustGetIncident(t, s, id); re.Status != IncidentStatusOpen || re.ClosedAt != nil || re.Title != "renamed twice" {
		t.Fatalf("after reopen = %+v", re)
	}
	// All nil: nothing to validate, nothing changes but updated_at.
	if err := s.Update(ctx, id, owner, nil, nil, nil); err != nil {
		t.Fatalf("empty update: %v", err)
	}
	// A nil field is not validated; a non-nil one still is.
	if err := s.Update(ctx, id, owner, ptr(""), nil, nil); !errors.Is(err, ErrIncidentInvalid) {
		t.Fatalf("empty title = %v; want ErrIncidentInvalid", err)
	}
	if err := s.Update(ctx, id, owner, nil, nil, ptr("resolved")); !errors.Is(err, ErrIncidentInvalid) {
		t.Fatalf("bad status = %v; want ErrIncidentInvalid", err)
	}
}

func TestIncidentStore_ListVisibleIncludesGrantedIncident(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner, collaborator := testOwnerID(t), testOwnerID(t)
	shared := mustCreateIncident(t, s, newIncident(owner, "shared"))
	mustCreateIncident(t, s, newIncident(owner, "private"))

	rows, _, err := s.ListVisible(ctx, collaborator, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("collaborator sees %v before any grant; want none", incidentIDs(rows))
	}

	insertGrant(t, pool, shared, collaborator, owner)
	rows, _, err = s.ListVisible(ctx, collaborator, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != shared {
		t.Errorf("collaborator sees %v; want exactly the granted %s", incidentIDs(rows), shared)
	}
	// A grant conveys no ownership: the collaborator still cannot mutate it.
	if err := s.Update(ctx, shared, collaborator, ptr("x"), ptr(""), ptr(IncidentStatusOpen)); !errors.Is(err, ErrNotOwner) {
		t.Errorf("collaborator Update = %v; want ErrNotOwner", err)
	}
	if err := s.Delete(ctx, shared, collaborator); !errors.Is(err, ErrNotOwner) {
		t.Errorf("collaborator Delete = %v; want ErrNotOwner", err)
	}
}

func TestIncidentStore_ListVisiblePaginatesStablyAcrossTies(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)

	var ids []uuid.UUID
	for i := range 7 {
		ids = append(ids, mustCreateIncident(t, s, newIncident(owner, "tie "+string(rune('a'+i)))))
	}
	// Five rows share one created_at exactly; the id tiebreak must still
	// produce a total order with no row skipped or repeated across pages.
	tie := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `UPDATE incidents SET created_at = $1 WHERE id = ANY($2)`, tie, ids[:5]); err != nil {
		t.Fatal(err)
	}

	var seen []IncidentRow
	cursor := ""
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("pagination did not terminate")
		}
		rows, next, err := s.ListVisible(ctx, owner, 2, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(rows) > 2 {
			t.Fatalf("page %d has %d rows; limit is 2", page, len(rows))
		}
		seen = append(seen, rows...)
		if next == "" {
			break
		}
		cursor = next
	}

	if len(seen) != len(ids) {
		t.Fatalf("paged through %d rows; want %d", len(seen), len(ids))
	}
	got := incidentIDs(seen)
	if !slices.IsSortedFunc(seen, func(x, y IncidentRow) int {
		// (created_at DESC, id DESC): x before y means x is "greater".
		if c := y.CreatedAt.Compare(x.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(y.ID.String(), x.ID.String())
	}) {
		t.Errorf("pages are not in (created_at DESC, id DESC) order: %v", got)
	}
	want := slices.Clone(ids)
	slices.SortFunc(want, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	slices.SortFunc(got, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	if !slices.Equal(got, want) {
		t.Errorf("rows across pages = %v; want each of %v exactly once", got, want)
	}
}

func TestIncidentStore_ListVisibleRejectsMalformedCursor(t *testing.T) {
	s, _ := newIncidentStore(t)
	if _, _, err := s.ListVisible(t.Context(), testOwnerID(t), 10, "garbage!"); !errors.Is(err, ErrInvalidIncidentCursor) {
		t.Errorf("ListVisible(bad cursor) = %v; want ErrInvalidIncidentCursor", err)
	}
}

func TestIncidentStore_NoteRevisionConflictReportsCurrentAndKeepsHistory(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))

	note, err := s.CreateNote(ctx, incident, owner, "first")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if note.Revision != 1 || note.Body != "first" || note.AuthorID != owner || note.IncidentID != incident {
		t.Fatalf("created note = %+v", note)
	}

	edited, err := s.UpdateNote(ctx, incident, note.ID, owner, "second", 1)
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if edited.Revision != 2 || edited.Body != "second" || !edited.UpdatedAt.After(note.UpdatedAt) {
		t.Errorf("edited note = %+v; want revision 2, body second, updated_at advanced", edited)
	}

	// A concurrent editor still holding revision 1 loses, and learns the
	// current revision so the UI can offer a merge.
	_, err = s.UpdateNote(ctx, incident, note.ID, owner, "stale overwrite", 1)
	if !errors.Is(err, ErrNoteRevisionConflict) {
		t.Fatalf("stale UpdateNote = %v; want ErrNoteRevisionConflict", err)
	}
	var conflict *NoteRevisionConflictError
	if !errors.As(err, &conflict) || conflict.Current != 2 {
		t.Fatalf("conflict = %+v; want Current 2", conflict)
	}

	notes, _, err := s.ListNotes(ctx, incident, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Body != "second" || notes[0].Revision != 2 {
		t.Errorf("notes after conflict = %+v; want the revision-2 body untouched", notes)
	}
	revs, err := s.ListNoteRevisions(ctx, incident, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 || revs[0].Revision != 1 || revs[0].Body != "first" || revs[0].AuthorID != owner {
		t.Errorf("revisions = %+v; want exactly revision 1 holding the prior body", revs)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_note_revisions WHERE note_id = $1`, note.ID); n != 1 {
		t.Errorf("%d revision rows; the conflicting edit must not have written one", n)
	}
}

func TestIncidentStore_NoteRevisionHistoryIsAppendOnly(t *testing.T) {
	s, _ := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))
	note, err := s.CreateNote(ctx, incident, owner, "v1")
	if err != nil {
		t.Fatal(err)
	}
	for rev, body := range []string{"v2", "v3", "v4"} {
		if _, err := s.UpdateNote(ctx, incident, note.ID, owner, body, rev+1); err != nil {
			t.Fatalf("edit to %s: %v", body, err)
		}
	}
	revs, err := s.ListNoteRevisions(ctx, incident, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i, r := range revs {
		if r.Revision != i+1 {
			t.Errorf("revision %d at position %d; want ascending from 1", r.Revision, i)
		}
		got = append(got, r.Body)
	}
	if want := []string{"v1", "v2", "v3"}; !slices.Equal(got, want) {
		t.Errorf("revision bodies = %v; want %v (every prior body, oldest first)", got, want)
	}
	notes, _, err := s.ListNotes(ctx, incident, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Body != "v4" || notes[0].Revision != 4 {
		t.Errorf("current note = %+v; want v4 at revision 4", notes)
	}
}

func TestIncidentStore_NoteMutationsCannotCrossIncidents(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	incidentA := mustCreateIncident(t, s, newIncident(owner, "A"))
	incidentB := mustCreateIncident(t, s, newIncident(owner, "B"))
	noteB, err := s.CreateNote(ctx, incidentB, owner, "B's note")
	if err != nil {
		t.Fatal(err)
	}
	// Give noteB a revision history through its own incident, so the
	// cross-incident revision read below has something it could leak.
	if _, err := s.UpdateNote(ctx, incidentB, noteB.ID, owner, "v2", 1); err != nil {
		t.Fatalf("UpdateNote through B: %v", err)
	}

	// The note's own author, authorized on A, addresses B's note through A.
	if _, err := s.UpdateNote(ctx, incidentA, noteB.ID, owner, "via A", 2); !errors.Is(err, ErrNoteNotFound) {
		t.Errorf("UpdateNote through the wrong incident = %v; want ErrNoteNotFound", err)
	}
	if err := s.DeleteNote(ctx, incidentA, noteB.ID, owner); !errors.Is(err, ErrNoteNotFound) {
		t.Errorf("DeleteNote through the wrong incident = %v; want ErrNoteNotFound", err)
	}
	viaA, err := s.ListNoteRevisions(ctx, incidentA, noteB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(viaA) != 0 {
		t.Errorf("ListNoteRevisions through incident A returned %d of B's revisions; want 0", len(viaA))
	}
	viaB, err := s.ListNoteRevisions(ctx, incidentB, noteB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(viaB) != 1 || viaB[0].Body != "B's note" {
		t.Errorf("ListNoteRevisions through incident B = %+v; want the one prior body", viaB)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_notes WHERE id = $1 AND body = 'v2' AND revision = 2`, noteB.ID); n != 1 {
		t.Error("B's note was modified through incident A")
	}
	if _, err := s.UpdateNote(ctx, incidentB, uuid.New(), owner, "x", 1); !errors.Is(err, ErrNoteNotFound) {
		t.Errorf("UpdateNote(missing note) = %v; want ErrNoteNotFound", err)
	}
}

func TestIncidentStore_NonAuthorCannotEditOrDeleteNote(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner, collaborator := testOwnerID(t), testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))
	insertGrant(t, pool, incident, collaborator, owner)

	note, err := s.CreateNote(ctx, incident, collaborator, "collaborator's note")
	if err != nil {
		t.Fatal(err)
	}
	// Not even the incident owner may rewrite or remove someone else's note.
	if _, err := s.UpdateNote(ctx, incident, note.ID, owner, "owner edit", 1); !errors.Is(err, ErrNotNoteAuthor) {
		t.Errorf("owner UpdateNote of a collaborator's note = %v; want ErrNotNoteAuthor", err)
	}
	if err := s.DeleteNote(ctx, incident, note.ID, owner); !errors.Is(err, ErrNotNoteAuthor) {
		t.Errorf("owner DeleteNote of a collaborator's note = %v; want ErrNotNoteAuthor", err)
	}
	// A non-author never learns the revision: a wrong revision still reports
	// authorship, not a conflict.
	if _, err := s.UpdateNote(ctx, incident, note.ID, owner, "owner edit", 99); !errors.Is(err, ErrNotNoteAuthor) {
		t.Errorf("owner UpdateNote with a stale revision = %v; want ErrNotNoteAuthor", err)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_note_revisions WHERE note_id = $1`, note.ID); n != 0 {
		t.Errorf("%d revision rows written by rejected edits; want 0", n)
	}

	if err := s.DeleteNote(ctx, incident, note.ID, collaborator); err != nil {
		t.Fatalf("author DeleteNote: %v", err)
	}
	if err := s.DeleteNote(ctx, incident, note.ID, collaborator); !errors.Is(err, ErrNoteNotFound) {
		t.Errorf("second DeleteNote = %v; want ErrNoteNotFound", err)
	}
}

func TestIncidentStore_DeleteNoteCascadesRevisions(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))
	note, err := s.CreateNote(ctx, incident, owner, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateNote(ctx, incident, note.ID, owner, "v2", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNote(ctx, incident, note.ID, owner); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_note_revisions WHERE note_id = $1`, note.ID); n != 0 {
		t.Errorf("%d revisions survived their note", n)
	}
}

func TestIncidentStore_CreateNoteOnMissingIncident(t *testing.T) {
	s, _ := newIncidentStore(t)
	if _, err := s.CreateNote(t.Context(), uuid.New(), testOwnerID(t), "x"); !errors.Is(err, ErrIncidentNotFound) {
		t.Errorf("CreateNote(missing incident) = %v; want ErrIncidentNotFound", err)
	}
}

// TestIncidentStore_ListNotesPagesOldestFirstAndExactly: notes page by
// (created_at, id) ascending with a keyset cursor, so every note is reachable
// however many there are (U23a review: the former 500 cap left later notes
// written but unlistable). The cursor is exact: a page that ends on the last
// note carries no cursor, and a cursor is only issued when a further note
// exists.
func TestIncidentStore_ListNotesPagesOldestFirstAndExactly(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))

	// 501 notes, created_at ascending with n, and a timestamp tie at the end
	// so the id tiebreak is exercised.
	if _, err := pool.Exec(ctx, `
		INSERT INTO incident_notes (incident_id, author_id, body, created_at)
		SELECT $1, $2, 'note ' || n, NOW() - (1000 - LEAST(n, 500)) * INTERVAL '1 second'
		  FROM generate_series(1, 501) AS n`, incident, owner); err != nil {
		t.Fatal(err)
	}

	var all []IncidentNoteRow
	cursor := ""
	pages := 0
	for {
		page, next, err := s.ListNotes(ctx, incident, IncidentMaxPageSize, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		all = append(all, page...)
		if next == "" {
			break
		}
		if len(page) != IncidentMaxPageSize {
			t.Fatalf("page %d has %d notes with a cursor; a cursor means a full page", pages, len(page))
		}
		cursor = next
		if pages > 10 {
			t.Fatal("cursor never ended")
		}
	}
	if len(all) != 501 || pages != 3 {
		t.Fatalf("paged %d notes over %d pages; want all 501 over 3 pages of %d", len(all), pages, IncidentMaxPageSize)
	}
	if all[0].Body != "note 1" || all[498].Body != "note 499" {
		t.Errorf("first/499th = %q/%q; want oldest first", all[0].Body, all[498].Body)
	}
	// The tied pair comes last, in id order.
	if tied := all[499].Body + "," + all[500].Body; tied != "note 500,note 501" && tied != "note 501,note 500" {
		t.Errorf("tied tail = %q; want notes 500 and 501", tied)
	}
	seen := map[uuid.UUID]bool{}
	for i, n := range all {
		if seen[n.ID] {
			t.Fatalf("note %s listed twice (position %d)", n.ID, i)
		}
		seen[n.ID] = true
		if i > 0 && (n.CreatedAt.Before(all[i-1].CreatedAt) || (n.CreatedAt.Equal(all[i-1].CreatedAt) && n.ID.String() <= all[i-1].ID.String())) {
			t.Fatalf("position %d is out of (created_at, id) order", i)
		}
	}

	// Exactly a page's worth: no cursor, because nothing follows.
	if _, err := pool.Exec(ctx, `DELETE FROM incident_notes WHERE incident_id = $1 AND body IN ('note 501')`, incident); err != nil {
		t.Fatal(err)
	}
	_, next, err := s.ListNotes(ctx, incident, 200, "")
	if err != nil || next == "" {
		t.Fatalf("first of 500 at 200: next=%q err=%v; want a cursor", next, err)
	}
	for i := 0; i < 2; i++ {
		_, next, err = s.ListNotes(ctx, incident, 200, next)
		if err != nil {
			t.Fatal(err)
		}
	}
	if next != "" {
		t.Fatalf("page ending on the 500th note carried a cursor %q; want none", next)
	}
	// Short page.
	page, next, err := s.ListNotes(ctx, incident, 0, "")
	if err != nil || len(page) != IncidentDefaultPageSize || next == "" {
		t.Fatalf("default page = (%d, %q, %v); want %d notes and a cursor", len(page), next, err, IncidentDefaultPageSize)
	}
	if _, _, err := s.ListNotes(ctx, incident, 0, "not-a-cursor"); !errors.Is(err, ErrInvalidNoteCursor) {
		t.Fatalf("bad cursor = %v; want ErrInvalidNoteCursor", err)
	}
}

func TestIncidentStore_DeleteCascades(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner, collaborator := testOwnerID(t), testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))
	note, err := s.CreateNote(ctx, incident, owner, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateNote(ctx, incident, note.ID, owner, "v2", 1); err != nil {
		t.Fatal(err)
	}
	insertGrant(t, pool, incident, collaborator, owner)
	insertEvidence(t, pool, incident, "k1")

	if err := s.Delete(ctx, incident, owner); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for table, sql := range map[string]string{
		"incident_notes":    `SELECT COUNT(*) FROM incident_notes WHERE incident_id = $1`,
		"incident_grants":   `SELECT COUNT(*) FROM incident_grants WHERE incident_id = $1`,
		"incident_evidence": `SELECT COUNT(*) FROM incident_evidence WHERE incident_id = $1`,
	} {
		if n := countRows(t, pool, sql, incident); n != 0 {
			t.Errorf("%d %s rows survived the incident's deletion", n, table)
		}
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_note_revisions WHERE note_id = $1`, note.ID); n != 0 {
		t.Errorf("%d incident_note_revisions rows survived the incident's deletion", n)
	}
}

func TestIncidentStore_CleanupDeletesOnlyExpired(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	old := mustCreateIncident(t, s, newIncident(owner, "old"))
	fresh := mustCreateIncident(t, s, newIncident(owner, "fresh"))
	if _, err := s.CreateNote(ctx, old, owner, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE incidents SET created_at = NOW() - INTERVAL '40 days' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}

	n, err := s.Cleanup(ctx, 30)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if n < 1 {
		t.Errorf("Cleanup deleted %d rows; want at least our expired one", n)
	}
	if got, err := s.Get(ctx, old); got != nil || err != nil {
		t.Errorf("expired incident after sweep = (%v, %v); want (nil, nil)", got, err)
	}
	mustGetIncident(t, s, fresh)
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_notes WHERE incident_id = $1`, old); n != 0 {
		t.Errorf("%d notes survived their incident's expiry", n)
	}
}

func TestIncidentStore_CleanupDeletesInBoundedBatches(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	for i := range 8 {
		mustCreateIncident(t, s, newIncident(owner, "expired-"+strconv.Itoa(i)))
	}
	// Older than any row another test ages, so ORDER BY created_at reaches ours first.
	if _, err := pool.Exec(ctx, `UPDATE incidents SET created_at = NOW() - INTERVAL '2000 days' WHERE owner_id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	mine := func() int {
		return countRows(t, pool, `SELECT COUNT(*) FROM incidents WHERE owner_id = $1`, owner)
	}

	// Stop after the first batch: exactly one batch of 3 is gone, the rest
	// remain, so a timeout part-way keeps its progress and no statement is
	// larger than a batch.
	var sizes []int64
	total, err := s.cleanupBatched(ctx, 30, 3, func(n int64) bool { sizes = append(sizes, n); return false })
	if err != nil {
		t.Fatalf("cleanupBatched: %v", err)
	}
	if total != 3 || len(sizes) != 1 || sizes[0] != 3 {
		t.Fatalf("first batch: total=%d sizes=%v, want exactly one batch of 3", total, sizes)
	}
	if got := mine(); got != 5 {
		t.Fatalf("%d of our incidents remain after one batch of 3, want 5", got)
	}

	// Run to completion in batches of 3: no batch exceeds 3, and ours all go.
	sizes = nil
	total, err = s.cleanupBatched(ctx, 30, 3, func(n int64) bool { sizes = append(sizes, n); return true })
	if err != nil {
		t.Fatalf("cleanupBatched: %v", err)
	}
	var sum int64
	for _, n := range sizes {
		if n > 3 {
			t.Errorf("batch deleted %d rows, want at most 3", n)
		}
		sum += n
	}
	if sum != total || total < 5 || len(sizes) < 2 {
		t.Errorf("total=%d sum=%d sizes=%v, want a multi-batch run deleting at least our 5", total, sum, sizes)
	}
	if got := mine(); got != 0 {
		t.Errorf("%d of our incidents survived the sweep", got)
	}
}

func TestIncidentStore_CleanupRejectsBadBatchSize(t *testing.T) {
	s := NewIncidentStore(nil)
	if _, err := s.cleanupBatched(t.Context(), 30, 0, nil); err == nil {
		t.Error("batch size 0 accepted; want an error before any SQL")
	}
}

func TestIncidentStore_RetentionLoweringImpact(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	if _, err := s.RetentionLoweringImpact(ctx, 0); err == nil {
		t.Error("retention 0 accepted; want an error before any SQL")
	}

	// The table is shared across tests, so assert on deltas and bounds.
	before, err := s.RetentionLoweringImpact(ctx, 30)
	if err != nil {
		t.Fatalf("RetentionLoweringImpact: %v", err)
	}
	age := func(title string, capturedUnder int, days int) {
		row := newIncident(owner, title)
		row.RetentionDaysAtCapture = capturedUnder
		id := mustCreateIncident(t, s, row)
		if _, err := pool.Exec(ctx, `UPDATE incidents SET created_at = NOW() - $2 * INTERVAL '1 day' WHERE id = $1`, id, days); err != nil {
			t.Fatal(err)
		}
	}
	age("lowered", 90, 60)          // older than 30d, inside its own 90d: counted
	age("expired normally", 30, 40) // older than its own 30d: not counted
	age("fresh", 90, 10)            // inside 30d: not counted

	after, err := s.RetentionLoweringImpact(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != before.Count+1 {
		t.Errorf("Count = %d, want %d (only the lowered incident is new)", after.Count, before.Count+1)
	}
	if after.Oldest.IsZero() || after.Newest.IsZero() || after.Oldest.After(after.Newest) {
		t.Errorf("bounds = %v..%v, want a populated, ordered range", after.Oldest, after.Newest)
	}
	// "fresh" (captured under 90d, 10 days old) is still inside the 30d window,
	// so it is a later-affected incident and NOT in the current count.
	if after.LaterCount != before.LaterCount+1 {
		t.Errorf("LaterCount = %d, want %d (only the fresh old-policy incident is new)", after.LaterCount, before.LaterCount+1)
	}

	// Once the sweep has run the warning condition clears: it cannot re-fire.
	if _, err := s.Cleanup(ctx, 30); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.RetentionLoweringImpact(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Count != 0 || !cleared.Oldest.IsZero() || !cleared.Newest.IsZero() {
		t.Errorf("after the sweep impact = %+v, want zero", cleared)
	}
	// The sweep does not touch the still-live incident, so LaterCount remains:
	// the warning can recur until those incidents age past the window.
	if cleared.LaterCount < after.LaterCount {
		t.Errorf("LaterCount fell to %d after the sweep, want it kept at %d", cleared.LaterCount, after.LaterCount)
	}

	// Empty path: no incident here is ten years old, so nothing is affected.
	none, err := s.RetentionLoweringImpact(ctx, IncidentMaxRetentionDays)
	if err != nil {
		t.Fatal(err)
	}
	if none.Count != 0 || none.LaterCount != 0 || !none.Oldest.IsZero() || !none.Newest.IsZero() {
		t.Errorf("impact at the maximum retention = %+v, want zero", none)
	}
}

func TestIncidentStore_CleanupHonoursCancelledContext(t *testing.T) {
	s, pool := newIncidentStore(t)
	owner := testOwnerID(t)
	old := mustCreateIncident(t, s, newIncident(owner, "old"))
	if _, err := pool.Exec(t.Context(), `UPDATE incidents SET created_at = NOW() - INTERVAL '40 days' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Cleanup(ctx, 30); !errors.Is(err, context.Canceled) {
		t.Errorf("Cleanup(cancelled ctx) = %v; want context.Canceled", err)
	}
	mustGetIncident(t, s, old)
	// Tidy our own expired row so a later sweep elsewhere has nothing of ours.
	if err := s.Delete(t.Context(), old, owner); err != nil {
		t.Fatal(err)
	}
}

// noteUpdateCanceller cancels a context the moment UpdateNote's body UPDATE
// starts, i.e. after the prior body was already copied into
// incident_note_revisions inside the same transaction.
type noteUpdateCanceller struct{ cancel context.CancelFunc }

func (c *noteUpdateCanceller) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if c.cancel != nil && strings.Contains(d.SQL, "UPDATE incident_notes") {
		c.cancel()
	}
	return ctx
}

func (c *noteUpdateCanceller) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestIncidentStore_UpdateNoteFailureMidTransactionChangesNothing(t *testing.T) {
	tracer := &noteUpdateCanceller{}
	pool := testDBWithOptions(t, 2, func(c *pgxpool.Config) { c.ConnConfig.Tracer = tracer })
	s := NewIncidentStore(pool)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))
	note, err := s.CreateNote(t.Context(), incident, owner, "original")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	tracer.cancel = cancel
	_, err = s.UpdateNote(ctx, incident, note.ID, owner, "never lands", 1)
	tracer.cancel = nil
	cancel()
	if err == nil {
		t.Fatal("UpdateNote succeeded although its UPDATE was cancelled")
	}

	reader := testDB(t)
	var (
		body string
		rev  int
	)
	if err := reader.QueryRow(t.Context(),
		`SELECT body, revision FROM incident_notes WHERE id = $1`, note.ID).Scan(&body, &rev); err != nil {
		t.Fatal(err)
	}
	if body != "original" || rev != 1 {
		t.Errorf("note after failed edit = %q rev %d; want the original at revision 1", body, rev)
	}
	if n := countRows(t, reader, `SELECT COUNT(*) FROM incident_note_revisions WHERE note_id = $1`, note.ID); n != 0 {
		t.Errorf("%d revision rows survived the rolled-back edit; want 0", n)
	}
	// The lock was released: a fresh edit at revision 1 succeeds.
	if _, err := NewIncidentStore(reader).UpdateNote(t.Context(), incident, note.ID, owner, "retry", 1); err != nil {
		t.Errorf("retry after the failed edit: %v", err)
	}
}

func TestIncidentSchema_CheckConstraintsRejectBadRows(t *testing.T) {
	_, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)

	insertIncident := func(extraCols, extraVals string, args ...any) error {
		all := append([]any{owner}, args...)
		_, err := pool.Exec(ctx, `
			INSERT INTO incidents (owner_id, cluster_id, title, window_start, retention_days_at_capture`+extraCols+`)
			VALUES ($1, $1, 't', NOW(), 30`+extraVals+`)`, all...)
		return err
	}
	if err := insertIncident("", ""); err != nil {
		t.Fatalf("a minimal valid incident was rejected: %v", err)
	}
	rejects := map[string]error{
		"bad status":               insertIncident(", status", ", 'resolved'"),
		"closed without closed_at": insertIncident(", status", ", 'closed'"),
		"inverted window":          insertIncident(", window_end", ", NOW() - INTERVAL '1 day'"),
	}
	// The title CHECK counts characters: 201 is one too many.
	_, err := pool.Exec(ctx, `
		INSERT INTO incidents (owner_id, title, window_start, retention_days_at_capture)
		VALUES ($1, repeat('€', 201), NOW(), 30)`, owner)
	rejects["overlong title"] = err
	_, err = pool.Exec(ctx, `
		INSERT INTO incidents (owner_id, title, window_start, retention_days_at_capture)
		VALUES ($1, '', NOW(), 30)`, owner)
	rejects["empty title"] = err
	for name, err := range rejects {
		if err == nil {
			t.Errorf("CHECK accepted an incident with %s", name)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO incidents (owner_id, title, window_start, retention_days_at_capture)
		VALUES ($1, 't', NOW(), 0)`, owner); err == nil {
		t.Error("CHECK accepted retention_days_at_capture = 0")
	}

	var incident uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO incidents (owner_id, title, window_start, retention_days_at_capture)
		VALUES ($1, 't', NOW(), 30) RETURNING id`, owner).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	evidence := func(mode, payload string, payloadBytes int) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO incident_evidence (incident_id, evidence_kind, mode, cluster_id, resource,
				source_kind, name, completeness, payload, payload_bytes, capture_key)
			VALUES ($1, 'object_summary', $2, 'local', 'pods', 'Pod', 'p', 'complete', $3::jsonb, $4, $5)`,
			incident, mode, nullableJSON(payload), payloadBytes, uuid.NewString())
		return err
	}
	if err := evidence("snapshot", "{}", 2); err != nil {
		t.Errorf("a valid snapshot was rejected: %v", err)
	}
	if err := evidence("live_link", "", 0); err != nil {
		t.Errorf("a valid live_link was rejected: %v", err)
	}
	if err := evidence("live_link", "{}", 0); err == nil {
		t.Error("CHECK accepted a live_link carrying a payload")
	}
	if err := evidence("snapshot", "", 0); err == nil {
		t.Error("CHECK accepted a snapshot without a payload")
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO incident_notes (incident_id, author_id, body) VALUES ($1, $2, '')`, incident, owner); err == nil {
		t.Error("CHECK accepted an empty note body")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM incidents WHERE owner_id = $1`, owner); err != nil {
		t.Fatal(err)
	}
}

// nullableJSON maps "" to a SQL NULL for the payload column.
func nullableJSON(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func incidentIDs(rows []IncidentRow) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// TestMigration000024_RoundTripLeavesOtherTablesIntact applies 000024 on a
// throwaway database that already holds the 000023 schema, checks the shape
// and constraints, populates it, rolls it back and confirms exactly the five
// incident tables disappeared, then re-applies it.
func TestMigration000024_RoundTripLeavesOtherTablesIntact(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()

	if err := m.Migrate(23); err != nil {
		t.Fatalf("migrating to 000023: %v", err)
	}
	before := publicTables(t, pool)
	if slices.Contains(before, "incidents") {
		t.Fatal("incidents exists before 000024")
	}
	// Unrelated rows that must survive the round trip.
	if _, err := pool.Exec(ctx,
		`INSERT INTO change_receipts (id, owner_id, content_digest, document_count) VALUES ($1, 'o', 'sha256:x', 1)`,
		uuid.New()); err != nil {
		t.Fatalf("seeding an unrelated row: %v", err)
	}

	if err := m.Migrate(24); err != nil {
		t.Fatalf("applying 000024: %v", err)
	}
	for _, table := range incidentTables {
		if !tableExists(t, pool, table) {
			t.Errorf("table %s missing after 000024", table)
		}
	}
	for _, idx := range incidentIndexes {
		if !indexExists(t, pool, idx) {
			t.Errorf("index %s missing after 000024", idx)
		}
	}
	if got, want := slices.Sorted(slices.Values(publicTables(t, pool))),
		slices.Sorted(slices.Values(append(slices.Clone(before), incidentTables...))); !slices.Equal(got, want) {
		t.Errorf("tables after up = %v; want %v", got, want)
	}

	// Populate every table, then prove the CHECKs and FKs are live.
	s := NewIncidentStore(pool)
	id, err := s.Create(ctx, IncidentRow{OwnerID: "o", Title: "t", WindowStart: time.Now(), RetentionDaysAtCapture: 30})
	if err != nil {
		t.Fatalf("Create on the scratch schema: %v", err)
	}
	if _, err := s.CreateNote(ctx, id, "o", "n"); err != nil {
		t.Fatalf("CreateNote on the scratch schema: %v", err)
	}
	insertGrant(t, pool, id, "g", "o")
	insertEvidence(t, pool, id, "k")
	if _, err := pool.Exec(ctx,
		`INSERT INTO incidents (owner_id, title, window_start, retention_days_at_capture, status) VALUES ('o', 't', NOW(), 30, 'bogus')`); err == nil {
		t.Error("the status CHECK accepted 'bogus'")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO incident_grants (incident_id, grantee_id, granted_by) VALUES ($1, 'g', 'o')`, uuid.New()); err == nil {
		t.Error("a grant for a nonexistent incident was accepted")
	}

	if err := m.Migrate(23); err != nil {
		t.Fatalf("rolling back 000024: %v", err)
	}
	after := publicTables(t, pool)
	if !slices.Equal(slices.Sorted(slices.Values(after)), slices.Sorted(slices.Values(before))) {
		t.Errorf("tables after down = %v; want exactly the pre-000024 set %v", after, before)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM change_receipts`); n != 1 {
		t.Errorf("unrelated change_receipts rows after rollback = %d; want 1", n)
	}

	// Re-applying after a rollback works (the down leaves nothing behind).
	if err := m.Migrate(24); err != nil {
		t.Fatalf("re-applying 000024 after rollback: %v", err)
	}
}

// olderBinaryMigrator returns a migrator whose embedded source stops at
// maxVersion, which is what the runner inside an older backend image sees,
// pointed at the same database as pool.
func olderBinaryMigrator(t *testing.T, pool *pgxpool.Pool, maxVersion uint64) *migrate.Migrate {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	truncated := fstest.MapFS{}
	for _, e := range entries {
		seq, _, ok := strings.Cut(e.Name(), "_")
		if !ok || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := strconv.ParseUint(seq, 10, 64)
		if err != nil || v > maxVersion {
			continue
		}
		b, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		truncated["migrations/"+e.Name()] = &fstest.MapFile{Data: b}
	}
	source, err := iofs.New(truncated, "migrations")
	if err != nil {
		t.Fatalf("creating truncated migration source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, pool.Config().ConnString())
	if err != nil {
		t.Fatalf("creating older-binary migrator: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// TestMigration000024_DocumentedRollbackKeepsDataAndRollsForward walks the
// NOTES.txt (000024) rollback procedure on a scratch database: an older
// binary's runner refuses a database recorded at 24; after the documented
// version reset it starts cleanly with the incident tables left in place; and
// a later roll-forward re-runs 000024 over the existing tables without error
// or data loss (every statement in the up file is re-runnable).
func TestMigration000024_DocumentedRollbackKeepsDataAndRollsForward(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()

	if err := m.Migrate(24); err != nil {
		t.Fatalf("migrating to 000024: %v", err)
	}
	s := NewIncidentStore(pool)
	id, err := s.Create(ctx, IncidentRow{OwnerID: "o", Title: "kept", WindowStart: time.Now(), RetentionDaysAtCapture: 30})
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateNote(ctx, id, "o", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateNote(ctx, id, note.ID, "o", "n2", 1); err != nil {
		t.Fatal(err)
	}

	// The failure the procedure exists to avoid: an image built before 000024
	// cannot run its migrations against a database recorded at version 24.
	older := olderBinaryMigrator(t, pool, 23)
	if err := older.Up(); err == nil || errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("older runner against version 24 = %v; want a hard error (the reason for the documented reset)", err)
	}

	// The documented step, run before deploying the older image.
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET version = 23, dirty = false`); err != nil {
		t.Fatal(err)
	}
	if err := older.Up(); !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("older runner after the version reset = %v; want ErrNoChange (a clean start)", err)
	}
	for _, table := range incidentTables {
		if !tableExists(t, pool, table) {
			t.Errorf("table %s was removed by the rollback procedure; it must be left in place", table)
		}
	}

	// Roll forward with the current binary's runner: 000024 runs again over
	// the tables it already created.
	if err := m.Migrate(24); err != nil {
		t.Fatalf("roll-forward re-running 000024 over existing tables: %v", err)
	}
	version, dirty, err := m.Version()
	if err != nil || dirty || version != 24 {
		t.Fatalf("after roll-forward version=%d dirty=%v err=%v; want 24, clean", version, dirty, err)
	}
	got, err := s.Get(ctx, id)
	if err != nil || got == nil || got.Title != "kept" {
		t.Fatalf("incident after rollback and roll-forward = (%+v, %v); want it intact", got, err)
	}
	revs, err := s.ListNoteRevisions(ctx, id, note.ID)
	if err != nil || len(revs) != 1 || revs[0].Body != "n1" {
		t.Fatalf("note history after rollback and roll-forward = (%+v, %v); want the one prior body", revs, err)
	}
	for _, idx := range incidentIndexes {
		if !indexExists(t, pool, idx) {
			t.Errorf("index %s missing after roll-forward", idx)
		}
	}
}

// noteLockRendezvous holds the first UpdateNote transaction that reads the
// note row until a second transaction has also read it (or a timeout passes),
// so two editors overlap deterministically. It keys on the read itself, not on
// FOR UPDATE, so it still synchronizes if the lock clause is removed.
type noteLockRendezvous struct {
	mu     sync.Mutex
	reads  int
	second chan struct{}
}

func (r *noteLockRendezvous) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (r *noteLockRendezvous) TraceQueryEnd(_ context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if d.Err != nil || !strings.Contains(d.CommandTag.String(), "SELECT") {
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

func TestIncidentStore_ConcurrentUpdateNoteYieldsOneWinnerOneConflict(t *testing.T) {
	setup := testDB(t)
	s := NewIncidentStore(setup)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, s, newIncident(owner, "t"))
	note, err := s.CreateNote(t.Context(), incident, owner, "v1")
	if err != nil {
		t.Fatal(err)
	}

	// The traced pool is used only by the two racing edits, so its SELECTs
	// are exactly their note reads.
	rv := &noteLockRendezvous{second: make(chan struct{})}
	raced := NewIncidentStore(testDBWithOptions(t, 4, func(c *pgxpool.Config) { c.ConnConfig.Tracer = rv }))

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
			_, results[i] = raced.UpdateNote(t.Context(), incident, note.ID, owner, "edit "+strconv.Itoa(i), 1)
		}()
	}
	close(start)
	wg.Wait()

	var wins, conflicts int
	for _, err := range results {
		var conflict *NoteRevisionConflictError
		switch {
		case err == nil:
			wins++
		case errors.As(err, &conflict) && conflict.Current == 2:
			conflicts++
		default:
			t.Errorf("racing UpdateNote returned %v; want success or a revision conflict at current 2", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d; want exactly one of each", wins, conflicts)
	}
	revs, err := s.ListNoteRevisions(t.Context(), incident, note.ID)
	if err != nil || len(revs) != 1 || revs[0].Body != "v1" {
		t.Errorf("revisions = (%+v, %v); want exactly the v1 body", revs, err)
	}
}

func TestIncidentStore_CleanupHonoursRetentionDays(t *testing.T) {
	s, pool := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	forty := mustCreateIncident(t, s, newIncident(owner, "40 days"))
	inside := mustCreateIncident(t, s, newIncident(owner, "29 days"))
	for id, age := range map[uuid.UUID]string{forty: "40 days", inside: "29 days"} {
		if _, err := pool.Exec(ctx, `UPDATE incidents SET created_at = NOW() - $2::interval WHERE id = $1`, id, age); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.Cleanup(ctx, 60); err != nil {
		t.Fatal(err)
	}
	mustGetIncident(t, s, forty)
	mustGetIncident(t, s, inside)

	if _, err := s.Cleanup(ctx, 30); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, forty); got != nil || err != nil {
		t.Errorf("40-day-old incident after a 30-day sweep = (%v, %v); want deleted", got, err)
	}
	mustGetIncident(t, s, inside)
	if err := s.Delete(ctx, inside, owner); err != nil {
		t.Fatal(err)
	}
}

func TestIncidentStore_ListVisibleFullFinalPageEndsWithEmptyPage(t *testing.T) {
	s, _ := newIncidentStore(t)
	ctx := t.Context()
	owner := testOwnerID(t)
	for i := range 4 {
		mustCreateIncident(t, s, newIncident(owner, "row "+strconv.Itoa(i)))
	}

	first, cursor, err := s.ListVisible(ctx, owner, 2, "")
	if err != nil || len(first) != 2 || cursor == "" {
		t.Fatalf("page 1 = %d rows, cursor %q, err %v; want 2 rows and a cursor", len(first), cursor, err)
	}
	second, cursor, err := s.ListVisible(ctx, owner, 2, cursor)
	if err != nil || len(second) != 2 || cursor == "" {
		t.Fatalf("page 2 = %d rows, cursor %q, err %v; want 2 rows and a cursor (a full page may have more)", len(second), cursor, err)
	}
	third, cursor, err := s.ListVisible(ctx, owner, 2, cursor)
	if err != nil || len(third) != 0 || cursor != "" {
		t.Fatalf("page 3 = %d rows, cursor %q, err %v; want an empty page and no cursor", len(third), cursor, err)
	}
	if ids := incidentIDs(append(first, second...)); len(slices.Compact(slices.SortedFunc(slices.Values(ids),
		func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) }))) != 4 {
		t.Errorf("pages 1 and 2 = %v; want 4 distinct incidents", ids)
	}
}
