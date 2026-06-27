package certmanager

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const (
	cacheTTL        = 30 * time.Second
	maxRenewRetries = 1
	issuingCondType = "Issuing"
	issuingReason   = "ManuallyTriggered"
	issuingMessage  = "Certificate re-issuance manually triggered"
)

// Handler serves cert-manager HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	ClusterRouter *k8s.ClusterRouter
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	AuditLogger   audit.Logger
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cache      *cachedData
	cacheGen   uint64

	// F#16 (round-2) — per-cluster cache for fetchAllRemote results so a
	// single page load with N cert-manager widgets doesn't fan out into N
	// round-trips against the remote API. Coalesces across HandleList*
	// + HandleGetCertificate + HandleListExpiring concurrent calls in
	// the same 30s window (cacheTTL above). Singleflight (remoteFetchGroup)
	// protects against concurrent first-loads; the cache + TTL bound the
	// rate after that. F#11 (round-3) — was "60s window"; the constant
	// has always been 30s.
	remoteFetchGroup singleflight.Group
	remoteCacheMu    sync.RWMutex
	remoteCache      map[string]*cachedData // keyed by clusterID
}

type cachedData struct {
	certificates   []Certificate
	issuers        []Issuer
	clusterIssuers []Issuer
	fetchedAt      time.Time
}

// NewHandler creates a new cert-manager handler.
func NewHandler(
	k8sClient *k8s.ClientFactory,
	clusterRouter *k8s.ClusterRouter,
	discoverer *Discoverer,
	accessChecker *resources.AccessChecker,
	auditLogger audit.Logger,
	notifService *notifications.NotificationService,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     k8sClient,
		ClusterRouter: clusterRouter,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		AuditLogger:   auditLogger,
		NotifService:  notifService,
		Logger:        logger,
	}
}

// InvalidateCache clears the cached data and emits a notification.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cacheGen++
	h.cache = nil
	h.cacheMu.Unlock()

	if h.NotifService != nil {
		go h.NotifService.Emit(context.Background(), notifications.Notification{
			Source:   notifications.SourceCertManager,
			Severity: notifications.SeverityInfo,
			Title:    "cert-manager data updated",
			Message:  "Certificate or issuer data has changed",
		})
	}
}

// EvictRemoteCache drops the per-cluster fetchAllRemote cache entry for
// the given clusterID. Wired into ClusterRouter.RegisterEvictHook from
// main.go so a cluster deletion or credential update wipes the cert-
// manager remote cache in the same operation. Without this hook a
// re-registered cluster ID could briefly serve the previous tenant's
// data through HandleListCertificates / HandleGetCertificate /
// HandleListExpiring until the next cacheTTL expired. F#8 round-3.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remoteCacheMu.Lock()
	if h.remoteCache != nil {
		delete(h.remoteCache, clusterID)
	}
	h.remoteCacheMu.Unlock()
}

// getImpersonatingClient creates a dynamic client impersonating the user, routing to the
// correct cluster via ClusterRouter. Returns (nil, false) and writes an error response on failure.
func (h *Handler) getImpersonatingClient(ctx context.Context, w http.ResponseWriter, clusterID string, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.ClusterRouter.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "clusterID", clusterID, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return nil, false
	}
	return client, true
}

// getTypedClient creates a typed Kubernetes clientset impersonating the user, routing to the
// correct cluster via ClusterRouter. Returns (nil, false) and writes an error response on failure.
func (h *Handler) getTypedClient(ctx context.Context, w http.ResponseWriter, clusterID string, user *auth.User) (*kubernetes.Clientset, bool) {
	cs, err := h.ClusterRouter.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create typed client", "clusterID", clusterID, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return nil, false
	}
	return cs, true
}

// canAccess checks if the user can access a cert-manager resource. The
// cluster routing comes from the request context so RBAC runs against the
// correct cluster (F#9). Pass ctx derived from the http.Request.
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool {
	clusterID := middleware.ClusterIDFromContext(ctx)
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx,
		clusterID,
		user.KubernetesUsername,
		user.KubernetesGroups,
		verb,
		"cert-manager.io",
		resource,
		namespace,
	)
	return err == nil && can
}

