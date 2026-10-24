package loki

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
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

// Covers AE1: under a remote cluster, every Loki REST read is refused with
// the remote-unsupported reason and the local Loki is never asked.
func TestLokiREST_RemoteClusterRefused(t *testing.T) {
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

// TestLokiREST_RemoteRefusedWithoutLocalLoki verifies the remote refusal
// comes before the local-availability check: on a server with no Loki of
// its own, a remote request still gets the remote-unsupported reason, not
// "Loki is not available", which would wrongly suggest installing it.
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

// TestLokiREST_RemoteStatusNotDetected verifies the status probe reports Loki
// as not detected for a remote cluster, with the reason, rather than the
// local discovery result. Web and mobile both treat detected:false as "Loki
// unavailable", so the 200 keeps their existing degraded state.
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
