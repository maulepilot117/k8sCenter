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
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// Tests for the generic list/get handlers under a remote cluster selection.
// A remote cluster has no informers, so both handlers must read it directly
// as the user and never answer from the local cache. Every case seeds the
// local informers and the remote fake with different objects, so an answer
// from the wrong cluster is visible.

// crudDeployment returns a Deployment in ns carrying labels.
func crudDeployment(ns, name string, lbls map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: lbls}}
}

// resourceRequest builds a GET for kind against clusterID. segs fill the chi
// {namespace} and {name} params in order; a cluster-scoped get passes its
// name as the single segment, which chi maps to {namespace}.
func resourceRequest(clusterID, kind string, query url.Values, segs ...string) *http.Request {
	path := "/api/v1/resources/" + kind
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", kind)
	keys := []string{"namespace", "name"}
	for i, s := range segs {
		path += "/" + s
		rctx.URLParams.Add(keys[i], s)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	req := requestWithUser("GET", path, "")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if clusterID != "" {
		ctx = middleware.WithClusterID(ctx, clusterID)
	}
	return req.WithContext(ctx)
}

func listResource(h *Handler, clusterID, kind string, query url.Values, segs ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.HandleListResource(rr, resourceRequest(clusterID, kind, query, segs...))
	return rr
}

func getResource(h *Handler, clusterID, kind string, segs ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.HandleGetResource(rr, resourceRequest(clusterID, kind, nil, segs...))
	return rr
}

// objectMeta is the part of any served object these tests inspect.
type objectMeta struct {
	Metadata metav1.ObjectMeta `json:"metadata"`
}

// listedNames decodes a 200 list response into sorted object names.
func listedNames(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data []objectMeta `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v (%s)", err, rr.Body.String())
	}
	names := make([]string, 0, len(resp.Data))
	for _, o := range resp.Data {
		names = append(names, o.Metadata.Name)
	}
	sort.Strings(names)
	return names
}

// gotObject decodes a 200 get response's object metadata.
func gotObject(t *testing.T, rr *httptest.ResponseRecorder) metav1.ObjectMeta {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data objectMeta `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode get: %v (%s)", err, rr.Body.String())
	}
	return resp.Data.Metadata
}

// listActions returns the list actions for resource cs received.
func listActions(cs *fake.Clientset, resource string) []k8stesting.ListAction {
	var out []k8stesting.ListAction
	for _, a := range cs.Actions() {
		if la, ok := a.(k8stesting.ListAction); ok && a.GetResource().Resource == resource {
			out = append(out, la)
		}
	}
	return out
}

// countVerb counts the actions with verb on resource cs received.
func countVerb(cs *fake.Clientset, verb, resource string) int {
	n := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

// readActions returns the list and get actions cs received. The local fake
// also records informer watches, which can land after ClearActions and are
// not reads a handler made.
func readActions(cs *fake.Clientset) []k8stesting.Action {
	var out []k8stesting.Action
	for _, a := range cs.Actions() {
		if v := a.GetVerb(); v == "list" || v == "get" {
			out = append(out, a)
		}
	}
	return out
}

// resolveFailingHandler returns a Handler whose remote client never resolves,
// with an error carrying an internal address that must not reach the body.
func resolveFailingHandler(t *testing.T, localObjs ...runtime.Object) (*Handler, *fake.Clientset) {
	t.Helper()
	h, local := testHandler(t, localObjs...)
	h.remoteClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return nil, errors.New("dial tcp 10.0.0.9:6443: connect: connection refused")
	}
	local.ClearActions()
	return h, local
}

// --- list: reads the selected cluster ---

