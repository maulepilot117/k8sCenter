package servicemesh

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// --- precedence resolver unit tests ----------------------------------------

// TestResolveIstioMTLSMode_MeshStrict_NoOverrides is the canonical happy
// path: a single mesh-root PeerAuthentication with STRICT applies to every
// workload in every namespace.
func TestResolveIstioMTLSMode_MeshStrict_NoOverrides(t *testing.T) {
	pas := []peerAuthRef{
		{Namespace: istioMeshRootNamespace, Name: "default", Mode: IstioMTLSStrict},
	}
	mode, detail := resolveIstioMTLSMode(map[string]string{"app": "cart"}, "shop", istioMeshRootNamespace, pas)
	if mode != IstioMTLSStrict {
		t.Errorf("mode = %q, want %q", mode, IstioMTLSStrict)
	}
	if detail != "mesh" {
		t.Errorf("detail = %q, want \"mesh\"", detail)
	}
}

// TestResolveIstioMTLSMode_NamespaceOverridesMesh: the plan scenario where
// namespace PERMISSIVE overrides mesh STRICT.
func TestResolveIstioMTLSMode_NamespaceOverridesMesh(t *testing.T) {
	pas := []peerAuthRef{
		{Namespace: istioMeshRootNamespace, Name: "default", Mode: IstioMTLSStrict},
		{Namespace: "shop", Name: "ns-default", Mode: IstioMTLSPermissive},
	}
	mode, detail := resolveIstioMTLSMode(map[string]string{"app": "cart"}, "shop", istioMeshRootNamespace, pas)
	if mode != IstioMTLSPermissive {
		t.Errorf("mode = %q, want %q", mode, IstioMTLSPermissive)
	}
	if detail != "namespace" {
		t.Errorf("detail = %q, want \"namespace\"", detail)
	}
}

// TestResolveIstioMTLSMode_WorkloadOverridesNamespace: selector-matched
// workload PA wins over a namespace-wide PA.
func TestResolveIstioMTLSMode_WorkloadOverridesNamespace(t *testing.T) {
	pas := []peerAuthRef{
		{Namespace: "shop", Name: "cart-strict", Mode: IstioMTLSStrict, Selector: map[string]string{"app": "cart"}},
		{Namespace: "shop", Name: "ns-default", Mode: IstioMTLSDisable},
	}
	mode, detail := resolveIstioMTLSMode(map[string]string{"app": "cart"}, "shop", istioMeshRootNamespace, pas)
	if mode != IstioMTLSStrict {
		t.Errorf("mode = %q, want %q", mode, IstioMTLSStrict)
	}
	if detail != "workload" {
		t.Errorf("detail = %q, want \"workload\"", detail)
	}
}

// TestResolveIstioMTLSMode_WorkloadDisableOverridesStrict: the edge case
// from the plan — workload DISABLE wins over mesh/namespace STRICT.
func TestResolveIstioMTLSMode_WorkloadDisableOverridesStrict(t *testing.T) {
	pas := []peerAuthRef{
		{Namespace: istioMeshRootNamespace, Name: "default", Mode: IstioMTLSStrict},
		{Namespace: "shop", Name: "ns-default", Mode: IstioMTLSStrict},
		{Namespace: "shop", Name: "legacy-disable", Mode: IstioMTLSDisable, Selector: map[string]string{"app": "legacy"}},
	}
	mode, detail := resolveIstioMTLSMode(map[string]string{"app": "legacy"}, "shop", istioMeshRootNamespace, pas)
	if mode != IstioMTLSDisable {
		t.Errorf("mode = %q, want %q", mode, IstioMTLSDisable)
	}
	if detail != "workload" {
		t.Errorf("detail = %q, want \"workload\"", detail)
	}
}

