package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

const remoteTestClusterID = "remote-1"

// remoteDashboardHandler builds a Handler whose informers hold localObjs and
// whose remote client (the remoteClient test override) is a fake clientset
// seeded with remoteObjs. The two object sets are deliberately distinct so a
// test can tell which cluster a value came from.
func remoteDashboardHandler(t *testing.T, localObjs []runtime.Object, remoteObjs ...runtime.Object) (*Handler, *fake.Clientset) {
	t.Helper()
	h, _ := testHandler(t, localObjs...)
	remote := fake.NewSimpleClientset(remoteObjs...)
	h.remoteClient = func(_ context.Context, clusterID string, _ *auth.User) (kubernetes.Interface, error) {
		if clusterID != remoteTestClusterID {
			t.Errorf("remote client resolved for cluster %q, want %q", clusterID, remoteTestClusterID)
		}
		return remote, nil
	}
	return h, remote
}

// callRemoteDashboard fires the dashboard request against the remote cluster
// with the given raw query string and returns the recorder.
func callRemoteDashboard(t *testing.T, h *Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/cluster/dashboard-summary"
	if query != "" {
		path += "?" + query
	}
	req := requestWithUser("GET", path, "")
	req = req.WithContext(middleware.WithClusterID(req.Context(), remoteTestClusterID))
	rr := httptest.NewRecorder()
	h.HandleDashboardSummary(rr, req)
	return rr
}

// remoteSummaryOK calls the opt-in remote path and decodes the 200 body.
func remoteSummaryOK(t *testing.T, h *Handler) DashboardSummary {
	t.Helper()
	rr := callRemoteDashboard(t, h, "coverage=1")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeDashboard(t, rr)
}

func coverageRow(t *testing.T, s DashboardSummary, section string) SectionCoverage {
	t.Helper()
	for _, c := range s.Coverage {
		if c.Section == section {
			return c
		}
	}
	t.Fatalf("no coverage row for section %q in %+v", section, s.Coverage)
	return SectionCoverage{}
}

func assertRow(t *testing.T, s DashboardSummary, section, status, reason string) {
	t.Helper()
	row := coverageRow(t, s, section)
	if row.Status != status || row.ReasonCode != reason {
		t.Errorf("coverage[%s] = {status:%q reason:%q detail:%q}, want {status:%q reason:%q}",
			section, row.Status, row.ReasonCode, row.Detail, status, reason)
	}
}

// nodeWithAllocatable returns a Ready node carrying the given allocatable CPU
// and memory.
func nodeWithAllocatable(name, cpu, mem string) *corev1.Node {
	n := readyNode(name)
	n.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
	return n
}

// podWithResources returns a pod in the given phase with a single container
// carrying the given CPU/memory requests and limits.
func podWithResources(name string, phase corev1.PodPhase, cpuReq, cpuLim, memReq, memLim string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "c",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpuReq),
					corev1.ResourceMemory: resource.MustParse(memReq),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpuLim),
					corev1.ResourceMemory: resource.MustParse(memLim),
				},
			},
		}}},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func service(name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}
}

// ── Extraction regression ──────────────────────────────────────────────────

func TestAggregateCounts_Pure(t *testing.T) {
	notReady := readyNode("n-notready")
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse
	noConditions := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n-bare"}}

	pending := runningPod("default", "p-pending")
	pending.Status.Phase = corev1.PodPending
	failed := runningPod("default", "p-failed")
	failed.Status.Phase = corev1.PodFailed
	succeeded := runningPod("default", "p-succeeded")
	succeeded.Status.Phase = corev1.PodSucceeded

	tests := []struct {
		name     string
		nodes    []*corev1.Node
		pods     []*corev1.Pod
		services int
		wantN    NodeSummary
		wantP    PodSummary
		wantS    ServiceCount
	}{
		{name: "empty", wantN: NodeSummary{}, wantP: PodSummary{}, wantS: ServiceCount{}},
		{
			name:     "mixed",
			nodes:    []*corev1.Node{readyNode("n1"), readyNode("n2"), notReady, noConditions},
			pods:     []*corev1.Pod{runningPod("default", "p1"), runningPod("default", "p2"), pending, failed, succeeded},
			services: 3,
			wantN:    NodeSummary{Total: 4, Ready: 2},
			// Succeeded counts toward Total but no phase bucket — unchanged
			// from the pre-extraction switch.
			wantP: PodSummary{Total: 5, Running: 2, Pending: 1, Failed: 1},
			wantS: ServiceCount{Total: 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, p, s := aggregateCounts(tt.nodes, tt.pods, tt.services)
			if n != tt.wantN {
				t.Errorf("nodes = %+v, want %+v", n, tt.wantN)
			}
			if p != tt.wantP {
				t.Errorf("pods = %+v, want %+v", p, tt.wantP)
			}
			if s != tt.wantS {
				t.Errorf("services = %+v, want %+v", s, tt.wantS)
			}
		})
	}
}

func TestAggregateCapacity_Pure(t *testing.T) {
	nodes := []*corev1.Node{
		nodeWithAllocatable("n1", "2", "4Gi"),
		nodeWithAllocatable("n2", "1500m", "2Gi"),
		readyNode("n-no-allocatable"),
	}
	pods := []*corev1.Pod{
		podWithResources("run", corev1.PodRunning, "250m", "500m", "128Mi", "256Mi"),
		podWithResources("pend", corev1.PodPending, "250m", "1", "128Mi", "512Mi"),
		// Terminal phases are excluded from requests/limits.
		podWithResources("done", corev1.PodSucceeded, "4", "4", "8Gi", "8Gi"),
		podWithResources("dead", corev1.PodFailed, "4", "4", "8Gi", "8Gi"),
	}
	got := aggregateCapacity(nodes, pods)

	checks := []struct {
		name string
		got  resource.Quantity
		want string
	}{
		{"CPUAllocatable", got.CPUAllocatable, "3500m"},
		{"MemAllocatable", got.MemAllocatable, "6Gi"},
		{"CPURequests", got.CPURequests, "500m"},
		{"CPULimits", got.CPULimits, "1500m"},
		{"MemRequests", got.MemRequests, "256Mi"},
		{"MemLimits", got.MemLimits, "768Mi"},
	}
	for _, c := range checks {
		if c.got.Cmp(resource.MustParse(c.want)) != 0 {
			t.Errorf("%s = %s, want %s", c.name, c.got.String(), c.want)
		}
	}
}

