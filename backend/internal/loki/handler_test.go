package loki

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kfake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// lokiTestHandler returns a Handler wired to a fake Loki that answers every
// path, plus a counter of the requests it saw after discovery's /ready probe.
// The counter is how a test proves a remote request never reached the local
// Loki.
func lokiTestHandler(t *testing.T) (*Handler, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	discovered := false
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if discovered {
			calls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[]}}`)
	}))
	t.Cleanup(fake.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disc := NewDiscoverer(nil, config.LokiConfig{URL: fake.URL}, logger)
	disc.Discover(context.Background())
	if disc.Client() == nil {
		t.Fatal("fake Loki was not discovered")
	}
	discovered = true

	return &Handler{
		Discoverer:    disc,
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        logger,
	}, &calls
}

// serve runs one request through the handler as an admin targeting cluster.
func serve(h http.HandlerFunc, cluster, target string, params map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := auth.ContextWithUser(r.Context(), &auth.User{Username: "admin", Roles: []string{"admin"}})
	ctx = middleware.WithClusterID(ctx, cluster)
	if len(params) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range params {
			rctx.URLParams.Add(k, v)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	w := httptest.NewRecorder()
	h(w, r.WithContext(ctx))
	return w
}

// Without a cluster router, a remote cluster is unsupported: every Loki
// REST read is refused with that reason and the local Loki is never asked.
func TestLokiREST_RemoteWithoutRouterUnsupported(t *testing.T) {
	h, calls := lokiTestHandler(t)
	cases := []struct {
		name   string
		fn     http.HandlerFunc
		target string
		params map[string]string
	}{
		{"query", h.HandleQuery, `/logs/query?query={app="web"}`, nil},
		{"labels", h.HandleLabels, "/logs/labels", nil},
		{"label values", h.HandleLabelValues, "/logs/labels/pod/values", map[string]string{"name": "pod"}},
		{"volume", h.HandleVolume, `/logs/volume?query={app="web"}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serve(tc.fn, "remote-cluster-99", tc.target, tc.params)
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", w.Code)
			}
			var body struct {
				Error struct {
					Reason string `json:"reason"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error.Reason != "unsupported_platform" {
				t.Fatalf("reason = %q, want unsupported_platform", body.Error.Reason)
			}
		})
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("local Loki received %d requests for a remote cluster, want 0", n)
	}
}

// TestLokiREST_RemoteRefusedWithoutLocalLoki verifies a remote request is
// never answered from the local cluster's availability: on a server with no
// Loki of its own, it still gets the remote reason, not the local "not
// detected", which would wrongly suggest installing Loki locally.
func TestLokiREST_RemoteRefusedWithoutLocalLoki(t *testing.T) {
	h := &Handler{
		Discoverer:    NewDiscoverer(nil, config.LokiConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for name, fn := range map[string]http.HandlerFunc{
		"query":        h.HandleQuery,
		"labels":       h.HandleLabels,
		"label values": h.HandleLabelValues,
		"volume":       h.HandleVolume,
	} {
		if w := serve(fn, "remote-cluster-99", `/logs/x?query={app="web"}`, map[string]string{"name": "pod"}); w.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501 before the local-availability check", name, w.Code)
		}
	}
}

// TestLokiREST_RemoteStatusNotDetected verifies the status probe of a remote
// cluster with no reachable Loki reports not detected, with the reason,
// rather than the local discovery result. Web and mobile both treat
// detected:false as "Loki unavailable", so the 200 keeps their degraded
// state.
func TestLokiREST_RemoteStatusNotDetected(t *testing.T) {
	h, _ := lokiTestHandler(t)
	w := serve(h.HandleStatus, "remote-cluster-99", "/logs/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Data LokiStatus `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.Detected || body.Data.URL != "" {
		t.Fatalf("remote status leaked the local discovery result: %+v", body.Data)
	}
	if body.Data.Reason != "unsupported_platform" {
		t.Fatalf("reason = %q, want unsupported_platform", body.Data.Reason)
	}
}

// TestLokiREST_LocalClusterServed verifies the local cluster still reaches
// Loki: the refusal applies only to remote targets.
func TestLokiREST_LocalClusterServed(t *testing.T) {
	h, calls := lokiTestHandler(t)

	if w := serve(h.HandleQuery, "local", `/logs/query?query={app="web"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("local query status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if calls.Load() == 0 {
		t.Fatal("local query never reached Loki")
	}

	w := serve(h.HandleStatus, "local", "/logs/status", nil)
	var body struct {
		Data LokiStatus `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Data.Detected || body.Data.Reason != "" {
		t.Fatalf("local status = %+v, want detected with no reason", body.Data)
	}
}

// --- per-cluster resolution (U12) ---

// remoteLokiServer answers every Loki read with one remote log line, and
// counts the requests it gets.
func remoteLokiServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/query_range"):
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"shop"},"values":[["1","remote-line"]]}]}}`)
		case strings.HasSuffix(r.URL.Path, "/volume_range"):
			_, _ = io.WriteString(w, `{"status":"success","data":{"result":[]}}`)
		default:
			_, _ = io.WriteString(w, `{"status":"success","data":["namespace"]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// resolvingHandler serves the local cluster from the fake local Loki and a
// remote cluster from objs, with tunnels reaching remoteAddr.
func resolvingHandler(t *testing.T, remoteAddr string, objs ...kruntime.Object) (*Handler, *atomic.Int64, *fakeForwarder) {
	t.Helper()
	h, localCalls := lokiTestHandler(t)
	r, _, fwd, _ := remoteResolver(t, remoteAddr, objs...)
	r.local = h.Discoverer
	h.Resolver = r
	return h, localCalls, fwd
}

// Covers AE1: a remote cluster with no Loki answers loki_not_found on every
// read, and the local Loki is never asked.
func TestLokiREST_RemoteWithoutLokiIsNotFound(t *testing.T) {
	h, localCalls, fwd := resolvingHandler(t, "127.0.0.1:1")
	for name, fn := range map[string]http.HandlerFunc{
		"query":        h.HandleQuery,
		"labels":       h.HandleLabels,
		"label values": h.HandleLabelValues,
		"volume":       h.HandleVolume,
	} {
		w := serve(fn, remoteID, `/logs/x?query={app="web"}`, map[string]string{"name": "pod"})
		if w.Code != http.StatusNotFound || errorReason(t, w) != ReasonLokiNotFound {
			t.Errorf("%s: %d %s, want 404 loki_not_found", name, w.Code, w.Body.String())
		}
	}
	if n := localCalls.Load(); n != 0 {
		t.Fatalf("local Loki received %d requests for a remote cluster", n)
	}
	if len(fwd.called()) != 0 {
		t.Fatal("a tunnel was opened with no Loki Service")
	}
}

// A remote read goes through a tunnel to the remote Loki and returns its
// lines; each request opens its own tunnel and closes it.
func TestLokiREST_RemoteQueryThroughTunnel(t *testing.T) {
	remote, remoteCalls := remoteLokiServer(t)
	h, localCalls, fwd := resolvingHandler(t, remote.Listener.Addr().String(), realLoki()...)

	w := serve(h.HandleQuery, remoteID, `/logs/query?query={namespace="shop"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "remote-line") {
		t.Fatalf("remote query = %d %s, want the remote Loki's line", w.Code, w.Body.String())
	}
	if remoteCalls.Load() != 1 || localCalls.Load() != 0 {
		t.Fatalf("remote got %d, local got %d requests; want 1 and 0", remoteCalls.Load(), localCalls.Load())
	}
	if len(fwd.called()) != 1 || !fwd.tunnel.closed.Load() {
		t.Fatalf("tunnels opened %d, closed %v; want one, closed after the request", len(fwd.called()), fwd.tunnel.closed.Load())
	}
	for name, fn := range map[string]http.HandlerFunc{
		"labels": h.HandleLabels, "label values": h.HandleLabelValues, "volume": h.HandleVolume,
	} {
		fwd.tunnel = newFakeTunnel(remote.Listener.Addr().String())
		if w := serve(fn, remoteID, `/logs/x?query={app="web"}`, map[string]string{"name": "pod"}); w.Code != http.StatusOK {
			t.Errorf("%s = %d %s, want 200", name, w.Code, w.Body.String())
		}
		if !fwd.tunnel.closed.Load() {
			t.Errorf("%s left its tunnel open after the request", name)
		}
	}
}

// Covers AE2 at the API: a port-forward refusal answers forbidden naming
// Loki's namespace.
func TestLokiREST_RemotePortForwardForbidden(t *testing.T) {
	h, _, fwd := resolvingHandler(t, "127.0.0.1:1", realLoki()...)
	fwd.err = apierrors.NewForbidden(schema.GroupResource{Resource: "pods/portforward"}, "loki-gateway-0", errors.New("denied"))
	w := serve(h.HandleQuery, remoteID, `/logs/query?query={app="web"}`, nil)
	if w.Code != http.StatusForbidden || errorReason(t, w) != string(k8s.ReasonForbidden) {
		t.Fatalf("got %d %s, want 403 forbidden", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "namespace monitoring") {
		t.Fatalf("body %s does not name Loki's namespace", w.Body.String())
	}
}

// A Loki that answers 401 over the port-forward path, which sends no
// credentials, says the cluster needs a configured URL and token.
func TestLokiREST_PortForwardUnauthorizedNamesBinding(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(refusing.Close)
	h, _, _ := resolvingHandler(t, refusing.Listener.Addr().String(), realLoki()...)
	w := serve(h.HandleLabels, remoteID, "/logs/labels", nil)
	if w.Code != http.StatusBadGateway || errorReason(t, w) != string(k8s.ReasonCredentialsInvalid) {
		t.Fatalf("got %d %s, want 502 credentials_invalid", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "configure a Loki URL and token") {
		t.Fatalf("body %s does not point at the binding", w.Body.String())
	}
}

// The remote status names the namespace and Service the port-forward path
// chose, or the configured URL without any userinfo.
func TestLokiREST_RemoteStatusReportsLocation(t *testing.T) {
	h, _, _ := resolvingHandler(t, "127.0.0.1:1", append(realLoki(), decoyLoki()...)...)
	st := statusOf(t, serve(h.HandleStatus, remoteID, "/logs/status", nil))
	if !st.Detected || st.DetectedVia != string(SourcePortForward) || st.Namespace != "monitoring" || st.Service != "loki-gateway" {
		t.Fatalf("status = %+v, want monitoring/loki-gateway over port-forward", st)
	}

	h.Resolver.bindings = &fakeBindings{binding: store.LokiBinding{URL: "https://bob:hunter2@loki.example.com/base"}}
	st = statusOf(t, serve(h.HandleStatus, remoteID, "/logs/status", nil))
	if !st.Detected || st.DetectedVia != string(SourceBinding) || st.URL != "https://loki.example.com/base" {
		t.Fatalf("status = %+v, want the binding URL without userinfo", st)
	}

	h.Resolver.bindings = nil
	h.Resolver.Evict(remoteID)
	h.Resolver.clients.(*fakeClients).cs = kfake.NewClientset()
	st = statusOf(t, serve(h.HandleStatus, remoteID, "/logs/status", nil))
	if st.Detected || st.Reason != ReasonLokiNotFound || st.Message == "" {
		t.Fatalf("status = %+v, want not detected with loki_not_found and its message", st)
	}
}

func errorReason(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return body.Error.Reason
}

func statusOf(t *testing.T, w *httptest.ResponseRecorder) LokiStatus {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Data LokiStatus `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data
}

// A local Loki failure keeps the answer it always had: a 502 with the fixed
// message and no unavailable reason, whatever the failure was.
func TestLokiREST_LocalFailureKeepsFixedAnswer(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disc := NewDiscoverer(nil, config.LokiConfig{URL: failing.URL}, logger)
	disc.Discover(context.Background())
	if disc.Client() == nil {
		t.Fatal("failing Loki was not discovered")
	}
	h := &Handler{Discoverer: disc, Logger: logger}

	for name, tc := range map[string]struct {
		fn      http.HandlerFunc
		message string
	}{
		"query":        {h.HandleQuery, "Log query failed"},
		"labels":       {h.HandleLabels, "Label query failed"},
		"label values": {h.HandleLabelValues, "Label values query failed"},
		"volume":       {h.HandleVolume, "Volume query failed"},
	} {
		w := serve(tc.fn, "local", `/logs/x?query={app="web"}`, map[string]string{"name": "pod"})
		if w.Code != http.StatusBadGateway || errorReason(t, w) != "" || !strings.Contains(w.Body.String(), tc.message) {
			t.Errorf("%s = %d %s, want 502 %q with no reason", name, w.Code, w.Body.String(), tc.message)
		}
		if strings.Contains(w.Body.String(), "retryable") {
			t.Errorf("%s answered %s, want no retryable flag", name, w.Body.String())
		}
	}
}

// A remote query that runs out its time is a failed query, not an
// unavailable Loki: a slow query does not mean Loki is down.
func TestLokiREST_RemoteTimeoutIsFailedQuery(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	h, _, _ := resolvingHandler(t, slow.Listener.Addr().String(), realLoki()...)

	r := httptest.NewRequest(http.MethodGet, `/logs/query?query={namespace="shop"}`, nil)
	ctx := auth.ContextWithUser(r.Context(), &auth.User{Username: "admin", Roles: []string{"admin"}})
	ctx, cancel := context.WithTimeout(middleware.WithClusterID(ctx, remoteID), 300*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	h.HandleQuery(w, r.WithContext(ctx))
	if w.Code != http.StatusBadGateway || errorReason(t, w) != "" || !strings.Contains(w.Body.String(), "Log query failed") {
		t.Fatalf("got %d %s, want 502 Log query failed with no unavailable reason", w.Code, w.Body.String())
	}
}
