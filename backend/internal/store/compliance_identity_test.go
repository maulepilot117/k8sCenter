package store

// compliance_identity_test.go - compliance identity persistence (#608 final slice).
//
// Env-gated (testDB / migrationScratchDB, skipped without
// KUBECENTER_TEST_DATABASE_URL). Rows are keyed by a unique-per-test cluster id
// from testOwnerID(t), per the isolation contract in testdb_test.go.

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestComplianceIdentityStore_UpsertStoresAndBumpsRevisionOnChange(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewComplianceIdentityStore(pool)

	first, err := s.Upsert(ctx, id, "svc-compliance", []string{"auditors"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if first.Username != "svc-compliance" || !slices.Equal(first.Groups, []string{"auditors"}) ||
		first.UpdatedBy != "admin" || first.Revision <= 0 {
		t.Fatalf("stored = %+v; want the saved identity and a positive revision", first)
	}

	same, err := s.Upsert(ctx, id, "svc-compliance", []string{"auditors"}, "other-admin")
	if err != nil {
		t.Fatal(err)
	}
	if same.Revision != first.Revision {
		t.Errorf("identical save revision = %d; want it kept at %d", same.Revision, first.Revision)
	}
	if same.UpdatedBy != "other-admin" {
		t.Errorf("identical save updated_by = %q; want other-admin", same.UpdatedBy)
	}

	changedGroups, err := s.Upsert(ctx, id, "svc-compliance", []string{"auditors", "sre"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if changedGroups.Revision <= first.Revision {
		t.Errorf("changed-groups revision = %d; want above %d", changedGroups.Revision, first.Revision)
	}
	changedUser, err := s.Upsert(ctx, id, "other-user", []string{"auditors", "sre"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if changedUser.Revision <= changedGroups.Revision {
		t.Errorf("changed-username revision = %d; want above %d", changedUser.Revision, changedGroups.Revision)
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != changedUser.Revision || got.Username != "other-user" {
		t.Errorf("Get = %+v; want the latest save %+v", got, changedUser)
	}
}

func TestComplianceIdentityStore_NilGroupsStoredEmpty(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewComplianceIdentityStore(pool)

	got, err := s.Upsert(ctx, id, "svc", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 0 {
		t.Errorf("groups = %v; want none", got.Groups)
	}
}

// Clear then name the same identity again: the revision is higher than any
// before it, because the sequence is never rewound.
func TestComplianceIdentityStore_RevisionNeverReusedAfterClear(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewComplianceIdentityStore(pool)

	first, err := s.Upsert(ctx, id, "svc", []string{"g"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	again, err := s.Upsert(ctx, id, "svc", []string{"g"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if again.Revision <= first.Revision {
		t.Errorf("revision after clear and re-name = %d; want above %d", again.Revision, first.Revision)
	}
}

func TestComplianceIdentityStore_GetAndDeleteUnknown(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	s := NewComplianceIdentityStore(pool)

	if _, err := s.Get(ctx, id); !errors.Is(err, ErrComplianceIdentityNotFound) {
		t.Errorf("Get unknown = %v; want ErrComplianceIdentityNotFound", err)
	}
	if err := s.Delete(ctx, id); !errors.Is(err, ErrComplianceIdentityNotFound) {
		t.Errorf("Delete unknown = %v; want ErrComplianceIdentityNotFound", err)
	}
}

func TestComplianceIdentityStore_UpsertUnregisteredClusterFails(t *testing.T) {
	pool := testDB(t)
	s := NewComplianceIdentityStore(pool)
	if _, err := s.Upsert(t.Context(), testOwnerID(t), "svc", nil, ""); err == nil {
		t.Error("Upsert for an unregistered cluster succeeded; want the foreign key to refuse it")
	}
}

func TestComplianceIdentityStore_ListReturnsAllRows(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	a := testOwnerID(t) + "-a"
	b := testOwnerID(t) + "-b"
	insertMetricsTestCluster(ctx, t, pool, a)
	insertMetricsTestCluster(ctx, t, pool, b)
	s := NewComplianceIdentityStore(pool)
	if _, err := s.Upsert(ctx, a, "ua", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(ctx, b, "ub", []string{"g"}, ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, it := range list {
		seen[it.ClusterID] = it.Username
	}
	if seen[a] != "ua" || seen[b] != "ub" {
		t.Errorf("List = %v; want both %s and %s", seen, a, b)
	}
}

func TestComplianceIdentityStore_ClusterDeleteCascades(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewComplianceIdentityStore(pool)
	if _, err := s.Upsert(ctx, id, "svc", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM clusters WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, ErrComplianceIdentityNotFound) {
		t.Errorf("Get after the cluster was deleted = %v; want ErrComplianceIdentityNotFound", err)
	}
}

// Down drops only the new tables; existing snapshots survive down and re-apply.
func TestMigration000029_RoundTripKeepsSnapshots(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(29); err != nil {
		t.Fatalf("migrating to 000029: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO compliance_snapshots (snapshot_date, cluster_id, overall_score)
		VALUES (CURRENT_DATE, 'c1', 80)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(28); err != nil {
		t.Fatalf("rolling back 000029: %v", err)
	}
	for _, tbl := range []string{"cluster_compliance_identity", "compliance_snapshot_gaps"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tbl).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Errorf("%s survived the down migration", tbl)
		}
	}
	var score float64
	if err := pool.QueryRow(ctx, `SELECT overall_score FROM compliance_snapshots WHERE cluster_id = 'c1'`).Scan(&score); err != nil || score != 80 {
		t.Fatalf("snapshot after rollback = (%v, %v); want 80 kept", score, err)
	}
	if err := m.Migrate(29); err != nil {
		t.Fatalf("re-applying 000029: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT overall_score FROM compliance_snapshots WHERE cluster_id = 'c1'`).Scan(&score); err != nil || score != 80 {
		t.Fatalf("snapshot after re-apply = (%v, %v); want 80 kept", score, err)
	}
}

// Running the up file against a database already at 29 must not fail.
func TestMigration000029_UpIsIdempotent(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(29); err != nil {
		t.Fatalf("migrating to 000029: %v", err)
	}
	up, err := os.ReadFile("migrations/000029_compliance_identity_and_gaps.up.sql")
	if err != nil {
		t.Fatalf("reading the up file: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Errorf("re-running 000029 up on a database at 29: %v", err)
	}
}
