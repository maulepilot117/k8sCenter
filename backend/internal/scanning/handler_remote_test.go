package scanning

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

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const (
	remoteCluster = "remote-1"
	localCluster  = "local"
	// remoteHost is the address a remote cluster's transport errors name. No
	// response may carry it.
	remoteHost = "10.20.30.40"
	// leakMarker is text a remote API server puts in an error. No response
	// may carry it.
	leakMarker = "remote-internal-detail-7f3a"
)

// fakeCluster is one fake cluster: its discovery, typed and dynamic client.
type fakeCluster struct {
	disc *fakediscovery.FakeDiscovery
	kube *kfake.Clientset
	dyn  *dynfake.FakeDynamicClient
}

// clientCall records one client resolution: which cluster, as whom.
type clientCall struct {
	cluster, username string
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. It
// records every dynamic client resolution so a test can assert the identity
// a read impersonated. A target error makes every resolution fail.
type fakeClients struct {
	clusters  map[string]*fakeCluster
	targetErr error

	mu       sync.Mutex
	dynCalls []clientCall
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
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.kube, nil
}

func (f *fakeClients) DynamicClientForCluster(_ context.Context, id, username string, _ []string) (dynamic.Interface, error) {
	f.mu.Lock()
	f.dynCalls = append(f.dynCalls, clientCall{cluster: k8s.NormalizedClusterID(id), username: username})
	f.mu.Unlock()
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

func (f *fakeClients) calls() []clientCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]clientCall(nil), f.dynCalls...)
}

var scanListKinds = map[schema.GroupVersionResource]string{
	trivyVulnReportGVR:      "VulnerabilityReportList",
	kubescapeVulnSummaryGVR: "VulnerabilitySummaryList",
}

// scannerDiscovery is discovery for a cluster serving the named scanners'
// report CRDs.
func scannerDiscovery(trivy, kubescape bool) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}}}}
	if trivy {
		lists = append(lists, &metav1.APIResourceList{GroupVersion: "aquasecurity.github.io/v1alpha1", APIResources: []metav1.APIResource{
			{Name: "vulnerabilityreports", Kind: "VulnerabilityReport", Namespaced: true},
		}})
	}
	if kubescape {
		lists = append(lists, &metav1.APIResourceList{GroupVersion: "spdx.softwarecomposition.org/v1beta1", APIResources: []metav1.APIResource{
			{Name: "vulnerabilitysummaries", Kind: "VulnerabilitySummary", Namespaced: true},
		}})
	}
	return lists
}

// trivyReport is a VulnerabilityReport for one container of a workload,
// carrying one critical CVE.
func trivyReport(ns, kind, name, cve string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "aquasecurity.github.io/v1alpha1",
		"kind":       "VulnerabilityReport",
		"metadata": map[string]any{
			"name":      strings.ToLower(kind) + "-" + name + "-app",
			"namespace": ns,
			"labels": map[string]any{
				"trivy-operator.resource.kind":      kind,
				"trivy-operator.resource.name":      name,
				"trivy-operator.resource.namespace": ns,
				"trivy-operator.container.name":     "app",
			},
		},
		"report": map[string]any{
			"artifact":        map[string]any{"repository": "registry/" + name, "tag": "1.0"},
			"updateTimestamp": "2026-10-01T00:00:00Z",
			"summary":         map[string]any{"criticalCount": int64(1), "highCount": int64(0), "mediumCount": int64(0), "lowCount": int64(0)},
			"vulnerabilities": []any{map[string]any{"vulnerabilityID": cve, "severity": "CRITICAL", "resource": "openssl"}},
		},
	}}
}

// kubescapeSummary is a VulnerabilitySummary for a workload.
func kubescapeSummary(ns, kind, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "spdx.softwarecomposition.org/v1beta1",
		"kind":       "VulnerabilitySummary",
		"metadata": map[string]any{
			"name":      strings.ToLower(kind) + "-" + name,
			"namespace": ns,
			"labels": map[string]any{
				"kubescape.io/workload-kind":      kind,
				"kubescape.io/workload-name":      name,
				"kubescape.io/workload-namespace": ns,
			},
		},
		"spec": map[string]any{"severities": map[string]any{"high": map[string]any{"all": int64(2)}}},
	}}
}

