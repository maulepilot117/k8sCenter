package diagnostics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	Clients       k8s.ClusterClients
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
	Results     []resultWire `json:"results"`
	BlastRadius *BlastResult `json:"blastRadius"`
}

// resultWire is one check on the resource diagnostics wire: the legacy Result,
// whose keys and values are unchanged, plus observedAt (#595). The Flutter
// client decodes Result by key and ignores the addition.
type resultWire struct {
	Result
	// ObservedAt is the server-clock instant taken when the request's checks
	// finished evaluating: RFC 3339, UTC, in the
	// ECMAScript date-time format (exactly three fraction digits). Go's own
	// time encoding emits up to nine digits and trims trailing zeros, which
	// browsers parse only through implementation-specific fallbacks, and the
	// incident capture reads this with Date.parse.
	ObservedAt string `json:"observedAt"`
}

// observedAtLayout is resultWire.ObservedAt's format for a UTC time.
const observedAtLayout = "2006-01-02T15:04:05.000Z07:00"

// resultsWire encodes checks for the legacy wire. The legacy fields come from
// Denormalize, so the wire stays byte-compatible with what RunDiagnostics
// produced, and observedAt is each check's own: the stamp HandleDiagnostics
// takes once RunDiagnostics returns. Truncating (never rounding) to the
// millisecond keeps the encoded value at or before that stamp. A nil checks
// yields nil and an empty one an empty slice, so the response keeps encoding
// them as null and [] as it always has.
func resultsWire(checks []CheckResult) []resultWire {
	if checks == nil {
		return nil
	}
	legacy := Denormalize(checks)
	out := make([]resultWire, len(checks))
	for i, c := range checks {
		out[i] = resultWire{
			Result:     legacy[i],
			ObservedAt: c.ObservedAt.UTC().Truncate(time.Millisecond).Format(observedAtLayout),
		}
	}
	return out
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

// RuleDependsOn returns the plural resources ("pods", "replicasets") of the
// related resolutions the named rule reads for target, beyond the target
// object itself; nil for a rule that reads only the target, an unregistered
// name or a nil target. Incident capture scopes a check's evidence to the
// resource its content derives from with this (Q1: the stored scope must be
// what the read path re-authorizes).
func RuleDependsOn(rule string, target *DiagnosticTarget) []string {
	return ruleDependsOn(rule, target)
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

// summaryTruncatedMessage answers a remote namespace summary whose pod list
// exceeded the remote read cap. The summary has no field to say it is
// partial, so it is refused rather than undercounted.
const summaryTruncatedMessage = "pod list exceeded the remote read cap"

// remoteReadTimeout bounds one request's reads on a remote cluster: client
// resolution, every list and the blast-radius graph. A var so tests can
// shorten it.
var remoteReadTimeout = 10 * time.Second

// sources returns the lister and blast-radius builder for the request's
// cluster. The local cluster uses the informer-backed Lister and TopoBuilder.
// A remote cluster gets a per-request topology.RemoteLister over a client
// impersonating the user, and a builder over it with no overlay providers
// (those read local inventories); there is never a local fallback. On
// failure the response is written and ok is false.
func (h *Handler) sources(ctx context.Context, w http.ResponseWriter, user *auth.User, clusterID string) (topology.ResourceLister, *topology.Builder, bool) {
	if k8s.IsLocalClusterID(clusterID) {
		return h.Lister, h.TopoBuilder, true
	}
	if h.Clients == nil {
		h.Logger.Error("diagnostics: remote cluster requested but no cluster clients are wired", "clusterID", clusterID)
		httputil.WriteError(w, http.StatusInternalServerError, "diagnostics are not configured", "")
		return nil, nil, false
	}
	cs, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteTargetError(w, err)
		return nil, nil, false
	}
	lister := topology.NewRemoteLister(cs, h.Logger)
	return lister, topology.NewBuilder(lister, nil, h.Logger), true
}

// writeRemoteListError answers a remote list failure with a fixed message:
// forbidden is 403, anything else 502. The raw error reaches only the log.
func (h *Handler) writeRemoteListError(w http.ResponseWriter, err error, clusterID, resource, namespace string) {
	h.Logger.Error("diagnostics: remote list failed", "clusterID", clusterID, "resource", resource, "namespace", namespace, "error", err)
	if apierrors.IsForbidden(err) {
		httputil.WriteError(w, http.StatusForbidden,
			"you do not have permission to list "+resource+" in namespace "+namespace+" on the selected cluster", "")
		return
	}
	httputil.WriteError(w, http.StatusBadGateway, "failed to list "+resource+" on the selected cluster", "")
}

// HandleDiagnostics runs diagnostic checks and blast radius analysis for a resource.
// GET /api/v1/diagnostics/{namespace}/{kind}/{name}
//
// On a remote cluster the target, its related pods and the blast-radius graph
// are read directly from that cluster as the user (see sources), within
// remoteReadTimeout. A target list cut short by the read cap without the
// target in it is a 502, never a 404; related pods cut short are a
// ReasonTruncated limitation.
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

	clusterID := middleware.ClusterIDFromContext(ctx)
	if !k8s.IsLocalClusterID(clusterID) {
		var cancelRemote context.CancelFunc
		ctx, cancelRemote = context.WithTimeout(ctx, remoteReadTimeout)
		defer cancelRemote()
	}

	// RBAC check: user must be able to list the target resource kind
	resource, known := kindToResource[kind]
	if !known {
		httputil.WriteError(w, http.StatusBadRequest, "unsupported resource kind", "")
		return
	}

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

	lister, builder, ok := h.sources(ctx, w, user, clusterID)
	if !ok {
		return
	}

	// Resolve the target resource and its related pods
	target, err := Resolve(ctx, lister, namespace, kind, name, related)
	if err != nil {
		if errors.Is(err, ErrTargetNotFound) {
			httputil.WriteError(w, http.StatusNotFound, err.Error(), "")
			return
		}
		if !k8s.IsLocalClusterID(clusterID) {
			if isTruncated(err) {
				h.Logger.Warn("diagnostics: remote target list truncated", "clusterID", clusterID, "kind", kind, "name", name, "error", err)
				httputil.WriteError(w, http.StatusBadGateway,
					"too many "+resource+" in namespace "+namespace+" on the selected cluster to find "+kind+" "+name, "")
				return
			}
			h.writeRemoteListError(w, err, clusterID, resource, namespace)
			return
		}
		h.Logger.Error("failed to resolve diagnostic target", "kind", kind, "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to resolve resource", "")
		return
	}

	// Run diagnostic checks. One evaluation time, from the server clock,
	// stamps every check, as incident capture does (incidents/sources.go).
	results := RunDiagnostics(ctx, target)
	checks := Normalize(clusterID, target, time.Now().UTC(), results)

	// Build topology graph for blast radius analysis
	var blast *BlastResult
	if builder != nil {
		graph, err := builder.BuildNamespaceGraph(ctx, namespace, user, h.AccessChecker)
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
		Results:     resultsWire(checks),
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
// On a remote cluster the pods are listed directly from that cluster as the
// user (see sources), within remoteReadTimeout. The summary has no field to
// say it is partial, so a pod list cut short by the read cap is a 502
// (summaryTruncatedMessage) rather than an undercount.
func (h *Handler) HandleNamespaceSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := chi.URLParam(r, "namespace")

	user, ok := auth.UserFromContext(ctx)
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	clusterID := middleware.ClusterIDFromContext(ctx)
	if !k8s.IsLocalClusterID(clusterID) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, remoteReadTimeout)
		defer cancel()
	}

	allowed, err := h.AccessChecker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "pods", namespace)
	if err != nil {
		h.Logger.Error("RBAC check failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}
	if !allowed {
		httputil.WriteError(w, http.StatusForbidden, "insufficient permissions", "")
		return
	}

	lister, _, ok := h.sources(ctx, w, user, clusterID)
	if !ok {
		return
	}

	pods, err := lister.ListPods(ctx, namespace)
	if err != nil && !k8s.IsLocalClusterID(clusterID) {
		if isTruncated(err) {
			h.Logger.Warn("diagnostics: remote pod list truncated", "clusterID", clusterID, "namespace", namespace, "error", err)
			httputil.WriteError(w, http.StatusBadGateway, summaryTruncatedMessage, "")
			return
		}
		h.writeRemoteListError(w, err, clusterID, "pods", namespace)
		return
	}
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
