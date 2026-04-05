package diagnostics

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/topology"
)

// Handler serves diagnostic HTTP endpoints.
type Handler struct {
	Lister        topology.ResourceLister
	TopoBuilder   *topology.Builder
	AccessChecker *resources.AccessChecker
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

// HandleDiagnostics runs diagnostic checks and blast radius analysis for a resource.
// GET /api/v1/diagnostics/{namespace}/{kind}/{name}
func (h *Handler) HandleDiagnostics(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")
	kind := chi.URLParam(r, "kind")
	name := chi.URLParam(r, "name")

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	// RBAC check: user must be able to list the target resource kind
	resource, known := kindToResource[kind]
	if !known {
		httputil.WriteError(w, http.StatusBadRequest, "unsupported resource kind: "+kind, "")
		return
	}

	allowed, err := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", resource, namespace)
	if err != nil {
		h.Logger.Error("RBAC check failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}
	if !allowed {
		httputil.WriteError(w, http.StatusForbidden, "insufficient permissions", "")
		return
	}

	// Resolve the target resource and its related pods
	target, err := Resolve(r.Context(), h.Lister, namespace, kind, name)
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
	results := RunDiagnostics(r.Context(), target)

	// Build topology graph for blast radius analysis
	var blast *BlastResult
	if h.TopoBuilder != nil {
		graph, err := h.TopoBuilder.BuildNamespaceGraph(r.Context(), namespace, user, h.AccessChecker)
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

	allowed, err := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "pods", namespace)
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

	allStatuses := append(pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses...)
	for _, cs := range allStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull":
				return cs.State.Waiting.Reason
			}
		}
	}

	return ""
}
