package store

// cluster_metrics_test.go - metrics binding persistence (#608 PR 4a, 4b).
//
// Env-gated (testDB / migrationScratchDB, skipped without
// KUBECENTER_TEST_DATABASE_URL). Rows are keyed by a unique-per-test cluster id
// from testOwnerID(t), per the isolation contract in testdb_test.go.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

const metricsTestKey = "metrics-test-master-secret"

// prevReleaseUpsertSQL is the PR 4a (schema 27) Upsert statement, copied
// verbatim from the previous release's store. It knows nothing of the
// Alertmanager token columns, which is exactly how a previous-release pod
// writes the table during a rolling update or after a code rollback over
// schema 28. Do not edit it to match the current store.
const prevReleaseUpsertSQL = `
		INSERT INTO cluster_monitoring (cluster_id, prometheus_url, alertmanager_url, prometheus_token, updated_at)
		VALUES ($1, $2, NULLIF($3, ''), $4, now())
		ON CONFLICT (cluster_id) DO UPDATE SET
			prometheus_url   = EXCLUDED.prometheus_url,
			alertmanager_url = EXCLUDED.alertmanager_url,
			prometheus_token = CASE WHEN $5::boolean THEN EXCLUDED.prometheus_token
			                        ELSE cluster_monitoring.prometheus_token END,
			updated_at       = now()
		WHERE $5::boolean OR COALESCE(cluster_monitoring.prometheus_url, '') = $6
		RETURNING cluster_id, prometheus_url, COALESCE(alertmanager_url, ''),
		          prometheus_token IS NOT NULL, updated_at`

// prevReleaseUpsert runs prevReleaseUpsertSQL with the previous release's
// token semantics: a nil token keeps the stored one, guarded by keptFrom.
func prevReleaseUpsert(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id, promURL, amURL string, token []byte, setToken bool, keptFrom string) {
	t.Helper()
	var (
		gotID, gotProm, gotAM string
		hasTok                bool
		updatedAt             time.Time
	)
	if err := pool.QueryRow(ctx, prevReleaseUpsertSQL, id, promURL, amURL, token, setToken, keptFrom).
		Scan(&gotID, &gotProm, &gotAM, &hasTok, &updatedAt); err != nil {
		t.Fatalf("previous-release upsert: %v", err)
	}
}

