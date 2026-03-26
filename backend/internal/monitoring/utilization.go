package monitoring

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/common/model"
)

// UtilizationAdapter implements resources.UtilizationProvider using PrometheusClient.
type UtilizationAdapter struct {
	Discoverer *Discoverer
}

// CPUPercent returns the cluster-wide CPU utilization percentage via PromQL.
func (a *UtilizationAdapter) CPUPercent(ctx context.Context) (float64, error) {
	pc := a.Discoverer.PrometheusClient()
	if pc == nil {
		return 0, fmt.Errorf("prometheus not available")
	}
	result, _, err := pc.Query(ctx, `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`, time.Now())
	if err != nil {
		return 0, err
	}
	return parseScalarResult(result)
}

// MemoryPercent returns the cluster-wide memory utilization percentage via PromQL.
func (a *UtilizationAdapter) MemoryPercent(ctx context.Context) (float64, error) {
	pc := a.Discoverer.PrometheusClient()
	if pc == nil {
		return 0, fmt.Errorf("prometheus not available")
	}
	result, _, err := pc.Query(ctx, `(1 - (avg(node_memory_MemAvailable_bytes) / avg(node_memory_MemTotal_bytes))) * 100`, time.Now())
	if err != nil {
		return 0, err
	}
	return parseScalarResult(result)
}

// parseScalarResult extracts a float64 from a Prometheus Vector result.
func parseScalarResult(val model.Value) (float64, error) {
	vec, ok := val.(model.Vector)
	if !ok || len(vec) == 0 {
		return 0, fmt.Errorf("unexpected result type")
	}
	return float64(vec[0].Value), nil
}