// TestResolveIstioMTLSMode_WorkloadSelectorMisses: a workload PA whose
// selector does not match falls through to namespace scope.
func TestResolveIstioMTLSMode_WorkloadSelectorMisses(t *testing.T) {
	pas := []peerAuthRef{
		{Namespace: "shop", Name: "cart-strict", Mode: IstioMTLSStrict, Selector: map[string]string{"app": "cart"}},
		{Namespace: "shop", Name: "ns-default", Mode: IstioMTLSPermissive},
	}
	// checkout pod — does not carry app=cart.
	mode, detail := resolveIstioMTLSMode(map[string]string{"app": "checkout"}, "shop", istioMeshRootNamespace, pas)
	if mode != IstioMTLSPermissive {
		t.Errorf("mode = %q, want %q", mode, IstioMTLSPermissive)
	}
	if detail != "namespace" {
		t.Errorf("detail = %q, want \"namespace\"", detail)
	}
}

// TestResolveIstioMTLSMode_NoApplicablePA: no PA in any scope falls back
// to UNSET, which the caller classifies as PERMISSIVE per Istio's default.
func TestResolveIstioMTLSMode_NoApplicablePA(t *testing.T) {
	mode, detail := resolveIstioMTLSMode(map[string]string{"app": "cart"}, "shop", istioMeshRootNamespace, nil)
	if mode != IstioMTLSUnset {
		t.Errorf("mode = %q, want %q", mode, IstioMTLSUnset)
	}
	if detail != "" {
		t.Errorf("detail = %q, want \"\"", detail)
	}
}

// --- modeToState -----------------------------------------------------------

func TestModeToState(t *testing.T) {
	cases := map[string]MTLSState{
		IstioMTLSStrict:     MTLSActive,
		IstioMTLSPermissive: MTLSInactive,
		IstioMTLSDisable:    MTLSInactive,
		IstioMTLSUnset:      MTLSInactive,
		"":                  MTLSInactive,
		"MYSTERY":           MTLSInactive, // unknown future mode fails closed
	}
	for mode, want := range cases {
		if got := modeToState(mode); got != want {
			t.Errorf("modeToState(%q) = %q, want %q", mode, got, want)
		}
	}
}

// --- Linkerd ---------------------------------------------------------------

// TestLinkerdPodState_Meshed covers the plan's edge case: a Linkerd pod in
// an unmeshed namespace is still active because the proxy annotation wins.
func TestLinkerdPodState_Meshed(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web-abc",
			Namespace:   "default", // namespace has no injection label
			Annotations: map[string]string{linkerdProxyAnnotation: "edge-25.1.1"},
		},
	}
	if got := linkerdPodState(pod); got != MTLSActive {
		t.Errorf("state = %q, want %q", got, MTLSActive)
	}
}

func TestLinkerdPodState_Unmeshed(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "raw"}}
	if got := linkerdPodState(pod); got != MTLSUnmeshed {
		t.Errorf("state = %q, want %q", got, MTLSUnmeshed)
	}
}

// --- mesh membership -------------------------------------------------------

func TestPodMeshMembership(t *testing.T) {
	sidecar := func(name string) *corev1.Pod {
		return &corev1.Pod{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}, {Name: name}}},
		}
	}
	if got := podMeshMembership(sidecar("istio-proxy")); got != MeshIstio {
		t.Errorf("istio-proxy sidecar: mesh = %q, want %q", got, MeshIstio)
	}
	if got := podMeshMembership(sidecar("linkerd-proxy")); got != MeshLinkerd {
		t.Errorf("linkerd-proxy sidecar: mesh = %q, want %q", got, MeshLinkerd)
	}
	annotated := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{linkerdProxyAnnotation: "x"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	}
	if got := podMeshMembership(annotated); got != MeshLinkerd {
		t.Errorf("linkerd-annotated: mesh = %q, want %q", got, MeshLinkerd)
	}
	bare := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}}
	if got := podMeshMembership(bare); got != MeshNone {
		t.Errorf("bare pod: mesh = %q, want %q", got, MeshNone)
	}
}

// --- workloadKey -----------------------------------------------------------

func TestWorkloadKey_DeploymentStrippedFromReplicaSet(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cart-6d4b7-xyz",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "cart-6d4b7"},
			},
		},
	}
	kind, name := workloadKey(pod)
	if kind != "Deployment" || name != "cart" {
		t.Errorf("got (%q,%q), want (Deployment, cart)", kind, name)
	}
}

