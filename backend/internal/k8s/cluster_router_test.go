package k8s

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/kubecenter/kubecenter/internal/store"
)

func TestIsLocalClusterID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"local", true},
		{"abc123", false},
		{"LOCAL", false}, // case-sensitive on purpose; middleware lowercases via WithClusterID
		{"local-something", false},
	}
	for _, tc := range cases {
		if got := isLocalClusterID(tc.in); got != tc.want {
			t.Errorf("isLocalClusterID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestIsLocalClusterIDExported verifies the exported IsLocalClusterID function
// produces identical results to the unexported alias. This ensures callers
// outside the k8s package (e.g. resources, server) see consistent behavior.
func TestIsLocalClusterIDExported(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"local", true},
		{"abc123", false},
		{"remote-cluster-1", false},
	}
	for _, tc := range cases {
		if got := IsLocalClusterID(tc.in); got != tc.want {
			t.Errorf("IsLocalClusterID(%q) = %v, want %v", tc.in, got, tc.want)
		}
		// Must agree with the unexported alias
		if IsLocalClusterID(tc.in) != isLocalClusterID(tc.in) {
			t.Errorf("IsLocalClusterID(%q) disagrees with isLocalClusterID(%q)", tc.in, tc.in)
		}
	}
}

func TestNormalizedClusterID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "local"},
		{"local", "local"},
		{"abc123", "abc123"},
		{"my-remote-cluster", "my-remote-cluster"},
	}
	for _, tc := range cases {
		if got := normalizedClusterID(tc.in); got != tc.want {
			t.Errorf("normalizedClusterID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRouterFor_LocalPath verifies that an empty/local clusterID dispatches
// to the localFactory and returns a populated ClientPair with IsLocal=true.
// We construct ClusterRouter with a test ClientFactory whose ClientForUser
// returns a stub clientset; no live Kubernetes API is required.
func TestRouterFor_LocalPath(t *testing.T) {
	// Use the dynamic-aware test factory so both client types stub out
	// without dialling a real cluster.
	stubClient := &kubernetes.Clientset{}
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(stubClient, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx := context.Background()

	cases := []struct {
		name      string
		clusterID string
	}{
		{"empty clusterID", ""},
		{"explicit local", "local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pair, err := router.RouterFor(ctx, tc.clusterID, "alice", []string{"engineering"})
			if err != nil {
				t.Fatalf("RouterFor returned error: %v", err)
			}
			if pair == nil {
				t.Fatal("RouterFor returned nil ClientPair")
			}
			if !pair.IsLocal {
				t.Errorf("IsLocal = false for %q; want true", tc.clusterID)
			}
			if pair.ClusterID != "local" {
				t.Errorf("ClusterID = %q for input %q; want \"local\"", pair.ClusterID, tc.clusterID)
			}
			if pair.Typed == nil {
				t.Error("Typed client is nil")
			}
			if pair.Dynamic == nil {
				t.Error("Dynamic client is nil")
			}
		})
	}
}

// TestRouterFor_RemoteFailsClosedWhenStoreNil verifies F#18: when
// clusterStore is nil (local-only deployment without a database), a
// non-local clusterID hard-errors instead of silently routing to the
// local factory. The previous behavior was a silent downgrade — handlers
// that didn't double-check pair.IsLocal would execute "remote" requests
// against the local cluster. AccessChecker already failed closed in the
// same scenario; both layers now agree.
func TestRouterFor_RemoteFailsClosedWhenStoreNil(t *testing.T) {
	stubClient := &kubernetes.Clientset{}
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(stubClient, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := router.RouterFor(context.Background(), "some-remote-id", "alice", nil)
	if err == nil {
		t.Fatal("RouterFor returned nil error for non-local clusterID with nil clusterStore; want hard-error (F#18 fail-closed)")
	}
	if !strings.Contains(err.Error(), "no cluster store") {
		t.Errorf("error = %q; want substring 'no cluster store'", err.Error())
	}
}

// TestNewClusterRouter_NilStoreIsNilInterface pins the nil-to-interface
// guard inside NewClusterRouter itself (`if cs != nil { cr.clusterStore = cs }`),
// as opposed to every other fail-closed test in this file, which builds the
// router via a struct literal and therefore never exercises that guard. cs
// here is a genuinely nil *store.ClusterStore — the exact value production
// passes for a local-only deployment (main.go's
// `var clusterStore *appstore.ClusterStore`, left nil when no database is
// configured) — routed through the real constructor. If the guard were
// removed and cs were assigned to the clusterGetter field directly, cr.clusterStore
// would become a non-nil interface wrapping a nil pointer (the Go
// nil-interface trap): the `cr.clusterStore == nil` fail-closed check in
// TargetSchemaFor would then be false, and the call would instead panic
// inside (*store.ClusterStore).Get on a nil receiver — either way this test
// fails, which is exactly the point.
func TestNewClusterRouter_NilStoreIsNilInterface(t *testing.T) {
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, stubDyn)

	var nilStore *store.ClusterStore // deliberately nil, concrete type
	router := NewClusterRouter(factory, nilStore, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	schema, err := router.TargetSchemaFor(context.Background(), "some-remote-id", "alice", nil)
	if err == nil {
		t.Fatal("TargetSchemaFor returned nil error for a nil *store.ClusterStore passed through NewClusterRouter; want fail-closed error")
	}
	if !strings.Contains(err.Error(), "no cluster store") {
		t.Errorf("error = %q; want substring 'no cluster store'", err.Error())
	}
	if schema != nil {
		t.Errorf("schema = %+v; want nil on error", schema)
	}

	// Same assertion through RouterFor, since NewClusterRouter's guard
	// protects both ClientForCluster/DynamicClientForCluster and
	// TargetSchemaFor from the same nil-interface trap.
	if _, err := router.RouterFor(context.Background(), "some-remote-id", "alice", nil); err == nil {
		t.Fatal("RouterFor returned nil error for a nil *store.ClusterStore passed through NewClusterRouter; want fail-closed error")
	}
}

// TestApplyClusterTLS_FailsClosedWithoutCAData is the F#5 regression test:
// when no CA data is stored and AllowInsecureTLS is false, building the
// remote config must error rather than silently disable TLS verification.
func TestApplyClusterTLS_FailsClosedWithoutCAData(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("no CA + no opt-in → error", func(t *testing.T) {
		cfg := &rest.Config{}
		err := applyClusterTLS(cfg, "my-cluster", nil, false, logger)
		if err == nil {
			t.Fatal("expected error; got nil")
		}
		want := "AllowInsecureTLS is false"
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q; want substring %q", err.Error(), want)
		}
		if cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = true; want false (fail-closed must NOT mutate config)")
		}
	})

	t.Run("no CA + admin opt-in → insecure set", func(t *testing.T) {
		cfg := &rest.Config{}
		if err := applyClusterTLS(cfg, "homelab", nil, true, logger); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = false; want true (AllowInsecureTLS opt-in must disable verification)")
		}
	})

	t.Run("CA data present → no change", func(t *testing.T) {
		cfg := &rest.Config{TLSClientConfig: rest.TLSClientConfig{CAData: []byte("-----BEGIN CERTIFICATE-----...")}}
		if err := applyClusterTLS(cfg, "prod", cfg.TLSClientConfig.CAData, false, logger); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = true; want false (CA data must NOT trigger insecure path)")
		}
	})

	t.Run("CA data present + opt-in → no change (CA wins)", func(t *testing.T) {
		// AllowInsecureTLS is only consulted when CA data is missing.
		// Operators with both set keep the verified path.
		cfg := &rest.Config{TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca")}}
		if err := applyClusterTLS(cfg, "weird", cfg.TLSClientConfig.CAData, true, logger); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = true; want false — CA data must beat the AllowInsecureTLS flag")
		}
	})
}