// insertMetricsTestCluster registers a minimal cluster row the FK needs.
func insertMetricsTestCluster(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO clusters (id, name, api_server_url, auth_type, auth_data)
		VALUES ($1, $1, 'https://example.invalid', 'token', '\x7b7d')`, id); err != nil {
		t.Fatalf("inserting test cluster: %v", err)
	}
}

// encryptForTest encrypts a token the way the store does, for rows a test
// writes with SQL.
func encryptForTest(t *testing.T, plain string) []byte {
	t.Helper()
	enc, err := Encrypt([]byte(plain), metricsTestKey)
	if err != nil {
		t.Fatalf("encrypting test token: %v", err)
	}
	return enc
}

// readAlertmanagerColumns returns the raw stored Alertmanager token and origin.
func readAlertmanagerColumns(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string) ([]byte, *string) {
	t.Helper()
	var (
		tok    []byte
		origin *string
	)
	if err := pool.QueryRow(ctx, `SELECT alertmanager_token, alertmanager_token_origin
		FROM cluster_monitoring WHERE cluster_id = $1`, id).Scan(&tok, &origin); err != nil {
		t.Fatalf("reading alertmanager columns: %v", err)
	}
	return tok, origin
}

// The current store needs schema 28, so this drives schema 27 with the
// previous release's statement and reads the row back with SQL.
func TestMigration000027_RoundTripKeepsBindings(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(27); err != nil {
		t.Fatalf("migrating to 000027: %v", err)
	}
	insertMetricsTestCluster(ctx, t, pool, "c1")
	prevReleaseUpsert(ctx, t, pool, "c1", "https://prom.example.com", "", encryptForTest(t, "secret"), true, "")

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
	var promTok []byte
	if err := pool.QueryRow(ctx, `SELECT prometheus_url, prometheus_token FROM cluster_monitoring
		WHERE cluster_id = 'c1'`).Scan(&gotURL, &promTok); err != nil {
		t.Fatalf("binding row after re-apply: %v", err)
	}
	if gotURL != "https://prom.example.com" || promTok != nil {
		t.Errorf("after re-apply url=%q token bytes=%d; want the URL kept and the token gone", gotURL, len(promTok))
	}
}

func TestMigration000028_RoundTripKeepsBindings(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(28); err != nil {
		t.Fatalf("migrating to 000028: %v", err)
	}
	insertMetricsTestCluster(ctx, t, pool, "c1")
	hasCol := func(name string) bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'cluster_monitoring' AND column_name = $1)`, name).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	newCols := []string{"alertmanager_token", "alertmanager_token_origin"}
	for _, c := range newCols {
		if !hasCol(c) {
			t.Errorf("cluster_monitoring.%s missing after the up migration", c)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cluster_monitoring (cluster_id, prometheus_url, prometheus_token, alertmanager_url,
			alertmanager_token, alertmanager_token_origin)
		VALUES ('c1', 'https://prom.example.com', $1, 'https://am.example.com', $2, 'https://am.example.com')`,
		encryptForTest(t, "prom-secret"), encryptForTest(t, "am-secret")); err != nil {
		t.Fatalf("writing a binding with both tokens: %v", err)
	}

	if err := m.Migrate(27); err != nil {
		t.Fatalf("rolling back 000028: %v", err)
	}
	for _, c := range newCols {
		if hasCol(c) {
			t.Errorf("cluster_monitoring.%s survived the down migration", c)
		}
	}
	var promURL, amURL string
	var promTok []byte
	if err := pool.QueryRow(ctx, `SELECT prometheus_url, alertmanager_url, prometheus_token
		FROM cluster_monitoring WHERE cluster_id = 'c1'`).Scan(&promURL, &amURL, &promTok); err != nil {
		t.Fatalf("binding row after rollback: %v", err)
	}
	if promURL != "https://prom.example.com" || amURL != "https://am.example.com" || len(promTok) == 0 {
		t.Errorf("after rollback = (%q, %q, %d token bytes); want URLs and the Prometheus token kept", promURL, amURL, len(promTok))
	}

	if err := m.Migrate(28); err != nil {
		t.Fatalf("re-applying 000028: %v", err)
	}
	amTok, amOrigin := readAlertmanagerColumns(ctx, t, pool, "c1")
	if amTok != nil || amOrigin != nil {
		t.Errorf("after re-apply token=%v origin=%v; want both NULL", amTok, amOrigin)
	}

	// Through the store: no Alertmanager token (and no stale one), and a save
	// that omits both tokens succeeds and stores no Alertmanager token.
	s := NewClusterMetricsStore(pool, metricsTestKey)
	b, toks, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("Get after re-apply: %v", err)
	}
	if b.HasAlertmanagerToken || b.AlertmanagerTokenStale || toks.Alertmanager != "" {
		t.Errorf("Get after re-apply = %+v, alertmanager token %q; want none, not stale", b, toks.Alertmanager)
	}
	if !b.HasToken || toks.Prometheus != "prom-secret" {
		t.Errorf("Get after re-apply: HasToken=%v prometheus token %q; want the Prometheus token kept", b.HasToken, toks.Prometheus)
	}
	b, err = s.Upsert(ctx, "c1", MetricsBindingWrite{
		PrometheusURL:           "https://prom.example.com",
		AlertmanagerURL:         "https://am.example.com",
		AlertmanagerTokenOrigin: urlorigin.Of("https://am.example.com"),
		KeptFrom:                "https://prom.example.com",
		KeptAMFrom:              "https://am.example.com",
	})
	if err != nil {
		t.Fatalf("keep save after re-apply: %v", err)
	}
	if b.HasAlertmanagerToken || !b.HasToken {
		t.Errorf("keep save after re-apply = %+v; want the Prometheus token kept and no Alertmanager token", b)
	}
	if amTok, amOrigin := readAlertmanagerColumns(ctx, t, pool, "c1"); amTok != nil || amOrigin != nil {
		t.Errorf("after keep save token=%v origin=%v; want both NULL", amTok, amOrigin)
	}
}

// Running the up file against a database already at 28 must not fail.
func TestMigration000028_UpIsIdempotent(t *testing.T) {
	m, pool := migrationScratchDB(t)
	ctx := t.Context()
	if err := m.Migrate(28); err != nil {
		t.Fatalf("migrating to 000028: %v", err)
	}
	up, err := os.ReadFile("migrations/000028_cluster_alertmanager_token.up.sql")
	if err != nil {
		t.Fatalf("reading the up file: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Errorf("re-running 000028 up on a database at 28: %v", err)
	}
}

func TestClusterMetricsStore_UpsertKeepsTokenOnNil(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	tok := "first"
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com", PrometheusToken: &tok}); err != nil {
		t.Fatal(err)
	}
	b, err := s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: "https://b.example.com", AlertmanagerURL: "https://am.example.com",
		AlertmanagerTokenOrigin: urlorigin.Of("https://am.example.com"), KeptFrom: "https://a.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !b.HasToken || b.PrometheusURL != "https://b.example.com" || b.AlertmanagerURL != "https://am.example.com" {
		t.Errorf("binding = %+v; want the token kept and the URLs updated", b)
	}
	if _, got, err := s.Get(ctx, id); err != nil || got.Prometheus != "first" {
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
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://b.example.com", PrometheusToken: &tok}); err != nil {
		t.Fatal(err)
	}
	// The caller checked the binding while it still pointed at a.example.com.
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com", KeptFrom: "https://a.example.com"}); !errors.Is(err, ErrMetricsBindingChanged) {
		t.Fatalf("Upsert with a stale keptFrom = %v; want ErrMetricsBindingChanged", err)
	}
	// The caller saw no binding, but one was created meanwhile.
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com"}); !errors.Is(err, ErrMetricsBindingChanged) {
		t.Fatalf("Upsert with keptFrom empty over a new binding = %v; want ErrMetricsBindingChanged", err)
	}
	b, got, err := s.Get(ctx, id)
	if err != nil || b.PrometheusURL != "https://b.example.com" || got.Prometheus != "b-token" {
		t.Errorf("Get = (%+v, %q, %v); want b.example.com with b-token untouched", b, got, err)
	}
	// A supplied token ignores keptFrom.
	other := "a-token"
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com", PrometheusToken: &other, KeptFrom: "stale"}); err != nil {
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
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com", PrometheusToken: &tok}); err != nil {
		t.Fatal(err)
	}
	empty := ""
	b, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com", PrometheusToken: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if b.HasToken {
		t.Error("HasToken = true after clearing with an empty token")
	}
	if _, got, err := s.Get(ctx, id); err != nil || got.Prometheus != "" {
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
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com", PrometheusToken: &tok}); err != nil {
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
	if err != nil || got.Prometheus != tok || !b.HasToken || b.ClusterID != id || b.UpdatedAt.IsZero() {
		t.Errorf("Get = (%+v, %q, %v); want the decrypted token", b, got, err)
	}
}

func TestClusterMetricsStore_GetAfterDeleteNotFound(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com"}); err != nil {
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

// Revision is what lets a pod notice a binding write made by another pod: it
// equals Get's UpdatedAt, moves on every write (the previous release's upsert
// included), and fails the same way Get does.
func TestClusterMetricsStore_RevisionTracksEveryWrite(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	if _, err := s.Revision(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("Revision without a binding = %v; want ErrMetricsBindingNotFound", err)
	}
	if _, err := s.Revision(ctx, id+"-unregistered"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Revision on an unregistered cluster = %v; want a wrapped pgx.ErrNoRows", err)
	}

	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{PrometheusURL: "https://a.example.com"}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Revision(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(b.UpdatedAt) {
		t.Errorf("Revision = %v; want Get's UpdatedAt %v", first, b.UpdatedAt)
	}

	prevReleaseUpsert(ctx, t, pool, id, "https://a.example.com", "https://am-moved.example.com", nil, false, "https://a.example.com")
	second, err := s.Revision(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !second.After(first) {
		t.Errorf("Revision after a previous-release write = %v; want it after %v", second, first)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revision(ctx, id); !errors.Is(err, ErrMetricsBindingNotFound) {
		t.Errorf("Revision after delete = %v; want ErrMetricsBindingNotFound", err)
	}
}

// --- Alertmanager token (#608 PR 4b, KTD1 + KTD8) ---

const (
	amProm  = "https://prom.example.com"
	amHostB = "https://am-b.example.com:9093"
	amHostA = "https://am-a.example.com:9093"
)

// seedBothTokens writes a binding with a Prometheus token and an Alertmanager
// URL on amHostB with its token.
func seedBothTokens(ctx context.Context, t *testing.T, s *ClusterMetricsStore, id string) {
	t.Helper()
	pt, at := "prom-token", "am-token"
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: amProm, AlertmanagerURL: amHostB,
		PrometheusToken: &pt, AlertmanagerToken: &at,
		AlertmanagerTokenOrigin: urlorigin.Of(amHostB),
	}); err != nil {
		t.Fatalf("seeding the binding: %v", err)
	}
}

// assertSeedUntouched checks the binding is still what seedBothTokens wrote.
func assertSeedUntouched(ctx context.Context, t *testing.T, s *ClusterMetricsStore, id string) {
	t.Helper()
	b, toks, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if b.PrometheusURL != amProm || b.AlertmanagerURL != amHostB || toks.Prometheus != "prom-token" ||
		toks.Alertmanager != "am-token" || !b.HasAlertmanagerToken {
		t.Errorf("binding = %+v (prometheus token %q, alertmanager token %q); want the seed untouched",
			b, toks.Prometheus, toks.Alertmanager)
	}
}

func TestClusterMetricsStore_AlertmanagerTokenStoredEncryptedWithOrigin(t *testing.T) {
	// The expected origins are literals, not computed by the helper: they pin
	// what is stored for an uppercase host, a path prefix and an explicit port.
	cases := []struct {
		name, amURL, wantOrigin string
	}{
		{"uppercase host", "https://AM.Example.COM", "https://am.example.com"},
		{"path prefix", "https://am.example.com/alertmanager", "https://am.example.com"},
		{"explicit port", "https://am.example.com:9443", "https://am.example.com:9443"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testDB(t)
			ctx := t.Context()
			id := testOwnerID(t)
			insertMetricsTestCluster(ctx, t, pool, id)
			s := NewClusterMetricsStore(pool, metricsTestKey)

			tok := "am-bearer-value"
			b, err := s.Upsert(ctx, id, MetricsBindingWrite{
				PrometheusURL: amProm, AlertmanagerURL: c.amURL,
				AlertmanagerToken: &tok, AlertmanagerTokenOrigin: urlorigin.Of(c.amURL),
			})
			if err != nil {
				t.Fatal(err)
			}
			if !b.HasAlertmanagerToken || b.AlertmanagerTokenStale {
				t.Errorf("Upsert binding = %+v; want HasAlertmanagerToken, not stale", b)
			}
			raw, origin := readAlertmanagerColumns(ctx, t, pool, id)
			if len(raw) == 0 || string(raw) == tok {
				t.Error("alertmanager token is not encrypted at rest")
			}
			if origin == nil || *origin != c.wantOrigin {
				t.Errorf("stored origin = %v; want %q", origin, c.wantOrigin)
			}
			b, toks, err := s.Get(ctx, id)
			if err != nil || toks.Alertmanager != tok || !b.HasAlertmanagerToken || b.AlertmanagerTokenStale {
				t.Errorf("Get = (%+v, alertmanager token %q, %v); want the decrypted token", b, toks.Alertmanager, err)
			}
			if b.HasToken || toks.Prometheus != "" {
				t.Errorf("Get: HasToken=%v prometheus token %q; the Alertmanager token must not read as the Prometheus one", b.HasToken, toks.Prometheus)
			}
		})
	}
}

// A token row whose origin is not the current URL's origin, or is NULL or
// empty, is never returned (fails closed) and reads as stale. The next write
// drops it.
func TestClusterMetricsStore_AlertmanagerTokenOriginMismatchFailsClosed(t *testing.T) {
	other, empty := "https://am-a.example.com:9093", ""
	cases := []struct {
		name   string
		origin *string
	}{
		{"origin of another host", &other},
		{"NULL origin", nil},
		{"empty origin", &empty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testDB(t)
			ctx := t.Context()
			id := testOwnerID(t)
			insertMetricsTestCluster(ctx, t, pool, id)
			s := NewClusterMetricsStore(pool, metricsTestKey)

			if _, err := pool.Exec(ctx, `
				INSERT INTO cluster_monitoring (cluster_id, prometheus_url, prometheus_token, alertmanager_url,
					alertmanager_token, alertmanager_token_origin)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				id, amProm, encryptForTest(t, "prom-token"), amHostB, encryptForTest(t, "am-token"), c.origin); err != nil {
				t.Fatal(err)
			}
			b, toks, err := s.Get(ctx, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if toks.Alertmanager != "" || b.HasAlertmanagerToken {
				t.Errorf("Get returned alertmanager token %q, HasAlertmanagerToken=%v; want none", toks.Alertmanager, b.HasAlertmanagerToken)
			}
			if !b.AlertmanagerTokenStale {
				t.Error("AlertmanagerTokenStale = false; want true for a token row whose origin does not match")
			}
			if toks.Prometheus != "prom-token" {
				t.Errorf("prometheus token = %q; want it unaffected", toks.Prometheus)
			}

			// The next write by this release drops the mismatched token, even
			// a keep save that leaves the URL where it is.
			b, err = s.Upsert(ctx, id, MetricsBindingWrite{
				PrometheusURL: amProm, AlertmanagerURL: amHostB,
				AlertmanagerTokenOrigin: urlorigin.Of(amHostB),
				KeptFrom:                amProm, KeptAMFrom: amHostB,
			})
			if err != nil {
				t.Fatalf("keep save: %v", err)
			}
			if b.HasAlertmanagerToken || b.AlertmanagerTokenStale {
				t.Errorf("keep save binding = %+v; want no token and nothing stale", b)
			}
			if raw, origin := readAlertmanagerColumns(ctx, t, pool, id); raw != nil || origin != nil {
				t.Errorf("after keep save token=%v origin=%v; want both NULL", raw, origin)
			}
		})
	}
}