func TestUtilizationFrom(t *testing.T) {
	totals := aggregateCapacity(
		[]*corev1.Node{nodeWithAllocatable("n1", "4", "8Gi")},
		[]*corev1.Pod{podWithResources("p", corev1.PodRunning, "500m", "2", "512Mi", "1Gi")},
	)
	half := 50.0

	t.Run("observed percentage", func(t *testing.T) {
		cpu := utilizationFrom(totals, resourceKindCPU, &half)
		want := &Utilization{Percentage: 50, Used: "2.0 cores", Total: "4.0 cores", Requests: "500m", Limits: "2.0 cores"}
		if cpu == nil || *cpu != *want {
			t.Errorf("cpu = %+v, want %+v", cpu, want)
		}
		mem := utilizationFrom(totals, resourceKindMemory, &half)
		wantMem := &Utilization{Percentage: 50, Used: "4.0 Gi", Total: "8.0 Gi", Requests: "512 Mi", Limits: "1.0 Gi"}
		if mem == nil || *mem != *wantMem {
			t.Errorf("mem = %+v, want %+v", mem, wantMem)
		}
	})

	t.Run("no percentage falls back to N/A", func(t *testing.T) {
		cpu := utilizationFrom(totals, resourceKindCPU, nil)
		want := &Utilization{Percentage: 0, Used: "N/A", Total: "4.0 cores", Requests: "500m", Limits: "2.0 cores"}
		if cpu == nil || *cpu != *want {
			t.Errorf("cpu = %+v, want %+v", cpu, want)
		}
	})

	t.Run("no percentage and no allocatable is nil", func(t *testing.T) {
		if got := utilizationFrom(capacityTotals{}, resourceKindCPU, nil); got != nil {
			t.Errorf("cpu = %+v, want nil", got)
		}
		if got := utilizationFrom(capacityTotals{}, resourceKindMemory, nil); got != nil {
			t.Errorf("mem = %+v, want nil", got)
		}
	})
}

// ── Mobile / local compatibility ───────────────────────────────────────────

func TestDashboardSummary_LocalUnchangedWithoutCoverageParam(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil, readyNode("remote-node"))

	rr := callRemoteDashboard(t, h, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("remote without ?coverage=1: status = %d, want 400", rr.Code)
	}
	// mobile's DashboardLocalOnlyError matches HTTP 400 + "local cluster".
	const wantMsg = "dashboard summary is only available for the local cluster"
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rr.Body.String())
	}
	if body.Error.Message != wantMsg {
		t.Errorf("message = %q, want %q", body.Error.Message, wantMsg)
	}
	if n := len(remote.Actions()); n != 0 {
		t.Errorf("remote cluster received %d API calls on the rejected path, want 0", n)
	}

	// Any value other than exactly "1" stays on the legacy 400.
	if rr := callRemoteDashboard(t, h, "coverage=true"); rr.Code != http.StatusBadRequest {
		t.Errorf("?coverage=true: status = %d, want 400", rr.Code)
	}

	// Local with the opt-in parameter is byte-shape-identical: no coverage key.
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-summary?coverage=1", "")
	lrr := httptest.NewRecorder()
	h.HandleDashboardSummary(lrr, req)
	if lrr.Code != http.StatusOK {
		t.Fatalf("local ?coverage=1: status = %d, want 200", lrr.Code)
	}
	if strings.Contains(lrr.Body.String(), `"coverage"`) {
		t.Errorf("local response carries a coverage key: %s", lrr.Body.String())
	}
}

// ── Remote path (AE3) ──────────────────────────────────────────────────────

func TestRemoteSummary_CountsWithoutMetrics(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil,
		nodeWithAllocatable("r-n1", "4", "8Gi"),
		nodeWithAllocatable("r-n2", "4", "8Gi"),
		podWithResources("r-p1", corev1.PodRunning, "1", "2", "1Gi", "2Gi"),
		service("r-s1"),
	)
	// The cluster has no metrics binding: the provider says so for the
	// remote id, and the rows must report "not configured", not a number.
	fu := &fakeUtilization{cpu: 42, mem: 42, err: ErrNoMetricsBinding}
	h.Utilization = fu

	s := remoteSummaryOK(t, h)

	if s.Nodes != (NodeSummary{Total: 2, Ready: 2}) {
		t.Errorf("nodes = %+v", s.Nodes)
	}
	if s.Pods != (PodSummary{Total: 1, Running: 1}) {
		t.Errorf("pods = %+v", s.Pods)
	}
	if s.Services.Total != 1 {
		t.Errorf("services = %+v", s.Services)
	}
	// No observed percentage: the shape is the existing "N/A" sentinel that
	// web and mobile already render as unavailable, never a number.
	wantCPU := Utilization{Percentage: 0, Used: "N/A", Total: "8.0 cores", Requests: "1.0 cores", Limits: "2.0 cores"}
	if s.CPU == nil || *s.CPU != wantCPU {
		t.Errorf("cpu = %+v, want %+v", s.CPU, wantCPU)
	}
	wantMem := Utilization{Percentage: 0, Used: "N/A", Total: "16.0 Gi", Requests: "1.0 Gi", Limits: "2.0 Gi"}
	if s.Memory == nil || *s.Memory != wantMem {
		t.Errorf("memory = %+v, want %+v", s.Memory, wantMem)
	}
	assertRow(t, s, "cpu", "unavailable", "metrics_not_configured")
	assertRow(t, s, "memory", "unavailable", "metrics_not_configured")
	for _, sec := range []string{"cpu", "memory"} {
		if d := coverageRow(t, s, sec).Detail; d != "metrics are not configured for this cluster" {
			t.Errorf("%s detail = %q, want the not-configured sentence", sec, d)
		}
	}
	assertRow(t, s, "alerts", "unavailable", "unsupported_platform")
	assertRow(t, s, "nodes", "ok", "ok")
	assertRow(t, s, "pods", "ok", "ok")
	assertRow(t, s, "services", "ok", "ok")
	fu.assertOnlyCluster(t, remoteTestClusterID)
}

