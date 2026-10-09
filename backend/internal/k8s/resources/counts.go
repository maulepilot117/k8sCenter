package resources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// HandleResourceCounts returns per-kind object counts for the selected
// cluster. Each kind is only included if the user has RBAC "list" permission
// for it on that cluster.
//
// The local cluster is counted from the informer cache. A remote cluster has
// no informers, so its kinds are listed directly from its API server, as the
// user; a failure there is never answered from the local cluster.
// GET /api/v1/resources/counts[?namespace=default]
func (h *Handler) HandleResourceCounts(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	namespace := r.URL.Query().Get("namespace")
	if !k8s.IsLocalClusterID(clusterID) {
		h.handleRemoteResourceCounts(w, r, user, clusterID, namespace)
		return
	}

	counts := h.countResources(r.Context(), user, namespace)
	writeData(w, counts)
}

// canList checks "list" permission on the local cluster. It serves the local
// dashboard (dashboard.go), whose reads all come from the local informers.
func (h *Handler) canList(ctx context.Context, user *auth.User, resource, namespace string) bool {
	return h.canListOn(ctx, k8s.LocalClusterID, user, resource, namespace)
}

// canListOn checks if the user has "list" permission for the given resource
// in the namespace on clusterID. The SAR runs on the selected cluster, exactly
// as checkAccess does. A check that fails counts as a denial (the kind is
// omitted) but is logged, so a broken check is not silent. Local path only:
// the remote counts path treats a failed check as a failed read instead
// (countRemoteResources).
func (h *Handler) canListOn(ctx context.Context, clusterID string, user *auth.User, resource, namespace string) bool {
	allowed, err := h.AccessChecker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", resource, namespace)
	if err != nil {
		h.Logger.Warn("resource counts: list permission check failed",
			"cluster", clusterID, "resource", resource, "namespace", namespace, "error", err)
		return false
	}
	return allowed
}

// countCheck is one kind the counts endpoint reports, with the namespace its
// RBAC check and list use ("" for a cluster-scoped kind or all namespaces).
type countCheck struct {
	kind string // lowercase plural: the response key and the RBAC resource
	ns   string
}

// countedKinds returns every kind the counts endpoint reports. Cluster-scoped
// kinds always use namespace "" for RBAC; namespaced kinds use namespace.
func countedKinds(namespace string) []countCheck {
	return []countCheck{
		{"nodes", ""},
		{"namespaces", ""},
		{"persistentvolumes", ""},
		{"storageclasses", ""},
		{"clusterroles", ""},
		{"clusterrolebindings", ""},

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
}

// listableKinds runs every kind's RBAC check on clusterID concurrently and
// returns the set of kinds the user may list. Local path only: the remote
// path checks each kind inside its own worker.
func (h *Handler) listableKinds(ctx context.Context, clusterID string, user *auth.User, checks []countCheck) map[string]bool {
	allowed := make([]bool, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recoverutil.Safe(h.Logger, "resources counts rbac "+c.kind, func() {
				allowed[i] = h.canListOn(ctx, clusterID, user, c.kind, c.ns)
			})
		}()
	}
	wg.Wait()

	out := make(map[string]bool, len(checks))
	for i, c := range checks {
		out[c.kind] = allowed[i]
	}
	return out
}

// countResources queries the informer cache for each tracked resource kind
// and returns a map of kind -> count. Only includes resources the user has
// RBAC "list" permission for. Local cluster only.
//
// RBAC checks are parallelized — all SelfSubjectAccessReview calls run
// concurrently, then the fast informer cache reads happen sequentially.
func (h *Handler) countResources(ctx context.Context, user *auth.User, namespace string) map[string]int {
	sel := labels.Everything()
	canListKind := h.listableKinds(ctx, k8s.LocalClusterID, user, countedKinds(namespace))

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

// remoteCountsBudget is the shared deadline for one remote counts request:
// client resolution, every kind's RBAC check and every kind's paged list. A
// variable only so a test can shorten it; nothing in production writes it.
var remoteCountsBudget = 10 * time.Second

// The fixed messages a failed remote counts read answers with. The raw error
// reaches only the log.
const (
	remoteCountsFailedMsg  = "failed to read counts on the selected cluster"
	remoteCountsTimeoutMsg = "timed out reading counts on the selected cluster"
)

// remoteCountsWorkers bounds how many kinds are listed on a remote API server
// at once.
const remoteCountsWorkers = 6

// adapterKindForCount maps a counts key (the lowercase plural API resource)
// to the registry kind of the adapter that lists it.
func adapterKindForCount(kind string) string {
	switch kind {
	case "persistentvolumes":
		return "pvs"
	case "persistentvolumeclaims":
		return "pvcs"
	case "horizontalpodautoscalers":
		return "hpas"
	case "poddisruptionbudgets":
		return "pdbs"
	default:
		return kind
	}
}

// handleRemoteResourceCounts counts every kind the user may list on a remote
// cluster by paging that cluster's API server as the user. A kind the user is
// denied, by the access review or by the cluster refusing the list, is
// omitted. Any other failure (a check that could not run, a transport error,
// the budget running out) fails the whole request: an omitted kind renders as
// zero, so a partial map would be served as if it were complete. Raw errors
// reach only the log. A kind still carrying a continue token at the paging cap
// reports the count read, and the response then sets metadata.truncated, with
// metadata.total the sum of the counts read.
func (h *Handler) handleRemoteResourceCounts(w http.ResponseWriter, r *http.Request, user *auth.User, clusterID, namespace string) {
	// The budget starts before client resolution: the cluster-store read,
	// credential decrypt and dial are part of this request too.
	ctx, cancel := context.WithTimeout(r.Context(), remoteCountsBudget)
	defer cancel()

	cs, err := h.remoteClientFor(ctx, clusterID, user)
	if err != nil {
		// No local fallback: an unresolvable remote target fails the request.
		h.Logger.Error("remote counts: resolve cluster client", "cluster", clusterID, "error", err)
		writeError(w, http.StatusBadGateway, "failed to reach the selected cluster", "")
		return
	}

	counts, truncated, err := h.countRemoteResources(ctx, cs, clusterID, user, namespace)
	if err != nil {
		h.Logger.Error("remote counts: read failed", "cluster", clusterID, "namespace", namespace, "error", err)
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, remoteCountsTimeoutMsg, "")
			return
		}
		writeError(w, http.StatusBadGateway, remoteCountsFailedMsg, "")
		return
	}
	if truncated {
		total := 0
		for _, n := range counts {
			total += n
		}
		writeJSON(w, http.StatusOK, api.Response{Data: counts, Metadata: &api.Metadata{Total: total, Truncated: true}})
		return
	}
	// Same envelope as the local path: no metadata object on a complete read.
	writeData(w, counts)
}

