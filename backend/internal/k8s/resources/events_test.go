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
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// testEvent returns an Event in ns about the involved object kind/name.
func testEvent(ns, name, kind, objName string) *corev1.Event {
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		InvolvedObject: corev1.ObjectReference{
			Kind:      kind,
			Name:      objName,
			Namespace: ns,
		},
		Reason:  "Test",
		Message: name,
	}
}

// eventFixtures holds events about two pods and a same-named Deployment, so a
// filter that matches on name alone, or on kind alone, is caught.
func eventFixtures() []runtime.Object {
	return []runtime.Object{
		testEvent("default", "web-1.a", "Pod", "web-1"),
		testEvent("default", "web-1.b", "Pod", "web-1"),
		testEvent("default", "web-2.a", "Pod", "web-2"),
		testEvent("default", "web-1.dep", "Deployment", "web-1"),
		testEvent("other", "web-1.other", "Pod", "web-1"),
	}
}

// listEvents fires HandleListResource for events against clusterID. ns may be
// empty (all namespaces); query is the raw query string.
func listEvents(t *testing.T, h *Handler, clusterID, ns string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/resources/events"
	if ns != "" {
		path += "/" + ns
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	req := requestWithUser("GET", path, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "events")
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

// eventNames decodes a 200 list response and returns the sorted event names
// plus the reported total.
func eventNames(t *testing.T, rr *httptest.ResponseRecorder) ([]string, int) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("re-marshal data: %v", err)
	}
	var evts []corev1.Event
	if err := json.Unmarshal(raw, &evts); err != nil {
		t.Fatalf("decode events: %v (%s)", err, raw)
	}
	names := make([]string, 0, len(evts))
	for _, e := range evts {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	total := -1
	if resp.Metadata != nil {
		total = resp.Metadata.Total
	}
	return names, total
}

func filterQuery(kind, name string) url.Values {
	q := url.Values{}
	q.Set("involvedObjectKind", kind)
	q.Set("involvedObjectName", name)
	return q
}

func assertNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// remoteEventsHandler builds a Handler whose local informers hold localObjs
// and whose remote client is a separate fake seeded with remoteObjs.
func remoteEventsHandler(t *testing.T, localObjs []runtime.Object, remoteObjs ...runtime.Object) (*Handler, *fake.Clientset, *fake.Clientset) {
	t.Helper()
	h, local := testHandler(t, localObjs...)
	remote := fake.NewSimpleClientset(remoteObjs...)
	h.remoteClient = func(_ context.Context, clusterID string, _ *auth.User) (kubernetes.Interface, error) {
		if clusterID != remoteTestClusterID {
			t.Errorf("remote client resolved for cluster %q, want %q", clusterID, remoteTestClusterID)
		}
		return remote, nil
	}
	return h, local, remote
}

// eventListSelectors returns the field selector of every events list the fake
// clientset received.
func eventListSelectors(t *testing.T, cs *fake.Clientset) []string {
	t.Helper()
	var out []string
	for _, a := range cs.Actions() {
		la, ok := a.(k8stesting.ListAction)
		if !ok || a.GetResource().Resource != "events" {
			continue
		}
		out = append(out, la.GetListRestrictions().Fields.String())
	}
	return out
}

// --- local (informer) path ---

func TestListEvents_LocalFilteredToInvolvedObject(t *testing.T) {
	h, _ := testHandler(t, eventFixtures()...)
	names, total := eventNames(t, listEvents(t, h, "", "default", filterQuery("Pod", "web-1")))
	assertNames(t, names, "web-1.a", "web-1.b")
	if total != 2 {
		t.Errorf("total = %d, want 2 (the filtered count, not the namespace count)", total)
	}
}

func TestListEvents_LocalFilterAcrossNamespaces(t *testing.T) {
	// Cluster-scoped resources (a Node) fetch events without a namespace.
	objs := append(eventFixtures(),
		testEvent("default", "node-1.a", "Node", "node-1"),
		testEvent("default", "node-2.a", "Node", "node-2"),
	)
	h, _ := testHandler(t, objs...)
	names, _ := eventNames(t, listEvents(t, h, "", "", filterQuery("Node", "node-1")))
	assertNames(t, names, "node-1.a")
}

func TestListEvents_LocalUnfilteredUnchanged(t *testing.T) {
	h, _ := testHandler(t, eventFixtures()...)
	names, total := eventNames(t, listEvents(t, h, "", "default", nil))
	assertNames(t, names, "web-1.a", "web-1.b", "web-2.a", "web-1.dep")
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}

	all, _ := eventNames(t, listEvents(t, h, "", "", url.Values{"limit": {"10"}}))
	assertNames(t, all, "web-1.a", "web-1.b", "web-2.a", "web-1.dep", "web-1.other")
}

