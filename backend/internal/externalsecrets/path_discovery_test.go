package externalsecrets

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// makeKubernetesProviderStore builds a SecretStore configured for the
// kubernetes provider with the given source namespace.
// ESO v1 KubernetesProvider uses `remoteNamespace` at the provider-spec level.
func makeKubernetesProviderStore(ns, name, sourceNS string) *unstructured.Unstructured {
	provider := map[string]any{}
	if sourceNS != "" {
		provider["remoteNamespace"] = sourceNS
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "SecretStore",
			"metadata":   map[string]any{"name": name, "namespace": ns, "uid": "uid-store"},
			"spec":       map[string]any{"provider": map[string]any{"kubernetes": provider}},
			"status": map[string]any{
				"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			},
		},
	}
}

// pathDiscoveryHandler wires the typed and dynamic fake clients for tests.
func pathDiscoveryHandler(esObjs []runtime.Object, secrets ...runtime.Object) *Handler {
	dynFake := newEsoFakeDynClient(esObjs...)
	var typedFake kubernetes.Interface = kubefake.NewClientset(secrets...)
	return &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
		clientForUserOverride: func(string, []string) (kubernetes.Interface, error) {
			return typedFake, nil
		},
	}
}

func decodePathDiscovery(t *testing.T, w *httptest.ResponseRecorder) pathDiscoveryResponse {
	t.Helper()
	var env struct {
		Data pathDiscoveryResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	return env.Data
}

func TestHandleListPaths_KubernetesProvider(t *testing.T) {
	store := makeKubernetesProviderStore("apps", "k8s-store", "source")
	secrets := []runtime.Object{
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "source"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "source"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "alphabet", Namespace: "source"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "elsewhere"}},
	}
	h := pathDiscoveryHandler([]runtime.Object{store}, secrets...)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/?prefix=alph", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "k8s-store"})
	h.HandleListPaths(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	got := decodePathDiscovery(t, w)
	if !got.Supported || got.Provider != "kubernetes" {
		t.Errorf("expected supported kubernetes provider, got %+v", got)
	}
	if len(got.Paths) != 2 {
		t.Errorf("expected 2 paths matching prefix 'alph', got %d: %v", len(got.Paths), got.Paths)
	}
	// Sorted output
	if got.Paths[0] != "alpha" || got.Paths[1] != "alphabet" {
		t.Errorf("expected sorted paths, got %v", got.Paths)
	}
}

func TestHandleListPaths_KubernetesProvider_DefaultsToStoreNamespace(t *testing.T) {
	// Store has no remoteRef.namespace set — falls back to store's own namespace.
	store := makeKubernetesProviderStore("apps", "k8s-store", "")
	secrets := []runtime.Object{
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "in-apps", Namespace: "apps"}},
	}
	h := pathDiscoveryHandler([]runtime.Object{store}, secrets...)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "k8s-store"})
	h.HandleListPaths(w, r)

	got := decodePathDiscovery(t, w)
	if len(got.Paths) != 1 || got.Paths[0] != "in-apps" {
		t.Errorf("expected fallback to store namespace, got %+v", got)
	}
}

func TestHandleListPaths_NonKubernetesProvider(t *testing.T) {
	// Vault provider — wizard should fall through to free-text.
	store := makeStore("apps", "vault-store", "uid-vault")
	h := pathDiscoveryHandler([]runtime.Object{store})

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault-store"})
	h.HandleListPaths(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	got := decodePathDiscovery(t, w)
	if got.Supported {
		t.Errorf("expected supported=false for vault provider, got %+v", got)
	}
	if got.Provider != "vault" {
		t.Errorf("expected provider echo 'vault', got %q", got.Provider)
	}
}

func TestHandleListPaths_StoreNotFound(t *testing.T) {
	h := pathDiscoveryHandler(nil)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "missing"})
	h.HandleListPaths(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleListPaths_RBACDeniedOnStore(t *testing.T) {
	store := makeKubernetesProviderStore("apps", "k8s-store", "source")
	h := pathDiscoveryHandler([]runtime.Object{store})
	h.AccessChecker = resources.NewAlwaysDenyAccessChecker()

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "k8s-store"})
	h.HandleListPaths(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
}

// TestHandleListPaths_PartialRBACDeny verifies that when the user can read the
// SecretStore (get secretstores) but is denied listing Secrets, the endpoint
// returns HTTP 200 with supported=true and an empty path list — the UI degrades
// to free-text rather than surfacing a 403.
func TestHandleListPaths_PartialRBACDeny(t *testing.T) {
	store := makeKubernetesProviderStore("apps", "k8s-store", "source")
	h := pathDiscoveryHandler([]runtime.Object{store})
	// Allow `get secretstores` but deny `list secrets` (and everything else).
	h.AccessChecker = resources.NewPredicateAccessChecker(func(verb, _, resource, _ string) bool {
		return verb == "get" && resource == "secretstores"
	})

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "k8s-store"})
	h.HandleListPaths(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	got := decodePathDiscovery(t, w)
	if !got.Supported {
		t.Error("expected supported=true (kubernetes provider recognised)")
	}
	if len(got.Paths) != 0 {
		t.Errorf("expected empty paths on RBAC deny, got %v", got.Paths)
	}
}

// TestHandleListPaths_LimitCap verifies that when the source namespace contains
// more than pathDiscoveryLimit Secrets the response is capped and Truncated=true.
func TestHandleListPaths_LimitCap(t *testing.T) {
	store := makeKubernetesProviderStore("apps", "k8s-store", "source")
	secrets := make([]runtime.Object, pathDiscoveryLimit+5)
	for i := range secrets {
		secrets[i] = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("secret-%04d", i),
				Namespace: "source",
			},
		}
	}
	h := pathDiscoveryHandler([]runtime.Object{store}, secrets...)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "k8s-store"})
	h.HandleListPaths(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	got := decodePathDiscovery(t, w)
	if len(got.Paths) != pathDiscoveryLimit {
		t.Errorf("expected %d paths (cap), got %d", pathDiscoveryLimit, len(got.Paths))
	}
	if !got.Truncated {
		t.Error("expected truncated=true when result exceeds cap")
	}
}

func TestKubernetesProviderSourceNamespace(t *testing.T) {
	// ESO v1 KubernetesProvider only supports `remoteNamespace` at the
	// provider-spec level. The `remoteRef` key lives on ExternalSecret data
	// items, not on the SecretStore provider config.
	cases := []struct {
		name string
		spec map[string]any
		want string
	}{
		{"nil", nil, ""},
		{"empty", map[string]any{}, ""},
		{
			"remoteNamespace",
			map[string]any{"remoteNamespace": "apps"},
			"apps",
		},
		{
			"remoteRef.namespace ignored (not a valid KubernetesProvider field)",
			map[string]any{"remoteRef": map[string]any{"namespace": "apps"}},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kubernetesProviderSourceNamespace(c.spec); got != c.want {
				t.Errorf("got %q; want %q", got, c.want)
			}
		})
	}
}
