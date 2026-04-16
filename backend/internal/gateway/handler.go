package gateway

import (
	"context"
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
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// routeKindToKind maps query-param route kind values to Gateway API Kind names.
var routeKindToKind = map[string]string{
	"grpcroutes": "GRPCRoute",
	"tcproutes":  "TCPRoute",
	"tlsroutes":  "TLSRoute",
	"udproutes":  "UDPRoute",
}

// Handler serves Gateway API HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cache      *cachedData
	cacheGen   uint64
}

type cachedData struct {
	gatewayClasses []GatewayClassSummary
	gateways       []GatewaySummary
	httpRoutes     []HTTPRouteSummary
	routes         []RouteSummary        // ALL non-HTTP routes (GRPC, TCP, TLS, UDP), differentiated by Kind
	summary        GatewayAPISummary     // pre-computed unfiltered summary
	fetchedAt      time.Time
}

// NewHandler creates a new Gateway API handler.
func NewHandler(
	k8sClient *k8s.ClientFactory,
	discoverer *Discoverer,
	accessChecker *resources.AccessChecker,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     k8sClient,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		Logger:        logger,
	}
}

// InvalidateCache clears the cached data.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cacheGen++
	h.cache = nil
	h.cacheMu.Unlock()
}

// getImpersonatingClient creates a dynamic client impersonating the user and handles errors.
func (h *Handler) getImpersonatingClient(w http.ResponseWriter, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return nil, false
	}
	return client, true
}

// canAccess checks if the user can access a Gateway API resource.
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx,
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
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("all", func() (any, error) {
		return h.fetchAll(ctx, gen)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedData), nil
}

