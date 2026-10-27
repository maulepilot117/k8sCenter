package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testPod(ns, name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

// podFixtures holds one pod per phase plus a second Failed pod in another
// namespace, so a filter that drops namespaces or matches the wrong phase is
// caught.
func podFixtures() []runtime.Object {
	return []runtime.Object{
		testPod("default", "pending", corev1.PodPending),
		testPod("default", "running", corev1.PodRunning),
		testPod("default", "succeeded", corev1.PodSucceeded),
		testPod("default", "failed", corev1.PodFailed),
		testPod("default", "unknown", corev1.PodUnknown),
		testPod("minigolf", "job-failed", corev1.PodFailed),
	}
}

// listPods fires HandleListResource for pods against clusterID. ns may be
// empty (all namespaces).
func listPods(t *testing.T, h *Handler, clusterID, ns string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/resources/pods"
	if ns != "" {
		path += "/" + ns
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	req := requestWithUser("GET", path, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "pods")
	if ns != "" {
		rctx.URLParams.Add("namespace", ns)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if clusterID != "" {
		ctx = middleware.WithClusterID(ctx, clusterID)
	}
	rr := httptest.NewRecorder()
	h.HandleListResource(rr, req.WithContext(ctx))
	return rr
}

// podNames decodes a 200 list response and returns the sorted pod names plus
// the reported total.
func podNames(t *testing.T, rr *httptest.ResponseRecorder) ([]string, int) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("re-marshal data: %v", err)
	}
	var pods []corev1.Pod
	if err := json.Unmarshal(raw, &pods); err != nil {
		t.Fatalf("decode pods: %v (%s)", err, raw)
	}
	names := make([]string, 0, len(pods))
	for _, p := range pods {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	total := -1
	if resp.Metadata != nil {
		total = resp.Metadata.Total
	}
	return names, total
}

func assertPodNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pods = %v, want %v", got, want)
	}
}

func phaseQuery(phases ...string) url.Values {
	return url.Values{queryPhase: phases}
}

// podListSelectors returns the field selector of every pods list the fake
// clientset received.
func podListSelectors(t *testing.T, cs *fake.Clientset) []string {
	t.Helper()
	var out []string
	for _, a := range cs.Actions() {
		la, ok := a.(k8stesting.ListAction)
		if !ok || a.GetResource().Resource != "pods" {
			continue
		}
		out = append(out, la.GetListRestrictions().Fields.String())
	}
	return out
}

// --- local (informer) path ---

func TestListPods_LocalFailedAcrossNamespaces(t *testing.T) {
	h, _ := testHandler(t, podFixtures()...)
	names, total := podNames(t, listPods(t, h, "", "", phaseQuery("Failed")))
	// Unknown is not Failed: the dashboard's pods.failed count excludes it.
	assertPodNames(t, names, "failed", "job-failed")
	if total != 2 {
		t.Errorf("total = %d, want 2 (the filtered set)", total)
	}
}

func TestListPods_LocalFilterKeepsNamespace(t *testing.T) {
	h, _ := testHandler(t, podFixtures()...)
	names, _ := podNames(t, listPods(t, h, "", "minigolf", phaseQuery("Failed")))
	assertPodNames(t, names, "job-failed")
}

func TestListPods_LocalSeveralPhasesMatchAny(t *testing.T) {
	h, _ := testHandler(t, podFixtures()...)
	names, total := podNames(t, listPods(t, h, "", "", phaseQuery("Running", "Succeeded")))
	assertPodNames(t, names, "running", "succeeded")
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
}

func TestListPods_LocalUnfilteredUnchanged(t *testing.T) {
	h, _ := testHandler(t, podFixtures()...)
	names, total := podNames(t, listPods(t, h, "", "", nil))
	assertPodNames(t, names, "pending", "running", "succeeded", "failed", "unknown", "job-failed")
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
}

func TestListPods_EmptyPhaseParamIgnored(t *testing.T) {
	h, _ := testHandler(t, podFixtures()...)
	names, _ := podNames(t, listPods(t, h, "", "default", phaseQuery("")))
	assertPodNames(t, names, "pending", "running", "succeeded", "failed", "unknown")
}

