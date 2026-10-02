package policy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const remoteCluster = "remote-1"

// requiredLabelsGVR is the Gatekeeper constraint kind the fixtures serve.
var requiredLabelsGVR = schema.GroupVersionResource{Group: gatekeeperConstraintsGroup, Version: "v1beta1", Resource: "k8srequiredlabels"}

// requiredLabelsV1GVR is the same constraint kind at v1, which current
// Gatekeeper releases serve alongside v1beta1.
var requiredLabelsV1GVR = schema.GroupVersionResource{Group: gatekeeperConstraintsGroup, Version: "v1", Resource: "k8srequiredlabels"}

// listKinds maps every GVR the fixtures serve to its list kind.
var listKinds = map[schema.GroupVersionResource]string{
	KyvernoClusterPolicyGVR: "ClusterPolicyList",
	KyvernoPolicyGVR:        "PolicyList",
	PolicyReportGVR:         "PolicyReportList",
	ClusterPolicyReportGVR:  "ClusterPolicyReportList",
	requiredLabelsGVR:       "K8sRequiredLabelsList",
	requiredLabelsV1GVR:     "K8sRequiredLabelsList",
}

// fakeCluster is one fake cluster: its discovery, dynamic and typed clients.
type fakeCluster struct {
	disc  *fakediscovery.FakeDiscovery
	dyn   *dynfake.FakeDynamicClient
	typed *kfake.Clientset
	// discOverride, when set, is the discovery handed out instead of disc.
	discOverride discovery.DiscoveryInterface
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. A target
// error makes every resolution fail.
type fakeClients struct {
	clusters  map[string]*fakeCluster
	targetErr error
	resolved  atomic.Int32
}

func (f *fakeClients) cluster(id string) (*fakeCluster, error) {
	f.resolved.Add(1)
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.typed, nil
}

func (f *fakeClients) DynamicClientForCluster(_ context.Context, id, _ string, _ []string) (dynamic.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.dyn, nil
}

func (f *fakeClients) TargetSchemaFor(_ context.Context, id, _ string, _ []string) (*k8s.TargetSchema, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	var disc discovery.DiscoveryInterface = c.disc
	if c.discOverride != nil {
		disc = c.discOverride
	}
	return &k8s.TargetSchema{ClusterID: id, Discovery: disc, Invalidate: func() {}}, nil
}

// engineLists is the discovery of a cluster serving the named engines.
func engineLists(kyverno, gatekeeper bool) []*metav1.APIResourceList {
	var out []*metav1.APIResourceList
	if kyverno {
		out = append(out,
			&metav1.APIResourceList{GroupVersion: "kyverno.io/v1", APIResources: []metav1.APIResource{
				{Name: "clusterpolicies", Kind: "ClusterPolicy"},
				{Name: "clusterpolicies/status", Kind: "ClusterPolicy"},
				{Name: "policies", Kind: "Policy", Namespaced: true},
			}},
			&metav1.APIResourceList{GroupVersion: "wgpolicyk8s.io/v1alpha2", APIResources: []metav1.APIResource{
				{Name: "policyreports", Kind: "PolicyReport", Namespaced: true},
				{Name: "clusterpolicyreports", Kind: "ClusterPolicyReport"},
			}},
		)
	}
	if gatekeeper {
		out = append(out,
			&metav1.APIResourceList{GroupVersion: "templates.gatekeeper.sh/v1", APIResources: []metav1.APIResource{
				{Name: "constrainttemplates", Kind: "ConstraintTemplate"},
			}},
			&metav1.APIResourceList{GroupVersion: "constraints.gatekeeper.sh/v1beta1", APIResources: []metav1.APIResource{
				{Name: "k8srequiredlabels", Kind: "K8sRequiredLabels"},
				{Name: "k8srequiredlabels/status", Kind: "K8sRequiredLabels"},
			}},
		)
	}
	return out
}

func newFakeCluster(lists []*metav1.APIResourceList, objs []runtime.Object, typed ...runtime.Object) *fakeCluster {
	scheme := runtime.NewScheme()
	return &fakeCluster{
		disc:  &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: lists}},
		dyn:   dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...),
		typed: kfake.NewClientset(typed...),
	}
}

