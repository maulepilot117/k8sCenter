package externalsecrets

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
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

// Handler serves ESO observatory HTTP endpoints for the cluster a request
// selects. The local cluster is read through a service-account cache and the
// local Discoverer; a remote cluster through Clients, as the requesting
// identity, with its lists held briefly per identity in remote (remote.go).
// Write actions and bulk refresh are local-only.
//
// Local concurrency model:
//   - One in-flight fetchAll at a time per Handler (singleflight).
//   - One cache snapshot, replaced atomically when fetch completes.
//   - cacheGen guards against torn writes from concurrent invalidations.
type Handler struct {
	K8sClient *k8s.ClientFactory
	// Clients resolves the per-user clients for the cluster a request
	// targets. Production passes the ClusterRouter.
	Clients       k8s.ClusterClients
	Presence      *k8s.Presence
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	AuditLogger   audit.Logger
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger

	// BulkJobStore + BulkWorker drive Phase E Unit 15 bulk refresh. Both are
	// optional: when either is nil the bulk-refresh endpoints respond 503
	// rather than panicking. Wired in main.go alongside the poller.
	BulkJobStore BulkJobReadWriter
	BulkWorker   BulkWorkerEnqueuer

	// MonitoringDisc is optional; when set, Phase F endpoints query
	// Prometheus for per-store request-rate metrics. Nil is supported —
	// the metrics endpoint returns `{rate: nil, error: "rate metrics
	// offline"}` rather than 500. Wired in main.go after monDiscoverer.
	MonitoringDisc *monitoring.Discoverer

	// HistoryStore is optional; nil => the history endpoint answers 503
	// history_unavailable rather than panicking. Wired in main.go.
	HistoryStore ESOHistoryReader
	// ClusterID is the configured id this process polls (cfg.ClusterID).
	// History rows are stamped with it; the read path pins it.
	ClusterID string

	remote *remotecache.Cache[*snapshot]

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cache      *cachedData
	cacheGen   uint64

	// evidenceGVRs caches the discovery walk that resolves each ESO kind's
	// served version for the evidence endpoints (detail_evidence.go).
	evidenceGVRs evidenceGVRCache

	// observedDrift is the poller's last-observed DriftStatus per ES UID,
	// populated by RecordDrift on every successful Secret fetch. The list
	// endpoint reads from this map to surface a coarse drift hint without
	// an N+1 impersonated `get secret`. Detail endpoint bypasses this and
	// resolves drift live for source-of-truth accuracy. Pruned at the end
	// of each poller tick via PruneObservedDrift to drop UIDs that have
	// vanished from the inventory.
	observedDrift sync.Map // map[string]DriftStatus

	// dynOverride and promQuerierOverride are test-only seams (per-user
	// clients come from Clients, which tests stub). Production wiring leaves
	// them nil and the handler delegates to K8sClient and MonitoringDisc.
	// They live as struct fields rather than constructor parameters so
	// handler-level tests don't have to build a fake ClientFactory from
	// scratch (the factory holds a *rest.Config that FakeDynamicClient can't
	// substitute for cleanly). The small surface buys unit tests of RBAC
	// behaviour, drift resolution and the cache layer, which catch real bugs
	// the cert-manager package can only surface in integration tests.

	// dynOverride, when non-nil, replaces K8sClient.BaseDynamicClient() for
	// service-account list calls.
	dynOverride dynamic.Interface

	// promQuerierOverride, when non-nil, replaces the live Prometheus
	// client returned by MonitoringDisc for the metrics endpoints. Tests
	// inject a fake here so we don't have to spin up a full
	// monitoring.Discoverer.
	promQuerierOverride promQuerier
}

// dynClient returns the dynamic client to use for service-account-scoped
// list calls. Tests inject dynOverride; production reads BaseDynamicClient
// from the K8sClient factory.
func (h *Handler) dynClient() dynamic.Interface {
	if h.dynOverride != nil {
		return h.dynOverride
	}
	// nolint:cluster-routing local path: the service-account cache serves the local cluster only; remote reads go through fetchRemote.
	return h.K8sClient.BaseDynamicClient()
}

// dynForRequest returns a dynamic client impersonating the user on the
// cluster the request targets, so that cluster's API server enforces RBAC.
// A remote failure is an error, never a local fallback.
func (h *Handler) dynForRequest(ctx context.Context, user *auth.User) (dynamic.Interface, error) {
	return h.Clients.DynamicClientForCluster(ctx, middleware.ClusterIDFromContext(ctx), user.KubernetesUsername, user.KubernetesGroups)
}