func TestRemoteSummary_NilProviderIsNotConfigured(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil, nodeWithAllocatable("r-n1", "4", "8Gi"))

	s := remoteSummaryOK(t, h)

	assertRow(t, s, "cpu", "unavailable", "metrics_not_configured")
	assertRow(t, s, "memory", "unavailable", "metrics_not_configured")
	if s.CPU == nil || s.CPU.Used != "N/A" {
		t.Errorf("cpu = %+v, want the N/A usage sentinel", s.CPU)
	}
}

func TestRemoteSummary_MetricsFromBinding(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil,
		nodeWithAllocatable("r-n1", "4", "8Gi"),
		nodeWithAllocatable("r-n2", "4", "8Gi"),
		podWithResources("r-p1", corev1.PodRunning, "1", "2", "1Gi", "2Gi"),
	)
	fu := &fakeUtilization{cpu: 42, mem: 42}
	h.Utilization = fu

	s := remoteSummaryOK(t, h)

	// 42% of the remote cluster's 8 cores / 16 Gi allocatable.
	wantCPU := Utilization{Percentage: 42, Used: "3.4 cores", Total: "8.0 cores", Requests: "1.0 cores", Limits: "2.0 cores"}
	if s.CPU == nil || *s.CPU != wantCPU {
		t.Errorf("cpu = %+v, want %+v", s.CPU, wantCPU)
	}
	wantMem := Utilization{Percentage: 42, Used: "6.7 Gi", Total: "16.0 Gi", Requests: "1.0 Gi", Limits: "2.0 Gi"}
	if s.Memory == nil || *s.Memory != wantMem {
		t.Errorf("memory = %+v, want %+v", s.Memory, wantMem)
	}
	for _, sec := range []string{"cpu", "memory"} {
		row := coverageRow(t, s, sec)
		if row.Status != "ok" || row.ReasonCode != "ok" || row.ObservedAt == "" || row.Detail != "" {
			t.Errorf("%s row = %+v, want ok/ok with observedAt and no detail", sec, row)
		}
	}
	if s.Health != nil {
		t.Errorf("Health = %+v, want nil: a metrics binding does not enable remote health scoring", s.Health)
	}
	assertRow(t, s, "health", "unavailable", "unsupported_platform")
	fu.assertOnlyCluster(t, remoteTestClusterID)
}

func TestRemoteSummary_ProviderErrorIsUnreachable(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil, nodeWithAllocatable("r-n1", "4", "8Gi"))
	h.Utilization = &fakeUtilization{cpu: 42, mem: 42, err: errors.New("dial tcp 203.0.113.9:443: i/o timeout")}

	rr := callRemoteDashboard(t, h, "coverage=1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "203.0.113.9") {
		t.Errorf("response leaks the provider error: %s", rr.Body.String())
	}
	s := decodeDashboard(t, rr)

	for _, sec := range []string{"cpu", "memory"} {
		row := coverageRow(t, s, sec)
		if row.Status != "unavailable" || row.ReasonCode != "unreachable" || row.Detail != "the cluster's Prometheus could not be read" {
			t.Errorf("%s row = %+v, want unavailable/unreachable with the unreadable detail", sec, row)
		}
	}
	if s.CPU == nil || s.CPU.Used != "N/A" || s.CPU.Percentage != 0 {
		t.Errorf("cpu = %+v, want the N/A usage sentinel", s.CPU)
	}
}

func TestRemoteSummary_ProviderPanicIsUnreachable(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil, nodeWithAllocatable("r-n1", "4", "8Gi"))
	h.Utilization = &fakeUtilization{panics: true}

	s := remoteSummaryOK(t, h)

	assertRow(t, s, "cpu", "unavailable", "unreachable")
	assertRow(t, s, "memory", "unavailable", "unreachable")
	assertRow(t, s, "nodes", "ok", "ok")
}

func TestRemoteSummary_MetricsWithUnreadPodsIsPartial(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil,
		nodeWithAllocatable("r-n1", "4", "8Gi"),
		podWithResources("r-p1", corev1.PodRunning, "1", "2", "1Gi", "2Gi"),
	)
	h.AccessChecker = NewDenyResourcesAccessChecker("pods")
	h.Utilization = &fakeUtilization{cpu: 50, mem: 50}

	s := remoteSummaryOK(t, h)

	wantCPU := Utilization{Percentage: 50, Used: "2.0 cores", Total: "4.0 cores", Requests: "N/A", Limits: "N/A"}
	if s.CPU == nil || *s.CPU != wantCPU {
		t.Errorf("cpu = %+v, want %+v", s.CPU, wantCPU)
	}
	for _, sec := range []string{"cpu", "memory"} {
		row := coverageRow(t, s, sec)
		if row.Status != "partial" || row.ReasonCode != "ok" || row.ObservedAt == "" {
			t.Errorf("%s row = %+v, want partial/ok with observedAt", sec, row)
		}
		if row.Detail != "requests and limits need the pods section, which did not load" {
			t.Errorf("%s detail = %q, want the pods amendment without a leading separator", sec, row.Detail)
		}
	}
}