func TestClusterMetricsStore_NeverHadAlertmanagerTokenIsNotStale(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)

	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: amProm, AlertmanagerURL: amHostB, AlertmanagerTokenOrigin: urlorigin.Of(amHostB),
	}); err != nil {
		t.Fatal(err)
	}
	b, toks, err := s.Get(ctx, id)
	if err != nil || b.HasAlertmanagerToken || b.AlertmanagerTokenStale || toks.Alertmanager != "" {
		t.Errorf("Get = (%+v, %q, %v); want no Alertmanager token and not stale", b, toks.Alertmanager, err)
	}
}

// A keep save follows the URL within its origin and never carries the token
// to another origin, even if the caller skipped its own check.
func TestClusterMetricsStore_AlertmanagerKeepStaysOnOrigin(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)
	seedBothTokens(ctx, t, s, id)

	samePath := amHostB + "/alertmanager"
	b, err := s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: amProm, AlertmanagerURL: samePath,
		AlertmanagerTokenOrigin: urlorigin.Of(samePath),
		KeptFrom:                amProm, KeptAMFrom: amHostB,
	})
	if err != nil || !b.HasAlertmanagerToken || !b.HasToken {
		t.Fatalf("keep save on the same origin = (%+v, %v); want both tokens kept", b, err)
	}
	if _, toks, err := s.Get(ctx, id); err != nil || toks.Alertmanager != "am-token" {
		t.Errorf("Get alertmanager token = (%q, %v); want am-token", toks.Alertmanager, err)
	}

	b, err = s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: amProm, AlertmanagerURL: amHostA,
		AlertmanagerTokenOrigin: urlorigin.Of(amHostA),
		KeptFrom:                amProm, KeptAMFrom: samePath,
	})
	if err != nil {
		t.Fatalf("keep save to another origin: %v", err)
	}
	if b.HasAlertmanagerToken {
		t.Errorf("keep save to another origin = %+v; want the token dropped", b)
	}
	if raw, origin := readAlertmanagerColumns(ctx, t, pool, id); raw != nil || origin != nil {
		t.Errorf("after moving origin token=%v origin=%v; want both NULL", raw, origin)
	}
}