// remoteCountsHeavyFirst names the kinds that usually take longest to page on
// a busy cluster. They start first so the cheap kinds, not these, are the
// ones waiting on a free worker when the budget runs low.
var remoteCountsHeavyFirst = []string{
	"pods", "configmaps", "replicasets", "endpointslices", "endpoints", "clusterroles",
}

// remoteCountOrder returns checks with the remoteCountsHeavyFirst kinds moved
// to the front, the rest keeping their order. The response map does not
// depend on it.
func remoteCountOrder(checks []countCheck) []countCheck {
	rank := make(map[string]int, len(remoteCountsHeavyFirst))
	for i, kind := range remoteCountsHeavyFirst {
		rank[kind] = i
	}
	ordered := slices.Clone(checks)
	slices.SortStableFunc(ordered, func(a, b countCheck) int {
		ra, aHeavy := rank[a.kind]
		rb, bHeavy := rank[b.kind]
		switch {
		case aHeavy && bHeavy:
			return ra - rb
		case aHeavy:
			return -1
		case bHeavy:
			return 1
		}
		return 0
	})
	return ordered
}

// countRemoteResources counts every kind the user may list on cs, at most
// remoteCountsWorkers kinds at a time, and returns the counts and whether any
// kind was cut off at the paging cap. Each worker runs its kind's RBAC check
// just before the list, so a list never waits on every other kind's check; a
// denied kind is omitted. err is the first failure that was not a denial (a
// check that could not run, a list that failed, the budget expiring before a
// kind started); the counts are then incomplete and are not returned.
func (h *Handler) countRemoteResources(
	ctx context.Context, cs kubernetes.Interface, clusterID string, user *auth.User, namespace string,
) (counts map[string]int, truncated bool, err error) {
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	counts = make(map[string]int)
	fail := func(e error) {
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			err = e
		}
	}
	sem := make(chan struct{}, remoteCountsWorkers)
	for _, c := range remoteCountOrder(countedKinds(namespace)) {
		adapter := GetAdapter(adapterKindForCount(c.kind))
		if adapter == nil {
			// Guarded by TestResourceCounts_EveryKindHasAnAdapter.
			h.Logger.Error("remote counts: no adapter for kind", "kind", c.kind)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			finished := false
			recoverutil.Safe(h.Logger, "resources remote counts "+c.kind, func() {
				defer func() { finished = true }()
				// An expired budget stops the fan-out here rather than
				// sending, and logging, a doomed check per remaining kind.
				if ctxErr := ctx.Err(); ctxErr != nil {
					fail(ctxErr)
					return
				}
				// Unlike canListOn, a check that could not run is a failure,
				// not a denial: the kind's count is unknown, not absent.
				allowed, checkErr := h.AccessChecker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", c.kind, c.ns)
				if checkErr != nil {
					fail(fmt.Errorf("list permission check for %s: %w", c.kind, checkErr))
					return
				}
				if !allowed {
					return
				}
				n, cut, denied, listErr := h.countRemoteKind(ctx, cs, clusterID, adapter, c)
				switch {
				case listErr != nil:
					fail(fmt.Errorf("list %s: %w", c.kind, listErr))
					return
				case denied:
					return
				}
				mu.Lock()
				defer mu.Unlock()
				counts[c.kind] = n
				truncated = truncated || cut
			})
			if !finished {
				// recoverutil logged the panic; the kind's count is unknown.
				fail(errors.New("count " + c.kind + " panicked"))
			}
		}()
	}
	wg.Wait()
	if err != nil {
		return nil, false, err
	}
	return counts, truncated, nil
}

// countRemoteKind pages one kind's list on cs and returns its count. denied
// is set when the cluster refused the list, which omits the kind as a denied
// RBAC check does; err is any other failure, which the caller logs.
func (h *Handler) countRemoteKind(
	ctx context.Context, cs kubernetes.Interface, clusterID string, adapter ResourceAdapter, c countCheck,
) (n int, truncated, denied bool, err error) {
	items, truncated, err := k8s.PageList(ctx, metav1.ListOptions{}, func(ctx context.Context, opts metav1.ListOptions) ([]any, string, error) {
		return adapter.ListDirect(ctx, cs, c.ns, opts)
	})
	if err != nil {
		if apierrors.IsForbidden(err) {
			return 0, false, true, nil
		}
		return 0, false, false, err
	}
	if truncated {
		h.Logger.Warn("remote counts: truncated",
			"cluster", clusterID, "resource", c.kind, "namespace", c.ns, "items", len(items))
	}
	return len(items), truncated, false, nil
}
