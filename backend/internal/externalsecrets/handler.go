package externalsecrets

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
)

const (
	// cacheTTL caps how long the cached service-account-fetched snapshot
	// stays fresh. Matches Phase 11A. Annotation edits and CRD writes take
	// up to this long to surface in list responses.
	cacheTTL = 30 * time.Second

	// fetchTimeout bounds each fetchAll cycle. Five concurrent CRD lists
	// against a healthy API server complete in well under 10s; the timeout
	// catches a wedged etcd or a flapping CRD watch without hanging the
	// HTTP request indefinitely.
	fetchTimeout = 10 * time.Second
)

// Handler serves ESO observatory HTTP endpoints. Phase A surface is read-only
// (status + 5 list + 5 detail endpoints in U3). Write actions and the bulk
// refresh job model land in Phase E.
//
// Concurrency model:
//   - One in-flight fetchAll at a time per Handler (singleflight).
//   - One cache snapshot, replaced atomically when fetch completes.
//   - cacheGen guards against torn writes from concurrent invalidations.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	AuditLogger   audit.Logger
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cache      *cachedData
	cacheGen   uint64

	// dynOverride, when non-nil, replaces K8sClient.BaseDynamicClient() for
	// service-account list calls. Test-only seam — production wiring leaves
	// this nil.
	dynOverride dynamic.Interface

	// dynForUserOverride, when non-nil, replaces K8sClient.DynamicClientForUser
	// for impersonated CRD fetches in detail endpoints. Test-only seam.
	dynForUserOverride func(username string, groups []string) (dynamic.Interface, error)

	// clientForUserOverride, when non-nil, replaces K8sClient.ClientForUser
	// for impersonated typed client lookups (synced-Secret RV check).
	// Test-only seam.
	clientForUserOverride func(username string, groups []string) (kubernetes.Interface, error)
}

// dynClient returns the dynamic client to use for service-account-scoped
// list calls. Tests inject dynOverride; production reads BaseDynamicClient
// from the K8sClient factory.
func (h *Handler) dynClient() dynamic.Interface {
	if h.dynOverride != nil {
		return h.dynOverride
	}
	return h.K8sClient.BaseDynamicClient()
}

// dynForUser returns an impersonating dynamic client for the requesting user.
// Tests inject dynForUserOverride; production delegates to the K8sClient
// factory so RBAC is enforced by the API server itself.
func (h *Handler) dynForUser(username string, groups []string) (dynamic.Interface, error) {
	if h.dynForUserOverride != nil {
		return h.dynForUserOverride(username, groups)
	}
	return h.K8sClient.DynamicClientForUser(username, groups)
}

// clientForUser returns an impersonating typed client for the requesting
// user. Used by detail endpoints to look up the synced Secret's live
// resourceVersion for drift detection.
func (h *Handler) clientForUser(username string, groups []string) (kubernetes.Interface, error) {
	if h.clientForUserOverride != nil {
		return h.clientForUserOverride(username, groups)
	}
	return h.K8sClient.ClientForUser(username, groups)
}

// cachedData is the per-Handler snapshot. Built once per cacheTTL via
// fetchAll. Each slice carries the service-account view; per-user RBAC
// filtering happens at read time so the cache is shared across users.
type cachedData struct {
	externalSecrets        []ExternalSecret
	clusterExternalSecrets []ClusterExternalSecret
	stores                 []SecretStore
	clusterStores          []SecretStore
	pushSecrets            []PushSecret
	fetchedAt              time.Time
}

// NewHandler creates an ESO observatory handler. NotifService may be nil; cache
// invalidation events fire only when it's set (matches cert-manager precedent).
func NewHandler(
	k8sClient *k8s.ClientFactory,
	discoverer *Discoverer,
	accessChecker *resources.AccessChecker,
	auditLogger audit.Logger,
	notifService *notifications.NotificationService,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     k8sClient,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		AuditLogger:   auditLogger,
		NotifService:  notifService,
		Logger:        logger,
	}
}

