package gateway

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// routeKindToKind maps query-param route kind values to Gateway API Kind names.
var routeKindToKind = map[string]string{
	"grpcroutes": "GRPCRoute",
	"tcproutes":  "TCPRoute",
	"tlsroutes":  "TLSRoute",
	"udproutes":  "UDPRoute",
}

// routeKindOrder fixes the order non-HTTP route kinds are listed in.
var routeKindOrder = []routeKind{RouteKindGRPC, RouteKindTCP, RouteKindTLS, RouteKindUDP}

// coreSources are the lists every Gateway API view needs; summarySources
// adds every route kind, for the summary that counts them all.
var (
	coreSources    = []string{"gatewayclasses", "gateways", "httproutes"}
	summarySources = func() []string {
		out := append([]string(nil), coreSources...)
		for _, rk := range routeKindOrder {
			out = append(out, string(rk))
		}
		return out
	}()
)

// gatewaysGR is the resource whose presence says Gateway API is installed.
var gatewaysGR = schema.GroupResource{Group: APIGroup, Resource: "gateways"}

// listTimeout bounds one fetch of every Gateway API list.
const listTimeout = 10 * time.Second

// errDiscoveryUnavailable means a remote cluster's discovery could not be
// read, so whether Gateway API is installed there is unknown.
var errDiscoveryUnavailable = errors.New("gateway: discovery on the selected cluster is unavailable")

// errListPanicked stands in for the result of a list whose goroutine
// panicked; recoverutil logs the panic itself.
var errListPanicked = errors.New("gateway: list panicked")

// targetError is a failure to resolve a client or schema for the selected
// cluster, answered by httputil.WriteTargetError rather than as a failure of
// a call the cluster received.
type targetError struct{ err error }

func (e targetError) Error() string { return e.err.Error() }
func (e targetError) Unwrap() error { return e.err }

// Handler serves Gateway API HTTP endpoints for the cluster a request
// selects. The local cluster is read through a service-account cache and
// the local Discoverer; a remote cluster through Clients, as the requesting
// identity, with its lists held briefly per identity in remote.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	Clients       k8s.ClusterClients
	Presence      *k8s.Presence
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cache      *cachedData

	remote *remotecache.Cache[*snapshot]
}

type cachedData struct {
	gatewayClasses []GatewayClassSummary
	gateways       []GatewaySummary
	httpRoutes     []HTTPRouteSummary
	routes         []RouteSummary // ALL non-HTTP routes (GRPC, TCP, TLS, UDP), differentiated by Kind
	fetchedAt      time.Time
}

// snapshot is one cluster's Gateway API state as a request sees it.
type snapshot struct {
	status GatewayAPIStatus
	data   *cachedData // nil when Gateway API is not available
	// failed holds the error each failed list returned on a remote cluster,
	// keyed by resource name. It is always empty for the local cluster,
	// where a failed core list fails the whole fetch.
	failed map[string]error
}

// sourceErr returns the first failure among the named sources.
func (s *snapshot) sourceErr(sources ...string) error {
	return firstErr(s.failed, sources...)
}

// firstErr returns the first of sources, in order, that has an error in failed.
func firstErr(failed map[string]error, sources ...string) error {
	for _, src := range sources {
		if err := failed[src]; err != nil {
			return err
		}
	}
	return nil
}

// NewHandler creates a new Gateway API handler.
func NewHandler(
	k8sClient *k8s.ClientFactory,
	discoverer *Discoverer,
	accessChecker *resources.AccessChecker,
	clients k8s.ClusterClients,
	presence *k8s.Presence,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     k8sClient,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		Clients:       clients,
		Presence:      presence,
		Logger:        logger,
		remote:        remotecache.New[*snapshot](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, logger),
	}
}

// EvictRemoteCache drops every identity's cached view of clusterID.
// Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remote.EvictCluster(clusterID)
}

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
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

// writeGetError answers a failed detail Get. The local cluster keeps its
// historical 404; a remote cluster's error is classified without leaking
// its text.
func writeGetError(w http.ResponseWriter, r *http.Request, err error, notFound string) {
	if isLocal(r.Context()) {
		httputil.WriteError(w, http.StatusNotFound, notFound, "")
		return
	}
	httputil.WriteRemoteError(w, err)
}