func TestListResource_RemoteNamespacedReadsRemoteNotLocal(t *testing.T) {
	h, local, _ := remoteEventsHandler(t,
		[]runtime.Object{crudDeployment("default", "local-web", nil)},
		crudDeployment("default", "remote-a", nil),
		crudDeployment("default", "remote-b", nil),
		crudDeployment("other", "remote-other", nil),
	)
	local.ClearActions()

	rr := listResource(h, remoteTestClusterID, "deployments", nil, "default")
	body := rr.Body.String()
	assertNames(t, listedNames(t, rr), "remote-a", "remote-b")
	if total, _, _ := listMetadata(t, rr); total != 2 {
		t.Errorf("total = %d, want 2 (the remote count)", total)
	}
	if strings.Contains(body, "local-web") {
		t.Errorf("remote list served a local object: %s", body)
	}
	if n := len(listActions(local, "deployments")); n != 0 {
		t.Errorf("local clientset listed deployments %d times for a remote request", n)
	}
}

func TestListResource_RemoteClusterScopedReadsRemoteNotLocal(t *testing.T) {
	h, local, remote := remoteEventsHandler(t,
		[]runtime.Object{readyNode("local-node")},
		readyNode("remote-node-a"), readyNode("remote-node-b"),
	)
	local.ClearActions()

	rr := listResource(h, remoteTestClusterID, "nodes", nil)
	if body := rr.Body.String(); strings.Contains(body, "local-node") {
		t.Errorf("remote list served a local node: %s", body)
	}
	assertNames(t, listedNames(t, rr), "remote-node-a", "remote-node-b")
	if total, _, _ := listMetadata(t, rr); total != 2 {
		t.Errorf("total = %d, want 2 (the remote count)", total)
	}
	if n := len(listActions(local, "nodes")); n != 0 {
		t.Errorf("local clientset listed nodes %d times for a remote request", n)
	}
	for _, la := range listActions(remote, "nodes") {
		if ns := la.GetNamespace(); ns != "" {
			t.Errorf("cluster-scoped remote list sent namespace %q", ns)
		}
	}
}

func TestListResource_RemoteForwardsLabelSelector(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil,
		crudDeployment("default", "web", map[string]string{"app": "web"}),
		crudDeployment("default", "db", map[string]string{"app": "db"}),
	)
	rr := listResource(h, remoteTestClusterID, "deployments", url.Values{"labelSelector": {"app=web"}}, "default")
	assertNames(t, listedNames(t, rr), "web")

	las := listActions(remote, "deployments")
	if len(las) == 0 {
		t.Fatal("remote clientset received no deployments list")
	}
	for _, la := range las {
		if got := la.GetListRestrictions().Labels.String(); got != "app=web" {
			t.Errorf("remote label selector = %q, want app=web", got)
		}
	}
}

func TestListResource_RemotePaginatesOverFullRead(t *testing.T) {
	h, _, _ := remoteEventsHandler(t, nil,
		crudDeployment("default", "a", nil),
		crudDeployment("default", "b", nil),
		crudDeployment("default", "c", nil),
	)
	first := listResource(h, remoteTestClusterID, "deployments", url.Values{"limit": {"2"}}, "default")
	page1 := listedNames(t, first)
	total1, cont, _ := listMetadata(t, first)
	if len(page1) != 2 || total1 != 3 || cont == "" {
		t.Fatalf("page 1 = %v total=%d continue=%q, want 2 items of 3 and a continue token", page1, total1, cont)
	}

	second := listResource(h, remoteTestClusterID, "deployments", url.Values{"limit": {"2"}, "continue": {cont}}, "default")
	page2 := listedNames(t, second)
	total2, cont2, _ := listMetadata(t, second)
	if total2 != 3 || cont2 != "" {
		t.Errorf("page 2 total=%d continue=%q, want 3 and no token", total2, cont2)
	}
	assertNames(t, append(page1, page2...), "a", "b", "c")
}

