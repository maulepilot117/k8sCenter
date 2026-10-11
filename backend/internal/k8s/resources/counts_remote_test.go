package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	k8stesting "k8s.io/client-go/testing"
)

// Tests for GET /resources/counts. The local cluster is counted from the
// informer cache; a remote cluster is counted from its own API server, as the
// user, and never from the local cache. Local and remote fixtures differ so an
// answer from the wrong cluster is visible.

func countsRequest(clusterID, namespace string) *http.Request {
	path := "/api/v1/resources/counts"
	if namespace != "" {
		path += "?namespace=" + namespace
	}
	req := requestWithUser("GET", path, "")
	if clusterID != "" {
		req = req.WithContext(middleware.WithClusterID(req.Context(), clusterID))
	}
	return req
}

// decodeCounts returns the counts map and whether the body carried a
// metadata object, and its truncated flag.
func decodeCounts(t *testing.T, rr *httptest.ResponseRecorder) (counts map[string]int, hasMetadata, truncated bool) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body: %v (body: %s)", err, rr.Body.String())
	}
	if err := json.Unmarshal(raw["data"], &counts); err != nil {
		t.Fatalf("decode data: %v (body: %s)", err, rr.Body.String())
	}
	md, hasMetadata := raw["metadata"]
	if hasMetadata {
		var m struct {
			Truncated bool `json:"truncated"`
		}
		if err := json.Unmarshal(md, &m); err != nil {
			t.Fatalf("decode metadata: %v", err)
		}
		truncated = m.Truncated
	}
	return counts, hasMetadata, truncated
}

func countsNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func countsNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func countsDeployment(ns, name string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func assertCount(t *testing.T, counts map[string]int, kind string, want int) {
	t.Helper()
	got, ok := counts[kind]
	if !ok {
		t.Errorf("counts[%q] missing, want %d (counts: %v)", kind, want, counts)
		return
	}
	if got != want {
		t.Errorf("counts[%q] = %d, want %d", kind, got, want)
	}
}

// --- local (informer) path ---

func TestResourceCounts_LocalReadsInformerCache(t *testing.T) {
	for _, clusterID := range []string{"", k8s.LocalClusterID} {
		t.Run(fmt.Sprintf("cluster=%q", clusterID), func(t *testing.T) {
			h, _ := testHandler(t,
				countsDeployment("a", "d1"), countsDeployment("b", "d2"), countsNode("n1"))
			h.remoteClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
				t.Errorf("remote client resolved for a local counts request")
				return nil, errors.New("unexpected")
			}

			rr := httptest.NewRecorder()
			h.HandleResourceCounts(rr, countsRequest(clusterID, ""))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
			}
			counts, hasMetadata, _ := decodeCounts(t, rr)
			assertCount(t, counts, "deployments", 2)
			assertCount(t, counts, "nodes", 1)
			if _, ok := counts["secrets"]; ok {
				t.Errorf("secrets counted; they are excluded")
			}
			if hasMetadata {
				t.Errorf("local body carries a metadata object: %s", rr.Body.String())
			}
		})
	}
}

func TestResourceCounts_LocalDeniedKindOmitted(t *testing.T) {
	h, _ := testHandler(t, countsDeployment("a", "d1"), countsNode("n1"))
	h.AccessChecker = NewDenyResourcesAccessChecker("deployments")

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest("", ""))
	counts, _, _ := decodeCounts(t, rr)
	if _, ok := counts["deployments"]; ok {
		t.Errorf("denied kind deployments was counted")
	}
	assertCount(t, counts, "nodes", 1)
}

// --- remote path ---