// InvalidateCache forces the next read to re-fetch from the API server.
// Bumps cacheGen so an in-flight fetch from before invalidation cannot
// overwrite a fresh cache populated after.
//
// Phase A has no write actions, so this method is dormant until Phase E /
// Phase D wire the first cache-invalidating call sites. Exported now so the
// future wiring lands as a small additive change.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cacheGen++
	h.cache = nil
	h.cacheMu.Unlock()
}

// canAccess checks a single (verb, resource, namespace) tuple via the
// AccessChecker. Phase A only ever passes "list" / "get"; write verbs land
// in Phases D / E.
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx,
		user.KubernetesUsername,
		user.KubernetesGroups,
		verb,
		GroupName,
		resource,
		namespace,
	)
	return err == nil && can
}

// namespacedResource is implemented by types that carry a Kubernetes
// namespace. The two cluster-scoped kinds (ClusterExternalSecret,
// cluster-scope SecretStore) skip this filter and use a single
// CanAccessGroupResource call with empty namespace.
type namespacedResource interface {
	getNamespace() string
}

func (e ExternalSecret) getNamespace() string { return e.Namespace }
func (s SecretStore) getNamespace() string    { return s.Namespace }
func (p PushSecret) getNamespace() string     { return p.Namespace }

// filterByRBAC returns only items the user can access in their respective
// namespaces. Caches per-namespace allow decisions inside a single call so a
// list of 1000 ExternalSecrets across 10 namespaces issues 10
// SelfSubjectAccessReviews, not 1000.
func filterByRBAC[T namespacedResource](ctx context.Context, h *Handler, user *auth.User, resource string, items []T) []T {
	nsAllow := map[string]bool{}
	out := make([]T, 0, len(items))
	for _, item := range items {
		ns := item.getNamespace()
		allowed, ok := nsAllow[ns]
		if !ok {
			allowed = h.canAccess(ctx, user, "get", resource, ns)
			nsAllow[ns] = allowed
		}
		if allowed {
			out = append(out, item)
		}
	}
	return out
}

