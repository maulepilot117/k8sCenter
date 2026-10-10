package monitoring

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/common/model"
)

func sample(v float64) model.SamplePair {
	return model.SamplePair{Timestamp: 0, Value: model.SampleValue(v)}
}

func TestParseMatrixSeries_Nil(t *testing.T) {
	if got := parseMatrixSeries(nil); got != nil {
		t.Fatalf("nil value: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_WrongType(t *testing.T) {
	// A range query that resolved to a Vector (instant) instead of a Matrix.
	if got := parseMatrixSeries(model.Vector{}); got != nil {
		t.Fatalf("vector value: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_EmptyMatrix(t *testing.T) {
	if got := parseMatrixSeries(model.Matrix{}); got != nil {
		t.Fatalf("empty matrix: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_EmptyValues(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{}}}
	if got := parseMatrixSeries(m); got != nil {
		t.Fatalf("series with no samples: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_HappyPath(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		sample(3), sample(5), sample(4),
	}}}
	got := parseMatrixSeries(m)
	want := []float64{3, 5, 4}
	if len(got) != len(want) {
		t.Fatalf("length: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: want %v, got %v", i, want[i], got[i])
		}
	}
}

func TestParseMatrixSeries_FirstSeriesOnly(t *testing.T) {
	// Scalar-aggregate queries return exactly one series; if a future query
	// returns multiple, we intentionally take only the first. Pin that so the
	// behavior is a conscious choice, not a silent surprise.
	m := model.Matrix{
		&model.SampleStream{Values: []model.SamplePair{sample(1), sample(2)}},
		&model.SampleStream{Values: []model.SamplePair{sample(9), sample(9)}},
	}
	got := parseMatrixSeries(m)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("want first series [1 2], got %v", got)
	}
}

func TestParseMatrixSeries_DropsNonFinite(t *testing.T) {
	// Prometheus can emit NaN/±Inf (div-by-zero, counter resets). These must be
	// dropped — encoding/json fails on them after the 200 header is sent, which
	// would blank every sparkline.
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		sample(10),
		sample(math.NaN()),
		sample(20),
		sample(math.Inf(1)),
		sample(math.Inf(-1)),
		sample(30),
	}}}
	got := parseMatrixSeries(m)
	want := []float64{10, 20, 30}
	if len(got) != len(want) {
		t.Fatalf("length: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: want %v, got %v", i, want[i], got[i])
		}
	}
}

func TestParseMatrixSeries_AllNonFinite(t *testing.T) {
	// A series of only non-finite samples collapses to nil — the frontend then
	// renders no sparkline rather than a broken one.
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		sample(math.NaN()), sample(math.Inf(1)),
	}}}
	if got := parseMatrixSeries(m); got != nil {
		t.Fatalf("all-non-finite: want nil, got %v", got)
	}
}

// trendsPrometheus is an https Prometheus whose range queries fail with a
// 500 when fail(query) is true and otherwise answer a two-point series.
func trendsPrometheus(t *testing.T, fail func(query string) bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if fail(r.Form.Get("query")) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"status":"error","errorType":"internal","error":"boom"}`)
			return
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"7"],[1700000060,"8"]]}]}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func remoteTrendsAdapter(t *testing.T, promURL string) *UtilizationAdapter {
	t.Helper()
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: promURL, Token: "tok"})
	return &UtilizationAdapter{Resolver: newTestResolver(nil, b)}
}

func TestDashboardTrends_RemoteAllQueriesFailIsError(t *testing.T) {
	srv, hits := trendsPrometheus(t, func(string) bool { return true })
	a := remoteTrendsAdapter(t, srv.URL)

	_, err := a.DashboardTrends(context.Background(), "remote-1", time.Hour, 2*time.Minute)
	if !errors.Is(err, ErrAllTrendQueriesFailed) {
		t.Fatalf("want ErrAllTrendQueriesFailed, got %v", err)
	}
	if errors.Is(err, ErrNoMetricsBinding) {
		t.Fatal("a failed query must not read as a missing binding")
	}
	if got := int(hits.Load()); got != len(trendQueries) {
		t.Fatalf("want every trend query attempted (%d), got %d", len(trendQueries), got)
	}
}

func TestDashboardTrends_RemotePartialFailureKeepsSurvivingSeries(t *testing.T) {
	srv, _ := trendsPrometheus(t, func(q string) bool { return strings.Contains(q, "kube_") })
	a := remoteTrendsAdapter(t, srv.URL)

	got, err := a.DashboardTrends(context.Background(), "remote-1", time.Hour, 2*time.Minute)
	if err != nil {
		t.Fatalf("a partial failure must not fail the request: %v", err)
	}
	if len(got.Nodes) != 0 || len(got.Pods) != 0 || len(got.Services) != 0 {
		t.Fatalf("failed series must be empty, got nodes=%v pods=%v services=%v", got.Nodes, got.Pods, got.Services)
	}
	if len(got.CPU) != 2 || len(got.Memory) != 2 || len(got.NetworkRx) != 2 || len(got.NetworkTx) != 2 {
		t.Fatalf("surviving series must be kept, got %+v", got)
	}
}

func TestDashboardTrends_LocalAllQueriesFailIsEmptyNotError(t *testing.T) {
	srv, _ := trendsPrometheus(t, func(string) bool { return true })
	pc, err := NewPrometheusClientWithTransport(srv.URL, testTransport())
	if err != nil {
		t.Fatalf("local client: %v", err)
	}
	d := &Discoverer{status: &MonitoringStatus{}, promClient: pc, logger: testLogger()}
	a := &UtilizationAdapter{Resolver: newTestResolver(d, nil)}

	got, err := a.DashboardTrends(context.Background(), "local", time.Hour, 2*time.Minute)
	if err != nil {
		t.Fatalf("the local cluster keeps its empty-series answer, got %v", err)
	}
	if got.Nodes != nil || got.Pods != nil || got.Services != nil || got.CPU != nil ||
		got.Memory != nil || got.NetworkRx != nil || got.NetworkTx != nil {
		t.Fatalf("want empty series, got %+v", got)
	}
	if got.Window != "1h0m0s" || got.Step != "2m0s" {
		t.Fatalf("window/step = %q/%q", got.Window, got.Step)
	}
}
