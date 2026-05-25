package diagnostics

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/topology"
)

// Handler serves diagnostic HTTP endpoints.
type Handler struct {
	Lister        topology.ResourceLister
	TopoBuilder   *topology.Builder
	AccessChecker *resources.AccessChecker
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger
}

// diagnosticsResponse is the combined diagnostics + blast radius response.
type diagnosticsResponse struct {
	Target struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"target"`
	Results     []Result     `json:"results"`
	BlastRadius *BlastResult `json:"blastRadius"`
}

// namespaceSummaryResponse is the response for namespace-level diagnostic summary.
type namespaceSummaryResponse struct {
	Failing []failingResource `json:"failing"`
	Total   int               `json:"total"`
}

type failingResource struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// kindToResource maps Kubernetes Kind names to their plural API resource names.
var kindToResource = map[string]string{
	"Deployment":            "deployments",
	"StatefulSet":           "statefulsets",
	"DaemonSet":             "daemonsets",
	"Pod":                   "pods",
	"Service":               "services",
	"PersistentVolumeClaim": "persistentvolumeclaims",
}

// kindNeedsReplicaSets enumerates target kinds whose related-pod resolution
// walks through ReplicaSets. P3-3 security audit 2026-05-22: when the user
// can't list ReplicaSets, we must skip the chain rather than leak owner data.
var kindNeedsReplicaSets = map[string]bool{
	"Deployment": true,
}

// kindNeedsPods enumerates target kinds whose related-pod resolution lists
// pods directly. Pods themselves trivially need pod access; resource kinds
// that don't traverse to pods (PVC today) get false. P3-3 security audit
// 2026-05-22.
var kindNeedsPods = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"Pod":         true,
	"Service":     true,
}

// HandleDiagnostics runs diagnostic checks and blast radius analysis for a resource.
// GET /api/v1/diagnostics/{namespace}/{kind}/{name}
func (h *Handler) HandleDiagnostics(w http.ResponseWriter, r *http.Request) {
	// Request-scoped timeout for the entire diagnostics + topology build
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	namespace := chi.URLParam(r, "namespace")
	kind := chi.URLParam(r, "kind")
	name := chi.URLParam(r, "name")

	user, ok := auth.UserFromContext(ctx)
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	// RBAC check: user must be able to list the target resource kind
	resource, known := kindToResource[kind]
	if !known {
		httputil.WriteError(w, http.StatusBadRequest, "unsupported resource kind", "")
		return
	}

	clusterID := middleware.ClusterIDFromContext(ctx)
	allowed, err := h.AccessChecker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", resource, namespace)
	if err != nil {
		h.Logger.Error("RBAC check failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}
	if !allowed {
		httputil.WriteError(w, http.StatusForbidden, "insufficient permissions", "")
		return
	}

	// P3-3 (security audit 2026-05-22): SSAR-check every related resource type
	// the diagnostic resolver would traverse for this target kind. Without this,
	// diagnostics leak pod / ReplicaSet existence + state to users who only have
	// RBAC on the target kind. Denial is graceful — the related branch is
	// skipped, downstream rules see empty lists, and the caller still gets the
	// target-kind diagnostic findings. SSAR transport errors are logged and
	// treated as denial inside resolveRelatedRBAC (review-fix REL-003 / adv-5).
	related := h.resolveRelatedRBAC(ctx, user, clusterID, kind, namespace)

	// Resolve the target resource and its related pods
	target, err := Resolve(ctx, h.Lister, namespace, kind, name, related)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			httputil.WriteError(w, http.StatusNotFound, err.Error(), "")
			return
		}
		h.Logger.Error("failed to resolve diagnostic target", "kind", kind, "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to resolve resource", "")
		return
	}

	// Run diagnostic checks
	results := RunDiagnostics(ctx, target)

	// Build topology graph for blast radius analysis
	var blast *BlastResult
	if h.TopoBuilder != nil {
		graph, err := h.TopoBuilder.BuildNamespaceGraph(ctx, namespace, user, h.AccessChecker)
		if err != nil {
			h.Logger.Warn("failed to build topology graph for blast radius", "error", err)
		} else {
			// Find the target node ID in the graph
			targetID := findNodeID(graph, kind, name)
			if targetID != "" {
				blast = ComputeBlastRadius(graph, targetID)
			}
		}
	}

	if blast == nil {
		blast = &BlastResult{
			DirectlyAffected:    []AffectedResource{},
			PotentiallyAffected: []AffectedResource{},
		}
	}

	// Emit notifications for critical/warning diagnostic findings
	if h.NotifService != nil {
		for _, result := range results {
			if result.Status == "fail" {
				sev := notifications.SeverityWarning
				if result.Severity == SeverityCritical {
					sev = notifications.SeverityCritical
				}
				h.NotifService.Emit(ctx, notifications.Notification{
					Source:       notifications.SourceDiagnostic,
					Severity:     sev,
					Title:        result.RuleName + ": " + name,
					Message:      result.Message,
					ResourceKind: kind,
					ResourceNS:   namespace,
					ResourceName: name,
				})
			}
		}
	}

	resp := diagnosticsResponse{
		Results:     results,
		BlastRadius: blast,
	}
	resp.Target.Kind = kind
	resp.Target.Name = name
	resp.Target.Namespace = namespace

	httputil.WriteData(w, resp)
}

// HandleNamespaceSummary returns a quick diagnostic summary for a namespace.
// GET /api/v1/diagnostics/{namespace}/summary
func (h *Handler) HandleNamespaceSummary(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	allowed, err := h.AccessChecker.CanAccess(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "pods", namespace)
	if err != nil {
		h.Logger.Error("RBAC check failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}
	if !allowed {
		httputil.WriteError(w, http.StatusForbidden, "insufficient permissions", "")
		return
	}

	pods, err := h.Lister.ListPods(r.Context(), namespace)
	if err != nil {
		h.Logger.Error("failed to list pods for namespace summary", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list pods", "")
		return
	}

	var failing []failingResource
	for _, pod := range pods {
		if reason := podFailureReason(pod); reason != "" {
			failing = append(failing, failingResource{
				Kind:   "Pod",
				Name:   pod.Name,
				Reason: reason,
			})
		}
	}

	if failing == nil {
		failing = []failingResource{}
	}

	httputil.WriteData(w, namespaceSummaryResponse{
		Failing: failing,
		Total:   len(pods),
	})
}

// resolveRelatedRBAC computes which related-resource resolutions the user is
// permitted to perform for the given target kind in the given namespace. Each
// permission is checked via SSAR against the request's cluster context.
//
// SSAR transport errors (apiserver unreachable, RBAC webhook outage) are
// logged and treated as denial rather than failing the request — this matches
// the documented graceful-degradation contract (the related branch is skipped,
// downstream rules see an empty list, target-kind findings still surface). The
// alternative (return error → HTTP 500) would convert a transient apiserver
// blip into a hard failure for every diagnostic request, which is strictly
// worse than a temporarily reduced result set. P3-3 review-fix REL-003 / adv-5
// (security audit 2026-05-22).
func (h *Handler) resolveRelatedRBAC(ctx context.Context, user *auth.User, clusterID, kind, namespace string) *RelatedRBAC {
	related := &RelatedRBAC{}

	if kindNeedsPods[kind] {
		allowed, err := h.AccessChecker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "pods", namespace)
		if err != nil {
			h.Logger.Warn("related-pod RBAC check failed; treating as denied", "kind", kind, "namespace", namespace, "error", err)
		} else {
			related.Pods = allowed
		}
	}

	if kindNeedsReplicaSets[kind] {
		allowed, err := h.AccessChecker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "replicasets", namespace)
		if err != nil {
			h.Logger.Warn("related-replicaset RBAC check failed; treating as denied", "kind", kind, "namespace", namespace, "error", err)
		} else {
			related.ReplicaSets = allowed
		}
	}

	return related
}

// findNodeID searches the graph for a node matching the given kind and name.
func findNodeID(graph *topology.Graph, kind, name string) string {
	for _, node := range graph.Nodes {
		if node.Kind == kind && node.Name == name {
			return node.ID
		}
	}
	return ""
}

// podFailureReason returns the failure reason for a pod, or empty if healthy.
func podFailureReason(pod *corev1.Pod) string {
	if pod.Status.Phase == corev1.PodPending {
		return "Pending"
	}

	// Check both regular and init container statuses (do NOT use append on
	// the pod's slice — it could mutate the informer cache).
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull":
				return cs.State.Waiting.Reason
			}
		}
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull":
				return cs.State.Waiting.Reason
			}
		}
	}

	return ""
}