func kyvernoPolicy(gvr schema.GroupVersionResource, kind, ns, name string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.GroupVersion().String(),
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"validationFailureAction": "Enforce",
			"rules":                   []any{map[string]any{"name": "r1"}},
		},
	}}
	if ns != "" {
		o.SetNamespace(ns)
	}
	return o
}

func policyReport(ns, name, policy string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": PolicyReportGVR.GroupVersion().String(),
		"kind":       "PolicyReport",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"scope":      map[string]any{"kind": "Pod", "name": "web", "namespace": ns},
		"results": []any{
			map[string]any{"policy": policy, "rule": "r1", "result": "fail", "message": "missing limits"},
		},
	}}
}

func requiredLabels(name string, violationNS string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": requiredLabelsGVR.GroupVersion().String(),
		"kind":       "K8sRequiredLabels",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"enforcementAction": "deny"},
		"status": map[string]any{"violations": []any{
			map[string]any{"kind": "Namespace", "name": violationNS, "namespace": violationNS, "message": "needs owner"},
		}},
	}}
}

func webhookConfig(name, ns string) *admissionregistrationv1.ValidatingWebhookConfiguration {
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name:         name + ".example",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{Service: &admissionregistrationv1.ServiceReference{Namespace: ns, Name: "svc"}},
		}},
	}
}

// remoteBothEngines is a remote cluster running Kyverno and Gatekeeper with
// one policy, one report and one constraint, all named "remote-*".
func remoteBothEngines() *fakeCluster {
	c := newFakeCluster(engineLists(true, true), []runtime.Object{
		kyvernoPolicy(KyvernoClusterPolicyGVR, "ClusterPolicy", "", "remote-require-limits"),
		policyReport("shop", "remote-report", "remote-require-limits"),
	}, webhookConfig("kyverno-resource-validating-webhook-cfg", "kyverno-remote"))
	// Added under its explicit resource: the tracker would otherwise guess
	// "k8srequiredlabelses" from the kind.
	if err := c.dyn.Tracker().Create(requiredLabelsGVR, requiredLabels("remote-owner-label", "shop"), ""); err != nil {
		panic(err)
	}
	return c
}

// remoteFixture is a Handler whose remote cluster serves remote. The local
// cluster is reachable through the same ClusterClients and its local cache
// holds a "local-*" policy and violation, and the local Discoverer reports
// Kyverno only, so any local read on the remote path shows up either in a
// recorded action or in the answer.
type remoteFixture struct {
	h       *Handler
	clients *fakeClients
	local   *fakeCluster
}

