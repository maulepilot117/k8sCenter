package certmanager

// Remote-cluster reads and the helpers every handler uses to act on the
// cluster a request selects (R-8, #531). The local cluster keeps its
// service-account cache in handler.go and its Discoverer in discovery.go;
// whether cert-manager is installed on a remote cluster is decided by that
// cluster's own discovery, never by the local one.

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// remoteSnapshot is one remote cluster's cert-manager state as one identity
// sees it.
type remoteSnapshot struct {
	// data is nil when cert-manager is not installed on the cluster.
	data *cachedData
	// failed holds the error each failed list returned, keyed by resource
	// name. A list whose resource type is gone counts as empty, not failed.
	failed map[string]error
}

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
}

// EvictRemoteCache drops every identity's cached view of clusterID.
// Registered as a ClusterRouter evict hook, so a deleted or re-registered
// cluster never serves its previous lists.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remote.EvictCluster(clusterID)
	h.peeked.evict(clusterID)
}

// load returns the request cluster's certificates and issuers, or nil data
// when cert-manager is not installed there: the service-account cache for
// the local cluster, or a per-identity read of a remote one. On a remote
// cluster failed holds each list that failed while the others were read.
func (h *Handler) load(ctx context.Context, user *auth.User) (data *cachedData, failed map[string]error, err error) {
	if isLocal(ctx) {
		if !h.Discoverer.IsAvailable(ctx) {
			return nil, nil, nil
		}
		data, err := h.getCached(ctx)
		return data, nil, err
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	snap, err := h.remote.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*remoteSnapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
	if err != nil {
		return nil, nil, err
	}
	return snap.data, snap.failed, nil
}

// loadList loads the request cluster's lists for an endpoint that serves the
// gvr list, writing the error response and returning false when that list
// could not be read. Nil data means cert-manager is not installed.
func (h *Handler) loadList(w http.ResponseWriter, r *http.Request, user *auth.User, gvr schema.GroupVersionResource, what string) (*cachedData, bool) {
	data, failed, err := h.load(r.Context(), user)
	if err == nil && data != nil {
		err = failed[gvr.Resource]
	}
	if err != nil {
		h.writeLoadError(w, r, err, what)
		return nil, false
	}
	return data, true
}

// remoteInstalled reports whether cert-manager is installed on a remote
// cluster, as the user sees it. When that cannot be told, the error says why.
func (h *Handler) remoteInstalled(ctx context.Context, clusterID string, user *auth.User) (bool, error) {
	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so cert-manager installed later is seen
	// within seconds rather than when the schema cache expires.
	verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, CertificateGVR.GroupResource())
	if verdict.Installed != nil {
		return *verdict.Installed, nil
	}
	// The verdict carries only a reason; resolve the target again for the
	// error the response is built from.
	if _, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups); err != nil {
		return false, k8s.TargetError{Err: err}
	}
	return false, k8s.ErrDiscoveryUnavailable
}

// remoteStatus answers the status route for a remote cluster. A failure to
// tell is a status with a reason, not an error (R-8 KTD5). Namespace and
// version are not probed on a remote cluster.
func (h *Handler) remoteStatus(ctx context.Context, user *auth.User) CertManagerStatus {
	status := CertManagerStatus{LastChecked: time.Now().UTC()}
	installed, err := h.remoteInstalled(ctx, middleware.ClusterIDFromContext(ctx), user)
	switch {
	case err != nil:
		status.Reason = string(k8s.RemoteReason(err))
	case !installed:
		status.Reason = string(k8s.ReasonDiscoveryMissing)
	default:
		status.Detected = true
	}
	return status
}