// canAccess checks if the user can access a Gateway API resource. clusterID
// comes from ctx so the SAR runs against the right cluster (F#9).
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool {
	clusterID := middleware.ClusterIDFromContext(ctx)
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx,
		clusterID,
		user.KubernetesUsername,
		user.KubernetesGroups,
		verb,
		APIGroup,
		resource,
		namespace,
	)
	return err == nil && can
}

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
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("all", func() (any, error) {
		return h.fetchAll(ctx)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedData), nil
}

// fetchAll fills the local service-account cache. A failed core list fails
// the fetch; a failed route list is dropped.
func (h *Handler) fetchAll(ctx context.Context) (*cachedData, error) {
	// nolint:cluster-routing local path: fetchAll backs the local-cluster cache only; remote reads go through fetchRemote.
	dynClient := h.K8sClient.BaseDynamicClient()

	data, failed := h.listSources(ctx, dynClient, sourcesFor(h.Discoverer.Status(ctx)), nil)
	for _, src := range coreSources {
		if err := failed[src]; err != nil {
			return nil, fmt.Errorf("list %s: %w", src, err)
		}
	}
	for src, err := range failed {
		h.Logger.Debug("failed to list routes", "resource", src, "error", err)
	}

	h.cacheMu.Lock()
	h.cache = data
	h.cacheMu.Unlock()

	return data, nil
}

// source is one list a snapshot is built from.
type source struct {
	gvr schema.GroupVersionResource
	add func(d *cachedData, list *unstructured.UnstructuredList)
}

// sourcesFor returns the lists to fetch for a cluster with status: the core
// kinds, then each installed route kind at the version the cluster serves.
func sourcesFor(status GatewayAPIStatus) []source {
	out := []source{
		{GatewayClassGVR, func(d *cachedData, list *unstructured.UnstructuredList) {
			for i := range list.Items {
				d.gatewayClasses = append(d.gatewayClasses, normalizeGatewayClass(&list.Items[i]))
			}
		}},
		{GatewayGVR, func(d *cachedData, list *unstructured.UnstructuredList) {
			for i := range list.Items {
				d.gateways = append(d.gateways, normalizeGateway(&list.Items[i]))
			}
		}},
		{HTTPRouteGVR, func(d *cachedData, list *unstructured.UnstructuredList) {
			for i := range list.Items {
				d.httpRoutes = append(d.httpRoutes, normalizeHTTPRoute(&list.Items[i]))
			}
		}},
	}
	for _, rk := range routeKindOrder {
		gvr, ok := status.routeGVRs[rk]
		if !ok {
			continue
		}
		kind := routeKindToKind[string(rk)]
		out = append(out, source{gvr, func(d *cachedData, list *unstructured.UnstructuredList) {
			for i := range list.Items {
				d.routes = append(d.routes, normalizeRoute(&list.Items[i], kind))
			}
		}})
	}
	return out
}

// listSources lists every source concurrently and independently.
// It returns what listed and, keyed by resource, what failed. When onGone is
// set, a list whose resource type is no longer served counts as empty and is
// reported to onGone instead of failing.
func (h *Handler) listSources(ctx context.Context, dyn dynamic.Interface, sources []source, onGone func(schema.GroupVersionResource)) (*cachedData, map[string]error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	lists := make([]*unstructured.UnstructuredList, len(sources))
	errs := make([]error, len(sources))
	var g errgroup.Group
	for i, src := range sources {
		errs[i] = errListPanicked // overwritten unless the list panics
		recoverutil.Go(&g, h.Logger, "gateway list "+src.gvr.Resource, func() error {
			lists[i], errs[i] = dyn.Resource(src.gvr).List(ctx, metav1.ListOptions{ResourceVersion: "0"})
			return nil
		})
	}
	_ = g.Wait() // every list records its own outcome in errs

	data := &cachedData{
		gatewayClasses: []GatewayClassSummary{},
		gateways:       []GatewaySummary{},
		httpRoutes:     []HTTPRouteSummary{},
		routes:         []RouteSummary{},
		fetchedAt:      time.Now(),
	}
	failed := map[string]error{}
	for i, src := range sources {
		switch err := errs[i]; {
		case err == nil:
			src.add(data, lists[i])
		case onGone != nil && k8s.IsResourceGone(err):
			onGone(src.gvr)
		default:
			failed[src.gvr.Resource] = err
		}
	}
	return data, failed
}