func newRemoteFixture(t *testing.T, remote *fakeCluster) *remoteFixture {
	t.Helper()
	local := newFakeCluster(engineLists(true, false), []runtime.Object{
		kyvernoPolicy(KyvernoClusterPolicyGVR, "ClusterPolicy", "", "local-only-policy"),
	})
	clients := &fakeClients{clusters: map[string]*fakeCluster{remoteCluster: remote, k8s.LocalClusterID: local}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disc := NewDiscoverer(nil, nil, logger)
	disc.status = &EngineStatus{Detected: EngineKyverno, Kyverno: &EngineDetail{Available: true, Namespace: "kyverno-local"}}
	h := &Handler{
		Discoverer:    disc,
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Clients:       clients,
		Presence:      k8s.NewPresence(clients),
		Logger:        logger,
		// The local service-account cache, fresh so the local path never
		// needs K8sClient (nil here: reaching it would panic).
		cachedData: &cachedPolicyData{
			policies:   []NormalizedPolicy{{ID: "kyverno::local-only-policy", Name: "local-only-policy", MatchKey: "local-only-policy", Engine: EngineKyverno, Severity: "medium"}},
			violations: []NormalizedViolation{{Policy: "local-only-policy", Namespace: "local-ns", Engine: EngineKyverno, Severity: "medium", Blocking: true}},
			fetchedAt:  time.Now(),
		},
	}
	return &remoteFixture{h: h, clients: clients, local: local}
}

func (f *remoteFixture) assertNoLocal(t *testing.T) {
	t.Helper()
	if n := len(f.local.dyn.Actions()) + len(f.local.typed.Actions()); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

// remoteLists counts the list calls the remote's dynamic client received.
func remoteLists(c *fakeCluster) int {
	n := 0
	for _, a := range c.dyn.Actions() {
		if a.GetVerb() == "list" {
			n++
		}
	}
	return n
}

func callAs(t *testing.T, clusterID string, user *auth.User, h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := auth.ContextWithUser(req.Context(), user)
	ctx = middleware.WithClusterID(ctx, clusterID)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

func adminUser() *auth.User {
	return &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}}
}

func callOn(t *testing.T, clusterID string, h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	return callAs(t, clusterID, adminUser(), h, target)
}

func call(t *testing.T, h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	return callOn(t, remoteCluster, h, target)
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return env.Data
}

type errorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

// unreachableErr is a transport failure whose text names the remote address.
var unreachableErr = &url.Error{Op: "Get", URL: "https://10.20.30.40:6443/apis",
	Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}

func assertNoLeak(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	for _, s := range []string{"10.20.30.40", "connection refused", "RBAC: user admin"} {
		if strings.Contains(rr.Body.String(), s) {
			t.Errorf("response leaks %q: %s", s, rr.Body.String())
		}
	}
}

func policyNames(ps []NormalizedPolicy) map[string]int {
	out := map[string]int{}
	for _, p := range ps {
		out[p.Name] = p.ViolationCount
	}
	return out
}

func TestRemote_ListPoliciesReturnsRemoteObjects(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())

	rr := call(t, f.h.HandleListPolicies, "/policies")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := policyNames(decode[[]NormalizedPolicy](t, rr))
	want := map[string]int{"remote-require-limits": 1, "remote-owner-label": 1}
	if len(got) != len(want) || got["remote-require-limits"] != 1 || got["remote-owner-label"] != 1 {
		t.Errorf("policies (name -> violations) = %v, want %v", got, want)
	}
	f.assertNoLocal(t)
}

func TestRemote_ListViolationsReturnsRemoteObjects(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())

	rr := call(t, f.h.HandleListViolations, "/policies/violations")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[[]NormalizedViolation](t, rr)
	policies := map[string]bool{}
	for _, v := range got {
		policies[v.Policy] = true
		if v.Namespace != "shop" {
			t.Errorf("violation %+v, want namespace shop", v)
		}
	}
	if len(got) != 2 || !policies["remote-require-limits"] || !policies["K8sRequiredLabels/remote-owner-label"] {
		t.Errorf("violations = %+v, want the remote report's and the remote constraint's", got)
	}
	f.assertNoLocal(t)
}

func TestRemote_ComplianceScoresTheRemote(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())

	rr := call(t, f.h.HandleCompliance, "/policies/compliance")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[ComplianceScore](t, rr)
	// Two remote policies, both violated by a blocking violation; the local
	// cache's one policy must not be counted.
	if got.Total != 2 || got.Pass != 0 || got.Fail != 2 || got.Score != 0 {
		t.Errorf("compliance = %+v, want total 2, pass 0, fail 2, score 0", got)
	}
	f.assertNoLocal(t)
}

func TestRemote_StatusReadsTheRemote(t *testing.T) {
	tests := []struct {
		name       string
		cluster    *fakeCluster
		targetErr  error
		wantEngine Engine
		wantReason string
	}{
		{"both engines on remote", remoteBothEngines(), nil, EngineBoth, ""},
		{"gatekeeper only", newFakeCluster(engineLists(false, true), nil), nil, EngineGatekeeper, ""},
		{"nothing on remote", newFakeCluster(nil, nil), nil, EngineNone, string(k8s.ReasonDiscoveryMissing)},
		{"unreachable", newFakeCluster(nil, nil), unreachableErr, EngineNone, string(k8s.ReasonUnreachable)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRemoteFixture(t, tt.cluster)
			f.clients.targetErr = tt.targetErr

			rr := call(t, f.h.HandleStatus, "/policies/status")
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			got := decode[EngineStatus](t, rr)
			if got.Detected != tt.wantEngine || got.Reason != tt.wantReason {
				t.Errorf("status = %+v, want detected %q reason %q", got, tt.wantEngine, tt.wantReason)
			}
			assertNoLeak(t, rr)
			f.assertNoLocal(t)
		})
	}
}

