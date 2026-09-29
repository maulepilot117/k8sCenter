package gitops

// Remote-cluster reads and the helpers every handler uses to act on the
// cluster a request selects (R-8). The local cluster keeps its
// service-account cache and discoverer in handler.go and discovery.go.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
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

// listTimeout bounds one fetch of every remote GitOps list.
const listTimeout = 10 * time.Second

// appSources are the lists that make up the applications view.
var appSources = []string{"applications", "kustomizations", "helmreleases"}

// snapshot is one remote cluster's GitOps state as one identity sees it.
type snapshot struct {
	status  GitOpsStatus
	apps    []NormalizedApp
	appSets []NormalizedAppSet
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
// local cluster keeps its historical 500 and message; a remote cluster's
// error is classified without leaking its text.
func writeClusterError(w http.ResponseWriter, r *http.Request, err error, localMsg string) {
	if isLocal(r.Context()) {
		httputil.WriteError(w, http.StatusInternalServerError, localMsg, "")
		return
	}
	httputil.WriteRemoteError(w, err)
}

// isOwnConflict reports whether err is one of this package's own conflict
// checks (sync already running, app suspended, ...), identified by phrase.
// An error the cluster returned never is, even when its text happens to
// contain the phrase: echoing it would leak the cluster's error text.
func isOwnConflict(err error, phrases ...string) bool {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return false
	}
	for _, phrase := range phrases {
		if strings.Contains(err.Error(), phrase) {
			return true
		}
	}
	return false
}

// auditResult records a refusal by the cluster as denied rather than failed.
func auditResult(err error) audit.Result {
	if apierrors.IsForbidden(err) {
		return audit.ResultDenied
	}
	return audit.ResultFailure
}

// writeLoadError answers a failure to read the cluster's GitOps state.
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, localMsg string) {
	if isLocal(r.Context()) {
		h.Logger.Error(localMsg, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, localMsg, "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, "GitOps")
}

// loadApps returns the applications the request's cluster serves: the
// service-account cache for the local cluster, or a per-identity read of a
// remote one, with coverage for any remote list that failed.
func (h *Handler) loadApps(ctx context.Context, user *auth.User) ([]NormalizedApp, []k8s.SourceCoverage, error) {
	if isLocal(ctx) {
		apps, err := h.fetchApps(ctx)
		return apps, nil, err
	}
	snap, err := h.load(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	return snap.apps, k8s.CoverageOf(snap.failed, appSources...), nil
}

// load returns the remote cluster's GitOps snapshot for the user.
func (h *Handler) load(ctx context.Context, user *auth.User) (*snapshot, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.remoteCache().Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
}

// remoteDiscovery reads which GitOps tools a remote cluster serves, as the
// user sees it, and returns the discovery lists it read. A definite absence
// is a status with Reason discovery_missing, not an error.
func (h *Handler) remoteDiscovery(ctx context.Context, clusterID string, user *auth.User) (GitOpsStatus, []*metav1.APIResourceList, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	missing := GitOpsStatus{Detected: ToolNone, Reason: string(k8s.ReasonDiscoveryMissing), LastChecked: now}

	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so a tool installed later is seen within
	// seconds rather than when the schema cache expires.
	absent := true
	for _, gvr := range []schema.GroupVersionResource{ArgoApplicationGVR, FluxKustomizationGVR, FluxHelmReleaseGVR} {
		verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gvr.GroupResource())
		if verdict.Installed == nil || *verdict.Installed {
			absent = false
		}
	}
	if absent {
		return missing, nil, nil
	}

	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return GitOpsStatus{}, nil, k8s.TargetError{Err: err}
	}
	lists, unavailable, failedGroups := k8s.DiscoveryLists(target.Discovery)
	if unavailable {
		return GitOpsStatus{}, nil, k8s.ErrDiscoveryUnavailable
	}
	for _, group := range toolGroups {
		if failedGroups[group] {
			return GitOpsStatus{}, nil, k8s.ErrDiscoveryUnavailable
		}
	}
	status := statusFromLists(lists)
	if status.Detected == ToolNone {
		return missing, lists, nil
	}
	status.LastChecked = now
	return status, lists, nil
}

// fetchRemote reads a remote cluster's GitOps applications and
// ApplicationSets as the user. Each list succeeds or fails on its own; only
// when every list fails is the fetch itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*snapshot, error) {
	status, lists, err := h.remoteDiscovery(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	snap := &snapshot{status: status, apps: []NormalizedApp{}, appSets: []NormalizedAppSet{}, failed: map[string]error{}}

	type source struct {
		gvr  schema.GroupVersionResource
		list func(context.Context, dynamic.Interface) error // appends to snap under mu
	}
	var mu sync.Mutex
	appsFrom := func(fn func(context.Context, dynamic.Interface) ([]NormalizedApp, error)) func(context.Context, dynamic.Interface) error {
		return func(ctx context.Context, dyn dynamic.Interface) error {
			apps, err := fn(ctx, dyn)
			if err == nil {
				mu.Lock()
				snap.apps = append(snap.apps, apps...)
				mu.Unlock()
			}
			return err
		}
	}
	candidates := []source{
		{ArgoApplicationGVR, appsFrom(ListArgoApplications)},
		{FluxKustomizationGVR, appsFrom(ListFluxKustomizations)},
		{FluxHelmReleaseGVR, appsFrom(ListFluxHelmReleases)},
		{ArgoApplicationSetGVR, func(ctx context.Context, dyn dynamic.Interface) error {
			appSets, err := ListArgoAppSets(ctx, dyn)
			if err == nil {
				mu.Lock()
				snap.appSets = appSets
				mu.Unlock()
			}
			return err
		}},
	}
	var sources []source
	for _, src := range candidates {
		if k8s.GVRPresentIn(lists, src.gvr.Group, src.gvr.Resource) {
			sources = append(sources, src)
		}
	}
	if len(sources) == 0 {
		return snap, nil
	}

	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	runs := make([]func() error, len(sources))
	for i, src := range sources {
		runs[i] = func() error { return src.list(listCtx, dyn) }
	}
	errs := k8s.RunLists(h.Logger, "gitops remote list", runs)

	for i, src := range sources {
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			// The CRD went away after discovery was cached: re-read it so
			// the next fetch stops asking for it.
			h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, src.gvr.GroupResource())
		default:
			snap.failed[src.gvr.Resource] = err
		}
	}
	if len(snap.failed) == len(sources) {
		return nil, errs[0]
	}
	return snap, nil
}
