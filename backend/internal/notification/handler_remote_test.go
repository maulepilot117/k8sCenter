package notification

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
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const remoteCluster = "remote-1"

// remoteHost is the address a remote cluster's transport errors name. No
// response or audit row may carry it.
const remoteHost = "10.20.30.40"

// fakeCluster is one fake cluster: its discovery and dynamic client.
type fakeCluster struct {
	disc *fakediscovery.FakeDiscovery
	dyn  *dynfake.FakeDynamicClient
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. A
// target error makes every resolution fail.
type fakeClients struct {
	clusters  map[string]*fakeCluster
	targetErr error
}

func (f *fakeClients) cluster(id string) (*fakeCluster, error) {
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
	return &k8s.TargetSchema{ClusterID: id, Discovery: disc, Invalidate: func() {}}, nil
}

var listKinds = map[schema.GroupVersionResource]string{
	FluxProviderGVR: "ProviderList",
	FluxAlertGVR:    "AlertList",
	FluxReceiverGVR: "ReceiverList",
}

var gvrForKind = map[string]schema.GroupVersionResource{
	"Provider": FluxProviderGVR,
	"Alert":    FluxAlertGVR,
	"Receiver": FluxReceiverGVR,
}

const testNS = "flux-system"

func fluxObj(kind, name string) *unstructured.Unstructured {
	gvr := gvrForKind[kind]
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.GroupVersion().String(),
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": testNS},
		"spec":       map[string]any{"type": "generic"},
	}}
}

// notificationLists is discovery for a cluster serving the notification API
// at providerVersion (Providers and Alerts) plus Receivers at v1. An empty
// providerVersion serves no notification API at all.
func notificationLists(providerVersion string) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}}}}
	if providerVersion == "" {
		return lists
	}
	return append(lists,
		&metav1.APIResourceList{GroupVersion: FluxProviderGVR.Group + "/" + providerVersion, APIResources: []metav1.APIResource{
			{Name: "providers", Kind: "Provider", Namespaced: true},
			{Name: "alerts", Kind: "Alert", Namespaced: true},
		}},
		&metav1.APIResourceList{GroupVersion: FluxReceiverGVR.GroupVersion().String(), APIResources: []metav1.APIResource{
			{Name: "receivers", Kind: "Receiver", Namespaced: true},
		}},
	)
}

func newFakeCluster(t *testing.T, providerVersion string, objs ...*unstructured.Unstructured) *fakeCluster {
	t.Helper()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds)
	for _, o := range objs {
		if err := dyn.Tracker().Create(gvrForKind[o.GetKind()], o, o.GetNamespace()); err != nil {
			t.Fatalf("seed %s: %v", o.GetName(), err)
		}
	}
	return &fakeCluster{disc: &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: notificationLists(providerVersion)}}, dyn: dyn}
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

type harness struct {
	h       *Handler
	clients *fakeClients
	audit   *recordingAudit
}

// newHarness builds a Handler over a remote cluster serving remoteObjs at
// providerVersion and a local cluster (reachable only through the same
// ClusterClients) seeded with its own Provider, so a local read on the
// remote path shows up in its recorded actions. K8sClient is nil, so a
// service-account read on the remote path panics.
func newHarness(t *testing.T, providerVersion string, remoteObjs ...*unstructured.Unstructured) *harness {
	t.Helper()
	clients := &fakeClients{clusters: map[string]*fakeCluster{
		remoteCluster: newFakeCluster(t, providerVersion, remoteObjs...),
		"local":       newFakeCluster(t, FluxProviderGVR.Version, fluxObj("Provider", "local-provider")),
	}}
	hs := &harness{clients: clients, audit: &recordingAudit{}}
	hs.h = &Handler{
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuditLogger:   hs.audit,
		Clients:       clients,
		Presence:      k8s.NewPresence(clients),
	}
	return hs
}

func (hs *harness) localActions() int { return len(hs.clients.clusters["local"].dyn.Actions()) }

func (hs *harness) remoteDyn() *dynfake.FakeDynamicClient {
	return hs.clients.clusters[remoteCluster].dyn
}

func countVerb(dyn *dynfake.FakeDynamicClient, verb, resource string) int {
	n := 0
	for _, a := range dyn.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

func do(t *testing.T, clusterID, method string, h http.HandlerFunc, params map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/gitops/notifications", strings.NewReader(body))
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

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var resp struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return resp.Data
}

type errorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error %q: %v", rr.Body.String(), err)
	}
	return body
}

type providerList struct {
	Providers []NormalizedProvider `json:"providers"`
	Total     int                  `json:"total"`
}

