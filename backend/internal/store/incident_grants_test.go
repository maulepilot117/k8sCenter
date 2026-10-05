package store

// incident_grants_test.go — coverage for IncidentGrantStore (Release D, U21b):
// grantee validation, the owner gate asserted in SQL, self-grant, upsert,
// immediate revocation, the per-incident cap (including under concurrency),
// the lock timeout and cascade on incident delete. DB tests are env-gated
// (testDB) and isolate by testOwnerID(t).

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

// TestConcurrentAddGrantNeverExceedsCap races two new grantees at 49 grants.
// The rendezvous keys on AddGrant's incident read, so without FOR UPDATE both
// count 49 and both insert.
func TestConcurrentAddGrantNeverExceedsCap(t *testing.T) {
	is, gs, pool := newGrantStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "grant race"))
	for i := range IncidentMaxGrants - 1 {
		if err := gs.AddGrant(t.Context(), incident, owner, owner+"-g"+strconv.Itoa(i), true); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}

	rv := newIncidentLockRendezvous("SELECT owner_id FROM incidents WHERE id = $1")
	raced := NewIncidentGrantStore(testDBWithOptions(t, 4, func(c *pgxpool.Config) { c.ConnConfig.Tracer = rv }))
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
			results[i] = raced.AddGrant(t.Context(), incident, owner, owner+"-racer"+strconv.Itoa(i), true)
		}()
	}
	close(start)
	wg.Wait()

	var wins, limited int
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrGrantLimit):
			limited++
		default:
			t.Errorf("racing AddGrant returned %v; want success or ErrGrantLimit", err)
		}
	}
	if wins != 1 || limited != 1 {
		t.Errorf("wins=%d limited=%d; want exactly one of each", wins, limited)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_grants WHERE incident_id = $1`, incident); n != IncidentMaxGrants {
		t.Errorf("%d grants after the race; want %d", n, IncidentMaxGrants)
	}
}

func TestAddGrantLockTimeoutIsBusy(t *testing.T) {
	is, gs, pool := newGrantStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "grant busy"))
	if gs.lockTimeout != incidentLockTimeout {
		t.Fatalf("default lock timeout = %s; want incidentLockTimeout", gs.lockTimeout)
	}
	gs.lockTimeout = 200 * time.Millisecond
	release := holdIncidentLock(t, incident)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := gs.AddGrant(ctx, incident, owner, owner+"-g", true); !errors.Is(err, ErrIncidentBusy) {
		t.Fatalf("grant behind a held lock = %v; want ErrIncidentBusy", err)
	}
	release()
	if n := countRows(t, pool, `SELECT COUNT(*) FROM incident_grants WHERE incident_id = $1`, incident); n != 0 {
		t.Errorf("%d grants written by the timed-out call; want 0", n)
	}
	if err := gs.AddGrant(t.Context(), incident, owner, owner+"-g", true); err != nil {
		t.Errorf("grant after release = %v", err)
	}
}

// TestAddGrantUpsertLockWaitIsBusy: the incident lock is free, but the
// grant row the upsert must update is locked elsewhere (as a concurrent
// RemoveGrant would hold it). The lock timeout fires on the INSERT, not on the
// incident read, and must still surface as ErrIncidentBusy.
func TestAddGrantUpsertLockWaitIsBusy(t *testing.T) {
	is, gs, _ := newGrantStores(t)
	owner := testOwnerID(t)
	grantee := owner + "-g"
	incident := mustCreateIncident(t, is, newIncident(owner, "grant row busy"))
	if err := gs.AddGrant(t.Context(), incident, owner, grantee, false); err != nil {
		t.Fatal(err)
	}
	gs.lockTimeout = 200 * time.Millisecond

	holder := testDB(t)
	tx, err := holder.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := tx.Exec(t.Context(),
		`SELECT 1 FROM incident_grants WHERE incident_id = $1 AND grantee_id = $2 FOR UPDATE`, incident, grantee); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := gs.AddGrant(ctx, incident, owner, grantee, true); !errors.Is(err, ErrIncidentBusy) {
		t.Fatalf("re-grant behind a locked grant row = %v; want ErrIncidentBusy", err)
	}
	_ = tx.Rollback(t.Context())
	if g, err := gs.GetGrant(t.Context(), incident, grantee); err != nil || g == nil || g.CanAnnotate {
		t.Errorf("grant after the busy re-grant = (%+v, %v); want it unchanged (read-only)", g, err)
	}
}
