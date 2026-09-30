package notification

// Remote-cluster reads and the helpers every handler uses to act on the
// cluster a request selects (R-8). The local cluster keeps its
// service-account cache in handler.go.

import (
	"context"
	"errors"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// listTimeout bounds one fetch of every remote notification list.
const listTimeout = 10 * time.Second

// notInstalledMsg answers a write to a remote cluster that does not serve
// the notification API this package writes.
const notInstalledMsg = "Flux notifications are not installed on the selected cluster"

// notificationGVRs are the lists that make up the notification views, in
// the order their coverage is reported.
var notificationGVRs = []schema.GroupVersionResource{FluxProviderGVR, FluxAlertGVR, FluxReceiverGVR}

// snapshot is one remote cluster's notification state as one identity sees
// it.
type snapshot struct {
	providers []NormalizedProvider
	alerts    []NormalizedAlert
	receivers []NormalizedReceiver
	// served holds each resource the cluster serves at the version this
	// package uses, keyed by resource name. Empty when the notification API
	// is not installed.
	served map[string]bool
	// failed holds the error each failed list returned, keyed by resource
	// name. A list whose resource type is gone counts as empty, not failed.
	failed map[string]error
}

func (h *Handler) remoteCache() *remotecache.Cache[*snapshot] {
	h.remoteOnce.Do(func() {
		h.remote = remotecache.New[*snapshot](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
	})
	return h.remote
}

// EvictRemoteCache drops every identity's cached view of clusterID.
// Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remoteCache().EvictCluster(clusterID)
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

// invalidate drops what a write to gvr made stale: the local list cache, or
// every identity's view of a remote cluster, which sends no informer events
// (R-8 KTD7).
func (h *Handler) invalidate(ctx context.Context, gvr schema.GroupVersionResource) {
	if !isLocal(ctx) {
		h.EvictRemoteCache(middleware.ClusterIDFromContext(ctx))
		return
	}
	switch gvr {
	case FluxProviderGVR:
		h.InvalidateProviders()
	case FluxAlertGVR:
		h.InvalidateAlerts()
	case FluxReceiverGVR:
		h.InvalidateReceivers()
	}
}

// allowed runs the RBAC pre-check for a write on the request's cluster and
// reports whether the request may proceed. A denial is answered with 403 and
// denyMsg. On a remote cluster a check that could not be made is answered
// with its classified error rather than a denial; the local cluster keeps
// treating it as one.
func (h *Handler) allowed(w http.ResponseWriter, r *http.Request, user *auth.User, verb, resource, namespace, denyMsg string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), middleware.ClusterIDFromContext(r.Context()),
		user.KubernetesUsername, user.KubernetesGroups, verb, FluxProviderGVR.Group, resource, namespace)
	switch {
	case err != nil && !isLocal(r.Context()):
		writeAccessCheckError(w, err)
		return false
	case err != nil || !can:
		httputil.WriteError(w, http.StatusForbidden, denyMsg, "")
		return false
	}
	return true
}

// writeAccessCheckError answers a remote RBAC check that could not be made:
// a SAR the cluster answered with an error status through WriteRemoteError,
// and a failure to reach or resolve the cluster through WriteTargetError.
func writeAccessCheckError(w http.ResponseWriter, err error) {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		httputil.WriteRemoteError(w, err)
		return
	}
	httputil.WriteTargetError(w, err)
}

// writeClient returns a dynamic client impersonating the user on the
// request's cluster for a write to gvr, writing the error response when it
// cannot. A remote cluster that does not serve gvr at the version this
// package writes is answered 404 before anything is sent to it.
func (h *Handler) writeClient(w http.ResponseWriter, r *http.Request, user *auth.User, gvr schema.GroupVersionResource) (dynamic.Interface, bool) {
	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)
	if !isLocal(ctx) {
		served, err := h.remoteServed(ctx, clusterID, user)
		if err != nil {
			httputil.WriteRemoteLoadError(w, err, "Flux notification")
			return nil, false
		}
		if !served[gvr.Resource] {
			httputil.WriteErrorWithReason(w, http.StatusNotFound, notInstalledMsg, string(k8s.ReasonDiscoveryMissing), nil)
			return nil, false
		}
	}
	client, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		if isLocal(ctx) {
			h.Logger.Error("failed to create impersonating client", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		} else {
			httputil.WriteTargetError(w, err)
		}
		return nil, false
	}
	return client, true
}

// failWrite audits a write the cluster refused or failed and answers it.
// The local cluster keeps its messages and records the error as detail; a
// remote cluster's error is classified without leaking its text, into the
// response or the audit row.
func (h *Handler) failWrite(w http.ResponseWriter, r *http.Request, user *auth.User, err error, action audit.Action, verb, kind, ns, name string, gvr schema.GroupVersionResource) {
	result := audit.ResultFailure
	if apierrors.IsForbidden(err) {
		result = audit.ResultDenied
	}
	if isLocal(r.Context()) {
		h.auditLog(r, user, action, kind, ns, name, result, err.Error())
		h.writeK8sError(w, err, verb, kind, ns, name)
		return
	}
	h.auditLog(r, user, action, kind, ns, name, result, "")
	if k8s.IsResourceGone(err) {
		// The CRD went away after discovery was cached: re-read it so the
		// status and the next write see the removal.
		h.Presence.Recheck(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups, gvr.GroupResource())
	}
	httputil.WriteRemoteError(w, err)
}