func TestListResource_RemoteTruncatedListIsFlagged(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil)
	pages := 0
	remote.PrependReactor("list", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pages++
		if l := a.(k8stesting.ListActionImpl).ListOptions.Limit; l != remoteListPageSize {
			t.Errorf("page %d requested Limit=%d, want %d", pages, l, remoteListPageSize)
		}
		return true, &appsv1.DeploymentList{
			ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", pages)},
			Items:    []appsv1.Deployment{*crudDeployment("default", fmt.Sprintf("d-%d", pages), nil)},
		}, nil
	})

	rr := listResource(h, remoteTestClusterID, "deployments", url.Values{"limit": {"100"}}, "default")
	total, _, truncated := listMetadata(t, rr)
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

// --- list: failures never fall back to the local cluster ---

func TestListResource_RemoteForbidden(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "",
		errors.New("RBAC: user admin cannot list deployments in 10.0.0.9"))
	cases := []struct {
		name      string
		kind      string
		resource  string
		segs      []string
		wantInMsg string
		page      func(n int) runtime.Object
	}{
		{
			name: "namespaced, first page", kind: "deployments", resource: "deployments", segs: []string{"default"},
			wantInMsg: "list Deployment in namespace default on the selected cluster",
		},
		{
			name: "cluster-scoped, first page", kind: "nodes", resource: "nodes",
			wantInMsg: "list Node on the selected cluster",
		},
		{
			name: "namespaced, second page", kind: "deployments", resource: "deployments", segs: []string{"default"},
			wantInMsg: "list Deployment in namespace default on the selected cluster",
			page: func(int) runtime.Object {
				return &appsv1.DeploymentList{
					ListMeta: metav1.ListMeta{Continue: "tok-1"},
					Items:    []appsv1.Deployment{*crudDeployment("default", "read-before-refusal", nil)},
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, remote := remoteEventsHandler(t, []runtime.Object{crudDeployment("default", "local-web", nil), readyNode("local-node")})
			n := 0
			remote.PrependReactor("list", tc.resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				n++
				if tc.page != nil && n == 1 {
					return true, tc.page(n), nil
				}
				return true, nil, forbidden
			})

			rr := listResource(h, remoteTestClusterID, tc.kind, nil, tc.segs...)
			body := rr.Body.String()
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", rr.Code, body)
			}
			msg, detail := errorMessage(t, rr)
			if !strings.Contains(msg, tc.wantInMsg) {
				t.Errorf("message = %q, want it to contain %q", msg, tc.wantInMsg)
			}
			for _, leak := range []string{"10.0.0.9", "RBAC:", "read-before-refusal", "local-web", "local-node"} {
				if strings.Contains(msg+detail+body, leak) {
					t.Errorf("403 body contains %q: %s", leak, body)
				}
			}
			if strings.Contains(body, `"data"`) {
				t.Errorf("a refused list served items: %s", body)
			}
		})
	}
}

func TestListResource_RemoteOtherErrorIs502WithoutLocalFallback(t *testing.T) {
	h, local, remote := remoteEventsHandler(t, []runtime.Object{crudDeployment("default", "local-web", nil)})
	remote.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("dial tcp 10.0.0.9:6443: i/o timeout")
	})
	local.ClearActions()

	rr := listResource(h, remoteTestClusterID, "deployments", nil, "default")
	body := rr.Body.String()
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rr.Code, body)
	}
	msg, _ := errorMessage(t, rr)
	if msg != "failed to list Deployment on the selected cluster" {
		t.Errorf("message = %q", msg)
	}
	for _, leak := range []string{"10.0.0.9", "local-web"} {
		if strings.Contains(body, leak) {
			t.Errorf("502 body contains %q: %s", leak, body)
		}
	}
	if reads := readActions(local); len(reads) != 0 {
		t.Errorf("a remote list failure read from the local clientset: %v", reads)
	}
}