func newFakeCluster(t *testing.T, trivy, kubescape bool, objs ...*unstructured.Unstructured) *fakeCluster {
	t.Helper()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), scanListKinds)
	for _, o := range objs {
		gvr := trivyVulnReportGVR
		if o.GetKind() == "VulnerabilitySummary" {
			gvr = kubescapeVulnSummaryGVR
		}
		if err := dyn.Tracker().Create(gvr, o, o.GetNamespace()); err != nil {
			t.Fatalf("seed %s: %v", o.GetName(), err)
		}
	}
	return &fakeCluster{
		disc: &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: scannerDiscovery(trivy, kubescape)}},
		kube: kfake.NewSimpleClientset(),
		dyn:  dyn,
	}
}

type harness struct {
	h       *Handler
	clients *fakeClients
}

// newHarness builds a Handler over a remote cluster and a local one. The
// local cluster is seeded with its own reports and its Discoverer says both
// scanners are installed, so a local read or a local presence answer on the
// remote path shows up. K8sClient is nil, so any service-account read
// panics.
func newHarness(t *testing.T, remote *fakeCluster) *harness {
	t.Helper()
	clients := &fakeClients{clusters: map[string]*fakeCluster{
		remoteCluster: remote,
		localCluster: newFakeCluster(t, true, true,
			trivyReport("apps", "Deployment", "local-app", "CVE-LOCAL-1"),
			kubescapeSummary("apps", "Deployment", "local-app")),
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disc := NewDiscoverer(nil, logger)
	disc.status = &ScannerStatus{
		Detected:    ScannerBoth,
		Trivy:       &ScannerDetail{Available: true, Namespace: "trivy-system"},
		Kubescape:   &ScannerDetail{Available: true, Namespace: "kubescape"},
		LastChecked: "2026-10-01T00:00:00Z",
	}
	h := &Handler{
		Discoverer:    disc,
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        logger,
		Clients:       clients,
		Presence:      k8s.NewPresence(clients),
	}
	h.InitCache()
	return &harness{h: h, clients: clients}
}

func (hs *harness) localActions() int {
	local := hs.clients.clusters[localCluster]
	return len(local.dyn.Actions()) + len(local.kube.Actions())
}

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

var (
	devUser   = &auth.User{Username: "dev", KubernetesUsername: "dev-k8s", KubernetesGroups: []string{"devs"}, Roles: []string{"viewer"}}
	adminUser = &auth.User{Username: "admin", KubernetesUsername: "admin-k8s", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}}
)

func doAs(t *testing.T, user *auth.User, clusterID string, h http.HandlerFunc, target string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, user)
	ctx = middleware.WithClusterID(ctx, clusterID)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

const listTarget = "/scanning/vulnerabilities?namespace=apps"

var detailParams = map[string]string{"namespace": "apps", "kind": "Deployment", "name": "remote-app"}

type listBody struct {
	Data struct {
		Vulnerabilities []WorkloadVulnSummary `json:"vulnerabilities"`
		Summary         VulnListMetadata      `json:"summary"`
	} `json:"data"`
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return v
}

func workloadNames(vulns []WorkloadVulnSummary) []string {
	names := make([]string, 0, len(vulns))
	for _, v := range vulns {
		names = append(names, string(v.Scanner)+"/"+v.Name)
	}
	return names
}

func unreachable() error {
	return &url.Error{Op: "Get", URL: "https://" + remoteHost + ":6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

func TestRemote_StatusDetectsTheRemoteScanners(t *testing.T) {
	hs := newHarness(t, newFakeCluster(t, true, false))

	for _, user := range []*auth.User{adminUser, devUser} {
		rr := doAs(t, user, remoteCluster, hs.h.HandleStatus, "/scanning/status", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", user.Username, rr.Code, rr.Body.String())
		}
		got := decode[struct{ Data ScannerStatus }](t, rr).Data
		if got.Detected != ScannerTrivy || got.Trivy == nil || !got.Trivy.Available || got.Kubescape != nil {
			t.Errorf("%s: status = %+v, want trivy only (the remote's, not the local discoverer's both)", user.Username, got)
		}
		if got.Trivy != nil && got.Trivy.Namespace != "" {
			t.Errorf("%s: trivy namespace = %q, want blank: no namespace probe runs on a remote cluster", user.Username, got.Trivy.Namespace)
		}
		if got.LastChecked == "" {
			t.Errorf("%s: lastChecked is blank", user.Username)
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_VulnerabilitiesListTheRemoteReportsOnly(t *testing.T) {
	hs := newHarness(t, newFakeCluster(t, true, true,
		trivyReport("apps", "Deployment", "remote-app", "CVE-REMOTE-1"),
		kubescapeSummary("apps", "Deployment", "remote-app")))

	rr := doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilities, listTarget, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[listBody](t, rr).Data
	names := workloadNames(got.Vulnerabilities)
	if len(names) != 2 || !strings.Contains(strings.Join(names, ","), "trivy/remote-app") || !strings.Contains(strings.Join(names, ","), "kubescape/remote-app") {
		t.Errorf("workloads = %v, want trivy/remote-app and kubescape/remote-app only", names)
	}
	if got.Summary.Total != 2 || got.Summary.Severity.Critical != 1 || got.Summary.Severity.High != 2 {
		t.Errorf("summary = %+v, want total 2, 1 critical, 2 high", got.Summary)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestVulnerabilityReads_ImpersonateTheUserOnEveryCluster(t *testing.T) {
	hs := newHarness(t, newFakeCluster(t, true, true,
		trivyReport("apps", "Deployment", "remote-app", "CVE-REMOTE-1"),
		kubescapeSummary("apps", "Deployment", "remote-app")))

	for _, cluster := range []string{localCluster, remoteCluster} {
		name := "remote-app"
		if cluster == localCluster {
			name = "local-app"
		}
		if rr := doAs(t, devUser, cluster, hs.h.HandleVulnerabilities, listTarget, nil); rr.Code != http.StatusOK {
			t.Fatalf("%s list: status %d: %s", cluster, rr.Code, rr.Body.String())
		}
		params := map[string]string{"namespace": "apps", "kind": "Deployment", "name": name}
		rr := doAs(t, devUser, cluster, hs.h.HandleVulnerabilityDetail, "/scanning/vulnerabilities/apps/Deployment/"+name, params)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s detail: status %d: %s", cluster, rr.Code, rr.Body.String())
		}
		if got := decode[struct{ Data WorkloadVulnDetail }](t, rr).Data; len(got.Images) != 1 {
			t.Errorf("%s detail images = %+v, want the %s report", cluster, got.Images, name)
		}
	}

	calls := hs.clients.calls()
	seen := map[string]bool{}
	for _, c := range calls {
		seen[c.cluster] = true
		if c.username != devUser.KubernetesUsername {
			t.Errorf("read on %s impersonated %q, want %q", c.cluster, c.username, devUser.KubernetesUsername)
		}
	}
	if !seen[localCluster] || !seen[remoteCluster] {
		t.Errorf("client resolutions = %+v, want impersonated reads on both clusters", calls)
	}

	// Results are per identity: another user's request is not served from
	// the first user's cache.
	before := len(calls)
	if rr := doAs(t, adminUser, remoteCluster, hs.h.HandleVulnerabilities, listTarget, nil); rr.Code != http.StatusOK {
		t.Fatalf("admin list: status %d: %s", rr.Code, rr.Body.String())
	}
	after := hs.clients.calls()
	if len(after) == before || after[len(after)-1].username != adminUser.KubernetesUsername {
		t.Errorf("admin list did not read as the admin: calls %+v", after[before:])
	}
}

func TestRemote_ForbiddenListIs403WithoutTheClusterText(t *testing.T) {
	remote := newFakeCluster(t, true, false, trivyReport("apps", "Deployment", "remote-app", "CVE-REMOTE-1"))
	remote.dyn.PrependReactor("list", "vulnerabilityreports", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(trivyVulnReportGVR.GroupResource(), "", errors.New(leakMarker))
	})
	hs := newHarness(t, remote)

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list":   doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilities, listTarget, nil),
		"detail": doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilityDetail, "/", detailParams),
	} {
		body := rr.Body.String()
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d body %s, want 403", name, rr.Code, body)
		}
		if strings.Contains(body, leakMarker) || strings.Contains(body, "vulnerabilityreports") {
			t.Errorf("%s: body relays the cluster's error: %s", name, body)
		}
		var resp struct {
			Error struct {
				Detail string `json:"detail"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Error.Detail != "" {
			t.Errorf("%s: error detail = %q (%v), want empty", name, resp.Error.Detail, err)
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_TargetFailureIsATargetErrorAndNeverReadsLocal(t *testing.T) {
	hs := newHarness(t, newFakeCluster(t, true, true))
	hs.clients.targetErr = unreachable()

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"status": doAs(t, devUser, remoteCluster, hs.h.HandleStatus, "/scanning/status", nil),
		"list":   doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilities, listTarget, nil),
		"detail": doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilityDetail, "/", detailParams),
	} {
		body := rr.Body.String()
		if rr.Code != http.StatusBadGateway || !strings.Contains(body, `"reason":"unreachable"`) {
			t.Errorf("%s: status %d body %s, want 502 unreachable", name, rr.Code, body)
		}
		if strings.Contains(body, remoteHost) {
			t.Errorf("%s: body leaks the remote address: %s", name, body)
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
	for _, c := range hs.clients.calls() {
		if c.cluster != remoteCluster {
			t.Errorf("resolved a client for %s on a remote selection", c.cluster)
		}
	}
}

func TestRemote_DetailReadsTheRemoteReport(t *testing.T) {
	remote := newFakeCluster(t, true, false, trivyReport("apps", "Deployment", "remote-app", "CVE-REMOTE-1"))
	hs := newHarness(t, remote)
	// The local cluster holds the same workload with a different CVE.
	if err := hs.clients.clusters[localCluster].dyn.Tracker().Create(trivyVulnReportGVR,
		trivyReport("apps", "Deployment", "remote-app", "CVE-LOCAL-2"), "apps"); err != nil {
		t.Fatal(err)
	}

	rr := doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilityDetail, "/", detailParams)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[struct{ Data WorkloadVulnDetail }](t, rr).Data
	if len(got.Images) != 1 || len(got.Images[0].Vulnerabilities) != 1 || got.Images[0].Vulnerabilities[0].ID != "CVE-REMOTE-1" {
		t.Errorf("detail = %+v, want the remote report's CVE-REMOTE-1", got)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_ScannerAbsentOnRemoteIsSkipped(t *testing.T) {
	// Trivy only on the remote; the local discoverer says both.
	hs := newHarness(t, newFakeCluster(t, true, false, trivyReport("apps", "Deployment", "remote-app", "CVE-REMOTE-1")))

	rr := doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilities, listTarget, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if names := workloadNames(decode[listBody](t, rr).Data.Vulnerabilities); len(names) != 1 || names[0] != "trivy/remote-app" {
		t.Errorf("workloads = %v, want trivy/remote-app only", names)
	}
	if n := countVerb(hs.remoteDyn(), "list", "vulnerabilitysummaries"); n != 0 {
		t.Errorf("listed kubescape summaries %d times on a cluster that does not serve them", n)
	}

	// Kubescape only on the remote: CVE detail is a Trivy feature.
	hs = newHarness(t, newFakeCluster(t, false, true))
	rr = doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilityDetail, "/", detailParams)
	if rr.Code != http.StatusNotImplemented || !strings.Contains(rr.Body.String(), "requires Trivy Operator") {
		t.Errorf("detail without trivy: status %d body %s, want 501 requires Trivy Operator", rr.Code, rr.Body.String())
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_CRDRemovedAfterDiscoveryIsSkipped(t *testing.T) {
	remote := newFakeCluster(t, true, true, kubescapeSummary("apps", "Deployment", "remote-app"))
	remote.dyn.PrependReactor("list", "vulnerabilityreports", func(k8stesting.Action) (bool, runtime.Object, error) {
		remote.disc.Resources = scannerDiscovery(false, true)
		return true, nil, apierrors.NewNotFound(trivyVulnReportGVR.GroupResource(), "")
	})
	hs := newHarness(t, remote)

	rr := doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilities, listTarget, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if names := workloadNames(decode[listBody](t, rr).Data.Vulnerabilities); len(names) != 1 || names[0] != "kubescape/remote-app" {
		t.Errorf("workloads = %v, want kubescape/remote-app only", names)
	}
}

func TestRemote_EvictRemoteCacheDropsTheClusterEntries(t *testing.T) {
	remote := newFakeCluster(t, true, false, trivyReport("apps", "Deployment", "remote-app", "CVE-REMOTE-1"))
	hs := newHarness(t, remote)

	list := func() []string {
		rr := doAs(t, devUser, remoteCluster, hs.h.HandleVulnerabilities, listTarget, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
		return workloadNames(decode[listBody](t, rr).Data.Vulnerabilities)
	}
	list()
	if err := remote.dyn.Tracker().Create(trivyVulnReportGVR, trivyReport("apps", "StatefulSet", "db", "CVE-REMOTE-2"), "apps"); err != nil {
		t.Fatal(err)
	}
	if got := list(); len(got) != 1 {
		t.Fatalf("second list = %v, want the cached single workload", got)
	}
	hs.h.EvictRemoteCache(remoteCluster)
	if got := list(); len(got) != 2 {
		t.Errorf("list after evict = %v, want both workloads re-read from the cluster", got)
	}
}
