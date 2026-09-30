package alerting

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
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const remoteCluster = "remote-1"

// remoteHost is the address a remote cluster's transport errors name. No
// response or audit row may carry it.
const remoteHost = "10.20.30.40"

type fakeCluster struct {
	disc *fakediscovery.FakeDiscovery
	dyn  *dynfake.FakeDynamicClient
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. A
// target error makes every remote resolution fail; the local cluster keeps
// resolving so a test can tell the two apart.
type fakeClients struct {
	clusters  map[string]*fakeCluster
	targetErr error
}

func (f *fakeClients) cluster(id string) (*fakeCluster, error) {
	if f.targetErr != nil && !k8s.IsLocalClusterID(id) {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	if _, err := f.cluster(id); err != nil {
		return nil, err
	}
	return kfake.NewSimpleClientset(), nil
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
	return &k8s.TargetSchema{ClusterID: id, IsLocal: k8s.IsLocalClusterID(id), Discovery: disc, Invalidate: func() {}}, nil
}

func ruleLists(installed bool) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}}}}
	if installed {
		lists = append(lists, &metav1.APIResourceList{GroupVersion: "monitoring.coreos.com/v1", APIResources: []metav1.APIResource{
			{Name: "prometheusrules", Kind: "PrometheusRule", Namespaced: true},
		}})
	}
	return lists
}

func rule(name string, managed bool) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]any{"name": name, "namespace": "monitoring"},
		"spec": map[string]any{"groups": []any{map[string]any{
			"name":  "g",
			"rules": []any{map[string]any{"alert": "A", "expr": "up == 0"}},
		}}},
	}}
	if managed {
		u.SetLabels(map[string]string{managedByLabel: managedByValue})
	}
	return u
}

func newFakeCluster(t *testing.T, installed bool, objs ...*unstructured.Unstructured) *fakeCluster {
	t.Helper()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		prometheusRuleGVR: "PrometheusRuleList",
	})
	for _, o := range objs {
		if err := dyn.Tracker().Create(prometheusRuleGVR, o, o.GetNamespace()); err != nil {
			t.Fatalf("seed %s: %v", o.GetName(), err)
		}
	}
	return &fakeCluster{disc: &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: ruleLists(installed)}}, dyn: dyn}
}

type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Log(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) last(t *testing.T) audit.Entry {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.entries) == 0 {
		t.Fatal("no audit entry recorded")
	}
	return a.entries[len(a.entries)-1]
}

type rulesHarness struct {
	h       *Handler
	clients *fakeClients
	audit   *recordingAudit
}

