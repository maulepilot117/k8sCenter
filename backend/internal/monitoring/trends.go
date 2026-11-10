package monitoring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/prometheus/common/model"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
)

// Dashboard trend window and resolution are chosen by the caller (the handler
// maps the frontend's time-range tab to a window/step pair). Each tab targets
// ~30 points — dense enough to read as a trend, cheap enough to range-query
// every dashboard refresh.

// trendQueries are the PromQL expressions backing each metric card's sparkline.
//
//   - nodes/pods/services come from kube-state-metrics; if it is absent the
//     range query returns an empty matrix and the series stays empty (the
//     frontend then renders no sparkline rather than a misleading flat line).
//   - cpu/memory are the range-query companions of UtilizationAdapter's instant
//     CPUPercent/MemoryPercent queries (same PromQL); they come from
//     node-exporter, which is independent of kube-state-metrics.
//   - networkRx/networkTx are cluster-wide throughput in Mbps from node-exporter.
//
// Each entry carries its own assign func so a new metric self-registers its
// destination field — there is no separate key→field switch to keep in sync,
// so it is impossible to add a query and silently drop its result.
var trendQueries = []struct {
	query  string
	assign func(*resources.DashboardTrends, resources.TrendSeries)
}{
	{`count(kube_node_info)`, func(t *resources.DashboardTrends, v resources.TrendSeries) { t.Nodes = v }},
	{`count(kube_pod_info)`, func(t *resources.DashboardTrends, v resources.TrendSeries) { t.Pods = v }},
	{`count(kube_service_info)`, func(t *resources.DashboardTrends, v resources.TrendSeries) { t.Services = v }},
	{`100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`, func(t *resources.DashboardTrends, v resources.TrendSeries) { t.CPU = v }},
	{`(1 - (avg(node_memory_MemAvailable_bytes) / avg(node_memory_MemTotal_bytes))) * 100`, func(t *resources.DashboardTrends, v resources.TrendSeries) { t.Memory = v }},
	// Cluster-wide network throughput in Mbps (bytes/s * 8 / 1e6). Virtual
	// interfaces (veth/cali/lxc/cilium) are excluded so pod-to-pod traffic
	// isn't double-counted against physical NIC throughput — same device
	// filter the per-node network queries in query_registry.go use.
	{`sum(rate(node_network_receive_bytes_total{device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) * 8 / 1e6`, func(t *resources.DashboardTrends, v resources.TrendSeries) { t.NetworkRx = v }},
	{`sum(rate(node_network_transmit_bytes_total{device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) * 8 / 1e6`, func(t *resources.DashboardTrends, v resources.TrendSeries) { t.NetworkTx = v }},
}

// ErrAllTrendQueriesFailed means every trend range query against a remote
// cluster's Prometheus failed. It wraps the first query's error, which can
// name the cluster's Prometheus: log it, never return it to a client.
var ErrAllTrendQueriesFailed = errors.New("all dashboard trend queries failed")

