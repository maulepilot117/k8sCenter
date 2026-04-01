package resources

import (
	"context"
	"net/http"
	"sync"

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
// RBAC checks are parallelized — all SelfSubjectAccessReview calls run
// concurrently, then the fast informer cache reads happen sequentially.
func (h *Handler) countResources(ctx context.Context, user *auth.User, namespace string) map[string]int {
	sel := labels.Everything()

	// Build the list of resources to check. Cluster-scoped resources always
	// use namespace="" for RBAC; namespace-scoped use the provided namespace.
	type resourceCheck struct {
		kind string
		ns   string // namespace for RBAC check
	}

	clusterScoped := []resourceCheck{
		{"nodes", ""},
		{"namespaces", ""},
		{"persistentvolumes", ""},
		{"storageclasses", ""},
		{"clusterroles", ""},
		{"clusterrolebindings", ""},
	}

	nsScoped := []resourceCheck{
		{"deployments", namespace},
		{"statefulsets", namespace},
		{"daemonsets", namespace},
		{"pods", namespace},
		{"jobs", namespace},
		{"cronjobs", namespace},
		{"replicasets", namespace},
		{"services", namespace},
		{"ingresses", namespace},
		{"networkpolicies", namespace},
		{"configmaps", namespace},
		// Secrets intentionally skipped (not in informer cache).
		{"serviceaccounts", namespace},
		{"resourcequotas", namespace},
		{"limitranges", namespace},
		{"persistentvolumeclaims", namespace},
		{"roles", namespace},
		{"rolebindings", namespace},
		{"horizontalpodautoscalers", namespace},
		{"poddisruptionbudgets", namespace},
		{"endpoints", namespace},
		{"endpointslices", namespace},
	}

	allChecks := append(clusterScoped, nsScoped...)

	// Run all RBAC checks concurrently.
	allowed := make([]bool, len(allChecks))
	var wg sync.WaitGroup
	wg.Add(len(allChecks))
	for i, rc := range allChecks {
		go func(idx int, kind, ns string) {
			defer wg.Done()
			allowed[idx] = h.canList(ctx, user, kind, ns)
		}(i, rc.kind, rc.ns)
	}
	wg.Wait()

	// Build permission set from parallel results.
	canListKind := make(map[string]bool, len(allChecks))
	for i, rc := range allChecks {
		canListKind[rc.kind] = allowed[i]
	}

	counts := make(map[string]int)

	// --- Cluster-scoped resources ---

	if canListKind["nodes"] {
		if items, err := h.Informers.Nodes().List(sel); err == nil {
			counts["nodes"] = len(items)
		}
	}
	if canListKind["namespaces"] {
		if items, err := h.Informers.Namespaces().List(sel); err == nil {
			counts["namespaces"] = len(items)
		}
	}
	if canListKind["persistentvolumes"] {
		if items, err := h.Informers.PersistentVolumes().List(sel); err == nil {
			counts["persistentvolumes"] = len(items)
		}
	}
	if canListKind["storageclasses"] {
		if items, err := h.Informers.StorageClasses().List(sel); err == nil {
			counts["storageclasses"] = len(items)
		}
	}
	if canListKind["clusterroles"] {
		if items, err := h.Informers.ClusterRoles().List(sel); err == nil {
			counts["clusterroles"] = len(items)
		}
	}
	if canListKind["clusterrolebindings"] {
		if items, err := h.Informers.ClusterRoleBindings().List(sel); err == nil {
			counts["clusterrolebindings"] = len(items)
		}
	}

	// --- Namespace-scoped resources ---

	if namespace != "" {
		if canListKind["deployments"] {
			if items, err := h.Informers.Deployments().Deployments(namespace).List(sel); err == nil {
				counts["deployments"] = len(items)
			}
		}
		if canListKind["statefulsets"] {
			if items, err := h.Informers.StatefulSets().StatefulSets(namespace).List(sel); err == nil {
				counts["statefulsets"] = len(items)
			}
		}
		if canListKind["daemonsets"] {
			if items, err := h.Informers.DaemonSets().DaemonSets(namespace).List(sel); err == nil {
				counts["daemonsets"] = len(items)
			}
		}
		if canListKind["pods"] {
			if items, err := h.Informers.Pods().Pods(namespace).List(sel); err == nil {
				counts["pods"] = len(items)
			}
		}
		if canListKind["jobs"] {
			if items, err := h.Informers.Jobs().Jobs(namespace).List(sel); err == nil {
				counts["jobs"] = len(items)
			}
		}
		if canListKind["cronjobs"] {
			if items, err := h.Informers.CronJobs().CronJobs(namespace).List(sel); err == nil {
				counts["cronjobs"] = len(items)
			}
		}
		if canListKind["replicasets"] {
			if items, err := h.Informers.ReplicaSets().ReplicaSets(namespace).List(sel); err == nil {
				counts["replicasets"] = len(items)
			}
		}
		if canListKind["services"] {
			if items, err := h.Informers.Services().Services(namespace).List(sel); err == nil {
				counts["services"] = len(items)
			}
		}
		if canListKind["ingresses"] {
			if items, err := h.Informers.Ingresses().Ingresses(namespace).List(sel); err == nil {
				counts["ingresses"] = len(items)
			}
		}
		if canListKind["networkpolicies"] {
			if items, err := h.Informers.NetworkPolicies().NetworkPolicies(namespace).List(sel); err == nil {
				counts["networkpolicies"] = len(items)
			}
		}
		if canListKind["configmaps"] {
			if items, err := h.Informers.ConfigMaps().ConfigMaps(namespace).List(sel); err == nil {
				counts["configmaps"] = len(items)
			}
		}
		if canListKind["serviceaccounts"] {
			if items, err := h.Informers.ServiceAccounts().ServiceAccounts(namespace).List(sel); err == nil {
				counts["serviceaccounts"] = len(items)
			}
		}
		if canListKind["resourcequotas"] {
			if items, err := h.Informers.ResourceQuotas().ResourceQuotas(namespace).List(sel); err == nil {
				counts["resourcequotas"] = len(items)
			}
		}
		if canListKind["limitranges"] {
			if items, err := h.Informers.LimitRanges().LimitRanges(namespace).List(sel); err == nil {
				counts["limitranges"] = len(items)
			}
		}
		if canListKind["persistentvolumeclaims"] {
			if items, err := h.Informers.PersistentVolumeClaims().PersistentVolumeClaims(namespace).List(sel); err == nil {
				counts["persistentvolumeclaims"] = len(items)
			}
		}
		if canListKind["roles"] {
			if items, err := h.Informers.Roles().Roles(namespace).List(sel); err == nil {
				counts["roles"] = len(items)
			}
		}
		if canListKind["rolebindings"] {
			if items, err := h.Informers.RoleBindings().RoleBindings(namespace).List(sel); err == nil {
				counts["rolebindings"] = len(items)
			}
		}
		if canListKind["horizontalpodautoscalers"] {
			if items, err := h.Informers.HorizontalPodAutoscalers().HorizontalPodAutoscalers(namespace).List(sel); err == nil {
				counts["horizontalpodautoscalers"] = len(items)
			}
		}
		if canListKind["poddisruptionbudgets"] {
			if items, err := h.Informers.PodDisruptionBudgets().PodDisruptionBudgets(namespace).List(sel); err == nil {
				counts["poddisruptionbudgets"] = len(items)
			}
		}
		if canListKind["endpoints"] {
			if items, err := h.Informers.Endpoints().Endpoints(namespace).List(sel); err == nil {
				counts["endpoints"] = len(items)
			}
		}
		if canListKind["endpointslices"] {
			if items, err := h.Informers.EndpointSlices().EndpointSlices(namespace).List(sel); err == nil {
				counts["endpointslices"] = len(items)
			}
		}
	} else {
		if canListKind["deployments"] {
			if items, err := h.Informers.Deployments().List(sel); err == nil {
				counts["deployments"] = len(items)
			}
		}
		if canListKind["statefulsets"] {
			if items, err := h.Informers.StatefulSets().List(sel); err == nil {
				counts["statefulsets"] = len(items)
			}
		}
		if canListKind["daemonsets"] {
			if items, err := h.Informers.DaemonSets().List(sel); err == nil {
				counts["daemonsets"] = len(items)
			}
		}
		if canListKind["pods"] {
			if items, err := h.Informers.Pods().List(sel); err == nil {
				counts["pods"] = len(items)
			}
		}
		if canListKind["jobs"] {
			if items, err := h.Informers.Jobs().List(sel); err == nil {
				counts["jobs"] = len(items)
			}
		}
		if canListKind["cronjobs"] {
			if items, err := h.Informers.CronJobs().List(sel); err == nil {
				counts["cronjobs"] = len(items)
			}
		}
		if canListKind["replicasets"] {
			if items, err := h.Informers.ReplicaSets().List(sel); err == nil {
				counts["replicasets"] = len(items)
			}
		}
		if canListKind["services"] {
			if items, err := h.Informers.Services().List(sel); err == nil {
				counts["services"] = len(items)
			}
		}
		if canListKind["ingresses"] {
			if items, err := h.Informers.Ingresses().List(sel); err == nil {
				counts["ingresses"] = len(items)
			}
		}
		if canListKind["networkpolicies"] {
			if items, err := h.Informers.NetworkPolicies().List(sel); err == nil {
				counts["networkpolicies"] = len(items)
			}
		}
		if canListKind["configmaps"] {
			if items, err := h.Informers.ConfigMaps().List(sel); err == nil {
				counts["configmaps"] = len(items)
			}
		}
		if canListKind["serviceaccounts"] {
			if items, err := h.Informers.ServiceAccounts().List(sel); err == nil {
				counts["serviceaccounts"] = len(items)
			}
		}
		if canListKind["resourcequotas"] {
			if items, err := h.Informers.ResourceQuotas().List(sel); err == nil {
				counts["resourcequotas"] = len(items)
			}
		}
		if canListKind["limitranges"] {
			if items, err := h.Informers.LimitRanges().List(sel); err == nil {
				counts["limitranges"] = len(items)
			}
		}
		if canListKind["persistentvolumeclaims"] {
			if items, err := h.Informers.PersistentVolumeClaims().List(sel); err == nil {
				counts["persistentvolumeclaims"] = len(items)
			}
		}
		if canListKind["roles"] {
			if items, err := h.Informers.Roles().List(sel); err == nil {
				counts["roles"] = len(items)
			}
		}
		if canListKind["rolebindings"] {
			if items, err := h.Informers.RoleBindings().List(sel); err == nil {
				counts["rolebindings"] = len(items)
			}
		}
		if canListKind["horizontalpodautoscalers"] {
			if items, err := h.Informers.HorizontalPodAutoscalers().List(sel); err == nil {
				counts["horizontalpodautoscalers"] = len(items)
			}
		}
		if canListKind["poddisruptionbudgets"] {
			if items, err := h.Informers.PodDisruptionBudgets().List(sel); err == nil {
				counts["poddisruptionbudgets"] = len(items)
			}
		}
		if canListKind["endpoints"] {
			if items, err := h.Informers.Endpoints().List(sel); err == nil {
				counts["endpoints"] = len(items)
			}
		}
		if canListKind["endpointslices"] {
			if items, err := h.Informers.EndpointSlices().List(sel); err == nil {
				counts["endpointslices"] = len(items)
			}
		}
	}

	return counts
}