// The point of filtering on the server: the filtered set is what gets paged,
// so a failed pod is never hidden behind unfiltered pages.
func TestListPods_LocalFilterPaginatesFilteredSet(t *testing.T) {
	h, _ := testHandler(t, podFixtures()...)
	q := phaseQuery("Failed")
	q.Set("limit", "1")
	rr := listPods(t, h, "", "", q)
	names, _ := podNames(t, rr)
	if len(names) != 1 {
		t.Fatalf("page = %v, want one pod", names)
	}
	total, cont, _ := listMetadata(t, listPods(t, h, "", "", q))
	if total != 2 || cont == "" {
		t.Errorf("total = %d, continue = %q; want 2 and a token for the second failed pod", total, cont)
	}
	q.Set("continue", cont)
	second, _ := podNames(t, listPods(t, h, "", "", q))
	if len(second) != 1 || second[0] == names[0] {
		t.Errorf("second page = %v after %v, want the other failed pod", second, names)
	}
}

// --- remote (direct API) path ---

func TestListPods_RemoteSinglePhaseSendsFieldSelector(t *testing.T) {
	h, local, remote := remoteEventsHandler(t, podFixtures(),
		testPod("default", "remote-failed", corev1.PodFailed),
		testPod("default", "remote-running", corev1.PodRunning),
	)
	local.ClearActions()

	// The fake ignores field selectors, so this also proves the handler
	// re-filters what the API server returns.
	names, _ := podNames(t, listPods(t, h, remoteTestClusterID, "default", phaseQuery("Failed")))
	assertPodNames(t, names, "remote-failed")

	sels := podListSelectors(t, remote)
	if len(sels) == 0 {
		t.Fatal("remote clientset received no pods list")
	}
	for _, s := range sels {
		if s != "status.phase=Failed" {
			t.Errorf("remote field selector = %q, want status.phase=Failed", s)
		}
	}
	if got := podListSelectors(t, local); len(got) != 0 {
		t.Errorf("local clientset listed pods for a remote request: %v", got)
	}
}

func TestListPods_RemoteSeveralPhasesFilterAfterList(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil,
		testPod("default", "r-running", corev1.PodRunning),
		testPod("default", "r-succeeded", corev1.PodSucceeded),
		testPod("default", "r-failed", corev1.PodFailed),
	)
	names, _ := podNames(t, listPods(t, h, remoteTestClusterID, "", phaseQuery("Running", "Succeeded")))
	assertPodNames(t, names, "r-running", "r-succeeded")
	// Field selectors have no OR, so nothing is pushed down for two phases.
	for _, s := range podListSelectors(t, remote) {
		if s != "" {
			t.Errorf("two-phase remote list sent field selector %q, want none", s)
		}
	}
}

func TestListPods_RemoteUnfilteredSendsNoSelector(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil, testPod("default", "r-a", corev1.PodFailed))
	names, _ := podNames(t, listPods(t, h, remoteTestClusterID, "", nil))
	assertPodNames(t, names, "r-a")
	for _, s := range podListSelectors(t, remote) {
		if s != "" {
			t.Errorf("unfiltered remote list sent field selector %q, want none", s)
		}
	}
}

func TestListPods_InvalidPhaseRejected(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, podFixtures())
	for _, phase := range []string{"failed", "Crashed", "Failed,Running", "status.phase=Failed"} {
		for _, cluster := range []string{"", remoteTestClusterID} {
			rr := listPods(t, h, cluster, "", phaseQuery(phase))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("phase %q on cluster %q: status = %d, want 400", phase, cluster, rr.Code)
			}
		}
	}
	if got := podListSelectors(t, remote); len(got) != 0 {
		t.Errorf("an invalid phase still reached the remote API server: %v", got)
	}
}

func TestPodPhaseFilter_FieldSelector(t *testing.T) {
	cases := []struct {
		phases []string
		want   string
	}{
		{nil, ""},
		{[]string{"Failed"}, "status.phase=Failed"},
		{[]string{"Failed", "Failed"}, "status.phase=Failed"},
		{[]string{"Running", "Succeeded"}, ""},
	}
	for _, c := range cases {
		f, err := parsePodPhaseFilter(phaseQuery(c.phases...))
		if err != nil {
			t.Fatalf("parse %v: %v", c.phases, err)
		}
		if got := f.fieldSelector(); got != c.want {
			t.Errorf("fieldSelector(%v) = %q, want %q", c.phases, got, c.want)
		}
	}
}