// fetchRemote reads a remote cluster's certificates, issuers and
// clusterissuers as the user, or an empty snapshot when cert-manager is not
// installed there. The lists run as the user, who may be allowed some kinds
// and not others (a namespace tenant rarely may list cluster-scoped
// ClusterIssuers), so each list succeeds or fails on its own; only when
// every list fails is the fetch itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*remoteSnapshot, error) {
	installed, err := h.remoteInstalled(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	if !installed {
		return &remoteSnapshot{}, nil
	}
	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	data := newCachedData()
	sources := data.sources()
	lists := make([]k8s.NamedList, len(sources))
	for i, src := range sources {
		lists[i] = k8s.NamedList{Label: "certmanager remote list " + src.gvr.Resource, Run: func() error { return src.list(listCtx, dyn) }}
	}
	errs := k8s.RunLists(h.Logger, lists)

	failed := map[string]error{}
	for i, src := range sources {
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			// The CRD went away after discovery was cached: re-read it so the
			// next fetch stops asking for it, and report cert-manager not
			// installed when the Certificate type itself is confirmed gone.
			v := h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, src.gvr.GroupResource())
			if src.gvr == CertificateGVR && v.Installed != nil && !*v.Installed {
				return &remoteSnapshot{}, nil
			}
		default:
			failed[src.gvr.Resource] = err
		}
	}
	if len(failed) == len(sources) {
		return nil, errs[0]
	}
	data.resolve(h.Logger)
	return &remoteSnapshot{data: data, failed: failed}, nil
}

// writeLoadError answers a failure to read the cluster's cert-manager lists.
// The local cluster keeps its historical 500; a remote failure is classified
// without leaking its text.
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, what string) {
	if isLocal(r.Context()) {
		h.Logger.Error("failed to fetch "+what, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch "+what, "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, "cert-manager")
}

// writeClientError answers a failure to resolve a client for the request's
// cluster.
func (h *Handler) writeClientError(w http.ResponseWriter, r *http.Request, err error) {
	if isLocal(r.Context()) {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	httputil.WriteTargetError(w, err)
}

// writeClusterError answers a failed call to the request's cluster: the
// local cluster keeps its historical status and message, a remote cluster's
// error is classified without leaking its text.
func (h *Handler) writeClusterError(w http.ResponseWriter, r *http.Request, err error, localStatus int, msg string) {
	h.Logger.Error(msg, "error", err)
	if isLocal(r.Context()) {
		httputil.WriteError(w, localStatus, msg, "")
		return
	}
	httputil.WriteRemoteError(w, err)
}

// writeGetError answers a failed read of one named object. Every local
// failure keeps its historical 404; on a remote cluster only a NotFound is
// one, and anything else is classified without leaking its text.
func (h *Handler) writeGetError(w http.ResponseWriter, r *http.Request, err error, notFoundMsg string) {
	h.Logger.Error(notFoundMsg, "error", err)
	if isLocal(r.Context()) || apierrors.IsNotFound(err) {
		httputil.WriteError(w, http.StatusNotFound, notFoundMsg, "")
		return
	}
	httputil.WriteRemoteError(w, err)
}

// dynamicClient returns a dynamic client impersonating the user on the
// request's cluster, writing the error response when it cannot.
func (h *Handler) dynamicClient(w http.ResponseWriter, r *http.Request, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.Clients.DynamicClientForCluster(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.writeClientError(w, r, err)
		return nil, false
	}
	return client, true
}

// typedClient is dynamicClient for the typed clientset.
func (h *Handler) typedClient(w http.ResponseWriter, r *http.Request, user *auth.User) (kubernetes.Interface, bool) {
	cs, err := h.Clients.ClientForCluster(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.writeClientError(w, r, err)
		return nil, false
	}
	return cs, true
}

// afterWrite makes the next read see a write: the local cache is dropped,
// or on a remote cluster, which sends no informer events, every identity's
// cached view of it (R-8 KTD7). The notification names the cluster that was
// written.
func (h *Handler) afterWrite(ctx context.Context) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	if k8s.IsLocalClusterID(clusterID) {
		h.cacheMu.Lock()
		h.cacheGen++
		h.cache = nil
		h.cacheMu.Unlock()
	} else {
		h.EvictRemoteCache(clusterID)
	}

	if h.NotifService != nil {
		go recoverutil.Safe(h.Logger, "certmanager notify", func() {
			h.NotifService.Emit(context.Background(), notifications.Notification{
				Source:    notifications.SourceCertManager,
				Severity:  notifications.SeverityInfo,
				Title:     "cert-manager data updated",
				Message:   "Certificate or issuer data has changed",
				ClusterID: clusterID,
			})
		})
	}
}

// maxPeekEntries bounds peeked so a burst of distinct (cluster, identity)
// pairs cannot grow it without limit.
const maxPeekEntries = 128

// peekKey names one identity's view of one cluster.
type peekKey struct {
	clusterID string // normalized
	identity  string // k8s.IdentityKey(username, groups)
}

func newPeekKey(clusterID string, user *auth.User) peekKey {
	return peekKey{clusterID: k8s.NormalizedClusterID(clusterID), identity: k8s.IdentityKey(user.KubernetesUsername, user.KubernetesGroups)}
}

type peekEntry struct {
	snap      *remoteSnapshot
	fetchedAt time.Time
}

// remotePeek is the last remote snapshot per (cluster, identity), kept up to
// peekStaleness. The zero value is usable. A refresh records the cluster's
// generation when it starts and stores only if no eviction happened since,
// so a write or a cluster removal is never masked by a refresh that read the
// old state.
type remotePeek struct {
	mu       sync.Mutex
	entries  map[peekKey]peekEntry
	fetching map[peekKey]*peekRefresh
	gens     map[string]uint64
}

// get returns the snapshot for k and its age, when one is within peekStaleness.
func (p *remotePeek) get(k peekKey, now time.Time) (*remoteSnapshot, time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[k]
	if !ok || now.Sub(e.fetchedAt) >= peekStaleness {
		return nil, 0, false
	}
	return e.snap, now.Sub(e.fetchedAt), true
}

// put stores snap for k as fetched at fetchedAt.
func (p *remotePeek) put(k peekKey, snap *remoteSnapshot, fetchedAt time.Time) {
	if p.entries == nil {
		p.entries = map[peekKey]peekEntry{}
	}
	p.entries[k] = peekEntry{snap: snap, fetchedAt: fetchedAt}
	for len(p.entries) > maxPeekEntries {
		var oldest peekKey
		var oldestAt time.Time
		first := true
		for key, e := range p.entries {
			if first || e.fetchedAt.Before(oldestAt) {
				oldest, oldestAt, first = key, e.fetchedAt, false
			}
		}
		delete(p.entries, oldest)
	}
}

// peekRefresh is one running refresh of a (cluster, identity) view. snap and
// err are written before done closes, and read only after.
type peekRefresh struct {
	done chan struct{}
	snap *remoteSnapshot
	err  error
}

// begin returns the running refresh of k, or reserves a new one and reports
// started with the generation it must store under.
func (p *remotePeek) begin(k peekKey) (r *peekRefresh, gen uint64, started bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if running, ok := p.fetching[k]; ok {
		return running, 0, false
	}
	if p.fetching == nil {
		p.fetching = map[peekKey]*peekRefresh{}
	}
	r = &peekRefresh{done: make(chan struct{})}
	p.fetching[k] = r
	return r, p.gens[k.clusterID], true
}

// finish ends the refresh r of k and wakes its waiters. A nil snap, or an
// eviction since begin, stores nothing.
func (p *remotePeek) finish(k peekKey, gen uint64, r *peekRefresh, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.fetching, k)
	if r.snap != nil && p.gens[k.clusterID] == gen {
		p.put(k, r.snap, now)
	}
	close(r.done)
}