// newRulesHarness builds a Handler over a remote cluster serving remoteObjs
// and a local cluster seeded with its own managed rule. ClusterID is the
// static local id, so an audit row still reading it shows up as "local".
func newRulesHarness(t *testing.T, remoteInstalled, localInstalled bool, remoteObjs ...*unstructured.Unstructured) *rulesHarness {
	t.Helper()
	clients := &fakeClients{clusters: map[string]*fakeCluster{
		remoteCluster: newFakeCluster(t, remoteInstalled, remoteObjs...),
		"local":       newFakeCluster(t, localInstalled, rule("local-rule", true)),
	}}
	rh := &rulesHarness{clients: clients, audit: &recordingAudit{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rh.h = &Handler{
		Store:       NewMemoryStore(),
		Rules:       NewRulesManager(clients, k8s.NewPresence(clients), logger),
		AuditLogger: rh.audit,
		Logger:      logger,
		ClusterID:   "local",
	}
	return rh
}

func (rh *rulesHarness) dyn(id string) *dynfake.FakeDynamicClient { return rh.clients.clusters[id].dyn }

func countVerb(dyn *dynfake.FakeDynamicClient, verb string) int {
	n := 0
	for _, a := range dyn.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == "prometheusrules" {
			n++
		}
	}
	return n
}

func doRule(t *testing.T, clusterID, method string, h http.HandlerFunc, params map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/alerts/rules", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}})
	ctx = middleware.WithClusterID(ctx, clusterID)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

type ruleListBody struct {
	Data     []RuleSummary `json:"data"`
	Metadata struct {
		Available *bool  `json:"available"`
		Reason    string `json:"reason"`
	} `json:"metadata"`
}

func decodeList(t *testing.T, rr *httptest.ResponseRecorder) ruleListBody {
	t.Helper()
	var b ruleListBody
	if err := json.Unmarshal(rr.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return b
}

func errReason(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode error %q: %v", rr.Body.String(), err)
	}
	return b.Error.Reason
}

func unreachable() error {
	return &url.Error{Op: "Get", URL: "https://" + remoteHost + ":6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

func assertNoLeak(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(rr.Body.String(), remoteHost) {
		t.Errorf("body leaks the remote address: %s", rr.Body.String())
	}
}

const createBody = `{"namespace":"monitoring","content":{"metadata":{"name":"new-rule"},"spec":{"groups":[]}}}`

var ruleParams = map[string]string{"namespace": "monitoring", "name": "remote-rule"}

func TestRemoteRules_ListReturnsRemoteRules(t *testing.T) {
	rh := newRulesHarness(t, true, true, rule("remote-rule", true))

	rr := doRule(t, remoteCluster, http.MethodGet, rh.h.HandleListRules, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	b := decodeList(t, rr)
	if len(b.Data) != 1 || b.Data[0].Name != "remote-rule" || b.Data[0].RulesCount != 1 {
		t.Errorf("rules = %+v, want only remote-rule with one rule", b.Data)
	}
	if n := len(rh.dyn("local").Actions()); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemoteRules_WritesReachTheRemoteAndAuditIt(t *testing.T) {
	rh := newRulesHarness(t, true, true, rule("remote-rule", true))

	rr := doRule(t, remoteCluster, http.MethodPost, rh.h.HandleCreateRule, nil, createBody)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rr.Code, rr.Body.String())
	}
	if e := rh.audit.last(t); e.ClusterID != remoteCluster || e.Action != audit.ActionAlertRuleCreate || e.Result != audit.ResultSuccess {
		t.Errorf("create audit = %+v, want remote success", e)
	}

	rr = doRule(t, remoteCluster, http.MethodGet, rh.h.HandleGetRule, ruleParams, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", rr.Code, rr.Body.String())
	}

	rr = doRule(t, remoteCluster, http.MethodDelete, rh.h.HandleDeleteRule, ruleParams, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status %d: %s", rr.Code, rr.Body.String())
	}
	if e := rh.audit.last(t); e.ClusterID != remoteCluster || e.Action != audit.ActionAlertRuleDelete {
		t.Errorf("delete audit = %+v, want remote delete", e)
	}

	if n := countVerb(rh.dyn(remoteCluster), "create"); n != 1 {
		t.Errorf("remote creates = %d, want 1", n)
	}
	if n := countVerb(rh.dyn(remoteCluster), "delete"); n != 1 {
		t.Errorf("remote deletes = %d, want 1", n)
	}
	if n := len(rh.dyn("local").Actions()); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemoteRules_UpdateAppliesOnTheRemote(t *testing.T) {
	rh := newRulesHarness(t, true, true, rule("remote-rule", true))
	// The fake tracker cannot apply a server-side-apply patch; answer it.
	rh.dyn(remoteCluster).PrependReactor("patch", "prometheusrules", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, rule("remote-rule", true), nil
	})

	rr := doRule(t, remoteCluster, http.MethodPut, rh.h.HandleUpdateRule, ruleParams, `{"spec":{"groups":[]}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(rh.dyn(remoteCluster), "patch"); n != 1 {
		t.Errorf("remote patches = %d, want 1", n)
	}
	if e := rh.audit.last(t); e.ClusterID != remoteCluster || e.Action != audit.ActionAlertRuleUpdate {
		t.Errorf("update audit = %+v, want remote update", e)
	}
	if n := len(rh.dyn("local").Actions()); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemoteRules_NotInstalledIsExplicitAndIndependentOfLocal(t *testing.T) {
	// Local has the CRD, the remote does not.
	rh := newRulesHarness(t, false, true)

	rr := doRule(t, remoteCluster, http.MethodGet, rh.h.HandleListRules, nil, "")
	b := decodeList(t, rr)
	if rr.Code != http.StatusOK || b.Metadata.Available == nil || *b.Metadata.Available || b.Metadata.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("list: status %d, body %s, want available:false with discovery_missing", rr.Code, rr.Body.String())
	}
	rr = doRule(t, remoteCluster, http.MethodPost, rh.h.HandleCreateRule, nil, createBody)
	if rr.Code != http.StatusNotFound || errReason(t, rr) != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("create: status %d, body %s, want 404 discovery_missing", rr.Code, rr.Body.String())
	}
	if n := len(rh.dyn(remoteCluster).Actions()); n != 0 {
		t.Errorf("remote recorded %d actions, want none", n)
	}

	// Local still lists its rule.
	lb := decodeList(t, doRule(t, "local", http.MethodGet, rh.h.HandleListRules, nil, ""))
	if len(lb.Data) != 1 || lb.Data[0].Name != "local-rule" {
		t.Errorf("local rules = %+v, want local-rule", lb.Data)
	}
}

func TestRemoteRules_RemoteInstalledDoesNotMakeLocalAvailable(t *testing.T) {
	rh := newRulesHarness(t, true, false, rule("remote-rule", true))

	// Warm the remote verdict first.
	if b := decodeList(t, doRule(t, remoteCluster, http.MethodGet, rh.h.HandleListRules, nil, "")); len(b.Data) != 1 {
		t.Fatalf("remote rules = %+v, want one", b.Data)
	}
	b := decodeList(t, doRule(t, "local", http.MethodGet, rh.h.HandleListRules, nil, ""))
	if len(b.Data) != 0 || b.Metadata.Available == nil || *b.Metadata.Available || b.Metadata.Reason != "" {
		t.Errorf("local list = %+v, want not available with no reason", b)
	}
}

func TestLocalRules_AvailabilityIsNotLatched(t *testing.T) {
	rh := newRulesHarness(t, false, true)
	now := time.Now()
	rh.h.Rules.now = func() time.Time { return now }

	if b := decodeList(t, doRule(t, "local", http.MethodGet, rh.h.HandleListRules, nil, "")); len(b.Data) != 1 {
		t.Fatalf("local rules = %+v, want one", b.Data)
	}
	// The CRD is removed. After the recheck window the answer follows.
	rh.clients.clusters["local"].disc.Resources = ruleLists(false)
	now = now.Add(localRecheckInstalled + time.Second)
	b := decodeList(t, doRule(t, "local", http.MethodGet, rh.h.HandleListRules, nil, ""))
	if b.Metadata.Available == nil || *b.Metadata.Available {
		t.Errorf("local list after CRD removal = %+v, want not available", b)
	}
}

func TestRemoteRules_UnreachableReturnsTargetError(t *testing.T) {
	rh := newRulesHarness(t, true, true, rule("remote-rule", true))
	rh.clients.targetErr = unreachable()

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list":   doRule(t, remoteCluster, http.MethodGet, rh.h.HandleListRules, nil, ""),
		"create": doRule(t, remoteCluster, http.MethodPost, rh.h.HandleCreateRule, nil, createBody),
		"delete": doRule(t, remoteCluster, http.MethodDelete, rh.h.HandleDeleteRule, ruleParams, ""),
	} {
		if rr.Code != http.StatusBadGateway || errReason(t, rr) != string(k8s.ReasonUnreachable) {
			t.Errorf("%s: status %d, body %s, want 502 unreachable", name, rr.Code, rr.Body.String())
		}
		assertNoLeak(t, rr)
	}
	if n := len(rh.dyn("local").Actions()); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemoteRules_WriteTransportErrorIsRedactedAndAudited(t *testing.T) {
	rh := newRulesHarness(t, true, true)
	rh.dyn(remoteCluster).PrependReactor("create", "prometheusrules", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachable()
	})

	rr := doRule(t, remoteCluster, http.MethodPost, rh.h.HandleCreateRule, nil, createBody)
	if rr.Code != http.StatusBadGateway || errReason(t, rr) != string(k8s.ReasonUnreachable) {
		t.Errorf("status %d, body %s, want 502 unreachable", rr.Code, rr.Body.String())
	}
	assertNoLeak(t, rr)
	if e := rh.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultFailure || strings.Contains(e.Detail, remoteHost) {
		t.Errorf("audit = %+v, want remote failure without the remote address", e)
	}
}

func TestRemoteRules_ForbiddenWriteIsAuditedDenied(t *testing.T) {
	rh := newRulesHarness(t, true, true, rule("remote-rule", true))
	rh.dyn(remoteCluster).PrependReactor("delete", "prometheusrules", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(prometheusRuleGVR.GroupResource(), "remote-rule", errors.New("denied"))
	})

	rr := doRule(t, remoteCluster, http.MethodDelete, rh.h.HandleDeleteRule, ruleParams, "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if e := rh.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultDenied {
		t.Errorf("audit = %+v, want remote denied", e)
	}
}

func TestRules_DeleteRefusesUnmanagedRule(t *testing.T) {
	rh := newRulesHarness(t, true, true, rule("remote-rule", false))

	rr := doRule(t, remoteCluster, http.MethodDelete, rh.h.HandleDeleteRule, ruleParams, "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(rh.dyn(remoteCluster), "delete"); n != 0 {
		t.Errorf("remote deletes = %d, want 0", n)
	}
}

func TestRules_InvalidRuleNameIsABadRequest(t *testing.T) {
	rh := newRulesHarness(t, true, true)

	for _, clusterID := range []string{"local", remoteCluster} {
		rr := doRule(t, clusterID, http.MethodPost, rh.h.HandleCreateRule, nil, `{"namespace":"monitoring","content":{"metadata":{"name":"Bad_Name"}}}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", clusterID, rr.Code, rr.Body.String())
		}
	}
	if n := countVerb(rh.dyn(remoteCluster), "create") + countVerb(rh.dyn("local"), "create"); n != 0 {
		t.Errorf("creates = %d, want 0", n)
	}
}

func TestLocalRules_KeepTheirErrorMessages(t *testing.T) {
	rh := newRulesHarness(t, true, true)

	rr := doRule(t, "local", http.MethodGet, rh.h.HandleGetRule, map[string]string{"namespace": "monitoring", "name": "missing"}, "")
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "resource not found") {
		t.Errorf("status %d, body %s, want the local 404", rr.Code, rr.Body.String())
	}
	if n := len(rh.dyn(remoteCluster).Actions()); n != 0 {
		t.Errorf("remote cluster recorded %d actions, want 0", n)
	}
}

// R14: the active and history alert feeds read the local Alertmanager
// store whatever cluster is selected.
func TestAlertFeeds_IgnoreTheSelectedCluster(t *testing.T) {
	rh := newRulesHarness(t, true, true)
	if err := rh.h.Store.Record(context.Background(), AlertEvent{ID: "e1", Fingerprint: "fp1", Status: "firing", AlertName: "A", StartsAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	for _, clusterID := range []string{"local", remoteCluster} {
		rr := doRule(t, clusterID, http.MethodGet, rh.h.HandleListActive, nil, "")
		var b struct {
			Data []AlertEvent `json:"data"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &b); err != nil || rr.Code != http.StatusOK || len(b.Data) != 1 {
			t.Errorf("%s active: status %d, body %s, want the one local alert", clusterID, rr.Code, rr.Body.String())
		}
		rr = doRule(t, clusterID, http.MethodGet, rh.h.HandleListHistory, nil, "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "fp1") {
			t.Errorf("%s history: status %d, body %s, want the local alert", clusterID, rr.Code, rr.Body.String())
		}
	}
	if n := len(rh.dyn(remoteCluster).Actions()); n != 0 {
		t.Errorf("remote cluster recorded %d actions, want 0", n)
	}
}
