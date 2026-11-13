package store

// rubrik_test.go - Rubrik connection, mapping and compliance-state
// persistence, and the 000031 migration.
//
// Env-gated (testDB / migrationScratchDB, skipped without
// KUBECENTER_TEST_DATABASE_URL). Every test uses its own connection id and
// cluster ids from testOwnerID(t), never the production "default" row, per
// the isolation contract in testdb_test.go.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const rubrikTestKey = "rubrik-test-master-secret"

var rubrikTables = []string{
	"rubrik_connection", "rubrik_cluster_mappings", "rubrik_operations", "rubrik_poller_state",
	"rubrik_compliance_states", "rubrik_poller_events", "rubrik_poller_deliveries", "rubrik_poller_lease",
}

// insertRubrikNotification writes one notification with the given source.
func insertRubrikNotification(ctx context.Context, pool *pgxpool.Pool, source string) error {
	_, err := pool.Exec(ctx, `INSERT INTO nc_notifications (source, severity, title)
		VALUES ($1, 'warning', 'Rubrik snapshot failed')`, source)
	return err
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCheckViolation
}

func TestMigration000031_RoundTrip(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(30); err != nil {
		t.Fatalf("migrating to 000030: %v", err)
	}
	if err := insertRubrikNotification(ctx, pool, "rubrik"); !isCheckViolation(err) {
		t.Fatalf("rubrik notification at 30 = %v; want a CHECK violation", err)
	}

	if err := m.Migrate(31); err != nil {
		t.Fatalf("migrating to 000031: %v", err)
	}
	for _, table := range rubrikTables {
		if !tableExists(t, pool, table) {
			t.Errorf("%s missing after the up migration", table)
		}
	}
	if err := insertRubrikNotification(ctx, pool, "rubrik"); err != nil {
		t.Fatalf("rubrik notification at 31: %v", err)
	}
	if err := insertRubrikNotification(ctx, pool, "external_secrets"); err != nil {
		t.Errorf("an existing source at 31: %v", err)
	}
	if err := insertRubrikNotification(ctx, pool, "not_a_source"); !isCheckViolation(err) {
		t.Errorf("unknown source at 31 = %v; want a CHECK violation", err)
	}

	// A rule that targets rubrik and velero keeps velero after rollback.
	var channelID string
	if err := pool.QueryRow(ctx, `INSERT INTO nc_channels (name, type, config, created_by)
		VALUES ('rubrik-rollback', 'webhook', '\x7b7d', 'test') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("writing channel: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nc_rules (name, source_filter, channel_id, created_by)
		VALUES ('r', ARRAY['rubrik','velero'], $1, 'test')`, channelID); err != nil {
		t.Fatalf("writing rule: %v", err)
	}
	// A rule that targets only rubrik must not become a match-all rule.
	var onlyChannelID string
	if err := pool.QueryRow(ctx, `INSERT INTO nc_channels (name, type, config, created_by)
		VALUES ('rubrik-only-rollback', 'webhook', '\x7b7d', 'test') RETURNING id`).Scan(&onlyChannelID); err != nil {
		t.Fatalf("writing channel: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nc_rules (name, source_filter, channel_id, created_by)
		VALUES ('only', ARRAY['rubrik'], $1, 'test')`, onlyChannelID); err != nil {
		t.Fatalf("writing rubrik-only rule: %v", err)
	}

	// The down file refuses while rubrik notifications exist (NOTES.txt);
	// the operator deletes them first.
	if _, err := pool.Exec(ctx, `DELETE FROM nc_notifications WHERE source = 'rubrik'`); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(30); err != nil {
		t.Fatalf("rolling back 000031: %v", err)
	}
	for _, table := range rubrikTables {
		if tableExists(t, pool, table) {
			t.Errorf("%s survived the down migration", table)
		}
	}
	if err := insertRubrikNotification(ctx, pool, "rubrik"); !isCheckViolation(err) {
		t.Errorf("rubrik notification after rollback = %v; want a CHECK violation", err)
	}
	var filter []string
	if err := pool.QueryRow(ctx, `SELECT source_filter FROM nc_rules WHERE channel_id = $1`, channelID).Scan(&filter); err != nil {
		t.Fatal(err)
	}
	if len(filter) != 1 || filter[0] != "velero" {
		t.Errorf("rule source_filter after rollback = %v; want [velero]", filter)
	}
	var onlyEnabled bool
	if err := pool.QueryRow(ctx, `SELECT enabled FROM nc_rules WHERE channel_id = $1`, onlyChannelID).Scan(&onlyEnabled); err != nil {
		t.Fatal(err)
	}
	if onlyEnabled {
		t.Error("rubrik-only rule is still enabled after rollback; its empty filter would match every source")
	}

	if err := m.Migrate(31); err != nil {
		t.Fatalf("re-applying 000031: %v", err)
	}
	if err := insertRubrikNotification(ctx, pool, "rubrik"); err != nil {
		t.Errorf("rubrik notification after re-apply: %v", err)
	}
}

// Running the up file against a database already at 31 must not fail.
func TestMigration000031_UpIsIdempotent(t *testing.T) {
	m, pool := migrationScratchDB(t)
	if err := m.Migrate(31); err != nil {
		t.Fatalf("migrating to 000031: %v", err)
	}
	up, err := os.ReadFile("migrations/000031_create_rubrik.up.sql")
	if err != nil {
		t.Fatalf("reading the up file: %v", err)
	}
	if _, err := pool.Exec(t.Context(), string(up)); err != nil {
		t.Errorf("re-running 000031 up on a database at 31: %v", err)
	}
}

// newRubrikTestConnection saves a connection with a secret under a
// test-unique id.
func newRubrikTestConnection(t *testing.T, s *RubrikStore, url string) (string, RubrikConnection) {
	t.Helper()
	id := testOwnerID(t)
	c, err := s.UpsertConnection(t.Context(), id, RubrikConnectionWrite{
		TenantURL: url, ClientID: "client-1", ClientSecret: new("s3cret"), UpdatedBy: "admin",
	})
	if err != nil {
		t.Fatalf("saving test connection: %v", err)
	}
	return id, c
}

// mapRubrikTestCluster registers a cluster and maps it under a connection.
func mapRubrikTestCluster(t *testing.T, s *RubrikStore, pool *pgxpool.Pool, connID, rscID string) string {
	t.Helper()
	clusterID := testOwnerID(t)
	insertMetricsTestCluster(t.Context(), t, pool, clusterID)
	rev, err := s.ConnectionRevision(t.Context(), connID)
	if err != nil {
		t.Fatalf("reading connection revision: %v", err)
	}
	if _, err := s.SetMapping(t.Context(), RubrikMappingWrite{
		ClusterID: clusterID, ConnectionID: connID, ConnectionRevision: rev, RSCClusterID: rscID,
		RSCClusterName: "rsc-" + rscID, ClusterGeneration: "2026-11-12T10:00:00.123456789Z", UpdatedBy: "admin",
	}); err != nil {
		t.Fatalf("mapping test cluster: %v", err)
	}
	return clusterID
}

// insertRubrikPollerRows writes one row into each per-cluster poller table.
func insertRubrikPollerRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, clusterID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO rubrik_poller_state (cluster_id, connection_failure_streak)
		VALUES ($1, 1)`, clusterID); err != nil {
		t.Fatalf("writing poller state: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO rubrik_compliance_states (cluster_id, rsc_object_id, namespace, status)
		VALUES ($1, 'obj-1', 'team-a', 'out_of_compliance')`, clusterID); err != nil {
		t.Fatalf("writing compliance state: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH ev AS (
			INSERT INTO rubrik_poller_events (id, cluster_id, kind, rsc_object_id, namespace, dedup_key, occurred_at)
			VALUES (gen_random_uuid(), $1, 'snapshot_failed', 'obj-1', 'team-a', 'series-1', now())
			RETURNING id)
		INSERT INTO rubrik_poller_deliveries (id, event_id, state)
		SELECT gen_random_uuid(), id, 'pending' FROM ev`, clusterID); err != nil {
		t.Fatalf("writing event and delivery: %v", err)
	}
}

// countRubrikPollerRows counts a cluster's rows across the poller tables.
func countRubrikPollerRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, clusterID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM rubrik_poller_state WHERE cluster_id = $1)
		     + (SELECT count(*) FROM rubrik_compliance_states WHERE cluster_id = $1)
		     + (SELECT count(*) FROM rubrik_poller_events WHERE cluster_id = $1)
		     + (SELECT count(*) FROM rubrik_poller_deliveries d
		          JOIN rubrik_poller_events e ON e.id = d.event_id WHERE e.cluster_id = $1)`,
		clusterID).Scan(&n); err != nil {
		t.Fatalf("counting poller rows: %v", err)
	}
	return n
}

func TestRubrikStore_ConnectionRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)

	id, c := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	if !c.HasSecret || c.Revision != 1 || c.TenantURL != "https://tenant.my.rubrik.com" || c.ClientID != "client-1" {
		t.Errorf("first save = %+v; want a stored secret at revision 1", c)
	}

	var raw []byte
	var origin string
	if err := pool.QueryRow(ctx, `SELECT client_secret, secret_origin FROM rubrik_connection WHERE id = $1`, id).
		Scan(&raw, &origin); err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || string(raw) == "s3cret" {
		t.Fatal("client secret is not encrypted at rest")
	}
	if origin != "https://tenant.my.rubrik.com" {
		t.Errorf("secret_origin = %q; want the tenant origin", origin)
	}

	got, creds, err := s.GetConnection(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasSecret || creds.ClientSecret != "s3cret" || got.Revision != 1 {
		t.Errorf("GetConnection = (%+v, secret set %v); want the connection and its secret", got, creds.ClientSecret != "")
	}
	if rev, err := s.ConnectionRevision(ctx, id); err != nil || rev != 1 {
		t.Errorf("ConnectionRevision = (%d, %v); want 1", rev, err)
	}

	if _, _, err := s.GetConnection(ctx, testOwnerID(t)); !errors.Is(err, ErrRubrikConnectionNotFound) {
		t.Errorf("GetConnection of a missing id = %v; want ErrRubrikConnectionNotFound", err)
	}
	if _, err := s.ConnectionRevision(ctx, testOwnerID(t)); !errors.Is(err, ErrRubrikConnectionNotFound) {
		t.Errorf("ConnectionRevision of a missing id = %v; want ErrRubrikConnectionNotFound", err)
	}
}

func TestRubrikStore_SameOriginKeepsSecret(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	id, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	clusterID := mapRubrikTestCluster(t, s, pool, id, "rsc-a")
	insertRubrikPollerRows(ctx, t, pool, clusterID)
	before := countRubrikPollerRows(ctx, t, pool, clusterID)

	c, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "https://tenant.my.rubrik.com/api", ClientID: "client-2",
		KeptFrom: "https://tenant.my.rubrik.com", UpdatedBy: "admin2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasSecret || c.Revision != 2 || c.ClientID != "client-2" || c.UpdatedBy != "admin2" {
		t.Errorf("same-origin save = %+v; want the secret kept at revision 2", c)
	}
	if _, creds, err := s.GetConnection(ctx, id); err != nil || creds.ClientSecret != "s3cret" {
		t.Errorf("secret after same-origin save changed (err %v)", err)
	}

	// Rotating the secret on the same origin keeps mappings too.
	if _, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "https://tenant.my.rubrik.com/api", ClientID: "client-2", ClientSecret: new("rotated"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMapping(ctx, clusterID); err != nil {
		t.Errorf("mapping after same-origin saves = %v; want it kept", err)
	}
	if n := countRubrikPollerRows(ctx, t, pool, clusterID); n != before {
		t.Errorf("poller rows after same-origin saves = %d; want %d", n, before)
	}
}

func TestRubrikStore_SchemeOrPortChangeIsANewOrigin(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	id, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")

	for _, url := range []string{"http://tenant.my.rubrik.com", "https://tenant.my.rubrik.com:8443"} {
		if _, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
			TenantURL: url, ClientID: "client-1", KeptFrom: "https://tenant.my.rubrik.com",
		}); !errors.Is(err, ErrRubrikSecretRequired) {
			t.Errorf("keeping the secret for %s = %v; want ErrRubrikSecretRequired", url, err)
		}
	}
}

func TestRubrikStore_SecretWithMismatchedOriginIsNotReturned(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	id, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")

	// A hand edit that moves the URL without the store's origin rule.
	if _, err := pool.Exec(ctx, `UPDATE rubrik_connection SET tenant_url = 'https://evil.example.com' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	c, creds, err := s.GetConnection(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.HasSecret || creds.ClientSecret != "" {
		t.Errorf("GetConnection after a hand-edited URL = (HasSecret %v, secret set %v); want no usable secret",
			c.HasSecret, creds.ClientSecret != "")
	}
}

func TestRubrikStore_SecretRequired(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	id, before := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")

	// A new origin without a secret is refused and changes nothing.
	_, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "https://other.my.rubrik.com", ClientID: "client-1",
		KeptFrom: "https://tenant.my.rubrik.com", UpdatedBy: "admin",
	})
	if !errors.Is(err, ErrRubrikSecretRequired) {
		t.Fatalf("new origin without a secret = %v; want ErrRubrikSecretRequired", err)
	}
	after, creds, err := s.GetConnection(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenantURL != before.TenantURL || after.Revision != before.Revision || creds.ClientSecret != "s3cret" {
		t.Errorf("connection after a refused save = %+v; want it unchanged", after)
	}

	// No connection yet and no secret, or an empty secret, is refused too.
	if _, err := s.UpsertConnection(ctx, testOwnerID(t), RubrikConnectionWrite{
		TenantURL: "https://tenant.my.rubrik.com", ClientID: "c",
	}); !errors.Is(err, ErrRubrikSecretRequired) {
		t.Errorf("first save without a secret = %v; want ErrRubrikSecretRequired", err)
	}
	if _, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "https://tenant.my.rubrik.com", ClientID: "c", ClientSecret: new(""),
	}); !errors.Is(err, ErrRubrikSecretRequired) {
		t.Errorf("empty secret = %v; want ErrRubrikSecretRequired", err)
	}
	if _, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "not a url", ClientID: "c", ClientSecret: new("x"),
	}); !errors.Is(err, ErrRubrikInvalidTenantURL) {
		t.Errorf("URL without an origin = %v; want ErrRubrikInvalidTenantURL", err)
	}
}

