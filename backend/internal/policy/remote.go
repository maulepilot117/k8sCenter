package policy

// Remote-cluster reads and the helpers every handler uses to act on the
// cluster a request selects (R-8, #530). The local cluster keeps its
// service-account cache in handler.go and its discoverer in discovery.go;
// the compliance recorder and its history stay local, since snapshots are
// only ever taken of the local cluster.

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const (
	// gatekeeperConstraintsGroup serves one resource per ConstraintTemplate.
	gatekeeperConstraintsGroup = "constraints.gatekeeper.sh"
	// remoteListTimeout bounds each remote list call.
	remoteListTimeout = 10 * time.Second
	// remoteListLabel prefixes each remote list's recovered-panic log label,
	// so it reads apart from the local fan-out's "policy normalize-*".
	remoteListLabel = "policy remote list "
)

var (
	// kyvernoDetectGV / gatekeeperDetectGV and their kinds are what the
	// local Discoverer detects each engine by; remote detection matches.
	kyvernoDetectGV    = KyvernoClusterPolicyGVR.GroupVersion().String()
	gatekeeperDetectGV = "templates.gatekeeper.sh/v1"

	// presenceResources are the resources whose presence means an engine
	// is installed. Both definitely absent is "no engine" without reading
	// the rest of discovery.
	presenceResources = []schema.GroupResource{
		KyvernoClusterPolicyGVR.GroupResource(),
		{Group: "templates.gatekeeper.sh", Resource: "constrainttemplates"},
	}
)

// discovered is what a remote cluster's discovery says about its policy
// engines, as one identity sees it. The status route and the list snapshot
// share it, so a page reading both reads the remote's discovery once.
type discovered struct {
	status EngineStatus
	// sources are the lists the snapshot reads, in a fixed order.
	sources []remoteSource
}

// remoteSource is one list a remote snapshot is built from.
type remoteSource struct {
	label string // the resource name, for logs
	gvr   schema.GroupVersionResource
	add   func(*remoteData, *unstructured.Unstructured)
}

// remoteData is one remote cluster's policies and violations as one
// identity sees them, before RBAC filtering. Callers must treat it as
// read-only; it is shared through the cache.
type remoteData struct {
	policies   []NormalizedPolicy
	violations []NormalizedViolation
}

