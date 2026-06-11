package monitoring

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/common/model"

	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Dashboard trend window and resolution. A 1h window at a 2m step yields ~31
// points — dense enough to read as a trend, cheap enough to range-query every
// dashboard refresh.
const (
	trendWindow = time.Hour
	trendStep   = 2 * time.Minute
)

// trendQueries are the PromQL expressions backing each metric card's sparkline.
//
//   - nodes/pods/services come from kube-state-metrics; if it is absent the
//     range query returns an empty matrix and the series stays empty (the
//     frontend then renders no sparkline rather than a misleading flat line).
//   - alerts uses `or vector(0)` so a cluster with zero firing alerts over the
//     window still produces a flat zero baseline — that reads as "all clear",
//     which is meaningful, unlike the missing-data case above.
//   - cpu/memory are the range-query companions of UtilizationAdapter's instant
//     CPUPercent/MemoryPercent queries (same PromQL); they come from
//     node-exporter, which is independent of kube-state-metrics.
var trendQueries = []struct {
	key   string
	query string
}{
	{"nodes", `count(kube_node_info)`},
	{"pods", `count(kube_pod_info)`},
	{"services", `count(kube_service_info)`},
	{"alerts", `count(ALERTS{alertstate="firing"}) or vector(0)`},
	{"cpu", `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`},
	{"memory", `(1 - (avg(node_memory_MemAvailable_bytes) / avg(node_memory_MemTotal_bytes))) * 100`},
}

// DashboardTrends implements resources.TrendProvider. It range-queries
// Prometheus for the four metric-card series concurrently and returns whatever
// resolved; individual query failures yield an empty series for that metric
// rather than failing the whole request.
func (a *UtilizationAdapter) DashboardTrends(ctx context.Context) (resources.DashboardTrends, error) {
	out := resources.DashboardTrends{
		Window: trendWindow.String(),
		Step:   trendStep.String(),
	}

	pc := a.Discoverer.PrometheusClient()
	if pc == nil {
		return out, fmt.Errorf("prometheus not available")
	}

	// Bound the whole fan-out; QueryRange also applies its own per-call timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	end := time.Now()
	start := end.Add(-trendWindow)

	series := make([][]float64, len(trendQueries))
	var wg sync.WaitGroup
	wg.Add(len(trendQueries))
	for i, q := range trendQueries {
		go func(i int, query string) {
			defer wg.Done()
			val, _, err := pc.QueryRange(ctx, query, start, end, trendStep)
			if err != nil {
				return // leave series[i] nil → empty slice in JSON
			}
			series[i] = parseMatrixSeries(val)
		}(i, q.query)
	}
	wg.Wait()

	for i, q := range trendQueries {
		switch q.key {
		case "nodes":
			out.Nodes = series[i]
		case "pods":
			out.Pods = series[i]
		case "services":
			out.Services = series[i]
		case "alerts":
			out.Alerts = series[i]
		case "cpu":
			out.CPU = series[i]
		case "memory":
			out.Memory = series[i]
		}
	}

	return out, nil
}

// parseMatrixSeries extracts the sample values of the first series from a range
// query result. Our trend queries are scalar aggregates, so there is exactly
// one series; anything else (empty matrix, wrong type) yields nil.
func parseMatrixSeries(val model.Value) []float64 {
	matrix, ok := val.(model.Matrix)
	if !ok || len(matrix) == 0 {
		return nil
	}
	pairs := matrix[0].Values
	if len(pairs) == 0 {
		return nil
	}
	values := make([]float64, len(pairs))
	for i, p := range pairs {
		values[i] = float64(p.Value)
	}
	return values
}