func TestWorkloadKey_StatefulSet(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "db-0",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "StatefulSet", Name: "db"},
			},
		},
	}
	kind, name := workloadKey(pod)
	if kind != "StatefulSet" || name != "db" {
		t.Errorf("got (%q,%q), want (StatefulSet, db)", kind, name)
	}
}

func TestWorkloadKey_OrphanPod(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "solo"}}
	kind, name := workloadKey(pod)
	if kind != "Pod" || name != "solo" {
		t.Errorf("got (%q,%q), want (Pod, solo)", kind, name)
	}
}

// TestWorkloadKey_DaemonSetJobCronJob covers the StatefulSet-adjacent
// owner kinds that previously had no test coverage. The resolver must
// return (OwnerKind, OwnerName) verbatim for these — no "-<hash>"
// strip.
func TestWorkloadKey_DaemonSetJobCronJob(t *testing.T) {
	cases := []struct {
		kind     string
		ownerKey string
	}{
		{"DaemonSet", "fluentd"},
		{"Job", "migrate-db"},
		{"CronJob", "nightly-report"},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-x",
					OwnerReferences: []metav1.OwnerReference{
						{Kind: c.kind, Name: c.ownerKey},
					},
				},
			}
			kind, name := workloadKey(pod)
			if kind != c.kind || name != c.ownerKey {
				t.Errorf("got (%q,%q), want (%q,%q)", kind, name, c.kind, c.ownerKey)
			}
		})
	}
}

// TestWorkloadKey_ReplicaSetNoHash: a ReplicaSet name with no hyphen
// (e.g. a user-created RS named "frontend") falls through to the
// (ReplicaSet, name) branch rather than attempting a Deployment strip.
// Documents the intentional fallback.
func TestWorkloadKey_ReplicaSetNoHash(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "frontend-0",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "frontend"},
			},
		},
	}
	kind, name := workloadKey(pod)
	if kind != "ReplicaSet" || name != "frontend" {
		t.Errorf("got (%q,%q), want (ReplicaSet, frontend)", kind, name)
	}
}

// TestWorkloadKey_ReplicaSetHashSuffixHeuristic exercises the boundary
// of the "is this RS owned by a Deployment?" heuristic. Only suffixes
// matching the kube-controller pod-template-hash safe alphabet
// (bcdfghjklmnpqrstvwxz2456789, length 5-10) are stripped as
// Deployment-owned. Anything else — orphan RSes, user-named RSes with
// dashed non-hash suffixes — is reported verbatim as a ReplicaSet so we
// never fabricate a Deployment that isn't there.
func TestWorkloadKey_ReplicaSetHashSuffixHeuristic(t *testing.T) {
	cases := []struct {
		name     string
		rsName   string
		wantKind string
		wantName string
	}{
		// Real Deployment-owned ReplicaSets: 5-10 chars from the safe
		// alphabet, mix of letters and digits.
		{"five-char-hash", "cart-6d4b7", "Deployment", "cart"},
		{"ten-char-hash", "cart-5d4f7c8b9d", "Deployment", "cart"},
		{"hash-with-multi-segment-base", "my-app-svc-78b8b6c789", "Deployment", "my-app-svc"},

		// Not-a-hash suffixes — the suffix is too short, contains
		// vowels, or contains digits (0/1/3) that the safe alphabet
		// excludes. These are typically orphan or user-managed RSes.
		{"version-suffix", "worker-v1", "ReplicaSet", "worker-v1"},
		{"vowel-suffix", "my-standalone", "ReplicaSet", "my-standalone"},
		{"unsafe-digit-suffix", "app-12345", "ReplicaSet", "app-12345"},
		{"too-short-suffix", "svc-ab12", "ReplicaSet", "svc-ab12"},
		{"too-long-suffix", "svc-bcdfghjklmn", "ReplicaSet", "svc-bcdfghjklmn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: c.rsName + "-pod",
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "ReplicaSet", Name: c.rsName},
					},
				},
			}
			kind, name := workloadKey(pod)
			if kind != c.wantKind || name != c.wantName {
				t.Errorf("got (%q,%q), want (%q,%q)", kind, name, c.wantKind, c.wantName)
			}
		})
	}
}