// A non-status failure after a full first page discards that page: a
// partial list would be served as if it were complete.
func TestListResource_RemoteSecondPageFailureIs502(t *testing.T) {
	h, local, remote := remoteEventsHandler(t, []runtime.Object{crudDeployment("default", "local-web", nil)})
	n := 0
	remote.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		n++
		if n == 1 {
			return true, &appsv1.DeploymentList{
				ListMeta: metav1.ListMeta{Continue: "tok-1"},
				Items:    []appsv1.Deployment{*crudDeployment("default", "read-before-failure", nil)},
			}, nil
		}
		return true, nil, errors.New("dial tcp 10.0.0.9:6443: i/o timeout")
	})
	local.ClearActions()

	rr := listResource(h, remoteTestClusterID, "deployments", nil, "default")
	body := rr.Body.String()
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rr.Code, body)
	}
	if n != 2 {
		t.Errorf("remote list called %d times, want 2 (the failure is on page 2)", n)
	}
	msg, detail := errorMessage(t, rr)
	if msg != "failed to list Deployment on the selected cluster" || detail != "" {
		t.Errorf("message=%q detail=%q", msg, detail)
	}
	for _, leak := range []string{"10.0.0.9", "read-before-failure", "local-web", `"data"`} {
		if strings.Contains(body, leak) {
			t.Errorf("502 body contains %q: %s", leak, body)
		}
	}
	if reads := readActions(local); len(reads) != 0 {
		t.Errorf("a remote list failure read from the local clientset: %v", reads)
	}
}

func TestListResource_RemoteClientResolveFailureIs502WithoutLocalFallback(t *testing.T) {
	h, local := resolveFailingHandler(t, crudDeployment("default", "local-web", nil))

	for _, tc := range []struct {
		kind string
		segs []string
	}{{"deployments", []string{"default"}}, {"nodes", nil}} {
		rr := listResource(h, remoteTestClusterID, tc.kind, nil, tc.segs...)
		body := rr.Body.String()
		if rr.Code != http.StatusBadGateway {
			t.Fatalf("%s: status = %d, want 502: %s", tc.kind, rr.Code, body)
		}
		msg, detail := errorMessage(t, rr)
		if msg != "failed to reach the selected cluster" || detail != "" {
			t.Errorf("%s: message=%q detail=%q", tc.kind, msg, detail)
		}
		if strings.Contains(body, "10.0.0.9") || strings.Contains(body, "local-web") {
			t.Errorf("%s: 502 body leaks the internal error or a local object: %s", tc.kind, body)
		}
	}
	if reads := readActions(local); len(reads) != 0 {
		t.Errorf("a remote resolve failure fell back to the local clientset: %v", reads)
	}
}

// --- get: reads the selected cluster ---