// DashboardTrends implements resources.TrendProvider. It range-queries
// Prometheus for the metric-card series concurrently and returns whatever
// resolved; individual query failures are logged and yield an empty series
// for that metric rather than failing the whole request.
//
// A resolution failure (no binding for a remote cluster, local Prometheus not
// discovered, binding unreadable) is returned as the error, so the caller can
// tell "not configured" from "failed". For a remote cluster, every query
// failing is also an error (ErrAllTrendQueriesFailed): a broken binding must
// not read as "no data". The local cluster keeps its empty-series answer.
func (a *UtilizationAdapter) DashboardTrends(ctx context.Context, clusterID string, window, step time.Duration) (resources.DashboardTrends, error) {
	out := resources.DashboardTrends{
		Window: window.String(),
		Step:   step.String(),
	}

	pc, err := a.client(ctx, clusterID)
	if err != nil {
		return out, err
	}
	logger := a.Resolver.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Bound the whole fan-out; QueryRange also applies its own per-call timeout.
	//
	// TODO(perf): this single 5s deadline is shared across all concurrent
	// trendQueries (currently 7, including the two cluster-wide network
	// range queries). On a loaded Prometheus the 24h window at a 48m step can
	// push several queries past 5s together, blanking every sparkline at once.
	// Locally that failure is benign (empty series, HTTP 200 — the cards just
	// hide); for a remote cluster it surfaces as ErrAllTrendQueriesFailed. It
	// is a tuning question, not a correctness bug: measure 24h/48m latency
	// against a representative-retention Prometheus, then either raise this to
	// ~10s or give each query its own ~4s deadline so slow ones fail in
	// isolation. Flagged by the 2026-06-23 code review of the Network I/O tile.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Prometheus evaluates a range query at start, start+step, ... up to end,
	// with millisecond timestamps. Truncating to the millisecond first keeps
	// end - start exactly one window on the wire, so the last step lands on end
	// rather than being lost to rounding.
	end := time.Now().Truncate(time.Millisecond)
	start := end.Add(-window)
	steps := trendGridSteps(window, step)

	series := make([]resources.TrendSeries, len(trendQueries))
	errs := make([]error, len(trendQueries))
	var wg sync.WaitGroup
	wg.Add(len(trendQueries))
	for i, q := range trendQueries {
		go func(i int, query string) {
			defer wg.Done()
			recoverutil.Safe(logger, "monitoring trends-query", func() {
				val, _, err := pc.QueryRange(ctx, query, start, end, step)
				if err != nil {
					errs[i] = err
					logger.Warn("dashboard trend query failed", "cluster", clusterID, "query", i, "error", err)
					return // leave series[i] nil → null in JSON
				}
				series[i] = parseMatrixSeries(val, start, step, steps)
			})
		}(i, q.query)
	}
	wg.Wait()

	var firstErr error
	failed := 0
	for _, err := range errs {
		if err == nil {
			continue
		}
		failed++
		if firstErr == nil {
			firstErr = err
		}
	}
	if failed == len(trendQueries) && !k8s.IsLocalClusterID(clusterID) {
		return out, fmt.Errorf("%w: %w", ErrAllTrendQueriesFailed, firstErr)
	}

	for i, q := range trendQueries {
		q.assign(&out, series[i])
	}
	out.End = end.UTC()

	return out, nil
}

// trendGridSteps is how many grid steps a window holds: start, start+step,
// ..., up to and including end. Every window/step pair the handler uses
// divides evenly, giving window/step + 1 entries (31 for each tab).
func trendGridSteps(window, step time.Duration) int {
	if step <= 0 || window < 0 {
		return 0
	}
	return int(window/step) + 1
}

// parseMatrixSeries places the samples of the first series from a range query
// result onto the query's time grid: entry k is the sample taken at
// start + k*step. Our trend queries are scalar aggregates, so there is exactly
// one series; anything else (empty matrix, wrong type) yields nil.
//
// Each sample goes in by its own timestamp, never by its position in the
// result. Prometheus leaves out a step that has no value (a scrape gap, a
// target that was down), and rate() and an instant selector lose different
// steps, so position would move every older sample later and put CPU and
// memory out of step with each other. A step with no sample stays NaN, as
// does a NaN or ±Inf sample (division by zero on the memory query during a
// scrape gap, counter resets); TrendSeries encodes both as null.
//
// A series with no finite sample anywhere on the grid yields nil, so the
// frontend renders no sparkline rather than a row of gaps.
func parseMatrixSeries(val model.Value, start time.Time, step time.Duration, steps int) resources.TrendSeries {
	matrix, ok := val.(model.Matrix)
	if !ok || len(matrix) == 0 || step <= 0 || steps <= 0 {
		return nil
	}
	values := make(resources.TrendSeries, steps)
	for i := range values {
		values[i] = math.NaN()
	}
	startMs := start.UnixMilli()
	stepMs := float64(step.Milliseconds())
	found := false
	for _, p := range matrix[0].Values {
		f := float64(p.Value)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		// Round to the nearest step: Prometheus timestamps are milliseconds,
		// so a sample can sit a millisecond off the grid it was taken on.
		k := int(math.Round(float64(int64(p.Timestamp)-startMs) / stepMs))
		if k < 0 || k >= steps {
			continue
		}
		values[k] = f
		found = true
	}
	if !found {
		return nil
	}
	return values
}