// TestPodMeshMembership_AmbientAnnotation covers the Istio ambient-mode
// annotation path: no istio-proxy sidecar, but the
// sidecar.istio.io/status annotation is still present. The resolver
// classifies this as MeshIstio so ambient pods don't fall off the
// posture table.
func TestPodMeshMembership_AmbientAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"sidecar.istio.io/status": `{"initContainers":[],"containers":[]}`,
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	}
	if got := podMeshMembership(pod); got != MeshIstio {
		t.Errorf("ambient-annotated pod: mesh = %q, want %q", got, MeshIstio)
	}
}

// --- peerAuthsFromPolicies ordering ---------------------------------------

// TestPeerAuthsFromPolicies_WorkloadBeatsNamespaceInSort asserts the
// load-bearing sort that makes the precedence resolver correct:
// workload-scoped PAs must sort before namespace-scoped PAs within the
// same namespace. A regression here would silently flip every
// workload-vs-namespace decision in the cluster.
func TestPeerAuthsFromPolicies_WorkloadBeatsNamespaceInSort(t *testing.T) {
	policies := []MeshedPolicy{
		{
			Mesh: MeshIstio, Kind: "PeerAuthentication",
			Namespace: "shop", Name: "namespace-wide",
			MTLSMode: IstioMTLSPermissive,
			Raw: map[string]any{
				"spec": map[string]any{"mtls": map[string]any{"mode": IstioMTLSPermissive}},
			},
		},
		{
			Mesh: MeshIstio, Kind: "PeerAuthentication",
			Namespace: "shop", Name: "cart-strict",
			MTLSMode: IstioMTLSStrict,
			Raw: map[string]any{
				"spec": map[string]any{
					"selector": map[string]any{
						"matchLabels": map[string]any{"app": "cart"},
					},
					"mtls": map[string]any{"mode": IstioMTLSStrict},
				},
			},
		},
	}
	out := peerAuthsFromPolicies(policies)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Name != "cart-strict" {
		t.Errorf("first = %q, want cart-strict (workload-scoped must sort first)", out[0].Name)
	}
	if out[0].compiled == nil {
		t.Error("workload-scoped PA should have a pre-compiled selector")
	}
	if len(out[1].Selector) != 0 {
		t.Errorf("second = %+v, want namespace-wide (empty selector)", out[1])
	}
}

// TestPeerAuthsFromPolicies_FiltersNonPeerAuths confirms the filter
// drops non-PA policies and normalizes empty Mode to UNSET.
func TestPeerAuthsFromPolicies_FiltersNonPeerAuths(t *testing.T) {
	policies := []MeshedPolicy{
		{Mesh: MeshIstio, Kind: "PeerAuthentication", Namespace: "shop", Name: "pa1", MTLSMode: ""},
		{Mesh: MeshIstio, Kind: "AuthorizationPolicy", Namespace: "shop", Name: "ap"},
		{Mesh: MeshLinkerd, Kind: "Server", Namespace: "shop", Name: "srv"},
	}
	out := peerAuthsFromPolicies(policies)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1 (only the PA)", len(out))
	}
	if out[0].Mode != IstioMTLSUnset {
		t.Errorf("Mode = %q, want %q (empty normalizes to UNSET)", out[0].Mode, IstioMTLSUnset)
	}
}

// --- cluster-wide handler path --------------------------------------------

