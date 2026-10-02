package servicemesh

// Remote-cluster reads and the helpers every handler uses to act on the
// cluster a request selects (R-8). The local cluster keeps its
// service-account cache in handler.go and its discoverer in discovery.go;
// the topology overlay (Routes, MeshDetected) stays on those, since the
// topology graph is local-only.

import (
	"context"
	"net/http"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// meshGroups are the API groups this package lists from. Discovery failing
// for any of them makes a remote cluster's mesh state unknown.
var meshGroups = []string{"networking.istio.io", "security.istio.io", "linkerd.io", "policy.linkerd.io"}

// presenceResources are the resources whose presence means a mesh is
// installed: the same ones the local Discoverer probes.
var presenceResources = []schema.GroupResource{IstioVirtualServiceGVR.GroupResource(), LinkerdServerGVR.GroupResource()}

// crossCheckUnavailableRemote is the mTLS posture warning on a remote
// cluster. The metric cross-check queries the local Prometheus, which knows
// nothing of a remote cluster's traffic (R14).
const crossCheckUnavailableRemote = "metric cross-check is unavailable on remote clusters; posture derived from policies only"

// meshSource is one list a mesh snapshot is built from. Exactly one of
// route and policy is set.
type meshSource struct {
	mesh   MeshType
	key    string // "{mesh}/{Kind}", the key the errors map has always used
	gvr    schema.GroupVersionResource
	route  func(*unstructured.Unstructured) TrafficRoute
	policy func(*unstructured.Unstructured) MeshedPolicy
}

// meshSources lists every mesh CRD in a fixed order, so coverage and errors
// come out in a stable order.
var meshSources = buildMeshSources()

func buildMeshSources() []meshSource {
	var out []meshSource
	for _, c := range istioRouteCRDs {
		out = append(out, meshSource{mesh: MeshIstio, key: "istio/" + c.Kind, gvr: c.GVR,
			route: func(o *unstructured.Unstructured) TrafficRoute { return normalizeIstioRoute(o, c.Kind) }})
	}
	for _, c := range istioPolicyCRDs {
		out = append(out, meshSource{mesh: MeshIstio, key: "istio/" + c.Kind, gvr: c.GVR,
			policy: func(o *unstructured.Unstructured) MeshedPolicy { return normalizeIstioPolicy(o, c.Kind) }})
	}
	for _, c := range linkerdRouteCRDs {
		out = append(out, meshSource{mesh: MeshLinkerd, key: "linkerd/" + c.Kind, gvr: c.GVR,
			route: func(o *unstructured.Unstructured) TrafficRoute { return normalizeLinkerdRoute(o, c.Kind) }})
	}
	for _, c := range linkerdPolicyCRDs {
		out = append(out, meshSource{mesh: MeshLinkerd, key: "linkerd/" + c.Kind, gvr: c.GVR,
			policy: func(o *unstructured.Unstructured) MeshedPolicy { return normalizeLinkerdPolicy(o, c.Kind) }})
	}
	return out
}

// snapshot is one cluster's mesh state as a request sees it.
type snapshot struct {
	status   MeshStatus
	routes   []TrafficRoute
	policies []MeshedPolicy
	// errors is the wire-facing per-source failure map, keyed "{mesh}/{Kind}".
	// On a remote cluster it carries fixed messages only, never the
	// cluster's error text.
	errors map[string]string
	// coverage names each remote list that failed. Always nil locally.
	coverage []k8s.SourceCoverage
}

// discovered is what a remote cluster's discovery and control plane say
// about its meshes, as one identity sees them. The status route and the
// list snapshot share it, so a page reading both reads the remote once.
type discovered struct {
	status MeshStatus
	lists  []*metav1.APIResourceList // nil when no mesh is installed
}

// remoteCaches returns the per-(cluster, identity) caches of remote
// snapshots and remote discovery, building them on first use.
func (h *Handler) remoteCaches() (*remotecache.Cache[*snapshot], *remotecache.Cache[*discovered]) {
	h.remoteOnce.Do(func() {
		h.remote = remotecache.New[*snapshot](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
		h.remoteDiscovered = remotecache.New[*discovered](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
	})
	return h.remote, h.remoteDiscovered
}

// EvictRemoteCache drops every identity's cached view of clusterID.
// Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	snapshots, discoveries := h.remoteCaches()
	snapshots.EvictCluster(clusterID)
	discoveries.EvictCluster(clusterID)
}

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
}

// userClient returns a typed client impersonating the user on the request's
// cluster. All request-time typed reads in this handler MUST go through it:
// the service account's own clientset would bypass the user's RBAC and
// mis-attribute the call in the Kubernetes audit log. clientsetOverride is a
// test seam for the local cluster only.
func (h *Handler) userClient(ctx context.Context, user *auth.User) (kubernetes.Interface, error) {
	if h.clientsetOverride != nil && isLocal(ctx) {
		return h.clientsetOverride, nil
	}
	return h.Clients.ClientForCluster(ctx, middleware.ClusterIDFromContext(ctx), user.KubernetesUsername, user.KubernetesGroups)
}

// writeClientError answers a failure to resolve a client for the request's
// cluster. The local cluster keeps its historical 500 with localMsg.
func (h *Handler) writeClientError(w http.ResponseWriter, r *http.Request, err error, localMsg string) {
	if isLocal(r.Context()) {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, localMsg, "")
		return
	}
	httputil.WriteTargetError(w, err)
}