// The remote status names the remote's own engine namespace, read from its
// webhook configurations as the user.
func TestRemote_StatusReadsRemoteWebhooks(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())

	got := decode[EngineStatus](t, call(t, f.h.HandleStatus, "/policies/status"))
	if got.Kyverno == nil || got.Kyverno.Namespace != "kyverno-remote" || got.Kyverno.Webhooks != 1 {
		t.Errorf("kyverno = %+v, want the remote's namespace kyverno-remote with 1 webhook", got.Kyverno)
	}
	if got.Gatekeeper == nil || got.Gatekeeper.Namespace != "" {
		t.Errorf("gatekeeper = %+v, want available with no webhook namespace", got.Gatekeeper)
	}
}

// failingDiscovery fails ServerGroupsAndResources outright, so the remote's
// discovery is unreadable.
type failingDiscovery struct {
	*fakediscovery.FakeDiscovery
}

func (failingDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, nil, errors.New("discovery failed at 10.20.30.40")
}

// A discovery the engines' presence could be read from but whose full read
// fails is unknown, not absent: the status says discovery_unavailable and
// the lists fail with the same reason.
func TestRemote_DiscoveryUnavailableIsUnknown(t *testing.T) {
	remote := remoteBothEngines()
	f := newRemoteFixture(t, remote)
	remote.discOverride = failingDiscovery{remote.disc}

	got := decode[EngineStatus](t, call(t, f.h.HandleStatus, "/policies/status"))
	if got.Detected != EngineNone || got.Reason != string(k8s.ReasonDiscoveryUnavailable) {
		t.Errorf("status = %+v, want detected none with reason discovery_unavailable", got)
	}

	rr := call(t, f.h.HandleListPolicies, "/policies")
	var body errorBody
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if rr.Code != http.StatusBadGateway || body.Error.Reason != string(k8s.ReasonDiscoveryUnavailable) {
		t.Errorf("list = %d %s, want 502 discovery_unavailable", rr.Code, rr.Body.String())
	}
	assertNoLeak(t, rr)
	f.assertNoLocal(t)
}

// A remote with no engine answers an empty list, even though the local
// cluster has a policy in its cache.
func TestRemote_NoEngineOnRemoteIsEmpty(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(nil, nil))

	for _, route := range []struct {
		name string
		h    http.HandlerFunc
	}{{"policies", f.h.HandleListPolicies}, {"violations", f.h.HandleListViolations}} {
		rr := call(t, route.h, "/policies")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", route.name, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "local-only-policy") {
			t.Errorf("%s: answer carries the local cluster's data: %s", route.name, rr.Body.String())
		}
	}
	score := decode[ComplianceScore](t, call(t, f.h.HandleCompliance, "/policies/compliance"))
	if score.Total != 0 {
		t.Errorf("compliance = %+v, want no policies", score)
	}
	f.assertNoLocal(t)
}

