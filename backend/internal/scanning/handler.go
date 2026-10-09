package scanning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// Handler serves security scanning HTTP endpoints for the cluster a request
// selects. Every vulnerability read impersonates the requesting user
// through Clients, on the local cluster and a remote one alike, so a read is
// exactly what the access reviews authorised. Scanner presence comes from
// the local Discoverer for the local cluster and from Presence, as the user,
// for a remote one: the local Discoverer is never consulted for a remote
// selection, and a remote read never falls back to the local cluster.
type Handler struct {
	Discoverer    *ScannerDiscoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger
	// Clients resolves the impersonated client for the request's cluster.
	// Nil means scanning reads are not wired; they answer 500.
	Clients k8s.ClusterClients
	// Presence answers whether a remote cluster serves a scanner's CRD, as
	// the requesting identity sees it.
	Presence *k8s.Presence

	fetchGroup  singleflight.Group
	cacheMu     sync.RWMutex
	cacheGen    uint64                         // bumped by every invalidation; guarded by cacheMu
	nsCache     map[cacheKey]*cachedNSData     // namespace-scoped summary cache
	detailCache map[cacheKey]*cachedDetailData // per-workload detail cache
}

// cacheKey scopes a cached read to the cluster it came from and the identity
// it was read as. Reads are impersonated, so two identities can see
// different reports on one cluster, and one cluster never serves another's.
type cacheKey struct {
	cluster  string // k8s.NormalizedClusterID
	identity string // k8s.IdentityKey
	scope    string // what was read, e.g. "vulns:<ns>:<scanners>"
}

func newCacheKey(clusterID string, user *auth.User, scope string) cacheKey {
	return cacheKey{
		cluster:  k8s.NormalizedClusterID(clusterID),
		identity: k8s.IdentityKey(user.KubernetesUsername, user.KubernetesGroups),
		scope:    scope,
	}
}

// flightKey is the singleflight key for k at cache generation gen, so a
// request after an invalidation never joins a read of the old state.
func (k cacheKey) flightKey(gen uint64) string {
	return strconv.FormatUint(gen, 10) + "\x00" + k.cluster + "\x00" + k.identity + "\x00" + k.scope
}

type cachedNSData struct {
	vulns     []WorkloadVulnSummary
	fetchedAt time.Time
}

type cachedDetailData struct {
	detail    *WorkloadVulnDetail
	fetchedAt time.Time
}

const (
	cacheTTL              = 30 * time.Second
	cacheMaxEntries       = 200 // evict oldest entries beyond this
	detailCacheMaxEntries = 200
	// readTimeout bounds one vulnerability read. Healthy apiservers answer a
	// namespaced list in well under a second; a degraded one tying up
	// handler goroutines amplifies a partial outage into a full one.
	readTimeout = 10 * time.Second
)

var validNamespace = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// errFetchPanicked is a scanner read whose goroutine panicked; recoverutil
// logs the panic itself.
var errFetchPanicked = errors.New("scanner read panicked")

// scannerSet names the scanners a read covers.
type scannerSet struct {
	trivy, kubescape bool
}

func (s scannerSet) String() string {
	return "trivy=" + strconv.FormatBool(s.trivy) + ",kubescape=" + strconv.FormatBool(s.kubescape)
}

// InitCache must be called after construction to initialize the cache maps.
// This avoids lazy init under locks.
func (h *Handler) InitCache() {
	h.nsCache = make(map[cacheKey]*cachedNSData)
	h.detailCache = make(map[cacheKey]*cachedDetailData)
}

// EvictRemoteCache drops every identity's cached scan data for clusterID and
// keeps reads already in flight from caching what they return. Register it
// as a ClusterRouter evict hook so a deleted or re-registered cluster is
// never answered from cache.
func (h *Handler) EvictRemoteCache(clusterID string) {
	clusterID = k8s.NormalizedClusterID(clusterID)
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	h.cacheGen++
	for k := range h.nsCache {
		if k.cluster == clusterID {
			delete(h.nsCache, k)
		}
	}
	for k := range h.detailCache {
		if k.cluster == clusterID {
			delete(h.detailCache, k)
		}
	}
}