// writeLoadError answers a failure to read the cluster's mesh state.
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error) {
	if isLocal(r.Context()) {
		h.Logger.Error("failed to fetch mesh data", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch mesh data", "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, "Service mesh")
}

// clusterStatus returns the mesh status of the request's cluster. A remote
// failure is an error; HandleStatus turns it into a status with a reason.
func (h *Handler) clusterStatus(ctx context.Context, user *auth.User) (MeshStatus, error) {
	if isLocal(ctx) {
		return h.Discoverer.Status(ctx), nil
	}
	d, err := h.discover(ctx, middleware.ClusterIDFromContext(ctx), user)
	if err != nil {
		return MeshStatus{}, err
	}
	return d.status, nil
}

// discover returns the remote cluster's discovery for the user, cached per
// identity like the lists, so a page polling the status does not re-read
// the remote's discovery and control plane each time.
func (h *Handler) discover(ctx context.Context, clusterID string, user *auth.User) (*discovered, error) {
	_, discoveries := h.remoteCaches()
	return discoveries.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*discovered, error) {
		status, lists, err := h.remoteDiscovery(ctx, clusterID, user)
		if err != nil {
			return nil, err
		}
		return &discovered{status: status, lists: lists}, nil
	})
}

// load returns the request cluster's mesh snapshot: the service-account
// cache for the local cluster, or a per-identity read of a remote one.
// Callers must treat it as read-only; it may be shared through a cache.
func (h *Handler) load(ctx context.Context, user *auth.User) (*snapshot, error) {
	if isLocal(ctx) {
		status := h.Discoverer.Status(ctx)
		data, err := h.fetchData(ctx)
		if err != nil {
			return nil, err
		}
		return &snapshot{status: status, routes: data.routes, policies: data.policies, errors: data.errors}, nil
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	snapshots, _ := h.remoteCaches()
	return snapshots.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
}

// remoteDiscovery reads which meshes a remote cluster serves, as the user
// sees it, and returns the discovery lists it read. A definite absence is a
// status with Reason discovery_missing, not an error.
func (h *Handler) remoteDiscovery(ctx context.Context, clusterID string, user *auth.User) (MeshStatus, []*metav1.APIResourceList, error) {
	now := time.Now().UTC()
	missing := MeshStatus{Detected: MeshNone, Reason: string(k8s.ReasonDiscoveryMissing), LastChecked: now}

	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so a mesh installed later is seen within
	// seconds rather than when the schema cache expires.
	absent := true
	for _, gr := range presenceResources {
		verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gr)
		if verdict.Installed == nil || *verdict.Installed {
			absent = false
			break
		}
	}
	if absent {
		return missing, nil, nil
	}

	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return MeshStatus{}, nil, k8s.TargetError{Err: err}
	}
	lists, unavailable, failedGroups := k8s.DiscoveryLists(target.Discovery)
	if unavailable {
		return MeshStatus{}, nil, k8s.ErrDiscoveryUnavailable
	}
	for _, g := range meshGroups {
		if failedGroups[g] {
			return MeshStatus{}, nil, k8s.ErrDiscoveryUnavailable
		}
	}

	// Detection mirrors the local Discoverer: a mesh is installed when the
	// version this package lists its anchor kind at is served.
	istio, linkerd := k8s.ServesGVR(lists, IstioVirtualServiceGVR), k8s.ServesGVR(lists, LinkerdServerGVR)
	if !istio && !linkerd {
		return missing, lists, nil
	}
	status := MeshStatus{LastChecked: now}
	// The control plane is read as the user; one they cannot read leaves
	// the version and mode at their defaults, as it does locally.
	cs, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return MeshStatus{}, nil, k8s.TargetError{Err: err}
	}
	if istio {
		status.Istio = istioControlPlane(ctx, cs)
	}
	if linkerd {
		status.Linkerd = linkerdControlPlane(ctx, cs)
	}
	status.Detected = detectionFrom(status.Istio, status.Linkerd)
	return status, lists, nil
}

