package store

// preferences_test.go — PostgreSQL-backed coverage for PreferenceStore and
// migration 000018 (Release A, U1).
//
// Every test here obtains its pool from testDB(t) and scopes every row it
// writes to testOwnerID(t), per the isolation contract in testdb_test.go:
// user_preferences is owner-scoped, so residue from a failed run is invisible
// to every other suite and nothing needs truncating.
//
// The one exception is TestPreferenceMigration_UpDownUp, which must roll a
// migration back. Rolling the SHARED test database back to 000017 would
// destroy the schema every other test in the binary depends on, so that test
// builds its own throwaway database on the same server instead. See the
// comment on that function.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// newPreferenceStore returns a store over the shared test database. The
// calling test is skipped when no test database is configured.
func newPreferenceStore(t *testing.T) *PreferenceStore {
	t.Helper()
	return NewPreferenceStore(testDB(t))
}

// savedView builds an unsaved saved-view record with the dedup key the
// handler layer will derive (lower(name)).
func savedView(ownerID, name string) PreferenceRecord {
	return PreferenceRecord{
		OwnerID:       ownerID,
		Kind:          PreferenceKindSavedView,
		Name:          name,
		ClusterID:     "local",
		DedupKey:      strings.ToLower(name),
		SchemaVersion: 1,
		Config: json.RawMessage(
			`{"schemaVersion":1,"resourceKind":"pods","namespace":"","search":"","statusFilter":"all","sortKey":"name","sortDir":"asc"}`),
	}
}

// pinRecord builds an unsaved pin record. The dedup key is
// "<resourceKind>/<namespace>/<name>" — deliberately excluding uid, so a
// recreated object collides with the existing pin (D3, Correction 7).
func pinRecord(ownerID, resourceKind, namespace, name, uid string) PreferenceRecord {
	return PreferenceRecord{
		OwnerID:       ownerID,
		Kind:          PreferenceKindPin,
		Name:          name,
		ClusterID:     "local",
		DedupKey:      fmt.Sprintf("%s/%s/%s", resourceKind, namespace, name),
		SchemaVersion: 1,
		Config: json.RawMessage(fmt.Sprintf(
			`{"schemaVersion":1,"resourceKind":%q,"group":"","version":"","namespace":%q,"name":%q,"uid":%q,"displayKind":"Deployment"}`,
			resourceKind, namespace, name, uid)),
	}
}

// mustCreate creates a record and fails the test on any error.
func mustCreate(t *testing.T, s *PreferenceStore, rec PreferenceRecord, maxPerKind int) *PreferenceRecord {
	t.Helper()

	got, err := s.Create(t.Context(), rec, maxPerKind)
	if err != nil {
		t.Fatalf("Create(%s/%s): %v", rec.Kind, rec.Name, err)
	}
	return got
}

// testMaxPerKind is a ceiling high enough not to interfere with tests that
// are not about limits.
const testMaxPerKind = 100

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

func TestPreferenceStore_CreateAndList(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	created := mustCreate(t, s, savedView(owner, "Prod failing pods"), testMaxPerKind)

	if created.ID == uuid.Nil {
		t.Error("Create returned the nil UUID; want a generated id")
	}
	if created.OwnerID != owner {
		t.Errorf("OwnerID = %q; want %q", created.OwnerID, owner)
	}
	if created.Revision != 1 {
		t.Errorf("Revision = %d; want 1 on create", created.Revision)
	}
	if created.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d; want 1", created.SchemaVersion)
	}
	if created.ClusterID != "local" {
		t.Errorf("ClusterID = %q; want %q", created.ClusterID, "local")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("timestamps not populated: created=%v updated=%v", created.CreatedAt, created.UpdatedAt)
	}

	var cfg map[string]any
	if err := json.Unmarshal(created.Config, &cfg); err != nil {
		t.Fatalf("returned config is not valid JSON: %v", err)
	}
	if cfg["resourceKind"] != "pods" {
		t.Errorf("config.resourceKind = %v; want pods", cfg["resourceKind"])
	}

	// A pin under the same owner must not appear in the saved-view listing.
	mustCreate(t, s, pinRecord(owner, "deployments", "prod", "api", "uid-1"), testMaxPerKind)

	views, err := s.List(ctx, owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List(saved_view): %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("List(saved_view) returned %d records; want 1", len(views))
	}
	if views[0].ID != created.ID {
		t.Errorf("List returned id %s; want %s", views[0].ID, created.ID)
	}

	pins, err := s.List(ctx, owner, PreferenceKindPin)
	if err != nil {
		t.Fatalf("List(pin): %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("List(pin) returned %d records; want 1", len(pins))
	}

	got, err := s.Get(ctx, owner, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Prod failing pods" {
		t.Errorf("Get().Name = %q; want %q", got.Name, "Prod failing pods")
	}

	n, err := s.CountByKind(ctx, owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("CountByKind: %v", err)
	}
	if n != 1 {
		t.Errorf("CountByKind(saved_view) = %d; want 1", n)
	}

	if err := s.Delete(ctx, owner, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, owner, created.ID); !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("Get after Delete = %v; want ErrPreferenceNotFound", err)
	}
	if err := s.Delete(ctx, owner, created.ID); !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("second Delete = %v; want ErrPreferenceNotFound", err)
	}
}