// writeLoadError answers a failure to read one of the cluster's lists.
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, what string) {
	if isLocal(r.Context()) {
		h.Logger.Error("failed to fetch notification "+what, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch "+what, "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, "Flux notification")
}

// loadItems returns the request cluster's items of gvr: the local
// service-account cache, or a per-identity read of a remote cluster.
func loadItems[T any](h *Handler, ctx context.Context, user *auth.User, gvr schema.GroupVersionResource, local func() ([]T, error), pick func(*snapshot) []T) ([]T, error) {
	if isLocal(ctx) {
		return local()
	}
	snap, err := h.load(ctx, user)
	if err != nil {
		return nil, err
	}
	if err := snap.failed[gvr.Resource]; err != nil {
		return nil, err
	}
	return pick(snap), nil
}

// load returns the remote cluster's notification snapshot for the user.
func (h *Handler) load(ctx context.Context, user *auth.User) (*snapshot, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.remoteCache().Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
}

// servedAt reports whether lists serve gvr at exactly its version.
func servedAt(lists []*metav1.APIResourceList, gvr schema.GroupVersionResource) bool {
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

// remoteServed reads which notification resources a remote cluster serves
// at the versions this package reads and writes, as the user sees it. The
// API counts as installed only when Providers or Alerts are served at their
// v1beta3 version, the one this package builds bodies for (R-8 U8); a
// cluster serving only v1beta2 is not installed. A nil map means not
// installed. When that cannot be told, the error says why.
func (h *Handler) remoteServed(ctx context.Context, clusterID string, user *auth.User) (map[string]bool, error) {
	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so the API installed later is seen within
	// seconds rather than when the schema cache expires.
	absent := true
	for _, gvr := range notificationGVRs {
		verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gvr.GroupResource())
		if verdict.Installed == nil || *verdict.Installed {
			absent = false
			break
		}
	}
	if absent {
		return nil, nil
	}

	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	lists, unavailable, failedGroups := k8s.DiscoveryLists(target.Discovery)
	if unavailable || failedGroups[FluxProviderGVR.Group] {
		return nil, k8s.ErrDiscoveryUnavailable
	}
	if !servedAt(lists, FluxProviderGVR) && !servedAt(lists, FluxAlertGVR) {
		// The group is served at another version. Presence does not track
		// versions, so drop the cached schema here: an upgrade to v1beta3 is
		// then seen by the next fetch rather than when the schema expires.
		target.Invalidate()
		return nil, nil
	}
	served := make(map[string]bool, len(notificationGVRs))
	for _, gvr := range notificationGVRs {
		if servedAt(lists, gvr) {
			served[gvr.Resource] = true
		}
	}
	return served, nil
}

// fetchRemote reads a remote cluster's notification lists as the user. Each
// list succeeds or fails on its own; only when every list fails is the fetch
// itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*snapshot, error) {
	served, err := h.remoteServed(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	snap := &snapshot{served: served, failed: map[string]error{}}
	if len(served) == 0 {
		return snap, nil
	}
	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	// Each list writes only its own snapshot field, so they need no lock.
	lists := map[schema.GroupVersionResource]func() error{
		FluxProviderGVR: func() (err error) { snap.providers, err = ListProviders(listCtx, dyn); return err },
		FluxAlertGVR:    func() (err error) { snap.alerts, err = ListAlerts(listCtx, dyn); return err },
		FluxReceiverGVR: func() (err error) { snap.receivers, err = ListReceivers(listCtx, dyn); return err },
	}
	var sources []schema.GroupVersionResource
	var runs []k8s.NamedList
	for _, gvr := range notificationGVRs {
		if served[gvr.Resource] {
			sources = append(sources, gvr)
			runs = append(runs, k8s.NamedList{Label: "notification list " + gvr.Resource, Run: lists[gvr]})
		}
	}
	errs := k8s.RunLists(h.Logger, runs)

	for i, gvr := range sources {
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			// The CRD went away after discovery was cached: re-read it so
			// the next fetch stops asking for it.
			h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gvr.GroupResource())
		default:
			snap.failed[gvr.Resource] = err
		}
	}
	if len(snap.failed) == len(sources) {
		return nil, errs[0]
	}
	return snap, nil
}

// remoteStatus answers the status route for a remote cluster. A failure to
// tell is a status with a reason, not an error (R-8 KTD5); a list that could
// not be read or RBAC-filtered is disclosed in Coverage (KTD8).
func (h *Handler) remoteStatus(ctx context.Context, user *auth.User) NotificationStatus {
	served, err := h.remoteServed(ctx, middleware.ClusterIDFromContext(ctx), user)
	switch {
	case err != nil:
		return NotificationStatus{Reason: string(k8s.RemoteReason(err))}
	case len(served) == 0:
		return NotificationStatus{Reason: string(k8s.ReasonDiscoveryMissing)}
	}

	status := NotificationStatus{Available: true}
	failed := map[string]error{}
	snap, err := h.load(ctx, user)
	if err != nil {
		for res := range served {
			failed[res] = err
		}
	} else {
		for res, ferr := range snap.failed {
			failed[res] = ferr
		}
		count := func(resource string, n int, err error) int {
			if err != nil {
				failed[resource] = err
			}
			return n
		}
		if failed["providers"] == nil {
			items, err := filterByRBAC(ctx, h.AccessChecker, user, snap.providers, "providers")
			status.ProviderCount = count("providers", len(items), err)
		}
		if failed["alerts"] == nil {
			items, err := filterByRBAC(ctx, h.AccessChecker, user, snap.alerts, "alerts")
			status.AlertCount = count("alerts", len(items), err)
		}
		if failed["receivers"] == nil {
			items, err := filterByRBAC(ctx, h.AccessChecker, user, snap.receivers, "receivers")
			status.ReceiverCount = count("receivers", len(items), err)
		}
	}
	sources := make([]string, len(notificationGVRs))
	for i, gvr := range notificationGVRs {
		sources[i] = gvr.Resource
	}
	status.Coverage = k8s.CoverageOf(failed, sources...)
	return status
}