func TestResourceCounts_RemoteCountsRemoteObjectsOnly(t *testing.T) {
	h, local, _ := remoteEventsHandler(t,
		[]runtime.Object{countsDeployment("a", "local-1"), countsDeployment("a", "local-2"), countsNode("local-n")},
		countsDeployment("a", "r1"), countsDeployment("a", "r2"), countsDeployment("b", "r3"),
		countsNode("r-n1"), countsNode("r-n2"), countsNamespace("a"),
	)
	local.ClearActions()

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	counts, hasMetadata, _ := decodeCounts(t, rr)
	assertCount(t, counts, "deployments", 3)
	assertCount(t, counts, "nodes", 2)
	assertCount(t, counts, "namespaces", 1)
	assertCount(t, counts, "pods", 0)
	if _, ok := counts["secrets"]; ok {
		t.Errorf("secrets counted on a remote cluster; they are excluded")
	}
	if hasMetadata {
		t.Errorf("complete remote counts carry a metadata object: %s", rr.Body.String())
	}
	if got := readActions(local); len(got) != 0 {
		t.Errorf("local cluster read during a remote counts request: %v", got)
	}
}

func TestResourceCounts_RemoteNamespaceScopesNamespacedKinds(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil,
		countsDeployment("a", "r1"), countsDeployment("b", "r2"), countsNode("r-n1"),
	)

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, "a"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	counts, _, _ := decodeCounts(t, rr)
	assertCount(t, counts, "deployments", 1)
	assertCount(t, counts, "nodes", 1)

	for _, a := range remote.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == "deployments" && a.GetNamespace() != "a" {
			t.Errorf("deployments listed in namespace %q, want a", a.GetNamespace())
		}
	}
}

func TestResourceCounts_RemoteForbiddenKindOmitted(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil, countsDeployment("a", "r1"), countsNode("r-n1"))
	remote.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("denied"))
	})

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	counts, _, _ := decodeCounts(t, rr)
	if _, ok := counts["deployments"]; ok {
		t.Errorf("forbidden kind deployments was counted: %v", counts)
	}
	assertCount(t, counts, "nodes", 1)
}

// A list that fails for any reason other than the cluster's refusal fails
// the whole request: a kind silently missing from the map would render as
// zero, and a partial answer would be served as if it were complete.
func TestResourceCounts_RemoteListErrorIs502WithoutLeak(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil, countsDeployment("a", "r1"), countsNode("r-n1"))
	remote.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("dial tcp 10.0.0.9:6443: i/o timeout")
	})

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Error == nil || resp.Error.Message != remoteCountsFailedMsg || resp.Error.Detail != "" {
		t.Errorf("error = %+v, want the fixed message with no detail", resp.Error)
	}
	if body := rr.Body.String(); containsAny(body, "10.0.0.9", "i/o timeout") {
		t.Errorf("raw list error reached the body: %s", body)
	}
}

// A permission check that could not run is not a denial: the kind's count is
// unknown, so the request fails rather than omitting it.
func TestResourceCounts_RemoteSARErrorIs502(t *testing.T) {
	h, _, _ := remoteEventsHandler(t, nil, countsDeployment("a", "r1"), countsNode("r-n1"))
	h.AccessChecker = NewErroringAccessChecker(errors.New("sar: 401 Unauthorized from 10.0.0.9"))

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); containsAny(body, "10.0.0.9", "Unauthorized") {
		t.Errorf("raw check error reached the body: %s", body)
	}
}