// getCached returns the cached snapshot, refreshing it if stale.
// Singleflight collapses concurrent refreshes — a thundering herd of
// dashboard polls produces exactly one fetchAll call per cacheTTL window.
func (h *Handler) getCached(ctx context.Context) (*cachedData, error) {
	h.cacheMu.RLock()
	if h.cache != nil && time.Since(h.cache.fetchedAt) < cacheTTL {
		data := h.cache
		h.cacheMu.RUnlock()
		return data, nil
	}
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("all", func() (any, error) {
		return h.fetchAll(ctx, gen)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedData), nil
}

// fetchAll concurrently lists all five ESO CRDs from the service-account
// dynamic client and normalizes them. Per-CRD failures are isolated and
// logged — one failed CRD does not erase the other four from the response.
// errgroup is used for context-cancellation hygiene; fail-fast semantics are
// suppressed by always returning nil from each g.Go body.
func (h *Handler) fetchAll(ctx context.Context, gen uint64) (*cachedData, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	dynClient := h.dynClient()

	var (
		externalSecrets        []ExternalSecret
		clusterExternalSecrets []ClusterExternalSecret
		stores                 []SecretStore
		clusterStores          []SecretStore
		pushSecrets            []PushSecret
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		list, err := dynClient.Resource(ExternalSecretGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			h.Logger.Warn("list externalsecrets failed", "error", err)
			externalSecrets = []ExternalSecret{}
			return nil
		}
		externalSecrets = make([]ExternalSecret, 0, len(list.Items))
		for i := range list.Items {
			externalSecrets = append(externalSecrets, normalizeExternalSecret(&list.Items[i]))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(ClusterExternalSecretGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			h.Logger.Warn("list clusterexternalsecrets failed", "error", err)
			clusterExternalSecrets = []ClusterExternalSecret{}
			return nil
		}
		clusterExternalSecrets = make([]ClusterExternalSecret, 0, len(list.Items))
		for i := range list.Items {
			clusterExternalSecrets = append(clusterExternalSecrets, normalizeClusterExternalSecret(&list.Items[i]))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(SecretStoreGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			h.Logger.Warn("list secretstores failed", "error", err)
			stores = []SecretStore{}
			return nil
		}
		stores = make([]SecretStore, 0, len(list.Items))
		for i := range list.Items {
			stores = append(stores, normalizeSecretStore(&list.Items[i], "Namespaced"))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(ClusterSecretStoreGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			h.Logger.Warn("list clustersecretstores failed", "error", err)
			clusterStores = []SecretStore{}
			return nil
		}
		clusterStores = make([]SecretStore, 0, len(list.Items))
		for i := range list.Items {
			clusterStores = append(clusterStores, normalizeSecretStore(&list.Items[i], "Cluster"))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(PushSecretGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			h.Logger.Warn("list pushsecrets failed", "error", err)
			pushSecrets = []PushSecret{}
			return nil
		}
		pushSecrets = make([]PushSecret, 0, len(list.Items))
		for i := range list.Items {
			pushSecrets = append(pushSecrets, normalizePushSecret(&list.Items[i]))
		}
		return nil
	})

	// Per-CRD goroutines never return errors above, so g.Wait() can only
	// surface a context cancellation. Treat that as a hard failure — a
	// timed-out fetch shouldn't poison the cache.
	if err := g.Wait(); err != nil {
		return nil, err
	}

	data := &cachedData{
		externalSecrets:        externalSecrets,
		clusterExternalSecrets: clusterExternalSecrets,
		stores:                 stores,
		clusterStores:          clusterStores,
		pushSecrets:            pushSecrets,
		fetchedAt:              time.Now(),
	}

	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cache = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// CachedExternalSecrets returns the cached ExternalSecret list. Used by the
// Phase C poller (Unit 10) and the Phase D dispatch (Unit 13) so they share
// the same singleflight + cache layer the HTTP path uses.
func (h *Handler) CachedExternalSecrets(ctx context.Context) ([]ExternalSecret, error) {
	data, err := h.getCached(ctx)
	if err != nil {
		return nil, err
	}
	return data.externalSecrets, nil
}

// HandleStatus returns the ESO discovery status. Cheap — reads the
// discoverer's cached status (re-probe is bounded by staleDuration).
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}
	httputil.WriteData(w, h.Discoverer.Status(r.Context()))
}

// HandleListExternalSecrets returns ExternalSecrets the user can access,
// optionally filtered by ?namespace=.
func (h *Handler) HandleListExternalSecrets(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []ExternalSecret{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch external secrets", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch external secrets", "")
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "externalsecrets", data.externalSecrets)
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		nsFiltered := make([]ExternalSecret, 0, len(filtered))
		for _, e := range filtered {
			if e.Namespace == ns {
				nsFiltered = append(nsFiltered, e)
			}
		}
		filtered = nsFiltered
	}

	httputil.WriteData(w, filtered)
}

// HandleListClusterExternalSecrets returns cluster-scoped ClusterExternalSecrets.
// Permissive-read: any user with `list clusterexternalsecrets` cluster-wide
// sees them; users without the grant get an empty list silently rather than a
// 403 (avoids existence-leak via timing).
func (h *Handler) HandleListClusterExternalSecrets(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []ClusterExternalSecret{})
		return
	}

	if !h.canAccess(r.Context(), user, "list", "clusterexternalsecrets", "") {
		httputil.WriteData(w, []ClusterExternalSecret{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch cluster external secrets", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch cluster external secrets", "")
		return
	}
	httputil.WriteData(w, data.clusterExternalSecrets)
}

