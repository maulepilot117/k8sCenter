package resources

import (
	"context"
	"net/http"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"k8s.io/apimachinery/pkg/labels"
)

// HandleResourceCounts returns counts for all informer-tracked resource types.
// For remote clusters (non-local), returns an error since informer cache is local only.
// Each resource kind is only included if the user has RBAC "list" permission for it.
// GET /api/v1/resources/counts[?namespace=default]
func (h *Handler) HandleResourceCounts(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	// Resource counts rely on the local informer cache — remote clusters
	// use direct API calls and do not populate informers.
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if clusterID != "" && clusterID != "local" {
		writeError(w, http.StatusBadRequest, "resource counts are only available for the local cluster", "")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	counts := h.countResources(r.Context(), user, namespace)
	writeData(w, counts)
}

// canList checks if the user has "list" permission for the given resource in the namespace.
func (h *Handler) canList(ctx context.Context, user *auth.User, resource, namespace string) bool {
	allowed, _ := h.AccessChecker.CanAccess(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", resource, namespace)
	return allowed
}

// countResources queries the informer cache for each tracked resource kind
// and returns a map of kind -> count. Only includes resources the user has
// RBAC "list" permission for.
//
// Scaling note: the cluster-wide path calls Lister.List(labels.Everything()),
// which allocates a full []*T slice just to take len(). For very large clusters
// (10K+ pods) this creates GC pressure. A lower-allocation alternative is
// Informer().GetStore().ListKeys() which returns only string keys, e.g.:
//
//	store := h.Informers.Factory().Core().V1().Pods().Informer().GetStore()
//	counts["pods"] = len(store.ListKeys())
//
// However, ListKeys() ignores namespace scoping and label selectors, so it
// can only replace the cluster-wide (else) branch. The namespace-scoped branch
// must continue using List() for filtering. Kept as-is for now since both
// paths share the same structure and the current approach is correct.
func (h *Handler) countResources(ctx context.Context, user *auth.User, namespace string) map[string]int {
	counts := make(map[string]int)
	sel := labels.Everything()

	// --- Cluster-scoped resources (namespace param ignored) ---

	if h.canList(ctx, user, "nodes", "") {
		if items, err := h.Informers.Nodes().List(sel); err == nil {
			counts["nodes"] = len(items)
		}
	}
	if h.canList(ctx, user, "namespaces", "") {
		if items, err := h.Informers.Namespaces().List(sel); err == nil {
			counts["namespaces"] = len(items)
		}
	}
	if h.canList(ctx, user, "persistentvolumes", "") {
		if items, err := h.Informers.PersistentVolumes().List(sel); err == nil {
			counts["persistentvolumes"] = len(items)
		}
	}
	if h.canList(ctx, user, "storageclasses", "") {
		if items, err := h.Informers.StorageClasses().List(sel); err == nil {
			counts["storageclasses"] = len(items)
		}
	}
	if h.canList(ctx, user, "clusterroles", "") {
		if items, err := h.Informers.ClusterRoles().List(sel); err == nil {
			counts["clusterroles"] = len(items)
		}
	}
	if h.canList(ctx, user, "clusterrolebindings", "") {
		if items, err := h.Informers.ClusterRoleBindings().List(sel); err == nil {
			counts["clusterrolebindings"] = len(items)
		}
	}

	// --- Namespace-scoped resources ---

	if namespace != "" {
		// Scoped to a single namespace
		if h.canList(ctx, user, "deployments", namespace) {
			if items, err := h.Informers.Deployments().Deployments(namespace).List(sel); err == nil {
				counts["deployments"] = len(items)
			}
		}
		if h.canList(ctx, user, "statefulsets", namespace) {
			if items, err := h.Informers.StatefulSets().StatefulSets(namespace).List(sel); err == nil {
				counts["statefulsets"] = len(items)
			}
		}
		if h.canList(ctx, user, "daemonsets", namespace) {
			if items, err := h.Informers.DaemonSets().DaemonSets(namespace).List(sel); err == nil {
				counts["daemonsets"] = len(items)
			}
		}
		if h.canList(ctx, user, "pods", namespace) {
			if items, err := h.Informers.Pods().Pods(namespace).List(sel); err == nil {
				counts["pods"] = len(items)
			}
		}
		if h.canList(ctx, user, "jobs", namespace) {
			if items, err := h.Informers.Jobs().Jobs(namespace).List(sel); err == nil {
				counts["jobs"] = len(items)
			}
		}
		if h.canList(ctx, user, "cronjobs", namespace) {
			if items, err := h.Informers.CronJobs().CronJobs(namespace).List(sel); err == nil {
				counts["cronjobs"] = len(items)
			}
		}
		if h.canList(ctx, user, "replicasets", namespace) {
			if items, err := h.Informers.ReplicaSets().ReplicaSets(namespace).List(sel); err == nil {
				counts["replicasets"] = len(items)
			}
		}
		if h.canList(ctx, user, "services", namespace) {
			if items, err := h.Informers.Services().Services(namespace).List(sel); err == nil {
				counts["services"] = len(items)
			}
		}
		if h.canList(ctx, user, "ingresses", namespace) {
			if items, err := h.Informers.Ingresses().Ingresses(namespace).List(sel); err == nil {
				counts["ingresses"] = len(items)
			}
		}
		if h.canList(ctx, user, "networkpolicies", namespace) {
			if items, err := h.Informers.NetworkPolicies().NetworkPolicies(namespace).List(sel); err == nil {
				counts["networkpolicies"] = len(items)
			}
		}
		if h.canList(ctx, user, "configmaps", namespace) {
			if items, err := h.Informers.ConfigMaps().ConfigMaps(namespace).List(sel); err == nil {
				counts["configmaps"] = len(items)
			}
		}
		// Secrets are intentionally NOT cached in the informer to avoid holding
		// all cluster secrets in process memory. Skipped from counts.
		if h.canList(ctx, user, "serviceaccounts", namespace) {
			if items, err := h.Informers.ServiceAccounts().ServiceAccounts(namespace).List(sel); err == nil {
				counts["serviceaccounts"] = len(items)
			}
		}
		if h.canList(ctx, user, "resourcequotas", namespace) {
			if items, err := h.Informers.ResourceQuotas().ResourceQuotas(namespace).List(sel); err == nil {
				counts["resourcequotas"] = len(items)
			}
		}
		if h.canList(ctx, user, "limitranges", namespace) {
			if items, err := h.Informers.LimitRanges().LimitRanges(namespace).List(sel); err == nil {
				counts["limitranges"] = len(items)
			}
		}
		if h.canList(ctx, user, "persistentvolumeclaims", namespace) {
			if items, err := h.Informers.PersistentVolumeClaims().PersistentVolumeClaims(namespace).List(sel); err == nil {
				counts["persistentvolumeclaims"] = len(items)
			}
		}
		if h.canList(ctx, user, "roles", namespace) {
			if items, err := h.Informers.Roles().Roles(namespace).List(sel); err == nil {
				counts["roles"] = len(items)
			}
		}
		if h.canList(ctx, user, "rolebindings", namespace) {
			if items, err := h.Informers.RoleBindings().RoleBindings(namespace).List(sel); err == nil {
				counts["rolebindings"] = len(items)
			}
		}
		if h.canList(ctx, user, "horizontalpodautoscalers", namespace) {
			if items, err := h.Informers.HorizontalPodAutoscalers().HorizontalPodAutoscalers(namespace).List(sel); err == nil {
				counts["horizontalpodautoscalers"] = len(items)
			}
		}
		if h.canList(ctx, user, "poddisruptionbudgets", namespace) {
			if items, err := h.Informers.PodDisruptionBudgets().PodDisruptionBudgets(namespace).List(sel); err == nil {
				counts["poddisruptionbudgets"] = len(items)
			}
		}
		if h.canList(ctx, user, "endpoints", namespace) {
			if items, err := h.Informers.Endpoints().Endpoints(namespace).List(sel); err == nil {
				counts["endpoints"] = len(items)
			}
		}
		if h.canList(ctx, user, "endpointslices", namespace) {
			if items, err := h.Informers.EndpointSlices().EndpointSlices(namespace).List(sel); err == nil {
				counts["endpointslices"] = len(items)
			}
		}
	} else {
		// Cluster-wide (all namespaces)
		if h.canList(ctx, user, "deployments", "") {
			if items, err := h.Informers.Deployments().List(sel); err == nil {
				counts["deployments"] = len(items)
			}
		}
		if h.canList(ctx, user, "statefulsets", "") {
			if items, err := h.Informers.StatefulSets().List(sel); err == nil {
				counts["statefulsets"] = len(items)
			}
		}
		if h.canList(ctx, user, "daemonsets", "") {
			if items, err := h.Informers.DaemonSets().List(sel); err == nil {
				counts["daemonsets"] = len(items)
			}
		}
		if h.canList(ctx, user, "pods", "") {
			if items, err := h.Informers.Pods().List(sel); err == nil {
				counts["pods"] = len(items)
			}
		}
		if h.canList(ctx, user, "jobs", "") {
			if items, err := h.Informers.Jobs().List(sel); err == nil {
				counts["jobs"] = len(items)
			}
		}
		if h.canList(ctx, user, "cronjobs", "") {
			if items, err := h.Informers.CronJobs().List(sel); err == nil {
				counts["cronjobs"] = len(items)
			}
		}
		if h.canList(ctx, user, "replicasets", "") {
			if items, err := h.Informers.ReplicaSets().List(sel); err == nil {
				counts["replicasets"] = len(items)
			}
		}
		if h.canList(ctx, user, "services", "") {
			if items, err := h.Informers.Services().List(sel); err == nil {
				counts["services"] = len(items)
			}
		}
		if h.canList(ctx, user, "ingresses", "") {
			if items, err := h.Informers.Ingresses().List(sel); err == nil {
				counts["ingresses"] = len(items)
			}
		}
		if h.canList(ctx, user, "networkpolicies", "") {
			if items, err := h.Informers.NetworkPolicies().List(sel); err == nil {
				counts["networkpolicies"] = len(items)
			}
		}
		if h.canList(ctx, user, "configmaps", "") {
			if items, err := h.Informers.ConfigMaps().List(sel); err == nil {
				counts["configmaps"] = len(items)
			}
		}
		// Secrets intentionally skipped (not in informer cache).
		if h.canList(ctx, user, "serviceaccounts", "") {
			if items, err := h.Informers.ServiceAccounts().List(sel); err == nil {
				counts["serviceaccounts"] = len(items)
			}
		}
		if h.canList(ctx, user, "resourcequotas", "") {
			if items, err := h.Informers.ResourceQuotas().List(sel); err == nil {
				counts["resourcequotas"] = len(items)
			}
		}
		if h.canList(ctx, user, "limitranges", "") {
			if items, err := h.Informers.LimitRanges().List(sel); err == nil {
				counts["limitranges"] = len(items)
			}
		}
		if h.canList(ctx, user, "persistentvolumeclaims", "") {
			if items, err := h.Informers.PersistentVolumeClaims().List(sel); err == nil {
				counts["persistentvolumeclaims"] = len(items)
			}
		}
		if h.canList(ctx, user, "roles", "") {
			if items, err := h.Informers.Roles().List(sel); err == nil {
				counts["roles"] = len(items)
			}
		}
		if h.canList(ctx, user, "rolebindings", "") {
			if items, err := h.Informers.RoleBindings().List(sel); err == nil {
				counts["rolebindings"] = len(items)
			}
		}
		if h.canList(ctx, user, "horizontalpodautoscalers", "") {
			if items, err := h.Informers.HorizontalPodAutoscalers().List(sel); err == nil {
				counts["horizontalpodautoscalers"] = len(items)
			}
		}
		if h.canList(ctx, user, "poddisruptionbudgets", "") {
			if items, err := h.Informers.PodDisruptionBudgets().List(sel); err == nil {
				counts["poddisruptionbudgets"] = len(items)
			}
		}
		if h.canList(ctx, user, "endpoints", "") {
			if items, err := h.Informers.Endpoints().List(sel); err == nil {
				counts["endpoints"] = len(items)
			}
		}
		if h.canList(ctx, user, "endpointslices", "") {
			if items, err := h.Informers.EndpointSlices().List(sel); err == nil {
				counts["endpointslices"] = len(items)
			}
		}
	}

	return counts
}