// TestHandler_MTLSPosture_ClusterWide exercises the namespace-omitted
// path: posture should aggregate across every visible namespace and the
// Prometheus cross-check is intentionally skipped (the template
// requires a concrete namespace). Gives the cluster-wide code path its
// first integration-level coverage.
func TestHandler_MTLSPosture_ClusterWide(t *testing.T) {
	pa := newPeerAuth(istioMeshRootNamespace, "default", IstioMTLSStrict, nil)
	cs := fake.NewClientset(
		injectedPod("shop", "cart-6d4b7-xyz", map[string]string{"app": "cart"}),
		injectedPod("billing", "invoice-7c9d4-aa", map[string]string{"app": "invoice"}),
	)

	h := &Handler{
		Discoverer:        seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true, Version: "1.24.0"}}),
		AccessChecker:     resources.NewAlwaysAllowAccessChecker(),
		Logger:            slog.Default(),
		dynOverride:       newIstioFakeDynClient(pa),
		clientsetOverride: cs,
	}

	// No ?namespace= → cluster-wide.
	req := httptest.NewRequest(http.MethodGet, "/mesh/mtls", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	w := httptest.NewRecorder()
	h.HandleMTLSPosture(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data MTLSPostureResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Workloads) != 2 {
		t.Fatalf("workloads = %d, want 2 (one per namespace)", len(env.Data.Workloads))
	}
	seen := map[string]bool{}
	for _, w := range env.Data.Workloads {
		seen[w.Namespace] = true
		if w.Source != MTLSSourcePolicy {
			t.Errorf("workload %s/%s Source = %q, want policy (cluster-wide skips Prom cross-check)",
				w.Namespace, w.Workload, w.Source)
		}
	}
	if !seen["shop"] || !seen["billing"] {
		t.Errorf("missing namespace coverage: %+v", seen)
	}
}

// --- metric overrides ------------------------------------------------------

// TestApplyMTLSMetricOverrides_MixedFromRatio: policy says STRICT, but
// the metric cross-check observed 90% mTLS → posture becomes mixed.
func TestApplyMTLSMetricOverrides_MixedFromRatio(t *testing.T) {
	wl := []WorkloadMTLS{
		{Namespace: "shop", Workload: "cart", Mesh: MeshIstio, State: MTLSActive, Source: MTLSSourcePolicy, IstioMode: IstioMTLSStrict},
	}
	ratios := []IstioMTLSRatio{
		{Namespace: "shop", Workload: "cart", MTLS: 90, Total: 100},
	}
	out := applyMTLSMetricOverrides(wl, ratios)
	if out[0].State != MTLSMixed {
		t.Errorf("state = %q, want %q", out[0].State, MTLSMixed)
	}
	if out[0].Source != MTLSSourceMetric {
		t.Errorf("source = %q, want %q", out[0].Source, MTLSSourceMetric)
	}
}

// TestApplyMTLSMetricOverrides_ZeroTrafficLeavesPolicyAlone is the
// no-traffic edge case: a silent service keeps its policy-derived state.
func TestApplyMTLSMetricOverrides_ZeroTrafficLeavesPolicyAlone(t *testing.T) {
	wl := []WorkloadMTLS{
		{Namespace: "shop", Workload: "cart", Mesh: MeshIstio, State: MTLSActive, Source: MTLSSourcePolicy},
	}
	ratios := []IstioMTLSRatio{
		{Namespace: "shop", Workload: "cart", MTLS: 0, Total: 0},
	}
	out := applyMTLSMetricOverrides(wl, ratios)
	if out[0].State != MTLSActive || out[0].Source != MTLSSourcePolicy {
		t.Errorf("got (%q,%q), want (active, policy)", out[0].State, out[0].Source)
	}
}

// TestApplyMTLSMetricOverrides_AllMTLSPreservesActive: 100% mTLS means
// policy and metric agree — no override.
func TestApplyMTLSMetricOverrides_AllMTLSPreservesActive(t *testing.T) {
	wl := []WorkloadMTLS{
		{Namespace: "shop", Workload: "cart", Mesh: MeshIstio, State: MTLSActive, Source: MTLSSourcePolicy},
	}
	ratios := []IstioMTLSRatio{
		{Namespace: "shop", Workload: "cart", MTLS: 100, Total: 100},
	}
	out := applyMTLSMetricOverrides(wl, ratios)
	if out[0].State != MTLSActive || out[0].Source != MTLSSourcePolicy {
		t.Errorf("got (%q,%q), want (active, policy)", out[0].State, out[0].Source)
	}
}