// One failed list fails the view: a partial union would undercount
// violations and read as a cleaner cluster than the remote is.
func TestRemote_FailedListFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		resource   string
		err        error
		wantCode   int
		wantReason string
	}{
		{"report list forbidden", "policyreports", apierrors.NewForbidden(PolicyReportGVR.GroupResource(), "", errors.New("RBAC: user admin at 10.20.30.40 cannot list")), http.StatusForbidden, string(k8s.ReasonForbidden)},
		{"report list unreachable", "policyreports", unreachableErr, http.StatusBadGateway, string(k8s.ReasonUnreachable)},
		{"kyverno policy list unreachable", "clusterpolicies", unreachableErr, http.StatusBadGateway, string(k8s.ReasonUnreachable)},
		{"constraint list forbidden", "k8srequiredlabels", apierrors.NewForbidden(requiredLabelsGVR.GroupResource(), "", errors.New("RBAC: user admin at 10.20.30.40 cannot list")), http.StatusForbidden, string(k8s.ReasonForbidden)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := remoteBothEngines()
			remote.dyn.PrependReactor("list", tt.resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, tt.err
			})
			f := newRemoteFixture(t, remote)

			for _, h := range []http.HandlerFunc{f.h.HandleListPolicies, f.h.HandleListViolations, f.h.HandleCompliance} {
				rr := call(t, h, "/policies")
				var body errorBody
				_ = json.Unmarshal(rr.Body.Bytes(), &body)
				if rr.Code != tt.wantCode || body.Error.Reason != tt.wantReason {
					t.Errorf("got %d %s, want %d with reason %q", rr.Code, rr.Body.String(), tt.wantCode, tt.wantReason)
				}
				assertNoLeak(t, rr)
			}
			f.assertNoLocal(t)
		})
	}
}

// A list of a resource removed after discovery was cached is gone, not
// failed: the view answers without it.
func TestRemote_GoneResourceIsSkipped(t *testing.T) {
	remote := remoteBothEngines()
	remote.dyn.PrependReactor("list", "k8srequiredlabels", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(requiredLabelsGVR.GroupResource(), "")
	})
	f := newRemoteFixture(t, remote)

	rr := call(t, f.h.HandleListPolicies, "/policies")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := policyNames(decode[[]NormalizedPolicy](t, rr))
	if len(got) != 1 || got["remote-require-limits"] != 1 {
		t.Errorf("policies = %v, want only remote-require-limits", got)
	}
}

func TestRemote_TargetErrorIsFixedText(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())
	f.clients.targetErr = unreachableErr

	rr := call(t, f.h.HandleListViolations, "/policies/violations")
	var body errorBody
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if rr.Code != http.StatusBadGateway || body.Error.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("got %d %s, want 502 unreachable", rr.Code, rr.Body.String())
	}
	assertNoLeak(t, rr)
}

// Remote reads are cached per (cluster, identity): one identity's repeat
// reads share a fetch, and another identity gets its own (R5).
func TestRemote_CachePerIdentity(t *testing.T) {
	remote := remoteBothEngines()
	f := newRemoteFixture(t, remote)

	call(t, f.h.HandleListPolicies, "/policies")
	first := remoteLists(remote)
	if first == 0 {
		t.Fatal("first read listed nothing on the remote")
	}
	call(t, f.h.HandleListViolations, "/policies/violations")
	if n := remoteLists(remote); n != first {
		t.Errorf("same identity re-listed: %d lists after the second read, want %d", n, first)
	}

	other := &auth.User{Username: "ops", KubernetesUsername: "ops", KubernetesGroups: []string{"ops"}, Roles: []string{"admin"}}
	callAs(t, remoteCluster, other, f.h.HandleListPolicies, "/policies")
	if n := remoteLists(remote); n != 2*first {
		t.Errorf("another identity shared the cached read: %d lists, want %d", n, 2*first)
	}

	f.h.EvictRemoteCache(remoteCluster)
	call(t, f.h.HandleListPolicies, "/policies")
	if n := remoteLists(remote); n != 3*first {
		t.Errorf("evicted cluster was not re-read: %d lists, want %d", n, 3*first)
	}
}

// Compliance history is recorded for the local cluster only, so a remote
// selection is refused, not answered with the local trend. The handler has
// no ComplianceStore, which locally answers 503: the 501 proves the remote
// check runs first and reads nothing.
func TestRemote_ComplianceHistoryIsUnsupported(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())

	rr := call(t, f.h.HandleComplianceHistory, "/policies/compliance/history")
	var body errorBody
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if rr.Code != http.StatusNotImplemented || body.Error.Reason != "remote_history_unsupported" {
		t.Errorf("got %d %s, want 501 remote_history_unsupported", rr.Code, rr.Body.String())
	}

	local := callOn(t, k8s.LocalClusterID, f.h.HandleComplianceHistory, "/policies/compliance/history")
	if local.Code != http.StatusServiceUnavailable {
		t.Errorf("local history without a store = %d, want the historical 503", local.Code)
	}
}

