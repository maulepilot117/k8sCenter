package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBindings is a MetricsBindingReader backed by a map. A cluster absent
// from the map answers ErrNoMetricsBinding wrapped the way the store adapter
// wraps its own sentinel, so the tests prove errors.Is is what callers use.
type fakeBindings struct {
	mu       sync.Mutex
	bindings map[string]MetricsBinding
	err      error // returned for every read when set
	reads    atomic.Int32
}

func (f *fakeBindings) GetMetricsBinding(_ context.Context, clusterID string) (MetricsBinding, error) {
	f.reads.Add(1)
	if f.err != nil {
		return MetricsBinding{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bindings[clusterID]
	if !ok {
		return MetricsBinding{}, fmt.Errorf("store: metrics binding not found: %w", ErrNoMetricsBinding)
	}
	return b, nil
}

func (f *fakeBindings) set(clusterID string, b MetricsBinding) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bindings == nil {
		f.bindings = map[string]MetricsBinding{}
	}
	f.bindings[clusterID] = b
}

// fakePrometheus is an httptest Prometheus that answers every instant or
// range query with value, records the Authorization header of the last
// request, and counts requests.
type fakePrometheus struct {
	srv      *httptest.Server
	mu       sync.Mutex
	lastAuth string
	hits     atomic.Int32
}

func newFakePrometheus(t *testing.T, value string) *fakePrometheus {
	t.Helper()
	fp := &fakePrometheus{}
	fp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fp.hits.Add(1)
		fp.mu.Lock()
		fp.lastAuth = r.Header.Get("Authorization")
		fp.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"):
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,%q],[1700000060,%q]]}]}}`, value, value)
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/buildinfo"):
			fmt.Fprint(w, `{"status":"success","data":{"version":"2.53.0"}}`)
		default:
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,%q]}]}}`, value)
		}
	}))
	t.Cleanup(fp.srv.Close)
	return fp
}

func (fp *fakePrometheus) auth() string {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.lastAuth
}

// newTestResolver returns a resolver whose remote base transport may dial
// loopback, which the production strict transport refuses.
func newTestResolver(d *Discoverer, b MetricsBindingReader) *ClientResolver {
	r := NewClientResolver(d, b, testLogger())
	r.base = http.DefaultTransport
	return r
}

func localDiscoverer(t *testing.T, fp *fakePrometheus) *Discoverer {
	t.Helper()
	pc, err := NewPrometheusClientWithTransport(fp.srv.URL, http.DefaultTransport)
	if err != nil {
		t.Fatalf("local client: %v", err)
	}
	return &Discoverer{status: &MonitoringStatus{}, promClient: pc, logger: testLogger()}
}

func TestClientResolver_LocalUsesDiscoverer(t *testing.T) {
	local := newFakePrometheus(t, "1")
	d := localDiscoverer(t, local)
	r := newTestResolver(d, &fakeBindings{})

	for _, id := range []string{"", "local"} {
		pc, err := r.PrometheusFor(context.Background(), id)
		if err != nil {
			t.Fatalf("cluster %q: %v", id, err)
		}
		if pc != d.PrometheusClient() {
			t.Fatalf("cluster %q: want the Discoverer's client", id)
		}
	}
}

func TestClientResolver_LocalWithoutPrometheusIsUnavailable(t *testing.T) {
	r := newTestResolver(&Discoverer{status: &MonitoringStatus{}}, nil)
	if _, err := r.PrometheusFor(context.Background(), "local"); !errors.Is(err, ErrPrometheusUnavailable) {
		t.Fatalf("want ErrPrometheusUnavailable, got %v", err)
	}
	r = newTestResolver(nil, nil)
	if _, err := r.PrometheusFor(context.Background(), ""); !errors.Is(err, ErrPrometheusUnavailable) {
		t.Fatalf("nil Discoverer: want ErrPrometheusUnavailable, got %v", err)
	}
}

func TestClientResolver_RemoteNilReaderIsNoBinding(t *testing.T) {
	local := newFakePrometheus(t, "1")
	r := newTestResolver(localDiscoverer(t, local), nil)
	if _, err := r.PrometheusFor(context.Background(), "remote-1"); !errors.Is(err, ErrNoMetricsBinding) {
		t.Fatalf("want ErrNoMetricsBinding, got %v", err)
	}
	if local.hits.Load() != 0 {
		t.Fatal("a remote selection must never reach the local Prometheus")
	}
}

