package store

// cluster_logs_test.go - Loki binding persistence (remote logs and shells
// U10, KTD8).
//
// Env-gated (testDB / migrationScratchDB, skipped without
// KUBECENTER_TEST_DATABASE_URL). Rows are keyed by a unique-per-test cluster id
// from testOwnerID(t), per the isolation contract in testdb_test.go.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

// readLokiColumns returns the raw stored Loki token and origin.
func readLokiColumns(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string) ([]byte, *string) {
	t.Helper()
	var (
		tok    []byte
		origin *string
	)
	if err := pool.QueryRow(ctx, `SELECT loki_token, loki_token_origin
		FROM cluster_monitoring WHERE cluster_id = $1`, id).Scan(&tok, &origin); err != nil {
		t.Fatalf("reading loki columns: %v", err)
	}
	return tok, origin
}

func lokiColumnExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'cluster_monitoring' AND column_name = $1)`, name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

var lokiColumns = []string{"loki_url", "loki_token", "loki_token_origin", "loki_tenant_id", "loki_updated_at"}

func TestMigration000030_RoundTripKeepsMetricsBindings(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(30); err != nil {
		t.Fatalf("migrating to 000030: %v", err)
	}
	for _, c := range lokiColumns {
		if !lokiColumnExists(ctx, t, pool, c) {
			t.Errorf("cluster_monitoring.%s missing after the up migration", c)
		}
	}
	insertMetricsTestCluster(ctx, t, pool, "c1")
	if _, err := pool.Exec(ctx, `
		INSERT INTO cluster_monitoring (cluster_id, prometheus_url, prometheus_token,
			loki_url, loki_token, loki_token_origin, loki_tenant_id, loki_updated_at)
		VALUES ('c1', 'https://prom.example.com', $1, 'https://loki.example.com', $2,
			'https://loki.example.com', 'team-a', now())`,
		encryptForTest(t, "prom-secret"), encryptForTest(t, "loki-secret")); err != nil {
		t.Fatalf("writing both bindings: %v", err)
	}

	if err := m.Migrate(29); err != nil {
		t.Fatalf("rolling back 000030: %v", err)
	}
	for _, c := range lokiColumns {
		if lokiColumnExists(ctx, t, pool, c) {
			t.Errorf("cluster_monitoring.%s survived the down migration", c)
		}
	}
	var promURL string
	var promTok []byte
	if err := pool.QueryRow(ctx, `SELECT prometheus_url, prometheus_token
		FROM cluster_monitoring WHERE cluster_id = 'c1'`).Scan(&promURL, &promTok); err != nil {
		t.Fatalf("binding row after rollback: %v", err)
	}
	if promURL != "https://prom.example.com" || len(promTok) == 0 {
		t.Errorf("after rollback = (%q, %d token bytes); want the metrics binding kept", promURL, len(promTok))
	}

	if err := m.Migrate(30); err != nil {
		t.Fatalf("re-applying 000030: %v", err)
	}
	s := NewClusterLogsStore(pool, metricsTestKey)
	if _, _, err := s.Get(ctx, "c1"); !errors.Is(err, ErrLokiBindingNotFound) {
		t.Errorf("Get after re-apply = %v; want ErrLokiBindingNotFound", err)
	}
	if b, toks, err := NewClusterMetricsStore(pool, metricsTestKey).Get(ctx, "c1"); err != nil || toks.Prometheus != "prom-secret" || b.PrometheusURL != promURL {
		t.Errorf("metrics Get after re-apply = (%+v, %v); want the metrics binding and its token", b, err)
	}
}

// Running the up file against a database already at 30 must not fail.
func TestMigration000030_UpIsIdempotent(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(30); err != nil {
		t.Fatalf("migrating to 000030: %v", err)
	}
	up, err := os.ReadFile("migrations/000030_cluster_loki_binding.up.sql")
	if err != nil {
		t.Fatalf("reading the up file: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Errorf("re-running 000030 up on a database at 30: %v", err)
	}
}

func TestClusterLogsStore_SetGetClear(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterLogsStore(pool, metricsTestKey)

	tok := "loki-secret"
	b, err := s.Upsert(ctx, id, LokiBindingWrite{URL: "https://loki.example.com/base", TenantID: "team-a", Token: &tok})
	if err != nil {
		t.Fatal(err)
	}
	if b.URL != "https://loki.example.com/base" || b.TenantID != "team-a" || !b.HasToken || b.TokenStale || b.UpdatedAt.IsZero() {
		t.Errorf("Upsert = %+v; want the URL, tenant and a usable token", b)
	}
	raw, origin := readLokiColumns(ctx, t, pool, id)
	if len(raw) == 0 || string(raw) == tok {
		t.Fatal("token is not encrypted at rest")
	}
	if origin == nil || *origin != "https://loki.example.com" {
		t.Errorf("stored origin = %v; want https://loki.example.com", origin)
	}

	got, gotTok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if gotTok != tok || !got.HasToken || got.ClusterID != id || got.TenantID != "team-a" || !got.UpdatedAt.Equal(b.UpdatedAt) {
		t.Errorf("Get = (%+v, %q); want the binding and the decrypted token", got, gotTok)
	}

	// A pointer to "" clears the token and its origin.
	empty := ""
	b, err = s.Upsert(ctx, id, LokiBindingWrite{URL: "https://loki.example.com/base", Token: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if b.HasToken || b.TokenStale || b.TenantID != "" {
		t.Errorf("Upsert clearing the token and tenant = %+v; want neither", b)
	}
	if raw, origin := readLokiColumns(ctx, t, pool, id); raw != nil || origin != nil {
		t.Errorf("after clearing token=%v origin=%v; want both NULL", raw, origin)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, id); !errors.Is(err, ErrLokiBindingNotFound) {
		t.Errorf("Get after delete = %v; want ErrLokiBindingNotFound", err)
	}
	if err := s.Delete(ctx, id); !errors.Is(err, ErrLokiBindingNotFound) {
		t.Errorf("second Delete = %v; want ErrLokiBindingNotFound", err)
	}
}

func TestClusterLogsStore_UpsertRequiresURL(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	if _, err := NewClusterLogsStore(pool, metricsTestKey).Upsert(ctx, id, LokiBindingWrite{}); err == nil {
		t.Error("Upsert without a URL succeeded; want an error")
	}
}

// The token is pinned to the origin it was entered for. Moving the URL within
// the origin keeps it; moving it to another origin drops it until it is
// entered again.
func TestClusterLogsStore_TokenFollowsOnlyItsOrigin(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterLogsStore(pool, metricsTestKey)

	tok := "loki-secret"
	if _, err := s.Upsert(ctx, id, LokiBindingWrite{URL: "https://loki.example.com", Token: &tok}); err != nil {
		t.Fatal(err)
	}
	b, err := s.Upsert(ctx, id, LokiBindingWrite{URL: "https://loki.example.com/other", KeptFrom: "https://loki.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !b.HasToken {
		t.Errorf("same-origin move = %+v; want the token kept", b)
	}

	b, err = s.Upsert(ctx, id, LokiBindingWrite{URL: "https://evil.example.net", KeptFrom: "https://loki.example.com/other"})
	if err != nil {
		t.Fatal(err)
	}
	if b.HasToken || b.TokenStale {
		t.Errorf("cross-origin move = %+v; want no token and nothing stale", b)
	}
	if raw, _ := readLokiColumns(ctx, t, pool, id); raw != nil {
		t.Error("the token survived a move to another origin")
	}
	if _, got, err := s.Get(ctx, id); err != nil || got != "" {
		t.Errorf("Get after a cross-origin move = (%q, %v); want no token", got, err)
	}
}

// A row whose URL another writer moved without touching the token (a direct
// edit, or a future previous-release pod) still never hands the token to the
// new host: the read checks the origin, and the token is reported stale.
func TestClusterLogsStore_MovedURLMakesTokenStale(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterLogsStore(pool, metricsTestKey)

	tok := "loki-secret"
	if _, err := s.Upsert(ctx, id, LokiBindingWrite{URL: "https://loki.example.com", Token: &tok}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE cluster_monitoring SET loki_url = 'https://evil.example.net'
		WHERE cluster_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	b, got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" || b.HasToken || !b.TokenStale {
		t.Errorf("Get after the URL moved = (%+v, %q); want no token, reported stale", b, got)
	}

	// Keeping the stale token on the new URL drops it.
	b, err = s.Upsert(ctx, id, LokiBindingWrite{URL: "https://evil.example.net", KeptFrom: "https://evil.example.net"})
	if err != nil {
		t.Fatal(err)
	}
	if b.HasToken || b.TokenStale {
		t.Errorf("keep save on a stale token = %+v; want the token dropped", b)
	}
}

func TestClusterLogsStore_ConcurrentMoveIsRefused(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterLogsStore(pool, metricsTestKey)

	tok := "loki-secret"
	if _, err := s.Upsert(ctx, id, LokiBindingWrite{URL: "https://a.example.com", Token: &tok}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Upsert(ctx, id, LokiBindingWrite{URL: "https://a.example.com/x", KeptFrom: "https://stale-read.example.com"})
	if !errors.Is(err, ErrLokiBindingChanged) {
		t.Errorf("keep save from a stale read = %v; want ErrLokiBindingChanged", err)
	}
	b, _, err := s.Get(ctx, id)
	if err != nil || b.URL != "https://a.example.com" || !b.HasToken {
		t.Errorf("binding after the refused save = (%+v, %v); want it unchanged", b, err)
	}
}

// An unregistered cluster is not "Loki not configured".
func TestClusterLogsStore_UnregisteredClusterIsErrNoRows(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewClusterLogsStore(pool, metricsTestKey)

	for name, read := range map[string]func(string) error{
		"Get":      func(id string) error { _, _, err := s.Get(ctx, id); return err },
		"Revision": func(id string) error { _, err := s.Revision(ctx, id); return err },
	} {
		err := read(testOwnerID(t) + "-unregistered")
		if !errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrLokiBindingNotFound) {
			t.Errorf("%s on an unregistered cluster = %v; want a wrapped pgx.ErrNoRows only", name, err)
		}
	}

	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	if _, _, err := s.Get(ctx, id); !errors.Is(err, ErrLokiBindingNotFound) || errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Get on a registered cluster without a binding = %v; want ErrLokiBindingNotFound only", err)
	}
	if _, err := s.Revision(ctx, id); !errors.Is(err, ErrLokiBindingNotFound) {
		t.Errorf("Revision on a registered cluster without a binding = %v; want ErrLokiBindingNotFound", err)
	}
}

// The two bindings share a row but neither store's writes or deletes reach
// the other's binding or revision.
func TestClusterLogsStore_IndependentOfMetricsBinding(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	logs := NewClusterLogsStore(pool, metricsTestKey)
	metrics := NewClusterMetricsStore(pool, metricsTestKey)

	// A Loki-only row is no metrics binding, and a metrics delete leaves it.
	lokiTok := "loki-secret"
	if _, err := logs.Upsert(ctx, id, LokiBindingWrite{URL: "https://loki.example.com", Token: &lokiTok}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := metrics.Get(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("metrics Get on a Loki-only row = %v; want ErrMetricsBindingNotFound", err)
	}
	if err := metrics.Delete(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("metrics Delete on a Loki-only row = %v; want ErrMetricsBindingNotFound", err)
	}
	if _, got, err := logs.Get(ctx, id); err != nil || got != lokiTok {
		t.Fatalf("Loki binding after a metrics delete with none = (%q, %v); want it kept", got, err)
	}

	// Writes to one binding leave the other's revision alone.
	lokiRev, err := logs.Revision(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	promTok := "prom-secret"
	if _, err := metrics.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://prom.example.com", PrometheusToken: &promTok}); err != nil {
		t.Fatal(err)
	}
	if got, err := logs.Revision(ctx, id); err != nil || !got.Equal(lokiRev) {
		t.Errorf("Loki revision after a metrics write = (%v, %v); want %v", got, err, lokiRev)
	}
	metricsRev, err := metrics.Revision(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Upsert(ctx, id, LokiBindingWrite{URL: "https://loki.example.com", TenantID: "team-b", KeptFrom: "https://loki.example.com"}); err != nil {
		t.Fatal(err)
	}
	if got, err := metrics.Revision(ctx, id); err != nil || !got.Equal(metricsRev) {
		t.Errorf("metrics revision after a Loki write = (%v, %v); want %v", got, err, metricsRev)
	}

	// Deleting one binding keeps the other, token included.
	if err := metrics.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if b, got, err := logs.Get(ctx, id); err != nil || got != lokiTok || b.TenantID != "team-b" {
		t.Errorf("Loki binding after a metrics delete = (%+v, %q, %v); want it kept", b, got, err)
	}
	if _, err := metrics.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://prom.example.com", PrometheusToken: &promTok}); err != nil {
		t.Fatal(err)
	}
	if err := logs.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if b, toks, err := metrics.Get(ctx, id); err != nil || toks.Prometheus != promTok || b.PrometheusURL != "https://prom.example.com" {
		t.Errorf("metrics binding after a Loki delete = (%+v, %v); want it kept", b, err)
	}
	if _, _, err := logs.Get(ctx, id); !errors.Is(err, ErrLokiBindingNotFound) {
		t.Errorf("Loki Get after its delete = %v; want ErrLokiBindingNotFound", err)
	}

	// With neither binding left, the row is gone.
	if err := metrics.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cluster_monitoring WHERE cluster_id = $1`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d cluster_monitoring rows left with no binding; want none", rows)
	}
}

// The pinning rule is the same function the Alertmanager token uses.
func TestPinnedTokenState(t *testing.T) {
	cases := []struct {
		name          string
		url           string
		stored        bool
		origin        string
		usable, stale bool
	}{
		{"no token", "https://l.example.com", false, "", false, false},
		{"no URL", "", true, "https://l.example.com", false, false},
		{"matching origin", "https://l.example.com/p", true, urlorigin.Of("https://l.example.com"), true, false},
		{"other origin", "https://x.example.com", true, "https://l.example.com", false, true},
		{"empty stored origin", "https://l.example.com", true, "", false, true},
		{"URL without an origin", "not a url", true, "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usable, stale := pinnedTokenState(tc.url, tc.stored, tc.origin)
			if usable != tc.usable || stale != tc.stale {
				t.Errorf("pinnedTokenState = (%v, %v); want (%v, %v)", usable, stale, tc.usable, tc.stale)
			}
		})
	}
}
