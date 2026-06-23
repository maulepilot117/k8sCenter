package monitoring

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/prometheus/common/model"

	"github.com/kubecenter/kubecenter/internal/k8s/resources"
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
	assign func(*resources.DashboardTrends, []float64)
}{
	{`count(kube_node_info)`, func(t *resources.DashboardTrends, v []float64) { t.Nodes = v }},
	{`count(kube_pod_info)`, func(t *resources.DashboardTrends, v []float64) { t.Pods = v }},
	{`count(kube_service_info)`, func(t *resources.DashboardTrends, v []float64) { t.Services = v }},
	{`100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`, func(t *resources.DashboardTrends, v []float64) { t.CPU = v }},
	{`(1 - (avg(node_memory_MemAvailable_bytes) / avg(node_memory_MemTotal_bytes))) * 100`, func(t *resources.DashboardTrends, v []float64) { t.Memory = v }},
	// Cluster-wide network throughput in Mbps (bytes/s * 8 / 1e6). Virtual
	// interfaces (veth/cali/lxc/cilium) are excluded so pod-to-pod traffic
	// isn't double-counted against physical NIC throughput — same device
	// filter the per-node network queries in query_registry.go use.
	{`sum(rate(node_network_receive_bytes_total{device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) * 8 / 1e6`, func(t *resources.DashboardTrends, v []float64) { t.NetworkRx = v }},
	{`sum(rate(node_network_transmit_bytes_total{device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) * 8 / 1e6`, func(t *resources.DashboardTrends, v []float64) { t.NetworkTx = v }},
}

// DashboardTrends implements resources.TrendProvider. It range-queries
// Prometheus for the metric-card series concurrently and returns whatever
// resolved; individual query failures yield an empty series for that metric
// rather than failing the whole request.
func (a *UtilizationAdapter) DashboardTrends(ctx context.Context, window, step time.Duration) (resources.DashboardTrends, error) {
	out := resources.DashboardTrends{
		Window: window.String(),
		Step:   step.String(),
	}

	pc := a.Discoverer.PrometheusClient()
	if pc == nil {
		return out, fmt.Errorf("prometheus not available")
	}

	// Bound the whole fan-out; QueryRange also applies its own per-call timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	end := time.Now()
	start := end.Add(-window)

	series := make([][]float64, len(trendQueries))
	var wg sync.WaitGroup
	wg.Add(len(trendQueries))
	for i, q := range trendQueries {
		go func(i int, query string) {
			defer wg.Done()
			val, _, err := pc.QueryRange(ctx, query, start, end, step)
			if err != nil {
				return // leave series[i] nil → empty slice in JSON
			}
			series[i] = parseMatrixSeries(val)
		}(i, q.query)
	}
	wg.Wait()

	for i, q := range trendQueries {
		q.assign(&out, series[i])
	}

	return out, nil
}

// parseMatrixSeries extracts the sample values of the first series from a range
// query result. Our trend queries are scalar aggregates, so there is exactly
// one series; anything else (empty matrix, wrong type) yields nil.
//
// Non-finite samples (NaN/±Inf) are dropped: Prometheus emits them from
// division-by-zero (e.g. the memory query during a scrape gap) and counter
// resets, and encoding/json fails on NaN/±Inf — which, because writeJSON has
// already sent the 200 header, would truncate the body and silently blank
// every sparkline. Dropping the bad points keeps the response valid JSON.
func parseMatrixSeries(val model.Value) []float64 {
	matrix, ok := val.(model.Matrix)
	if !ok || len(matrix) == 0 {
		return nil
	}
	pairs := matrix[0].Values
	if len(pairs) == 0 {
		return nil
	}
	values := make([]float64, 0, len(pairs))
	for _, p := range pairs {
		f := float64(p.Value)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		values = append(values, f)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}
