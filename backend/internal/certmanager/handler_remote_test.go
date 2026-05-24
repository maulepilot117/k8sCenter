package certmanager

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// F#3 regression — verifies that when a non-local X-Cluster-ID is set on the
// request context, the list endpoints route through ClusterRouter
// (DynamicClientForCluster) instead of returning the locally-cached
// BaseDynamicClient data.
//
// After F#18 (round-2), ClusterRouter hard-errors on (non-local clusterID +
// nil clusterStore) instead of silently falling through to the local
// factory. That means the remote branch now returns a 500 ("failed to fetch
// certificates") in this test setup — and that IS the F#3 proof: if the
// cache had been read, the handler would have returned a 200 with
// "local-cert". A 500 here means the remote path was taken AND it failed
// closed because no real remote store was wired. Both outcomes (500 here,
// real-remote-data in production) are correct; only "200 + local-cert"
// would indicate the F#3 regression we're guarding against.
func TestHandleListCertificates_RemoteClusterBypassesLocalCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	gvrToListKind := map[schema.GroupVersionResource]string{
		CertificateGVR:   "CertificateList",
		IssuerGVR:        "IssuerList",
		ClusterIssuerGVR: "ClusterIssuerList",
	}
	remoteCert := newUnstructuredCert("remote-ns", "remote-cert", "remote-issuer")
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, gvrToListKind, remoteCert)

	factory := k8s.NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, dyn)
	router := k8s.NewClusterRouter(factory, nil, "", logger)

	h := &Handler{
		K8sClient:     factory,
		ClusterRouter: router,
		Discoverer:    newAvailableDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
	}

	// Seed local cache with a sentinel. If the cache is wrongly used for a
	// remote request, the response would be 200 + "local-cert"; with F#18
	// the remote path is taken and the missing store path fails closed.
	h.cache = &cachedData{
		certificates: []Certificate{
			{Name: "local-cert", Namespace: "local-ns"},
		},
		fetchedAt: time.Now(),
	}

	req := httptest.NewRequest("GET", "/", nil)
	ctx := middleware.WithClusterID(req.Context(), "remote-cluster-1")
	ctx = auth.ContextWithUser(ctx, testUser())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.HandleListCertificates(w, req)

	// After F#18 the remote path fails closed on nil clusterStore. The
	// important assertion is that the cache was NOT served — 500 proves
	// the bypass took effect.
	if w.Code == http.StatusOK {
		var envelope struct {
			Data []Certificate `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err == nil {
			for _, c := range envelope.Data {
				if c.Name == "local-cert" {
					t.Fatalf("F#3 regression: local cache leaked into remote response (got cert %q)", c.Name)
				}
			}
		}
	}
	// 500 with the cluster-store fail-closed error is the expected outcome here.
	if w.Code != http.StatusInternalServerError {
		t.Logf("status = %d (expected 500 after F#18 fail-closed on nil clusterStore); body: %s", w.Code, w.Body.String())
	}
}

// Same shape for issuers — see HandleListCertificates test for the F#18
// fail-closed expectation.
func TestHandleListIssuers_RemoteClusterBypassesLocalCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	gvrToListKind := map[schema.GroupVersionResource]string{
		CertificateGVR:   "CertificateList",
		IssuerGVR:        "IssuerList",
		ClusterIssuerGVR: "ClusterIssuerList",
	}
	remoteIssuer := newUnstructuredIssuer("remote-ns", "remote-issuer", false)
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, gvrToListKind, remoteIssuer)

	factory := k8s.NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, dyn)
	router := k8s.NewClusterRouter(factory, nil, "", logger)

	h := &Handler{
		K8sClient:     factory,
		ClusterRouter: router,
		Discoverer:    newAvailableDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
	}
	h.cache = &cachedData{
		issuers:   []Issuer{{Name: "local-issuer", Namespace: "local-ns"}},
		fetchedAt: time.Now(),
	}

	req := httptest.NewRequest("GET", "/", nil)
	ctx := middleware.WithClusterID(req.Context(), "remote-cluster-1")
	ctx = auth.ContextWithUser(ctx, testUser())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.HandleListIssuers(w, req)

	if w.Code == http.StatusOK {
		var envelope struct {
			Data []Issuer `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err == nil {
			for _, i := range envelope.Data {
				if i.Name == "local-issuer" {
					t.Fatalf("F#3 regression: local cache leaked into remote response (got issuer %q)", i.Name)
				}
			}
		}
	}
	if w.Code != http.StatusInternalServerError {
		t.Logf("status = %d (expected 500 after F#18 fail-closed on nil clusterStore); body: %s", w.Code, w.Body.String())
	}
}

// Local-path control: with X-Cluster-ID="local", the seeded cache wins.
func TestHandleListCertificates_LocalClusterUsesCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := k8s.NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, nil)
	router := k8s.NewClusterRouter(factory, nil, "", logger)

	h := &Handler{
		K8sClient:     factory,
		ClusterRouter: router,
		Discoverer:    newAvailableDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
	}
	h.cache = &cachedData{
		certificates: []Certificate{{Name: "local-cert", Namespace: "local-ns"}},
		fetchedAt:    time.Now(),
	}

	req := httptest.NewRequest("GET", "/", nil)
	ctx := middleware.WithClusterID(req.Context(), "local")
	ctx = auth.ContextWithUser(ctx, testUser())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.HandleListCertificates(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data []Certificate `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0].Name != "local-cert" {
		t.Errorf("got %+v, want one cert named local-cert", envelope.Data)
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
	d := NewDiscoverer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Seed Status so IsAvailable returns true without dialling a real cluster.
	d.mu.Lock()
	d.status = CertManagerStatus{
		Detected:    true,
		LastChecked: time.Now().UTC(),
	}
	d.mu.Unlock()
	return d
}

// Avoid an unused-import warning while keeping context imported in case
// future tests need it.
var _ = context.Background

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