func TestPreferenceStore_ListOrdersByUpdatedAtDesc(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	first := mustCreate(t, s, savedView(owner, "first"), testMaxPerKind)
	second := mustCreate(t, s, savedView(owner, "second"), testMaxPerKind)

	// Touch the older record so it becomes the most recently updated.
	if _, err := s.Update(ctx, owner, first.ID, first.Revision,
		first.Name, first.DedupKey, first.SchemaVersion, first.Config); err != nil {
		t.Fatalf("Update(first): %v", err)
	}

	views, err := s.List(ctx, owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("List returned %d records; want 2", len(views))
	}
	if views[0].ID != first.ID || views[1].ID != second.ID {
		t.Errorf("List order = [%s %s]; want most-recently-updated first [%s %s]",
			views[0].ID, views[1].ID, first.ID, second.ID)
	}
}

func TestPreferenceStore_ListEmptyReturnsNoError(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	views, err := s.List(t.Context(), owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List on empty owner: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("List returned %d records for a fresh owner; want 0", len(views))
	}
}

// ---------------------------------------------------------------------------
// Owner isolation (D1) — a record belonging to another owner must be
// indistinguishable from one that does not exist.
// ---------------------------------------------------------------------------

func TestPreferenceStore_TwoUsersSameName_Isolated(t *testing.T) {
	s := newPreferenceStore(t)
	alice, bob := testOwnerID(t), testOwnerID(t)
	ctx := t.Context()

	aliceView := mustCreate(t, s, savedView(alice, "My view"), testMaxPerKind)
	bobView := mustCreate(t, s, savedView(bob, "My view"), testMaxPerKind)

	if aliceView.ID == bobView.ID {
		t.Fatal("both owners received the same record id")
	}

	aliceList, err := s.List(ctx, alice, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List(alice): %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ID != aliceView.ID {
		t.Fatalf("alice sees %d records; want exactly her own", len(aliceList))
	}

	// Bob cannot overwrite Alice's record even holding its real revision.
	if _, err := s.Update(ctx, bob, aliceView.ID, aliceView.Revision,
		"hijacked", "hijacked", 1, aliceView.Config); !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("Update across owners = %v; want ErrPreferenceNotFound", err)
	}

	after, err := s.Get(ctx, alice, aliceView.ID)
	if err != nil {
		t.Fatalf("Get(alice) after cross-owner update attempt: %v", err)
	}
	if after.Name != "My view" || after.Revision != 1 {
		t.Errorf("alice's record was mutated: name=%q revision=%d", after.Name, after.Revision)
	}
}

func TestPreferenceStore_GetOtherOwner_ReturnsNotFound(t *testing.T) {
	s := newPreferenceStore(t)
	alice, bob := testOwnerID(t), testOwnerID(t)

	rec := mustCreate(t, s, savedView(alice, "alice view"), testMaxPerKind)

	got, err := s.Get(t.Context(), bob, rec.ID)
	if !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("Get(bob, alice's id) = %v; want ErrPreferenceNotFound", err)
	}
	if got != nil {
		t.Errorf("Get across owners leaked a record: %+v", got)
	}
}

func TestPreferenceStore_UpdateOtherOwner_ReturnsNotFound(t *testing.T) {
	s := newPreferenceStore(t)
	alice, bob := testOwnerID(t), testOwnerID(t)

	rec := mustCreate(t, s, savedView(alice, "alice view"), testMaxPerKind)

	// The existence probe behind the conflict/not-found split MUST carry
	// owner_id. Without it this call would return ErrPreferenceConflict and
	// leak that the id exists.
	_, err := s.Update(t.Context(), bob, rec.ID, 999, "x", "x", 1, rec.Config)
	if !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("Update(bob, alice's id, stale revision) = %v; want ErrPreferenceNotFound", err)
	}
}

func TestPreferenceStore_DeleteOtherOwner_ReturnsNotFound(t *testing.T) {
	s := newPreferenceStore(t)
	alice, bob := testOwnerID(t), testOwnerID(t)

	rec := mustCreate(t, s, savedView(alice, "alice view"), testMaxPerKind)

	if err := s.Delete(t.Context(), bob, rec.ID); !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("Delete(bob, alice's id) = %v; want ErrPreferenceNotFound", err)
	}
	if _, err := s.Get(t.Context(), alice, rec.ID); err != nil {
		t.Errorf("alice's record was deleted by bob: %v", err)
	}
}

func TestPreferenceStore_CountByKindIsOwnerScoped(t *testing.T) {
	s := newPreferenceStore(t)
	alice, bob := testOwnerID(t), testOwnerID(t)

	mustCreate(t, s, savedView(alice, "a1"), testMaxPerKind)
	mustCreate(t, s, savedView(alice, "a2"), testMaxPerKind)
	mustCreate(t, s, savedView(bob, "b1"), testMaxPerKind)

	n, err := s.CountByKind(t.Context(), bob, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("CountByKind: %v", err)
	}
	if n != 1 {
		t.Errorf("CountByKind(bob) = %d; want 1 — count is leaking other owners", n)
	}
}