// clientForRequest is dynForRequest for the typed client, used by detail
// endpoints to read the synced Secret's live resourceVersion for drift.
func (h *Handler) clientForRequest(ctx context.Context, user *auth.User) (kubernetes.Interface, error) {
	return h.Clients.ClientForCluster(ctx, middleware.ClusterIDFromContext(ctx), user.KubernetesUsername, user.KubernetesGroups)
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
	clients k8s.ClusterClients,
	presence *k8s.Presence,
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
		Clients:       clients,
		Presence:      presence,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		AuditLogger:   auditLogger,
		NotifService:  notifService,
		Logger:        logger,
		remote:        remotecache.New[*snapshot](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, logger),
	}
}

// InvalidateCache forces the next local read to re-fetch from the API
// server. Bumps cacheGen so an in-flight fetch from before invalidation
// cannot overwrite a fresh cache populated after. Called after the local-only
// bulk refresh writes.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cacheGen++
	h.cache = nil
	h.cacheMu.Unlock()
}

// RecordDrift stashes the poller's drift observation for an ES UID. Called
// from the poller's secret-fetch path each tick. Subsequent list responses
// will surface this state via LastObservedDriftStatus until a newer value
// overwrites it or PruneObservedDrift removes it.
func (h *Handler) RecordDrift(uid string, status DriftStatus) {
	if uid == "" {
		return
	}
	h.observedDrift.Store(uid, status)
}

// observedDriftFor returns the last-observed drift state for a UID, or
// the empty string if no observation has been recorded yet (poller hasn't
// run, or this UID is brand-new). The empty return is what makes
// `LastObservedDriftStatus` omitempty actually fire on list responses —
// returning DriftUnknown ("Unknown") would always serialize as a non-empty
// string and the field would be present on every row, contradicting the
// json:"omitempty" tag.
//
// Callers distinguish "no observation" (empty) from "observed Unknown"
// (rare; only set on Secret-fetch error paths in resolveDiffKeys). Both
// surface to operators identically — the dashboard counts only the
// Drifted bucket — but the wire shape is honest about what we know.
func (h *Handler) observedDriftFor(uid string) DriftStatus {
	if v, ok := h.observedDrift.Load(uid); ok {
		if d, ok := v.(DriftStatus); ok {
			return d
		}
	}
	return ""
}

// PruneObservedDrift drops drift observations for UIDs no longer in the
// poller's currentUIDs set. Called by the poller at the end of each tick
// so deleted ESes don't accumulate stale drift state in the map.
func (h *Handler) PruneObservedDrift(currentUIDs map[string]bool) {
	h.observedDrift.Range(func(key, _ any) bool {
		uid, ok := key.(string)
		if !ok {
			h.observedDrift.Delete(key)
			return true
		}
		if !currentUIDs[uid] {
			h.observedDrift.Delete(uid)
		}
		return true
	})
}

// canAccess checks a single (verb, resource, namespace) tuple in the ESO API
// group via the AccessChecker. Phase A only ever passes "list" / "get"; write
// verbs land in Phases D / E.
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool {
	return h.canAccessGroup(ctx, user, verb, GroupName, resource, namespace)
}