func providerNames(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("provider list status %d: %s", rr.Code, rr.Body.String())
	}
	var out []string
	for _, p := range decode[providerList](t, rr).Providers {
		out = append(out, p.Name)
	}
	return out
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

const providerBody = `{"name":"new-provider","namespace":"flux-system","type":"generic","address":"https://hooks.example.com"}`

var providerParams = map[string]string{"namespace": testNS, "name": "remote-provider"}

func TestRemote_ListsReturnRemoteObjects(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version,
		fluxObj("Provider", "remote-provider"), fluxObj("Alert", "remote-alert"), fluxObj("Receiver", "remote-receiver"))

	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 1 || got[0] != "remote-provider" {
		t.Errorf("providers = %v, want only remote-provider", got)
	}

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListAlerts, nil, "")
	alerts := decode[struct {
		Alerts []NormalizedAlert `json:"alerts"`
	}](t, rr).Alerts
	if rr.Code != http.StatusOK || len(alerts) != 1 || alerts[0].Name != "remote-alert" {
		t.Errorf("alerts: status %d, %+v, want only remote-alert", rr.Code, alerts)
	}

	rr = do(t, remoteCluster, http.MethodGet, hs.h.HandleListReceivers, nil, "")
	receivers := decode[struct {
		Receivers []NormalizedReceiver `json:"receivers"`
	}](t, rr).Receivers
	if rr.Code != http.StatusOK || len(receivers) != 1 || receivers[0].Name != "remote-receiver" {
		t.Errorf("receivers: status %d, %+v, want only remote-receiver", rr.Code, receivers)
	}

	st := decode[NotificationStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if !st.Available || st.Reason != "" || st.ProviderCount != 1 || st.AlertCount != 1 || st.ReceiverCount != 1 || len(st.Coverage) != 0 {
		t.Errorf("status = %+v, want available with one of each and no reason", st)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_CreateProviderReachesRemoteAndNextListShowsIt(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version)

	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 0 {
		t.Fatalf("providers before create = %v, want none", got)
	}

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.remoteDyn(), "create", "providers"); n != 1 {
		t.Errorf("remote creates = %d, want 1", n)
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultSuccess || e.Action != audit.ActionCreate {
		t.Errorf("audit = %+v, want remote cluster create success", e)
	}

	// The write evicted the cached list: no 30s stale window.
	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 1 || got[0] != "new-provider" {
		t.Errorf("providers after create = %v, want [new-provider]", got)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_EveryWriteActsOnTheRemote(t *testing.T) {
	alertParams := map[string]string{"namespace": testNS, "name": "remote-alert"}
	receiverParams := map[string]string{"namespace": testNS, "name": "remote-receiver"}
	cases := []struct {
		name     string
		handler  func(*Handler) http.HandlerFunc
		method   string
		params   map[string]string
		body     string
		verb     string
		resource string
	}{
		{"update provider", func(h *Handler) http.HandlerFunc { return h.HandleUpdateProvider }, http.MethodPut, providerParams, `{"type":"generic"}`, "update", "providers"},
		{"delete provider", func(h *Handler) http.HandlerFunc { return h.HandleDeleteProvider }, http.MethodDelete, providerParams, "", "delete", "providers"},
		{"suspend provider", func(h *Handler) http.HandlerFunc { return h.HandleSuspendProvider }, http.MethodPost, providerParams, `{"suspend":true}`, "patch", "providers"},
		{"create alert", func(h *Handler) http.HandlerFunc { return h.HandleCreateAlert }, http.MethodPost, nil,
			`{"name":"new-alert","namespace":"flux-system","providerRef":"remote-provider","eventSources":[{"kind":"Kustomization","name":"*"}]}`, "create", "alerts"},
		{"update alert", func(h *Handler) http.HandlerFunc { return h.HandleUpdateAlert }, http.MethodPut, alertParams,
			`{"providerRef":"remote-provider","eventSources":[{"kind":"Kustomization","name":"*"}]}`, "update", "alerts"},
		{"delete alert", func(h *Handler) http.HandlerFunc { return h.HandleDeleteAlert }, http.MethodDelete, alertParams, "", "delete", "alerts"},
		{"suspend alert", func(h *Handler) http.HandlerFunc { return h.HandleSuspendAlert }, http.MethodPost, alertParams, `{"suspend":true}`, "patch", "alerts"},
		{"create receiver", func(h *Handler) http.HandlerFunc { return h.HandleCreateReceiver }, http.MethodPost, nil,
			`{"name":"new-receiver","namespace":"flux-system","type":"generic","secretRef":"token","resources":[{"kind":"GitRepository","name":"repo"}]}`, "create", "receivers"},
		{"update receiver", func(h *Handler) http.HandlerFunc { return h.HandleUpdateReceiver }, http.MethodPut, receiverParams,
			`{"type":"generic","secretRef":"token","resources":[{"kind":"GitRepository","name":"repo"}]}`, "update", "receivers"},
		{"delete receiver", func(h *Handler) http.HandlerFunc { return h.HandleDeleteReceiver }, http.MethodDelete, receiverParams, "", "delete", "receivers"},
		{"suspend receiver", func(h *Handler) http.HandlerFunc { return h.HandleSuspendReceiver }, http.MethodPost, receiverParams, `{"suspend":true}`, "patch", "receivers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHarness(t, FluxProviderGVR.Version,
				fluxObj("Provider", "remote-provider"), fluxObj("Alert", "remote-alert"), fluxObj("Receiver", "remote-receiver"))

			rr := do(t, remoteCluster, tc.method, tc.handler(hs.h), tc.params, tc.body)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			if n := countVerb(hs.remoteDyn(), tc.verb, tc.resource); n != 1 {
				t.Errorf("remote %s %s = %d, want 1", tc.verb, tc.resource, n)
			}
			if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultSuccess {
				t.Errorf("audit = %+v, want remote cluster success", e)
			}
			if n := hs.localActions(); n != 0 {
				t.Errorf("local cluster recorded %d actions, want 0", n)
			}
		})
	}
}

func TestRemote_V1beta2OnlyReportsNotInstalledAndRefusesWrites(t *testing.T) {
	hs := newHarness(t, "v1beta2")

	st := decode[NotificationStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if st.Available || st.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("status = %+v, want unavailable with discovery_missing", st)
	}
	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 0 {
		t.Errorf("providers = %v, want none", got)
	}

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody)
	if rr.Code != http.StatusNotFound || decodeError(t, rr).Error.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("create: status %d, body %s, want 404 discovery_missing", rr.Code, rr.Body.String())
	}
	for _, a := range hs.remoteDyn().Actions() {
		if a.GetVerb() != "list" {
			t.Errorf("remote recorded %s %s, want no write", a.GetVerb(), a.GetResource().Resource)
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_NotInstalledListsAreEmptyAndStatusSaysMissing(t *testing.T) {
	hs := newHarness(t, "")

	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 0 {
		t.Errorf("providers = %v, want none", got)
	}
	st := decode[NotificationStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if st.Available || st.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("status = %+v, want unavailable with discovery_missing", st)
	}
	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleSuspendProvider, providerParams, `{"suspend":true}`)
	if rr.Code != http.StatusNotFound {
		t.Errorf("suspend status %d, want 404: %s", rr.Code, rr.Body.String())
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_ForbiddenReceiversGivePartialStatus(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version, fluxObj("Provider", "remote-provider"), fluxObj("Receiver", "remote-receiver"))
	hs.remoteDyn().PrependReactor("list", "receivers", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(FluxReceiverGVR.GroupResource(), "", errors.New("denied"))
	})

	st := decode[NotificationStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if !st.Available || st.ProviderCount != 1 || st.ReceiverCount != 0 {
		t.Errorf("status = %+v, want available with one provider", st)
	}
	if len(st.Coverage) != 1 || st.Coverage[0].Source != "receivers" || st.Coverage[0].Status != k8s.CoverageStatusForbidden {
		t.Errorf("coverage = %+v, want one forbidden receivers entry", st.Coverage)
	}

	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 1 {
		t.Errorf("providers = %v, want [remote-provider]", got)
	}
	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListReceivers, nil, "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("receivers list status %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestRemote_UnreachableReturnsTargetErrorWithoutTouchingLocal(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version, fluxObj("Provider", "remote-provider"))
	hs.clients.targetErr = unreachable()

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"suspend": do(t, remoteCluster, http.MethodPost, hs.h.HandleSuspendProvider, providerParams, `{"suspend":true}`),
		"list":    do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, ""),
		"create":  do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody),
	} {
		if rr.Code != http.StatusBadGateway || decodeError(t, rr).Error.Reason != string(k8s.ReasonUnreachable) {
			t.Errorf("%s: status %d, body %s, want 502 unreachable", name, rr.Code, rr.Body.String())
		}
		assertNoLeak(t, rr)
	}

	st := decode[NotificationStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if st.Available || st.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("status = %+v, want unavailable with unreachable", st)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_WriteTransportErrorIsRedactedAndAudited(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version)
	hs.remoteDyn().PrependReactor("create", "providers", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachable()
	})

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody)
	if rr.Code != http.StatusBadGateway || decodeError(t, rr).Error.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("status %d, body %s, want 502 unreachable", rr.Code, rr.Body.String())
	}
	assertNoLeak(t, rr)
	e := hs.audit.last(t)
	if e.ClusterID != remoteCluster || e.Result != audit.ResultFailure || strings.Contains(e.Detail, remoteHost) {
		t.Errorf("audit = %+v, want remote cluster failure without the remote address", e)
	}
}

