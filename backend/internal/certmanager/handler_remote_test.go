package certmanager

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
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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

// Remote-cluster tests (#531). cert-manager presence is decided on the
// selected cluster through k8s.Presence, never by the local Discoverer, and
// remote lists are cached per (cluster, identity) through remotecache.

const remoteCluster = "remote-1"

// remoteHost is the address a remote transport error names. No response
// may carry it.
const remoteHost = "10.20.30.40"

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

var certManagerListKinds = map[schema.GroupVersionResource]string{
	CertificateGVR:        "CertificateList",
	IssuerGVR:             "IssuerList",
	ClusterIssuerGVR:      "ClusterIssuerList",
	CertificateRequestGVR: "CertificateRequestList",
	OrderGVR:              "OrderList",
	ChallengeGVR:          "ChallengeList",
}

// certManagerLists is discovery for a cluster that does or does not serve
// cert-manager.
func certManagerLists(installed bool) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}}}}
	if installed {
		lists = append(lists, &metav1.APIResourceList{GroupVersion: "cert-manager.io/v1", APIResources: []metav1.APIResource{
			{Name: "certificates", Kind: "Certificate", Namespaced: true},
			{Name: "issuers", Kind: "Issuer", Namespaced: true},
			{Name: "clusterissuers", Kind: "ClusterIssuer", Namespaced: false},
			{Name: "certificaterequests", Kind: "CertificateRequest", Namespaced: true},
		}})
	}
	return lists
}

func newFakeCluster(installed bool, objs ...runtime.Object) *fakeCluster {
	return &fakeCluster{
		disc: &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: certManagerLists(installed)}},
		dyn:  dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), certManagerListKinds, objs...),
	}
}

// expiringCert is a Certificate that expires in days days.
func expiringCert(namespace, name string, days int) *unstructured.Unstructured {
	u := newUnstructuredCert(namespace, name, "remote-issuer")
	u.Object["status"] = map[string]any{
		"notAfter": time.Now().Add(time.Duration(days)*24*time.Hour + time.Hour).UTC().Format(time.RFC3339),
	}
	return u
}

