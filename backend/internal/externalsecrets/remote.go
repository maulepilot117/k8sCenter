package externalsecrets

// Remote-cluster reads and the helpers every read handler uses to answer for
// the cluster a request selects (R-8 U17). The local cluster keeps its
// service-account cache in handler.go and its Discoverer in discovery.go.
// Writes stay local-only (rejectNonLocalClusterWrite, R12), and the poller-
// and Prometheus-backed surfaces keep their own remote refusals (R14).

import (
	"context"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// snapshot is one remote cluster's ESO state as one identity sees it.
type snapshot struct {
	// data is nil when ESO's v1 API is not served on the cluster.
	data *cachedData
	// failed holds the error each failed list returned, keyed by resource
	// name. A list whose resource type is not served counts as empty.
	failed map[string]error
}

// EvictRemoteCache drops every identity's cached view of clusterID.
// Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remote.EvictCluster(clusterID)
}

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
}

// load returns the request cluster's ESO lists, or nil data when ESO is not
// installed there: the service-account cache for the local cluster, or a
// per-identity read of a remote one. On a remote cluster failed holds each
// list that failed while the others were read.
func (h *Handler) load(ctx context.Context, user *auth.User) (data *cachedData, failed map[string]error, err error) {
	if isLocal(ctx) {
		if !h.Discoverer.IsAvailable(ctx) {
			return nil, nil, nil
		}
		data, err := h.getCached(ctx)
		return data, nil, err
	}
	snap, err := h.loadRemote(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	return snap.data, snap.failed, nil
}

// loadRemote returns the request's remote cluster's snapshot for the user.
func (h *Handler) loadRemote(ctx context.Context, user *auth.User) (*snapshot, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.remote.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
}

// loadList loads the request cluster's ESO lists for an endpoint that serves
// the gvr list, writing the error response and returning false when that
// list could not be read. Nil data means ESO is not installed.
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

// writeLoadError answers a failure to read the cluster's ESO lists. The
// local cluster keeps its historical 500; a remote cluster's failure is
// classified without leaking its text (R-8 KTD4).
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, what string) {
	if isLocal(r.Context()) {
		h.Logger.Error("failed to fetch "+what, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch "+what, "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, "External Secrets")
}

// requireInstalled answers the detail routes' "is ESO here" gate for the
// request's cluster, writing the response and returning false when the
// request cannot proceed. Not installed keeps the historical 503 on both
// cluster kinds; a remote cluster whose answer is unknown gets the KTD4
// error instead.
func (h *Handler) requireInstalled(w http.ResponseWriter, r *http.Request, user *auth.User) bool {
	ctx := r.Context()
	if isLocal(ctx) {
		if !h.Discoverer.IsAvailable(ctx) {
			httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
			return false
		}
		return true
	}
	served, err := h.remoteServed(ctx, middleware.ClusterIDFromContext(ctx), user)
	if err != nil {
		httputil.WriteRemoteLoadError(w, err, "External Secrets")
		return false
	}
	if served == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, "ESO not detected", string(k8s.ReasonDiscoveryMissing), nil)
		return false
	}
	return true
}

// requestDyn returns a dynamic client impersonating the user on the
// request's cluster, writing the error response when it cannot.
func (h *Handler) requestDyn(w http.ResponseWriter, r *http.Request, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.dynForRequest(r.Context(), user)
	if err != nil {
		h.writeClientError(w, r, err, "create impersonating dynamic client")
		return nil, false
	}
	return client, true
}