// TestApplyMTLSMetricOverrides_AllPlaintextFlipsToInactive: metric says
// zero mTLS on an "active" policy → demote to inactive with source=metric.
func TestApplyMTLSMetricOverrides_AllPlaintextFlipsToInactive(t *testing.T) {
	wl := []WorkloadMTLS{
		{Namespace: "shop", Workload: "cart", Mesh: MeshIstio, State: MTLSActive, Source: MTLSSourcePolicy},
	}
	ratios := []IstioMTLSRatio{
		{Namespace: "shop", Workload: "cart", MTLS: 0, Total: 100},
	}
	out := applyMTLSMetricOverrides(wl, ratios)
	if out[0].State != MTLSInactive || out[0].Source != MTLSSourceMetric {
		t.Errorf("got (%q,%q), want (inactive, metric)", out[0].State, out[0].Source)
	}
}

// --- aggregation -----------------------------------------------------------

// TestAggregateWorkloads_MixedPodsCollapseToMixed: two pods of the same
// deployment with different states roll up to MTLSMixed.
func TestAggregateWorkloads_MixedPodsCollapseToMixed(t *testing.T) {
	postures := []podPosture{
		{Namespace: "shop", Workload: "cart", WorkloadKind: "Deployment", Mesh: MeshIstio, State: MTLSActive},
		{Namespace: "shop", Workload: "cart", WorkloadKind: "Deployment", Mesh: MeshIstio, State: MTLSInactive},
	}
	out := aggregateWorkloads(postures)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].State != MTLSMixed {
		t.Errorf("state = %q, want %q", out[0].State, MTLSMixed)
	}
}

// --- handler integration ---------------------------------------------------

// TestHandler_MTLSPosture_NoMesh: plan scenario — no mesh installed
// returns an empty workloads slice (never nil) with detected="none".
func TestHandler_MTLSPosture_NoMesh(t *testing.T) {
	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshNone}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}

	req := httptest.NewRequest(http.MethodGet, "/mesh/mtls", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	w := httptest.NewRecorder()
	h.HandleMTLSPosture(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data MTLSPostureResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Status.Detected != MeshNone {
		t.Errorf("detected = %q, want %q", env.Data.Status.Detected, MeshNone)
	}
	if env.Data.Workloads == nil {
		t.Error("Workloads is nil; frontend treats null as error — want empty slice")
	}
}

// TestHandler_MTLSPosture_Denied: user can't list pods → 403 with a
// user-safe message. An earlier revision returned 200 empty to match
// HandleListRoutes' precedent, but mTLS posture is not a pure list
// endpoint — silent denial would mask a misconfigured RBAC as
// "no workloads in this scope" and hide real infrastructure problems.
func TestHandler_MTLSPosture_Denied(t *testing.T) {
	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true, Version: "1.24.0"}}),
		AccessChecker: resources.NewAlwaysDenyAccessChecker(),
		Logger:        slog.Default(),
	}

	req := httptest.NewRequest(http.MethodGet, "/mesh/mtls", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	w := httptest.NewRecorder()
	h.HandleMTLSPosture(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// TestHandler_MTLSPosture_IstioNamespaceStrict: end-to-end integration
// with a real fake clientset. A single Istio-injected pod in a namespace
// with mesh-root STRICT resolves to active/policy/mesh.
func TestHandler_MTLSPosture_IstioNamespaceStrict(t *testing.T) {
	pa := newPeerAuth(istioMeshRootNamespace, "default", IstioMTLSStrict, nil)
	cs := fake.NewClientset(injectedPod("shop", "cart-6d4b7-xyz", map[string]string{"app": "cart"}))

	h := &Handler{
		Discoverer:        seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true, Version: "1.24.0"}}),
		AccessChecker:     resources.NewAlwaysAllowAccessChecker(),
		Logger:            slog.Default(),
		dynOverride:       newIstioFakeDynClient(pa),
		clientsetOverride: cs,
	}

	req := httptest.NewRequest(http.MethodGet, "/mesh/mtls?namespace=shop", nil)
	q := req.URL.Query()
	q.Set("namespace", "shop")
	req.URL.RawQuery = q.Encode()
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	w := httptest.NewRecorder()
	h.HandleMTLSPosture(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data MTLSPostureResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Workloads) != 1 {
		t.Fatalf("workloads = %d, want 1", len(env.Data.Workloads))
	}
	got := env.Data.Workloads[0]
	if got.Workload != "cart" {
		t.Errorf("Workload = %q, want cart", got.Workload)
	}
	if got.State != MTLSActive {
		t.Errorf("State = %q, want %q", got.State, MTLSActive)
	}
	if got.Source != MTLSSourcePolicy {
		t.Errorf("Source = %q, want %q", got.Source, MTLSSourcePolicy)
	}
	if got.SourceDetail != "mesh" {
		t.Errorf("SourceDetail = %q, want mesh", got.SourceDetail)
	}
	if got.IstioMode != IstioMTLSStrict {
		t.Errorf("IstioMode = %q, want %q", got.IstioMode, IstioMTLSStrict)
	}
}