// KTD8: a keep save is refused, writing nothing, when the stored Alertmanager
// URL is no longer the one the caller read.
func TestClusterMetricsStore_AlertmanagerKeepRefusedWhenURLMoved(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)
	seedBothTokens(ctx, t, s, id)

	// The caller read the binding while its Alertmanager URL was amHostA.
	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: amProm, AlertmanagerURL: amHostA,
		AlertmanagerTokenOrigin: urlorigin.Of(amHostA),
		KeptFrom:                amProm, KeptAMFrom: amHostA,
	}); !errors.Is(err, ErrMetricsBindingChanged) {
		t.Fatalf("keep save with a stale keptAMFrom = %v; want ErrMetricsBindingChanged", err)
	}
	assertSeedUntouched(ctx, t, s, id)
}

// The caller saw no Alertmanager URL, but the binding gained one meanwhile.
func TestClusterMetricsStore_AlertmanagerKeepRefusedWhenURLAdded(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)
	seedBothTokens(ctx, t, s, id)

	if _, err := s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: amProm, AlertmanagerURL: amHostA,
		AlertmanagerTokenOrigin: urlorigin.Of(amHostA),
		KeptFrom:                amProm, KeptAMFrom: "",
	}); !errors.Is(err, ErrMetricsBindingChanged) {
		t.Fatalf("keep save with keptAMFrom empty over a binding with an Alertmanager URL = %v; want ErrMetricsBindingChanged", err)
	}
	assertSeedUntouched(ctx, t, s, id)
}