// TestPreferenceStore_ProviderQualifiedOwnerIDs proves OIDC and LDAP ids
// round-trip verbatim, including colons, commas and equals signs.
func TestPreferenceStore_ProviderQualifiedOwnerIDs(t *testing.T) {
	s := newPreferenceStore(t)
	ctx := t.Context()
	unique := testOwnerID(t)

	tests := []struct {
		name    string
		ownerID string
	}{
		{"local opaque id", unique},
		{"oidc subject", "oidc:corp:sub-123-" + unique},
		{"ldap distinguished name", "ldap:dir:cn=alice,ou=eng,dc=example,dc=com," + unique},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := mustCreate(t, s, savedView(tc.ownerID, "view"), testMaxPerKind)
			if rec.OwnerID != tc.ownerID {
				t.Fatalf("OwnerID round-trip = %q; want %q", rec.OwnerID, tc.ownerID)
			}
			got, err := s.Get(ctx, tc.ownerID, rec.ID)
			if err != nil {
				t.Fatalf("Get with provider-qualified owner: %v", err)
			}
			if got.OwnerID != tc.ownerID {
				t.Errorf("Get().OwnerID = %q; want %q", got.OwnerID, tc.ownerID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Optimistic concurrency (D7)
// ---------------------------------------------------------------------------

func TestPreferenceStore_UpdateBumpsRevision(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	rec := mustCreate(t, s, savedView(owner, "view"), testMaxPerKind)
	newCfg := json.RawMessage(`{"schemaVersion":1,"resourceKind":"pods","sortKey":"age","sortDir":"desc"}`)

	updated, err := s.Update(ctx, owner, rec.ID, rec.Revision, "renamed", "renamed", 1, newCfg)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Revision != rec.Revision+1 {
		t.Errorf("Revision = %d; want %d", updated.Revision, rec.Revision+1)
	}
	if updated.Name != "renamed" {
		t.Errorf("Name = %q; want %q", updated.Name, "renamed")
	}
	if !updated.UpdatedAt.After(rec.UpdatedAt) {
		t.Errorf("UpdatedAt not advanced: %v -> %v", rec.UpdatedAt, updated.UpdatedAt)
	}
	if !updated.CreatedAt.Equal(rec.CreatedAt) {
		t.Errorf("CreatedAt changed: %v -> %v", rec.CreatedAt, updated.CreatedAt)
	}

	var cfg map[string]any
	if err := json.Unmarshal(updated.Config, &cfg); err != nil {
		t.Fatalf("updated config is not valid JSON: %v", err)
	}
	if cfg["sortKey"] != "age" {
		t.Errorf("config.sortKey = %v; want age", cfg["sortKey"])
	}

	// A second update with the new revision must succeed and bump again.
	again, err := s.Update(ctx, owner, rec.ID, updated.Revision, "renamed", "renamed", 1, newCfg)
	if err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if again.Revision != updated.Revision+1 {
		t.Errorf("second Revision = %d; want %d", again.Revision, updated.Revision+1)
	}
}

func TestPreferenceStore_StaleRevisionConflicts(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	rec := mustCreate(t, s, savedView(owner, "view"), testMaxPerKind)
	if _, err := s.Update(ctx, owner, rec.ID, rec.Revision, "first", "first", 1, rec.Config); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	// Second writer still holds revision 1.
	_, err := s.Update(ctx, owner, rec.ID, rec.Revision, "second", "second", 1, rec.Config)
	if !errors.Is(err, ErrPreferenceConflict) {
		t.Fatalf("stale Update = %v; want ErrPreferenceConflict", err)
	}

	got, err := s.Get(ctx, owner, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "first" {
		t.Errorf("Name = %q; the losing writer overwrote the winner", got.Name)
	}
	if got.Revision != 2 {
		t.Errorf("Revision = %d; want 2 (the conflicting write must not bump)", got.Revision)
	}
}

func TestPreferenceStore_UpdateMissingID_ReturnsNotFound(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	_, err := s.Update(t.Context(), owner, uuid.New(), 1, "x", "x", 1, json.RawMessage(`{}`))
	if !errors.Is(err, ErrPreferenceNotFound) {
		t.Errorf("Update(unknown id) = %v; want ErrPreferenceNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Dedup identity (D3) and limits (D6)
// ---------------------------------------------------------------------------

func TestPreferenceStore_DuplicateDedupKeyRejected(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	mustCreate(t, s, savedView(owner, "Prod pods"), testMaxPerKind)

	// Same dedup key (case-folded name), different display name.
	dup := savedView(owner, "PROD PODS")
	if _, err := s.Create(t.Context(), dup, testMaxPerKind); !errors.Is(err, ErrPreferenceDuplicate) {
		t.Errorf("Create(duplicate dedup key) = %v; want ErrPreferenceDuplicate", err)
	}
}

func TestPreferenceStore_UpdateToExistingDedupKeyRejected(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	first := mustCreate(t, s, savedView(owner, "one"), testMaxPerKind)
	second := mustCreate(t, s, savedView(owner, "two"), testMaxPerKind)

	_, err := s.Update(t.Context(), owner, second.ID, second.Revision,
		first.Name, first.DedupKey, 1, second.Config)
	if !errors.Is(err, ErrPreferenceDuplicate) {
		t.Errorf("Update onto an existing dedup key = %v; want ErrPreferenceDuplicate", err)
	}
}

func TestPreferenceStore_SameDedupKeyDifferentCluster_Allowed(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	mustCreate(t, s, savedView(owner, "shared name"), testMaxPerKind)

	remote := savedView(owner, "shared name")
	remote.ClusterID = "prod-east"
	if _, err := s.Create(t.Context(), remote, testMaxPerKind); err != nil {
		t.Fatalf("Create with the same dedup key on another cluster: %v", err)
	}

	views, err := s.List(t.Context(), owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("List returned %d records; want 2 (List spans clusters)", len(views))
	}
}

// TestPreferenceStore_PinIdentityIgnoresUID proves a recreated object with a
// new uid collides with the existing pin instead of silently duplicating it
// (R1/R6, Correction 7).
func TestPreferenceStore_PinIdentityIgnoresUID(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	mustCreate(t, s, pinRecord(owner, "deployments", "prod", "api", "uid-original"), testMaxPerKind)

	recreated := pinRecord(owner, "deployments", "prod", "api", "uid-recreated")
	if _, err := s.Create(t.Context(), recreated, testMaxPerKind); !errors.Is(err, ErrPreferenceDuplicate) {
		t.Fatalf("Create(recreated pin) = %v; want ErrPreferenceDuplicate", err)
	}

	pins, err := s.List(t.Context(), owner, PreferenceKindPin)
	if err != nil {
		t.Fatalf("List(pin): %v", err)
	}
	if len(pins) != 1 {
		t.Errorf("owner holds %d pins; want 1 — uid must not create a second identity", len(pins))
	}

	// A different namespace is a different pin.
	if _, err := s.Create(t.Context(), pinRecord(owner, "deployments", "staging", "api", "uid-2"), testMaxPerKind); err != nil {
		t.Fatalf("Create(same name, other namespace): %v", err)
	}
}

func TestPreferenceStore_LimitReached(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	const limit = 3
	for i := range limit {
		mustCreate(t, s, savedView(owner, fmt.Sprintf("view-%d", i)), limit)
	}

	if _, err := s.Create(ctx, savedView(owner, "one too many"), limit); !errors.Is(err, ErrPreferenceLimit) {
		t.Fatalf("Create beyond the limit = %v; want ErrPreferenceLimit", err)
	}

	// The limit is per kind: pins are unaffected by the saved-view ceiling.
	if _, err := s.Create(ctx, pinRecord(owner, "pods", "prod", "web", "uid-1"), limit); err != nil {
		t.Fatalf("pin Create under a saturated saved-view limit: %v", err)
	}

	// Deleting frees a slot.
	views, err := s.List(ctx, owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := s.Delete(ctx, owner, views[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Create(ctx, savedView(owner, "now it fits"), limit); err != nil {
		t.Fatalf("Create after freeing a slot: %v", err)
	}
}

// TestPreferenceStore_ConcurrentCreateRespectsLimit proves the ceiling holds
// when several creates race, which is the property the sequential limit test
// cannot see.
//
// Every racer uses a distinct name, so each one has its own dedup key and the
// unique index cannot mask the result: without serialization each racer's count
// subquery reads a snapshot taken before any sibling committed, they all pass
// the "< limit" test, and the owner ends up over the ceiling.
func TestPreferenceStore_ConcurrentCreateRespectsLimit(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	const (
		limit  = 1
		racers = 8
		rounds = 25 // the unserialized window is short; one round rarely catches it
	)

	for round := range rounds {
		owner := fmt.Sprintf("%s-round-%d", owner, round)

		var (
			wg      sync.WaitGroup
			start   = make(chan struct{})
			results = make([]error, racers)
		)
		for i := range racers {
			wg.Go(func() {
				<-start // release all racers together
				_, results[i] = s.Create(ctx, savedView(owner, fmt.Sprintf("racer-%d", i)), limit)
			})
		}
		close(start)
		wg.Wait()

		var created int
		for i, err := range results {
			switch {
			case err == nil:
				created++
			case errors.Is(err, ErrPreferenceLimit):
				// Expected for every racer that lost.
			default:
				t.Fatalf("round %d, racer %d: unexpected error: %v", round, i, err)
			}
		}
		if created != limit {
			t.Fatalf("round %d: %d racers were told their Create succeeded; want %d",
				round, created, limit)
		}

		n, err := s.CountByKind(ctx, owner, PreferenceKindSavedView)
		if err != nil {
			t.Fatalf("CountByKind: %v", err)
		}
		if n != limit {
			t.Fatalf("round %d: owner holds %d saved views after the race; the ceiling was %d",
				round, n, limit)
		}
	}
}

// TestPreferenceStore_LimitIsOwnerScoped proves the conditional INSERT counts
// only the caller's rows — a busy neighbour must not exhaust another user's
// quota.
func TestPreferenceStore_LimitIsOwnerScoped(t *testing.T) {
	s := newPreferenceStore(t)
	alice, bob := testOwnerID(t), testOwnerID(t)

	const limit = 2
	for i := range limit {
		mustCreate(t, s, savedView(alice, fmt.Sprintf("view-%d", i)), limit)
	}
	if _, err := s.Create(t.Context(), savedView(bob, "view-0"), limit); err != nil {
		t.Fatalf("bob's first Create under alice's saturated quota: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Rejected input and cancellation
// ---------------------------------------------------------------------------

// TestPreferenceStore_OversizedConfigRejected exercises the DDL's
// pg_column_size(config) <= 8192 check — the database's own backstop behind
// the handler-layer allowlist.
func TestPreferenceStore_OversizedConfigRejected(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	rec := savedView(owner, "huge")
	rec.Config = json.RawMessage(`{"schemaVersion":1,"resourceKind":"pods","search":"` +
		strings.Repeat("x", 16<<10) + `"}`)

	if _, err := s.Create(t.Context(), rec, testMaxPerKind); err == nil {
		t.Fatal("Create with an oversized config succeeded; want the DDL size check to reject it")
	}

	views, err := s.List(t.Context(), owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("owner holds %d records after a rejected Create; want 0", len(views))
	}
}

// TestPreferenceStore_UpdateOversizedConfigRejected covers Update's own
// configOrEmpty call site and error switch. Create's path is exercised by
// TestPreferenceStore_OversizedConfigRejected; Update reaches the same DDL
// ceiling through different code, where an oversized payload lands in the
// default branch rather than a sentinel.
func TestPreferenceStore_UpdateOversizedConfigRejected(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	rec := mustCreate(t, s, savedView(owner, "view"), testMaxPerKind)
	huge := json.RawMessage(`{"schemaVersion":1,"resourceKind":"pods","search":"` +
		strings.Repeat("x", 16<<10) + `"}`)

	if _, err := s.Update(ctx, owner, rec.ID, rec.Revision,
		rec.Name, rec.DedupKey, 1, huge); err == nil {
		t.Fatal("Update with an oversized config succeeded; want the DDL size check to reject it")
	}

	// The rejected write must leave the record exactly as it was — in
	// particular it must not consume the revision the caller still holds.
	got, err := s.Get(ctx, owner, rec.ID)
	if err != nil {
		t.Fatalf("Get after rejected Update: %v", err)
	}
	if got.Revision != rec.Revision {
		t.Errorf("Revision = %d after a rejected Update; want %d", got.Revision, rec.Revision)
	}
	if string(got.Config) != string(rec.Config) {
		t.Error("config changed despite the Update being rejected")
	}
}

// TestPreferenceStore_EmptyConfigNormalizedToEmptyObject covers configOrEmpty
// on both call sites: the column is NOT NULL, so a caller that supplies no
// config must still produce valid JSONB rather than a constraint violation.
func TestPreferenceStore_EmptyConfigNormalizedToEmptyObject(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	unmarshals := func(t *testing.T, label string, raw json.RawMessage) {
		t.Helper()
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("%s: stored config %q is not valid JSON: %v", label, raw, err)
		}
		if len(cfg) != 0 {
			t.Errorf("%s: stored config = %v; want an empty object", label, cfg)
		}
	}

	nilConfig := savedView(owner, "nil config")
	nilConfig.Config = nil
	created := mustCreate(t, s, nilConfig, testMaxPerKind)
	unmarshals(t, "Create with nil config", created.Config)

	updated, err := s.Update(ctx, owner, created.ID, created.Revision,
		created.Name, created.DedupKey, 1, json.RawMessage{})
	if err != nil {
		t.Fatalf("Update with an empty config: %v", err)
	}
	unmarshals(t, "Update with empty config", updated.Config)
}

// TestPreferenceStore_UpdateCannotChangeOwnerOrKind pins the contract that
// Update rewrites only mutable fields. Neither owner_id nor kind is in the
// signature or the SET list today; this test fails if a future edit adds
// either. A record whose kind could change would let its owner move it
// between quota buckets, and one whose owner could change would be a
// cross-tenant write.
func TestPreferenceStore_UpdateCannotChangeOwnerOrKind(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)
	ctx := t.Context()

	view := mustCreate(t, s, savedView(owner, "view"), testMaxPerKind)
	mustCreate(t, s, pinRecord(owner, "deployments", "prod", "api", "uid-1"), testMaxPerKind)

	updated, err := s.Update(ctx, owner, view.ID, view.Revision,
		"renamed", "renamed", 1, view.Config)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Kind != PreferenceKindSavedView {
		t.Errorf("Kind = %q after Update; want %q", updated.Kind, PreferenceKindSavedView)
	}
	if updated.OwnerID != owner {
		t.Errorf("OwnerID = %q after Update; want %q", updated.OwnerID, owner)
	}

	// Neither quota bucket moved.
	for kind, want := range map[PreferenceKind]int{
		PreferenceKindSavedView: 1,
		PreferenceKindPin:       1,
	} {
		n, err := s.CountByKind(ctx, owner, kind)
		if err != nil {
			t.Fatalf("CountByKind(%s): %v", kind, err)
		}
		if n != want {
			t.Errorf("CountByKind(%s) = %d after Update; want %d", kind, n, want)
		}
	}
}

func TestPreferenceStore_UnknownKindRejected(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	rec := savedView(owner, "bogus")
	// Deliberately a value no migration will ever add to the CHECK. This read
	// "dashboard_layout" until 000019 made that a real kind and turned this
	// test red -- which is the CHECK proving it is enforced, but it also means
	// the sentinel has to be something the allowlist cannot grow into.
	rec.Kind = PreferenceKind("not_a_preference_kind")

	if _, err := s.Create(t.Context(), rec, testMaxPerKind); err == nil {
		t.Fatal("Create with an unknown kind succeeded; want the DDL kind check to reject it")
	}
}

func TestPreferenceStore_ContextCancelled(t *testing.T) {
	s := newPreferenceStore(t)
	owner := testOwnerID(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.Create(ctx, savedView(owner, "cancelled"), testMaxPerKind); !errors.Is(err, context.Canceled) {
		t.Errorf("Create with a cancelled context = %v; want context.Canceled", err)
	}
	if _, err := s.List(ctx, owner, PreferenceKindSavedView); !errors.Is(err, context.Canceled) {
		t.Errorf("List with a cancelled context = %v; want context.Canceled", err)
	}

	// Nothing was written — verified on a live context.
	views, err := s.List(t.Context(), owner, PreferenceKindSavedView)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("cancelled Create wrote %d records; want 0", len(views))
	}
}

// ---------------------------------------------------------------------------
// Migration 000018 round trip
// ---------------------------------------------------------------------------

// TestPreferenceMigration_UpDownUp proves 000018 applies to a database that
// already carries 000001–000017 data, that its down migration removes only
// the objects it introduced, and that it re-applies cleanly afterwards.
//
// It deliberately does NOT use testDB(t): the shared harness database is
// migrated once per process and other tests in this binary hold live pools
// against it, so rolling it back to 000017 would delete the schema out from
// under them. Instead this test creates its own throwaway database on the
// same server, migrates it in isolation, and drops it in cleanup.
func TestPreferenceMigration_UpDownUp(t *testing.T) {
	baseURL := migrationTestBaseURL(t)
	ctx := t.Context()

	adminConn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connecting to the test server: %v", err)
	}
	// Registered as a cleanup rather than a defer: deferred calls run when
	// the test function returns, which is BEFORE t.Cleanup functions. A
	// deferred close would shut this connection down while the DROP
	// DATABASE cleanup below still needs it. Cleanups run last-in-first-out,
	// so registering the close first makes it run last.
	t.Cleanup(func() { adminConn.Close(context.Background()) })

	dbName := "kc_migtest_" + randomSuffix(t)
	quoted := pgx.Identifier{dbName}.Sanitize()
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		if testDatabaseRequired(os.LookupEnv) {
			t.Fatalf("creating throwaway database %s: %v", dbName, err)
		}
		t.Skipf("cannot CREATE DATABASE on this server (%v); skipping the migration round-trip", err)
	}
	t.Cleanup(func() {
		// FORCE (PostgreSQL 13+) evicts any connection the migrator left
		// behind so cleanup cannot hang on a lingering session.
		if _, err := adminConn.Exec(context.Background(),
			"DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping throwaway database %s: %v", dbName, err)
		}
	})

	scratchURL := withDatabase(t, baseURL, dbName)

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("creating migration source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, scratchURL)
	if err != nil {
		t.Fatalf("creating migrator: %v", err)
	}
	defer m.Close()

	// 1. Bring the scratch database up to the migration immediately before
	//    this one, then seed a row in an unrelated table.
	if err := m.Migrate(17); err != nil {
		t.Fatalf("migrating to 000017: %v", err)
	}

	pool, err := pgxpool.New(ctx, scratchURL)
	if err != nil {
		t.Fatalf("opening scratch pool: %v", err)
	}
	defer pool.Close()

	seedUser := testOwnerID(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_logs (cluster_id, "user", source_ip, action, result) VALUES ($1, $2, $3, $4, $5)`,
		"local", seedUser, "127.0.0.1", "test.seed", "success"); err != nil {
		t.Fatalf("seeding audit_logs before 000018: %v", err)
	}
	if tableExists(t, pool, "user_preferences") {
		t.Fatal("user_preferences exists at version 000017; the migration sequence is wrong")
	}

	// 2. Apply 000018 over populated data.
	if err := m.Migrate(18); err != nil {
		t.Fatalf("migrating 000017 -> 000018 over populated data: %v", err)
	}
	if !tableExists(t, pool, "user_preferences") {
		t.Fatal("user_preferences missing after 000018")
	}
	for _, idx := range []string{"idx_user_preferences_dedup", "idx_user_preferences_owner_kind"} {
		if !indexExists(t, pool, idx) {
			t.Errorf("index %s missing after 000018", idx)
		}
	}

	owner := testOwnerID(t)
	store := NewPreferenceStore(pool)
	if _, err := store.Create(ctx, savedView(owner, "survives"), testMaxPerKind); err != nil {
		t.Fatalf("writing through the freshly migrated schema: %v", err)
	}

	// 3. Roll 000018 back. Only its own objects may disappear.
	if err := m.Migrate(17); err != nil {
		t.Fatalf("rolling back 000018: %v", err)
	}
	if tableExists(t, pool, "user_preferences") {
		t.Error("user_preferences still exists after rollback")
	}
	for _, idx := range []string{"idx_user_preferences_dedup", "idx_user_preferences_owner_kind"} {
		if indexExists(t, pool, idx) {
			t.Errorf("index %s still exists after rollback", idx)
		}
	}

	var seeded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_logs WHERE "user" = $1`, seedUser).Scan(&seeded); err != nil {
		t.Fatalf("counting seeded audit_logs after rollback: %v", err)
	}
	if seeded != 1 {
		t.Errorf("seeded audit_logs row count = %d after rollback; want 1 — the down migration touched an unrelated table", seeded)
	}

	// 4. Re-apply. The table comes back empty, proving the pair is idempotent
	//    in both directions.
	if err := m.Migrate(18); err != nil {
		t.Fatalf("re-applying 000018: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_preferences`).Scan(&remaining); err != nil {
		t.Fatalf("counting user_preferences after re-apply: %v", err)
	}
	if remaining != 0 {
		t.Errorf("user_preferences holds %d rows after down+up; want 0", remaining)
	}
}

// TestMigration_DashboardLayoutKind_RoundTrip proves the CHECK actually
// widened, and that the rollback refuses to destroy layouts silently.
//
// Like the test above it works on its own throwaway database rather than the
// shared harness one, because it deliberately drives a migration failure and
// leaves the version table dirty in the middle.
func TestMigration_DashboardLayoutKind_RoundTrip(t *testing.T) {
	baseURL := migrationTestBaseURL(t)
	ctx := t.Context()

	adminConn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connecting to the test server: %v", err)
	}
	t.Cleanup(func() { adminConn.Close(context.Background()) })

	dbName := "kc_migtest_" + randomSuffix(t)
	quoted := pgx.Identifier{dbName}.Sanitize()
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		if testDatabaseRequired(os.LookupEnv) {
			t.Fatalf("creating throwaway database %s: %v", dbName, err)
		}
		t.Skipf("cannot CREATE DATABASE on this server (%v); skipping the migration round-trip", err)
	}
	t.Cleanup(func() {
		if _, err := adminConn.Exec(context.Background(),
			"DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping throwaway database %s: %v", dbName, err)
		}
	})

	scratchURL := withDatabase(t, baseURL, dbName)

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("creating migration source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, scratchURL)
	if err != nil {
		t.Fatalf("creating migrator: %v", err)
	}
	defer m.Close()

	if err := m.Migrate(18); err != nil {
		t.Fatalf("migrating to 000018: %v", err)
	}

	pool, err := pgxpool.New(ctx, scratchURL)
	if err != nil {
		t.Fatalf("opening scratch pool: %v", err)
	}
	defer pool.Close()

	owner := testOwnerID(t)
	// The kind is bound from the Go constant, not written into the SQL, so the
	// constant is the thing under test. With the literal inline, a typo in
	// PreferenceKindDashboardLayout would ship green: nothing else in the tree
	// reads it yet.
	layoutKind := string(PreferenceKindDashboardLayout)
	insertLayout := func() error {
		_, err := pool.Exec(ctx,
			`INSERT INTO user_preferences (owner_id, kind, name, dedup_key, config)
			 VALUES ($1, $2, 'Overview', 'overview', $3::jsonb)`,
			owner, layoutKind, `{"schemaVersion":1,"items":[]}`)
		return err
	}

	// A survivor from an existing kind. Every assertion about the rollback
	// below means nothing unless something that is not a layout is present to
	// be destroyed by a careless one.
	survivor := testOwnerID(t)
	store := NewPreferenceStore(pool)
	if _, err := store.Create(ctx, savedView(survivor, "survives"), testMaxPerKind); err != nil {
		t.Fatalf("seeding a saved view at 000018: %v", err)
	}

	// 1. At 000018 the kind is not allowed yet. Without this the test would
	//    pass against a database where the CHECK had never been narrow.
	if err := insertLayout(); err == nil {
		t.Fatal("inserted a dashboard_layout row at 000018; the CHECK was already wide")
	}

	// 2. Widen it.
	if err := m.Migrate(19); err != nil {
		t.Fatalf("migrating 000018 -> 000019: %v", err)
	}
	if err := insertLayout(); err != nil {
		t.Fatalf("inserting a dashboard_layout row after 000019: %v", err)
	}

	// 3. The rollback must refuse while a layout exists.
	if err := m.Migrate(18); err == nil {
		t.Fatal("rolled back 000019 while a dashboard_layout row existed; the down migration would have to drop the row silently to succeed")
	}
	// The failed down leaves golang-migrate's version row dirty. The schema
	// itself should be untouched -- PostgreSQL runs the file's statements in
	// one implicit transaction, so the DROP CONSTRAINT rolls back with the ADD
	// that failed -- and forcing the version back to 19 is only honest if that
	// holds. So check it here, before forcing anything.
	//
	// Nothing later in this test can stand in for this. The down migration
	// opens with DROP CONSTRAINT IF EXISTS, which is idempotent, so a genuinely
	// half-applied rollback would leave the table with no kind CHECK at all and
	// every assertion below would still pass: the second rollback would
	// succeed, the rejected insert would be rejected by the narrow CHECK it
	// then adds, and the re-apply would widen it again.
	var constraintDef string
	if err := pool.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint
		  WHERE conrelid = 'user_preferences'::regclass
		    AND conname = 'user_preferences_kind_check'`).Scan(&constraintDef); err != nil {
		t.Fatalf("reading the kind constraint after the refused rollback: %v", err)
	}
	if !strings.Contains(constraintDef, layoutKind) {
		t.Fatalf("kind constraint after the refused rollback = %q; want it to still allow %q -- the failed down migration was not atomic, so the schema is half-applied and forcing the version would hide it",
			constraintDef, layoutKind)
	}

	if err := m.Force(19); err != nil {
		t.Fatalf("clearing the dirty version after the expected failure: %v", err)
	}

	var layouts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_preferences WHERE kind = 'dashboard_layout'`).Scan(&layouts); err != nil {
		t.Fatalf("counting layouts after the refused rollback: %v", err)
	}
	if layouts != 1 {
		t.Fatalf("dashboard_layout row count = %d after the refused rollback; want 1", layouts)
	}

	// 4. Remove the layouts, as NOTES.txt tells the operator to, and the same
	//    rollback goes through.
	if _, err := pool.Exec(ctx,
		`DELETE FROM user_preferences WHERE kind = 'dashboard_layout'`); err != nil {
		t.Fatalf("deleting layouts before the rollback: %v", err)
	}
	if err := m.Migrate(18); err != nil {
		t.Fatalf("rolling back 000019 with no layouts present: %v", err)
	}
	if err := insertLayout(); err == nil {
		t.Error("dashboard_layout still accepted after the rollback; the CHECK did not narrow")
	}

	// 5. Re-apply. The unrelated row was never in scope for any of this.
	if err := m.Migrate(19); err != nil {
		t.Fatalf("re-applying 000019: %v", err)
	}
	if err := insertLayout(); err != nil {
		t.Errorf("inserting a dashboard_layout row after re-applying 000019: %v", err)
	}
	var survived int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_preferences WHERE owner_id = $1 AND kind = 'saved_view'`,
		survivor).Scan(&survived); err != nil {
		t.Fatalf("counting the seeded saved view: %v", err)
	}
	if survived != 1 {
		t.Errorf("seeded saved_view count = %d after down+up; want 1 -- the pair touched another kind", survived)
	}
}

// migrationTestBaseURL returns the configured test database URL, applying the
// same gating contract as testDB: skip without it, fail loudly when a real
// database was demanded.
func migrationTestBaseURL(t *testing.T) string {
	t.Helper()

	connString := testDatabaseURL(os.LookupEnv)
	if connString == "" {
		if testDatabaseRequired(os.LookupEnv) {
			t.Fatalf("%s is set but %s is empty; the migration round-trip was required to run",
				testDatabaseRequiredEnv, testDatabaseURLEnv)
		}
		t.Skipf("%s is not set; skipping the migration round-trip", testDatabaseURLEnv)
	}
	return connString
}

// withDatabase rewrites a PostgreSQL URL to point at another database on the
// same server, preserving credentials and query parameters.
func withDatabase(t *testing.T, connString, dbName string) string {
	t.Helper()

	u, err := url.Parse(connString)
	if err != nil {
		t.Fatalf("parsing %s: %v", testDatabaseURLEnv, err)
	}
	u.Path = "/" + dbName
	return u.String()
}

// randomSuffix returns 8 hex characters for building collision-free object
// names.
func randomSuffix(t *testing.T) string {
	t.Helper()

	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generating random suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()

	var exists bool
	if err := pool.QueryRow(t.Context(),
		`SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
		t.Fatalf("checking for table %s: %v", name, err)
	}
	return exists
}

func indexExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()

	var exists bool
	if err := pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`,
		name).Scan(&exists); err != nil {
		t.Fatalf("checking for index %s: %v", name, err)
	}
	return exists
}