// load returns the Gateway API snapshot for the request's cluster: the
// service-account cache for the local cluster, or a per-identity read of a
// remote one.
func (h *Handler) load(ctx context.Context, user *auth.User) (*snapshot, error) {
	if isLocal(ctx) {
		status := h.Discoverer.Status(ctx)
		if !status.Available {
			return &snapshot{status: status}, nil
		}
		data, err := h.getCached(ctx)
		if err != nil {
			return nil, err
		}
		return &snapshot{status: status, data: data}, nil
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.remote.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
}

// fetchRemote reads a remote cluster's Gateway API state as the user. Each
// list succeeds or fails on its own; only when every list fails is the
// fetch itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*snapshot, error) {
	status, err := h.remoteStatus(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	if !status.Available {
		return &snapshot{status: status}, nil
	}
	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, targetError{err}
	}

	sources := sourcesFor(status)
	data, failed := h.listSources(ctx, dyn, sources, func(gvr schema.GroupVersionResource) {
		// The CRD went away after discovery was cached: re-read it so the
		// next fetch stops asking for it.
		h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gvr.GroupResource())
	})
	if len(failed) == len(sources) {
		return nil, firstErr(failed, summarySources...)
	}
	return &snapshot{status: status, data: data, failed: failed}, nil
}

// remoteStatus reads Gateway API status from a remote cluster's discovery,
// as the user sees it. A definite absence is a status with Reason
// discovery_missing, not an error.
func (h *Handler) remoteStatus(ctx context.Context, clusterID string, user *auth.User) (GatewayAPIStatus, error) {
	now := time.Now().UTC()
	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so a CRD installed later is seen within
	// seconds rather than when the schema cache expires.
	verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gatewaysGR)
	if verdict.Installed != nil && !*verdict.Installed {
		return GatewayAPIStatus{Reason: string(k8s.ReasonDiscoveryMissing), LastChecked: now}, nil
	}

	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return GatewayAPIStatus{}, targetError{err}
	}
	lists, unavailable, failedGroups := k8s.DiscoveryLists(target.Discovery)
	if unavailable || failedGroups[APIGroup] {
		return GatewayAPIStatus{}, errDiscoveryUnavailable
	}
	status := statusFromLists(lists)
	status.LastChecked = now
	if !status.Available {
		status.Reason = string(k8s.ReasonDiscoveryMissing)
	}
	return status, nil
}

// clusterStatus returns the Gateway API status of the request's cluster.
func (h *Handler) clusterStatus(ctx context.Context, user *auth.User) (GatewayAPIStatus, error) {
	if isLocal(ctx) {
		return h.Discoverer.Status(ctx), nil
	}
	return h.remoteStatus(ctx, middleware.ClusterIDFromContext(ctx), user)
}

// writeLoadError answers a failure to read the cluster's Gateway API state.
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, what string) {
	var target targetError
	switch {
	case isLocal(r.Context()):
		h.Logger.Error("failed to fetch "+what, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch "+what, "")
	case errors.As(err, &target):
		httputil.WriteTargetError(w, target.err)
	case errors.Is(err, errDiscoveryUnavailable):
		httputil.WriteErrorWithReason(w, http.StatusBadGateway, "Gateway API discovery on the selected cluster failed", string(k8s.ReasonDiscoveryUnavailable), nil)
	default:
		httputil.WriteRemoteError(w, err)
	}
}

// loadFor loads the snapshot for a list endpoint that reads sources. It
// writes the error response and returns false when the cluster could not
// be read or, on a remote cluster, when one of sources failed. A snapshot
// with nil data means Gateway API is not available there.
func (h *Handler) loadFor(w http.ResponseWriter, r *http.Request, user *auth.User, what string, sources ...string) (*snapshot, bool) {
	snap, err := h.load(r.Context(), user)
	if err != nil {
		h.writeLoadError(w, r, err, what)
		return nil, false
	}
	if err := snap.sourceErr(sources...); err != nil {
		httputil.WriteRemoteError(w, err)
		return nil, false
	}
	return snap, true
}

// HandleStatus returns the Gateway API detection status. On a remote
// cluster a failure to read it is a status with a reason, not an error.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status, err := h.clusterStatus(r.Context(), user)
	if err != nil {
		reason := k8s.ReasonDiscoveryUnavailable
		var target targetError
		if errors.As(err, &target) {
			reason = k8s.ClassifyTargetErr(target.err)
		}
		status = GatewayAPIStatus{Reason: string(reason), LastChecked: time.Now().UTC()}
	}
	httputil.WriteData(w, status)
}