func TestRemoteSummary_MetricsWithUnreadNodesHasNoAmount(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil,
		nodeWithAllocatable("r-n1", "4", "8Gi"),
		podWithResources("r-p1", corev1.PodRunning, "1", "2", "1Gi", "2Gi"),
	)
	h.AccessChecker = NewDenyResourcesAccessChecker("nodes")
	h.Utilization = &fakeUtilization{cpu: 50, mem: 50}

	s := remoteSummaryOK(t, h)

	// The percentage is real; with no allocatable it is not an amount.
	wantCPU := Utilization{Percentage: 50, Used: "N/A", Total: "N/A", Requests: "1.0 cores", Limits: "2.0 cores"}
	if s.CPU == nil || *s.CPU != wantCPU {
		t.Errorf("cpu = %+v, want %+v", s.CPU, wantCPU)
	}
	for _, sec := range []string{"cpu", "memory"} {
		row := coverageRow(t, s, sec)
		if row.Status != "partial" || !strings.Contains(row.Detail, "nodes section") {
			t.Errorf("%s row = %+v, want partial naming the nodes section", sec, row)
		}
	}
}

func TestRemoteSummary_NeverSynthesizesHealth(t *testing.T) {
	sections := []string{"nodes", "pods", "services"}
	// Every combination of success/denial across the three live sections,
	// including all-succeed (mask 0) — the case a renormalising score would
	// be most tempted to report confidently.
	for mask := 0; mask < 1<<len(sections); mask++ {
		var denied []string
		for i, sec := range sections {
			if mask&(1<<i) != 0 {
				denied = append(denied, sec)
			}
		}
		t.Run(fmt.Sprintf("denied=%v", denied), func(t *testing.T) {
			h, _ := remoteDashboardHandler(t, nil, readyNode("r-n1"), runningPod("default", "r-p1"), service("r-s1"))
			h.Alerts = &fakeAlertCounter{active: 0, critical: 0}
			if len(denied) > 0 {
				h.AccessChecker = NewDenyResourcesAccessChecker(denied...)
			}

			rr := callRemoteDashboard(t, h, "coverage=1")
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
			}
			raw := rr.Body.String()
			s := decodeDashboard(t, rr)
			if s.Health != nil {
				t.Errorf("Health = %+v, want nil on every remote response", s.Health)
			}
			assertRow(t, s, "health", "unavailable", "unsupported_platform")
			if d := coverageRow(t, s, "health").Detail; d != "remote health scoring is not available yet" {
				t.Errorf("health detail = %q", d)
			}
			// The wire carries an explicit null, not a synthesised object.
			if !strings.Contains(raw, `"health":null`) {
				t.Errorf("body lacks \"health\":null: %s", raw)
			}
		})
	}
}

func TestRemoteSummary_PartialListPermissions(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil, readyNode("r-n1"), runningPod("default", "r-p1"))
	h.AccessChecker = NewDenyResourcesAccessChecker("pods")

	s := remoteSummaryOK(t, h)

	// A cluster-wide deny on a namespaced resource does not prove the user
	// lacks access in every namespace, so the reason is namespace-scoped.
	assertRow(t, s, "pods", "forbidden", "authz_namespace_scoped")
	assertRow(t, s, "nodes", "ok", "ok")
	if s.Pods != (PodSummary{}) {
		t.Errorf("pods = %+v, want zero when forbidden", s.Pods)
	}
	if s.Nodes.Total != 1 {
		t.Errorf("nodes.total = %d, want 1", s.Nodes.Total)
	}
	for _, a := range remote.Actions() {
		if a.GetResource().Resource == "pods" {
			t.Errorf("pods were listed despite a denied SAR: %v", a)
		}
	}
}

func TestRemoteSummary_APIForbiddenIsForbidden(t *testing.T) {
	// The SAR allowed it but the remote API server still refused the list
	// (e.g. RBAC changed between the cached SAR and the call).
	h, remote := remoteDashboardHandler(t, nil, readyNode("r-n1"))
	remote.PrependReactor("list", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", errors.New("denied"))
	})

	s := remoteSummaryOK(t, h)
	assertRow(t, s, "services", "forbidden", "authz_namespace_scoped")
	assertRow(t, s, "nodes", "ok", "ok")
}

func TestRemoteSummary_NodesDeniedIsForbidden(t *testing.T) {
	// Nodes are cluster-scoped: a cluster-wide deny is the whole answer, from
	// the permission check and from the API server alike.
	h, _ := remoteDashboardHandler(t, nil, readyNode("r-n1"), runningPod("default", "r-p1"))
	h.AccessChecker = NewDenyResourcesAccessChecker("nodes")
	s := remoteSummaryOK(t, h)
	assertRow(t, s, "nodes", "forbidden", "forbidden")
	assertRow(t, s, "pods", "ok", "ok")

	h, remote := remoteDashboardHandler(t, nil, readyNode("r-n1"))
	remote.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("denied"))
	})
	s = remoteSummaryOK(t, h)
	assertRow(t, s, "nodes", "forbidden", "forbidden")
}

func TestRemoteSummary_SARErrorIsAuthzUnknown(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil, readyNode("r-n1"))
	h.AccessChecker = NewErroringAccessChecker(errors.New("sar backend down"))

	s := remoteSummaryOK(t, h)
	for _, sec := range []string{"nodes", "pods", "services"} {
		assertRow(t, s, sec, "unavailable", "authz_unknown")
	}
	if n := len(remote.Actions()); n != 0 {
		t.Errorf("remote received %d list calls without an authorization answer, want 0", n)
	}
}

