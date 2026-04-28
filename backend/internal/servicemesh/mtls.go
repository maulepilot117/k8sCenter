package servicemesh

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/prometheus/common/model"

	"github.com/kubecenter/kubecenter/internal/monitoring"
)

// MTLSState is the three-state posture surfaced to the UI. "unmeshed" is
// a fourth value so the UI can distinguish "opted out" from "broken" —
// the plan's explicit guidance for Unit C4.
type MTLSState string

const (
	MTLSActive   MTLSState = "active"
	MTLSInactive MTLSState = "inactive"
	MTLSMixed    MTLSState = "mixed"
	MTLSUnmeshed MTLSState = "unmeshed"
)

// MTLSSource explains where the posture decision came from. When a metric
// cross-check overrides a policy-derived decision, Source flips to "metric".
type MTLSSource string

const (
	MTLSSourcePolicy  MTLSSource = "policy"
	MTLSSourceMetric  MTLSSource = "metric"
	MTLSSourceDefault MTLSSource = "default"
)

// Istio PeerAuthentication modes. The spec field value is a string; we
// keep a typed set so the precedence resolver can switch exhaustively.
const (
	IstioMTLSStrict     = "STRICT"
	IstioMTLSPermissive = "PERMISSIVE"
	IstioMTLSDisable    = "DISABLE"
	// IstioMTLSUnset is the sentinel when no PeerAuthentication applies.
	// Istio's published default is PERMISSIVE in that case, so the resolver
	// treats UNSET as PERMISSIVE semantically but keeps the token for source
	// disambiguation on the wire.
	IstioMTLSUnset = "UNSET"
)

// linkerdProxyAnnotation marks a pod as meshed by Linkerd. Presence is
// the canonical "this pod is in the mesh" signal.
const linkerdProxyAnnotation = "linkerd.io/proxy-version"

// istioMeshRootNamespace is the namespace whose empty-selector
// PeerAuthentication acts as the mesh-wide default. Matches Istio's
// install default; the plan explicitly limits v1 scope to this convention.
const istioMeshRootNamespace = "istio-system"

// WorkloadMTLS is a single workload's posture entry.
type WorkloadMTLS struct {
	Namespace    string     `json:"namespace"`
	Workload     string     `json:"workload"`
	WorkloadKind string     `json:"workloadKind,omitempty"`
	Mesh         MeshType   `json:"mesh"`
	State        MTLSState  `json:"state"`
	Source       MTLSSource `json:"source"`
	// IstioMode is the resolved PeerAuthentication mode
	// (STRICT/PERMISSIVE/DISABLE/UNSET). Empty for Linkerd.
	IstioMode string `json:"istioMode,omitempty"`
	// SourceDetail names the scope of the winning PeerAuthentication:
	// "workload", "namespace", or "mesh". Empty for Linkerd, for the
	// UNSET default, or for metric-driven decisions.
	SourceDetail string `json:"sourceDetail,omitempty"`
}

