package resources

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// stubTrendProvider is a test double for the TrendProvider interface.
type stubTrendProvider struct {
	result DashboardTrends
	err    error
}

func (s stubTrendProvider) DashboardTrends(_ context.Context) (DashboardTrends, error) {
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
	if got.Nodes != nil || got.CPU != nil {
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
	if got.Nodes != nil {
		t.Fatalf("provider error: want empty series, got %+v", got)
	}
}

func TestHandleDashboardTrends_HappyPath(t *testing.T) {
	h, _ := testHandler(t)
	h.Trends = stubTrendProvider{result: DashboardTrends{
		Nodes:  []float64{3, 3, 4},
		CPU:    []float64{12.5, 18.0},
		Window: "1h0m0s",
		Step:   "2m0s",
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
