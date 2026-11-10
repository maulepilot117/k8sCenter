package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/common/model"

	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// gridStart is an arbitrary millisecond-aligned query start for the parse
// tests; gridStep and gridSteps describe a 6m window at a 2m step.
var gridStart = time.UnixMilli(1_700_000_000_000)

const (
	gridStep  = 2 * time.Minute
	gridSteps = 4
)

// at is a sample taken on grid step k, offset by skewMs milliseconds.
func at(k int, v float64, skewMs int64) model.SamplePair {
	ts := gridStart.UnixMilli() + int64(k)*gridStep.Milliseconds() + skewMs
	return model.SamplePair{Timestamp: model.Time(ts), Value: model.SampleValue(v)}
}

func parseGrid(m model.Value) resources.TrendSeries {
	return parseMatrixSeries(m, gridStart, gridStep, gridSteps)
}

// assertSeries compares a series against want, where NaN in want means "a
// gap at this step".
func assertSeries(t *testing.T, name string, got resources.TrendSeries, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length want %d, got %d (%v)", name, len(want), len(got), got)
	}
	for i := range want {
		if math.IsNaN(want[i]) {
			if !math.IsNaN(got[i]) {
				t.Fatalf("%s: index %d: want a gap, got %v (%v)", name, i, got[i], got)
			}
			continue
		}
		if got[i] != want[i] {
			t.Fatalf("%s: index %d: want %v, got %v (%v)", name, i, want[i], got[i], got)
		}
	}
}

func TestTrendGridSteps(t *testing.T) {
	// Every tab the handler serves is 30 steps wide, so 31 grid entries.
	for _, c := range []struct{ window, step time.Duration }{
		{15 * time.Minute, 30 * time.Second},
		{time.Hour, 2 * time.Minute},
		{6 * time.Hour, 12 * time.Minute},
		{24 * time.Hour, 48 * time.Minute},
	} {
		if got := trendGridSteps(c.window, c.step); got != 31 {
			t.Errorf("%v/%v: want 31 steps, got %d", c.window, c.step, got)
		}
	}
	if got := trendGridSteps(time.Hour, 0); got != 0 {
		t.Errorf("zero step: want 0, got %d", got)
	}
}

func TestParseMatrixSeries_Nil(t *testing.T) {
	if got := parseGrid(nil); got != nil {
		t.Fatalf("nil value: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_WrongType(t *testing.T) {
	// A range query that resolved to a Vector (instant) instead of a Matrix.
	if got := parseGrid(model.Vector{}); got != nil {
		t.Fatalf("vector value: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_EmptyMatrix(t *testing.T) {
	if got := parseGrid(model.Matrix{}); got != nil {
		t.Fatalf("empty matrix: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_EmptyValues(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{}}}
	if got := parseGrid(m); got != nil {
		t.Fatalf("series with no samples: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_FullGrid(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		at(0, 3, 0), at(1, 5, 0), at(2, 4, 0), at(3, 6, 0),
	}}}
	assertSeries(t, "full", parseGrid(m), []float64{3, 5, 4, 6})
}

func TestParseMatrixSeries_MissingMiddleStepKeepsLaterSamplesInPlace(t *testing.T) {
	// Prometheus leaves out step 1. Reading the result by position would
	// return [3 4 6] and the chart would draw 3 one step late; by timestamp the
	// gap stays at step 1 and every other sample stays on its own step.
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		at(0, 3, 0), at(2, 4, 0), at(3, 6, 0),
	}}}
	assertSeries(t, "gap", parseGrid(m), []float64{3, math.NaN(), 4, 6})
}

func TestParseMatrixSeries_MissingLeadingAndTrailingSteps(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		at(1, 5, 0), at(2, 4, 0),
	}}}
	assertSeries(t, "edges", parseGrid(m), []float64{math.NaN(), 5, 4, math.NaN()})
}