func TestRemoteSummary_BudgetExpiryCutsOffHungSection(t *testing.T) {
	// A real clientset over HTTP, because the fake clientset ignores the
	// request context and so cannot show a deadline firing. The API server
	// answers nodes and pods, but holds the services list open until the
	// client gives up; only the dashboard's own budget can end that request.
	prev := remoteDashboardBudget
	remoteDashboardBudget = 300 * time.Millisecond
	t.Cleanup(func() { remoteDashboardBudget = prev })

	servicesCancelled := make(chan struct{})
	var cancelOnce sync.Once
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/nodes":
			_ = json.NewEncoder(w).Encode(&corev1.NodeList{
				TypeMeta: metav1.TypeMeta{Kind: "NodeList", APIVersion: "v1"},
				Items:    []corev1.Node{*readyNode("r-n1")},
			})
		case "/api/v1/pods":
			_ = json.NewEncoder(w).Encode(&corev1.PodList{
				TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"},
				Items:    []corev1.Pod{*runningPod("default", "r-p1")},
			})
		case "/api/v1/services":
			select {
			case <-r.Context().Done():
				cancelOnce.Do(func() { close(servicesCancelled) })
			case <-release:
			}
		default:
			http.NotFound(w, r)
		}
	}))
	// Cleanups run last-in-first-out: release any held request before Close
	// waits for in-flight handlers.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}
	h, _ := testHandler(t)
	h.remoteClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return cs, nil
	}

	// Serve in a goroutine so a missing deadline fails this test with a
	// message instead of hanging the package run. The release cleanup above
	// unblocks the stuck handler either way.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callRemoteDashboard(t, h, "coverage=1") }()
	var rr *httptest.ResponseRecorder
	select {
	case rr = <-done:
	case <-time.After(3 * time.Second):
		// Well under the 5s production budget, so only the shortened budget
		// can have ended the request in time.
		t.Fatalf("handler did not return within 3s on a %s budget; the hung services list was not cut off", remoteDashboardBudget)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	s := decodeDashboard(t, rr)

	select {
	case <-servicesCancelled:
	case <-time.After(2 * time.Second):
		t.Error("the services request was never cancelled by the budget deadline")
	}
	assertRow(t, s, "services", "unavailable", "unreachable")
	assertRow(t, s, "nodes", "ok", "ok")
	assertRow(t, s, "pods", "ok", "ok")
	if s.Nodes.Total != 1 || s.Pods.Total != 1 {
		t.Errorf("sections read before the deadline lost data: nodes=%+v pods=%+v", s.Nodes, s.Pods)
	}
}

func TestRemoteSummary_OneSectionTimeoutDoesNotFailOthers(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil, readyNode("r-n1"), runningPod("default", "r-p1"), service("r-s1"))
	remote.PrependReactor("list", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})

	s := remoteSummaryOK(t, h)

	assertRow(t, s, "services", "unavailable", "unreachable")
	assertRow(t, s, "nodes", "ok", "ok")
	assertRow(t, s, "pods", "ok", "ok")
	if s.Nodes.Total != 1 || s.Pods.Total != 1 {
		t.Errorf("unrelated sections lost data: nodes=%+v pods=%+v", s.Nodes, s.Pods)
	}
	if s.Services.Total != 0 {
		t.Errorf("services.total = %d, want 0 when the section failed", s.Services.Total)
	}
}

func TestRemoteSummary_SectionPanicIsRecoveredAsUnavailable(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil, readyNode("r-n1"), runningPod("default", "r-p1"), service("r-s1"))
	remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		panic("boom")
	})

	s := remoteSummaryOK(t, h)

	row := coverageRow(t, s, "pods")
	if row.Status != "unavailable" || row.ReasonCode != "unreachable" || row.Detail != "section failed to load" {
		t.Errorf("pods row = %+v, want unavailable/unreachable/'section failed to load'", row)
	}
	assertRow(t, s, "nodes", "ok", "ok")
	assertRow(t, s, "services", "ok", "ok")
	if s.Nodes.Total != 1 || s.Services.Total != 1 {
		t.Errorf("sibling sections lost data: nodes=%+v services=%+v", s.Nodes, s.Services)
	}
	if s.Pods != (PodSummary{}) {
		t.Errorf("pods = %+v, want zero after a recovered panic", s.Pods)
	}
}

func TestRemoteSummary_PodsUnreadReservationsUnknown(t *testing.T) {
	// Nodes load, so allocatable capacity is real. Requests and limits come
	// from pods, so when pods did not load they are unknown, not zero.
	cases := map[string]func(h *Handler, remote *fake.Clientset){
		"forbidden": func(h *Handler, _ *fake.Clientset) {
			h.AccessChecker = NewDenyResourcesAccessChecker("pods")
		},
		"unreachable": func(_ *Handler, remote *fake.Clientset) {
			remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("connection refused")
			})
		},
		"panicked": func(_ *Handler, remote *fake.Clientset) {
			remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
				panic("boom")
			})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h, remote := remoteDashboardHandler(t, nil,
				nodeWithAllocatable("r-n1", "4", "8Gi"),
				podWithResources("r-p1", corev1.PodRunning, "1", "2", "1Gi", "2Gi"),
			)
			setup(h, remote)

			s := remoteSummaryOK(t, h)

			wantCPU := Utilization{Percentage: 0, Used: "N/A", Total: "4.0 cores", Requests: "N/A", Limits: "N/A"}
			if s.CPU == nil || *s.CPU != wantCPU {
				t.Errorf("cpu = %+v, want %+v", s.CPU, wantCPU)
			}
			wantMem := Utilization{Percentage: 0, Used: "N/A", Total: "8.0 Gi", Requests: "N/A", Limits: "N/A"}
			if s.Memory == nil || *s.Memory != wantMem {
				t.Errorf("memory = %+v, want %+v", s.Memory, wantMem)
			}
			for _, sec := range []string{"cpu", "memory"} {
				row := coverageRow(t, s, sec)
				if row.Status != "unavailable" || row.ReasonCode != "metrics_not_configured" {
					t.Errorf("%s row = %+v, want unavailable/metrics_not_configured", sec, row)
				}
				if !strings.Contains(row.Detail, "pods") {
					t.Errorf("%s row detail %q does not say requests/limits need the pods section", sec, row.Detail)
				}
			}
		})
	}
}