func TestRubrikStore_KeptSecretGuardsConcurrentSave(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	id, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com/a")

	// The caller read /a, but another save already moved it to /b.
	if _, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "https://tenant.my.rubrik.com/b", ClientID: "client-1",
		KeptFrom: "https://tenant.my.rubrik.com/a", ClientSecret: new("s3cret"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "https://tenant.my.rubrik.com/c", ClientID: "client-1",
		KeptFrom: "https://tenant.my.rubrik.com/a",
	}); !errors.Is(err, ErrRubrikConnectionChanged) {
		t.Errorf("stale KeptFrom = %v; want ErrRubrikConnectionChanged", err)
	}
}

func TestRubrikStore_NewOriginClearsMappingsAndPollerState(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	id, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	clusterID := mapRubrikTestCluster(t, s, pool, id, "rsc-a")
	insertRubrikPollerRows(ctx, t, pool, clusterID)

	// Another connection's mapping must survive.
	otherID, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	otherCluster := mapRubrikTestCluster(t, s, pool, otherID, "rsc-a")

	c, err := s.UpsertConnection(ctx, id, RubrikConnectionWrite{
		TenantURL: "https://other.my.rubrik.com", ClientID: "client-1",
		ClientSecret: new("new-secret"), UpdatedBy: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasSecret || c.Revision != 2 {
		t.Errorf("new-origin save = %+v; want a usable secret at revision 2", c)
	}
	if _, err := s.GetMapping(ctx, clusterID); !errors.Is(err, ErrRubrikMappingNotFound) {
		t.Errorf("mapping after an origin change = %v; want ErrRubrikMappingNotFound", err)
	}
	if n := countRubrikPollerRows(ctx, t, pool, clusterID); n != 0 {
		t.Errorf("%d poller rows survived an origin change; want 0", n)
	}
	if _, err := s.GetMapping(ctx, otherCluster); err != nil {
		t.Errorf("another connection's mapping = %v; want it kept", err)
	}
	if _, creds, err := s.GetConnection(ctx, id); err != nil || creds.ClientSecret != "new-secret" {
		t.Errorf("secret after an origin change was not replaced (err %v)", err)
	}
}

func TestRubrikStore_MappingLifecycle(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	connID, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	clusterID := mapRubrikTestCluster(t, s, pool, connID, "rsc-a")

	m, err := s.GetMapping(ctx, clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if m.ConnectionID != connID || m.RSCClusterID != "rsc-a" || m.ClusterGeneration != "2026-11-12T10:00:00.123456789Z" ||
		m.CreatedBy != "admin" {
		t.Errorf("GetMapping = %+v; want the saved mapping", m)
	}
	list, err := s.ListMappings(ctx, connID)
	if err != nil || len(list) != 1 || list[0].ClusterID != clusterID {
		t.Errorf("ListMappings = (%+v, %v); want the one mapping", list, err)
	}

	// The same RSC cluster cannot be mapped to a second k8sCenter cluster.
	second := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, second)
	if _, err := s.SetMapping(ctx, RubrikMappingWrite{
		ClusterID: second, ConnectionID: connID, ConnectionRevision: 1, RSCClusterID: "rsc-a", ClusterGeneration: "local",
	}); !errors.Is(err, ErrRubrikMappingConflict) {
		t.Errorf("second cluster on the same RSC cluster = %v; want ErrRubrikMappingConflict", err)
	}

	// Unknown cluster and unknown connection are told apart.
	if _, err := s.SetMapping(ctx, RubrikMappingWrite{
		ClusterID: testOwnerID(t), ConnectionID: connID, ConnectionRevision: 1, RSCClusterID: "rsc-z", ClusterGeneration: "local",
	}); !errors.Is(err, ErrRubrikClusterUnknown) {
		t.Errorf("unregistered cluster = %v; want ErrRubrikClusterUnknown", err)
	}
	if _, err := s.SetMapping(ctx, RubrikMappingWrite{
		ClusterID: second, ConnectionID: testOwnerID(t), ConnectionRevision: 1, RSCClusterID: "rsc-z", ClusterGeneration: "local",
	}); !errors.Is(err, ErrRubrikConnectionNotFound) {
		t.Errorf("unknown connection = %v; want ErrRubrikConnectionNotFound", err)
	}

	if err := s.DeleteMapping(ctx, clusterID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMapping(ctx, clusterID); !errors.Is(err, ErrRubrikMappingNotFound) {
		t.Errorf("second DeleteMapping = %v; want ErrRubrikMappingNotFound", err)
	}
}

func TestRubrikStore_RemapDropsPollerStateOnlyWhenTargetChanges(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	connID, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	clusterID := mapRubrikTestCluster(t, s, pool, connID, "rsc-a")
	insertRubrikPollerRows(ctx, t, pool, clusterID)
	before := countRubrikPollerRows(ctx, t, pool, clusterID)

	// Saving the same target again keeps poller state and created_by.
	m, err := s.SetMapping(ctx, RubrikMappingWrite{
		ClusterID: clusterID, ConnectionID: connID, ConnectionRevision: 1, RSCClusterID: "rsc-a", RSCClusterName: "renamed",
		ClusterGeneration: "2026-11-12T10:00:00.123456789Z", UpdatedBy: "admin2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.RSCClusterName != "renamed" || m.CreatedBy != "admin" || m.UpdatedBy != "admin2" {
		t.Errorf("same-target save = %+v; want the name and updater changed, the creator kept", m)
	}
	if n := countRubrikPollerRows(ctx, t, pool, clusterID); n != before {
		t.Errorf("poller rows after a same-target save = %d; want %d", n, before)
	}

	// A new registration generation replaces the mapping and its state.
	if _, err := s.SetMapping(ctx, RubrikMappingWrite{
		ClusterID: clusterID, ConnectionID: connID, ConnectionRevision: 1, RSCClusterID: "rsc-a",
		ClusterGeneration: "2026-11-13T09:00:00Z", UpdatedBy: "admin3",
	}); err != nil {
		t.Fatal(err)
	}
	if n := countRubrikPollerRows(ctx, t, pool, clusterID); n != 0 {
		t.Errorf("poller rows after a generation change = %d; want 0", n)
	}
	if m, err := s.GetMapping(ctx, clusterID); err != nil || m.ClusterGeneration != "2026-11-13T09:00:00Z" || m.CreatedBy != "admin3" {
		t.Errorf("mapping after a generation change = (%+v, %v); want the new generation", m, err)
	}
}

func TestRubrikStore_MappingRefusedAfterConnectionSaved(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	connID, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	clusterID := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, clusterID)

	// The caller chose rsc-a at revision 1; the tenant moved before it saved.
	if _, err := s.UpsertConnection(ctx, connID, RubrikConnectionWrite{
		TenantURL: "https://other.my.rubrik.com", ClientID: "client-1", ClientSecret: new("new-secret"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMapping(ctx, RubrikMappingWrite{
		ClusterID: clusterID, ConnectionID: connID, ConnectionRevision: 1, RSCClusterID: "rsc-a", ClusterGeneration: "local",
	}); !errors.Is(err, ErrRubrikConnectionChanged) {
		t.Errorf("mapping prepared at an older revision = %v; want ErrRubrikConnectionChanged", err)
	}
	if _, err := s.GetMapping(ctx, clusterID); !errors.Is(err, ErrRubrikMappingNotFound) {
		t.Errorf("mapping after a refused save = %v; want none written", err)
	}
}

func TestRubrikStore_ClusterDeleteCascadesMapping(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	connID, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	clusterID := mapRubrikTestCluster(t, s, pool, connID, "rsc-a")
	insertRubrikPollerRows(ctx, t, pool, clusterID)

	if _, err := pool.Exec(ctx, `DELETE FROM clusters WHERE id = $1`, clusterID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMapping(ctx, clusterID); !errors.Is(err, ErrRubrikMappingNotFound) {
		t.Errorf("mapping after cluster delete = %v; want ErrRubrikMappingNotFound", err)
	}
	if n := countRubrikPollerRows(ctx, t, pool, clusterID); n != 0 {
		t.Errorf("poller rows after cluster delete = %d; want 0", n)
	}
}

func TestRubrikStore_ListComplianceStates(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	connID, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	clusterID := mapRubrikTestCluster(t, s, pool, connID, "rsc-a")

	if got, err := s.ListComplianceStates(ctx, clusterID); err != nil || len(got) != 0 {
		t.Errorf("before the poller runs = (%+v, %v); want no rows (unknown)", got, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO rubrik_compliance_states (cluster_id, rsc_object_id, namespace, status, late_streak)
		VALUES ($1, 'obj-b', 'team-b', 'late', 1), ($1, 'obj-a', 'team-a', 'out_of_compliance', 2)`, clusterID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListComplianceStates(ctx, clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RSCObjectID != "obj-a" || got[0].Status != RubrikComplianceOut || got[0].LateStreak != 2 ||
		got[1].Namespace != "team-b" || got[1].Status != RubrikComplianceLate {
		t.Errorf("ListComplianceStates = %+v; want obj-a out of compliance then obj-b late", got)
	}
}