func (h *Handler) fetchAll(ctx context.Context, gen uint64) (*cachedData, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dynClient := h.K8sClient.BaseDynamicClient()

	var (
		gatewayClasses []GatewayClassSummary
		gateways       []GatewaySummary
		httpRoutes     []HTTPRouteSummary
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		list, err := dynClient.Resource(GatewayClassGVR).List(gctx, metav1.ListOptions{ResourceVersion: "0"})
		if err != nil {
			return fmt.Errorf("list gatewayclasses: %w", err)
		}
		gatewayClasses = make([]GatewayClassSummary, 0, len(list.Items))
		for i := range list.Items {
			gatewayClasses = append(gatewayClasses, normalizeGatewayClass(&list.Items[i]))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(GatewayGVR).Namespace("").List(gctx, metav1.ListOptions{ResourceVersion: "0"})
		if err != nil {
			return fmt.Errorf("list gateways: %w", err)
		}
		gateways = make([]GatewaySummary, 0, len(list.Items))
		for i := range list.Items {
			gateways = append(gateways, normalizeGateway(&list.Items[i]))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(HTTPRouteGVR).Namespace("").List(gctx, metav1.ListOptions{ResourceVersion: "0"})
		if err != nil {
			return fmt.Errorf("list httproutes: %w", err)
		}
		httpRoutes = make([]HTTPRouteSummary, 0, len(list.Items))
		for i := range list.Items {
			httpRoutes = append(httpRoutes, normalizeHTTPRoute(&list.Items[i]))
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Fetch non-HTTP routes based on which kinds are installed.
	status := h.Discoverer.Status(ctx)
	installedSet := make(map[string]bool, len(status.InstalledKinds))
	for _, k := range status.InstalledKinds {
		installedSet[k] = true
	}

	var routes []RouteSummary
	var routesMu sync.Mutex

	g2, gctx2 := errgroup.WithContext(ctx)

	for rk, gvr := range routeKindGVR {
		if !installedSet[string(rk)] {
			continue
		}
		kindName := routeKindToKind[string(rk)]
		capturedGVR := gvr
		capturedKind := kindName
		g2.Go(func() error {
			list, err := dynClient.Resource(capturedGVR).Namespace("").List(gctx2, metav1.ListOptions{})
			if err != nil {
				h.Logger.Debug("failed to list routes", "kind", capturedKind, "error", err)
				return nil // skip unavailable kinds gracefully
			}
			items := make([]RouteSummary, 0, len(list.Items))
			for i := range list.Items {
				items = append(items, normalizeRoute(&list.Items[i], capturedKind))
			}
			routesMu.Lock()
			routes = append(routes, items...)
			routesMu.Unlock()
			return nil
		})
	}

	_ = g2.Wait()

	// Pre-compute unfiltered summary to avoid O(n) iteration on every summary request.
	sum := computeSummary(gatewayClasses, gateways, httpRoutes, routes)

	data := &cachedData{
		gatewayClasses: gatewayClasses,
		gateways:       gateways,
		httpRoutes:     httpRoutes,
		routes:         routes,
		summary:        sum,
		fetchedAt:      time.Now(),
	}

	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cache = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// HandleStatus returns the Gateway API detection status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	_, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	httputil.WriteData(w, status)
}

// HandleSummary returns aggregated counts and health per Gateway API kind.
func (h *Handler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, GatewayAPISummary{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch gateway data", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch gateway data", "")
		return
	}

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

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []GatewayClassSummary{})
		return
	}

	// Cluster-scoped RBAC check
	if !h.canAccess(r.Context(), user, "list", "gatewayclasses", "") {
		httputil.WriteData(w, []GatewayClassSummary{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch gateway classes", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch gateway classes", "")
		return
	}

	httputil.WriteData(w, data.gatewayClasses)
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

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(GatewayClassGVR).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get gatewayclass", "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "gateway class not found", "")
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

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []GatewaySummary{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch gateways", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch gateways", "")
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "gateways", data.gateways)
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

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(GatewayGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get gateway", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "gateway not found", "")
		return
	}

	detail := normalizeGatewayDetail(obj)

	// Resolve attached routes from cache.
	cached, err := h.getCached(r.Context())
	if err == nil {
		var attached []RouteSummary

		// Check HTTPRoutes
		for _, hr := range cached.httpRoutes {
			if matchesParentRef(hr.ParentRefs, name, ns) {
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
		for _, rt := range cached.routes {
			if matchesParentRef(rt.ParentRefs, name, ns) {
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

// matchesParentRef checks if any parentRef in the list references the given gateway name and namespace.
func matchesParentRef(refs []ParentRef, gwName, gwNamespace string) bool {
	for _, ref := range refs {
		if ref.Name == gwName && ref.Namespace == gwNamespace {
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

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []HTTPRouteSummary{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch http routes", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch http routes", "")
		return
	}

	filtered := filterByRBAC(r.Context(), h, user, "httproutes", data.httpRoutes)
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

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(HTTPRouteGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get httproute", "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "http route not found", "")
		return
	}

	detail := normalizeHTTPRouteDetail(obj)

	h.resolveRouteRelationships(r.Context(), user, dynClient, detail.ParentRefs, detail.Rules)

	httputil.WriteData(w, detail)
}

// HandleListRoutes returns non-HTTP routes filtered by kind and RBAC.
func (h *Handler) HandleListRoutes(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	kind := r.URL.Query().Get("kind")
	kindName, valid := routeKindToKind[strings.ToLower(kind)]
	if !valid {
		httputil.WriteError(w, http.StatusBadRequest, "missing or invalid kind parameter", "must be one of: grpcroutes, tcproutes, tlsroutes, udproutes")
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteData(w, []RouteSummary{})
		return
	}

	data, err := h.getCached(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch routes", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch routes", "")
		return
	}

	// Filter by kind
	kindFiltered := make([]RouteSummary, 0, len(data.routes))
	for _, rt := range data.routes {
		if rt.Kind == kindName {
			kindFiltered = append(kindFiltered, rt)
		}
	}

	filtered := filterByRBAC(r.Context(), h, user, strings.ToLower(kind), kindFiltered)
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
	gvr, valid := routeKindGVR[rk]
	if !valid {
		httputil.WriteError(w, http.StatusBadRequest, "invalid route kind", "must be one of: grpcroutes, tcproutes, tlsroutes, udproutes")
		return
	}

	if !h.canAccess(r.Context(), user, "get", string(rk), ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	obj, err := dynClient.Resource(gvr).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get route", "kind", kindParam, "namespace", ns, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "route not found", "")
		return
	}

	if rk == RouteKindGRPC {
		detail := normalizeGRPCRouteDetail(obj)
		// Resolve parent gateways
		h.resolveParentGateways(r.Context(), dynClient, detail.ParentRefs)
		// Resolve backend services from rules
		for ri := range detail.Rules {
			h.resolveBackendServices(r.Context(), user, ns, detail.Rules[ri].BackendRefs)
		}
		httputil.WriteData(w, detail)
		return
	}

	kindName := routeKindToKind[string(rk)]
	detail := normalizeSimpleRouteDetail(obj, kindName)
	// Resolve parent gateways and backend services
	h.resolveParentGateways(r.Context(), dynClient, detail.ParentRefs)
	h.resolveBackendServices(r.Context(), user, ns, detail.BackendRefs)
	httputil.WriteData(w, detail)
}

// maxResolveConcurrency caps goroutine fan-out for relationship resolution.
const maxResolveConcurrency = 10

// resolveRouteRelationships resolves parent gateway conditions and backend service existence
// for HTTPRoute detail views. Uses a WaitGroup with a 2s timeout and bounded concurrency.
func (h *Handler) resolveRouteRelationships(ctx context.Context, user *auth.User, dynClient dynamic.Interface, parentRefs []ParentRef, rules []HTTPRouteRule) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Hoist typed client creation outside goroutine loop.
	cs, csErr := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)

	sem := make(chan struct{}, maxResolveConcurrency)
	var wg sync.WaitGroup

	// Resolve parent gateways
	for i := range parentRefs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ref := &parentRefs[idx]
			gwObj, err := dynClient.Resource(GatewayGVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				return
			}
			ref.GatewayConditions = extractConditions(gwObj.Object, "status", "conditions")
		}(i)
	}

	// Resolve backend services
	if csErr == nil {
		for ri := range rules {
			for bi := range rules[ri].BackendRefs {
				wg.Add(1)
				go func(ruleIdx, backendIdx int) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					ref := &rules[ruleIdx].BackendRefs[backendIdx]
					if ref.Kind != "Service" && ref.Kind != "" {
						return
					}
					svcNs := ref.Namespace
					if svcNs == "" {
						if len(parentRefs) > 0 {
							svcNs = parentRefs[0].Namespace
						}
					}
					_, err := cs.CoreV1().Services(svcNs).Get(ctx, ref.Name, metav1.GetOptions{})
					if err == nil {
						ref.Resolved = true
					}
				}(ri, bi)
			}
		}
	}

	wg.Wait()
}