// HandleSummary returns aggregated counts and health per Gateway API kind.
// It spans every source, so on a remote cluster any failed list fails it
// rather than undercounting.
func (h *Handler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	snap, ok := h.loadFor(w, r, user, "gateway data", summarySources...)
	if !ok {
		return
	}
	if snap.data == nil {
		httputil.WriteData(w, GatewayAPISummary{})
		return
	}
	data := snap.data

	// RBAC-filter before counting so users only see counts for resources they can access.
	ctx := r.Context()

	var filteredClasses []GatewayClassSummary
	if h.canAccess(ctx, user, "list", "gatewayclasses", "") {
		filteredClasses = data.gatewayClasses
	}

	summary := computeSummary(
		filteredClasses,
		filterByRBAC(ctx, h, user, "gateways", data.gateways),
		filterByRBAC(ctx, h, user, "httproutes", data.httpRoutes),
		filterByRBAC(ctx, h, user, "httproutes", data.routes),
	)

	httputil.WriteData(w, summary)
}

// computeSummary builds a GatewayAPISummary from the given resource slices.
func computeSummary(gatewayClasses []GatewayClassSummary, gateways []GatewaySummary, httpRoutes []HTTPRouteSummary, routes []RouteSummary) GatewayAPISummary {
	s := GatewayAPISummary{}

	s.GatewayClasses.Total = len(gatewayClasses)
	for _, gc := range gatewayClasses {
		if hasCondition(gc.Conditions, "Accepted", "True") {
			s.GatewayClasses.Healthy++
		} else {
			s.GatewayClasses.Degraded++
		}
	}

	s.Gateways.Total = len(gateways)
	for _, gw := range gateways {
		if hasCondition(gw.Conditions, "Programmed", "True") {
			s.Gateways.Healthy++
		} else {
			s.Gateways.Degraded++
		}
	}

	s.HTTPRoutes.Total = len(httpRoutes)
	for _, hr := range httpRoutes {
		if hasCondition(hr.Conditions, "Accepted", "True") {
			s.HTTPRoutes.Healthy++
		} else {
			s.HTTPRoutes.Degraded++
		}
	}

	routesByKind := map[string]*KindSummary{
		"GRPCRoute": &s.GRPCRoutes,
		"TCPRoute":  &s.TCPRoutes,
		"TLSRoute":  &s.TLSRoutes,
		"UDPRoute":  &s.UDPRoutes,
	}
	for _, rt := range routes {
		ks, ok := routesByKind[rt.Kind]
		if !ok {
			continue
		}
		ks.Total++
		if hasCondition(rt.Conditions, "Accepted", "True") {
			ks.Healthy++
		} else {
			ks.Degraded++
		}
	}

	return s
}

// hasCondition checks if the conditions slice contains a condition with the given type and status.
func hasCondition(conds []Condition, condType, condStatus string) bool {
	for _, c := range conds {
		if c.Type == condType && c.Status == condStatus {
			return true
		}
	}
	return false
}

// HandleListGatewayClasses returns all GatewayClass resources.
func (h *Handler) HandleListGatewayClasses(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	// Cluster-scoped RBAC check
	if !h.canAccess(r.Context(), user, "list", "gatewayclasses", "") {
		httputil.WriteData(w, []GatewayClassSummary{})
		return
	}

	snap, ok := h.loadFor(w, r, user, "gateway classes", "gatewayclasses")
	if !ok {
		return
	}
	if snap.data == nil {
		httputil.WriteData(w, []GatewayClassSummary{})
		return
	}

	httputil.WriteData(w, snap.data.gatewayClasses)
}

// HandleGetGatewayClass returns a single GatewayClass by name.
func (h *Handler) HandleGetGatewayClass(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "gatewayclasses", "") {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(GatewayClassGVR).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get gatewayclass", "name", name, "error", err)
		writeGetError(w, r, err, "gateway class not found")
		return
	}

	httputil.WriteData(w, normalizeGatewayClass(obj))
}

