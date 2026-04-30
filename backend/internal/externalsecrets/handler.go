package externalsecrets

import (
	"context"
	"fmt"
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

	// dynOverride / dynForUserOverride / clientForUserOverride are test-only
	// seams. Production wiring leaves them nil — the real handler delegates
	// to K8sClient. They live as struct fields rather than constructor
	// parameters so handler-level tests don't have to build a fake
	// ClientFactory from scratch (the factory holds a *rest.Config that
	// FakeDynamicClient can't substitute for cleanly). The cert-manager
	// precedent has no handler tests at all, so it doesn't have these
	// seams; the comparison isn't apples-to-apples. Keeping the seams is a
	// deliberate architectural choice: the small surface in this struct
	// buys us the ability to test RBAC behaviour, drift resolution, and
	// the cache layer as a unit, which catches real bugs the cert-manager
	// package can only surface in integration tests.

	// dynOverride, when non-nil, replaces K8sClient.BaseDynamicClient() for
	// service-account list calls.
	dynOverride dynamic.Interface

	// dynForUserOverride, when non-nil, replaces K8sClient.DynamicClientForUser
	// for impersonated CRD fetches in detail endpoints.
	dynForUserOverride func(username string, groups []string) (dynamic.Interface, error)

	// clientForUserOverride, when non-nil, replaces K8sClient.ClientForUser
	// for impersonated typed client lookups (synced-Secret RV check).
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
//
// errors carries per-CRD list-call failures from the most recent fetchAll.
// Keys are short CRD identifiers (`externalsecrets`, `clusterexternalsecrets`,
// `secretstores`, `clustersecretstores`, `pushsecrets`); values are
// human-readable error strings (never raw API server bodies). Mirrors the
// service-mesh Phase D `errors` map pattern. Empty / nil when all CRDs fetched
// cleanly. Surfaced separately from the per-CRD slice so a failed CRD
// preserves the last-known-good slice rather than collapsing to empty.
type cachedData struct {
	externalSecrets        []ExternalSecret
	clusterExternalSecrets []ClusterExternalSecret
	stores                 []SecretStore
	clusterStores          []SecretStore
	pushSecrets            []PushSecret
	errors                 map[string]string
	fetchedAt              time.Time
}

// NewHandler creates an ESO observatory handler. NotifService may be nil; cache
// invalidation events fire only when it's set (matches cert-manager precedent).
// Logger may be nil; falls back to slog.Default() so a struct-literal misuse
// elsewhere (or a future call site that forgets to pass logger) doesn't panic
// the first time the handler logs.
func NewHandler(
	k8sClient *k8s.ClientFactory,
	discoverer *Discoverer,
	accessChecker *resources.AccessChecker,
	auditLogger audit.Logger,
	notifService *notifications.NotificationService,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
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
//
// The singleflight key embeds cacheGen so an InvalidateCache call splits a
// new wave of requests into a fresh in-flight fetch rather than waiting on
// the pre-invalidation fetch's result. Phase A has no cache-invalidating
// call sites; this is dormant pre-Phase E but cheap to keep correct now.
func (h *Handler) getCached(ctx context.Context) (*cachedData, error) {
	h.cacheMu.RLock()
	if h.cache != nil && time.Since(h.cache.fetchedAt) < cacheTTL {
		data := h.cache
		h.cacheMu.RUnlock()
		return data, nil
	}
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	key := fmt.Sprintf("all-%d", gen)
	result, err, _ := h.fetchGroup.Do(key, func() (any, error) {
		return h.fetchAll(ctx, gen)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedData), nil
}

// fetchAll concurrently lists all five ESO CRDs from the service-account
// dynamic client and normalizes them. Per-CRD failures are isolated: a failed
// CRD's slice stays nil, the error is recorded in cachedData.errors, and the
// previous cache's last-known-good slice is preserved on the rebuild. One
// failed CRD does not erase the other four from the response.
//
// ListOptions{ResourceVersion: "0"} serves all 5 lists from the API server's
// watch cache rather than going to etcd — same data freshness, lower etcd
// cost, no semantic change.
//
// errgroup is used for context-cancellation hygiene; fail-fast semantics are
// suppressed by always returning nil from each g.Go body. After g.Wait the
// parent ctx is re-checked so a cancelled / timed-out fetch produces an
// error rather than a half-empty cache.
func (h *Handler) fetchAll(ctx context.Context, gen uint64) (*cachedData, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	dynClient := h.dynClient()

	listOpts := metav1.ListOptions{ResourceVersion: "0"}

	var (
		externalSecrets        []ExternalSecret
		clusterExternalSecrets []ClusterExternalSecret
		stores                 []SecretStore
		clusterStores          []SecretStore
		pushSecrets            []PushSecret
	)

	var (
		errMu  sync.Mutex
		errMap map[string]string
	)
	recordErr := func(crd string, err error) {
		errMu.Lock()
		defer errMu.Unlock()
		if errMap == nil {
			errMap = map[string]string{}
		}
		errMap[crd] = err.Error()
	}

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		list, err := dynClient.Resource(ExternalSecretGVR).Namespace("").List(gctx, listOpts)
		if err != nil {
			h.Logger.Warn("list externalsecrets failed", "error", err)
			recordErr("externalsecrets", err)
			return nil
		}
		externalSecrets = make([]ExternalSecret, 0, len(list.Items))
		for i := range list.Items {
			externalSecrets = append(externalSecrets, normalizeExternalSecret(&list.Items[i]))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(ClusterExternalSecretGVR).Namespace("").List(gctx, listOpts)
		if err != nil {
			h.Logger.Warn("list clusterexternalsecrets failed", "error", err)
			recordErr("clusterexternalsecrets", err)
			return nil
		}
		clusterExternalSecrets = make([]ClusterExternalSecret, 0, len(list.Items))
		for i := range list.Items {
			clusterExternalSecrets = append(clusterExternalSecrets, normalizeClusterExternalSecret(&list.Items[i]))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(SecretStoreGVR).Namespace("").List(gctx, listOpts)
		if err != nil {
			h.Logger.Warn("list secretstores failed", "error", err)
			recordErr("secretstores", err)
			return nil
		}
		stores = make([]SecretStore, 0, len(list.Items))
		for i := range list.Items {
			stores = append(stores, normalizeSecretStore(&list.Items[i], "Namespaced"))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(ClusterSecretStoreGVR).Namespace("").List(gctx, listOpts)
		if err != nil {
			h.Logger.Warn("list clustersecretstores failed", "error", err)
			recordErr("clustersecretstores", err)
			return nil
		}
		clusterStores = make([]SecretStore, 0, len(list.Items))
		for i := range list.Items {
			clusterStores = append(clusterStores, normalizeSecretStore(&list.Items[i], "Cluster"))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(PushSecretGVR).Namespace("").List(gctx, listOpts)
		if err != nil {
			h.Logger.Warn("list pushsecrets failed", "error", err)
			recordErr("pushsecrets", err)
			return nil
		}
		pushSecrets = make([]PushSecret, 0, len(list.Items))
		for i := range list.Items {
			pushSecrets = append(pushSecrets, normalizePushSecret(&list.Items[i]))
		}
		return nil
	})

	// Per-CRD goroutines never return errors, so g.Wait() returns nil even
	// when the parent ctx has been cancelled / timed out. Re-check the parent
	// ctx explicitly: a cancelled fetch must NOT poison the cache with an
	// empty snapshot for the next 30s.
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Preserve last-known-good for any CRD that failed: read the prior cache
	// snapshot and keep its slice for failed CRDs. If no prior snapshot
	// exists (cold cache), nil stays nil — the handler treats nil as the
	// signal to fall through to the per-CRD overlay path on next read.
	h.cacheMu.RLock()
	prior := h.cache
	h.cacheMu.RUnlock()
	if prior != nil && errMap != nil {
		if _, failed := errMap["externalsecrets"]; failed && externalSecrets == nil {
			externalSecrets = prior.externalSecrets
		}
		if _, failed := errMap["clusterexternalsecrets"]; failed && clusterExternalSecrets == nil {
			clusterExternalSecrets = prior.clusterExternalSecrets
		}
		if _, failed := errMap["secretstores"]; failed && stores == nil {
			stores = prior.stores
		}
		if _, failed := errMap["clustersecretstores"]; failed && clusterStores == nil {
			clusterStores = prior.clusterStores
		}
		if _, failed := errMap["pushsecrets"]; failed && pushSecrets == nil {
			pushSecrets = prior.pushSecrets
		}
	}

	// Default any still-nil slice to an empty slice so handlers never write
	// JSON null for a CRD that had no last-known-good. Empty slice is the
	// frontend-safe shape.
	if externalSecrets == nil {
		externalSecrets = []ExternalSecret{}
	}
	if clusterExternalSecrets == nil {
		clusterExternalSecrets = []ClusterExternalSecret{}
	}
	if stores == nil {
		stores = []SecretStore{}
	}
	if clusterStores == nil {
		clusterStores = []SecretStore{}
	}
	if pushSecrets == nil {
		pushSecrets = []PushSecret{}
	}

	// Resolve annotation-driven thresholds (Phase D). ApplyThresholds runs
	// the ES > Store > ClusterStore > default chain per ES, writes resolved
	// values + per-key sources back onto each ES, and re-derives Status so
	// the stale overlay can fire. Idempotent across cache hits since the
	// resolver re-reads the same pointer fields each time.
	ApplyThresholds(externalSecrets, stores, clusterStores, h.Logger)

	data := &cachedData{
		externalSecrets:        externalSecrets,
		clusterExternalSecrets: clusterExternalSecrets,
		stores:                 stores,
		clusterStores:          clusterStores,
		pushSecrets:            pushSecrets,
		errors:                 errMap,
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

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
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
	// Resolve annotation thresholds before drift / status. Detail endpoint
	// pulls store snapshots from the cached fetchAll so the inheritance
	// chain still works on a single-ES path. ApplyThresholds with a
	// one-element slice is the simplest way to keep the resolver as the
	// single source of truth (L3.1 in the plan).
	ess := []ExternalSecret{es}
	stores, clusterStores := h.cachedStoresForResolver()
	ApplyThresholds(ess, stores, clusterStores, h.Logger)
	es = ess[0]

	es.DriftStatus, es.DriftUnknownReason = h.resolveDriftStatus(r.Context(), user, &es)
	es.Status = DeriveStatus(es)

	httputil.WriteData(w, es)
}

// cachedStoresForResolver returns the most recent cached store + cluster-store
// slices for use by the threshold resolver on detail-endpoint paths. Returns
// nil/nil when the cache is cold; the resolver falls through to defaults.
func (h *Handler) cachedStoresForResolver() (stores, clusterStores []SecretStore) {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()
	if h.cache == nil {
		return nil, nil
	}
	return h.cache.stores, h.cache.clusterStores
}

// HandleGetClusterExternalSecret returns a single ClusterExternalSecret.
// Cluster-scoped — no namespace param.
func (h *Handler) HandleGetClusterExternalSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
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

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
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

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
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

// Reasons populated alongside DriftStatus=Unknown so the UI can explain WHY
// drift wasn't resolvable. Empty string means DriftStatus was definite
// (InSync or Drifted). Frontend treats unknown values as the generic
// "drift not resolvable" copy.
const (
	DriftReasonNoSyncedRV    = "no_synced_rv"
	DriftReasonNoTargetName  = "no_target_name"
	DriftReasonSecretDeleted = "secret_deleted"
	DriftReasonRBACDenied    = "rbac_denied"
	DriftReasonTransient     = "transient_error"
	DriftReasonClientError   = "client_error"
)

// resolveDriftStatus resolves the live drift state of an ExternalSecret by
// looking up the synced Secret's current resourceVersion. Returns
// (DriftStatus, reason) where reason is non-empty only when DriftStatus
// is Unknown.
//
//   - DriftUnknown when the provider doesn't populate syncedResourceVersion,
//     when the synced Secret has been deleted, or when the requesting user
//     lacks `get secret` perm on the target namespace
//   - DriftInSync when syncedResourceVersion matches the live Secret's RV
//   - DriftDrifted when the RVs differ (operator likely edited the Secret)
//
// The caller is responsible for re-applying DeriveStatus afterwards so
// Drifted overlays the base Synced status.
func (h *Handler) resolveDriftStatus(ctx context.Context, user *auth.User, es *ExternalSecret) (DriftStatus, string) {
	if es.SyncedResourceVersion == "" {
		return DriftUnknown, DriftReasonNoSyncedRV
	}
	if es.TargetSecretName == "" {
		return DriftUnknown, DriftReasonNoTargetName
	}
	cs, err := h.clientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Warn("create impersonating typed client for drift check", "error", err)
		return DriftUnknown, DriftReasonClientError
	}
	secret, err := cs.CoreV1().Secrets(es.Namespace).Get(ctx, es.TargetSecretName, metav1.GetOptions{})
	if err != nil {
		switch {
		case apierrors.IsNotFound(err):
			// Synced Secret missing is abnormal — log louder so an operator
			// can correlate against the ES detail page.
			h.Logger.Warn("synced secret missing for drift check",
				"namespace", es.Namespace,
				"name", es.TargetSecretName)
			return DriftUnknown, DriftReasonSecretDeleted
		case apierrors.IsForbidden(err):
			return DriftUnknown, DriftReasonRBACDenied
		default:
			h.Logger.Warn("get synced secret for drift check",
				"namespace", es.Namespace,
				"name", es.TargetSecretName,
				"error", err)
			return DriftUnknown, DriftReasonTransient
		}
	}
	return computeDriftStatus(es.SyncedResourceVersion, secret.ResourceVersion), ""
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