func TestParseMatrixSeries_SnapsMillisecondSkewToItsStep(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		at(0, 1, 1), at(1, 2, -1), at(3, 4, 0),
	}}}
	assertSeries(t, "skew", parseGrid(m), []float64{1, 2, math.NaN(), 4})
}

func TestParseMatrixSeries_IgnoresSamplesOffTheGrid(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		at(-1, 99, 0), at(0, 1, 0), at(gridSteps, 99, 0),
	}}}
	assertSeries(t, "bounds", parseGrid(m), []float64{1, math.NaN(), math.NaN(), math.NaN()})
}

func TestParseMatrixSeries_FirstSeriesOnly(t *testing.T) {
	// Scalar-aggregate queries return exactly one series; if a future query
	// returns multiple, we intentionally take only the first. Pin that so the
	// behavior is a conscious choice, not a silent surprise.
	m := model.Matrix{
		&model.SampleStream{Values: []model.SamplePair{at(0, 1, 0), at(1, 2, 0)}},
		&model.SampleStream{Values: []model.SamplePair{at(0, 9, 0), at(1, 9, 0)}},
	}
	assertSeries(t, "first", parseGrid(m), []float64{1, 2, math.NaN(), math.NaN()})
}

func TestParseMatrixSeries_NonFiniteSampleIsAGap(t *testing.T) {
	// Prometheus can emit NaN/±Inf (div-by-zero, counter resets). Each becomes
	// a gap on its own step, which TrendSeries encodes as null.
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		at(0, 10, 0),
		at(1, math.NaN(), 0),
		at(2, math.Inf(1), 0),
		at(3, 30, 0),
	}}}
	assertSeries(t, "non-finite", parseGrid(m), []float64{10, math.NaN(), math.NaN(), 30})
}

func TestParseMatrixSeries_AllNonFinite(t *testing.T) {
	// A series of only non-finite samples collapses to nil — the frontend then
	// renders no sparkline rather than a row of gaps.
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		at(0, math.NaN(), 0), at(1, math.Inf(-1), 0),
	}}}
	if got := parseGrid(m); got != nil {
		t.Fatalf("all-non-finite: want nil, got %v", got)
	}
}

// gridAnswer is what a fake Prometheus returns for one range query: the value
// for each grid step it has a sample for. Steps not in the map are left out
// of the result, as Prometheus does for a step with no value.
type gridAnswer map[int]string

// trendsPrometheus is an https Prometheus that answers each range query on
// the grid the request asked for. answer(query) returning nil fails that
// query with a 500.
func trendsPrometheus(t *testing.T, answer func(query string) gridAnswer) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		steps := answer(r.Form.Get("query"))
		if steps == nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"status":"error","errorType":"internal","error":"boom"}`)
			return
		}
		start, err := strconv.ParseFloat(r.Form.Get("start"), 64)
		if err != nil {
			t.Errorf("start %q: %v", r.Form.Get("start"), err)
		}
		step, err := strconv.ParseFloat(r.Form.Get("step"), 64)
		if err != nil {
			t.Errorf("step %q: %v", r.Form.Get("step"), err)
		}
		// Prometheus resolves the start to the millisecond, and so does this.
		startMs := int64(math.Round(start * 1000))
		stepMs := int64(math.Round(step * 1000))
		ks := make([]int, 0, len(steps))
		for k := range steps {
			ks = append(ks, k)
		}
		sort.Ints(ks)
		pairs := make([]string, 0, len(ks))
		for _, k := range ks {
			ts := float64(startMs+int64(k)*stepMs) / 1000
			pairs = append(pairs, fmt.Sprintf(`[%s,%q]`, strconv.FormatFloat(ts, 'f', -1, 64), steps[k]))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[%s]}]}}`, strings.Join(pairs, ","))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// twoSteps answers a query with samples on the first two grid steps.
var twoSteps = gridAnswer{0: "7", 1: "8"}

func remoteTrendsAdapter(t *testing.T, promURL string) *UtilizationAdapter {
	t.Helper()
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: promURL, Token: "tok"})
	return &UtilizationAdapter{Resolver: newTestResolver(nil, b)}
}