// newRemoteHandler builds a Handler whose remote cluster is served by
// remote. The local cluster is a seeded cache plus a Discoverer reporting
// localDetected; K8sClient is nil, so any service-account read on the
// remote path panics.
func newRemoteHandler(t *testing.T, localDetected bool, remote *fakeCluster) (*Handler, *fakeClients) {
	t.Helper()
	clients := &fakeClients{clusters: map[string]*fakeCluster{remoteCluster: remote}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHandler(nil, clients, k8s.NewPresence(clients), seededDiscoverer(localDetected),
		resources.NewAlwaysAllowAccessChecker(), audit.NewSlogLogger(logger), nil, logger)
	h.cache = &cachedData{
		certificates:   []Certificate{{Name: "local-cert", Namespace: "local-ns"}},
		issuers:        []Issuer{{Name: "local-issuer", Namespace: "local-ns"}},
		clusterIssuers: []Issuer{{Name: "local-clusterissuer"}},
		fetchedAt:      time.Now(),
	}
	return h, clients
}

func seededDiscoverer(detected bool) *Discoverer {
	d := NewDiscoverer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.status = CertManagerStatus{Detected: detected, LastChecked: time.Now().UTC()}
	return d
}

var (
	alice = &auth.User{Username: "alice", KubernetesUsername: "alice", KubernetesGroups: []string{"admins"}, Roles: []string{"admin"}}
	bob   = &auth.User{Username: "bob", KubernetesUsername: "bob", KubernetesGroups: []string{"admins"}, Roles: []string{"admin"}}
)

func doAs(t *testing.T, user *auth.User, clusterID, method string, h http.HandlerFunc, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/certificates", nil)
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

func do(t *testing.T, clusterID string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	return doAs(t, alice, clusterID, http.MethodGet, h, nil)
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

func countLists(dyn *dynfake.FakeDynamicClient, resource string) int {
	n := 0
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

func unreachable() error {
	return &url.Error{Op: "Get", URL: "https://" + remoteHost + ":6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

// The reported bug: cert-manager on the remote, not on the local cluster.
// Every list must come from the remote, and status must say detected.
func TestRemote_InstalledRemoteServedWhenLocalLacksCertManager(t *testing.T) {
	h, _ := newRemoteHandler(t, false, newFakeCluster(true,
		expiringCert("remote-ns", "remote-cert", 3),
		newUnstructuredIssuer("remote-ns", "remote-issuer", false),
		newUnstructuredIssuer("", "remote-clusterissuer", true),
	))

	rr := do(t, remoteCluster, h.HandleListCertificates)
	if rr.Code != http.StatusOK {
		t.Fatalf("certificates status %d: %s", rr.Code, rr.Body.String())
	}
	if certs := decode[[]Certificate](t, rr); len(certs) != 1 || certs[0].Name != "remote-cert" {
		t.Errorf("certificates = %+v, want only remote-cert", certs)
	}

	rr = do(t, remoteCluster, h.HandleListIssuers)
	if issuers := decode[[]Issuer](t, rr); rr.Code != http.StatusOK || len(issuers) != 1 || issuers[0].Name != "remote-issuer" {
		t.Errorf("issuers: status %d, got %+v, want only remote-issuer", rr.Code, issuers)
	}

	rr = do(t, remoteCluster, h.HandleListClusterIssuers)
	if cis := decode[[]Issuer](t, rr); rr.Code != http.StatusOK || len(cis) != 1 || cis[0].Name != "remote-clusterissuer" {
		t.Errorf("clusterissuers: status %d, got %+v, want only remote-clusterissuer", rr.Code, cis)
	}

	rr = do(t, remoteCluster, h.HandleListExpiring)
	if exp := decode[[]ExpiringCertificate](t, rr); rr.Code != http.StatusOK || len(exp) != 1 || exp[0].Name != "remote-cert" {
		t.Errorf("expiring: status %d, got %+v, want only remote-cert", rr.Code, exp)
	}

	st := decode[CertManagerStatus](t, do(t, remoteCluster, h.HandleStatus))
	if !st.Detected || st.Reason != "" {
		t.Errorf("status = %+v, want detected with no reason", st)
	}
}

// The reverse case: cert-manager on the local cluster only. The remote is
// not asked for kinds it does not serve, the local cache never leaks into
// the response, and status reports discovery_missing.
func TestRemote_AbsentRemoteIsEmptyWhenLocalHasCertManager(t *testing.T) {
	remote := newFakeCluster(false)
	h, _ := newRemoteHandler(t, true, remote)

	for name, handler := range map[string]http.HandlerFunc{
		"certificates":   h.HandleListCertificates,
		"issuers":        h.HandleListIssuers,
		"clusterissuers": h.HandleListClusterIssuers,
		"expiring":       h.HandleListExpiring,
	} {
		rr := do(t, remoteCluster, handler)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status %d: %s", name, rr.Code, rr.Body.String())
		}
		if got := decode[[]json.RawMessage](t, rr); len(got) != 0 {
			t.Errorf("%s = %s, want empty", name, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "local-") {
			t.Errorf("%s leaked local data: %s", name, rr.Body.String())
		}
	}
	if n := len(remote.dyn.Actions()); n != 0 {
		t.Errorf("remote recorded %d dynamic actions, want 0 (cert-manager is not served there)", n)
	}

	st := decode[CertManagerStatus](t, do(t, remoteCluster, h.HandleStatus))
	if st.Detected || st.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("status = %+v, want not detected with discovery_missing", st)
	}
}

// Remote lists are cached per identity (R-8 KTD2): a repeat by the same
// identity is served from cache, a second identity fetches its own view.
func TestRemote_CacheIsKeyedPerIdentity(t *testing.T) {
	remote := newFakeCluster(true, newUnstructuredCert("remote-ns", "remote-cert", "remote-issuer"))
	h, _ := newRemoteHandler(t, false, remote)

	doAs(t, alice, remoteCluster, http.MethodGet, h.HandleListCertificates, nil)
	doAs(t, alice, remoteCluster, http.MethodGet, h.HandleListIssuers, nil)
	if n := countLists(remote.dyn, "certificates"); n != 1 {
		t.Fatalf("after two reads by one identity: %d certificate lists, want 1", n)
	}

	doAs(t, bob, remoteCluster, http.MethodGet, h.HandleListCertificates, nil)
	if n := countLists(remote.dyn, "certificates"); n != 2 {
		t.Errorf("after a second identity's read: %d certificate lists, want 2", n)
	}

	h.EvictRemoteCache(remoteCluster)
	doAs(t, alice, remoteCluster, http.MethodGet, h.HandleListCertificates, nil)
	if n := countLists(remote.dyn, "certificates"); n != 3 {
		t.Errorf("after EvictRemoteCache: %d certificate lists, want 3", n)
	}
}

// An unreachable remote answers the lists through the KTD4 writer (502
// unreachable, no address) and the status route with a reason (KTD5).
func TestRemote_UnreachableReturnsClassifiedError(t *testing.T) {
	h, clients := newRemoteHandler(t, true, newFakeCluster(true))
	clients.targetErr = unreachable()

	for name, handler := range map[string]http.HandlerFunc{
		"certificates":   h.HandleListCertificates,
		"issuers":        h.HandleListIssuers,
		"clusterissuers": h.HandleListClusterIssuers,
		"expiring":       h.HandleListExpiring,
	} {
		rr := do(t, remoteCluster, handler)
		if rr.Code != http.StatusBadGateway {
			t.Errorf("%s status %d, want 502: %s", name, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"reason":"unreachable"`) {
			t.Errorf("%s body lacks reason unreachable: %s", name, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), remoteHost) || strings.Contains(rr.Body.String(), "local-") {
			t.Errorf("%s leaked the remote address or local data: %s", name, rr.Body.String())
		}
	}

	rr := do(t, remoteCluster, h.HandleStatus)
	st := decode[CertManagerStatus](t, rr)
	if rr.Code != http.StatusOK || st.Detected || st.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("status: code %d, body %+v, want 200 not detected with reason unreachable", rr.Code, st)
	}
}

// A renew on a remote cluster drops every identity's cached view of it
// (KTD7), so the next list re-reads the cluster.
func TestRemote_RenewEvictsRemoteCache(t *testing.T) {
	remote := newFakeCluster(true, newUnstructuredCert("remote-ns", "remote-cert", "remote-issuer"))
	h, _ := newRemoteHandler(t, false, remote)

	doAs(t, alice, remoteCluster, http.MethodGet, h.HandleListCertificates, nil)
	rr := doAs(t, alice, remoteCluster, http.MethodPost, h.HandleRenew, map[string]string{"namespace": "remote-ns", "name": "remote-cert"})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("renew status %d: %s", rr.Code, rr.Body.String())
	}
	doAs(t, alice, remoteCluster, http.MethodGet, h.HandleListCertificates, nil)
	if n := countLists(remote.dyn, "certificates"); n != 2 {
		t.Errorf("certificate lists = %d, want 2 (the renew must evict the cached view)", n)
	}
}

// Local-path control: with X-Cluster-ID="local", the seeded cache wins and
// the status route carries no reason.
func TestHandleListCertificates_LocalClusterUsesCache(t *testing.T) {
	h, _ := newRemoteHandler(t, true, newFakeCluster(true))

	rr := do(t, "local", h.HandleListCertificates)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if certs := decode[[]Certificate](t, rr); len(certs) != 1 || certs[0].Name != "local-cert" {
		t.Errorf("got %+v, want one cert named local-cert", certs)
	}

	st := decode[CertManagerStatus](t, do(t, "local", h.HandleStatus))
	if !st.Detected || st.Reason != "" {
		t.Errorf("local status = %+v, want detected with no reason", st)
	}
}

// Local-path control: a local cluster without cert-manager keeps its empty
// lists, whatever the remote serves.
func TestHandleListCertificates_LocalWithoutCertManagerIsEmpty(t *testing.T) {
	h, _ := newRemoteHandler(t, false, newFakeCluster(true))

	rr := do(t, "local", h.HandleListCertificates)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if certs := decode[[]Certificate](t, rr); len(certs) != 0 {
		t.Errorf("got %+v, want none", certs)
	}
}

// --- Helpers --------------------------------------------------------------

func testUser() *auth.User {
	return &auth.User{
		ID:                 "test-user",
		Username:           "alice",
		KubernetesUsername: "alice",
		KubernetesGroups:   []string{"dev"},
		Roles:              []string{"viewer"},
	}
}

func newAvailableDiscoverer() *Discoverer {
	return seededDiscoverer(true)
}

func newUnstructuredCert(namespace, name, issuerName string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
	u.SetNamespace(namespace)
	u.SetName(name)
	u.Object["spec"] = map[string]any{
		"secretName": name + "-tls",
		"issuerRef": map[string]any{
			"name": issuerName,
			"kind": "Issuer",
		},
	}
	return u
}

func newUnstructuredIssuer(namespace, name string, cluster bool) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	kind := "Issuer"
	if cluster {
		kind = "ClusterIssuer"
	}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    kind,
	})
	if !cluster {
		u.SetNamespace(namespace)
	}
	u.SetName(name)
	u.Object["spec"] = map[string]any{}
	return u
}