func TestLocalFactory(t *testing.T) {
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := router.LocalFactory(); got != factory {
		t.Errorf("LocalFactory() returned %v; want the original factory", got)
	}
}

// ---------------------------------------------------------------------------
// U7 — TargetSchemaFor / TargetFor
//
// These tests build a *ClusterRouter directly (struct literal, not
// NewClusterRouter) so the remote-path tests can inject a fakeClusterGetter
// in place of *store.ClusterStore. That substitution is possible because the
// ClusterRouter.clusterStore field is typed as the unexported clusterGetter
// interface (see cluster_router.go) precisely so these tests don't need a
// live PostgreSQL connection to exercise a "successful remote resolve".
// ---------------------------------------------------------------------------

// fakeClusterGetter is a clusterGetter that never touches PostgreSQL. Every
// call returns a fresh *store.ClusterRecord copy (or the configured error),
// so concurrent callers never race on shared mutable state.
type fakeClusterGetter struct {
	mu     sync.Mutex
	record *store.ClusterRecord
	err    error
	calls  int
}

func (f *fakeClusterGetter) Get(_ context.Context, _ string) (*store.ClusterRecord, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	rec := *f.record
	return &rec, nil
}

// testClusterEncryptionKey is shared between testClusterRecord (which
// encrypts with it) and newRemoteTestRouter (which decrypts with it) so
// buildRemoteConfig's decrypt step succeeds without a real database-backed
// ClusterStore.
const testClusterEncryptionKey = "u7-test-encryption-key-not-a-secret"

