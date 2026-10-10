package store

// cluster_metrics_test.go - metrics binding persistence (#608 PR 4a).
//
// Env-gated (testDB / migrationScratchDB, skipped without
// KUBECENTER_TEST_DATABASE_URL). Rows are keyed by a unique-per-test cluster id
// from testOwnerID(t), per the isolation contract in testdb_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const metricsTestKey = "metrics-test-master-secret"

// insertMetricsTestCluster registers a minimal cluster row the FK needs.
func insertMetricsTestCluster(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO clusters (id, name, api_server_url, auth_type, auth_data)
		VALUES ($1, $1, 'https://example.invalid', 'token', '\x7b7d')`, id); err != nil {
		t.Fatalf("inserting test cluster: %v", err)
	}
}

func TestMigration000027_RoundTripKeepsBindings(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(27); err != nil {
		t.Fatalf("migrating to 000027: %v", err)
	}
	insertMetricsTestCluster(ctx, t, pool, "c1")
	s := NewClusterMetricsStore(pool, metricsTestKey)
	tok := "secret"
	if _, err := s.Upsert(ctx, "c1", "https://prom.example.com", "", &tok, ""); err != nil {
		t.Fatalf("upsert at 27: %v", err)
	}

	if err := m.Migrate(26); err != nil {
		t.Fatalf("rolling back 000027: %v", err)
	}
	hasCol := func(name string) bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'cluster_monitoring' AND column_name = $1)`, name).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for _, c := range []string{"prometheus_token", "alertmanager_url", "updated_at"} {
		if hasCol(c) {
			t.Errorf("cluster_monitoring.%s survived the down migration", c)
		}
	}
	var gotURL string
	if err := pool.QueryRow(ctx, `SELECT prometheus_url FROM cluster_monitoring WHERE cluster_id = 'c1'`).Scan(&gotURL); err != nil {
		t.Fatalf("binding row after rollback: %v", err)
	}
	if gotURL != "https://prom.example.com" {
		t.Errorf("prometheus_url after rollback = %q; want it kept", gotURL)
	}

	if err := m.Migrate(27); err != nil {
		t.Fatalf("re-applying 000027: %v", err)
	}
	b, _, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("get after re-apply: %v", err)
	}
	if b.PrometheusURL != "https://prom.example.com" || b.HasToken {
		t.Errorf("binding after re-apply = %+v; want the URL kept and the token gone", b)
	}
}

func TestClusterMetricsStore_UpsertKeepsTokenOnNil(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	tok := "first"
	if _, err := s.Upsert(ctx, id, "https://a.example.com", "", &tok, ""); err != nil {
		t.Fatal(err)
	}
	b, err := s.Upsert(ctx, id, "https://b.example.com", "https://am.example.com", nil, "https://a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !b.HasToken || b.PrometheusURL != "https://b.example.com" || b.AlertmanagerURL != "https://am.example.com" {
		t.Errorf("binding = %+v; want the token kept and the URLs updated", b)
	}
	if _, got, err := s.Get(ctx, id); err != nil || got != "first" {
		t.Errorf("Get token = (%q, %v); want first", got, err)
	}
}

// A keep-token write whose keptFrom no longer matches the stored URL (another
// save moved the binding in between) is refused and leaves the row as the
// other save wrote it, so a URL is never paired with another host's token.
func TestClusterMetricsStore_UpsertKeepRefusedWhenURLMoved(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	tok := "b-token"
	if _, err := s.Upsert(ctx, id, "https://b.example.com", "", &tok, ""); err != nil {
		t.Fatal(err)
	}
	// The caller checked the binding while it still pointed at a.example.com.
	if _, err := s.Upsert(ctx, id, "https://a.example.com", "", nil, "https://a.example.com"); !errors.Is(err, ErrMetricsBindingChanged) {
		t.Fatalf("Upsert with a stale keptFrom = %v; want ErrMetricsBindingChanged", err)
	}
	// The caller saw no binding, but one was created meanwhile.
	if _, err := s.Upsert(ctx, id, "https://a.example.com", "", nil, ""); !errors.Is(err, ErrMetricsBindingChanged) {
		t.Fatalf("Upsert with keptFrom empty over a new binding = %v; want ErrMetricsBindingChanged", err)
	}
	b, got, err := s.Get(ctx, id)
	if err != nil || b.PrometheusURL != "https://b.example.com" || got != "b-token" {
		t.Errorf("Get = (%+v, %q, %v); want b.example.com with b-token untouched", b, got, err)
	}
	// A supplied token ignores keptFrom.
	other := "a-token"
	if _, err := s.Upsert(ctx, id, "https://a.example.com", "", &other, "stale"); err != nil {
		t.Errorf("Upsert with a token and a stale keptFrom = %v; want success", err)
	}
}

func TestClusterMetricsStore_ClearsTokenOnEmpty(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	tok := "first"
	if _, err := s.Upsert(ctx, id, "https://a.example.com", "", &tok, ""); err != nil {
		t.Fatal(err)
	}
	empty := ""
	b, err := s.Upsert(ctx, id, "https://a.example.com", "", &empty, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.HasToken {
		t.Error("HasToken = true after clearing with an empty token")
	}
	if _, got, err := s.Get(ctx, id); err != nil || got != "" {
		t.Errorf("Get token = (%q, %v); want empty", got, err)
	}
}

func TestClusterMetricsStore_GetDecryptsToken(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	tok := "bearer-value"
	if _, err := s.Upsert(ctx, id, "https://a.example.com", "", &tok, ""); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT prometheus_token FROM cluster_monitoring WHERE cluster_id = $1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || string(raw) == tok {
		t.Fatal("token is not encrypted at rest")
	}
	b, got, err := s.Get(ctx, id)
	if err != nil || got != tok || !b.HasToken || b.ClusterID != id || b.UpdatedAt.IsZero() {
		t.Errorf("Get = (%+v, %q, %v); want the decrypted token", b, got, err)
	}
}

func TestClusterMetricsStore_GetAfterDeleteNotFound(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	if _, err := s.Upsert(ctx, id, "https://a.example.com", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("Get after delete = %v; want ErrMetricsBindingNotFound", err)
	}
	if err := s.Delete(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("second Delete = %v; want ErrMetricsBindingNotFound", err)
	}
}

func TestClusterMetricsStore_LegacyRowWithoutURLIsNotFound(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	if _, err := pool.Exec(ctx, `INSERT INTO cluster_monitoring (cluster_id, grafana_url) VALUES ($1, 'https://g.example.com')`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("Get on a grafana-only row = %v; want ErrMetricsBindingNotFound", err)
	}
}

// An unregistered cluster is not "metrics not configured": Get answers a
// wrapped pgx.ErrNoRows, which callers classify as cluster_unknown. A
// registered cluster without a binding row stays ErrMetricsBindingNotFound.
func TestClusterMetricsStore_GetUnregisteredClusterIsErrNoRows(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewClusterMetricsStore(pool, metricsTestKey)

	_, _, err := s.Get(ctx, testOwnerID(t)+"-unregistered")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Get on an unregistered cluster = %v; want a wrapped pgx.ErrNoRows", err)
	}
	if errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("Get on an unregistered cluster = %v; must not read as ErrMetricsBindingNotFound", err)
	}

	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	if _, _, err := s.Get(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) || errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Get on a registered cluster without a binding = %v; want ErrMetricsBindingNotFound only", err)
	}
}