// Each URL is guarded in its own clause: supplying one token does not lift
// the guard on the other.
func TestClusterMetricsStore_OneTokenSuppliedOtherURLMoved(t *testing.T) {
	t.Run("new Prometheus token, Alertmanager URL moved", func(t *testing.T) {
		pool := testDB(t)
		ctx := t.Context()
		id := testOwnerID(t)
		insertMetricsTestCluster(ctx, t, pool, id)
		s := NewClusterMetricsStore(pool, metricsTestKey)
		seedBothTokens(ctx, t, s, id)

		pt := "new-prom-token"
		if _, err := s.Upsert(ctx, id, MetricsBindingWrite{
			PrometheusURL: amProm, AlertmanagerURL: amHostA,
			PrometheusToken:         &pt,
			AlertmanagerTokenOrigin: urlorigin.Of(amHostA),
			KeptAMFrom:              amHostA,
		}); !errors.Is(err, ErrMetricsBindingChanged) {
			t.Fatalf("Upsert = %v; want ErrMetricsBindingChanged", err)
		}
		assertSeedUntouched(ctx, t, s, id)
	})
	t.Run("new Alertmanager token, Prometheus URL moved", func(t *testing.T) {
		pool := testDB(t)
		ctx := t.Context()
		id := testOwnerID(t)
		insertMetricsTestCluster(ctx, t, pool, id)
		s := NewClusterMetricsStore(pool, metricsTestKey)
		seedBothTokens(ctx, t, s, id)

		at := "new-am-token"
		if _, err := s.Upsert(ctx, id, MetricsBindingWrite{
			PrometheusURL: "https://prom-old.example.com", AlertmanagerURL: amHostB,
			AlertmanagerToken:       &at,
			AlertmanagerTokenOrigin: urlorigin.Of(amHostB),
			KeptFrom:                "https://prom-old.example.com",
		}); !errors.Is(err, ErrMetricsBindingChanged) {
			t.Fatalf("Upsert = %v; want ErrMetricsBindingChanged", err)
		}
		assertSeedUntouched(ctx, t, s, id)
	})
}