// testClusterRecord builds a ClusterRecord that survives buildRemoteConfig's
// full pipeline (SSRF check, decrypt, TLS policy) with no network access:
// the API server URL is a literal, non-private IP (TEST-NET-3, RFC 5737 —
// ValidateRemoteURL skips DNS resolution for literal IPs and this range is
// not loopback/private/link-local/CGNAT), AuthData is validly encrypted with
// testClusterEncryptionKey, and AllowInsecureTLS is set so the absent CAData
// doesn't fail closed.
func testClusterRecord(t *testing.T, createdAt time.Time) *store.ClusterRecord {
	t.Helper()
	authData, err := store.Encrypt([]byte("fake-bearer-token"), testClusterEncryptionKey)
	if err != nil {
		t.Fatalf("encrypting test auth data: %v", err)
	}
	return &store.ClusterRecord{
		ID:               "remote-1",
		APIServerURL:     "https://203.0.113.10:6443",
		AuthData:         authData,
		AllowInsecureTLS: true,
		CreatedAt:        createdAt,
	}
}

// newRemoteTestRouter builds a ClusterRouter wired for hermetic
// TargetSchemaFor/TargetFor tests: local factory stubs (as in
// TestRouterFor_LocalPath), an initialized schema cache (NewClusterRouter's
// own initialization, replicated here because injecting a fake clusterGetter
// isn't possible through NewClusterRouter's concrete *store.ClusterStore
// parameter), and the given fake store.
//
// getter is deliberately typed as the CONCRETE *fakeClusterGetter, not the
// clusterGetter interface, and the nil check below happens on that concrete
// type before it is ever converted to the interface field — mirroring
// NewClusterRouter's own `if cs != nil` guard mechanism, not just its
// syntax. Typing this parameter as clusterGetter instead would defeat the
// very trap this helper exists to guard against: a future
// `var g *fakeClusterGetter; newRemoteTestRouter(g)` would box the typed nil
// into a non-nil interface AT THE CALL SITE (Go's classic nil-interface
// gotcha), by which point a `getter == nil` check inside this function can
// no longer see it — the boxing already happened. Keeping the parameter
// concrete means the nil check runs before any boxing occurs, exactly like
// NewClusterRouter's cs *store.ClusterStore parameter.
func newRemoteTestRouter(getter *fakeClusterGetter) *ClusterRouter {
	stubClient := &kubernetes.Clientset{}
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(stubClient, stubDyn)
	cr := &ClusterRouter{
		localFactory:  factory,
		encryptionKey: testClusterEncryptionKey,
		schemaCache:   newTargetSchemaCache(),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if getter != nil {
		cr.clusterStore = getter
	}
	return cr
}

// TestTargetSchemaFor_LocalUsesLocalFactory verifies the local branch: it
// returns the shared ClientFactory's mapper/discovery unchanged, reports
// Generation == "local", and — per D1 — never touches the schema cache.
func TestTargetSchemaFor_LocalUsesLocalFactory(t *testing.T) {
	router := newRemoteTestRouter(nil)
	ctx := context.Background()

	for _, clusterID := range []string{"", "local"} {
		schema, err := router.TargetSchemaFor(ctx, clusterID, "alice", []string{"engineering"})
		if err != nil {
			t.Fatalf("TargetSchemaFor(%q) returned error: %v", clusterID, err)
		}
		if !schema.IsLocal {
			t.Errorf("IsLocal = false for %q; want true", clusterID)
		}
		if schema.Generation != "local" {
			t.Errorf("Generation = %q; want %q", schema.Generation, "local")
		}
		if schema.Mapper != router.LocalFactory().RESTMapper() {
			t.Error("Mapper is not identical to LocalFactory().RESTMapper()")
		}
		if schema.Discovery != router.LocalFactory().DiscoveryClient() {
			t.Error("Discovery is not identical to LocalFactory().DiscoveryClient()")
		}
	}

	if got := router.schemaCache.len(); got != 0 {
		t.Errorf("schemaCache.len() = %d after local-only calls; want 0 (local branch must not touch the cache)", got)
	}
}

// TestTargetSchemaFor_RemoteFailsClosedWhenStoreNil mirrors F#18 for the
// schema half: a non-local clusterID with no cluster store configured must
// hard-error, not silently resolve to the local mapper.
func TestTargetSchemaFor_RemoteFailsClosedWhenStoreNil(t *testing.T) {
	router := newRemoteTestRouter(nil)

	schema, err := router.TargetSchemaFor(context.Background(), "some-remote-id", "alice", nil)
	if err == nil {
		t.Fatal("TargetSchemaFor returned nil error for non-local clusterID with nil clusterStore; want fail-closed error")
	}
	if !strings.Contains(err.Error(), "some-remote-id") {
		t.Errorf("error = %q; want it to mention the cluster id", err.Error())
	}
	if schema != nil {
		t.Errorf("schema = %+v; want nil on error", schema)
	}
}

// TestTargetSchema_RemoteFailureReturnsNoLocalMapper is D6 mechanism 3: when
// the cluster store errors resolving a remote cluster, TargetSchemaFor must
// return a nil schema and a non-nil error — never fall back to a usable
// (local) mapper.
func TestTargetSchema_RemoteFailureReturnsNoLocalMapper(t *testing.T) {
	getter := &fakeClusterGetter{err: errors.New("boom: cluster store unavailable")}
	router := newRemoteTestRouter(getter)

	schema, err := router.TargetSchemaFor(context.Background(), "remote-1", "alice", nil)
	if err == nil {
		t.Fatal("TargetSchemaFor returned nil error when the cluster store errored; want an error")
	}
	if schema != nil {
		t.Errorf("schema = %+v; want nil on remote resolution failure (no silent local fallback)", schema)
	}
}

// TestTargetSchema_RemoteMapperIsNotLocalMapper is D6 mechanism 3's
// companion: on a *successful* remote resolve, the returned mapper/discovery
// must be pointer-distinct from the local factory's — "a CRD present only
// remotely resolves against that cluster."
func TestTargetSchema_RemoteMapperIsNotLocalMapper(t *testing.T) {
	rec := testClusterRecord(t, time.Now())
	getter := &fakeClusterGetter{record: rec}
	router := newRemoteTestRouter(getter)

	schema, err := router.TargetSchemaFor(context.Background(), "remote-1", "alice", []string{"engineering"})
	if err != nil {
		t.Fatalf("TargetSchemaFor returned error: %v", err)
	}
	if schema.IsLocal {
		t.Error("IsLocal = true for a remote cluster id")
	}
	if schema.Mapper == router.LocalFactory().RESTMapper() {
		t.Error("remote Mapper is pointer-equal to the local factory's RESTMapper; want a distinct instance")
	}
	if schema.Discovery == router.LocalFactory().DiscoveryClient() {
		t.Error("remote Discovery is pointer-equal to the local factory's DiscoveryClient; want a distinct instance")
	}
}

// TestTargetFor_ClientAndSchemaShareClusterID verifies that TargetFor's two
// halves — the ClientPair and the TargetSchema — resolve to the same
// normalized cluster id, for both the local and remote paths, so a caller
// can never end up with a remote client paired against a different
// cluster's schema (or vice versa).
func TestTargetFor_ClientAndSchemaShareClusterID(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		router := newRemoteTestRouter(nil)
		pair, schema, err := router.TargetFor(context.Background(), "", "alice", nil)
		if err != nil {
			t.Fatalf("TargetFor returned error: %v", err)
		}
		if pair.ClusterID != schema.ClusterID {
			t.Errorf("pair.ClusterID = %q, schema.ClusterID = %q; want equal", pair.ClusterID, schema.ClusterID)
		}
	})

	t.Run("remote", func(t *testing.T) {
		rec := testClusterRecord(t, time.Now())
		getter := &fakeClusterGetter{record: rec}
		router := newRemoteTestRouter(getter)

		pair, schema, err := router.TargetFor(context.Background(), "remote-1", "alice", []string{"engineering"})
		if err != nil {
			t.Fatalf("TargetFor returned error: %v", err)
		}
		if pair.ClusterID != schema.ClusterID {
			t.Errorf("pair.ClusterID = %q, schema.ClusterID = %q; want equal", pair.ClusterID, schema.ClusterID)
		}
	})
}