func TestGetResource_RemoteReadsRemoteNotLocal(t *testing.T) {
	h, local, remote := remoteEventsHandler(t,
		[]runtime.Object{
			crudDeployment("default", "web", map[string]string{"from": "local"}),
			crudDeployment("default", "local-only", nil),
		},
		crudDeployment("default", "web", map[string]string{"from": "remote"}),
	)
	local.ClearActions()

	meta := gotObject(t, getResource(h, remoteTestClusterID, "deployments", "default", "web"))
	if meta.Labels["from"] != "remote" {
		t.Errorf("get served labels %v, want the remote object (from=remote)", meta.Labels)
	}
	if n := countVerb(remote, "get", "deployments"); n != 1 {
		t.Errorf("remote clientset received %d deployment gets, want 1", n)
	}

	// An object only the local cluster holds is not found on the remote.
	rr := getResource(h, remoteTestClusterID, "deployments", "default", "local-only")
	if rr.Code != http.StatusNotFound {
		t.Errorf("local-only object: status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
	if reads := readActions(local); len(reads) != 0 {
		t.Errorf("a remote get read from the local clientset: %v", reads)
	}
}

func TestGetResource_RemoteClusterScopedReadsRemote(t *testing.T) {
	remoteNode := readyNode("node-1")
	remoteNode.Labels = map[string]string{"from": "remote"}
	localNode := readyNode("node-1")
	localNode.Labels = map[string]string{"from": "local"}
	h, _, _ := remoteEventsHandler(t, []runtime.Object{localNode}, remoteNode)

	meta := gotObject(t, getResource(h, remoteTestClusterID, "nodes", "node-1"))
	if meta.Labels["from"] != "remote" {
		t.Errorf("get served labels %v, want the remote node", meta.Labels)
	}
}

// TestGetResource_RemoteErrors pins the remote get error contract: not found
// and forbidden map to fixed messages, everything else (a remote 401
// included) is a 502, and the remote API server's own text never reaches the
// body. Every fake error carries an internal marker to prove it.
func TestGetResource_RemoteErrors(t *testing.T) {
	deployGR := schema.GroupResource{Group: "apps", Resource: "deployments"}
	nodeGR := schema.GroupResource{Group: "", Resource: "nodes"}
	notFound := func(msg string) error {
		return &apierrors.StatusError{ErrStatus: metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusNotFound,
			Reason: metav1.StatusReasonNotFound, Message: msg,
		}}
	}
	const (
		deployNotFound  = "Deployment 'web' not found in namespace default on the selected cluster"
		deployForbidden = "you do not have permission to get Deployment 'web' in namespace default on the selected cluster"
		deployFailed    = "failed to get Deployment on the selected cluster"
	)
	cases := []struct {
		name       string
		kind       string
		resource   string
		segs       []string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{
			name: "not found", kind: "deployments", resource: "deployments", segs: []string{"default", "web"},
			err:        notFound(`deployments.apps "web" not found (internal 10.0.0.9)`),
			wantStatus: http.StatusNotFound, wantMsg: deployNotFound,
		},
		{
			name: "forbidden", kind: "deployments", resource: "deployments", segs: []string{"default", "web"},
			err:        apierrors.NewForbidden(deployGR, "web", errors.New("RBAC: denied by 10.0.0.9")),
			wantStatus: http.StatusForbidden, wantMsg: deployForbidden,
		},
		{
			// A remote cluster's own 401 must not read as a k8sCenter 401: the
			// web client treats any 401 as its session expiring.
			name: "unauthorized", kind: "deployments", resource: "deployments", segs: []string{"default", "web"},
			err:        apierrors.NewUnauthorized("token expired for 10.0.0.9"),
			wantStatus: http.StatusBadGateway, wantMsg: deployFailed,
		},
		{
			name: "too many requests", kind: "deployments", resource: "deployments", segs: []string{"default", "web"},
			err:        apierrors.NewTooManyRequests("throttled by 10.0.0.9", 1),
			wantStatus: http.StatusBadGateway, wantMsg: deployFailed,
		},
		{
			name: "transport failure", kind: "deployments", resource: "deployments", segs: []string{"default", "web"},
			err:        errors.New("dial tcp 10.0.0.9:6443: i/o timeout"),
			wantStatus: http.StatusBadGateway, wantMsg: deployFailed,
		},
		{
			name: "cluster-scoped not found", kind: "nodes", resource: "nodes", segs: []string{"node-x"},
			err:        notFound(`nodes "node-x" not found (internal 10.0.0.9)`),
			wantStatus: http.StatusNotFound, wantMsg: "Node 'node-x' not found on the selected cluster",
		},
		{
			name: "cluster-scoped forbidden", kind: "nodes", resource: "nodes", segs: []string{"node-x"},
			err:        apierrors.NewForbidden(nodeGR, "node-x", errors.New("RBAC: denied by 10.0.0.9")),
			wantStatus: http.StatusForbidden, wantMsg: "you do not have permission to get Node 'node-x' on the selected cluster",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, local, remote := remoteEventsHandler(t, []runtime.Object{
				crudDeployment("default", "web", map[string]string{"from": "local"}),
				readyNode("node-x"),
			})
			remote.PrependReactor("get", tc.resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, tc.err
			})
			local.ClearActions()

			rr := getResource(h, remoteTestClusterID, tc.kind, tc.segs...)
			body := rr.Body.String()
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.wantStatus, body)
			}
			msg, detail := errorMessage(t, rr)
			if msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", msg, tc.wantMsg)
			}
			if detail != "" {
				t.Errorf("detail = %q, want empty", detail)
			}
			for _, leak := range []string{"10.0.0.9", "RBAC", "throttled", `"from"`} {
				if strings.Contains(body, leak) {
					t.Errorf("error body contains %q: %s", leak, body)
				}
			}
			if reads := readActions(local); len(reads) != 0 {
				t.Errorf("a remote get failure read from the local clientset: %v", reads)
			}
		})
	}
}