// The local cluster keeps its service-account cache and discoverer: no
// cluster client is resolved, the status carries no reason, and the cached
// local data is what is served.
func TestLocal_Unchanged(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())

	status := decode[EngineStatus](t, callOn(t, k8s.LocalClusterID, f.h.HandleStatus, "/policies/status"))
	if status.Detected != EngineKyverno || status.Reason != "" || status.Kyverno.Namespace != "kyverno-local" {
		t.Errorf("local status = %+v, want the local discoverer's kyverno with no reason", status)
	}
	got := policyNames(decode[[]NormalizedPolicy](t, callOn(t, k8s.LocalClusterID, f.h.HandleListPolicies, "/policies")))
	if len(got) != 1 || got["local-only-policy"] != 1 {
		t.Errorf("local policies = %v, want the local cache's local-only-policy", got)
	}
	if n := f.clients.resolved.Load(); n != 0 {
		t.Errorf("local reads resolved %d cluster clients, want 0", n)
	}
}

// Gatekeeper serves each constraint kind at several versions at once. The
// constraint is read once, at the highest version, not once per version,
// which would duplicate the policy and multiply its violations.
func TestRemote_MultiVersionConstraintReadOnce(t *testing.T) {
	lists := engineLists(false, true)
	lists = append(lists, &metav1.APIResourceList{GroupVersion: "constraints.gatekeeper.sh/v1", APIResources: []metav1.APIResource{
		{Name: "k8srequiredlabels", Kind: "K8sRequiredLabels"},
	}})
	remote := newFakeCluster(lists, nil)
	// The API server serves the same object at both versions.
	for _, gvr := range []schema.GroupVersionResource{requiredLabelsGVR, requiredLabelsV1GVR} {
		obj := requiredLabels("remote-owner-label", "shop")
		obj.SetAPIVersion(gvr.GroupVersion().String())
		if err := remote.dyn.Tracker().Create(gvr, obj, ""); err != nil {
			t.Fatal(err)
		}
	}
	f := newRemoteFixture(t, remote)

	got := decode[[]NormalizedPolicy](t, call(t, f.h.HandleListPolicies, "/policies"))
	if len(got) != 1 || got[0].ViolationCount != 1 {
		t.Fatalf("policies = %+v, want one constraint with one violation", got)
	}
	if v := decode[[]NormalizedViolation](t, call(t, f.h.HandleListViolations, "/policies/violations")); len(v) != 1 {
		t.Errorf("violations = %+v, want one", v)
	}
	for _, a := range remote.dyn.Actions() {
		if a.GetVerb() == "list" && a.GetResource() == requiredLabelsGVR {
			t.Errorf("listed %v; want only the highest served version, v1", a.GetResource())
		}
	}
}

// partialDiscovery serves its lists but reports the named groups as failed
// to load, the shape client-go returns when some APIServices are down.
type partialDiscovery struct {
	*fakediscovery.FakeDiscovery
	failed []string
}

func (p partialDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	groups, lists, _ := p.FakeDiscovery.ServerGroupsAndResources()
	failed := map[schema.GroupVersion]error{}
	for _, g := range p.failed {
		failed[schema.GroupVersion{Group: g, Version: "v1"}] = errors.New("unavailable at 10.20.30.40")
	}
	return groups, lists, &discovery.ErrGroupDiscoveryFailed{Groups: failed}
}