// TestEvictCluster_DropsSchemaCache verifies EvictCluster propagates to the
// schema cache alongside the existing client caches, so cluster deletion or
// re-registration cannot serve a stale (or cross-tenant) discovery/mapper
// pair from a cache entry that outlives the cluster record.
func TestEvictCluster_DropsSchemaCache(t *testing.T) {
	rec := testClusterRecord(t, time.Now())
	getter := &fakeClusterGetter{record: rec}
	router := newRemoteTestRouter(getter)

	if _, err := router.TargetSchemaFor(context.Background(), "remote-1", "alice", nil); err != nil {
		t.Fatalf("priming TargetSchemaFor returned error: %v", err)
	}
	if got := router.schemaCache.len(); got != 1 {
		t.Fatalf("schemaCache.len() = %d after priming; want 1", got)
	}

	router.EvictCluster("remote-1")

	if got := router.schemaCache.len(); got != 0 {
		t.Errorf("schemaCache.len() = %d after EvictCluster; want 0", got)
	}
}

// TestTargetSchema_ConcurrentColdCache fires 32 concurrent TargetSchemaFor
// calls for the same (cluster, identity) key against an empty cache. Run
// under `go test -race`: the schema cache's own locking must not race, and
// regardless of how many goroutines lose the cold-cache race, the cache must
// end up with exactly one entry for the shared key.
func TestTargetSchema_ConcurrentColdCache(t *testing.T) {
	rec := testClusterRecord(t, time.Now())
	getter := &fakeClusterGetter{record: rec}
	router := newRemoteTestRouter(getter)

	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := router.TargetSchemaFor(context.Background(), "remote-1", "alice", []string{"engineering"})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: TargetSchemaFor returned error: %v", i, err)
		}
	}
	if got := router.schemaCache.len(); got != 1 {
		t.Errorf("schemaCache.len() = %d after 32 concurrent cold-cache requests for the same key; want 1", got)
	}
}