func TestRemoteSummary_EmptyCluster(t *testing.T) {
	h, _ := remoteDashboardHandler(t, nil)

	s := remoteSummaryOK(t, h)

	if s.Nodes != (NodeSummary{}) || s.Pods != (PodSummary{}) || s.Services != (ServiceCount{}) {
		t.Errorf("counts = %+v %+v %+v, want zero", s.Nodes, s.Pods, s.Services)
	}
	// An empty answer is still a complete answer.
	assertRow(t, s, "nodes", "ok", "ok")
	assertRow(t, s, "pods", "ok", "ok")
	if s.Health != nil {
		t.Errorf("Health = %+v, want nil", s.Health)
	}
	if s.CPU != nil || s.Memory != nil {
		t.Errorf("cpu/memory = %+v/%+v, want nil with no allocatable capacity", s.CPU, s.Memory)
	}
}

func TestRemoteSummary_TruncatedListIsPartial(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil)
	pages := 0
	remote.PrependReactor("list", "nodes", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pages++
		opts := a.(k8stesting.ListActionImpl).ListOptions
		if opts.Limit != k8s.RemoteListPageSize {
			t.Errorf("page %d requested Limit=%d, want %d", pages, opts.Limit, k8s.RemoteListPageSize)
		}
		// Every page claims there is more — the pager must stop at its cap.
		return true, &corev1.NodeList{
			ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", pages)},
			Items:    []corev1.Node{*readyNode(fmt.Sprintf("n-%d", pages))},
		}, nil
	})

	s := remoteSummaryOK(t, h)

	row := coverageRow(t, s, "nodes")
	if row.Status != "partial" {
		t.Errorf("nodes status = %q, want partial", row.Status)
	}
	if row.Detail == "" {
		t.Error("partial row has no detail explaining the truncation")
	}
	if pages != k8s.RemoteListMaxPages {
		t.Errorf("pager fetched %d pages, want cap %d", pages, k8s.RemoteListMaxPages)
	}
	if s.Nodes.Total != k8s.RemoteListMaxPages {
		t.Errorf("nodes.total = %d, want the %d items actually read", s.Nodes.Total, k8s.RemoteListMaxPages)
	}
}

func TestRemoteSummary_PagesUntilContinueIsEmpty(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil)
	pages := 0
	remote.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pages++
		opts := a.(k8stesting.ListActionImpl).ListOptions
		wantContinue := ""
		if pages > 1 {
			wantContinue = fmt.Sprintf("tok-%d", pages-1)
		}
		if opts.Continue != wantContinue {
			t.Errorf("page %d Continue = %q, want %q", pages, opts.Continue, wantContinue)
		}
		list := &corev1.PodList{Items: []corev1.Pod{*runningPod("default", fmt.Sprintf("p-%d", pages))}}
		if pages < 3 {
			list.Continue = fmt.Sprintf("tok-%d", pages)
		}
		return true, list, nil
	})

	s := remoteSummaryOK(t, h)
	assertRow(t, s, "pods", "ok", "ok")
	if s.Pods.Total != 3 || s.Pods.Running != 3 {
		t.Errorf("pods = %+v, want 3 running across 3 pages", s.Pods)
	}
}

func TestRemoteSummary_StaleCarriesObservedAt(t *testing.T) {
	// v1 acquisition is live per request, so no row is ever stale today.
	// The invariant that makes a future cached/stale row honest is that
	// every row backed by a live read carries observedAt — pin that here,
	// across success, partial, and failure shapes.
	h, remote := remoteDashboardHandler(t, nil, readyNode("r-n1"), runningPod("default", "r-p1"))
	h.AccessChecker = NewDenyResourcesAccessChecker("services")
	remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})

	s := remoteSummaryOK(t, h)
	for _, row := range s.Coverage {
		if row.Status == "stale" && row.ObservedAt == "" {
			t.Errorf("stale row %q has no observedAt", row.Section)
		}
		if row.Status == "ok" || row.Status == "partial" {
			if row.ObservedAt == "" {
				t.Errorf("live-read row %q (%s) has no observedAt", row.Section, row.Status)
			}
		}
	}
	if coverageRow(t, s, "nodes").ObservedAt == "" {
		t.Error("nodes row (ok) must carry observedAt")
	}
}

func TestRemoteSummary_NeverUsesInformers(t *testing.T) {
	// Local informers hold 5 nodes / 4 pods / 3 services; the remote holds
	// 1 / 1 / 1. Any local value in the response is a silent fallback.
	local := []runtime.Object{
		nodeWithAllocatable("l-n1", "64", "256Gi"), nodeWithAllocatable("l-n2", "64", "256Gi"),
		readyNode("l-n3"), readyNode("l-n4"), readyNode("l-n5"),
		runningPod("default", "l-p1"), runningPod("default", "l-p2"),
		runningPod("default", "l-p3"), runningPod("default", "l-p4"),
		service("l-s1"), service("l-s2"), service("l-s3"),
	}
	h, _ := remoteDashboardHandler(t, local,
		nodeWithAllocatable("r-n1", "2", "4Gi"), runningPod("default", "r-p1"), service("r-s1"))
	h.Alerts = &fakeAlertCounter{active: 9, critical: 7}
	h.ControlPlane = &fakeControlPlane{}

	s := remoteSummaryOK(t, h)

	if s.Nodes.Total != 1 || s.Pods.Total != 1 || s.Services.Total != 1 {
		t.Errorf("counts = nodes %d / pods %d / services %d, want 1/1/1 (remote only)",
			s.Nodes.Total, s.Pods.Total, s.Services.Total)
	}
	if s.Alerts != (AlertSummary{}) {
		t.Errorf("alerts = %+v, want zero — the alert counter is bound to the local Alertmanager", s.Alerts)
	}
	if s.CPU == nil || s.CPU.Total != "2.0 cores" {
		t.Errorf("cpu = %+v, want remote allocatable 2.0 cores", s.CPU)
	}
}

