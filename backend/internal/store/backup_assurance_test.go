package store

// backup_assurance_test.go — coverage for BackupAssuranceStore and migration
// 000022 (Release F, U32).
//
// Store tests use the shared harness database (testDB) and isolate every row
// by a unique cluster id from testOwnerID(t): every uniqueness constraint on
// the assurance tables leads with cluster_id (or hangs off a row that does),
// so residue from a failed run is invisible to other suites.
//
// The two migration tests roll 000022 back, which would pull the schema out
// from under every other test in the binary, so they run against a throwaway
// database built by migrationScratchDB (preferences_test.go).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func newAssuranceStore(t *testing.T) (*BackupAssuranceStore, *pgxpool.Pool) {
	t.Helper()
	pool := testDB(t)
	return NewBackupAssuranceStore(pool), pool
}

func schedulePolicy(clusterID, namespace, name string) BackupAssurancePolicy {
	return BackupAssurancePolicy{
		ID:             uuid.New(),
		ClusterID:      clusterID,
		ScopeKind:      ScopeSchedule,
		ScopeNamespace: namespace,
		ScopeName:      name,
		MaxAge:         24 * time.Hour,
		Grace:          time.Hour,
		TreatPartialAs: AssuranceTreatPartialAsFailure,
		AlertOnPaused:  true,
		Enabled:        true,
		CreatedBy:      "local:admin",
	}
}

func mustInsertPolicy(t *testing.T, s *BackupAssuranceStore, p BackupAssurancePolicy) BackupAssurancePolicy {
	t.Helper()
	if err := s.InsertPolicy(t.Context(), p); err != nil {
		t.Fatalf("InsertPolicy(%s/%s/%s): %v", p.ScopeKind, p.ScopeNamespace, p.ScopeName, err)
	}
	return p
}

// rawException describes an exception row written straight to the table.
// U32 ships no transition API (that is U32b), so tests seed rows directly.
type rawException struct {
	clusterID  string
	policyID   uuid.UUID
	namespace  string
	name       string
	uid        string
	condition  AssuranceCondition
	state      string
	openedAt   time.Time
	resolvedAt *time.Time
}

func insertRawException(t *testing.T, pool *pgxpool.Pool, e rawException) (uuid.UUID, error) {
	t.Helper()
	id := uuid.New()
	kind := ScopeCluster
	switch {
	case e.name != "":
		kind = ScopeSchedule
	case e.namespace != "":
		kind = ScopeNamespace
	}
	if e.condition == "" {
		e.condition = ConditionOverdue
	}
	if e.state == "" {
		e.state = AssuranceStateOpen
	}
	if e.openedAt.IsZero() {
		e.openedAt = time.Now()
	}
	_, err := pool.Exec(t.Context(), `
		INSERT INTO backup_assurance_exceptions (
			id, cluster_id, policy_id, subject_kind, subject_namespace, subject_name,
			subject_uid, condition, state, severity, opened_at, last_observed_at, resolved_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'warning', $10, $10, $11)`,
		id, e.clusterID, e.policyID, string(kind), e.namespace, e.name,
		e.uid, string(e.condition), e.state, e.openedAt, e.resolvedAt)
	return id, err
}

func mustInsertRawException(t *testing.T, pool *pgxpool.Pool, e rawException) uuid.UUID {
	t.Helper()
	id, err := insertRawException(t, pool, e)
	if err != nil {
		t.Fatalf("seeding exception: %v", err)
	}
	return id
}

func exceptionExists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM backup_assurance_exceptions WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("probing exception %s: %v", id, err)
	}
	return exists
}

func ptrTime(t time.Time) *time.Time { return &t }

// ---------------------------------------------------------------------------
// Hermetic tests — no database required.
// ---------------------------------------------------------------------------

