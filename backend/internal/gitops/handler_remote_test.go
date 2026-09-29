package gitops

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
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/gitprovider"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const remoteCluster = "remote-1"

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
	return &k8s.TargetSchema{ClusterID: id, Discovery: c.disc, Invalidate: func() {}}, nil
}

var gitopsListKinds = map[schema.GroupVersionResource]string{
	ArgoApplicationGVR:    "ApplicationList",
	ArgoApplicationSetGVR: "ApplicationSetList",
	FluxKustomizationGVR:  "KustomizationList",
	FluxHelmReleaseGVR:    "HelmReleaseList",
}

var gvrForKind = map[string]schema.GroupVersionResource{
	"Application":    ArgoApplicationGVR,
	"ApplicationSet": ArgoApplicationSetGVR,
	"Kustomization":  FluxKustomizationGVR,
	"HelmRelease":    FluxHelmReleaseGVR,
}

func obj(kind, ns, name string, labels map[string]string, spec map[string]any) *unstructured.Unstructured {
	gvr := gvrForKind[kind]
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.GroupVersion().String(),
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": ns},
	}}
	if spec != nil {
		u.Object["spec"] = spec
	}
	if labels != nil {
		u.SetLabels(labels)
	}
	return u
}

func argoApp(ns, name, repo string) *unstructured.Unstructured {
	return obj("Application", ns, name, nil, map[string]any{"source": map[string]any{"repoURL": repo}})
}

func apiList(gv string, kinds map[string]string) *metav1.APIResourceList {
	l := &metav1.APIResourceList{GroupVersion: gv}
	for kind, resource := range kinds {
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: resource, Kind: kind, Namespaced: true})
	}
	return l
}

// toolLists serves Argo CD and/or Flux CD discovery.
func toolLists(argo, flux bool) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}}}}
	if argo {
		lists = append(lists, apiList("argoproj.io/v1alpha1", map[string]string{"Application": "applications", "ApplicationSet": "applicationsets"}))
	}
	if flux {
		lists = append(lists,
			apiList("kustomize.toolkit.fluxcd.io/v1", map[string]string{"Kustomization": "kustomizations"}),
			apiList("helm.toolkit.fluxcd.io/v2", map[string]string{"HelmRelease": "helmreleases"}))
	}
	return lists
}

func newFakeCluster(t *testing.T, lists []*metav1.APIResourceList, objs ...*unstructured.Unstructured) *fakeCluster {
	t.Helper()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gitopsListKinds)
	for _, o := range objs {
		if err := dyn.Tracker().Create(gvrForKind[o.GetKind()], o, o.GetNamespace()); err != nil {
			t.Fatalf("seed %s: %v", o.GetName(), err)
		}
	}
	return &fakeCluster{disc: &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: lists}}, dyn: dyn}
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

type recordingNotifier struct {
	mu   sync.Mutex
	sent []notifications.Notification
	done chan struct{}
}

func (n *recordingNotifier) Emit(_ context.Context, note notifications.Notification) {
	n.mu.Lock()
	n.sent = append(n.sent, note)
	n.mu.Unlock()
	select {
	case n.done <- struct{}{}:
	default:
	}
}

type harness struct {
	h       *Handler
	clients *fakeClients
	audit   *recordingAudit
	notes   *recordingNotifier
}