// TestHandler_MTLSPosture_LinkerdMeshed: a Linkerd-injected pod with the
// proxy annotation resolves to active/policy/empty-detail.
func TestHandler_MTLSPosture_LinkerdMeshed(t *testing.T) {
	cs := fake.NewClientset(linkerdPod("shop", "web-abc", map[string]string{"app": "web"}))

	h := &Handler{
		Discoverer:        seededDiscoverer(MeshStatus{Detected: MeshLinkerd, Linkerd: &MeshInfo{Installed: true, Version: "edge-25"}}),
		AccessChecker:     resources.NewAlwaysAllowAccessChecker(),
		Logger:            slog.Default(),
		dynOverride:       newLinkerdFakeDynClient(),
		clientsetOverride: cs,
	}

	req := httptest.NewRequest(http.MethodGet, "/mesh/mtls?namespace=shop", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	w := httptest.NewRecorder()
	h.HandleMTLSPosture(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data MTLSPostureResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Workloads) != 1 {
		t.Fatalf("workloads = %d, want 1", len(env.Data.Workloads))
	}
	got := env.Data.Workloads[0]
	if got.Mesh != MeshLinkerd || got.State != MTLSActive {
		t.Errorf("got (mesh=%q state=%q), want (linkerd, active)", got.Mesh, got.State)
	}
}

// --- helpers ---------------------------------------------------------------

// newPeerAuth returns an unstructured PeerAuthentication for fake client
// seeding. selector nil means namespace/mesh-level (no spec.selector).
func newPeerAuth(ns, name, mode string, selector map[string]string) *unstructured.Unstructured {
	spec := map[string]any{
		"mtls": map[string]any{"mode": mode},
	}
	if len(selector) > 0 {
		sel := map[string]any{}
		for k, v := range selector {
			sel[k] = v
		}
		spec["selector"] = map[string]any{"matchLabels": sel}
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "security.istio.io/v1",
			"kind":       "PeerAuthentication",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec":       spec,
		},
	}
}

// injectedPod returns a pod with the istio-proxy sidecar container and
// the standard Deployment → ReplicaSet → Pod ownership chain.
func injectedPod(ns, name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    labels,
			Annotations: map[string]string{
				"sidecar.istio.io/status": `{"initContainers":["istio-init"],"containers":["istio-proxy"]}`,
			},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: deriveRSName(name)},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "cart:1"},
				{Name: "istio-proxy", Image: "istio/proxyv2:1.24"},
			},
		},
	}
}

func linkerdPod(ns, name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      labels,
			Annotations: map[string]string{linkerdProxyAnnotation: "edge-25.1.1"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: deriveRSName(name)},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "web:1"},
				{Name: "linkerd-proxy", Image: "cr.l5d.io/proxy:edge-25"},
			},
		},
	}
}

// deriveRSName strips the last "-xxx" suffix of a pod name so that pod
// "cart-6d4b7-xyz" yields RS "cart-6d4b7" — the workload resolver in
// turn strips the hash-shaped suffix to "cart". Fixture pod names must
// embed a real pod-template-hash shape (5-10 chars from the kube
// safe alphabet) or workloadKey will keep the RS name verbatim.
func deriveRSName(podName string) string {
	for i := len(podName) - 1; i > 0; i-- {
		if podName[i] == '-' {
			return podName[:i]
		}
	}
	return podName
}