// evict drops every identity's snapshot of clusterID and invalidates
// refreshes already running for it.
func (p *remotePeek) evict(clusterID string) {
	clusterID = k8s.NormalizedClusterID(clusterID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gens == nil {
		p.gens = map[string]uint64{}
	}
	p.gens[clusterID]++
	for k := range p.entries {
		if k.clusterID == clusterID {
			delete(p.entries, k)
		}
	}
}

// remoteExpiryRefreshTimeout bounds one detached refresh.
const remoteExpiryRefreshTimeout = 30 * time.Second

// errRefreshPanicked is the cause of a refresh that panicked; recoverutil logs the panic.
var errRefreshPanicked = errors.New("certificate refresh panicked")

// remoteExpiry reads the expiring-certificate counts of a remote cluster as
// the user (KTD4, KTD11). It serves the identity's last snapshot while it is
// younger than peekStaleness, and a snapshot older than the remote cache's TTL
// also starts a detached refresh, so a 60s poll keeps finding a warm one.
// With no such snapshot it loads within the caller's context, through h.load
// so the remote cache and its singleflight still apply. The load runs
// detached: when ctx ends first the result is incomplete (cache not warm) and
// the load carries on to fill the snapshot for the next call.
func (h *Handler) remoteExpiry(ctx context.Context, clusterID string, user *auth.User) resources.CertExpiry {
	can, err := h.AccessChecker.CanAccessGroupResource(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "cert-manager.io", "certificates", "")
	switch {
	case err != nil:
		return resources.CertExpiry{State: resources.CertExpiryIncomplete, Cause: err}
	case !can:
		return resources.CertExpiry{State: resources.CertExpiryDenied}
	}

	k := newPeekKey(clusterID, user)
	snap, age, ok := h.peeked.get(k, time.Now())
	if ok {
		if age > remotecache.DefaultTTL {
			h.startRemoteExpiryRefresh(ctx, clusterID, user, k)
		}
		return expiryFromSnapshot(snap)
	}

	r := h.startRemoteExpiryRefresh(ctx, clusterID, user, k)
	select {
	case <-ctx.Done():
		return resources.CertExpiry{State: resources.CertExpiryIncomplete, Cause: resources.ErrCertCacheNotWarm}
	case <-r.done:
	}
	if r.err != nil {
		return resources.CertExpiry{State: resources.CertExpiryIncomplete, Cause: r.err}
	}
	return expiryFromSnapshot(r.snap)
}

// startRemoteExpiryRefresh reads the cluster's lists on a context detached
// from the caller and stores the snapshot, or joins the refresh of k that is
// already running.
func (h *Handler) startRemoteExpiryRefresh(ctx context.Context, clusterID string, user *auth.User, k peekKey) *peekRefresh {
	r, gen, started := h.peeked.begin(k)
	if !started {
		return r
	}
	ctx = middleware.WithClusterID(context.WithoutCancel(ctx), clusterID)
	go func() {
		r.err = errRefreshPanicked
		// finish runs even when the refresh panics, so the reservation is released.
		defer func() { h.peeked.finish(k, gen, r, time.Now()) }()
		recoverutil.Safe(h.Logger, "certmanager remote expiry refresh", func() {
			ctx, cancel := context.WithTimeout(ctx, remoteExpiryRefreshTimeout)
			defer cancel()
			data, failed, err := h.load(ctx, user)
			if err != nil {
				h.Logger.Warn("remote certificate expiry refresh failed", "cluster", clusterID, "error", err)
				r.err = err
				return
			}
			r.snap, r.err = &remoteSnapshot{data: data, failed: failed}, nil
		})
	}()
	return r
}

// expiryFromSnapshot buckets a remote snapshot the way the local adapter
// buckets its cache. A read the user is allowed that did not complete makes
// the result incomplete: a failed Certificate list always does, and a failed
// Issuer or ClusterIssuer list does when a certificate references that kind,
// since its thresholds would otherwise fall back to the defaults unseen.
// Forbidden on any list is a denial, not a failure.
func expiryFromSnapshot(snap *remoteSnapshot) resources.CertExpiry {
	if snap.data == nil {
		return resources.CertExpiry{State: resources.CertExpiryNotInstalled}
	}
	referenced := map[string]bool{}
	for _, c := range snap.data.certificates {
		referenced[c.IssuerRef.Kind] = true
	}
	needed := map[string]bool{
		CertificateGVR.Resource:   true,
		IssuerGVR.Resource:        referenced["Issuer"],
		ClusterIssuerGVR.Resource: referenced["ClusterIssuer"],
	}
	denied := false
	for _, gvr := range []schema.GroupVersionResource{CertificateGVR, IssuerGVR, ClusterIssuerGVR} {
		err := snap.failed[gvr.Resource]
		switch {
		case err == nil:
		case apierrors.IsForbidden(err):
			denied = true
		case needed[gvr.Resource]:
			return resources.CertExpiry{State: resources.CertExpiryIncomplete, Cause: err}
		}
	}
	if denied {
		return resources.CertExpiry{State: resources.CertExpiryDenied}
	}
	var out resources.CertExpiry
	for _, cert := range snap.data.certificates {
		switch thresholdBucket(cert) {
		case thresholdWarning:
			out.Warning++
		case thresholdCritical, thresholdExpired:
			out.Critical++
		}
	}
	return out
}