// newHarness builds a Handler over a remote cluster serving remoteObjs and
// a local cluster (reachable only through the same ClusterClients) seeded
// with its own Argo app, so a local read on the remote path shows up in its
// recorded actions. K8sClient and the local Discoverer are nil, so a
// service-account or local-discovery read on the remote path panics.
func newHarness(t *testing.T, remoteLists []*metav1.APIResourceList, remoteObjs ...*unstructured.Unstructured) *harness {
	t.Helper()
	clients := &fakeClients{clusters: map[string]*fakeCluster{
		remoteCluster: newFakeCluster(t, remoteLists, remoteObjs...),
		"local":       newFakeCluster(t, toolLists(true, true), argoApp("argocd", "local-app", "https://github.com/acme/local-only")),
	}}
	hs := &harness{
		clients: clients,
		audit:   &recordingAudit{},
		notes:   &recordingNotifier{done: make(chan struct{}, 8)},
	}
	hs.h = &Handler{
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuditLogger:   hs.audit,
		NotifService:  hs.notes,
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

func do(t *testing.T, clusterID, method string, h http.HandlerFunc, path string, params map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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

type appList struct {
	Applications []NormalizedApp  `json:"applications"`
	Summary      AppListMetadata  `json:"summary"`
	Coverage     []SourceCoverage `json:"coverage"`
}

func appNames(apps []NormalizedApp) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return out
}

func unreachable() error {
	return &url.Error{Op: "Get", URL: "https://10.20.30.40:6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

func TestRemote_ListReturnsRemoteArgoAndFluxApps(t *testing.T) {
	hs := newHarness(t, toolLists(true, true),
		argoApp("argocd", "remote-app", "https://github.com/acme/remote"),
		obj("Kustomization", "flux-system", "remote-ks", nil, nil))

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListApplications, "/applications", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := appNames(decode[appList](t, rr).Applications)
	if strings.Join(got, ",") != "remote-app,remote-ks" && strings.Join(got, ",") != "remote-ks,remote-app" {
		t.Errorf("apps = %v, want remote-app and remote-ks only", got)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_SyncPatchesRemoteAppAuditsAndNotifiesRemote(t *testing.T) {
	hs := newHarness(t, toolLists(true, false), argoApp("argocd", "remote-app", "https://github.com/acme/remote"))
	list := func() {
		if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListApplications, "/applications", nil, ""); rr.Code != http.StatusOK {
			t.Fatalf("list status %d: %s", rr.Code, rr.Body.String())
		}
	}
	list()
	listsBefore := countVerb(hs.remoteDyn(), "list", "applications")

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleSync, "/sync", map[string]string{"id": "argo:argocd:remote-app"}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("sync status %d: %s", rr.Code, rr.Body.String())
	}
	if countVerb(hs.remoteDyn(), "patch", "applications") != 1 {
		t.Error("sync did not patch the remote application")
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want remote cluster success", e)
	}
	<-hs.notes.done
	hs.notes.mu.Lock()
	if note := hs.notes.sent[len(hs.notes.sent)-1]; note.ClusterID != remoteCluster {
		t.Errorf("notification cluster = %q, want %q", note.ClusterID, remoteCluster)
	}
	hs.notes.mu.Unlock()

	// The write evicted the remote cache, so the next list reads the cluster again.
	list()
	if countVerb(hs.remoteDyn(), "list", "applications") != listsBefore+1 {
		t.Error("list after sync was served from the pre-sync cache")
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_ForbiddenSyncIs403AndAuditedDenied(t *testing.T) {
	hs := newHarness(t, toolLists(true, false), argoApp("argocd", "remote-app", ""))
	hs.remoteDyn().PrependReactor("patch", "applications", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(ArgoApplicationGVR.GroupResource(), "remote-app", errors.New("no"))
	})

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleSync, "/sync", map[string]string{"id": "argo:argocd:remote-app"}, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultDenied {
		t.Errorf("audit = %+v, want remote cluster denied", e)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_CommitsAuthorizeAgainstRemoteApps(t *testing.T) {
	hs := newHarness(t, toolLists(true, false), argoApp("argocd", "remote-app", "https://github.com/acme/remote"))
	gh, err := gitprovider.NewGitHubClient("test-token", "", hs.h.Logger)
	if err != nil {
		t.Fatal(err)
	}
	hs.h.CommitCache = gitprovider.NewCommitCache(nil, gh, hs.h.Logger)

	// The repo only a local app uses must not unlock commit lookups on the remote.
	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleGetCommits,
		"/commits?repoURL=https://github.com/acme/local-only&shas=abcdef1", nil, "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_ForbiddenFluxListGivesPartialListWithCoverage(t *testing.T) {
	hs := newHarness(t, toolLists(true, true),
		argoApp("argocd", "remote-app", ""),
		obj("Kustomization", "flux-system", "remote-ks", nil, nil))
	hs.remoteDyn().PrependReactor("list", "kustomizations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(FluxKustomizationGVR.GroupResource(), "", errors.New("no"))
	})

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListApplications, "/applications", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[appList](t, rr)
	if names := appNames(got.Applications); len(names) != 1 || names[0] != "remote-app" {
		t.Errorf("apps = %v, want only remote-app", names)
	}
	want := []SourceCoverage{{Source: "kustomizations", Status: "forbidden", ReasonCode: string(k8s.ReasonForbidden)}}
	if len(got.Coverage) != 1 || got.Coverage[0] != want[0] {
		t.Errorf("coverage = %+v, want %+v", got.Coverage, want)
	}
}

func TestRemote_UnreachableFailsListDetailAndActionWithoutTouchingLocal(t *testing.T) {
	hs := newHarness(t, toolLists(true, false))
	hs.clients.targetErr = unreachable()

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list":   do(t, remoteCluster, http.MethodGet, hs.h.HandleListApplications, "/applications", nil, ""),
		"detail": do(t, remoteCluster, http.MethodGet, hs.h.HandleGetApplication, "/app", map[string]string{"id": "argo:argocd:remote-app"}, ""),
		"sync":   do(t, remoteCluster, http.MethodPost, hs.h.HandleSync, "/sync", map[string]string{"id": "argo:argocd:remote-app"}, ""),
		"appset": do(t, remoteCluster, http.MethodGet, hs.h.HandleListAppSets, "/appsets", nil, ""),
	} {
		if rr.Code != http.StatusBadGateway {
			t.Errorf("%s: status %d, want 502: %s", name, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "10.20.30.40") {
			t.Errorf("%s: response leaks the remote address: %s", name, rr.Body.String())
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_StatusFollowsTheRemotesDiscovery(t *testing.T) {
	hs := newHarness(t, toolLists(true, false))
	st := decode[GitOpsStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, "/status", nil, ""))
	if st.Detected != ToolArgoCD || st.Reason != "" || st.ArgoCD == nil || !st.ArgoCD.AppSetsAvailable {
		t.Errorf("status = %+v, want argocd with appsets and no reason", st)
	}

	none := newHarness(t, toolLists(false, false))
	st = decode[GitOpsStatus](t, do(t, remoteCluster, http.MethodGet, none.h.HandleStatus, "/status", nil, ""))
	if st.Detected != ToolNone || st.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("status = %+v, want none with discovery_missing", st)
	}
	if rr := do(t, remoteCluster, http.MethodGet, none.h.HandleListApplications, "/applications", nil, ""); rr.Code != http.StatusOK || len(decode[appList](t, rr).Applications) != 0 {
		t.Errorf("no-gitops remote must list nothing, got %d %s", rr.Code, rr.Body.String())
	}

	down := newHarness(t, toolLists(true, false))
	down.clients.targetErr = unreachable()
	st = decode[GitOpsStatus](t, do(t, remoteCluster, http.MethodGet, down.h.HandleStatus, "/status", nil, ""))
	if st.Detected != ToolNone || st.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("status = %+v, want none with unreachable", st)
	}
}

func TestRemote_AppSetsListWithRemoteChildren(t *testing.T) {
	hs := newHarness(t, toolLists(true, false),
		obj("ApplicationSet", "argocd", "remote-set", nil, map[string]any{}),
		obj("Application", "argocd", "child", map[string]string{"argocd.argoproj.io/application-set-name": "remote-set"}, map[string]any{}))

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListAppSets, "/appsets", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[struct {
		ApplicationSets []NormalizedAppSet `json:"applicationSets"`
	}](t, rr)
	if len(got.ApplicationSets) != 1 || got.ApplicationSets[0].Name != "remote-set" || got.ApplicationSets[0].GeneratedAppCount != 1 {
		t.Errorf("appsets = %+v, want remote-set with one child", got.ApplicationSets)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

// The local cluster keeps its service-account cache and discoverer; actions
// go through ClusterClients and carry the local cluster id.
func TestLocal_ListAndSyncStayOnTheLocalCluster(t *testing.T) {
	hs := newHarness(t, toolLists(true, false))
	local := hs.clients.clusters["local"]
	hs.h.Discoverer = &GitOpsDiscoverer{status: &GitOpsStatus{Detected: ToolArgoCD, ArgoCD: &ToolDetail{Available: true}}}
	hs.h.baseDynOverride = local.dyn

	rr := do(t, "local", http.MethodGet, hs.h.HandleListApplications, "/applications", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if got := decode[appList](t, rr); len(got.Applications) != 1 || got.Applications[0].Name != "local-app" || got.Coverage != nil {
		t.Errorf("list = %+v, want only local-app and no coverage", got)
	}

	rr = do(t, "local", http.MethodPost, hs.h.HandleSync, "/sync", map[string]string{"id": "argo:argocd:local-app"}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("sync status %d: %s", rr.Code, rr.Body.String())
	}
	if countVerb(local.dyn, "patch", "applications") != 1 {
		t.Error("local sync did not patch the local application")
	}
	if e := hs.audit.last(t); e.ClusterID != "local" {
		t.Errorf("audit cluster = %q, want local", e.ClusterID)
	}
	if len(hs.remoteDyn().Actions()) != 0 {
		t.Error("local requests touched the remote cluster")
	}
}