// remoteCaches returns the per-(cluster, identity) caches of remote
// snapshots and remote discovery, building them on first use.
func (h *Handler) remoteCaches() (*remotecache.Cache[*remoteData], *remotecache.Cache[*discovered]) {
	h.remoteOnce.Do(func() {
		h.remote = remotecache.New[*remoteData](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
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

// clusterStatus returns the engine status of the request's cluster. A
// remote failure is an error; HandleStatus turns it into a status with a
// reason.
func (h *Handler) clusterStatus(ctx context.Context, user *auth.User) (EngineStatus, error) {
	if isLocal(ctx) {
		return h.Discoverer.Status(), nil
	}
	d, err := h.discover(ctx, middleware.ClusterIDFromContext(ctx), user)
	if err != nil {
		return EngineStatus{}, err
	}
	return d.status, nil
}

// load returns the request cluster's policies and violations before RBAC
// filtering: the service-account cache for the local cluster, or a
// per-identity read of a remote one. Callers must not mutate the slices.
func (h *Handler) load(ctx context.Context, user *auth.User) ([]NormalizedPolicy, []NormalizedViolation, error) {
	if isLocal(ctx) {
		return h.fetchPoliciesAndViolations(ctx)
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	snapshots, _ := h.remoteCaches()
	data, err := snapshots.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*remoteData, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
	if err != nil {
		return nil, nil, err
	}
	return data.policies, data.violations, nil
}

// writeLoadError answers a failure to read the cluster's policy data. The
// local cluster keeps its historical 500 with localMsg; a remote cluster's
// failure is classified without leaking its text (R-8 KTD4).
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, localMsg string) {
	if isLocal(r.Context()) {
		h.Logger.Error(localMsg, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, localMsg, "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, "Policy engine")
}

// discover returns the remote cluster's engine discovery for the user,
// cached per identity like the lists, so a page polling the status does not
// re-read the remote's discovery and webhooks each time.
func (h *Handler) discover(ctx context.Context, clusterID string, user *auth.User) (*discovered, error) {
	_, discoveries := h.remoteCaches()
	return discoveries.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*discovered, error) {
		return h.remoteDiscovery(ctx, clusterID, user)
	})
}

// remoteDiscovery reads which policy engines a remote cluster serves, as the
// user sees it, and the lists a snapshot of it reads. A definite absence is a
// status with Reason discovery_missing, not an error.
func (h *Handler) remoteDiscovery(ctx context.Context, clusterID string, user *auth.User) (*discovered, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	missing := &discovered{status: EngineStatus{Detected: EngineNone, Reason: string(k8s.ReasonDiscoveryMissing), LastChecked: now}}

	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so an engine installed later is seen within
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
		return missing, nil
	}

	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	lists, unavailable, failedGroups := k8s.DiscoveryLists(target.Discovery)
	if unavailable {
		return nil, k8s.ErrDiscoveryUnavailable
	}

	// Detection mirrors the local Discoverer. A group that failed to load
	// makes the answer unknown only when this package needs it: an engine's
	// detection group, or a group an installed engine is listed from.
	if failedGroups[KyvernoClusterPolicyGVR.Group] || failedGroups["templates.gatekeeper.sh"] {
		return nil, k8s.ErrDiscoveryUnavailable
	}
	kyverno := servesKind(lists, kyvernoDetectGV, "ClusterPolicy")
	gatekeeper := servesKind(lists, gatekeeperDetectGV, "ConstraintTemplate")
	if (kyverno && failedGroups[PolicyReportGVR.Group]) || (gatekeeper && failedGroups[gatekeeperConstraintsGroup]) {
		return nil, k8s.ErrDiscoveryUnavailable
	}
	if !kyverno && !gatekeeper {
		return missing, nil
	}

	// Webhooks are read as the user; webhook configurations they cannot list
	// leave the namespace and count at their zero values, as an unreadable
	// webhook list does locally.
	cs, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	d := &discovered{status: EngineStatus{LastChecked: now}}
	if kyverno {
		ns, webhooks := detectWebhooks(ctx, cs, "kyverno")
		d.status.Kyverno = &EngineDetail{Available: true, Namespace: ns, Webhooks: webhooks}
		d.sources = append(d.sources, kyvernoSources(lists)...)
	}
	if gatekeeper {
		ns, webhooks := detectWebhooks(ctx, cs, "gatekeeper")
		d.status.Gatekeeper = &EngineDetail{Available: true, Namespace: ns, Webhooks: webhooks}
		d.sources = append(d.sources, gatekeeperSources(lists)...)
	}
	d.status.Detected = detectedFrom(d.status.Kyverno, d.status.Gatekeeper)
	return d, nil
}

// kyvernoSources are the Kyverno policy and report lists lists serves. The
// policy lists are what the engine's detection rests on; the PolicyReport
// lists are skipped when the reports API is not installed, as there is then
// nothing to report.
func kyvernoSources(lists []*metav1.APIResourceList) []remoteSource {
	var out []remoteSource
	for _, s := range []remoteSource{
		{gvr: KyvernoClusterPolicyGVR, add: func(d *remoteData, o *unstructured.Unstructured) {
			d.policies = append(d.policies, NormalizeKyvernoPolicy(o, true))
		}},
		{gvr: KyvernoPolicyGVR, add: func(d *remoteData, o *unstructured.Unstructured) {
			d.policies = append(d.policies, NormalizeKyvernoPolicy(o, false))
		}},
		{gvr: PolicyReportGVR, add: func(d *remoteData, o *unstructured.Unstructured) {
			d.violations = append(d.violations, extractKyvernoViolations(o)...)
		}},
		{gvr: ClusterPolicyReportGVR, add: func(d *remoteData, o *unstructured.Unstructured) {
			d.violations = append(d.violations, extractKyvernoViolations(o)...)
		}},
	} {
		if k8s.ServesGVR(lists, s.gvr) {
			s.label = remoteListLabel + s.gvr.Resource
			out = append(out, s)
		}
	}
	return out
}

// gatekeeperSources are the constraint lists lists serves, one per
// ConstraintTemplate. Gatekeeper serves each constraint kind at several
// versions at once (v1, v1beta1, v1alpha1), and discovery returns one list
// per group-version, so each resource is read once, at its highest served
// version; listing every version would count each constraint and its
// violations once per version. Subresources (constraint status) are not
// lists and are skipped.
//
// Unlike the local path, the remote list is not capped at
// maxConstraintCRDs: a silently truncated list would undercount violations,
// which is what fetchRemote fails closed to avoid. The semaphore bounds the
// fan-out's concurrency and the cache's fetch deadline bounds its duration.
func gatekeeperSources(lists []*metav1.APIResourceList) []remoteSource {
	type served struct {
		version string
		kind    string
	}
	best := map[string]served{}
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil || gv.Group != gatekeeperConstraintsGroup {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			cur, seen := best[r.Name]
			if !seen || version.CompareKubeAwareVersionStrings(gv.Version, cur.version) > 0 {
				best[r.Name] = served{version: gv.Version, kind: r.Kind}
			}
		}
	}

	names := make([]string, 0, len(best))
	for name := range best {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]remoteSource, 0, len(names))
	for _, name := range names {
		s := best[name]
		kind := s.kind
		out = append(out, remoteSource{
			label: remoteListLabel + name,
			gvr:   schema.GroupVersionResource{Group: gatekeeperConstraintsGroup, Version: s.version, Resource: name},
			add: func(d *remoteData, o *unstructured.Unstructured) {
				d.policies = append(d.policies, NormalizeGatekeeperConstraint(o, kind))
				d.violations = append(d.violations, extractGatekeeperViolations(o, kind)...)
			},
		})
	}
	return out
}

// servesKind reports whether lists serve kind at exactly groupVersion.
func servesKind(lists []*metav1.APIResourceList, groupVersion, kind string) bool {
	for _, l := range lists {
		if l.GroupVersion != groupVersion {
			continue
		}
		for _, r := range l.APIResources {
			if r.Kind == kind && !strings.Contains(r.Name, "/") {
				return true
			}
		}
	}
	return false
}

// fetchRemote reads a remote cluster's policies and violations as the user.
//
// Unlike the multi-source lists of other R-8 packages, any failed list fails
// the whole fetch rather than producing a partial 200 (KTD8). Every policy
// route either is, or derives a compliance score from, the union of these
// lists: a missing PolicyReport or constraint list reads as fewer violations,
// so a partial answer would render as a cleaner, more compliant cluster than
// the remote really is. The routes return bare arrays, so there is also no
// body to carry a coverage field in. A list whose resource went away after
// discovery was cached is not a failure: the resource is gone, and presence
// is re-read so the next fetch stops asking for it.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*remoteData, error) {
	d, err := h.discover(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	data := &remoteData{policies: []NormalizedPolicy{}, violations: []NormalizedViolation{}}
	if len(d.sources) == 0 {
		return data, nil
	}

	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	var mu sync.Mutex
	// sem bounds concurrent remote lists, as the local constraint fan-out is.
	sem := make(chan struct{}, constraintSemaphore)
	runs := make([]k8s.NamedList, len(d.sources))
	for i, src := range d.sources {
		runs[i] = k8s.NamedList{Label: src.label, Run: func() error {
			sem <- struct{}{}
			defer func() { <-sem }()
			items, err := listRemote(ctx, dyn, src.gvr)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			for j := range items {
				src.add(data, &items[j])
			}
			return nil
		}}
	}
	errs := k8s.RunLists(h.Logger, runs)

	for i, src := range d.sources {
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, src.gvr.GroupResource())
		default:
			return nil, err
		}
	}
	return data, nil
}

// listRemote lists gvr across every namespace, bounded by remoteListTimeout.
func listRemote(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource) ([]unstructured.Unstructured, error) {
	callCtx, cancel := context.WithTimeout(ctx, remoteListTimeout)
	defer cancel()
	list, err := dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(callCtx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}