// writeClientError answers a failure to resolve a per-user client for the
// request's cluster.
func (h *Handler) writeClientError(w http.ResponseWriter, r *http.Request, err error, logMsg string) {
	if isLocal(r.Context()) {
		h.Logger.Error(logMsg, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	httputil.WriteTargetError(w, err)
}

// writeGetError answers a failed read of one named object (what, e.g.
// "external secret"). A NotFound is a 404 on both cluster kinds. Every other
// local failure keeps its historical answer; a remote one is classified
// without leaking its text.
func (h *Handler) writeGetError(w http.ResponseWriter, r *http.Request, err error, what, namespace, name string) {
	switch {
	case apierrors.IsNotFound(err):
		httputil.WriteError(w, http.StatusNotFound, what+" not found", "")
	case !isLocal(r.Context()):
		httputil.WriteRemoteError(w, err)
	case apierrors.IsForbidden(err):
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
	default:
		h.Logger.Error("get "+what, "namespace", namespace, "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch "+what, "")
	}
}

// storesForResolver returns the SecretStores and ClusterSecretStores the
// threshold resolver walks for one ExternalSecret on the request's cluster.
// A remote cluster's stores come from its own snapshot, never the local
// cache; when they cannot be read the resolver falls through to defaults.
func (h *Handler) storesForResolver(ctx context.Context, user *auth.User) (stores, clusterStores []SecretStore) {
	if isLocal(ctx) {
		return h.cachedStoresForResolver()
	}
	snap, err := h.loadRemote(ctx, user)
	if err != nil || snap.data == nil {
		return nil, nil
	}
	return snap.data.stores, snap.data.clusterStores
}

// servedV1 reports whether lists serve gvr at exactly its version.
func servedV1(lists []*metav1.APIResourceList, gvr schema.GroupVersionResource) bool {
	want := gvr.GroupVersion().String()
	for _, l := range lists {
		if l.GroupVersion != want {
			continue
		}
		for _, res := range l.APIResources {
			if res.Name == gvr.Resource {
				return true
			}
		}
	}
	return false
}

// remoteServed reads which ESO resources a remote cluster serves at v1, as
// the user sees it. ESO counts as installed only when ExternalSecret is
// served at v1, the version this package reads, matching the local
// Discoverer; a cluster serving only an older version is not installed. A
// nil map means not installed. When that cannot be told, the error says
// why.
func (h *Handler) remoteServed(ctx context.Context, clusterID string, user *auth.User) (map[string]bool, error) {
	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so ESO installed later is seen within
	// seconds rather than when the schema cache expires.
	verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, ExternalSecretGVR.GroupResource())
	if verdict.Installed != nil && !*verdict.Installed {
		return nil, nil
	}

	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	lists, unavailable, failedGroups := k8s.DiscoveryLists(target.Discovery)
	if verdict.Installed == nil || unavailable || failedGroups[GroupName] {
		return nil, k8s.ErrDiscoveryUnavailable
	}
	if !servedV1(lists, ExternalSecretGVR) {
		// The group is served at another version. Presence does not track
		// versions, so drop the cached schema here: an upgrade to v1 is then
		// seen by the next fetch rather than when the schema expires.
		target.Invalidate()
		return nil, nil
	}
	served := make(map[string]bool, len(esoGVRs))
	for _, gvr := range esoGVRs {
		if servedV1(lists, gvr) {
			served[gvr.Resource] = true
		}
	}
	return served, nil
}

// fetchRemote reads a remote cluster's ESO lists as the user. Each list
// succeeds or fails on its own; only when every list fails is the fetch
// itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*snapshot, error) {
	served, err := h.remoteServed(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	if served == nil {
		return &snapshot{}, nil
	}
	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	listCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	data := &cachedData{}
	var sources []source
	for _, src := range data.sources() {
		if served[src.gvr.Resource] {
			sources = append(sources, src)
		}
	}
	errs := k8s.RunLists(h.Logger, namedLists(listCtx, dyn, sources))

	failed := map[string]error{}
	for i, src := range sources {
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			// The CRD went away after discovery was cached: re-read it so
			// the next fetch stops asking for it.
			h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, src.gvr.GroupResource())
		default:
			failed[src.gvr.Resource] = err
		}
	}
	if len(failed) == len(sources) {
		return nil, errs[0]
	}
	data.finish(h.Logger)
	return &snapshot{data: data, failed: failed}, nil
}

// remoteStatus answers the status route for a remote cluster. A failure to
// tell is a status, not an error (R-8 KTD5): it reports not detected. The
// ESO controller's namespace and version come from a local Deployment
// lookup and are not reported for a remote cluster.
func (h *Handler) remoteStatus(ctx context.Context, user *auth.User) ESOStatus {
	served, err := h.remoteServed(ctx, middleware.ClusterIDFromContext(ctx), user)
	return ESOStatus{Detected: err == nil && served != nil, LastChecked: time.Now().UTC()}
}
