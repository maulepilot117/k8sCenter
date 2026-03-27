package resources

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
)

// DashboardSummary is the response payload for GET /api/v1/cluster/dashboard-summary.
type DashboardSummary struct {
	Nodes    NodeSummary    `json:"nodes"`
	Pods     PodSummary     `json:"pods"`
	Services ServiceCount   `json:"services"`
	Alerts   AlertSummary   `json:"alerts"`
	CPU      *Utilization   `json:"cpu"`
	Memory   *Utilization   `json:"memory"`
}

// NodeSummary contains node counts.
type NodeSummary struct {
	Total int `json:"total"`
	Ready int `json:"ready"`
}

// PodSummary contains pod phase counts.
type PodSummary struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

// ServiceCount contains the total number of services.
type ServiceCount struct {
	Total int `json:"total"`
}

// AlertSummary contains active alert counts.
type AlertSummary struct {
	Active   int `json:"active"`
	Critical int `json:"critical"`
}

// Utilization holds resource utilization data including requests and limits.
type Utilization struct {
	Percentage float64 `json:"percentage"`
	Used       string  `json:"used"`
	Total      string  `json:"total"`
	Requests   string  `json:"requests"`
	Limits     string  `json:"limits"`
}

// HandleDashboardSummary returns aggregated cluster health data from the informer cache.
// Prometheus metrics (CPU/Memory) are fetched asynchronously with a 1-second timeout.
// Only available for the local cluster (informer cache is not available for remote clusters).
func (h *Handler) HandleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	// Check for local cluster only
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if clusterID != "" && clusterID != "local" {
		writeError(w, http.StatusBadRequest, "dashboard summary is only available for the local cluster", "")
		return
	}

	summary := DashboardSummary{}

	// Node counts from informer (cluster-scoped: namespace="")
	if allowed, _ := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "nodes", ""); allowed {
		nodes, err := h.Informers.Nodes().List(labels.Everything())
		if err == nil {
			summary.Nodes.Total = len(nodes)
			for _, n := range nodes {
				for _, c := range n.Status.Conditions {
					if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
						summary.Nodes.Ready++
						break
					}
				}
			}
		}
	}

	// Pod counts from informer (cluster-wide)
	if allowed, _ := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "pods", ""); allowed {
		pods, err := h.Informers.Pods().List(labels.Everything())
		if err == nil {
			summary.Pods.Total = len(pods)
			for _, p := range pods {
				switch p.Status.Phase {
				case corev1.PodRunning:
					summary.Pods.Running++
				case corev1.PodPending:
					summary.Pods.Pending++
				case corev1.PodFailed:
					summary.Pods.Failed++
				}
			}
		}
	}

	// Service count from informer (cluster-wide)
	if allowed, _ := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "services", ""); allowed {
		services, err := h.Informers.Services().List(labels.Everything())
		if err == nil {
			summary.Services.Total = len(services)
		}
	}

	// Alert counts from alerting store
	if h.Alerts != nil {
		if allowed, _ := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "alertmanagers", ""); allowed {
			active, critical, err := h.Alerts.ActiveAlertCounts(r.Context())
			if err == nil {
				summary.Alerts.Active = active
				summary.Alerts.Critical = critical
			}
		}
	}

	// Aggregate resource requests/limits from all pod containers and node allocatable.
	// This data comes directly from the Kubernetes API (informer cache), not Prometheus.
	var cpuRequests, cpuLimits, memRequests, memLimits resource.Quantity
	var cpuAllocatable, memAllocatable resource.Quantity

	// Sum node allocatable
	if allowed, _ := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "nodes", ""); allowed {
		nodes, err := h.Informers.Nodes().List(labels.Everything())
		if err == nil {
			for _, n := range nodes {
				if cpu, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
					cpuAllocatable.Add(cpu)
				}
				if mem, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
					memAllocatable.Add(mem)
				}
			}
		}
	}

	// Sum pod container requests/limits
	if allowed, _ := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "pods", ""); allowed {
		pods, err := h.Informers.Pods().List(labels.Everything())
		if err == nil {
			for _, p := range pods {
				// Only count running/pending pods, not completed/failed
				if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodPending {
					continue
				}
				for _, c := range p.Spec.Containers {
					if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
						cpuRequests.Add(req)
					}
					if lim, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
						cpuLimits.Add(lim)
					}
					if req, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
						memRequests.Add(req)
					}
					if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
						memLimits.Add(lim)
					}
				}
			}
		}
	}

	// Format helpers
	formatCPU := func(q resource.Quantity) string {
		millis := q.MilliValue()
		if millis >= 1000 {
			return fmt.Sprintf("%.1f cores", float64(millis)/1000)
		}
		return fmt.Sprintf("%dm", millis)
	}
	formatMem := func(q resource.Quantity) string {
		bytes := q.Value()
		gi := float64(bytes) / (1024 * 1024 * 1024)
		if gi >= 1 {
			return fmt.Sprintf("%.1f Gi", gi)
		}
		mi := float64(bytes) / (1024 * 1024)
		return fmt.Sprintf("%.0f Mi", mi)
	}

	cpuTotalStr := formatCPU(cpuAllocatable)
	memTotalStr := formatMem(memAllocatable)

	// Run CPU and memory queries concurrently with a 1-second timeout.
	// We use sync.WaitGroup (not errgroup) because we want both queries to
	// complete independently — a failure in one should not cancel the other.
	// Each goroutine captures its own error variable for independent handling.
	if h.Utilization != nil {
		promCtx, promCancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer promCancel()

		var wg sync.WaitGroup
		var cpuPct, memPct float64
		var cpuErr, memErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			cpuPct, cpuErr = h.Utilization.CPUPercent(promCtx)
		}()
		go func() {
			defer wg.Done()
			memPct, memErr = h.Utilization.MemoryPercent(promCtx)
		}()
		wg.Wait()

		if cpuErr == nil {
			cpuUsedMillis := cpuPct / 100 * float64(cpuAllocatable.MilliValue())
			summary.CPU = &Utilization{
				Percentage: cpuPct,
				Used:       formatCPU(*resource.NewMilliQuantity(int64(cpuUsedMillis), resource.DecimalSI)),
				Total:      cpuTotalStr,
				Requests:   formatCPU(cpuRequests),
				Limits:     formatCPU(cpuLimits),
			}
		}
		if memErr == nil {
			memUsedBytes := memPct / 100 * float64(memAllocatable.Value())
			summary.Memory = &Utilization{
				Percentage: memPct,
				Used:       formatMem(*resource.NewQuantity(int64(memUsedBytes), resource.BinarySI)),
				Total:      memTotalStr,
				Requests:   formatMem(memRequests),
				Limits:     formatMem(memLimits),
			}
		}
	}

	// If Prometheus was unavailable, still provide requests/limits/total from k8s API
	if summary.CPU == nil && cpuAllocatable.MilliValue() > 0 {
		summary.CPU = &Utilization{
			Percentage: 0,
			Used:       "N/A",
			Total:      cpuTotalStr,
			Requests:   formatCPU(cpuRequests),
			Limits:     formatCPU(cpuLimits),
		}
	}
	if summary.Memory == nil && memAllocatable.Value() > 0 {
		summary.Memory = &Utilization{
			Percentage: 0,
			Used:       "N/A",
			Total:      memTotalStr,
			Requests:   formatMem(memRequests),
			Limits:     formatMem(memLimits),
		}
	}

	writeJSON(w, http.StatusOK, api.Response{Data: summary})
}
