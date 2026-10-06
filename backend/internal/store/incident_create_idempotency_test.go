package store

// incident_create_idempotency_test.go — coverage for
// IncidentStore.CreateWithRequestID and migration 000026 (Release D, U25c).
//
// The env-gated tests follow incidents_test.go's isolation contract: every
// incident is owned by an id from testOwnerID(t) and every assertion reads
// only that owner's rows.

import (
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIncidentStore_CreateWithRequestIDRejectsInvalidInputBeforeSQL(t *testing.T) {
	s := NewIncidentStore(nil) // any SQL would panic on the nil pool
	good := IncidentRow{OwnerID: "o", Title: "t", WindowStart: time.Now(), RetentionDaysAtCapture: 30}
	if _, _, err := s.CreateWithRequestID(t.Context(), good, uuid.Nil); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("CreateWithRequestID(nil request id) = %v; want ErrIncidentInvalid", err)
	}
	bad := good
	bad.Title = ""
	if _, _, err := s.CreateWithRequestID(t.Context(), bad, uuid.New()); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("CreateWithRequestID(empty title) = %v; want ErrIncidentInvalid", err)
	}
	bad = good
	bad.OwnerID = ""
	if _, _, err := s.CreateWithRequestID(t.Context(), bad, uuid.New()); !errors.Is(err, ErrIncidentInvalid) {
		t.Errorf("CreateWithRequestID(empty owner) = %v; want ErrIncidentInvalid", err)
	}
}

func countOwnerIncidents(t *testing.T, pool *pgxpool.Pool, owner string) int {
	t.Helper()
	return countRows(t, pool, `SELECT COUNT(*) FROM incidents WHERE owner_id = $1`, owner)
}

func TestIncidentStore_CreateWithRequestIDReplayReturnsTheFirstIncident(t *testing.T) {
	s, pool := newIncidentStore(t)
	owner := testOwnerID(t)
	reqID := uuid.New()

	first := newIncident(owner, "first attempt")
	id, created, err := s.CreateWithRequestID(t.Context(), first, reqID)
	if err != nil || !created || id == uuid.Nil {
		t.Fatalf("first create = (%s, created=%v, %v); want a new incident", id, created, err)
	}

	// The retry differs in every create field: the stored incident wins.
	retry := newIncident(owner, "retried with another title")
	end := retry.WindowStart.Add(time.Hour)
	retry.WindowEnd = &end
	again, created, err := s.CreateWithRequestID(t.Context(), retry, reqID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again != id || created {
		t.Fatalf("replay = (%s, created=%v); want (%s, created=false)", again, created, id)
	}
	if got := mustGetIncident(t, s, id); got.Title != "first attempt" || got.WindowEnd != nil {
		t.Errorf("stored incident after replay = %q window end %v; want the first write unchanged", got.Title, got.WindowEnd)
	}
	if n := countOwnerIncidents(t, pool, owner); n != 1 {
		t.Errorf("owner has %d incidents after a replay; want 1", n)
	}

	// A different request id from the same owner is a different incident, and
	// a plain Create never conflicts with either.
	other, created, err := s.CreateWithRequestID(t.Context(), first, uuid.New())
	if err != nil || !created || other == id {
		t.Errorf("create with a fresh request id = (%s, created=%v, %v); want a second incident", other, created, err)
	}
	mustCreateIncident(t, s, first)
	mustCreateIncident(t, s, first)
	if n := countOwnerIncidents(t, pool, owner); n != 4 {
		t.Errorf("owner has %d incidents; want 4", n)
	}
}

func TestIncidentStore_CreateWithRequestIDIsScopedPerOwner(t *testing.T) {
	s, _ := newIncidentStore(t)
	alice, bob := testOwnerID(t)+"-a", testOwnerID(t)+"-b"
	reqID := uuid.New()

	a, created, err := s.CreateWithRequestID(t.Context(), newIncident(alice, "alice's"), reqID)
	if err != nil || !created {
		t.Fatalf("alice create = (created=%v, %v)", created, err)
	}
	b, created, err := s.CreateWithRequestID(t.Context(), newIncident(bob, "bob's"), reqID)
	if err != nil || !created {
		t.Fatalf("bob create with alice's request id = (created=%v, %v); want his own new incident", created, err)
	}
	if a == b {
		t.Fatal("bob's create with the same request id returned alice's incident")
	}
	if got := mustGetIncident(t, s, b); got.OwnerID != bob || got.Title != "bob's" {
		t.Errorf("bob's incident = owner %q title %q", got.OwnerID, got.Title)
	}
}

func TestIncidentStore_CreateWithRequestIDAfterDeleteCreatesANewIncident(t *testing.T) {
	s, _ := newIncidentStore(t)
	owner := testOwnerID(t)
	reqID := uuid.New()

	id, _, err := s.CreateWithRequestID(t.Context(), newIncident(owner, "t"), reqID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(t.Context(), id, owner); err != nil {
		t.Fatal(err)
	}
	again, created, err := s.CreateWithRequestID(t.Context(), newIncident(owner, "t"), reqID)
	if err != nil || !created || again == id {
		t.Fatalf("create after delete = (%s, created=%v, %v); want a new incident, not %s", again, created, err, id)
	}
}

func TestIncidentStore_CreateWithRequestIDConcurrentCreatesMakeOneRow(t *testing.T) {
	const n = 16
	pool := testDBWithOptions(t, n, nil)
	s := NewIncidentStore(pool)
	owner := testOwnerID(t)
	reqID := uuid.New()

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		ids     [n]uuid.UUID
		created [n]bool
		errs    [n]error
	)
	for i := range n {
		wg.Go(func() {
			<-start
			ids[i], created[i], errs[i] = s.CreateWithRequestID(t.Context(), newIncident(owner, "racing"), reqID)
		})
	}
	close(start)
	wg.Wait()

	creators := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Errorf("goroutine %d got %s; want every caller to get %s", i, ids[i], ids[0])
		}
		if created[i] {
			creators++
		}
	}
	if creators != 1 {
		t.Errorf("%d callers report created=true; want exactly 1", creators)
	}
	if got := countOwnerIncidents(t, pool, owner); got != 1 {
		t.Errorf("%d incidents stored; want 1", got)
	}
}