// HandleListGateways returns all Gateway resources filtered by RBAC.
func (h *Handler) HandleListGateways(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	snap, ok := h.loadFor(w, r, user, "gateways", "gateways")
	if !ok {
		return
	}
	if snap.data == nil {
		httputil.WriteData(w, []GatewaySummary{})
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "gateways", snap.data.gateways)
	httputil.WriteData(w, filtered)
}

// HandleGetGateway returns a single Gateway with its attached routes.
func (h *Handler) HandleGetGateway(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "gateways", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(GatewayGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get gateway", "namespace", ns, "name", name, "error", err)
		writeGetError(w, r, err, "gateway not found")
		return
	}

	detail := normalizeGatewayDetail(obj)

	// Resolve attached routes from the cluster's lists, best effort: a
	// route kind that failed to list contributes none.
	if snap, err := h.load(r.Context(), user); err == nil && snap.data != nil {
		var attached []RouteSummary

		// Check HTTPRoutes
		for _, hr := range snap.data.httpRoutes {
			if matchesParentRef(hr.ParentRefs, hr.Namespace, name, ns) {
				attached = append(attached, RouteSummary{
					Kind:       "HTTPRoute",
					Name:       hr.Name,
					Namespace:  hr.Namespace,
					Hostnames:  hr.Hostnames,
					ParentRefs: hr.ParentRefs,
					Conditions: hr.Conditions,
					Age:        hr.Age,
				})
			}
		}

		// Check non-HTTP routes
		for _, rt := range snap.data.routes {
			if matchesParentRef(rt.ParentRefs, rt.Namespace, name, ns) {
				attached = append(attached, rt)
			}
		}

		// RBAC-filter attached routes so users only see routes in namespaces they can access.
		detail.AttachedRoutes = filterByRBAC(r.Context(), h, user, "httproutes", attached)
	}

	if detail.AttachedRoutes == nil {
		detail.AttachedRoutes = []RouteSummary{}
	}

	httputil.WriteData(w, detail)
}

// matchesParentRef checks if any parentRef of a route in routeNs references
// the given gateway. An unset parentRef namespace means the route's own, as
// the Gateway API specifies.
func matchesParentRef(refs []ParentRef, routeNs, gwName, gwNamespace string) bool {
	for _, ref := range refs {
		if ref.Name == gwName && cmp.Or(ref.Namespace, routeNs) == gwNamespace {
			return true
		}
	}
	return false
}

// HandleListHTTPRoutes returns all HTTPRoute resources filtered by RBAC.
func (h *Handler) HandleListHTTPRoutes(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	snap, ok := h.loadFor(w, r, user, "http routes", "httproutes")
	if !ok {
		return
	}
	if snap.data == nil {
		httputil.WriteData(w, []HTTPRouteSummary{})
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "httproutes", snap.data.httpRoutes)
	httputil.WriteData(w, filtered)
}

// HandleGetHTTPRoute returns a single HTTPRoute with resolved parent gateways and backend services.
func (h *Handler) HandleGetHTTPRoute(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "httproutes", ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(HTTPRouteGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get httproute", "namespace", ns, "name", name, "error", err)
		writeGetError(w, r, err, "http route not found")
		return
	}

	detail := normalizeHTTPRouteDetail(obj)

	backendRefs := make([][]BackendRef, 0, len(detail.Rules))
	for ri := range detail.Rules {
		backendRefs = append(backendRefs, detail.Rules[ri].BackendRefs)
	}
	h.resolveRelationships(r.Context(), user, dynClient, ns, detail.ParentRefs, backendRefs...)

	httputil.WriteData(w, detail)
}

// HandleListRoutes returns non-HTTP routes filtered by kind and RBAC.
func (h *Handler) HandleListRoutes(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	kind := strings.ToLower(r.URL.Query().Get("kind"))
	kindName, valid := routeKindToKind[kind]
	if !valid {
		httputil.WriteError(w, http.StatusBadRequest, "missing or invalid kind parameter", "must be one of: grpcroutes, tcproutes, tlsroutes, udproutes")
		return
	}

	snap, ok := h.loadFor(w, r, user, "routes", kind)
	if !ok {
		return
	}
	if snap.data == nil {
		httputil.WriteData(w, []RouteSummary{})
		return
	}

	// Filter by kind
	kindFiltered := make([]RouteSummary, 0, len(snap.data.routes))
	for _, rt := range snap.data.routes {
		if rt.Kind == kindName {
			kindFiltered = append(kindFiltered, rt)
		}
	}

	filtered := filterByRBAC(r.Context(), h, user, kind, kindFiltered)
	httputil.WriteData(w, filtered)
}