// TestBackupAssuranceStore_NilPoolIsNotConstructed pins the unavailable-DB
// contract: without a pool there is no store, so a caller that forgets to
// nil-check fails at wiring time in review rather than with a nil-pointer
// panic on the collector's first tick. main.go must only call
// NewBackupAssuranceStore with a live pool and must gate assurance on the
// result (U34c).
func TestBackupAssuranceStore_NilPoolIsNotConstructed(t *testing.T) {
	if s := NewBackupAssuranceStore(nil); s != nil {
		t.Fatalf("NewBackupAssuranceStore(nil) = %#v; want nil so callers must nil-check", s)
	}
}

func TestAssuranceEnums_Valid(t *testing.T) {
	// Every value the migration's CHECKs admit must be Valid, and nothing
	// else. The lists are spelled out rather than derived from the consts so
	// a const typo cannot make both sides agree.
	for _, c := range []string{"overdue", "failed", "partially_failed", "paused",
		"never_run", "location_unavailable", "collection_unknown"} {
		if !AssuranceCondition(c).Valid() {
			t.Errorf("AssuranceCondition(%q).Valid() = false; the 000022 CHECK admits it", c)
		}
	}
	for _, c := range []string{"", "Overdue", "stale", "open"} {
		if AssuranceCondition(c).Valid() {
			t.Errorf("AssuranceCondition(%q).Valid() = true; want false", c)
		}
	}
	for _, k := range []string{"schedule", "namespace", "cluster"} {
		if !AssuranceScopeKind(k).Valid() {
			t.Errorf("AssuranceScopeKind(%q).Valid() = false; the 000022 CHECK admits it", k)
		}
	}
	for _, k := range []string{"", "Schedule", "backup", "pod"} {
		if AssuranceScopeKind(k).Valid() {
			t.Errorf("AssuranceScopeKind(%q).Valid() = true; want false", k)
		}
	}
}