// MTLSPostureResponse is the envelope for GET /mesh/mtls.
type MTLSPostureResponse struct {
	Status    MeshStatus        `json:"status"`
	Workloads []WorkloadMTLS    `json:"workloads"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// peerAuthRef is the precedence-resolver-friendly shape extracted from
// a PeerAuthentication. We keep the raw Selector map (for equality and
// tests) plus a compiled labels.Selector so selectorMatches avoids
// reallocating one per (pod, PA) pair inside computePodPostures.
type peerAuthRef struct {
	Namespace string
	Name      string
	Mode      string
	// Selector is the PA's spec.selector.matchLabels. Nil/empty means the
	// PA applies to the whole namespace (or whole mesh when Namespace is
	// the mesh-root namespace).
	Selector map[string]string
	// compiled caches labels.SelectorFromSet(Selector). Populated by
	// peerAuthsFromPolicies; nil in tests that construct peerAuthRef
	// directly (selectorMatches falls back to just-in-time compilation).
	compiled labels.Selector
}

// resolveIstioMTLSMode applies Istio's three-level precedence:
//
//	workload (matching selector in pod NS) > namespace (empty selector in pod NS) > mesh (empty selector in mesh root).
//
// Returns the winning mode and a sourceDetail string for the response.
// When no PA applies, returns (IstioMTLSUnset, "") so the caller can
// classify the pod as PERMISSIVE-by-default.
func resolveIstioMTLSMode(podLabels map[string]string, podNS, meshRootNS string, peerAuths []peerAuthRef) (mode, sourceDetail string) {
	var namespacePA, meshPA *peerAuthRef
	// Workload-level PAs can fan out; the precedence within the same
	// scope is implementation-defined, so pick the first match in a
	// stable iteration (caller pre-sorts) to keep results deterministic.
	for i := range peerAuths {
		pa := peerAuths[i]
		switch {
		case pa.Namespace == podNS && len(pa.Selector) > 0:
			if peerAuths[i].matches(podLabels) {
				return pa.Mode, "workload"
			}
		case pa.Namespace == podNS && len(pa.Selector) == 0:
			if namespacePA == nil {
				namespacePA = &peerAuths[i]
			}
		case pa.Namespace == meshRootNS && len(pa.Selector) == 0:
			if meshPA == nil {
				meshPA = &peerAuths[i]
			}
		}
	}
	if namespacePA != nil {
		return namespacePA.Mode, "namespace"
	}
	if meshPA != nil {
		return meshPA.Mode, "mesh"
	}
	return IstioMTLSUnset, ""
}

// selectorMatches returns true when every label in selector is present
// in podLabels with the same value. Istio PA matchLabels are AND-ed.
func selectorMatches(selector, podLabels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	return labels.SelectorFromSet(selector).Matches(labels.Set(podLabels))
}

// matches is the peerAuthRef-scoped selector check. It prefers the
// pre-compiled selector when peerAuthsFromPolicies populated one;
// otherwise it falls back to the allocating helper. Production code
// paths always hit the compiled branch — the fallback exists so tests
// constructing peerAuthRef literals stay legible.
func (p *peerAuthRef) matches(podLabels map[string]string) bool {
	if len(p.Selector) == 0 {
		return false
	}
	if p.compiled != nil {
		return p.compiled.Matches(labels.Set(podLabels))
	}
	return selectorMatches(p.Selector, podLabels)
}

// modeToState collapses a resolved PeerAuthentication mode to the
// unified three-state MTLSState. UNSET (no PA) and PERMISSIVE both
// resolve to inactive: Istio's published default is PERMISSIVE.
func modeToState(mode string) MTLSState {
	switch mode {
	case IstioMTLSStrict:
		return MTLSActive
	case IstioMTLSPermissive, IstioMTLSDisable, IstioMTLSUnset, "":
		return MTLSInactive
	}
	// Unknown future mode: fail closed so the UI surfaces an explicit
	// inactive rather than silently promoting to active.
	return MTLSInactive
}

// linkerdPodState returns active when the pod carries the Linkerd proxy
// annotation, unmeshed otherwise. Linkerd has no PERMISSIVE/DISABLE
// equivalents — mTLS is default-on whenever the sidecar is injected.
func linkerdPodState(pod *corev1.Pod) MTLSState {
	if _, ok := pod.Annotations[linkerdProxyAnnotation]; ok {
		return MTLSActive
	}
	return MTLSUnmeshed
}

// workloadKey identifies the top-level controller for a pod. We walk
// one level of OwnerReferences, unwrapping a Deployment-owned
// ReplicaSet via the kube-controller "-<hash>" suffix convention.
// Orphan ReplicaSets and user-named RSes whose suffix is not a real
// pod-template-hash are reported verbatim as ReplicaSet so we never
// fabricate a Deployment that isn't there. Orphan pods key under
// ("Pod", <podName>).
func workloadKey(pod *corev1.Pod) (kind, name string) {
	for _, or := range pod.OwnerReferences {
		switch or.Kind {
		case "ReplicaSet":
			if idx := strings.LastIndex(or.Name, "-"); idx > 0 && isReplicaSetHashSuffix(or.Name[idx+1:]) {
				return "Deployment", or.Name[:idx]
			}
			return "ReplicaSet", or.Name
		case "StatefulSet", "DaemonSet", "Job", "CronJob":
			return or.Kind, or.Name
		}
	}
	return "Pod", pod.Name
}

// isReplicaSetHashSuffix reports whether s looks like a kube-controller
// pod-template-hash. Modern Deployment hashes are produced by
// k8s.io/apimachinery/pkg/util/rand.SafeEncodeString, which translates
// every byte through a 27-character alphabet that excludes vowels and
// the digits 0/1/3 (avoids generating profanity-like strings). The
// length tracks fmt.Sprint(uint32) — never more than 10 chars — and we
// floor at 5 to match the shortest hash kube-controller is observed to
// produce in real clusters. A match is a strong signal that the "-"
// stripped from a ReplicaSet name belongs to a Deployment; a miss
// means the suffix is a user-chosen string ("worker-v1", "app-12345")
// and the name should be treated as the workload itself.
func isReplicaSetHashSuffix(s string) bool {
	if len(s) < 5 || len(s) > 10 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 'b', 'c', 'd', 'f', 'g', 'h', 'j', 'k', 'l', 'm',
			'n', 'p', 'q', 'r', 's', 't', 'v', 'w', 'x', 'z',
			'2', '4', '5', '6', '7', '8', '9':
			continue
		}
		return false
	}
	return true
}

// podMeshMembership returns which mesh (if any) a pod is a member of.
// Sidecar-container name is the authoritative signal; annotations are
// checked as a fallback so ambient Istio pods (no sidecar) still count.
//
// Known v1 scope limits (tracked in the plan's scope-boundaries section):
//   - Istio Ambient-mode pods that have neither the istio-proxy sidecar
//     nor the sidecar.istio.io/status annotation are classified as
//     unmeshed. ztunnel handles L4 mTLS out-of-band, so a dedicated
//     ambient-aware view is deferred beyond Phase B.
//   - Linkerd workloads do not have a Prometheus cross-check equivalent
//     to Istio's queryIstioMTLSRatios. A crashed linkerd-proxy will
//     continue to report MTLSActive with Source=policy until the pod is
//     restarted. Surfacing proxy-crash state is Phase D work.
func podMeshMembership(pod *corev1.Pod) MeshType {
	for _, c := range pod.Spec.Containers {
		switch c.Name {
		case "istio-proxy":
			return MeshIstio
		case "linkerd-proxy":
			return MeshLinkerd
		}
	}
	if _, ok := pod.Annotations[linkerdProxyAnnotation]; ok {
		return MeshLinkerd
	}
	if v, ok := pod.Annotations["sidecar.istio.io/status"]; ok && v != "" {
		return MeshIstio
	}
	return MeshNone
}

// peerAuthsFromPolicies filters normalized MeshedPolicy entries down to
// Istio PeerAuthentications and projects them into peerAuthRef. We
// re-read the selector from Raw since MeshedPolicy.Selector is a
// stringified form that discards the original map.
func peerAuthsFromPolicies(policies []MeshedPolicy) []peerAuthRef {
	out := make([]peerAuthRef, 0)
	for _, p := range policies {
		if p.Mesh != MeshIstio || p.Kind != "PeerAuthentication" {
			continue
		}
		mode := p.MTLSMode
		if mode == "" {
			mode = IstioMTLSUnset
		}
		selector, _, _ := unstructured.NestedStringMap(p.Raw, "spec", "selector", "matchLabels")
		ref := peerAuthRef{
			Namespace: p.Namespace,
			Name:      p.Name,
			Mode:      mode,
			Selector:  selector,
		}
		if len(selector) > 0 {
			ref.compiled = labels.SelectorFromSet(selector)
		}
		out = append(out, ref)
	}
	// Deterministic ordering: workload-scoped first within each namespace
	// (so a workload PA beats a namespace PA when both match via the
	// precedence resolver's selector check), then by name.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		iw := len(out[i].Selector) > 0
		jw := len(out[j].Selector) > 0
		if iw != jw {
			return iw
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// computePodPostures runs the precedence resolver per pod and produces a
// per-pod intermediate result. Workload aggregation happens in
// aggregateWorkloads so the Prom cross-check can run between the two
// passes without coupling to the resolver.
type podPosture struct {
	Namespace    string
	PodName      string
	PodLabels    map[string]string
	WorkloadKind string
	Workload     string
	Mesh         MeshType
	State        MTLSState
	Source       MTLSSource
	IstioMode    string
	SourceDetail string
}

func computePodPostures(pods []corev1.Pod, peerAuths []peerAuthRef) []podPosture {
	out := make([]podPosture, 0, len(pods))
	for i := range pods {
		pod := &pods[i]
		mesh := podMeshMembership(pod)
		kind, name := workloadKey(pod)

		pp := podPosture{
			Namespace:    pod.Namespace,
			PodName:      pod.Name,
			PodLabels:    pod.Labels,
			WorkloadKind: kind,
			Workload:     name,
			Mesh:         mesh,
		}

		switch mesh {
		case MeshIstio:
			mode, detail := resolveIstioMTLSMode(pod.Labels, pod.Namespace, istioMeshRootNamespace, peerAuths)
			pp.State = modeToState(mode)
			pp.IstioMode = mode
			pp.SourceDetail = detail
			if detail == "" {
				pp.Source = MTLSSourceDefault
			} else {
				pp.Source = MTLSSourcePolicy
			}
		case MeshLinkerd:
			pp.State = linkerdPodState(pod)
			pp.Source = MTLSSourcePolicy
		default:
			pp.State = MTLSUnmeshed
			pp.Source = MTLSSourceDefault
		}

		out = append(out, pp)
	}
	return out
}

// aggregateWorkloads groups pod postures by (namespace, workloadKind,
// workload) and resolves a single state per workload. Mixed membership
// (some active, some inactive) collapses to MTLSMixed.
func aggregateWorkloads(postures []podPosture) []WorkloadMTLS {
	type key struct {
		ns, kind, name string
	}
	groups := map[key][]podPosture{}
	order := []key{}
	for _, pp := range postures {
		k := key{pp.Namespace, pp.WorkloadKind, pp.Workload}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], pp)
	}

	out := make([]WorkloadMTLS, 0, len(groups))
	for _, k := range order {
		members := groups[k]
		// Start from the first pod and widen to mixed if any peer disagrees.
		first := members[0]
		agg := WorkloadMTLS{
			Namespace:    first.Namespace,
			Workload:     first.Workload,
			WorkloadKind: first.WorkloadKind,
			Mesh:         first.Mesh,
			State:        first.State,
			Source:       first.Source,
			IstioMode:    first.IstioMode,
			SourceDetail: first.SourceDetail,
		}
		for _, m := range members[1:] {
			if m.State != agg.State {
				agg.State = MTLSMixed
				// Mesh divergence across pods of the same workload is
				// pathological; keep the initial mesh label so the row
				// still renders.
			}
		}
		out = append(out, agg)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Workload < out[j].Workload
	})
	return out
}

// IstioMTLSRatio is the per-workload mTLS fraction reported by Prometheus.
// Total is rps across all connection_security_policy values; MTLS is just
// the mutual_tls subset. Ratio = MTLS/Total, 0 when Total == 0.
type IstioMTLSRatio struct {
	Namespace string
	Workload  string
	MTLS      float64
	Total     float64
}

// Ratio returns the mTLS fraction; 0 when no traffic was observed.
func (r IstioMTLSRatio) Ratio() float64 {
	if r.Total == 0 {
		return 0
	}
	return r.MTLS / r.Total
}

// queryIstioMTLSRatios runs one PromQL instant query per namespace to
// learn observed mTLS ratios by workload. Uses the shared
// promQueryTimeout budget (see metrics.go) so both Phase B Prom calls
// share one knob and age together; callers must tolerate a nil return.
func queryIstioMTLSRatios(ctx context.Context, pc *monitoring.PrometheusClient, namespace string) ([]IstioMTLSRatio, error) {
	if pc == nil {
		return nil, nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, promQueryTimeout)
	defer cancel()

	// Namespace is user-supplied; the existing monitoring.QueryTemplate
	// validator rejects anything that isn't a k8s-valid name, so we run
	// it through Render for defence in depth.
	tmpl := monitoring.QueryTemplate{
		Query: `sum by (destination_workload, destination_workload_namespace, connection_security_policy) (rate(istio_requests_total{destination_workload_namespace="$ns"}[5m]))`,
		Variables: []string{"ns"},
	}
	q, err := tmpl.Render(map[string]string{"ns": namespace})
	if err != nil {
		return nil, fmt.Errorf("render istio mtls query: %w", err)
	}
	val, _, err := pc.Query(queryCtx, q, time.Now())
	if err != nil {
		return nil, err
	}
	vec, ok := val.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected prometheus result type %T", val)
	}
	agg := map[string]*IstioMTLSRatio{}
	for _, s := range vec {
		workload := string(s.Metric["destination_workload"])
		ns := string(s.Metric["destination_workload_namespace"])
		policy := string(s.Metric["connection_security_policy"])
		if workload == "" {
			continue
		}
		key := ns + "/" + workload
		entry, ok := agg[key]
		if !ok {
			entry = &IstioMTLSRatio{Namespace: ns, Workload: workload}
			agg[key] = entry
		}
		v := float64(s.Value)
		entry.Total += v
		if policy == "mutual_tls" {
			entry.MTLS += v
		}
	}
	out := make([]IstioMTLSRatio, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	return out, nil
}

// applyMTLSMetricOverrides promotes policy-derived "active" results to
// "mixed" when observed traffic shows a fraction of non-mTLS requests.
// Zero-traffic workloads keep their policy state — a silent service
// isn't evidence against STRICT.
func applyMTLSMetricOverrides(workloads []WorkloadMTLS, ratios []IstioMTLSRatio) []WorkloadMTLS {
	if len(ratios) == 0 {
		return workloads
	}
	byKey := map[string]IstioMTLSRatio{}
	for _, r := range ratios {
		byKey[r.Namespace+"/"+r.Workload] = r
	}
	for i := range workloads {
		w := &workloads[i]
		if w.Mesh != MeshIstio {
			continue
		}
		r, ok := byKey[w.Namespace+"/"+w.Workload]
		if !ok || r.Total == 0 {
			continue
		}
		ratio := r.Ratio()
		if ratio < 1.0 && ratio > 0 {
			w.State = MTLSMixed
			w.Source = MTLSSourceMetric
		} else if ratio == 0 && w.State == MTLSActive {
			w.State = MTLSInactive
			w.Source = MTLSSourceMetric
		}
	}
	return workloads
}

// listNamespacePods lists pods visible to the caller-supplied clientset,
// optionally scoped to a namespace ("" means cluster-wide). The caller
// is expected to pass an impersonating client; RBAC filtering is the
// Kubernetes API server's responsibility from that point on.
//
// The List call caps at meshListCap items per request and does not
// continue. The truncated return flag tells the handler to surface a
// response-level warning so users reading mTLS posture on very large
// namespaces (or cluster-wide) know the result is partial rather than
// authoritative.
func listNamespacePods(ctx context.Context, cs kubernetes.Interface, namespace string) (pods []corev1.Pod, truncated bool, err error) {
	callCtx, cancel := context.WithTimeout(ctx, meshListTimeout)
	defer cancel()
	list, lerr := cs.CoreV1().Pods(namespace).List(callCtx, metav1.ListOptions{Limit: meshListCap})
	if lerr != nil {
		return nil, false, lerr
	}
	truncated = list.Continue != "" || int64(len(list.Items)) == meshListCap
	return list.Items, truncated, nil
}