// When the shared budget runs out the request answers 504 with a fixed
// message, promptly, and the context error stays in the log.
//
// The budget itself ends the stalled list. The fake clientset hands its
// reactors no context, so the test captures the context the handler resolves
// the client with (the budgeted one) and the deployments list waits on it,
// returning what client-go returns for an expired context. If the handler
// applied no budget, that context would never end: the list would give up on
// the test's own fallback timer with a plain error, and the request would
// answer a late 502, failing both the status and the elapsed-time check.
func TestResourceCounts_RemoteBudgetExpiryIs504(t *testing.T) {
	prev := remoteCountsBudget
	remoteCountsBudget = 200 * time.Millisecond
	t.Cleanup(func() { remoteCountsBudget = prev })
	fallback := remoteCountsBudget + 3*time.Second

	h, _, remote := remoteEventsHandler(t, nil, countsDeployment("a", "r1"), countsNode("r-n1"))
	var (
		ctxMu      sync.Mutex
		handlerCtx context.Context
	)
	h.remoteClient = func(ctx context.Context, _ string, _ *auth.User) (kubernetes.Interface, error) {
		ctxMu.Lock()
		defer ctxMu.Unlock()
		handlerCtx = ctx
		return remote, nil
	}
	remote.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		ctxMu.Lock()
		ctx := handlerCtx
		ctxMu.Unlock()
		select {
		case <-ctx.Done():
			return true, nil, fmt.Errorf("Get \"https://10.0.0.9:6443/apis/apps/v1/deployments\": %w", ctx.Err())
		case <-time.After(fallback):
			return true, nil, errors.New("stalled list outlived the test fallback: no budget ended it")
		}
	})

	start := time.Now()
	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	if elapsed := time.Since(start); elapsed >= fallback {
		t.Errorf("handler took %v, want it ended by the %v budget", elapsed, remoteCountsBudget)
	}
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body: %s)", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Error == nil || resp.Error.Message != remoteCountsTimeoutMsg || resp.Error.Detail != "" {
		t.Errorf("error = %+v, want the fixed timeout message with no detail", resp.Error)
	}
	if body := rr.Body.String(); containsAny(body, "deadline", "10.0.0.9") {
		t.Errorf("context error reached the body: %s", body)
	}
}

// A kind whose count panics fails the request: recoverutil keeps the process
// alive, but the kind's count is unknown, so it must not be silently omitted
// from a 200. The panic text stays in the log.
func TestResourceCounts_RemotePanickingKindIs502(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil, countsDeployment("a", "r1"), countsNode("r-n1"))
	remote.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		panic("boom: adapter exploded at 10.0.0.9")
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
		done <- rr
	}()
	var rr *httptest.ResponseRecorder
	select {
	case rr = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not complete after a kind panicked")
	}
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Error == nil || resp.Error.Message != remoteCountsFailedMsg || resp.Error.Detail != "" {
		t.Errorf("error = %+v, want the fixed message with no detail", resp.Error)
	}
	if body := rr.Body.String(); containsAny(body, "boom", "panic", "10.0.0.9") {
		t.Errorf("panic text reached the body: %s", body)
	}
}

// Every permission check and the client itself are the user's, on the
// selected cluster: a check against the local cluster, or a client resolved
// without the user, would count with the wrong permissions.
func TestResourceCounts_RemoteChecksAndReadsAsTheUserOnTheSelectedCluster(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil, countsDeployment("a", "r1"))
	var (
		mu    sync.Mutex
		sars  []recordedCheck
		users []*auth.User
	)
	h.AccessChecker = NewRecordingAccessChecker(func(clusterID, username string, groups []string, verb, resource, namespace string) {
		mu.Lock()
		defer mu.Unlock()
		sars = append(sars, recordedCheck{clusterID, username, verb, resource})
	})
	h.remoteClient = func(_ context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error) {
		mu.Lock()
		defer mu.Unlock()
		users = append(users, user)
		return remote, nil
	}

	req := countsRequest(remoteTestClusterID, "")
	want, _ := auth.UserFromContext(req.Context())
	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	if len(sars) != len(countedKinds("")) {
		t.Errorf("%d permission checks, want one per counted kind (%d)", len(sars), len(countedKinds("")))
	}
	for _, c := range sars {
		if c.clusterID != remoteTestClusterID || c.username != want.KubernetesUsername || c.verb != "list" {
			t.Errorf("check %+v, want verb list on %q as %q", c, remoteTestClusterID, want.KubernetesUsername)
		}
	}
	if len(users) != 1 || users[0] != want {
		t.Errorf("remote client resolved for %v, want once for the request user", users)
	}
}

type recordedCheck struct {
	clusterID, username, verb, resource string
}