// fetchVulns returns the vulnerability summaries of namespace on clusterID as
// the user, from cache when fresh. Only the scanners in want are read. A
// failure is never cached.
func (h *Handler) fetchVulns(ctx context.Context, clusterID string, user *auth.User, namespace string, want scannerSet) ([]WorkloadVulnSummary, error) {
	key := newCacheKey(clusterID, user, "vulns:"+namespace+":"+want.String())

	h.cacheMu.RLock()
	if entry := h.nsCache[key]; entry != nil && time.Since(entry.fetchedAt) < cacheTTL {
		vulns := entry.vulns
		h.cacheMu.RUnlock()
		return vulns, nil
	}
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	result, err := h.sharedFetch(ctx, key.flightKey(gen), func(ctx context.Context) (any, error) {
		vulns, err := h.doFetchNS(ctx, clusterID, user, namespace, want)
		if err != nil {
			return nil, err
		}
		data := &cachedNSData{vulns: vulns, fetchedAt: time.Now()}
		h.cacheMu.Lock()
		// An invalidation while this read ran means it may hold the old
		// state: answer the callers, but do not cache it.
		if h.cacheGen == gen {
			h.nsCache[key] = data
			if len(h.nsCache) > cacheMaxEntries {
				h.evictOldestLocked()
			}
		}
		h.cacheMu.Unlock()
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedNSData).vulns, nil
}

// sharedFetch runs fetch once for every concurrent caller of key and
// returns its result. The shared read runs on a context that keeps ctx's
// values but not its cancellation, bounded by readTimeout instead, and each
// caller waits on its own ctx: one caller disconnecting or timing out returns
// ctx.Err() to that caller only, never failing the others coalesced onto the
// read. This is the contract remotecache.Cache.Get keeps. singleflight
// re-panics a panicking fetch on a goroutine no middleware recovers, so the
// fetch runs under recoverutil.
func (h *Handler) sharedFetch(ctx context.Context, key string, fetch func(context.Context) (any, error)) (any, error) {
	ch := h.fetchGroup.DoChan(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readTimeout)
		defer cancel()
		var (
			v   any
			err = errFetchPanicked
		)
		recoverutil.Safe(h.Logger, "scanning shared fetch", func() {
			v, err = fetch(fetchCtx)
		})
		return v, err
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.Val, res.Err
	}
}

// doFetchNS reads the wanted scanners' reports in namespace on clusterID as
// the user and merges them. A scanner whose CRD turns out to be gone is
// skipped; any other failure fails the read, so a broken read never renders
// as a clean scan.
func (h *Handler) doFetchNS(ctx context.Context, clusterID string, user *auth.User, namespace string, want scannerSet) ([]WorkloadVulnSummary, error) {
	var allVulns []WorkloadVulnSummary
	if !want.trivy && !want.kubescape {
		return allVulns, nil
	}

	dynClient, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	type fetchResult struct {
		vulns []WorkloadVulnSummary
		err   error
	}
	start := func(enabled bool, label string, list func(context.Context, dynamic.Interface, string) ([]WorkloadVulnSummary, error)) <-chan fetchResult {
		ch := make(chan fetchResult, 1)
		if !enabled {
			ch <- fetchResult{}
			return ch
		}
		go func() {
			r := fetchResult{err: errFetchPanicked}
			recoverutil.Safe(h.Logger, label, func() {
				r.vulns, r.err = list(ctx, dynClient, namespace)
			})
			ch <- r
		}()
		return ch
	}
	trivyCh := start(want.trivy, "scanning trivy-fetch", ListTrivyVulnSummaries)
	kubescapeCh := start(want.kubescape, "scanning kubescape-fetch", ListKubescapeVulnSummaries)
	results := []struct {
		scanner Scanner
		gvr     schema.GroupVersionResource
		fetchResult
	}{
		{ScannerTrivy, trivyVulnReportGVR, <-trivyCh},
		{ScannerKubescape, kubescapeVulnSummaryGVR, <-kubescapeCh},
	}

	for _, res := range results {
		if res.err == nil {
			allVulns = append(allVulns, res.vulns...)
			continue
		}
		if h.scannerGone(ctx, clusterID, user, res.gvr, res.err) {
			h.Logger.Warn("scanner CRD no longer served; skipping it", "scanner", res.scanner, "cluster", clusterID, "namespace", namespace)
			continue
		}
		h.Logger.Warn("scanner fetch error", "scanner", res.scanner, "cluster", clusterID, "namespace", namespace, "error", res.err)
		return nil, res.err
	}
	return allVulns, nil
}

// evictOldestLocked removes the oldest cache entry. Must be called under write lock.
func (h *Handler) evictOldestLocked() {
	var oldestKey cacheKey
	var oldestTime time.Time
	found := false
	for k, v := range h.nsCache {
		if !found || v.fetchedAt.Before(oldestTime) {
			oldestKey, oldestTime, found = k, v.fetchedAt, true
		}
	}
	if found {
		delete(h.nsCache, oldestKey)
	}
}