func TestRemote_WriteRefusedByClusterIsAuditedDenied(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version, fluxObj("Provider", "remote-provider"))
	hs.remoteDyn().PrependReactor("delete", "providers", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(FluxProviderGVR.GroupResource(), "remote-provider", errors.New("denied"))
	})

	rr := do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteProvider, providerParams, "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultDenied {
		t.Errorf("audit = %+v, want remote cluster denied", e)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_AccessCheckFailureIsClassifiedNotDenied(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version, fluxObj("Provider", "remote-provider"))
	hs.h.AccessChecker = resources.NewErroringAccessChecker(unreachable())

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"create": do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody),
		"list":   do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, ""),
	} {
		if rr.Code != http.StatusBadGateway {
			t.Errorf("%s: status %d, want 502: %s", name, rr.Code, rr.Body.String())
		}
		assertNoLeak(t, rr)
	}
	if n := countVerb(hs.remoteDyn(), "create", "providers"); n != 0 {
		t.Errorf("remote creates = %d, want 0", n)
	}
}

func TestRemote_EvictRemoteCacheDropsCachedLists(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version)
	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 0 {
		t.Fatalf("providers = %v, want none", got)
	}
	if err := hs.remoteDyn().Tracker().Create(FluxProviderGVR, fluxObj("Provider", "out-of-band"), testNS); err != nil {
		t.Fatal(err)
	}
	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 0 {
		t.Fatalf("providers before evict = %v, want the cached empty list", got)
	}
	hs.h.EvictRemoteCache(remoteCluster)
	if got := providerNames(t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 1 {
		t.Errorf("providers after evict = %v, want [out-of-band]", got)
	}
}