func TestResourceCounts_RemoteDeniedSAROmitsKind(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil, countsDeployment("a", "r1"), countsNode("r-n1"))
	h.AccessChecker = NewDenyResourcesAccessChecker("nodes")

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	counts, _, _ := decodeCounts(t, rr)
	if _, ok := counts["nodes"]; ok {
		t.Errorf("denied kind nodes was counted: %v", counts)
	}
	assertCount(t, counts, "deployments", 1)
	// The check runs inside the kind's worker, before its list: a denied kind
	// is never listed at all.
	for _, a := range readActions(remote) {
		if a.GetResource().Resource == "nodes" {
			t.Errorf("denied kind nodes was listed on the remote cluster: %v", a)
		}
	}
}

func TestRemoteCountOrder_HeavyKindsFirstRestStable(t *testing.T) {
	checks := countedKinds("ns")
	got := remoteCountOrder(checks)
	if len(got) != len(checks) {
		t.Fatalf("len = %d, want %d", len(got), len(checks))
	}
	for i, kind := range remoteCountsHeavyFirst {
		if got[i].kind != kind {
			t.Errorf("position %d = %q, want %q", i, got[i].kind, kind)
		}
	}
	var rest []string
	for _, c := range checks {
		if !slices.Contains(remoteCountsHeavyFirst, c.kind) {
			rest = append(rest, c.kind)
		}
	}
	for i, kind := range rest {
		if got[len(remoteCountsHeavyFirst)+i].kind != kind {
			t.Errorf("tail position %d = %q, want %q (original order)", i, got[len(remoteCountsHeavyFirst)+i].kind, kind)
		}
	}
	if checks[0].kind != "nodes" {
		t.Error("remoteCountOrder modified its input")
	}
}

func TestResourceCounts_RemoteResolveFailure(t *testing.T) {
	h, local := resolveFailingHandler(t, countsDeployment("a", "local-1"), countsNode("local-n"))

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if resp.Error == nil || resp.Error.Message != "failed to reach the selected cluster" || resp.Error.Detail != "" {
		t.Errorf("error = %+v, want the fixed message with no detail", resp.Error)
	}
	if body := rr.Body.String(); containsAny(body, "10.0.0.9", "connection refused") {
		t.Errorf("raw resolve error reached the body: %s", body)
	}
	if got := readActions(local); len(got) != 0 {
		t.Errorf("local cluster read after a failed remote resolve: %v", got)
	}
}

func TestResourceCounts_RemoteTruncatedAtCap(t *testing.T) {
	h, _, remote := remoteEventsHandler(t, nil, countsNode("r-n1"))
	page := make([]corev1.Pod, k8s.RemoteListPageSize)
	for i := range page {
		page[i] = corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("p%d", i), Namespace: "a"}}
	}
	remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "more"}, Items: page}, nil
	})

	rr := httptest.NewRecorder()
	h.HandleResourceCounts(rr, countsRequest(remoteTestClusterID, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	counts, hasMetadata, truncated := decodeCounts(t, rr)
	assertCount(t, counts, "pods", k8s.RemoteListPageSize*k8s.RemoteListMaxPages)
	assertCount(t, counts, "nodes", 1)
	if !hasMetadata || !truncated {
		t.Errorf("metadata.truncated missing on a capped count: %s", rr.Body.String()[:min(200, rr.Body.Len())])
	}
	// metadata.total is the sum of the counts read, not the zero value.
	var md struct {
		Metadata struct {
			Total int `json:"total"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &md); err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, n := range counts {
		sum += n
	}
	if md.Metadata.Total != sum {
		t.Errorf("metadata.total = %d, want the sum of the counts %d", md.Metadata.Total, sum)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Every counts key must resolve to a registered adapter, or the remote path
// would silently drop that kind.
func TestResourceCounts_EveryKindHasAnAdapter(t *testing.T) {
	for _, c := range countedKinds("") {
		if GetAdapter(adapterKindForCount(c.kind)) == nil {
			t.Errorf("counts kind %q maps to adapter %q, which is not registered", c.kind, adapterKindForCount(c.kind))
		}
	}
}