// auditLog writes an audit entry for a cert-manager action.
func (h *Handler) auditLog(r *http.Request, user *auth.User, action audit.Action, kind, ns, name string, result audit.Result) {
	if h.AuditLogger == nil {
		return
	}
	_ = h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         middleware.ClusterIDFromContext(r.Context()),
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      kind,
		ResourceNamespace: ns,
		ResourceName:      name,
		Result:            result,
	})
}

// namespacedResource is implemented by types that carry a Kubernetes namespace.
type namespacedResource interface {
	getNamespace() string
}

func (c Certificate) getNamespace() string { return c.Namespace }
func (i Issuer) getNamespace() string      { return i.Namespace }

// filterByRBAC returns only items the user can access in their respective namespaces.
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

// safeGo launches fn on the errgroup with panic recovery. A panic in an
// errgroup worker goroutine is NOT caught by chi's recovery middleware — it
// runs on a separate goroutine stack — and would terminate the whole process;
// errgroup itself only propagates *returned* errors, never panics. Converting
// a panic into an error lets g.Wait() surface it as an ordinary failure (a
// graceful 500 on the request path, a skipped fill on the poller path) rather
// than a crash. Mirrors the poller's runTickWithRecover defense for the
// request-path fan-out that normalizes adversarial CRD data.
func safeGo(g *errgroup.Group, logger *slog.Logger, label string, fn func() error) {
	g.Go(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Error("certmanager: panic recovered in errgroup worker",
						"worker", label, "panic", r)
				}
				err = fmt.Errorf("%s: recovered panic: %v", label, r)
			}
		}()
		return fn()
	})
}