// fetchRemote reads a remote cluster's mesh routes and policies as the
// user. Each list succeeds or fails on its own; only when every list fails
// is the fetch itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*snapshot, error) {
	d, err := h.discover(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	status := d.status
	snap := &snapshot{status: status, routes: []TrafficRoute{}, policies: []MeshedPolicy{}, errors: map[string]string{}}

	var sources []meshSource
	for _, src := range meshSources {
		installed := (src.mesh == MeshIstio && status.Istio != nil) || (src.mesh == MeshLinkerd && status.Linkerd != nil)
		if installed && k8s.ServesGVR(d.lists, src.gvr) {
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

	var mu sync.Mutex
	listOpts := metav1.ListOptions{Limit: meshListCap}
	runs := make([]k8s.NamedList, len(sources))
	for i, src := range sources {
		runs[i] = k8s.NamedList{Label: "servicemesh list " + src.key, Run: func() error {
			// listCRD bounds each call with meshListTimeout.
			items, err := listCRD(ctx, dyn, src.gvr, "", listOpts)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			for j := range items {
				if src.route != nil {
					snap.routes = append(snap.routes, src.route(&items[j]))
				} else {
					snap.policies = append(snap.policies, src.policy(&items[j]))
				}
			}
			return nil
		}}
	}
	errs := k8s.RunLists(h.Logger, runs)

	failed := map[string]error{}
	keys := make([]string, 0, len(sources))
	for i, src := range sources {
		keys = append(keys, src.key)
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			// The CRD went away after discovery was cached: re-read it so
			// the next fetch stops asking for it.
			h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, src.gvr.GroupResource())
		default:
			failed[src.key] = err
		}
	}
	if len(failed) == len(sources) {
		return nil, errs[0]
	}
	snap.coverage = k8s.CoverageOf(failed, keys...)
	for _, c := range snap.coverage {
		snap.errors[c.Source] = coverageMessage(c)
	}
	return snap, nil
}

// coverageMessage is the fixed errors-map text for a failed remote list.
func coverageMessage(c k8s.SourceCoverage) string {
	if c.Status == k8s.CoverageStatusForbidden {
		return "the cluster denied listing this resource"
	}
	return "could not list this resource on the selected cluster"
}

// writeGetError answers a failed detail Get. The local cluster keeps its
// historical 404 and 500; a remote cluster's error is classified without
// leaking its text.
func (h *Handler) writeGetError(w http.ResponseWriter, r *http.Request, err error) {
	if !isLocal(r.Context()) {
		httputil.WriteRemoteError(w, err)
		return
	}
	if apierrors.IsNotFound(err) {
		httputil.WriteError(w, http.StatusNotFound, "mesh resource not found", "")
		return
	}
	httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch mesh resource", "")
}
