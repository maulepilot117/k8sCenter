package diagnostics

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
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

// TargetResource returns the API group, version and plural resource of a
// kind diagnostics can resolve, and false for any other kind. It is the
// exported view of kindToResource for callers (incident capture) that must
// SAR-gate a target with the same identity diagnostics use.
func TargetResource(kind string) (group, version, resource string, ok bool) {
	resource, ok = kindToResource[kind]
	if !ok {
		return "", "", "", false
	}
	gv := kindGroupVersion[kind]
	return gv.Group, gv.Version, resource, true
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

// remoteUnsupportedMessage is the fixed refusal for a diagnostics request
// under a remote cluster selection.
const remoteUnsupportedMessage = "resource diagnostics are available for the local cluster only"

// refuseRemote answers 501 unsupported_platform and returns true when the
// request targets a remote cluster (#532). Diagnostics resolve the target,
// its related pods and the blast-radius graph from the local cluster's
// informers (Lister, TopoBuilder), which remote clusters do not have, so
// answering would report the local cluster's state under the remote
// cluster's name. It runs before any lister read, SAR or notification. The
// capability row diagnostics.read declares the same.
func (h *Handler) refuseRemote(w http.ResponseWriter, r *http.Request) bool {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if k8s.IsLocalClusterID(clusterID) {
		return false
	}
	// The cluster id goes to the log only, never onto the wire.
	h.Logger.Info("diagnostics refused on remote cluster", "clusterID", clusterID, "path", r.URL.Path)
	httputil.WriteErrorWithReason(w, http.StatusNotImplemented, remoteUnsupportedMessage,
		string(k8s.ReasonUnsupportedPlatform), nil)
	return true
}

// HandleDiagnostics runs diagnostic checks and blast radius analysis for a resource.
// GET /api/v1/diagnostics/{namespace}/{kind}/{name}
//
// Refused on a remote cluster (see refuseRemote).
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

	if h.refuseRemote(w, r) {
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
				h.NotifService.Emit(ctx, findingNotification(clusterID, target, result))
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
//
// Refused on a remote cluster (see refuseRemote).
func (h *Handler) HandleNamespaceSummary(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	if h.refuseRemote(w, r) {
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
	return ResolveRelatedRBAC(ctx, h.AccessChecker, h.Logger, user, clusterID, kind, namespace)
}

// ResolveRelatedRBAC is resolveRelatedRBAC for callers outside the HTTP
// handler (incident capture runs the same resolution so its diagnostic
// evidence is gated exactly as the diagnostics endpoint is). A nil logger
// falls back to slog.Default().
func ResolveRelatedRBAC(ctx context.Context, ac *resources.AccessChecker, logger *slog.Logger, user *auth.User, clusterID, kind, namespace string) *RelatedRBAC {
	if logger == nil {
		logger = slog.Default()
	}
	related := &RelatedRBAC{}

	if kindNeedsPods[kind] {
		allowed, err := ac.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "pods", namespace)
		if err != nil {
			logger.Warn("related-pod RBAC check failed; treating as denied", "kind", kind, "namespace", namespace, "error", err)
		} else {
			related.Pods = allowed
		}
	}

	if kindNeedsReplicaSets[kind] {
		allowed, err := ac.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "replicasets", namespace)
		if err != nil {
			logger.Warn("related-replicaset RBAC check failed; treating as denied", "kind", kind, "namespace", namespace, "error", err)
		} else {
			related.ReplicaSets = allowed
		}
	}

	return related
}

// findingNotification builds the notification for one failed diagnostic
// result. clusterID and the target object's UID are part of the
// notification dedup identity: the same name on another cluster, or a
// resource recreated under the same name, is a separate event. A target
// without a readable object contributes no UID.
func findingNotification(clusterID string, target *DiagnosticTarget, result Result) notifications.Notification {
	sev := notifications.SeverityWarning
	if result.Severity == SeverityCritical {
		sev = notifications.SeverityCritical
	}
	var uid string
	if target.Object != nil {
		if obj, err := meta.Accessor(target.Object); err == nil {
			uid = string(obj.GetUID())
		}
	}
	return notifications.Notification{
		Source:       notifications.SourceDiagnostic,
		Severity:     sev,
		Title:        result.RuleName + ": " + target.Name,
		Message:      result.Message,
		ResourceKind: target.Kind,
		ResourceNS:   target.Namespace,
		ResourceName: target.Name,
		ResourceUID:  uid,
		ClusterID:    clusterID,
	}
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
