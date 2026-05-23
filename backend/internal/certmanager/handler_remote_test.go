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
// BaseDynamicClient data. Without the fix the cached "local cluster"
// certificates would leak into responses scoped to a remote cluster.
//
// Strategy: seed the in-process cache with one "local" cert, and arm the
// ClusterRouter's local factory (the only factory available to
// NewClusterRouter when clusterStore is nil) with a fake dynamic client
// holding a DIFFERENT cert. The local-path branch returns the cache entry;
// the remote-path branch returns the dynamic-client entry. We assert which
// one came back to know which branch executed.
//
// We intentionally use clusterStore=nil so ClusterRouter.DynamicClientForCluster
// falls through to the local factory's testDynOverride. Even with the
// fall-through, the gate inside HandleListCertificates routes by clusterID
// (not by whether the store is configured), so the remote branch executes.
func TestHandleListCertificates_RemoteClusterBypassesLocalCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Build a fake dynamic client armed with a remote-only certificate.
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

	// Seed the local cache with a different cert. If the handler reads the cache,
	// the response will carry "local-cert"; if it routes via the remote fetch,
	// the response will carry "remote-cert".
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

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Data []Certificate `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("got %d certs, want 1; body: %s", len(envelope.Data), w.Body.String())
	}
	if got := envelope.Data[0].Name; got != "remote-cert" {
		t.Errorf("certificate name = %q, want %q (cache leaked into remote response — F#3 regression)", got, "remote-cert")
	}
}

// Same shape for issuers — ensures the cache-bypass also applies to the
// issuers endpoint.
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

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data []Issuer `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0].Name != "remote-issuer" {
		t.Errorf("got %+v, want one issuer named remote-issuer (cache leaked into remote response — F#3 regression)", envelope.Data)
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