func TestRemoteSummary_ClientResolveFailureDoesNotFallBackLocal(t *testing.T) {
	h, _ := testHandler(t, readyNode("l-n1"))
	h.remoteClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return nil, errors.New("cluster not found")
	}

	rr := callRemoteDashboard(t, h, "coverage=1")
	if rr.Code < 500 {
		t.Fatalf("status = %d, want a 5xx when the remote target cannot be resolved", rr.Code)
	}
	if strings.Contains(rr.Body.String(), `"nodes"`) {
		t.Errorf("error response carries summary data: %s", rr.Body.String())
	}
}

func TestRemoteSummary_ProductionRouterFailsClosed(t *testing.T) {
	// Every other remote test installs h.remoteClient. This one leaves it
	// nil, so remoteClientFor takes the production branch through a real
	// ClusterRouter. With no cluster registry wired, the router must refuse
	// the remote target rather than fall back to local clients.
	h, _ := testHandler(t, readyNode("l-n1"), runningPod("default", "l-p1"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// A local factory the fallback would have to use. It wraps an empty
	// clientset, so any local list through it would fail loudly, and the
	// informers seeded above make any local count in the body detectable.
	local := k8s.NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	h.ClusterRouter = k8s.NewClusterRouter(local, nil, "", logger)
	if h.remoteClient != nil {
		t.Fatal("test precondition: remoteClient override must be unset")
	}

	rr := callRemoteDashboard(t, h, "coverage=1")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the router cannot resolve the remote target: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"nodes"`) || strings.Contains(rr.Body.String(), `"coverage"`) {
		t.Errorf("error response carries summary data, so something answered locally: %s", rr.Body.String())
	}
}

// fakeUtilization implements UtilizationProvider with fixed percentages (or
// err, or a panic) and records every cluster id it was asked about.
type fakeUtilization struct {
	cpu, mem float64
	err      error
	panics   bool

	mu       sync.Mutex
	clusters []string
}

func (f *fakeUtilization) read(clusterID string, v float64) (float64, error) {
	f.mu.Lock()
	f.clusters = append(f.clusters, clusterID)
	f.mu.Unlock()
	if f.panics {
		panic("boom")
	}
	if f.err != nil {
		return 0, f.err
	}
	return v, nil
}

func (f *fakeUtilization) CPUPercent(_ context.Context, clusterID string) (float64, error) {
	return f.read(clusterID, f.cpu)
}

func (f *fakeUtilization) MemoryPercent(_ context.Context, clusterID string) (float64, error) {
	return f.read(clusterID, f.mem)
}

// assertOnlyCluster fails unless every read named clusterID, and there was
// at least one.
func (f *fakeUtilization) assertOnlyCluster(t *testing.T, clusterID string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.clusters) == 0 {
		t.Fatal("the utilization provider was never asked")
	}
	for _, c := range f.clusters {
		if c != clusterID {
			t.Errorf("the utilization provider was asked about cluster %q, want only %q", c, clusterID)
		}
	}
}

// ── Review round 2 follow-ups ──────────────────────────────────────────────

// truncatingReactor makes every list page of resource claim there is more,
// so the pager stops at its cap and reports the section partial.
func truncatingReactor(remote *fake.Clientset, resource string, page func(n int) runtime.Object) {
	n := 0
	remote.PrependReactor("list", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		n++
		return true, page(n), nil
	})
}

func TestRemoteSummary_TruncatedSectionsArePartial(t *testing.T) {
	cases := map[string]func(n int) runtime.Object{
		"pods": func(n int) runtime.Object {
			return &corev1.PodList{
				ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", n)},
				Items:    []corev1.Pod{*podWithResources(fmt.Sprintf("p-%d", n), corev1.PodRunning, "100m", "200m", "64Mi", "128Mi")},
			}
		},
		"services": func(n int) runtime.Object {
			return &corev1.ServiceList{
				ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", n)},
				Items:    []corev1.Service{*service(fmt.Sprintf("s-%d", n))},
			}
		},
	}
	for section, page := range cases {
		t.Run(section, func(t *testing.T) {
			h, remote := remoteDashboardHandler(t, nil, nodeWithAllocatable("r-n1", "4", "8Gi"))
			truncatingReactor(remote, section, page)

			s := remoteSummaryOK(t, h)

			row := coverageRow(t, s, section)
			if row.Status != "partial" || row.ObservedAt == "" || !strings.Contains(row.Detail, "truncated") {
				t.Errorf("%s row = %+v, want partial with observedAt and a truncation detail", section, row)
			}
		})
	}
}

func TestRemoteSummary_PartialPodsQualifyReservations(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil, nodeWithAllocatable("r-n1", "4", "8Gi"))
	truncatingReactor(remote, "pods", func(n int) runtime.Object {
		return &corev1.PodList{
			ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", n)},
			Items:    []corev1.Pod{*podWithResources(fmt.Sprintf("p-%d", n), corev1.PodRunning, "100m", "200m", "64Mi", "128Mi")},
		}
	})

	s := remoteSummaryOK(t, h)

	// The sums over the pods that were read are a real lower bound, so they
	// are kept, but the cpu/memory rows must say so.
	wantCPU := Utilization{Percentage: 0, Used: "N/A", Total: "4.0 cores", Requests: "1.0 cores", Limits: "2.0 cores"}
	if s.CPU == nil || *s.CPU != wantCPU {
		t.Errorf("cpu = %+v, want %+v (sums over the %d pods read)", s.CPU, wantCPU, k8s.RemoteListMaxPages)
	}
	for _, sec := range []string{"cpu", "memory"} {
		if d := coverageRow(t, s, sec).Detail; !strings.Contains(d, "truncated pod list") {
			t.Errorf("%s row detail %q does not say requests/limits come from a truncated pod list", sec, d)
		}
	}
}

func TestRemoteSummary_PartialNodesQualifyAllocatable(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil)
	truncatingReactor(remote, "nodes", func(n int) runtime.Object {
		return &corev1.NodeList{
			ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", n)},
			Items:    []corev1.Node{*nodeWithAllocatable(fmt.Sprintf("n-%d", n), "1", "1Gi")},
		}
	})

	s := remoteSummaryOK(t, h)

	if s.CPU == nil || s.CPU.Total != "10.0 cores" {
		t.Errorf("cpu = %+v, want total over the %d nodes read", s.CPU, k8s.RemoteListMaxPages)
	}
	for _, sec := range []string{"cpu", "memory"} {
		if d := coverageRow(t, s, sec).Detail; !strings.Contains(d, "nodes that loaded") {
			t.Errorf("%s row detail %q does not say allocatable covers only the nodes that loaded", sec, d)
		}
	}
}

func TestRemoteSummary_LaterPageFailureKeepsReadItems(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil)
	pages := 0
	remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		pages++
		if pages == 1 {
			return true, &corev1.PodList{
				ListMeta: metav1.ListMeta{Continue: "tok-1"},
				Items:    []corev1.Pod{*runningPod("default", "p-1"), *runningPod("default", "p-2")},
			}, nil
		}
		return true, nil, errors.New("connection reset by peer")
	})

	s := remoteSummaryOK(t, h)

	row := coverageRow(t, s, "pods")
	if row.Status != "partial" || row.ReasonCode != "unreachable" || row.ObservedAt == "" {
		t.Errorf("pods row = %+v, want partial/unreachable with observedAt", row)
	}
	if !strings.Contains(row.Detail, "2 items") {
		t.Errorf("pods row detail %q does not say how many items were read before the failure", row.Detail)
	}
	if s.Pods.Total != 2 || s.Pods.Running != 2 {
		t.Errorf("pods = %+v, want the 2 pods read before the failure", s.Pods)
	}
}

func TestRemoteSummary_LaterPageForbiddenDropsItems(t *testing.T) {
	// A permission refusal mid-list is still a refusal: counting what was
	// read before RBAC changed would report data the user may no longer see.
	h, remote := remoteDashboardHandler(t, nil)
	pages := 0
	remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		pages++
		if pages == 1 {
			return true, &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "tok-1"}, Items: []corev1.Pod{*runningPod("default", "p-1")}}, nil
		}
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("denied"))
	})

	s := remoteSummaryOK(t, h)
	assertRow(t, s, "pods", "forbidden", "authz_namespace_scoped")
	if s.Pods != (PodSummary{}) {
		t.Errorf("pods = %+v, want zero after a mid-list refusal", s.Pods)
	}
}

func TestRemoteSummary_BudgetCoversClientResolution(t *testing.T) {
	// Resolving the remote client (cluster-store read, credential decrypt,
	// dial) is part of the request and must also end at the budget.
	prev := remoteDashboardBudget
	remoteDashboardBudget = 200 * time.Millisecond
	t.Cleanup(func() { remoteDashboardBudget = prev })

	h, _ := testHandler(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.remoteClient = func(ctx context.Context, _ string, _ *auth.User) (kubernetes.Interface, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, errors.New("released by test cleanup")
		}
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callRemoteDashboard(t, h, "coverage=1") }()
	select {
	case rr := <-done:
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500 when client resolution runs out of budget", rr.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("handler did not return within 3s on a %s budget; client resolution is not bounded by it", remoteDashboardBudget)
	}
}

// goldenLocalSummary is the exact local response produced by main @ 1e3a053c
// (before U10) for the fixture in TestDashboardSummary_LocalResponseMatchesPreU10Golden.
// It was captured by running that fixture against the pre-U10 code, so any
// drift in the local shape, counts, capacity strings or health fails here.
const goldenLocalSummary = `{"data":{"nodes":{"total":2,"ready":1},"pods":{"total":3,"running":1,"pending":1,"failed":1},"services":{"total":1},"alerts":{"active":3,"critical":1},"cpu":{"percentage":0,"used":"N/A","total":"4.0 cores","requests":"250m","limits":"1.0 cores"},"memory":{"percentage":0,"used":"N/A","total":"8.0 Gi","requests":"256 Mi","limits":"1.0 Gi"},"health":{"status":"critical","score":81,"signals":[{"name":"nodes","status":"ok","score":50},{"name":"workloads","status":"ok","score":100},{"name":"pods","status":"ok","score":100},{"name":"alerts","status":"ok","score":84},{"name":"certificates","status":"skipped","score":null,"reason":"cert-manager not configured"},{"name":"storage","status":"ok","score":null},{"name":"controlPlane","status":"skipped","score":null,"reason":"control plane data unavailable"}],"reasons":["1 of 2 nodes not ready","1 critical alert(s) firing"]}}}`

func TestDashboardSummary_LocalResponseMatchesPreU10Golden(t *testing.T) {
	n := readyNode("n1")
	n.Status.Allocatable = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")}
	notReady := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n2"}}
	p := runningPod("default", "p1")
	p.Spec.Containers = []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("256Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
	}}}
	pending := runningPod("default", "p2")
	pending.Status.Phase = corev1.PodPending
	failed := runningPod("default", "p3")
	failed.Status.Phase = corev1.PodFailed
	h, _ := testHandler(t, n, notReady, p, pending, failed, service("s1"), deployment1x1("default", "d1"))
	h.Alerts = &fakeAlertCounter{active: 3, critical: 1}

	for _, query := range []string{"", "coverage=1"} {
		path := "/api/v1/cluster/dashboard-summary"
		if query != "" {
			path += "?" + query
		}
		rr := httptest.NewRecorder()
		h.HandleDashboardSummary(rr, requestWithUser("GET", path, ""))
		if rr.Code != http.StatusOK {
			t.Fatalf("%q: status = %d", query, rr.Code)
		}
		if got := strings.TrimSpace(rr.Body.String()); got != goldenLocalSummary {
			t.Errorf("local response (query %q) differs from the pre-U10 golden:\n got: %s\nwant: %s", query, got, goldenLocalSummary)
		}
	}
}