// canAccessGroup is canAccess for an explicit API group ("" is the core
// group). A failed check is a denial: every caller uses it to decide whether
// to allow or widen a response.
func (h *Handler) canAccessGroup(ctx context.Context, user *auth.User, verb, apiGroup, resource, namespace string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx,
		middleware.ClusterIDFromContext(ctx),
		user.KubernetesUsername,
		user.KubernetesGroups,
		verb,
		apiGroup,
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
// CRD's error is recorded in cachedData.errors and the previous cache's
// last-known-good slice is kept for it. One failed CRD does not erase the
// other four from the response.
//
// A list that panics is recovered by RunLists and recorded as failed like
// any other. After the lists finish the parent ctx is re-checked so a
// cancelled / timed-out fetch produces an error rather than a half-empty
// cache.
func (h *Handler) fetchAll(ctx context.Context, gen uint64) (*cachedData, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	data := &cachedData{}
	sources := data.sources()
	errs := k8s.RunLists(h.Logger, namedLists(ctx, h.dynClient(), sources))
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Preserve last-known-good for any CRD that failed. If no prior snapshot
	// exists (cold cache), the failed CRD's slice defaults to empty below.
	h.cacheMu.RLock()
	prior := h.cache
	h.cacheMu.RUnlock()
	for i, src := range sources {
		err := errs[i]
		if err == nil {
			continue
		}
		h.Logger.Warn("list "+src.gvr.Resource+" failed", "error", err)
		if data.errors == nil {
			data.errors = map[string]string{}
		}
		data.errors[src.gvr.Resource] = err.Error()
		if prior != nil {
			src.keep(prior)
		}
	}

	data.finish(h.Logger)

	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cache = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// source is one cluster-wide ESO list: how to fill its field of a
// cachedData, and how to carry that field over from an earlier snapshot.
type source struct {
	gvr  schema.GroupVersionResource
	list func(context.Context, dynamic.Interface) error
	keep func(prior *cachedData)
}

// sources are the lists that fill d, one per ESO kind. Each writes its own
// field of d, so they run concurrently without a lock.
func (d *cachedData) sources() []source {
	return []source{
		{
			ExternalSecretGVR,
			func(ctx context.Context, dyn dynamic.Interface) error {
				return listInto(ctx, dyn, ExternalSecretGVR, normalizeExternalSecret, &d.externalSecrets)
			},
			func(p *cachedData) { d.externalSecrets = p.externalSecrets },
		},
		{
			ClusterExternalSecretGVR,
			func(ctx context.Context, dyn dynamic.Interface) error {
				return listInto(ctx, dyn, ClusterExternalSecretGVR, normalizeClusterExternalSecret, &d.clusterExternalSecrets)
			},
			func(p *cachedData) { d.clusterExternalSecrets = p.clusterExternalSecrets },
		},
		{
			SecretStoreGVR,
			func(ctx context.Context, dyn dynamic.Interface) error {
				return listInto(ctx, dyn, SecretStoreGVR, func(u *unstructured.Unstructured) SecretStore {
					return normalizeSecretStore(u, "Namespaced")
				}, &d.stores)
			},
			func(p *cachedData) { d.stores = p.stores },
		},
		{
			ClusterSecretStoreGVR,
			func(ctx context.Context, dyn dynamic.Interface) error {
				return listInto(ctx, dyn, ClusterSecretStoreGVR, func(u *unstructured.Unstructured) SecretStore {
					return normalizeSecretStore(u, "Cluster")
				}, &d.clusterStores)
			},
			func(p *cachedData) { d.clusterStores = p.clusterStores },
		},
		{
			PushSecretGVR,
			func(ctx context.Context, dyn dynamic.Interface) error {
				return listInto(ctx, dyn, PushSecretGVR, normalizePushSecret, &d.pushSecrets)
			},
			func(p *cachedData) { d.pushSecrets = p.pushSecrets },
		},
	}
}

// finish completes a freshly listed snapshot: every slice left nil becomes
// empty, so handlers never write JSON null for a CRD, then the annotation
// thresholds are resolved and the fetch time stamped.
func (d *cachedData) finish(logger *slog.Logger) {
	if d.externalSecrets == nil {
		d.externalSecrets = []ExternalSecret{}
	}
	if d.clusterExternalSecrets == nil {
		d.clusterExternalSecrets = []ClusterExternalSecret{}
	}
	if d.stores == nil {
		d.stores = []SecretStore{}
	}
	if d.clusterStores == nil {
		d.clusterStores = []SecretStore{}
	}
	if d.pushSecrets == nil {
		d.pushSecrets = []PushSecret{}
	}

	// Resolve annotation-driven thresholds (Phase D). ApplyThresholds runs
	// the ES > Store > ClusterStore > default chain per ES, writes resolved
	// values + per-key sources back onto each ES, and re-derives Status so
	// the stale overlay can fire.
	ApplyThresholds(d.externalSecrets, d.stores, d.clusterStores, logger)
	d.fetchedAt = time.Now()
}

// namedLists runs each of sources against dyn under ctx.
func namedLists(ctx context.Context, dyn dynamic.Interface, sources []source) []k8s.NamedList {
	lists := make([]k8s.NamedList, len(sources))
	for i, src := range sources {
		lists[i] = k8s.NamedList{Label: "externalsecrets list " + src.gvr.Resource, Run: func() error { return src.list(ctx, dyn) }}
	}
	return lists
}

// listInto lists gvr across every namespace and stores the normalized items
// in dst, leaving dst untouched on error. ResourceVersion "0" serves the list
// from the API server's watch cache rather than etcd: same freshness, lower
// etcd cost.
func listInto[T any](ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, normalize func(*unstructured.Unstructured) T, dst *[]T) error {
	list, err := dyn.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		return err
	}
	items := make([]T, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, normalize(&list.Items[i]))
	}
	*dst = items
	return nil
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

// HandleStatus returns the ESO discovery status for the request's cluster.
// Cheap on local — reads the discoverer's cached status (re-probe is bounded
// by staleDuration).
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if !isLocal(r.Context()) {
		httputil.WriteData(w, h.remoteStatus(r.Context(), user))
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

	data, ok := h.loadList(w, r, user, ExternalSecretGVR, "external secrets")
	if !ok {
		return
	}
	if data == nil {
		httputil.WriteData(w, []ExternalSecret{})
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

	// Phase C: surface the poller's last-observed drift state so the
	// dashboard's Drifted count and the list view's status badge reflect
	// drift without an N+1 impersonated `get secret`. Operates on a fresh
	// copy of each ES so the cached snapshot (shared across users) is
	// not mutated.
	//
	// Wire-shape contract: the list response sets ONLY
	// LastObservedDriftStatus; DriftStatus is reserved for the detail
	// endpoint's live impersonated read and stays absent here. The
	// Status field is overlaid to "Drifted" so the existing dashboard
	// count (which checks `it.status === 'Drifted'`) works without
	// frontend changes — but the live-vs-cached distinction is
	// preserved by keeping DriftStatus empty on the list path.
	//
	// The poller observes the local cluster only, so a remote row's hint is
	// always Unknown: never absent, which would read as "not drifted" (R14).
	isRemote := !isLocal(r.Context())
	out := make([]ExternalSecret, len(filtered))
	for i, es := range filtered {
		// Clear any DriftStatus the cached normalize step may have set
		// (DriftUnknown is the zero-value default in Phase A normalize).
		// On the list path, DriftStatus is always absent on the wire.
		es.DriftStatus = ""
		es.DriftUnknownReason = ""
		if isRemote {
			es.LastObservedDriftStatus = DriftUnknown
			out[i] = es
			continue
		}
		drift := h.observedDriftFor(es.UID)
		if drift != "" {
			es.LastObservedDriftStatus = drift
			if drift == DriftDrifted && es.Status == StatusSynced {
				es.Status = StatusDrifted
			}
		}
		out[i] = es
	}

	httputil.WriteData(w, out)
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

	// Load before the RBAC check: on a remote cluster a failed check would
	// otherwise answer an unreachable cluster with an empty 200.
	data, ok := h.loadList(w, r, user, ClusterExternalSecretGVR, "cluster external secrets")
	if !ok {
		return
	}
	if data == nil || !h.canAccess(r.Context(), user, "list", "clusterexternalsecrets", "") {
		httputil.WriteData(w, []ClusterExternalSecret{})
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

	data, ok := h.loadList(w, r, user, SecretStoreGVR, "secret stores")
	if !ok {
		return
	}
	if data == nil {
		httputil.WriteData(w, []SecretStore{})
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

	if !h.requireInstalled(w, r, user) {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "externalsecrets", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.requestDyn(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(ExternalSecretGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.writeGetError(w, r, err, "external secret", ns, name)
		return
	}

	es := normalizeExternalSecret(obj)
	// Resolve annotation thresholds before drift / status. Detail endpoint
	// pulls store snapshots from the request cluster's cached lists so the
	// inheritance chain still works on a single-ES path. ApplyThresholds with a
	// one-element slice is the simplest way to keep the resolver as the
	// single source of truth (L3.1 in the plan).
	ess := []ExternalSecret{es}
	stores, clusterStores := h.storesForResolver(r.Context(), user)
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

	if !h.requireInstalled(w, r, user) {
		return
	}

	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "clusterexternalsecrets", "") {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.requestDyn(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(ClusterExternalSecretGVR).Namespace("").Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.writeGetError(w, r, err, "cluster external secret", "", name)
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

	if !h.requireInstalled(w, r, user) {
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

	dynClient, ok := h.requestDyn(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(gvr).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.writeGetError(w, r, err, "store", ns, name)
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

	if !h.requireInstalled(w, r, user) {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "pushsecrets", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.requestDyn(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(PushSecretGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.writeGetError(w, r, err, "push secret", ns, name)
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
	cs, err := h.clientForRequest(ctx, user)
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

	// Load before the RBAC check, as HandleListClusterExternalSecrets does.
	data, ok := h.loadList(w, r, user, ClusterSecretStoreGVR, "cluster secret stores")
	if !ok {
		return
	}
	if data == nil || !h.canAccess(r.Context(), user, "list", "clustersecretstores", "") {
		httputil.WriteData(w, []SecretStore{})
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

	data, ok := h.loadList(w, r, user, PushSecretGVR, "push secrets")
	if !ok {
		return
	}
	if data == nil {
		httputil.WriteData(w, []PushSecret{})
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