func TestValidatePolicy_RejectsInconsistentShapes(t *testing.T) {
	base := schedulePolicy("local", "velero", "daily")
	cases := []struct {
		name   string
		mutate func(*BackupAssurancePolicy)
	}{
		{"nil id", func(p *BackupAssurancePolicy) { p.ID = uuid.Nil }},
		{"empty cluster", func(p *BackupAssurancePolicy) { p.ClusterID = "" }},
		{"unknown kind", func(p *BackupAssurancePolicy) { p.ScopeKind = "backup" }},
		{"schedule without name", func(p *BackupAssurancePolicy) { p.ScopeName = "" }},
		{"schedule without namespace", func(p *BackupAssurancePolicy) { p.ScopeNamespace = "" }},
		{"namespace with name", func(p *BackupAssurancePolicy) { p.ScopeKind = ScopeNamespace }},
		{"cluster with namespace", func(p *BackupAssurancePolicy) {
			p.ScopeKind = ScopeCluster
			p.ScopeName = ""
		}},
		{"bad treatPartialAs", func(p *BackupAssurancePolicy) { p.TreatPartialAs = "ignore" }},
		{"fractional max age", func(p *BackupAssurancePolicy) { p.MaxAge = 10*time.Minute + time.Millisecond }},
		{"max age overflows INTEGER", func(p *BackupAssurancePolicy) { p.MaxAge = 1 << 31 * time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			if err := validatePolicy(p); !errors.Is(err, ErrAssurancePolicyInvalid) {
				t.Fatalf("validatePolicy = %v; want ErrAssurancePolicyInvalid", err)
			}
		})
	}
	valid := []BackupAssurancePolicy{base}
	ns := base
	ns.ScopeKind, ns.ScopeName = ScopeNamespace, ""
	cl := base
	cl.ScopeKind, cl.ScopeNamespace, cl.ScopeName = ScopeCluster, "", ""
	valid = append(valid, ns, cl)
	for _, p := range valid {
		if err := validatePolicy(p); err != nil {
			t.Errorf("validatePolicy(%s) = %v; want nil", p.ScopeKind, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Migration 000022
// ---------------------------------------------------------------------------

// assuranceTables are the four tables 000022 owns.
var assuranceTables = []string{
	"backup_assurance_policies",
	"backup_assurance_exceptions",
	"backup_assurance_deliveries",
	"backup_assurance_collector_lease",
}

// preexistingSnapshot renders every row of the tables 000022 must not touch
// as ordered JSON text, so "unchanged" is a byte comparison rather than a
// column-by-column argument.
func preexistingSnapshot(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	snap := map[string]string{}
	for _, table := range []string{"nc_notifications", "eso_sync_history", "clusters"} {
		var rows string
		if err := pool.QueryRow(t.Context(),
			`SELECT COALESCE(json_agg(t ORDER BY t::text)::text, '[]') FROM `+table+` t`).Scan(&rows); err != nil {
			t.Fatalf("snapshotting %s: %v", table, err)
		}
		snap[table] = rows
	}
	return snap
}

// seedPreexisting writes one row into each table 000022 must preserve.
func seedPreexisting(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	stmts := []string{
		`INSERT INTO nc_notifications (source, severity, title, message, resource_kind, resource_ns, resource_name, cluster_id, resource_uid)
		 VALUES ('velero', 'warning', 'Backup failed', 'msg', 'Backup', 'velero', 'daily-1', 'local', 'uid-1')`,
		`INSERT INTO eso_sync_history (cluster_id, uid, namespace, name, attempt_at, outcome)
		 VALUES ('local', 'es-uid', 'apps', 'db', '2026-09-01T00:00:00Z', 'success')`,
		`INSERT INTO clusters (id, name, api_server_url, auth_data, is_local)
		 VALUES ('remote-1', 'remote-1', 'https://10.0.0.1:6443', '\x00'::bytea, false)`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seeding pre-existing data: %v", err)
		}
	}
}

func TestBackupAssurance_MigrationUpDownPreservesExistingTables(t *testing.T) {
	m, pool := migrationScratchDB(t)

	if err := m.Migrate(21); err != nil {
		t.Fatalf("migrating to 000021: %v", err)
	}
	seedPreexisting(t, pool)
	before := preexistingSnapshot(t, pool)

	if err := m.Migrate(22); err != nil {
		t.Fatalf("migrating 000021 -> 000022: %v", err)
	}
	for _, table := range assuranceTables {
		if !tableExists(t, pool, table) {
			t.Errorf("table %s missing after 000022 up", table)
		}
	}
	for _, idx := range []string{
		"idx_backup_assurance_policies_scope",
		"idx_backup_assurance_exceptions_open_unique",
		"idx_backup_assurance_exceptions_read",
		"idx_backup_assurance_deliveries_once",
		"idx_backup_assurance_deliveries_pending",
	} {
		if !indexExists(t, pool, idx) {
			t.Errorf("index %s missing after 000022 up", idx)
		}
	}

	// Put state in the new tables so the down migration has something real
	// to drop, including rows that cascade.
	s := NewBackupAssuranceStore(pool)
	p := mustInsertPolicy(t, s, schedulePolicy("local", "velero", "daily"))
	mustInsertRawException(t, pool, rawException{clusterID: "local", policyID: p.ID, namespace: "velero", name: "daily", uid: "uid-a"})

	if err := m.Migrate(21); err != nil {
		t.Fatalf("rolling back 000022: %v", err)
	}
	for _, table := range assuranceTables {
		if tableExists(t, pool, table) {
			t.Errorf("table %s survived 000022 down", table)
		}
	}
	if after := preexistingSnapshot(t, pool); !mapsEqual(before, after) {
		t.Fatalf("pre-existing rows changed across 000022 up+down:\nbefore=%v\nafter=%v", before, after)
	}

	// Re-apply: a rollback must leave the database able to go forward again.
	if err := m.Migrate(22); err != nil {
		t.Fatalf("re-applying 000022: %v", err)
	}
	policies, err := s.ListPolicies(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListPolicies after re-apply: %v", err)
	}
	if len(policies) != 0 {
		t.Errorf("policies after down+up = %d; want 0 (the down migration discards assurance state)", len(policies))
	}
}

func TestBackupAssurance_MigrationAppliesToPopulatedDatabase(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()

	if err := m.Migrate(21); err != nil {
		t.Fatalf("migrating to 000021: %v", err)
	}
	seedPreexisting(t, pool)
	before := preexistingSnapshot(t, pool)

	if err := m.Migrate(22); err != nil {
		t.Fatalf("applying 000022 to a populated database: %v", err)
	}
	if after := preexistingSnapshot(t, pool); !mapsEqual(before, after) {
		t.Fatalf("000022 up changed pre-existing rows:\nbefore=%v\nafter=%v", before, after)
	}

	// O-2: install creates no default policy.
	var policyRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM backup_assurance_policies`).Scan(&policyRows); err != nil {
		t.Fatalf("counting policies: %v", err)
	}
	if policyRows != 0 {
		t.Errorf("000022 seeded %d policies; install must create none", policyRows)
	}

	// The new schema is usable alongside the old data.
	s := NewBackupAssuranceStore(pool)
	p := mustInsertPolicy(t, s, schedulePolicy("local", "velero", "daily"))
	got, err := s.GetPolicy(ctx, "local", p.ID)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if got.Revision != 1 || got.MaxAge != p.MaxAge || got.Grace != p.Grace || got.UpdatedAt != nil {
		t.Errorf("GetPolicy = %+v; want revision 1, the inserted thresholds and no updated_at", got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Policies
// ---------------------------------------------------------------------------

func TestPolicy_DuplicateScopeReturnsErrAssurancePolicyExists(t *testing.T) {
	s, _ := newAssuranceStore(t)
	cluster := testOwnerID(t)

	mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	dup := schedulePolicy(cluster, "velero", "daily") // fresh id, same scope
	if err := s.InsertPolicy(t.Context(), dup); !errors.Is(err, ErrAssurancePolicyExists) {
		t.Fatalf("second InsertPolicy for the same scope = %v; want ErrAssurancePolicyExists", err)
	}

	// A different scope kind over the same namespace is a different policy.
	ns := schedulePolicy(cluster, "velero", "")
	ns.ScopeKind = ScopeNamespace
	mustInsertPolicy(t, s, ns)
}

func TestPolicy_StaleRevisionConflicts(t *testing.T) {
	s, _ := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))

	// Two editors both loaded revision 1.
	first := p
	first.Revision = 1
	first.MaxAge = 12 * time.Hour
	first.UpdatedBy = "local:alice"
	if err := s.UpdatePolicy(ctx, first); err != nil {
		t.Fatalf("first UpdatePolicy: %v", err)
	}
	second := p
	second.Revision = 1
	second.MaxAge = 48 * time.Hour
	second.UpdatedBy = "local:bob"
	if err := s.UpdatePolicy(ctx, second); !errors.Is(err, ErrAssuranceRevisionConflict) {
		t.Fatalf("stale UpdatePolicy = %v; want ErrAssuranceRevisionConflict", err)
	}

	got, err := s.GetPolicy(ctx, cluster, p.ID)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if got.Revision != 2 || got.MaxAge != 12*time.Hour || got.UpdatedBy != "local:alice" || got.UpdatedAt == nil {
		t.Errorf("after one winning update got revision=%d maxAge=%s updatedBy=%q updatedAt=%v; want 2, 12h, local:alice, set",
			got.Revision, got.MaxAge, got.UpdatedBy, got.UpdatedAt)
	}

	// The scope is immutable: an update that names a different scope is
	// applied to the stored row's thresholds only.
	moved := *got
	moved.ScopeName = "weekly"
	if err := s.UpdatePolicy(ctx, moved); err != nil {
		t.Fatalf("UpdatePolicy at the current revision: %v", err)
	}
	if got, _ = s.GetPolicy(ctx, cluster, p.ID); got.ScopeName != "daily" {
		t.Errorf("UpdatePolicy rewrote scope_name to %q; scope must be immutable", got.ScopeName)
	}

	// A missing policy, or one in another cluster, is not-found rather than
	// a conflict, so the caller can tell "reload" from "gone".
	missing := schedulePolicy(cluster, "velero", "nope")
	missing.Revision = 1
	if err := s.UpdatePolicy(ctx, missing); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Errorf("UpdatePolicy of a missing policy = %v; want ErrAssurancePolicyNotFound", err)
	}
	other := *got
	other.ClusterID = testOwnerID(t)
	if err := s.UpdatePolicy(ctx, other); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Errorf("UpdatePolicy under another cluster id = %v; want ErrAssurancePolicyNotFound", err)
	}
}

func TestPolicy_RejectsMaxAgeBelowFloor(t *testing.T) {
	s, _ := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)

	below := schedulePolicy(cluster, "velero", "daily")
	below.MaxAge = AssuranceMinMaxAge - time.Second
	err := s.InsertPolicy(ctx, below)
	if !errors.Is(err, ErrAssurancePolicyInvalid) {
		t.Fatalf("InsertPolicy with max age %s = %v; want ErrAssurancePolicyInvalid from the CHECK", below.MaxAge, err)
	}
	if _, err := s.GetPolicy(ctx, cluster, below.ID); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Errorf("a refused insert left a row behind (GetPolicy = %v)", err)
	}

	negativeGrace := schedulePolicy(cluster, "velero", "daily")
	negativeGrace.Grace = -time.Second
	if err := s.InsertPolicy(ctx, negativeGrace); !errors.Is(err, ErrAssurancePolicyInvalid) {
		t.Errorf("InsertPolicy with negative grace = %v; want ErrAssurancePolicyInvalid", err)
	}

	atFloor := schedulePolicy(cluster, "velero", "daily")
	atFloor.MaxAge = AssuranceMinMaxAge
	mustInsertPolicy(t, s, atFloor)

	// The floor binds updates too.
	atFloor.Revision = 1
	atFloor.MaxAge = time.Minute
	if err := s.UpdatePolicy(ctx, atFloor); !errors.Is(err, ErrAssurancePolicyInvalid) {
		t.Errorf("UpdatePolicy below the floor = %v; want ErrAssurancePolicyInvalid", err)
	}
}

func TestPolicy_ScopedToClusterID(t *testing.T) {
	s, _ := newAssuranceStore(t)
	ctx := t.Context()
	clusterA, clusterB := testOwnerID(t), testOwnerID(t)

	a := mustInsertPolicy(t, s, schedulePolicy(clusterA, "velero", "daily"))
	b := mustInsertPolicy(t, s, schedulePolicy(clusterB, "velero", "daily"))

	for _, tc := range []struct {
		cluster string
		want    uuid.UUID
	}{{clusterA, a.ID}, {clusterB, b.ID}} {
		got, err := s.ListPolicies(ctx, tc.cluster)
		if err != nil {
			t.Fatalf("ListPolicies(%s): %v", tc.cluster, err)
		}
		if len(got) != 1 || got[0].ID != tc.want {
			t.Errorf("ListPolicies(%s) = %v; want only %s", tc.cluster, got, tc.want)
		}
	}

	// Reads and deletes through the wrong cluster id see nothing.
	if _, err := s.GetPolicy(ctx, clusterB, a.ID); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Errorf("GetPolicy across clusters = %v; want ErrAssurancePolicyNotFound", err)
	}
	if err := s.DeletePolicy(ctx, clusterB, a.ID); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Errorf("DeletePolicy across clusters = %v; want ErrAssurancePolicyNotFound", err)
	}
	if _, err := s.GetPolicy(ctx, clusterA, a.ID); err != nil {
		t.Errorf("cross-cluster delete removed the policy: %v", err)
	}

	empty, err := s.ListPolicies(ctx, testOwnerID(t))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("ListPolicies for a cluster with none = (%v, %v); want an empty non-nil slice", empty, err)
	}
}

// ---------------------------------------------------------------------------
// Exceptions
// ---------------------------------------------------------------------------

func TestException_ObserveIncrementsCountWithoutReopening(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	id := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: p.ID, namespace: "velero", name: "daily", uid: "uid-a"})

	observedAt := time.Now().Add(time.Minute).Truncate(time.Microsecond)
	lastSuccess := observedAt.Add(-30 * time.Hour)
	if err := s.ObserveException(ctx, id, observedAt.Add(-time.Second), "warning", nil, nil); err != nil {
		t.Fatalf("first ObserveException: %v", err)
	}
	if err := s.ObserveException(ctx, id, observedAt, "critical", &lastSuccess, []byte(`{"ageSeconds":108000}`)); err != nil {
		t.Fatalf("second ObserveException: %v", err)
	}

	open, err := s.ListOpenExceptions(ctx, cluster)
	if err != nil {
		t.Fatalf("ListOpenExceptions: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open exceptions = %d; want exactly 1 (observing must not open a second row)", len(open))
	}
	e := open[0]
	if e.ID != id || e.State != AssuranceStateOpen || e.ObservationCount != 3 {
		t.Errorf("after two observations got id=%s state=%s count=%d; want %s open 3", e.ID, e.State, e.ObservationCount, id)
	}
	if e.Severity != "critical" || !e.LastObservedAt.Equal(observedAt) ||
		e.LastSuccessAt == nil || !e.LastSuccessAt.Equal(lastSuccess) {
		t.Errorf("observation fields = severity %s lastObserved %v lastSuccess %v; want critical %v %v",
			e.Severity, e.LastObservedAt, e.LastSuccessAt, observedAt, lastSuccess)
	}
	var detail map[string]any
	if err := json.Unmarshal(e.Detail, &detail); err != nil || detail["ageSeconds"] != float64(108000) {
		t.Errorf("detail = %s (%v); want the last observation's detail", e.Detail, err)
	}

	// A resolved row is never resurrected by a late observation.
	if _, err := pool.Exec(ctx,
		`UPDATE backup_assurance_exceptions SET state = 'resolved', resolved_at = NOW() WHERE id = $1`, id); err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if err := s.ObserveException(ctx, id, time.Now(), "warning", nil, nil); !errors.Is(err, ErrAssuranceExceptionNotOpen) {
		t.Fatalf("ObserveException on a resolved row = %v; want ErrAssuranceExceptionNotOpen", err)
	}
	if err := s.ObserveException(ctx, uuid.New(), time.Now(), "warning", nil, nil); !errors.Is(err, ErrAssuranceExceptionNotOpen) {
		t.Errorf("ObserveException on a missing row = %v; want ErrAssuranceExceptionNotOpen", err)
	}
	var count int64
	var state string
	if err := pool.QueryRow(ctx,
		`SELECT observation_count, state FROM backup_assurance_exceptions WHERE id = $1`, id).Scan(&count, &state); err != nil {
		t.Fatalf("reading row: %v", err)
	}
	if count != 3 || state != AssuranceStateResolved {
		t.Errorf("resolved row after late observation: count=%d state=%s; want 3 resolved", count, state)
	}
}

func TestException_ListOpenExcludesResolved(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	identity := rawException{clusterID: cluster, policyID: p.ID, namespace: "velero", name: "daily", uid: "uid-a"}

	// An earlier occurrence, already resolved.
	resolved := identity
	resolved.state = AssuranceStateResolved
	resolved.openedAt = time.Now().Add(-48 * time.Hour)
	resolved.resolvedAt = ptrTime(time.Now().Add(-24 * time.Hour))
	resolvedID := mustInsertRawException(t, pool, resolved)

	// The partial index admits a new open row for the same identity...
	openID := mustInsertRawException(t, pool, identity)
	// ...but never a second open one.
	if _, err := insertRawException(t, pool, identity); !isUniqueViolation(err) {
		t.Fatalf("second open row for one identity = %v; want a unique violation from the partial index", err)
	}
	// A recreated schedule (new UID) is a different identity.
	recreated := identity
	recreated.uid = "uid-b"
	recreatedID := mustInsertRawException(t, pool, recreated)
	// Another cluster's identical identity is invisible here.
	other := identity
	other.clusterID = testOwnerID(t)
	other.policyID = mustInsertPolicy(t, s, schedulePolicy(other.clusterID, "velero", "daily")).ID
	mustInsertRawException(t, pool, other)

	open, err := s.ListOpenExceptions(ctx, cluster)
	if err != nil {
		t.Fatalf("ListOpenExceptions: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, e := range open {
		if e.State != AssuranceStateOpen {
			t.Errorf("ListOpenExceptions returned a %s row", e.State)
		}
		got[e.ID] = true
	}
	if len(open) != 2 || !got[openID] || !got[recreatedID] || got[resolvedID] {
		t.Errorf("ListOpenExceptions = %v; want exactly the two open rows of this cluster", got)
	}
}

func TestException_ListExceptionsFiltersAndPages(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	now := time.Now()

	// Newest first: apps (open), velero (resolved), cluster-scoped (open).
	appsID := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: p.ID, namespace: "apps", openedAt: now})
	veleroID := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: p.ID, namespace: "velero", name: "daily",
		state: AssuranceStateResolved, openedAt: now.Add(-time.Hour), resolvedAt: ptrTime(now)})
	clusterScopedID := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: p.ID, openedAt: now.Add(-2 * time.Hour)})

	ids := func(es []BackupAssuranceException) []uuid.UUID {
		out := make([]uuid.UUID, len(es))
		for i, e := range es {
			out[i] = e.ID
		}
		return out
	}
	cases := []struct {
		name      string
		q         AssuranceExceptionQuery
		want      []uuid.UUID
		wantTotal int
	}{
		{"all, newest first", AssuranceExceptionQuery{}, []uuid.UUID{appsID, veleroID, clusterScopedID}, 3},
		{"open only", AssuranceExceptionQuery{State: AssuranceStateOpen}, []uuid.UUID{appsID, clusterScopedID}, 2},
		{"resolved only", AssuranceExceptionQuery{State: AssuranceStateResolved}, []uuid.UUID{veleroID}, 1},
		{"restricted to apps", AssuranceExceptionQuery{RestrictNamespaces: true, Namespaces: []string{"apps"}}, []uuid.UUID{appsID}, 1},
		{"restricted with no namespaces sees nothing", AssuranceExceptionQuery{RestrictNamespaces: true}, []uuid.UUID{}, 0},
		{"page 2 of size 1", AssuranceExceptionQuery{Limit: 1, Offset: 1}, []uuid.UUID{veleroID}, 3},
		{"offset past the end keeps the total", AssuranceExceptionQuery{Offset: 10}, []uuid.UUID{}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := s.ListExceptions(ctx, cluster, tc.q)
			if err != nil {
				t.Fatalf("ListExceptions: %v", err)
			}
			gotIDs := ids(got)
			if total != tc.wantTotal || len(gotIDs) != len(tc.want) {
				t.Fatalf("ListExceptions = %v (total %d); want %v (total %d)", gotIDs, total, tc.want, tc.wantTotal)
			}
			for i := range tc.want {
				if gotIDs[i] != tc.want[i] {
					t.Fatalf("ListExceptions = %v; want %v in that order", gotIDs, tc.want)
				}
			}
		})
	}

	if _, _, err := s.ListExceptions(ctx, cluster, AssuranceExceptionQuery{State: "bogus"}); err == nil {
		t.Error("ListExceptions with an unknown state succeeded; want an error")
	}
}

func TestException_PruneResolvedRespectsRetention(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	now := time.Now()
	retention := BackupAssuranceExceptionRetention

	expired := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: p.ID, namespace: "a",
		state: AssuranceStateResolved, openedAt: now.Add(-retention - 48*time.Hour), resolvedAt: ptrTime(now.Add(-retention - time.Hour))})
	recent := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: p.ID, namespace: "b",
		state: AssuranceStateResolved, openedAt: now.Add(-retention - 48*time.Hour), resolvedAt: ptrTime(now.Add(-retention + time.Hour))})
	ancientOpen := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: p.ID, namespace: "c",
		openedAt: now.Add(-3 * retention)})

	n, err := s.PruneResolved(ctx, retention)
	if err != nil {
		t.Fatalf("PruneResolved: %v", err)
	}
	// The prune is global, so other suites' expired residue may be counted
	// too; this test's expired row guarantees at least one.
	if n < 1 {
		t.Errorf("PruneResolved removed %d rows; want at least the expired one", n)
	}
	if exceptionExists(t, pool, expired) {
		t.Error("resolved exception past retention survived the prune")
	}
	if !exceptionExists(t, pool, recent) {
		t.Error("resolved exception inside retention was pruned")
	}
	if !exceptionExists(t, pool, ancientOpen) {
		t.Error("open exception was pruned; open rows must never age out")
	}

	for _, bad := range []time.Duration{0, -time.Hour} {
		if _, err := s.PruneResolved(ctx, bad); err == nil {
			t.Errorf("PruneResolved(%s) succeeded; a non-positive retention must be refused", bad)
		}
	}
	if !exceptionExists(t, pool, recent) {
		t.Error("a refused prune still deleted rows")
	}
}

func TestException_CascadeOnPolicyDelete(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	doomed := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	kept := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "weekly"))

	doomedExc := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: doomed.ID, namespace: "velero", name: "daily"})
	keptExc := mustInsertRawException(t, pool, rawException{clusterID: cluster, policyID: kept.ID, namespace: "velero", name: "weekly"})
	deliveryID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO backup_assurance_deliveries (id, exception_id, transition, state) VALUES ($1, $2, 'opened', 'pending')`,
		deliveryID, doomedExc); err != nil {
		t.Fatalf("seeding delivery: %v", err)
	}

	if err := s.DeletePolicy(ctx, cluster, doomed.ID); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if exceptionExists(t, pool, doomedExc) {
		t.Error("exception survived its policy's deletion; want ON DELETE CASCADE")
	}
	var deliveries int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM backup_assurance_deliveries WHERE id = $1`, deliveryID).Scan(&deliveries); err != nil {
		t.Fatalf("counting deliveries: %v", err)
	}
	if deliveries != 0 {
		t.Error("delivery survived its exception's cascade delete")
	}
	if !exceptionExists(t, pool, keptExc) {
		t.Error("deleting one policy removed another policy's exception")
	}
	if err := s.DeletePolicy(ctx, cluster, doomed.ID); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Errorf("second DeletePolicy = %v; want ErrAssurancePolicyNotFound", err)
	}
}

func TestBackupAssuranceStore_WithTxRollsBackOnError(t *testing.T) {
	s, _ := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := schedulePolicy(cluster, "velero", "daily")
	sentinel := errors.New("abort")

	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO backup_assurance_policies (id, cluster_id, scope_kind, scope_namespace, scope_name, max_age_seconds, created_by)
			VALUES ($1, $2, 'schedule', 'velero', 'daily', 3600, 'test')`, p.ID, cluster); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx = %v; want fn's error returned unwrapped", err)
	}
	if _, err := s.GetPolicy(ctx, cluster, p.ID); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Fatalf("row written inside a failed WithTx is visible (GetPolicy = %v); want rollback", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.WithTx(cancelled, func(pgx.Tx) error { return nil }); err == nil {
		t.Error("WithTx on a cancelled context succeeded; want the begin error")
	}
}
