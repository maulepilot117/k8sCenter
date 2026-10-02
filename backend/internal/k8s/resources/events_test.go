package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

// --- remote error branches, paging and truncation ---

// listMetadata decodes a 200 list response's metadata.
func listMetadata(t *testing.T, rr *httptest.ResponseRecorder) (total int, cont string, truncated bool) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Metadata == nil {
		t.Fatal("list response has no metadata")
	}
	return resp.Metadata.Total, resp.Metadata.Continue, resp.Metadata.Truncated
}

// errorMessage decodes an error response and returns its message and detail.
func errorMessage(t *testing.T, rr *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	resp := decodeResponse(t, rr)
	if resp.Error == nil {
		t.Fatalf("expected an error body, got %s", rr.Body.String())
	}
	return resp.Error.Message, resp.Error.Detail
}

func TestListEvents_RemoteClientResolveFailureIs502WithoutLocalFallback(t *testing.T) {
	h, local := testHandler(t, eventFixtures()...)
	h.remoteClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return nil, errors.New("dial tcp 10.0.0.9:6443: connect: connection refused")
	}
	local.ClearActions()

	rr := listEvents(t, h, remoteTestClusterID, "default", filterQuery("Pod", "web-1"))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "10.0.0.9") {
		t.Errorf("502 body exposes the internal error: %s", rr.Body.String())
	}
	if got := eventListSelectors(t, local); len(got) != 0 {
		t.Errorf("a remote resolve failure fell back to the local cluster: %v", got)
	}
}

func TestListEvents_RemoteListErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantInMsg  string
	}{
		{
			name:       "forbidden",
			err:        apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("RBAC: denied")),
			wantStatus: http.StatusForbidden,
			wantInMsg:  "namespace default",
		},
		{
			name:       "other error",
			err:        errors.New("etcdserver: request timed out"),
			wantStatus: http.StatusBadGateway,
			wantInMsg:  "failed to list events",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, remote := remoteEventsHandler(t, eventFixtures())
			remote.PrependReactor("list", "events", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, tc.err
			})
			rr := listEvents(t, h, remoteTestClusterID, "default", filterQuery("Pod", "web-1"))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			msg, detail := errorMessage(t, rr)
			if !strings.Contains(msg, tc.wantInMsg) {
				t.Errorf("message = %q, want it to contain %q", msg, tc.wantInMsg)
			}
			if strings.Contains(msg+detail, tc.err.Error()) {
				t.Errorf("error body exposes the upstream error %q: %s", tc.err, rr.Body.String())
			}
		})
	}
}

func TestListEvents_RemotePagesUntilContinueIsEmpty(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil)
	var conts []string
	remote.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
		opts := a.(k8stesting.ListActionImpl).ListOptions
		conts = append(conts, opts.Continue)
		page := len(conts)
		next := ""
		if page < 3 {
			next = fmt.Sprintf("tok-%d", page)
		}
		return true, &corev1.EventList{
			ListMeta: metav1.ListMeta{Continue: next},
			Items:    []corev1.Event{*testEvent("default", fmt.Sprintf("e-%d", page), "Pod", "x")},
		}, nil
	})

	rr := listEvents(t, h, remoteTestClusterID, "default", url.Values{"limit": {"10"}})
	body := rr.Body.String()
	total, cont, truncated := listMetadata(t, rr)
	if want := []string{"", "tok-1", "tok-2"}; strings.Join(conts, "|") != strings.Join(want, "|") {
		t.Errorf("continue tokens sent = %q, want %q", conts, want)
	}
	if total != 3 || cont != "" || truncated {
		t.Errorf("total=%d continue=%q truncated=%v, want 3, empty, false", total, cont, truncated)
	}
	if strings.Contains(body, `"truncated"`) {
		t.Errorf("a complete list carries a truncated key: %s", body)
	}
}

func TestListEvents_RemoteTruncatedListIsFlagged(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil)
	pages := 0
	remote.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pages++
		opts := a.(k8stesting.ListActionImpl).ListOptions
		if opts.Limit != remoteListPageSize {
			t.Errorf("page %d requested Limit=%d, want %d", pages, opts.Limit, remoteListPageSize)
		}
		// Every page claims there is more; the pager must stop at its cap.
		return true, &corev1.EventList{
			ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", pages)},
			Items:    []corev1.Event{*testEvent("default", fmt.Sprintf("e-%d", pages), "Pod", "x")},
		}, nil
	})

	total, _, truncated := listMetadata(t, listEvents(t, h, remoteTestClusterID, "default", url.Values{"limit": {"100"}}))
	if pages != remoteListMaxPages {
		t.Errorf("pager fetched %d pages, want cap %d", pages, remoteListMaxPages)
	}
	if total != remoteListMaxPages {
		t.Errorf("total = %d, want the %d items actually read", total, remoteListMaxPages)
	}
	if !truncated {
		t.Error("a list stopped at the page cap is not flagged truncated")
	}
}

func TestListEvents_RemoteForwardsLabelSelector(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil)
	if rr := listEvents(t, h, remoteTestClusterID, "default", url.Values{"labelSelector": {"app=web"}}); rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got []string
	for _, a := range remote.Actions() {
		if la, ok := a.(k8stesting.ListAction); ok && a.GetResource().Resource == "events" {
			got = append(got, la.GetListRestrictions().Labels.String())
		}
	}
	if len(got) == 0 || got[0] != "app=web" {
		t.Errorf("remote label selectors = %v, want [app=web]", got)
	}
}

func TestListEvents_AccessDeniedBeforeAnyList(t *testing.T) {
	for _, cluster := range []string{"", remoteTestClusterID} {
		h, local, remote := remoteEventsHandler(t, eventFixtures())
		h.AccessChecker = NewAlwaysDenyAccessChecker()
		local.ClearActions()

		rr := listEvents(t, h, cluster, "default", filterQuery("Pod", "web-1"))
		if rr.Code != http.StatusForbidden {
			t.Errorf("cluster %q: status = %d, want 403: %s", cluster, rr.Code, rr.Body.String())
		}
		if got := eventListSelectors(t, local); len(got) != 0 {
			t.Errorf("cluster %q: local events listed despite a denied access check: %v", cluster, got)
		}
		if got := eventListSelectors(t, remote); len(got) != 0 {
			t.Errorf("cluster %q: remote events listed despite a denied access check: %v", cluster, got)
		}
	}
}
