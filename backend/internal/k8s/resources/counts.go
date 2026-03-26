package resources

import (
	"net/http"

	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"k8s.io/apimachinery/pkg/labels"
)

// HandleResourceCounts returns counts for all informer-tracked resource types.
// For remote clusters (non-local), returns an error since informer cache is local only.
// GET /api/v1/resources/counts[?namespace=default]
func (h *Handler) HandleResourceCounts(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUser(w, r)
	if !ok {
		return
	}

	// Resource counts rely on the local informer cache — remote clusters
	// use direct API calls and do not populate informers.
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if clusterID != "local" {
		writeError(w, http.StatusBadRequest, "resource counts are only available for the local cluster", "")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	counts := h.countResources(namespace)
	writeData(w, counts)
}

// countResources queries the informer cache for each tracked resource kind
// and returns a map of kind -> count.
func (h *Handler) countResources(namespace string) map[string]int {
	counts := make(map[string]int)
	sel := labels.Everything()

	// --- Cluster-scoped resources (namespace param ignored) ---

	if items, err := h.Informers.Nodes().List(sel); err == nil {
		counts["nodes"] = len(items)
	}
	if items, err := h.Informers.Namespaces().List(sel); err == nil {
		counts["namespaces"] = len(items)
	}
	if items, err := h.Informers.PersistentVolumes().List(sel); err == nil {
		counts["persistentvolumes"] = len(items)
	}
	if items, err := h.Informers.StorageClasses().List(sel); err == nil {
		counts["storageclasses"] = len(items)
	}
	if items, err := h.Informers.ClusterRoles().List(sel); err == nil {
		counts["clusterroles"] = len(items)
	}
	if items, err := h.Informers.ClusterRoleBindings().List(sel); err == nil {
		counts["clusterrolebindings"] = len(items)
	}

	// --- Namespace-scoped resources ---

	if namespace != "" {
		// Scoped to a single namespace
		if items, err := h.Informers.Deployments().Deployments(namespace).List(sel); err == nil {
			counts["deployments"] = len(items)
		}
		if items, err := h.Informers.StatefulSets().StatefulSets(namespace).List(sel); err == nil {
			counts["statefulsets"] = len(items)
		}
		if items, err := h.Informers.DaemonSets().DaemonSets(namespace).List(sel); err == nil {
			counts["daemonsets"] = len(items)
		}
		if items, err := h.Informers.Pods().Pods(namespace).List(sel); err == nil {
			counts["pods"] = len(items)
		}
		if items, err := h.Informers.Jobs().Jobs(namespace).List(sel); err == nil {
			counts["jobs"] = len(items)
		}
		if items, err := h.Informers.CronJobs().CronJobs(namespace).List(sel); err == nil {
			counts["cronjobs"] = len(items)
		}
		if items, err := h.Informers.ReplicaSets().ReplicaSets(namespace).List(sel); err == nil {
			counts["replicasets"] = len(items)
		}
		if items, err := h.Informers.Services().Services(namespace).List(sel); err == nil {
			counts["services"] = len(items)
		}
		if items, err := h.Informers.Ingresses().Ingresses(namespace).List(sel); err == nil {
			counts["ingresses"] = len(items)
		}
		if items, err := h.Informers.NetworkPolicies().NetworkPolicies(namespace).List(sel); err == nil {
			counts["networkpolicies"] = len(items)
		}
		if items, err := h.Informers.ConfigMaps().ConfigMaps(namespace).List(sel); err == nil {
			counts["configmaps"] = len(items)
		}
		// Secrets are intentionally NOT cached in the informer to avoid holding
		// all cluster secrets in process memory. Skipped from counts.
		if items, err := h.Informers.ServiceAccounts().ServiceAccounts(namespace).List(sel); err == nil {
			counts["serviceaccounts"] = len(items)
		}
		if items, err := h.Informers.ResourceQuotas().ResourceQuotas(namespace).List(sel); err == nil {
			counts["resourcequotas"] = len(items)
		}
		if items, err := h.Informers.LimitRanges().LimitRanges(namespace).List(sel); err == nil {
			counts["limitranges"] = len(items)
		}
		if items, err := h.Informers.PersistentVolumeClaims().PersistentVolumeClaims(namespace).List(sel); err == nil {
			counts["persistentvolumeclaims"] = len(items)
		}
		if items, err := h.Informers.Roles().Roles(namespace).List(sel); err == nil {
			counts["roles"] = len(items)
		}
		if items, err := h.Informers.RoleBindings().RoleBindings(namespace).List(sel); err == nil {
			counts["rolebindings"] = len(items)
		}
		if items, err := h.Informers.HorizontalPodAutoscalers().HorizontalPodAutoscalers(namespace).List(sel); err == nil {
			counts["horizontalpodautoscalers"] = len(items)
		}
		if items, err := h.Informers.PodDisruptionBudgets().PodDisruptionBudgets(namespace).List(sel); err == nil {
			counts["poddisruptionbudgets"] = len(items)
		}
		if items, err := h.Informers.Endpoints().Endpoints(namespace).List(sel); err == nil {
			counts["endpoints"] = len(items)
		}
		if items, err := h.Informers.EndpointSlices().EndpointSlices(namespace).List(sel); err == nil {
			counts["endpointslices"] = len(items)
		}
	} else {
		// Cluster-wide (all namespaces)
		if items, err := h.Informers.Deployments().List(sel); err == nil {
			counts["deployments"] = len(items)
		}
		if items, err := h.Informers.StatefulSets().List(sel); err == nil {
			counts["statefulsets"] = len(items)
		}
		if items, err := h.Informers.DaemonSets().List(sel); err == nil {
			counts["daemonsets"] = len(items)
		}
		if items, err := h.Informers.Pods().List(sel); err == nil {
			counts["pods"] = len(items)
		}
		if items, err := h.Informers.Jobs().List(sel); err == nil {
			counts["jobs"] = len(items)
		}
		if items, err := h.Informers.CronJobs().List(sel); err == nil {
			counts["cronjobs"] = len(items)
		}
		if items, err := h.Informers.ReplicaSets().List(sel); err == nil {
			counts["replicasets"] = len(items)
		}
		if items, err := h.Informers.Services().List(sel); err == nil {
			counts["services"] = len(items)
		}
		if items, err := h.Informers.Ingresses().List(sel); err == nil {
			counts["ingresses"] = len(items)
		}
		if items, err := h.Informers.NetworkPolicies().List(sel); err == nil {
			counts["networkpolicies"] = len(items)
		}
		if items, err := h.Informers.ConfigMaps().List(sel); err == nil {
			counts["configmaps"] = len(items)
		}
		// Secrets intentionally skipped (not in informer cache).
		if items, err := h.Informers.ServiceAccounts().List(sel); err == nil {
			counts["serviceaccounts"] = len(items)
		}
		if items, err := h.Informers.ResourceQuotas().List(sel); err == nil {
			counts["resourcequotas"] = len(items)
		}
		if items, err := h.Informers.LimitRanges().List(sel); err == nil {
			counts["limitranges"] = len(items)
		}
		if items, err := h.Informers.PersistentVolumeClaims().List(sel); err == nil {
			counts["persistentvolumeclaims"] = len(items)
		}
		if items, err := h.Informers.Roles().List(sel); err == nil {
			counts["roles"] = len(items)
		}
		if items, err := h.Informers.RoleBindings().List(sel); err == nil {
			counts["rolebindings"] = len(items)
		}
		if items, err := h.Informers.HorizontalPodAutoscalers().List(sel); err == nil {
			counts["horizontalpodautoscalers"] = len(items)
		}
		if items, err := h.Informers.PodDisruptionBudgets().List(sel); err == nil {
			counts["poddisruptionbudgets"] = len(items)
		}
		if items, err := h.Informers.Endpoints().List(sel); err == nil {
			counts["endpoints"] = len(items)
		}
		if items, err := h.Informers.EndpointSlices().List(sel); err == nil {
			counts["endpointslices"] = len(items)
		}
	}

	return counts
}