// AE5: a previous-release pod writes with the PR 4a statement, which leaves
// the token columns alone. Whether it clears the Alertmanager URL or moves it
// to another origin, the store must not hand the token out afterwards.
func TestClusterMetricsStore_PreviousReleaseUpsertLeavesNoUsableToken(t *testing.T) {
	cases := []struct {
		name      string
		amURL     string
		wantStale bool
	}{
		{"clears the Alertmanager URL", "", false},
		{"moves the Alertmanager URL to another origin", amHostA, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testDB(t)
			ctx := t.Context()
			id := testOwnerID(t)
			insertMetricsTestCluster(ctx, t, pool, id)
			s := NewClusterMetricsStore(pool, metricsTestKey)
			seedBothTokens(ctx, t, s, id)

			prevReleaseUpsert(ctx, t, pool, id, amProm, c.amURL, nil, false, amProm)
			// The scenario is real only if the old statement left the token row.
			if raw, _ := readAlertmanagerColumns(ctx, t, pool, id); raw == nil {
				t.Fatal("the previous-release upsert removed the token row; the scenario no longer applies")
			}

			b, toks, err := s.Get(ctx, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if toks.Alertmanager != "" || b.HasAlertmanagerToken {
				t.Errorf("Get returned alertmanager token %q, HasAlertmanagerToken=%v; want none", toks.Alertmanager, b.HasAlertmanagerToken)
			}
			if b.AlertmanagerTokenStale != c.wantStale {
				t.Errorf("AlertmanagerTokenStale = %v; want %v", b.AlertmanagerTokenStale, c.wantStale)
			}
			if b.AlertmanagerURL != c.amURL || toks.Prometheus != "prom-token" {
				t.Errorf("Get = %+v, prometheus token %q; want the previous release's URL and the Prometheus token", b, toks.Prometheus)
			}
		})
	}
}