func TestMigration000026_UpAndDownAreWellFormed(t *testing.T) {
	read := func(direction string) string {
		b, err := fs.ReadFile(migrationsFS, "migrations/000026_incident_client_request_id."+direction+".sql")
		if err != nil {
			t.Fatalf("reading 000026 %s migration: %v", direction, err)
		}
		return string(b)
	}
	up, down := read("up"), read("down")
	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS client_request_id UUID",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_owner_client_request",
		"(owner_id, client_request_id)",
		"WHERE client_request_id IS NOT NULL",
		"COMMENT ON COLUMN incidents.client_request_id",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("000026 up migration is missing %q", want)
		}
	}
	for _, want := range []string{"DROP INDEX IF EXISTS idx_incidents_owner_client_request",
		"DROP COLUMN IF EXISTS client_request_id", "NOTES.txt (000026)"} {
		if !strings.Contains(down, want) {
			t.Errorf("000026 down migration is missing %q", want)
		}
	}
	if strings.Contains(down, "DROP TABLE") {
		t.Error("000026 down migration drops a table; it must drop only the column and its index")
	}
}

func TestMigration000026_RoundTripKeepsIncidents(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(26); err != nil {
		t.Fatalf("migrating to 000026: %v", err)
	}
	if !indexExists(t, pool, "idx_incidents_owner_client_request") {
		t.Fatal("idx_incidents_owner_client_request missing after 000026")
	}
	s := NewIncidentStore(pool)
	row := IncidentRow{OwnerID: "o", Title: "kept", WindowStart: time.Now(), RetentionDaysAtCapture: 30}
	reqID := uuid.New()
	id, created, err := s.CreateWithRequestID(ctx, row, reqID)
	if err != nil || !created {
		t.Fatalf("create at 26 = (created=%v, %v)", created, err)
	}

	if err := m.Migrate(25); err != nil {
		t.Fatalf("rolling back 000026: %v", err)
	}
	if indexExists(t, pool, "idx_incidents_owner_client_request") {
		t.Error("the 000026 index survived the down migration")
	}
	var hasColumn bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'incidents' AND column_name = 'client_request_id')`).Scan(&hasColumn); err != nil {
		t.Fatal(err)
	}
	if hasColumn {
		t.Error("incidents.client_request_id survived the down migration")
	}
	if got, err := s.Get(ctx, id); err != nil || got == nil || got.Title != "kept" {
		t.Errorf("incident after the 000026 rollback = (%+v, %v); want it intact", got, err)
	}

	// Re-applying works, and the up file re-runs cleanly over its own column
	// and index (the documented rollback may leave them in place).
	if err := m.Migrate(26); err != nil {
		t.Fatalf("re-applying 000026: %v", err)
	}
	up, err := fs.ReadFile(migrationsFS, "migrations/000026_incident_client_request_id.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Errorf("re-running the 000026 up file over its own column failed: %v", err)
	}
	if _, created, err := s.CreateWithRequestID(ctx, row, reqID); err != nil || !created {
		t.Errorf("create after re-apply = (created=%v, %v); the pre-rollback key was dropped, so want a new incident", created, err)
	}
	if _, created, err := s.CreateWithRequestID(ctx, row, reqID); err != nil || created {
		t.Errorf("replay after re-apply = (created=%v, %v); want the existing incident", created, err)
	}
}
