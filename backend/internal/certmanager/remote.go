package certmanager

// Remote-cluster reads and the helpers every handler uses to act on the
// cluster a request selects (R-8, #531). The local cluster keeps its
// service-account cache in handler.go and its Discoverer in discovery.go;
// whether cert-manager is installed on a remote cluster is decided by that
// cluster's own discovery, never by the local one.

import (
	"context"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
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