// A group that failed to load makes the answer unknown only when this
// package needs it; an unrelated failed group does not stop the view.
func TestRemote_PartialDiscoveryFailure(t *testing.T) {
	tests := []struct {
		name        string
		failed      string
		wantUnknown bool
	}{
		{"kyverno group", "kyverno.io", true},
		{"gatekeeper templates group", "templates.gatekeeper.sh", true},
		{"reports group while kyverno is installed", "wgpolicyk8s.io", true},
		{"constraints group while gatekeeper is installed", gatekeeperConstraintsGroup, true},
		{"unrelated group", "example.io", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := remoteBothEngines()
			remote.discOverride = partialDiscovery{FakeDiscovery: remote.disc, failed: []string{tt.failed}}
			f := newRemoteFixture(t, remote)

			status := decode[EngineStatus](t, call(t, f.h.HandleStatus, "/policies/status"))
			rr := call(t, f.h.HandleListPolicies, "/policies")
			if tt.wantUnknown {
				if status.Detected != EngineNone || status.Reason != string(k8s.ReasonDiscoveryUnavailable) {
					t.Errorf("status = %+v, want detected none with reason discovery_unavailable", status)
				}
				var body errorBody
				_ = json.Unmarshal(rr.Body.Bytes(), &body)
				if rr.Code != http.StatusBadGateway || body.Error.Reason != string(k8s.ReasonDiscoveryUnavailable) {
					t.Errorf("list = %d %s, want 502 discovery_unavailable", rr.Code, rr.Body.String())
				}
				assertNoLeak(t, rr)
				return
			}
			if status.Detected != EngineBoth || status.Reason != "" {
				t.Errorf("status = %+v, want both engines with no reason", status)
			}
			if rr.Code != http.StatusOK || len(decode[[]NormalizedPolicy](t, rr)) != 2 {
				t.Errorf("list = %d %s, want 200 with both remote policies", rr.Code, rr.Body.String())
			}
		})
	}
}

// Kyverno without the PolicyReport API: its policies are listed, there are
// no violations to read, and nothing fails.
func TestRemote_KyvernoWithoutReports(t *testing.T) {
	lists := engineLists(true, false)[:1] // kyverno.io only, no wgpolicyk8s.io
	remote := newFakeCluster(lists, []runtime.Object{
		kyvernoPolicy(KyvernoClusterPolicyGVR, "ClusterPolicy", "", "remote-require-limits"),
	})
	f := newRemoteFixture(t, remote)

	got := policyNames(decode[[]NormalizedPolicy](t, call(t, f.h.HandleListPolicies, "/policies")))
	if len(got) != 1 || got["remote-require-limits"] != 0 {
		t.Errorf("policies = %v, want remote-require-limits with no violations", got)
	}
	for _, a := range remote.dyn.Actions() {
		if a.GetResource().Group == PolicyReportGVR.Group {
			t.Errorf("listed %v, which the remote does not serve", a.GetResource())
		}
	}
}

// Remote data still passes the per-namespace RBAC filter, asked of the
// remote cluster, and a non-admin sees no engine namespace.
func TestRemote_RBACFilterAndNonAdminStatus(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())
	f.h.AccessChecker = resources.NewAlwaysDenyAccessChecker()
	dev := &auth.User{Username: "dev", KubernetesUsername: "dev", KubernetesGroups: []string{"devs"}}

	// Every remote violation is in namespace shop, which the checker denies.
	if v := decode[[]NormalizedViolation](t, callAs(t, remoteCluster, dev, f.h.HandleListViolations, "/policies/violations")); len(v) != 0 {
		t.Errorf("violations = %+v, want none: the user cannot list pods in shop", v)
	}
	// Cluster-scoped policies stay visible; their counts come from the
	// filtered violations.
	got := policyNames(decode[[]NormalizedPolicy](t, callAs(t, remoteCluster, dev, f.h.HandleListPolicies, "/policies")))
	if len(got) != 2 || got["remote-require-limits"] != 0 || got["remote-owner-label"] != 0 {
		t.Errorf("policies = %v, want both cluster-scoped policies with zero visible violations", got)
	}

	status := decode[EngineStatus](t, callAs(t, remoteCluster, dev, f.h.HandleStatus, "/policies/status"))
	if status.Kyverno == nil || status.Kyverno.Namespace != "" {
		t.Errorf("non-admin kyverno = %+v, want the namespace stripped", status.Kyverno)
	}
	// The strip must not reach the shared cached status the admin reads.
	admin := decode[EngineStatus](t, call(t, f.h.HandleStatus, "/policies/status"))
	if admin.Kyverno == nil || admin.Kyverno.Namespace != "kyverno-remote" {
		t.Errorf("admin kyverno = %+v, want kyverno-remote", admin.Kyverno)
	}
}
