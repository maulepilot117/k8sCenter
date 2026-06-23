package resources

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// stubTrendProvider is a test double for the TrendProvider interface.
type stubTrendProvider struct {
	result DashboardTrends
	err    error
}

func (s stubTrendProvider) DashboardTrends(_ context.Context, _, _ time.Duration) (DashboardTrends, error) {
	return s.result, s.err
}

// decodeTrends pulls the DashboardTrends payload out of the {data:...} envelope.
func decodeTrends(t *testing.T, rr *httptest.ResponseRecorder) DashboardTrends {
	t.Helper()
	var resp struct {
		Data DashboardTrends `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	return resp.Data
}

func TestHandleDashboardTrends_NilProvider(t *testing.T) {
	h, _ := testHandler(t)
	// h.Trends is nil — monitoring unavailable. Must degrade to 200 + empty.
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-trends", "")
	rr := httptest.NewRecorder()

	h.HandleDashboardTrends(rr, req)

	if rr.Code != 200 {
		t.Fatalf("nil provider: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	got := decodeTrends(t, rr)
	if got.Nodes != nil || got.CPU != nil || got.NetworkRx != nil ||
		got.NetworkTx != nil {
		t.Fatalf("nil provider: want empty series, got %+v", got)
	}
}

func TestHandleDashboardTrends_ProviderError(t *testing.T) {
	h, _ := testHandler(t)
	h.Trends = stubTrendProvider{err: context.DeadlineExceeded}
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-trends", "")
	rr := httptest.NewRecorder()

	h.HandleDashboardTrends(rr, req)

	// A Prometheus error must not surface as a 5xx — graceful degradation.
	if rr.Code != 200 {
		t.Fatalf("provider error: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	got := decodeTrends(t, rr)
	if got.Nodes != nil || got.NetworkRx != nil || got.NetworkTx != nil {
		t.Fatalf("provider error: want empty series, got %+v", got)
	}
}

func TestHandleDashboardTrends_HappyPath(t *testing.T) {
	h, _ := testHandler(t)
	h.Trends = stubTrendProvider{result: DashboardTrends{
		Nodes:     []float64{3, 3, 4},
		CPU:       []float64{12.5, 18.0},
		NetworkRx: []float64{120, 340, 210},
		NetworkTx: []float64{80, 95, 110},
		Window:    "1h0m0s",
		Step:      "2m0s",
	}}
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-trends", "")
	rr := httptest.NewRecorder()

	h.HandleDashboardTrends(rr, req)

	if rr.Code != 200 {
		t.Fatalf("happy path: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	got := decodeTrends(t, rr)
	if len(got.Nodes) != 3 || got.Nodes[2] != 4 {
		t.Fatalf("happy path: want nodes [3 3 4], got %v", got.Nodes)
	}
	if len(got.CPU) != 2 || got.Window != "1h0m0s" {
		t.Fatalf("happy path: want cpu len 2 + window, got %+v", got)
	}
	if len(got.NetworkRx) != 3 || got.NetworkRx[1] != 340 {
		t.Fatalf("happy path: want networkRx [120 340 210], got %v", got.NetworkRx)
	}
	if len(got.NetworkTx) != 3 || got.NetworkTx[2] != 110 {
		t.Fatalf("happy path: want networkTx [80 95 110], got %v", got.NetworkTx)
	}
}

func TestDashboardTrendRange(t *testing.T) {
	// Known tabs map to their exact (window, step); unknown/empty falls back to
	// the 1h pair. Asserting step exactly (not just a bound) catches an
	// accidental window/step swap, which a `step < window` check would miss.
	cases := map[string]struct{ window, step time.Duration }{
		"15m": {15 * time.Minute, 30 * time.Second},
		"1h":  {time.Hour, 2 * time.Minute},
		"6h":  {6 * time.Hour, 12 * time.Minute},
		"24h": {24 * time.Hour, 48 * time.Minute},
		"":    {time.Hour, 2 * time.Minute},
		"99d": {time.Hour, 2 * time.Minute},
	}
	for in, want := range cases {
		window, step := dashboardTrendRange(in)
		if window != want.window || step != want.step {
			t.Errorf("range %q: want (%s, %s), got (%s, %s)",
				in, want.window, want.step, window, step)
		}
	}
}

// TestDashboardTrendRange_FrontendTabsResolve guards the implicit contract that
// every time-range tab the frontend renders (frontend/islands/DashboardV2.tsx
// TIME_RANGES) resolves to a non-default window. If a tab is added there without
// a matching dashboardTrendWindows entry, it silently falls back to 1h — this
// test makes that regression visible on the backend side.
func TestDashboardTrendRange_FrontendTabsResolve(t *testing.T) {
	frontendTabs := []string{"15m", "1h", "6h", "24h"}
	for _, tab := range frontendTabs {
		if _, ok := dashboardTrendWindows[tab]; !ok {
			t.Errorf("frontend tab %q has no dashboardTrendWindows entry; it would silently fall back to 1h", tab)
		}
	}
}

func TestHandleDashboardTrends_RemoteClusterRejected(t *testing.T) {
	h, _ := testHandler(t)
	h.Trends = stubTrendProvider{result: DashboardTrends{Nodes: []float64{1, 2}}}
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-trends", "")
	// Non-local cluster: trends are informer/Prometheus-local only.
	req = req.WithContext(middleware.WithClusterID(req.Context(), "remote-1"))
	rr := httptest.NewRecorder()

	h.HandleDashboardTrends(rr, req)

	if rr.Code != 400 {
		t.Fatalf("remote cluster: want 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}