// HandleGetRoute returns a single non-HTTP route with resolved parent gateways and backend services.
func (h *Handler) HandleGetRoute(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	kindParam := chi.URLParam(r, "kind")
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	rk := routeKind(strings.ToLower(kindParam))
	if _, valid := routeKindGVR[rk]; !valid {
		httputil.WriteError(w, http.StatusBadRequest, "invalid route kind", "must be one of: grpcroutes, tcproutes, tlsroutes, udproutes")
		return
	}

	if !h.canAccess(r.Context(), user, "get", string(rk), ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	// Get the route at the version the cluster serves it.
	status, err := h.clusterStatus(r.Context(), user)
	if err != nil {
		h.writeLoadError(w, r, err, "route")
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(status.routeGVR(rk)).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get route", "kind", kindParam, "namespace", ns, "name", name, "error", err)
		writeGetError(w, r, err, "route not found")
		return
	}

	if rk == RouteKindGRPC {
		detail := normalizeGRPCRouteDetail(obj)
		backendRefs := make([][]BackendRef, 0, len(detail.Rules))
		for ri := range detail.Rules {
			backendRefs = append(backendRefs, detail.Rules[ri].BackendRefs)
		}
		h.resolveRelationships(r.Context(), user, dynClient, ns, detail.ParentRefs, backendRefs...)
		httputil.WriteData(w, detail)
		return
	}

	kindName := routeKindToKind[string(rk)]
	detail := normalizeSimpleRouteDetail(obj, kindName)
	h.resolveRelationships(r.Context(), user, dynClient, ns, detail.ParentRefs, detail.BackendRefs)
	httputil.WriteData(w, detail)
}

// maxResolveConcurrency caps goroutine fan-out for relationship resolution.
const maxResolveConcurrency = 10

// resolveRelationships fills in parent gateway conditions and backend
// Service existence for a route in routeNs, on the request's cluster. Unset
// parent and backend namespaces mean the route's own, as the Gateway API
// specifies. Best effort: bounded by a 2s timeout and maxResolveConcurrency,
// and a lookup that fails leaves its ref unresolved.
func (h *Handler) resolveRelationships(ctx context.Context, user *auth.User, dynClient dynamic.Interface, routeNs string, parentRefs []ParentRef, backendRefs ...[]BackendRef) {
	// Resolve a typed client only when there is a Service to look up.
	var serviceRefs []*BackendRef
	for _, refs := range backendRefs {
		for i := range refs {
			if refs[i].Kind == "Service" || refs[i].Kind == "" {
				serviceRefs = append(serviceRefs, &refs[i])
			}
		}
	}
	var cs kubernetes.Interface
	if len(serviceRefs) > 0 {
		var err error
		cs, err = h.Clients.ClientForCluster(ctx, middleware.ClusterIDFromContext(ctx), user.KubernetesUsername, user.KubernetesGroups)
		if err != nil {
			h.Logger.Debug("backend service resolution skipped", "error", err)
			serviceRefs = nil
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	sem := make(chan struct{}, maxResolveConcurrency)
	var wg sync.WaitGroup
	run := func(label string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recoverutil.Safe(h.Logger, label, func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				fn()
			})
		}()
	}

	for i := range parentRefs {
		ref := &parentRefs[i]
		run("gateway resolve-parent-gateway", func() {
			gw, err := dynClient.Resource(GatewayGVR).Namespace(cmp.Or(ref.Namespace, routeNs)).Get(ctx, ref.Name, metav1.GetOptions{})
			if err == nil {
				ref.GatewayConditions = extractConditions(gw.Object, "status", "conditions")
			}
		})
	}

	for _, ref := range serviceRefs {
		run("gateway resolve-backend-service", func() {
			_, err := cs.CoreV1().Services(cmp.Or(ref.Namespace, routeNs)).Get(ctx, ref.Name, metav1.GetOptions{})
			if err == nil {
				ref.Resolved = true
			}
		})
	}

	wg.Wait()
}