func TestClientResolver_RemoteWithoutBindingIsNoBinding(t *testing.T) {
	r := newTestResolver(nil, &fakeBindings{})
	if _, err := r.PrometheusFor(context.Background(), "remote-1"); !errors.Is(err, ErrNoMetricsBinding) {
		t.Fatalf("want ErrNoMetricsBinding, got %v", err)
	}
}

func TestClientResolver_RemoteReaderErrorPassesThrough(t *testing.T) {
	boom := errors.New("registry down")
	r := newTestResolver(nil, &fakeBindings{err: boom})
	_, err := r.PrometheusFor(context.Background(), "remote-1")
	if !errors.Is(err, boom) {
		t.Fatalf("want the reader's error, got %v", err)
	}
	if errors.Is(err, ErrNoMetricsBinding) {
		t.Fatal("a reader failure must not read as a missing binding")
	}
}

func TestClientResolver_RemoteSendsBearerToken(t *testing.T) {
	local := newFakePrometheus(t, "1")
	remote := newFakePrometheus(t, "42")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL, Token: "s3cret"})
	r := newTestResolver(localDiscoverer(t, local), b)

	pc, err := r.PrometheusFor(context.Background(), "remote-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, _, err := pc.Query(context.Background(), "up", time.Now()); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := remote.auth(); got != "Bearer s3cret" {
		t.Fatalf("Authorization = %q, want the bound bearer token", got)
	}
	if local.hits.Load() != 0 {
		t.Fatal("the local Prometheus must not be queried for a remote cluster")
	}
}

func TestClientResolver_RemoteWithoutTokenSendsNoAuthorization(t *testing.T) {
	remote := newFakePrometheus(t, "42")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL})
	r := newTestResolver(nil, b)

	pc, err := r.PrometheusFor(context.Background(), "remote-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, _, err := pc.Query(context.Background(), "up", time.Now()); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := remote.auth(); got != "" {
		t.Fatalf("Authorization = %q, want none", got)
	}
}

func TestClientResolver_CachesWithinTTLAndEvicts(t *testing.T) {
	remote := newFakePrometheus(t, "42")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL})
	r := newTestResolver(nil, b)
	now := time.Unix(1_700_000_000, 0)
	r.now = func() time.Time { return now }

	first, err := r.PrometheusFor(context.Background(), "remote-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	now = now.Add(r.TTL - time.Second)
	second, _ := r.PrometheusFor(context.Background(), "remote-1")
	if second != first || b.reads.Load() != 1 {
		t.Fatalf("within TTL: want the cached client and one read, got reads=%d same=%v", b.reads.Load(), second == first)
	}

	now = now.Add(2 * time.Second)
	third, _ := r.PrometheusFor(context.Background(), "remote-1")
	if third == first || b.reads.Load() != 2 {
		t.Fatalf("after TTL: want a fresh client and a second read, got reads=%d", b.reads.Load())
	}

	r.Evict("remote-1")
	fourth, _ := r.PrometheusFor(context.Background(), "remote-1")
	if fourth == third || b.reads.Load() != 3 {
		t.Fatalf("after Evict: want a fresh client and a third read, got reads=%d", b.reads.Load())
	}
}

func TestClientResolver_EvictDropsDeletedBinding(t *testing.T) {
	remote := newFakePrometheus(t, "42")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL})
	r := newTestResolver(nil, b)
	if _, err := r.PrometheusFor(context.Background(), "remote-1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	b.mu.Lock()
	delete(b.bindings, "remote-1")
	b.mu.Unlock()
	r.Evict("remote-1")

	if _, err := r.PrometheusFor(context.Background(), "remote-1"); !errors.Is(err, ErrNoMetricsBinding) {
		t.Fatalf("after delete + Evict: want ErrNoMetricsBinding, got %v", err)
	}
}

// blockingBindings parks every read until release is closed, so a test can
// Evict while a read is in flight.
type blockingBindings struct {
	started chan struct{}
	release chan struct{}
	binding MetricsBinding
	reads   atomic.Int32
}

func (b *blockingBindings) GetMetricsBinding(context.Context, string) (MetricsBinding, error) {
	if b.reads.Add(1) == 1 {
		close(b.started)
		<-b.release
	}
	return b.binding, nil
}

