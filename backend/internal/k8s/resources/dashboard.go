package resources

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	corev1 "k8s.io/api/core/v1"
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

// Utilization holds a percentage value for a resource metric.
type Utilization struct {
	Percentage float64 `json:"percentage"`
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

	// Prometheus utilization (async, 1s timeout)
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
			summary.CPU = &Utilization{Percentage: cpuPct}
		}
		if memErr == nil {
			summary.Memory = &Utilization{Percentage: memPct}
		}
	}

	writeJSON(w, http.StatusOK, api.Response{Data: summary})
}