// Local-path characterization: the local cluster keeps its service-account
// cache, its messages and its RBAC-denial behaviour.

func TestLocal_ListReadsServiceAccountCache(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version, fluxObj("Provider", "remote-provider"))
	hs.h.baseDynOverride = hs.clients.clusters["local"].dyn

	if got := providerNames(t, do(t, "local", http.MethodGet, hs.h.HandleListProviders, nil, "")); len(got) != 1 || got[0] != "local-provider" {
		t.Errorf("providers = %v, want only local-provider", got)
	}
	if n := len(hs.remoteDyn().Actions()); n != 0 {
		t.Errorf("remote cluster recorded %d actions, want 0", n)
	}
}

func TestLocal_CreateActsOnLocalAndKeepsMessages(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version)
	hs.h.baseDynOverride = hs.clients.clusters["local"].dyn

	rr := do(t, "local", http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status %d: %s", rr.Code, rr.Body.String())
	}
	if e := hs.audit.last(t); e.ClusterID != "local" || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want local success", e)
	}

	rr = do(t, "local", http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody)
	if rr.Code != http.StatusConflict || !strings.Contains(decodeError(t, rr).Error.Message, "'new-provider' already exists") {
		t.Errorf("duplicate create: status %d, body %s, want the local 409 message", rr.Code, rr.Body.String())
	}
	if n := len(hs.remoteDyn().Actions()); n != 0 {
		t.Errorf("remote cluster recorded %d actions, want 0", n)
	}
}

func TestLocal_AccessCheckErrorIsADenial(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version)
	hs.h.AccessChecker = resources.NewErroringAccessChecker(errors.New("sar failed"))

	rr := do(t, "local", http.MethodPost, hs.h.HandleCreateProvider, nil, providerBody)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_EveryListFailingIsAvailableWithFullCoverage(t *testing.T) {
	hs := newHarness(t, FluxProviderGVR.Version, fluxObj("Provider", "remote-provider"))
	hs.remoteDyn().PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(FluxProviderGVR.GroupResource(), "", errors.New("denied"))
	})

	st := decode[NotificationStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if !st.Available || st.Reason != "" || len(st.Coverage) != 3 {
		t.Errorf("status = %+v, want available with a coverage entry per list", st)
	}
	if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListProviders, nil, ""); rr.Code != http.StatusForbidden {
		t.Errorf("providers list status %d, want 403: %s", rr.Code, rr.Body.String())
	}
}
