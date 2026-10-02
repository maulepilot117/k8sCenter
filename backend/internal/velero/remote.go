package velero

// Remote-cluster reads and the helpers every handler uses to act on the
// cluster a request selects (R-8). The local cluster keeps its
// service-account cache in handler.go and its Discoverer in discovery.go.

import (
	"context"
	"errors"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// listTimeout bounds one fetch of every Velero list.
const listTimeout = 10 * time.Second

// snapshot is one remote cluster's Velero state as one identity sees it.
type snapshot struct {
	// data is nil when Velero is not installed on the cluster.
	data *cachedVeleroData
	// failed holds the error each failed list returned, keyed by resource
	// name. A list whose resource type is gone counts as empty, not failed.
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

// baseDyn is the local service-account dynamic client.
func (h *Handler) baseDyn() dynamic.Interface {
	if h.baseDynOverride != nil {
		return h.baseDynOverride
	}
	// nolint:cluster-routing local path: the service-account cache serves the local cluster only; remote reads go through fetchRemote.
	return h.K8sClient.BaseDynamicClient()
}

// dynamicClient returns a dynamic client impersonating the user on the
// request's cluster, writing the error response when it cannot.
func (h *Handler) dynamicClient(w http.ResponseWriter, r *http.Request, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.Clients.DynamicClientForCluster(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		if isLocal(r.Context()) {
			h.Logger.Error("failed to create impersonating client", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		} else {
			httputil.WriteTargetError(w, err)
		}
		return nil, false
	}
	return client, true
}

// writeClusterError answers a failed call to the request's cluster. The
// local cluster keeps its historical 500 with the error as detail; a remote
// cluster's error is classified without leaking its text.
func (h *Handler) writeClusterError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	h.Logger.Error(msg, "error", err)
	if isLocal(r.Context()) {
		httputil.WriteError(w, http.StatusInternalServerError, msg, err.Error())
		return
	}
	httputil.WriteRemoteError(w, err)
}

// failWrite audits a write the cluster refused or failed, then answers it
// through writeClusterError.
func (h *Handler) failWrite(w http.ResponseWriter, r *http.Request, user *auth.User, err error, msg string, action audit.Action, kind, ns, name string) {
	h.auditLog(r, user, action, kind, ns, name, auditResult(err))
	h.writeClusterError(w, r, err, msg)
}

// writeAccessCheckError answers a remote RBAC pre-check that could not be
// made: a SAR the cluster answered with an error status through
// WriteRemoteError, and a failure to reach or resolve the cluster through
// WriteTargetError.
func writeAccessCheckError(w http.ResponseWriter, err error) {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		httputil.WriteRemoteError(w, err)
		return
	}
	httputil.WriteTargetError(w, err)
}

// writeGetError answers a failed read of one named object. Every local
// failure keeps its historical 404; on a remote cluster only a NotFound is
// one, and anything else is classified without leaking its text.
func (h *Handler) writeGetError(w http.ResponseWriter, r *http.Request, err error, notFoundMsg string) {
	if isLocal(r.Context()) || apierrors.IsNotFound(err) {
		httputil.WriteError(w, http.StatusNotFound, notFoundMsg, "")
		return
	}
	httputil.WriteRemoteError(w, err)
}

// writeLoadError answers a failure to read the cluster's Velero lists.
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, what string) {
	if isLocal(r.Context()) {
		h.Logger.Error("failed to fetch "+what, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch "+what, "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, "Velero")
}

// auditResult records a refusal by the cluster as denied rather than failed.
func auditResult(err error) audit.Result {
	if apierrors.IsForbidden(err) {
		return audit.ResultDenied
	}
	return audit.ResultFailure
}

// load returns the request cluster's Velero lists, or nil data when Velero
// is not installed there: the service-account cache for the local cluster,
// or a per-identity read of a remote one. On a remote cluster failed holds
// each list that failed while the others were read.
func (h *Handler) load(ctx context.Context, user *auth.User) (data *cachedVeleroData, failed map[string]error, err error) {
	if isLocal(ctx) {
		if !h.Discoverer.Status(ctx).Detected {
			return nil, nil, nil
		}
		data, err := h.fetchAll(ctx)
		return data, nil, err
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	snap, err := h.remote.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
	if err != nil {
		return nil, nil, err
	}
	return snap.data, snap.failed, nil
}

// loadList loads the request cluster's Velero lists for an endpoint that
// serves the gvr list, writing the error response and returning false when
// that list could not be read. Nil data means Velero is not installed.
// failed holds the other lists a remote cluster could not provide.
func (h *Handler) loadList(w http.ResponseWriter, r *http.Request, user *auth.User, gvr schema.GroupVersionResource, what string) (data *cachedVeleroData, failed map[string]error, ok bool) {
	data, failed, err := h.load(r.Context(), user)
	if err == nil && data != nil {
		err = failed[gvr.Resource]
	}
	if err != nil {
		h.writeLoadError(w, r, err, what)
		return nil, nil, false
	}
	return data, failed, true
}

// remoteInstalled reports whether Velero is installed on a remote cluster,
// as the user sees it. When that cannot be told, the error says why.
func (h *Handler) remoteInstalled(ctx context.Context, clusterID string, user *auth.User) (bool, error) {
	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so Velero installed later is seen within
	// seconds rather than when the schema cache expires.
	verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, BackupGVR.GroupResource())
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
// tell is a status with a reason, not an error (R-8 KTD5). The location
// counts come from the same cached lists the pages read.
func (h *Handler) remoteStatus(ctx context.Context, user *auth.User) VeleroStatus {
	status := VeleroStatus{LastChecked: time.Now().UTC()}
	installed, err := h.remoteInstalled(ctx, middleware.ClusterIDFromContext(ctx), user)
	switch {
	case err != nil:
		status.Reason = string(k8s.RemoteReason(err))
		return status
	case !installed:
		status.Reason = string(k8s.ReasonDiscoveryMissing)
		return status
	}
	status.Detected = true
	if data, _, err := h.load(ctx, user); err == nil && data != nil {
		status.BSLCount = len(data.locations.BackupStorageLocations)
		status.VSLCount = len(data.locations.VolumeSnapshotLocations)
	}
	return status
}

// fetchRemote reads a remote cluster's Velero lists as the user. Each list
// succeeds or fails on its own; only when every list fails is the fetch
// itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*snapshot, error) {
	installed, err := h.remoteInstalled(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	if !installed {
		return &snapshot{}, nil
	}
	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	data := newCachedData()
	sources := data.sources()
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
	return &snapshot{data: data, failed: failed}, nil
}

// restoresOf returns the restores in namespace on the request's cluster.
// A remote cluster is read live as the user, not from the cache: the
// delete-backup guard must not miss a restore started since the last fetch.
func (h *Handler) restoresOf(ctx context.Context, dyn dynamic.Interface, namespace string) ([]Restore, error) {
	if isLocal(ctx) {
		return h.fetchRestores(ctx)
	}
	var restores []Restore
	err := listInto(ctx, dyn, RestoreGVR, namespace, parseRestore, &restores)
	return restores, err
}