func TestClientResolver_EvictDuringReadIsNotUndone(t *testing.T) {
	remote := newFakePrometheus(t, "42")
	b := &blockingBindings{
		started: make(chan struct{}),
		release: make(chan struct{}),
		binding: MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL},
	}
	r := newTestResolver(nil, b)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.PrometheusFor(context.Background(), "remote-1")
	}()
	<-b.started
	r.Evict("remote-1") // the binding changed while the read above was in flight
	close(b.release)
	<-done

	if _, err := r.PrometheusFor(context.Background(), "remote-1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := b.reads.Load(); got != 2 {
		t.Fatalf("a read that began before Evict must not be cached; want 2 reads, got %d", got)
	}
}

func TestBearerRoundTripper_DoesNotMutateCallerRequest(t *testing.T) {
	remote := newFakePrometheus(t, "1")
	rt := newBearerRoundTripper(http.DefaultTransport, remote.srv.URL, "tok")
	req, _ := http.NewRequest(http.MethodGet, remote.srv.URL+"/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	resp.Body.Close()
	if req.Header.Get("Authorization") != "" {
		t.Fatal("the caller's request was mutated")
	}
	if remote.auth() != "Bearer tok" {
		t.Fatalf("Authorization = %q, want Bearer tok", remote.auth())
	}
}

func TestBearerRoundTripper_OnlyBoundHostGetsToken(t *testing.T) {
	bound := newFakePrometheus(t, "1")
	other := newFakePrometheus(t, "1")
	rt := newBearerRoundTripper(http.DefaultTransport, bound.srv.URL, "tok")
	req, _ := http.NewRequest(http.MethodGet, other.srv.URL+"/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	resp.Body.Close()
	if other.auth() != "" {
		t.Fatalf("a host other than the bound Prometheus received %q", other.auth())
	}
}

func TestProbePrometheus_Success(t *testing.T) {
	remote := newFakePrometheus(t, "1")
	if err := probePrometheus(context.Background(), http.DefaultTransport, remote.srv.URL, "tok"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if remote.auth() != "Bearer tok" {
		t.Fatalf("probe Authorization = %q, want Bearer tok", remote.auth())
	}
}

func TestProbePrometheus_NonSuccessStatusFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	if err := probePrometheus(context.Background(), http.DefaultTransport, srv.URL, ""); err == nil {
		t.Fatal("a 401 must fail the probe")
	}
}

func TestProbePrometheus_RedirectFails(t *testing.T) {
	target := newFakePrometheus(t, "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.srv.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	if err := probePrometheus(context.Background(), http.DefaultTransport, srv.URL, "tok"); err == nil {
		t.Fatal("a redirect must fail the probe rather than be followed")
	}
	if target.hits.Load() != 0 {
		t.Fatal("the probe followed a redirect")
	}
}

func TestProbePrometheus_TransportErrorFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if err := probePrometheus(context.Background(), http.DefaultTransport, url, ""); err == nil {
		t.Fatal("an unreachable server must fail the probe")
	}
}

// TestProbePrometheus_UsesStrictTransport proves the exported probe dials
// through the strict transport: a loopback Prometheus is refused.
func TestProbePrometheus_UsesStrictTransport(t *testing.T) {
	remote := newFakePrometheus(t, "1")
	if err := ProbePrometheus(context.Background(), remote.srv.URL, ""); err == nil {
		t.Fatal("the strict transport must refuse a loopback Prometheus")
	}
	if remote.hits.Load() != 0 {
		t.Fatal("the probe reached a loopback address")
	}
}

// TestClientResolver_ProductionTransportIsStrict proves a resolver built by
// NewClientResolver dials remote Prometheus through the strict transport.
func TestClientResolver_ProductionTransportIsStrict(t *testing.T) {
	remote := newFakePrometheus(t, "1")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL})
	r := NewClientResolver(nil, b, testLogger())
	pc, err := r.PrometheusFor(context.Background(), "remote-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, _, err := pc.Query(context.Background(), "up", time.Now()); err == nil {
		t.Fatal("the strict transport must refuse a loopback Prometheus")
	}
	if remote.hits.Load() != 0 {
		t.Fatal("the remote client reached a loopback address")
	}
}