func TestGetResource_RemoteClientResolveFailureIs502(t *testing.T) {
	h, local := resolveFailingHandler(t, crudDeployment("default", "web", nil))

	rr := getResource(h, remoteTestClusterID, "deployments", "default", "web")
	body := rr.Body.String()
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rr.Code, body)
	}
	msg, detail := errorMessage(t, rr)
	if msg != "failed to reach the selected cluster" || detail != "" {
		t.Errorf("message=%q detail=%q", msg, detail)
	}
	if strings.Contains(body, "10.0.0.9") || strings.Contains(body, "metadata") {
		t.Errorf("502 body leaks the internal error or an object: %s", body)
	}
	if reads := readActions(local); len(reads) != 0 {
		t.Errorf("a remote resolve failure fell back to the local clientset: %v", reads)
	}
}

// --- local selection still reads the informer cache ---

func TestListAndGetResource_LocalReadsCacheNotRemote(t *testing.T) {
	h, _, _ := remoteEventsHandler(t,
		[]runtime.Object{crudDeployment("default", "local-web", nil)},
		crudDeployment("default", "remote-web", nil),
	)
	h.remoteClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		t.Error("a local request resolved a remote client")
		return nil, errors.New("unexpected")
	}
	for _, cluster := range []string{"", k8s.LocalClusterID} {
		assertNames(t, listedNames(t, listResource(h, cluster, "deployments", nil, "default")), "local-web")
		if meta := gotObject(t, getResource(h, cluster, "deployments", "default", "local-web")); meta.Name != "local-web" {
			t.Errorf("cluster %q: get served %q, want local-web", cluster, meta.Name)
		}
	}
}

// --- Secrets never come through the generic route, on any cluster ---

func TestResource_RemoteSecretsAnswerLikeLocal(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("supersecret")},
	}
	h, _, remote := remoteEventsHandler(t, nil, secret)

	localList := listResource(h, "", "secrets", nil, "default")
	remoteList := listResource(h, remoteTestClusterID, "secrets", nil, "default")
	localGet := getResource(h, "", "secrets", "default", "db-creds")
	remoteGet := getResource(h, remoteTestClusterID, "secrets", "default", "db-creds")

	for label, pair := range map[string][2]*httptest.ResponseRecorder{
		"list": {localList, remoteList},
		"get":  {localGet, remoteGet},
	} {
		local, rem := pair[0], pair[1]
		if local.Code != http.StatusInternalServerError || rem.Code != local.Code {
			t.Errorf("%s: local status %d, remote status %d, want both 500", label, local.Code, rem.Code)
		}
		if rem.Body.String() != local.Body.String() {
			t.Errorf("%s: remote body %s differs from local body %s", label, rem.Body.String(), local.Body.String())
		}
		if strings.Contains(rem.Body.String(), "db-creds\"") || strings.Contains(rem.Body.String(), "c3VwZXJzZWNyZXQ") {
			t.Errorf("%s: remote body carries secret data: %s", label, rem.Body.String())
		}
	}
	if n := countVerb(remote, "list", "secrets") + countVerb(remote, "get", "secrets"); n != 0 {
		t.Errorf("the generic route read secrets from the remote cluster %d times", n)
	}
}