// After a previous-release pod cleared the Alertmanager URL (leaving the token
// row behind), a keep save that puts a URL on the old origin back must not
// revive the old token.
func TestClusterMetricsStore_KeepAfterPreviousReleaseClearStoresNoToken(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	s := NewClusterMetricsStore(pool, metricsTestKey)
	seedBothTokens(ctx, t, s, id)
	prevReleaseUpsert(ctx, t, pool, id, amProm, "", nil, false, amProm)

	b, err := s.Upsert(ctx, id, MetricsBindingWrite{
		PrometheusURL: amProm, AlertmanagerURL: amHostB,
		AlertmanagerTokenOrigin: urlorigin.Of(amHostB),
		KeptFrom:                amProm, KeptAMFrom: "",
	})
	if err != nil {
		t.Fatalf("keep save: %v", err)
	}
	if b.HasAlertmanagerToken || b.AlertmanagerTokenStale {
		t.Errorf("keep save binding = %+v; want no Alertmanager token and nothing stale", b)
	}
	if raw, origin := readAlertmanagerColumns(ctx, t, pool, id); raw != nil || origin != nil {
		t.Errorf("after keep save token=%v origin=%v; want both NULL", raw, origin)
	}
	if b2, toks, err := s.Get(ctx, id); err != nil || toks.Alertmanager != "" || b2.HasAlertmanagerToken {
		t.Errorf("Get = (%+v, %q, %v); want no Alertmanager token", b2, toks.Alertmanager, err)
	}
}

func TestClusterMetricsStore_AlertmanagerClear(t *testing.T) {
	t.Run("empty token clears it", func(t *testing.T) {
		pool := testDB(t)
		ctx := t.Context()
		id := testOwnerID(t)
		insertMetricsTestCluster(ctx, t, pool, id)
		s := NewClusterMetricsStore(pool, metricsTestKey)
		seedBothTokens(ctx, t, s, id)

		empty := ""
		b, err := s.Upsert(ctx, id, MetricsBindingWrite{
			PrometheusURL: amProm, AlertmanagerURL: amHostB,
			AlertmanagerToken: &empty, AlertmanagerTokenOrigin: urlorigin.Of(amHostB),
			KeptFrom: amProm,
		})
		if err != nil {
			t.Fatal(err)
		}
		if b.HasAlertmanagerToken || b.AlertmanagerTokenStale || !b.HasToken || b.AlertmanagerURL != amHostB {
			t.Errorf("binding = %+v; want the Alertmanager token cleared, its URL and the Prometheus token kept", b)
		}
		if raw, origin := readAlertmanagerColumns(ctx, t, pool, id); raw != nil || origin != nil {
			t.Errorf("after clear token=%v origin=%v; want both NULL", raw, origin)
		}
	})
	t.Run("empty URL clears the token and origin", func(t *testing.T) {
		pool := testDB(t)
		ctx := t.Context()
		id := testOwnerID(t)
		insertMetricsTestCluster(ctx, t, pool, id)
		s := NewClusterMetricsStore(pool, metricsTestKey)
		seedBothTokens(ctx, t, s, id)

		b, err := s.Upsert(ctx, id, MetricsBindingWrite{
			PrometheusURL: amProm, AlertmanagerURL: "",
			KeptFrom: amProm, KeptAMFrom: amHostB,
		})
		if err != nil {
			t.Fatal(err)
		}
		if b.HasAlertmanagerToken || b.AlertmanagerTokenStale || b.AlertmanagerURL != "" || !b.HasToken {
			t.Errorf("binding = %+v; want the Alertmanager URL and token cleared, the Prometheus token kept", b)
		}
		if raw, origin := readAlertmanagerColumns(ctx, t, pool, id); raw != nil || origin != nil {
			t.Errorf("after clearing the URL token=%v origin=%v; want both NULL", raw, origin)
		}
	})
}