// localTrendsAdapter is a local-cluster adapter over the given Prometheus.
func localTrendsAdapter(t *testing.T, promURL string) *UtilizationAdapter {
	t.Helper()
	pc, err := NewPrometheusClientWithTransport(promURL, testTransport())
	if err != nil {
		t.Fatalf("local client: %v", err)
	}
	d := &Discoverer{status: &MonitoringStatus{}, promClient: pc, logger: testLogger()}
	return &UtilizationAdapter{Resolver: newTestResolver(d, nil)}
}

func TestDashboardTrends_RemoteAllQueriesFailIsError(t *testing.T) {
	srv, hits := trendsPrometheus(t, func(string) gridAnswer { return nil })
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
	srv, _ := trendsPrometheus(t, func(q string) gridAnswer {
		if strings.Contains(q, "kube_") {
			return nil
		}
		return twoSteps
	})
	a := remoteTrendsAdapter(t, srv.URL)

	got, err := a.DashboardTrends(context.Background(), "remote-1", time.Hour, 2*time.Minute)
	if err != nil {
		t.Fatalf("a partial failure must not fail the request: %v", err)
	}
	if len(got.Nodes) != 0 || len(got.Pods) != 0 || len(got.Services) != 0 {
		t.Fatalf("failed series must be empty, got nodes=%v pods=%v services=%v", got.Nodes, got.Pods, got.Services)
	}
	// Two samples on a one-hour, two-minute grid: 31 entries, the first two set.
	for name, s := range map[string]resources.TrendSeries{
		"cpu": got.CPU, "memory": got.Memory, "networkRx": got.NetworkRx, "networkTx": got.NetworkTx,
	} {
		if len(s) != 31 || s[0] != 7 || s[1] != 8 {
			t.Fatalf("surviving series %s must be kept on its grid, got %v", name, s)
		}
	}
}

func TestDashboardTrends_LocalAllQueriesFailIsEmptyNotError(t *testing.T) {
	srv, _ := trendsPrometheus(t, func(string) gridAnswer { return nil })
	a := localTrendsAdapter(t, srv.URL)

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

func TestDashboardTrends_GapsKeepEverySampleOnItsStep(t *testing.T) {
	// A 6m window at a 2m step is four grid steps. CPU loses step 2; memory
	// loses step 0 and has a NaN at step 3. Prometheus answers with three CPU
	// samples and three memory samples at different steps. Read by position,
	// they would line up against the wrong times and against each other. Read
	// by timestamp, every sample stays on its own step and both series share
	// one grid.
	srv, _ := trendsPrometheus(t, func(q string) gridAnswer {
		switch {
		case strings.Contains(q, "node_cpu_seconds_total"):
			return gridAnswer{0: "10", 1: "11", 3: "13"}
		case strings.Contains(q, "node_memory_MemAvailable_bytes"):
			return gridAnswer{1: "21", 2: "22", 3: "NaN"}
		default:
			return gridAnswer{}
		}
	})
	a := localTrendsAdapter(t, srv.URL)

	before := time.Now()
	got, err := a.DashboardTrends(context.Background(), "local", 6*time.Minute, 2*time.Minute)
	if err != nil {
		t.Fatalf("DashboardTrends: %v", err)
	}

	assertSeries(t, "cpu", got.CPU, []float64{10, 11, math.NaN(), 13})
	assertSeries(t, "memory", got.Memory, []float64{math.NaN(), 21, 22, math.NaN()})
	if got.Nodes != nil {
		t.Fatalf("a query with no samples must stay null, got %v", got.Nodes)
	}
	if got.End.Before(before.Add(-time.Millisecond)) || got.End.After(time.Now()) {
		t.Fatalf("end %v is not the time of the query", got.End)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"cpu":[10,11,null,13]`, `"memory":[null,21,22,null]`, `"nodes":null`, `"end":"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body %s does not contain %s", body, want)
		}
	}
}