// resolveParentGateways resolves gateway conditions for parent refs.
func (h *Handler) resolveParentGateways(ctx context.Context, dynClient dynamic.Interface, parentRefs []ParentRef) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	sem := make(chan struct{}, maxResolveConcurrency)
	var wg sync.WaitGroup
	for i := range parentRefs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ref := &parentRefs[idx]
			gwObj, err := dynClient.Resource(GatewayGVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				return
			}
			ref.GatewayConditions = extractConditions(gwObj.Object, "status", "conditions")
		}(i)
	}
	wg.Wait()
}

// resolveBackendServices checks existence of backend service refs.
func (h *Handler) resolveBackendServices(ctx context.Context, user *auth.User, routeNs string, refs []BackendRef) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Hoist typed client creation outside goroutine loop.
	cs, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return
	}

	sem := make(chan struct{}, maxResolveConcurrency)
	var wg sync.WaitGroup
	for i := range refs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ref := &refs[idx]
			if ref.Kind != "Service" && ref.Kind != "" {
				return
			}
			svcNs := ref.Namespace
			if svcNs == "" {
				svcNs = routeNs
			}
			_, svcErr := cs.CoreV1().Services(svcNs).Get(ctx, ref.Name, metav1.GetOptions{})
			if svcErr == nil {
				ref.Resolved = true
			}
		}(i)
	}
	wg.Wait()
}