func TestListEvents_LocalFilterPaginatesFilteredSet(t *testing.T) {
	h, _ := testHandler(t, eventFixtures()...)
	q := filterQuery("Pod", "web-1")
	q.Set("limit", "1")
	rr := listEvents(t, h, "", "", q)
	names, total := eventNames(t, rr)
	if len(names) != 1 || total != 3 {
		t.Errorf("page = %v total = %d, want 1 item of 3 matching Pod/web-1 events", names, total)
	}
}

// --- remote (direct API) path ---

func TestListEvents_RemoteFilteredSendsFieldSelector(t *testing.T) {
	h, local, remote := remoteEventsHandler(t, eventFixtures(),
		testEvent("default", "remote-web-1", "Pod", "web-1"),
		testEvent("default", "remote-web-2", "Pod", "web-2"),
	)
	local.ClearActions()

	names, _ := eventNames(t, listEvents(t, h, remoteTestClusterID, "default", filterQuery("Pod", "web-1")))
	assertNames(t, names, "remote-web-1")

	sels := eventListSelectors(t, remote)
	if len(sels) == 0 {
		t.Fatal("remote clientset received no events list")
	}
	for _, s := range sels {
		if s != "involvedObject.kind=Pod,involvedObject.name=web-1" {
			t.Errorf("remote field selector = %q, want involvedObject.kind=Pod,involvedObject.name=web-1", s)
		}
	}
	if got := eventListSelectors(t, local); len(got) != 0 {
		t.Errorf("local clientset listed events for a remote request: %v", got)
	}
}

func TestListEvents_RemoteUnfilteredReadsRemoteNotLocal(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, eventFixtures(),
		testEvent("default", "remote-a", "Pod", "x"),
	)
	names, _ := eventNames(t, listEvents(t, h, remoteTestClusterID, "", url.Values{"limit": {"10"}}))
	assertNames(t, names, "remote-a")
	for _, s := range eventListSelectors(t, remote) {
		if s != "" {
			t.Errorf("unfiltered remote list sent field selector %q, want none", s)
		}
	}
}

func TestListEvents_RemoteEscapesSelectorValues(t *testing.T) {
	// RBAC object names may contain characters that are selector syntax; the
	// value must stay a literal and never add a second term.
	h, _, remote := remoteEventsHandler(t, nil)
	name := "a,involvedObject.kind=Secret"
	rr := listEvents(t, h, remoteTestClusterID, "default", filterQuery("ClusterRole", name))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	sels := eventListSelectors(t, remote)
	want := `involvedObject.kind=ClusterRole,involvedObject.name=a\,involvedObject.kind\=Secret`
	if len(sels) == 0 || sels[0] != want {
		t.Errorf("remote field selector = %v, want [%s]", sels, want)
	}
}

// --- invalid parameters ---

func TestListEvents_InvalidFilterRejected(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, eventFixtures())
	cases := map[string]url.Values{
		"kind with selector syntax": filterQuery("Pod,involvedObject.name=x", "web-1"),
		"kind with space":           filterQuery("Pod Disruption", "web-1"),
		"kind too long":             filterQuery("P"+strings.Repeat("o", 70), "web-1"),
		"name with slash":           filterQuery("Pod", "a/b"),
		"name dot-dot":              filterQuery("Pod", ".."),
		"name too long":             filterQuery("Pod", strings.Repeat("a", 254)),
		"name with control char":    filterQuery("Pod", "web\n1"),
	}
	for label, q := range cases {
		for _, cluster := range []string{"", remoteTestClusterID} {
			rr := listEvents(t, h, cluster, "default", q)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("%s (cluster %q): status = %d, want 400: %s", label, cluster, rr.Code, rr.Body.String())
			}
		}
	}
	if got := eventListSelectors(t, remote); len(got) != 0 {
		t.Errorf("an invalid filter still reached the remote API server: %v", got)
	}
}

func TestListEvents_EmptyFilterParamsIgnored(t *testing.T) {
	h, _ := testHandler(t, eventFixtures()...)
	names, _ := eventNames(t, listEvents(t, h, "", "default", filterQuery("", "")))
	assertNames(t, names, "web-1.a", "web-1.b", "web-2.a", "web-1.dep")
}

func TestListEvents_SingleFilterParam(t *testing.T) {
	h, _ := testHandler(t, eventFixtures()...)
	q := url.Values{"involvedObjectName": {"web-1"}}
	names, _ := eventNames(t, listEvents(t, h, "", "default", q))
	assertNames(t, names, "web-1.a", "web-1.b", "web-1.dep")
}

func TestListResource_FilterParamsIgnoredForOtherKinds(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"}}
	h, _ := testHandler(t, pod)
	req := requestWithUser("GET", "/api/v1/resources/pods/default?involvedObjectKind=Bad%20Kind", "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "pods")
	rctx.URLParams.Add("namespace", "default")
	rr := httptest.NewRecorder()
	h.HandleListResource(rr, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	if rr.Code != http.StatusOK {
		t.Fatalf("pods list with an events-only param: status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
}
