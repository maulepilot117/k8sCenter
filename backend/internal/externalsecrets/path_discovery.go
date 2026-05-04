package externalsecrets

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/httputil"
)

// pathDiscoveryLimit caps the number of paths returned in a single response.
// Tuned for typeahead UX: enough to be useful, small enough to keep the JSON
// payload tight and the user's eye scannable.
const pathDiscoveryLimit = 200

// pathDiscoveryResponse is the shape returned by the path-discovery endpoint.
//
//   - Supported=false → frontend renders a free-text path field.
//   - Paths may be nil (RBAC denied, non-Kubernetes provider) or an empty slice
//     (namespace is empty). Callers must treat nil and [] identically.
//   - Truncated=true means the result was capped at pathDiscoveryLimit; the
//     complete list is larger. The user should narrow the prefix.
//
// Provider echoes the resolved provider name (e.g. "kubernetes", "vault") so
// the UI can tailor helper text.
type pathDiscoveryResponse struct {
	Supported bool     `json:"supported"`
	Provider  string   `json:"provider,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

// HandleListPaths discovers candidate remote-key paths for a SecretStore in
// Phase G's ExternalSecret wizard. Kubernetes-provider stores list Secrets in
// the configured source namespace via the impersonating client; the typeahead
// shows that namespace's Secret names, prefix-filtered. All other providers
// return `{supported: false}` — k8sCenter never holds source-store
// credentials, so authenticating against Vault/AWS/GCP/Azure to enumerate
// paths is out of scope.
//
//	GET /externalsecrets/stores/{namespace}/{name}/paths?prefix=<>
//
// The Kubernetes provider's source namespace is read from
// `spec.provider.kubernetes.remoteNamespace` (v1 schema). Empty / missing →
// caller's namespace per ESO defaults.
func (h *Handler) HandleListPaths(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
		return
	}

	storeNS := chi.URLParam(r, "namespace")
	storeName := chi.URLParam(r, "name")

	// RBAC: the user must be allowed to read the SecretStore itself before we
	// reveal its provider config or proxy a Secret list against it.
	if !h.canAccess(r.Context(), user, "get", "secretstores", storeNS) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	obj, err := dynClient.Resource(SecretStoreGVR).Namespace(storeNS).Get(r.Context(), storeName, metav1.GetOptions{})
	if err != nil {
		switch {
		case apierrors.IsForbidden(err):
			httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		case apierrors.IsNotFound(err):
			httputil.WriteError(w, http.StatusNotFound, "store not found", "")
		default:
			h.Logger.Error("get store for path discovery", "namespace", storeNS, "name", storeName, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch store", "")
		}
		return
	}

	spec, _ := obj.Object["spec"].(map[string]any)
	provider, providerSpec := detectProvider(spec)

	// Non-Kubernetes providers fall through to free-text input on the
	// frontend. We still echo the provider name so the UI can tailor helper
	// text (e.g. "Vault path: secret/data/myapp").
	if provider != "kubernetes" {
		httputil.WriteData(w, pathDiscoveryResponse{Supported: false, Provider: provider})
		return
	}

	sourceNS := kubernetesProviderSourceNamespace(providerSpec)
	if sourceNS == "" {
		// Kubernetes provider without a configured source namespace: ESO
		// reads the SecretStore's own namespace. Mirror that default.
		sourceNS = storeNS
	}

	// RBAC: user must be allowed to list Secrets in the source namespace.
	// AccessChecker keys on group/resource; "core/secrets" uses empty
	// group plus resource "secrets".
	can, accErr := h.AccessChecker.CanAccessGroupResource(
		r.Context(),
		user.KubernetesUsername,
		user.KubernetesGroups,
		"list",
		"",
		"secrets",
		sourceNS,
	)
	if accErr != nil || !can {
		httputil.WriteData(w, pathDiscoveryResponse{Supported: true, Provider: provider})
		return
	}

	// List Secrets via the impersonating typed client. The API server enforces
	// RBAC again — defense in depth against an AccessChecker stale read.
	kubeClient, err := h.clientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating typed client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Use a server-side Limit to avoid pulling huge lists across the wire.
	// The limit is set higher than pathDiscoveryLimit to give prefix-filtering
	// headroom. We do not page via Continue because we cap immediately after;
	// this is a typeahead, not an exhaustive enumeration.
	secrets, err := kubeClient.CoreV1().Secrets(sourceNS).List(r.Context(), metav1.ListOptions{Limit: 500})
	if err != nil {
		if apierrors.IsForbidden(err) {
			// Apiserver said no — surface as supported-but-empty so the UI
			// degrades to free-text rather than 403'ing the form.
			httputil.WriteData(w, pathDiscoveryResponse{Supported: true, Provider: provider})
			return
		}
		h.Logger.Error("list secrets for path discovery",
			"sourceNamespace", sourceNS, "store", storeName, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list paths", "")
		return
	}

	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	paths := make([]string, 0, len(secrets.Items))
	for _, s := range secrets.Items {
		if prefix != "" && !strings.HasPrefix(s.Name, prefix) {
			continue
		}
		paths = append(paths, s.Name)
	}

	// Sort first so truncation is deterministic (alphabetically earliest set).
	sort.Strings(paths)
	truncated := len(paths) > pathDiscoveryLimit
	if truncated {
		paths = paths[:pathDiscoveryLimit]
	}

	httputil.WriteData(w, pathDiscoveryResponse{
		Supported: true,
		Provider:  provider,
		Paths:     paths,
		Truncated: truncated,
	})
}

// kubernetesProviderSourceNamespace reads the source namespace from a
// kubernetes-provider spec block.
//
// ESO v1 KubernetesProvider uses `remoteNamespace` at the provider-spec level
// (`spec.provider.kubernetes.remoteNamespace`). The `remoteRef` key lives on
// individual ExternalSecret data items, not on the SecretStore provider config,
// so only `remoteNamespace` is read here. Empty when not present.
func kubernetesProviderSourceNamespace(providerSpec map[string]any) string {
	if providerSpec == nil {
		return ""
	}
	if ns, ok := providerSpec["remoteNamespace"].(string); ok && ns != "" {
		return ns
	}
	return ""
}