func (h *Handler) fetchAll(ctx context.Context, gen uint64) (*cachedData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dynClient := h.K8sClient.BaseDynamicClient()

	var (
		certificates   []Certificate
		issuers        []Issuer
		clusterIssuers []Issuer
	)

	g, gctx := errgroup.WithContext(ctx)

	safeGo(g, h.Logger, "list certificates", func() error {
		list, err := dynClient.Resource(CertificateGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list certificates: %w", err)
		}
		certificates = make([]Certificate, 0, len(list.Items))
		for i := range list.Items {
			c, err := normalizeCertificate(&list.Items[i])
			if err != nil {
				continue
			}
			certificates = append(certificates, c)
		}
		return nil
	})

	safeGo(g, h.Logger, "list issuers", func() error {
		list, err := dynClient.Resource(IssuerGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list issuers: %w", err)
		}
		issuers = make([]Issuer, 0, len(list.Items))
		for i := range list.Items {
			issuers = append(issuers, normalizeIssuer(&list.Items[i], "Namespaced"))
		}
		return nil
	})

	safeGo(g, h.Logger, "list clusterissuers", func() error {
		list, err := dynClient.Resource(ClusterIssuerGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list clusterissuers: %w", err)
		}
		clusterIssuers = make([]Issuer, 0, len(list.Items))
		for i := range list.Items {
			clusterIssuers = append(clusterIssuers, normalizeIssuer(&list.Items[i], "Cluster"))
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Resolve per-cert thresholds against the just-fetched issuer set
	// before caching. Every read path (CachedCertificates, /expiring,
	// detail) consumes the cache, so doing it once here keeps the
	// resolution out of the hot path and ensures every consumer sees
	// the same view.
	ApplyThresholds(certificates, issuers, clusterIssuers, h.Logger)

	data := &cachedData{
		certificates:   certificates,
		issuers:        issuers,
		clusterIssuers: clusterIssuers,
		fetchedAt:      time.Now(),
	}

	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cache = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// fetchAllRemote pulls certificates, issuers, and clusterissuers directly
// from a remote cluster via an impersonating dynamic client.
//
// F#16 (round-2): wrapped by a 30s per-cluster cache + singleflight so a
// single page load that touches three remote endpoints (list certs, list
// issuers, get expiring) doesn't fan out into 3 sets of List calls against
// the remote API. The cache is keyed only on clusterID — RBAC filtering
// remains per-user-per-call via filterByRBAC, so the cached cluster-wide
// list is still safe to share across users. Per-user RBAC re-evaluation
// after the cache hit is what makes this safe; F#3's worry about RBAC
// drift is bounded by the 30s TTL (see cacheTTL).
//
// F#3 (round-3) — singleflight ctx-cancel poisoning fix. The previous
// implementation passed the FIRST caller's ctx straight into
// fetchAllRemoteDirect inside the shared Do() closure. If that caller's
// HTTP request was cancelled mid-flight (client disconnect, request
// timeout), every coalesced waiter saw the same context.Canceled error
// even though their own requests were still alive. Use context.WithoutCancel
// to preserve caller VALUES (request_id, trace span) while severing the
// cancel signal; cap at the caller's deadline if set, else default to
// 30s (matches cluster_router.remoteConfig — F#6 cap).
//
// Callers must already have verified the cluster is non-local via
// k8s.IsLocalClusterID. Returns (certs, issuers, clusterIssuers, err).
// F#3 — security audit 2026-05-22; F#16 — re-review; F#3/F#6 round-3.
func (h *Handler) fetchAllRemote(ctx context.Context, clusterID string, user *auth.User) ([]Certificate, []Issuer, []Issuer, error) {
	// Cache check
	h.remoteCacheMu.RLock()
	if cached, ok := h.remoteCache[clusterID]; ok && time.Since(cached.fetchedAt) < cacheTTL {
		h.remoteCacheMu.RUnlock()
		return cached.certificates, cached.issuers, cached.clusterIssuers, nil
	}
	h.remoteCacheMu.RUnlock()

	// Singleflight coalesce — concurrent first-loads share one round-trip.
	// Build a context that cannot be cancelled by any single caller's
	// disconnect (F#3 round-3) but still respects the caller's deadline
	// (and falls back to a 30s cap when no deadline was supplied — F#6).
	val, err, _ := h.remoteFetchGroup.Do(clusterID, func() (any, error) {
		sfCtx := context.WithoutCancel(ctx)
		var cancel context.CancelFunc
		if deadline, ok := ctx.Deadline(); ok {
			sfCtx, cancel = context.WithDeadline(sfCtx, deadline)
		} else {
			// F#6 — bound the no-deadline case so a hung remote API
			// doesn't pin the singleflight slot indefinitely. 30s
			// matches cluster_router.remoteConfig's default.
			sfCtx, cancel = context.WithTimeout(sfCtx, 30*time.Second)
		}
		defer cancel()

		certs, issuers, cissuers, ferr := h.fetchAllRemoteDirect(sfCtx, clusterID, user)
		if ferr != nil {
			return nil, ferr
		}
		entry := &cachedData{
			certificates:   certs,
			issuers:        issuers,
			clusterIssuers: cissuers,
			fetchedAt:      time.Now(),
		}
		h.remoteCacheMu.Lock()
		if h.remoteCache == nil {
			h.remoteCache = map[string]*cachedData{}
		}
		h.remoteCache[clusterID] = entry
		h.remoteCacheMu.Unlock()
		return entry, nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	d := val.(*cachedData)
	return d.certificates, d.issuers, d.clusterIssuers, nil
}

// fetchAllRemoteDirect is the un-cached, un-coalesced inner fetch. Exists
// so the cache-aware fetchAllRemote can compose it with singleflight.
func (h *Handler) fetchAllRemoteDirect(ctx context.Context, clusterID string, user *auth.User) ([]Certificate, []Issuer, []Issuer, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dyn, err := h.ClusterRouter.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dynamic client for cluster %s: %w", clusterID, err)
	}

	var (
		certificates   []Certificate
		issuers        []Issuer
		clusterIssuers []Issuer
	)

	g, gctx := errgroup.WithContext(ctx)

	safeGo(g, h.Logger, "list certificates", func() error {
		list, err := dyn.Resource(CertificateGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list certificates: %w", err)
		}
		certificates = make([]Certificate, 0, len(list.Items))
		for i := range list.Items {
			c, err := normalizeCertificate(&list.Items[i])
			if err != nil {
				continue
			}
			certificates = append(certificates, c)
		}
		return nil
	})

	safeGo(g, h.Logger, "list issuers", func() error {
		list, err := dyn.Resource(IssuerGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list issuers: %w", err)
		}
		issuers = make([]Issuer, 0, len(list.Items))
		for i := range list.Items {
			issuers = append(issuers, normalizeIssuer(&list.Items[i], "Namespaced"))
		}
		return nil
	})

	safeGo(g, h.Logger, "list clusterissuers", func() error {
		list, err := dyn.Resource(ClusterIssuerGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list clusterissuers: %w", err)
		}
		clusterIssuers = make([]Issuer, 0, len(list.Items))
		for i := range list.Items {
			clusterIssuers = append(clusterIssuers, normalizeIssuer(&list.Items[i], "Cluster"))
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, nil, nil, err
	}

	// Apply per-cert threshold resolution against the just-fetched issuer set,
	// so the response carries the same WarningThresholdDays / source fields
	// that the local cache path emits via ApplyThresholds.
	ApplyThresholds(certificates, issuers, clusterIssuers, h.Logger)

	return certificates, issuers, clusterIssuers, nil
}

// CachedCertificates returns the cached certificate list (for use by the Poller).
func (h *Handler) CachedCertificates(ctx context.Context) ([]Certificate, error) {
	data, err := h.getCached(ctx)
	if err != nil {
		return nil, err
	}
	return data.certificates, nil
}

// HandleStatus returns the cert-manager detection status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	_, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	httputil.WriteData(w, status)
}

// HandleListCertificates returns all cert-manager certificates, optionally filtered by namespace.
func (h *Handler) HandleListCertificates(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []Certificate{})
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())

	// F#3 — for non-local X-Cluster-ID, the cache (which is populated from the
	// local cluster's BaseDynamicClient) would return the wrong cluster's data.
	// Bypass it with a per-request dynamic.List against the remote cluster's
	// API. We accept the staleness/latency tradeoff (per-request remote round-
	// trip vs cached local) because returning wrong-cluster data silently is
	// strictly worse than serving uncached remote results.
	var certificates []Certificate
	if !k8s.IsLocalClusterID(clusterID) {
		remoteCerts, _, _, err := h.fetchAllRemote(r.Context(), clusterID, user)
		if err != nil {
			h.Logger.Error("failed to fetch certificates from remote cluster", "clusterID", clusterID, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch certificates", "")
			return
		}
		certificates = remoteCerts
	} else {
		data, err := h.getCached(r.Context())
		if err != nil {
			h.Logger.Error("failed to fetch certificates", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch certificates", "")
			return
		}
		certificates = data.certificates
	}

	filtered := filterByRBAC(r.Context(), h, user, "certificates", certificates)

	// Optional namespace filter
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		nsFiltered := make([]Certificate, 0, len(filtered))
		for _, c := range filtered {
			if c.Namespace == ns {
				nsFiltered = append(nsFiltered, c)
			}
		}
		filtered = nsFiltered
	}

	httputil.WriteData(w, filtered)
}

// HandleGetCertificate returns a single certificate with its sub-resources.
func (h *Handler) HandleGetCertificate(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "certificates", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)

	dynClient, ok := h.getImpersonatingClient(ctx, w, clusterID, user)
	if !ok {
		return
	}

	// Fetch the certificate
	certObj, err := dynClient.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get certificate", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "certificate not found", "")
		return
	}

	cert, err := normalizeCertificate(certObj)
	if err != nil {
		h.Logger.Error("failed to normalize certificate", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to parse certificate", "")
		return
	}

	// Resolve per-cert thresholds for the detail response so it carries
	// the same WarningThresholdDays / CriticalThresholdDays / source
	// fields that the list endpoints emit AND so DeriveStatus runs
	// (without it the response would never show Status="Expiring",
	// since computeStatus no longer overlays Expiring).
	//
	// F#9 (round-2): for non-local cluster IDs the local cache holds
	// local-cluster issuers — applying those thresholds to a remote
	// certificate would attribute the wrong issuer entirely. Route remote
	// detail requests through fetchAllRemote so threshold resolution sees
	// the remote cluster's own Issuers / ClusterIssuers. Local clusters
	// continue to hit the cache. Cache miss for local falls through to
	// defaults uniformly — same as before.
	var issuers, clusterIssuers []Issuer
	if !k8s.IsLocalClusterID(clusterID) {
		_, remoteIssuers, remoteCIssuers, ferr := h.fetchAllRemote(ctx, clusterID, user)
		if ferr != nil {
			// Don't fail the detail response on issuer-fetch error —
			// threshold attribution falls through to package defaults
			// just like a local cache miss. Log so the gap is visible.
			h.Logger.Warn("remote issuer fetch for cert detail failed; thresholds will fall back to defaults",
				"clusterID", clusterID, "namespace", ns, "name", name, "error", ferr)
		} else {
			issuers = remoteIssuers
			clusterIssuers = remoteCIssuers
		}
	} else if data, derr := h.getCached(ctx); derr == nil && data != nil {
		issuers = data.issuers
		clusterIssuers = data.clusterIssuers
	}
	certs := []Certificate{cert}
	ApplyThresholds(certs, issuers, clusterIssuers, h.Logger)
	cert = certs[0]

	// Fetch CertificateRequests for this certificate
	crSel := labels.Set{"cert-manager.io/certificate-name": name}.String()

	// Fetch CRs, Orders, Challenges in parallel
	g, gCtx := errgroup.WithContext(ctx)

	var crList *unstructured.UnstructuredList
	g.Go(func() error {
		var fetchErr error
		crList, fetchErr = dynClient.Resource(CertificateRequestGVR).Namespace(ns).List(gCtx, metav1.ListOptions{
			LabelSelector: crSel,
		})
		return fetchErr
	})

	var orderList *unstructured.UnstructuredList
	g.Go(func() error {
		var fetchErr error
		orderList, fetchErr = dynClient.Resource(OrderGVR).Namespace(ns).List(gCtx, metav1.ListOptions{})
		return fetchErr
	})

	var challengeList *unstructured.UnstructuredList
	g.Go(func() error {
		var fetchErr error
		challengeList, fetchErr = dynClient.Resource(ChallengeGVR).Namespace(ns).List(gCtx, metav1.ListOptions{})
		return fetchErr
	})

	if err := g.Wait(); err != nil {
		h.Logger.Debug("sub-resource fetch partial failure", "error", err)
	}

	// Process CertificateRequests
	var certRequests []CertificateRequest
	crUIDs := make(map[string]bool)
	if crList != nil {
		certRequests = make([]CertificateRequest, 0, len(crList.Items))
		for i := range crList.Items {
			cr := normalizeCertRequest(&crList.Items[i])
			certRequests = append(certRequests, cr)
			crUIDs[cr.UID] = true
		}
	} else {
		certRequests = []CertificateRequest{}
	}

	// Filter Orders owned by the CertificateRequests
	var orders []Order
	orderUIDs := make(map[string]bool)
	if orderList != nil {
		orders = make([]Order, 0)
		for i := range orderList.Items {
			owners := orderList.Items[i].GetOwnerReferences()
			for _, ref := range owners {
				if crUIDs[string(ref.UID)] {
					o := normalizeOrder(&orderList.Items[i])
					orders = append(orders, o)
					orderUIDs[o.UID] = true
					break
				}
			}
		}
	} else {
		orders = []Order{}
	}

	// Filter Challenges owned by the Orders
	var challenges []Challenge
	if challengeList != nil {
		challenges = make([]Challenge, 0)
		for i := range challengeList.Items {
			owners := challengeList.Items[i].GetOwnerReferences()
			for _, ref := range owners {
				if orderUIDs[string(ref.UID)] {
					challenges = append(challenges, normalizeChallenge(&challengeList.Items[i]))
					break
				}
			}
		}
	} else {
		challenges = []Challenge{}
	}

	detail := CertificateDetail{
		Certificate:         cert,
		CertificateRequests: certRequests,
		Orders:              orders,
		Challenges:          challenges,
	}

	httputil.WriteData(w, detail)
}

// HandleListIssuers returns all namespaced issuers.
func (h *Handler) HandleListIssuers(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []Issuer{})
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())

	// F#3 — bypass local cache for non-local clusters; see HandleListCertificates.
	var issuers []Issuer
	if !k8s.IsLocalClusterID(clusterID) {
		_, remoteIssuers, _, err := h.fetchAllRemote(r.Context(), clusterID, user)
		if err != nil {
			h.Logger.Error("failed to fetch issuers from remote cluster", "clusterID", clusterID, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch issuers", "")
			return
		}
		issuers = remoteIssuers
	} else {
		data, err := h.getCached(r.Context())
		if err != nil {
			h.Logger.Error("failed to fetch issuers", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch issuers", "")
			return
		}
		issuers = data.issuers
	}

	filtered := filterByRBAC(r.Context(), h, user, "issuers", issuers)
	httputil.WriteData(w, filtered)
}

// HandleListClusterIssuers returns all cluster-scoped issuers.
func (h *Handler) HandleListClusterIssuers(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []Issuer{})
		return
	}

	// Cluster-scoped RBAC check
	if !h.canAccess(r.Context(), user, "get", "clusterissuers", "") {
		httputil.WriteData(w, []Issuer{})
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())

	// F#3 — bypass local cache for non-local clusters; see HandleListCertificates.
	if !k8s.IsLocalClusterID(clusterID) {
		_, _, remoteClusterIssuers, err := h.fetchAllRemote(r.Context(), clusterID, user)
		if err != nil {
			h.Logger.Error("failed to fetch cluster issuers from remote cluster", "clusterID", clusterID, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch cluster issuers", "")
			return
		}
		httputil.WriteData(w, remoteClusterIssuers)
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch cluster issuers", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch cluster issuers", "")
		return
	}

	httputil.WriteData(w, data.clusterIssuers)
}

// HandleListExpiring returns certificates expiring within the warning threshold,
// sorted by days remaining ascending.
func (h *Handler) HandleListExpiring(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []ExpiringCertificate{})
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())

	// F#3 — bypass local cache for non-local clusters; see HandleListCertificates.
	var certificates []Certificate
	if !k8s.IsLocalClusterID(clusterID) {
		remoteCerts, _, _, err := h.fetchAllRemote(r.Context(), clusterID, user)
		if err != nil {
			h.Logger.Error("failed to fetch certificates from remote cluster", "clusterID", clusterID, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch certificates", "")
			return
		}
		certificates = remoteCerts
	} else {
		data, err := h.getCached(r.Context())
		if err != nil {
			h.Logger.Error("failed to fetch certificates", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch certificates", "")
			return
		}
		certificates = data.certificates
	}

	certs := filterByRBAC(r.Context(), h, user, "certificates", certificates)

	expiring := make([]ExpiringCertificate, 0)
	for _, c := range certs {
		// ApplyThresholds (during the cache fetch) resolved each cert's
		// effective warn/crit. effectiveWarn / effectiveCrit fall back
		// to package defaults if the resolution hasn't happened yet.
		warn := effectiveWarn(c)
		crit := effectiveCrit(c)
		if c.DaysRemaining == nil || *c.DaysRemaining > warn {
			continue
		}
		severity := "warning"
		if *c.DaysRemaining <= crit {
			severity = "critical"
		}
		var notAfter time.Time
		if c.NotAfter != nil {
			notAfter = *c.NotAfter
		}
		expiring = append(expiring, ExpiringCertificate{
			Namespace:     c.Namespace,
			Name:          c.Name,
			UID:           c.UID,
			IssuerName:    c.IssuerRef.Name,
			SecretName:    c.SecretName,
			NotAfter:      notAfter,
			DaysRemaining: *c.DaysRemaining,
			Severity:      severity,
		})
	}

	sort.Slice(expiring, func(i, j int) bool {
		return expiring[i].DaysRemaining < expiring[j].DaysRemaining
	})

	httputil.WriteData(w, expiring)
}

// HandleRenew triggers certificate renewal by setting the Issuing condition on the status subresource.
func (h *Handler) HandleRenew(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)

	// RBAC pre-check
	if !h.canAccess(ctx, user, "patch", "certificates", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(ctx, w, clusterID, user)
	if !ok {
		return
	}

	var lastErr error
	for attempt := 0; attempt <= maxRenewRetries; attempt++ {
		lastErr = h.doRenew(ctx, dynClient, ns, name)
		if lastErr == nil {
			break
		}
		h.Logger.Warn("renew attempt failed", "attempt", attempt, "error", lastErr)
	}

	if lastErr != nil {
		h.Logger.Error("failed to renew certificate", "namespace", ns, "name", name, "error", lastErr)
		h.auditLog(r, user, audit.ActionCertRenew, "Certificate", ns, name, audit.ResultFailure)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to renew certificate", "")
		return
	}

	h.auditLog(r, user, audit.ActionCertRenew, "Certificate", ns, name, audit.ResultSuccess)
	h.InvalidateCache()

	w.WriteHeader(http.StatusAccepted)
	httputil.WriteData(w, map[string]string{"status": "renewing"})
}

// doRenew performs one attempt at setting the Issuing=True condition on the certificate status.
func (h *Handler) doRenew(ctx context.Context, dynClient dynamic.Interface, ns, name string) error {
	cert, err := dynClient.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get certificate: %w", err)
	}

	generation := cert.GetGeneration()
	now := time.Now().UTC().Format(time.RFC3339)

	// Read existing status.conditions
	statusMap, _ := cert.Object["status"].(map[string]any)
	if statusMap == nil {
		statusMap = map[string]any{}
		cert.Object["status"] = statusMap
	}

	conditions, _ := statusMap["conditions"].([]any)

	// Upsert Issuing condition
	found := false
	for i, c := range conditions {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cm["type"].(string); t == issuingCondType {
			found = true
			existingStatus, _ := cm["status"].(string)
			if existingStatus != "True" {
				cm["status"] = "True"
				cm["lastTransitionTime"] = now
			}
			// Always update reason, message, and observedGeneration
			cm["reason"] = issuingReason
			cm["message"] = issuingMessage
			cm["observedGeneration"] = generation
			conditions[i] = cm
			break
		}
	}

	if !found {
		conditions = append(conditions, map[string]any{
			"type":               issuingCondType,
			"status":             "True",
			"reason":             issuingReason,
			"message":            issuingMessage,
			"lastTransitionTime": now,
			"observedGeneration": generation,
		})
	}

	statusMap["conditions"] = conditions

	_, err = dynClient.Resource(CertificateGVR).Namespace(ns).UpdateStatus(ctx, cert, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	return nil
}

// HandleReissue forces certificate re-issuance by deleting the backing Secret.
func (h *Handler) HandleReissue(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)

	// RBAC pre-check — need delete on secrets
	if !h.canAccess(ctx, user, "delete", "secrets", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(ctx, w, clusterID, user)
	if !ok {
		return
	}

	// GET the Certificate
	certObj, err := dynClient.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get certificate", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "certificate not found", "")
		return
	}

	// Extract spec.secretName
	secretName, _, _ := unstructured.NestedString(certObj.Object, "spec", "secretName")
	if secretName == "" {
		httputil.WriteError(w, http.StatusBadRequest, "certificate has no secretName", "")
		return
	}

	certUID := string(certObj.GetUID())

	// Get the typed clientset for secret operations, routed to the correct cluster
	cs, ok := h.getTypedClient(ctx, w, clusterID, user)
	if !ok {
		return
	}

	// GET the Secret
	secret, err := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get secret", "namespace", ns, "name", secretName, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "backing secret not found", "")
		return
	}

	// Validate ownership: check Secret's ownerReferences for the Certificate's UID
	owned := false
	for _, ref := range secret.OwnerReferences {
		if string(ref.UID) == certUID {
			owned = true
			break
		}
	}
	if !owned {
		httputil.WriteError(w, http.StatusBadRequest, "secret not owned by this certificate", "")
		return
	}

	// Delete the Secret
	if err := cs.CoreV1().Secrets(ns).Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil {
		h.Logger.Error("failed to delete secret", "namespace", ns, "name", secretName, "error", err)
		h.auditLog(r, user, audit.ActionCertReissue, "Certificate", ns, name, audit.ResultFailure)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete backing secret", "")
		return
	}

	h.auditLog(r, user, audit.ActionCertReissue, "Certificate", ns, name, audit.ResultSuccess)
	h.InvalidateCache()

	w.WriteHeader(http.StatusAccepted)
	httputil.WriteData(w, map[string]string{"status": "reissuing"})
}