// HandleListStores returns namespaced SecretStores.
func (h *Handler) HandleListStores(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []SecretStore{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch secret stores", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch secret stores", "")
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "secretstores", data.stores)
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		nsFiltered := make([]SecretStore, 0, len(filtered))
		for _, s := range filtered {
			if s.Namespace == ns {
				nsFiltered = append(nsFiltered, s)
			}
		}
		filtered = nsFiltered
	}

	httputil.WriteData(w, filtered)
}

// HandleGetExternalSecret returns a single ExternalSecret with drift status
// resolved against the live synced Secret's resourceVersion. The list
// endpoint never resolves drift — that would be N+1 impersonated Gets — so
// this endpoint is the source of truth for DriftStatus.
//
// RBAC: chi middleware ValidateURLParams runs before this handler. The
// impersonating dynamic client enforces RBAC at the API server, so a user
// without `get externalsecret` perm sees 404 from the dynamic Get below.
// The pre-check via canAccess avoids the round-trip for clearly-unauthorized
// requests.
func (h *Handler) HandleGetExternalSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "externalsecrets", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	obj, err := dynClient.Resource(ExternalSecretGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsForbidden(err) {
			httputil.WriteError(w, http.StatusForbidden, "access denied", "")
			return
		}
		if apierrors.IsNotFound(err) {
			httputil.WriteError(w, http.StatusNotFound, "external secret not found", "")
			return
		}
		h.Logger.Error("get externalsecret", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch external secret", "")
		return
	}

	es := normalizeExternalSecret(obj)
	es.DriftStatus = h.resolveDriftStatus(r.Context(), user, &es)
	es.Status = DeriveStatus(es)

	httputil.WriteData(w, es)
}

// HandleGetClusterExternalSecret returns a single ClusterExternalSecret.
// Cluster-scoped — no namespace param.
func (h *Handler) HandleGetClusterExternalSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "clusterexternalsecrets", "") {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	obj, err := dynClient.Resource(ClusterExternalSecretGVR).Namespace("").Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsForbidden(err) {
			httputil.WriteError(w, http.StatusForbidden, "access denied", "")
			return
		}
		if apierrors.IsNotFound(err) {
			httputil.WriteError(w, http.StatusNotFound, "cluster external secret not found", "")
			return
		}
		h.Logger.Error("get clusterexternalsecret", "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch cluster external secret", "")
		return
	}

	httputil.WriteData(w, normalizeClusterExternalSecret(obj))
}

// HandleGetStore returns a single namespaced SecretStore.
func (h *Handler) HandleGetStore(w http.ResponseWriter, r *http.Request) {
	h.handleGetStore(w, r, "Namespaced")
}

// HandleGetClusterStore returns a single ClusterSecretStore.
func (h *Handler) HandleGetClusterStore(w http.ResponseWriter, r *http.Request) {
	h.handleGetStore(w, r, "Cluster")
}

func (h *Handler) handleGetStore(w http.ResponseWriter, r *http.Request, scope string) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	name := chi.URLParam(r, "name")
	ns := ""
	resource := "clustersecretstores"
	gvr := ClusterSecretStoreGVR
	if scope == "Namespaced" {
		ns = chi.URLParam(r, "namespace")
		resource = "secretstores"
		gvr = SecretStoreGVR
	}

	if !h.canAccess(r.Context(), user, "get", resource, ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	obj, err := dynClient.Resource(gvr).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsForbidden(err) {
			httputil.WriteError(w, http.StatusForbidden, "access denied", "")
			return
		}
		if apierrors.IsNotFound(err) {
			httputil.WriteError(w, http.StatusNotFound, "store not found", "")
			return
		}
		h.Logger.Error("get store", "scope", scope, "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch store", "")
		return
	}

	httputil.WriteData(w, normalizeSecretStore(obj, scope))
}

