package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
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
	// A wired Prometheus provider must not leak into a remote response: it
	// is bound to the local cluster.
	h.Utilization = &fakeUtilization{cpu: 42, mem: 42}

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
	assertRow(t, s, "cpu", "unavailable", "unsupported_platform")
	assertRow(t, s, "memory", "unavailable", "unsupported_platform")
	assertRow(t, s, "alerts", "unavailable", "unsupported_platform")
	assertRow(t, s, "nodes", "ok", "ok")
	assertRow(t, s, "pods", "ok", "ok")
	assertRow(t, s, "services", "ok", "ok")
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
				if row.Status != "unavailable" || row.ReasonCode != "unsupported_platform" {
					t.Errorf("%s row = %+v, want unavailable/unsupported_platform", sec, row)
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
		if opts.Limit != remoteListPageSize {
			t.Errorf("page %d requested Limit=%d, want %d", pages, opts.Limit, remoteListPageSize)
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
	if pages != remoteListMaxPages {
		t.Errorf("pager fetched %d pages, want cap %d", pages, remoteListMaxPages)
	}
	if s.Nodes.Total != remoteListMaxPages {
		t.Errorf("nodes.total = %d, want the %d items actually read", s.Nodes.Total, remoteListMaxPages)
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

// fakeUtilization implements UtilizationProvider with fixed percentages.
type fakeUtilization struct{ cpu, mem float64 }

func (f *fakeUtilization) CPUPercent(context.Context) (float64, error)    { return f.cpu, nil }
func (f *fakeUtilization) MemoryPercent(context.Context) (float64, error) { return f.mem, nil }