// HandleStatus returns the security scanner detection status of the
// request's cluster.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	var status ScannerStatus
	if isLocal(r.Context()) {
		status = h.Discoverer.Status()
	} else {
		if !h.configured(w) {
			return
		}
		var err error
		if status, err = h.remoteStatus(r.Context(), user); err != nil {
			h.Logger.Warn("remote scanner presence unknown", "cluster", middleware.ClusterIDFromContext(r.Context()), "error", err)
			httputil.WriteRemoteLoadError(w, err, remoteFeature)
			return
		}
	}

	// Strip namespace details for non-admin users
	if !auth.IsAdmin(user) {
		if status.Trivy != nil {
			stripped := *status.Trivy
			stripped.Namespace = ""
			status.Trivy = &stripped
		}
		if status.Kubescape != nil {
			stripped := *status.Kubescape
			stripped.Namespace = ""
			status.Kubescape = &stripped
		}
	}

	httputil.WriteData(w, status)
}

// HandleVulnerabilities returns vulnerability summaries for a namespace.
func (h *Handler) HandleVulnerabilities(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		httputil.WriteError(w, http.StatusBadRequest, "namespace parameter required", "")
		return
	}

	// Validate namespace format
	if !validNamespace.MatchString(namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace", "")
		return
	}

	if !h.configured(w) {
		return
	}

	// RBAC: check per-scanner access. Only the scanners the user may list
	// are read, as the user, so no scanner's results need filtering after.
	canTrivy, terr := h.canAccessTrivy(r.Context(), user, namespace)
	if terr != nil {
		h.Logger.Error("scanning RBAC check failed", "scanner", "trivy", "namespace", namespace, "error", terr)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}
	canKubescape, kerr := h.canAccessKubescape(r.Context(), user, namespace)
	if kerr != nil {
		h.Logger.Error("scanning RBAC check failed", "scanner", "kubescape", "namespace", namespace, "error", kerr)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}

	if !canTrivy && !canKubescape {
		httputil.WriteError(w, http.StatusForbidden,
			fmt.Sprintf("access denied to scanning data in namespace %q", namespace), "")
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	present, err := h.scannersPresent(r.Context(), user, scannerSet{trivy: canTrivy, kubescape: canKubescape})
	if err != nil {
		h.Logger.Warn("remote scanner presence unknown", "cluster", clusterID, "error", err)
		httputil.WriteRemoteLoadError(w, err, remoteFeature)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	vulns, err := h.fetchVulns(ctx, clusterID, user, namespace, present)
	if err != nil {
		h.Logger.Error("failed to fetch vulnerabilities", "cluster", clusterID, "namespace", namespace, "error", err)
		h.writeReadError(w, r, err, "failed to fetch vulnerabilities")
		return
	}

	httputil.WriteData(w, struct {
		Vulnerabilities []WorkloadVulnSummary `json:"vulnerabilities"`
		Summary         VulnListMetadata      `json:"summary"`
	}{
		Vulnerabilities: vulns,
		Summary:         computeMetadata(vulns),
	})
}

// canAccessTrivy checks if the user can list Trivy VulnerabilityReports in the namespace.
//
// The error is RETURNED, not folded into the bool. `err == nil && can` reads a
// check that could not run as a refusal, and a refusal on both scanners is
// indistinguishable at the widget from a namespace with nothing to report —
// so a broken access review renders as a clean scan on the one card whose
// whole job is to say otherwise.
func (h *Handler) canAccessTrivy(ctx context.Context, user *auth.User, namespace string) (bool, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups,
		"list", "aquasecurity.github.io", "vulnerabilityreports", namespace,
	)
	if err != nil {
		return false, fmt.Errorf("trivy access review failed for namespace %q: %w", namespace, err)
	}
	return can, nil
}

// canAccessKubescape checks if the user can list Kubescape VulnerabilitySummaries in the namespace.
// See canAccessTrivy: a review that did not answer is not a denial.
func (h *Handler) canAccessKubescape(ctx context.Context, user *auth.User, namespace string) (bool, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups,
		"list", "spdx.softwarecomposition.org", "vulnerabilitysummaries", namespace,
	)
	if err != nil {
		return false, fmt.Errorf("kubescape access review failed for namespace %q: %w", namespace, err)
	}
	return can, nil
}

// computeMetadata builds summary counts for the vulnerability list response.
func computeMetadata(vulns []WorkloadVulnSummary) VulnListMetadata {
	m := VulnListMetadata{Total: len(vulns)}
	for _, v := range vulns {
		m.Severity.Critical += v.Total.Critical
		m.Severity.High += v.Total.High
		m.Severity.Medium += v.Total.Medium
		m.Severity.Low += v.Total.Low
	}
	return m
}