// HandleGetPushSecret returns a single PushSecret.
func (h *Handler) HandleGetPushSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "pushsecrets", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	obj, err := dynClient.Resource(PushSecretGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsForbidden(err) {
			httputil.WriteError(w, http.StatusForbidden, "access denied", "")
			return
		}
		if apierrors.IsNotFound(err) {
			httputil.WriteError(w, http.StatusNotFound, "push secret not found", "")
			return
		}
		h.Logger.Error("get pushsecret", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch push secret", "")
		return
	}

	httputil.WriteData(w, normalizePushSecret(obj))
}

// resolveDriftStatus resolves the live drift state of an ExternalSecret by
// looking up the synced Secret's current resourceVersion. Returns one of:
//
//   - DriftUnknown when the provider doesn't populate syncedResourceVersion,
//     when the synced Secret has been deleted, or when the requesting user
//     lacks `get secret` perm on the target namespace
//   - DriftInSync when syncedResourceVersion matches the live Secret's RV
//   - DriftDrifted when the RVs differ (operator likely edited the Secret)
//
// The caller is responsible for re-applying DeriveStatus afterwards so
// Drifted overlays the base Synced status.
func (h *Handler) resolveDriftStatus(ctx context.Context, user *auth.User, es *ExternalSecret) DriftStatus {
	if es.SyncedResourceVersion == "" {
		return DriftUnknown
	}
	if es.TargetSecretName == "" {
		return DriftUnknown
	}
	cs, err := h.clientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Warn("create impersonating typed client for drift check", "error", err)
		return DriftUnknown
	}
	secret, err := cs.CoreV1().Secrets(es.Namespace).Get(ctx, es.TargetSecretName, metav1.GetOptions{})
	if err != nil {
		// Forbidden / NotFound / any other error → degrade to Unknown.
		// The ExternalSecret detail endpoint stays 200 even when drift can't
		// be resolved; the ES itself exists, only its drift signal is missing.
		if !apierrors.IsForbidden(err) && !apierrors.IsNotFound(err) {
			h.Logger.Warn("get synced secret for drift check",
				"namespace", es.Namespace,
				"name", es.TargetSecretName,
				"error", err)
		}
		return DriftUnknown
	}
	return computeDriftStatus(es.SyncedResourceVersion, secret.ResourceVersion)
}

// computeDriftStatus is the pure comparison used by resolveDriftStatus.
// Extracted as a separate function so the comparison can be unit-tested
// without the impersonating-client setup. Empty syncedRV (provider doesn't
// populate the field) maps to Unknown rather than guessing InSync, which
// matches the requirements doc's tri-state contract (R20).
func computeDriftStatus(syncedRV, liveRV string) DriftStatus {
	if syncedRV == "" {
		return DriftUnknown
	}
	if syncedRV == liveRV {
		return DriftInSync
	}
	return DriftDrifted
}

// HandleListClusterStores returns ClusterSecretStores. Permissive-read like
// ClusterExternalSecrets — see HandleListClusterExternalSecrets for rationale.
func (h *Handler) HandleListClusterStores(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []SecretStore{})
		return
	}

	if !h.canAccess(r.Context(), user, "list", "clustersecretstores", "") {
		httputil.WriteData(w, []SecretStore{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch cluster secret stores", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch cluster secret stores", "")
		return
	}
	httputil.WriteData(w, data.clusterStores)
}

// HandleListPushSecrets returns PushSecrets the user can access, optionally
// filtered by ?namespace=. Read-only in v1 — write surface deferred until
// usage signals demand.
func (h *Handler) HandleListPushSecrets(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []PushSecret{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch push secrets", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch push secrets", "")
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "pushsecrets", data.pushSecrets)
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		nsFiltered := make([]PushSecret, 0, len(filtered))
		for _, p := range filtered {
			if p.Namespace == ns {
				nsFiltered = append(nsFiltered, p)
			}
		}
		filtered = nsFiltered
	}

	httputil.WriteData(w, filtered)
}
