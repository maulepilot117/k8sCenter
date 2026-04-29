package servicemesh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/monitoring"
)

const meshCacheTTL = 30 * time.Second

// Handler serves service-mesh HTTP endpoints.
//
// The struct mirrors gitops.Handler and policy.Handler: service-account
// clients populate a shared cache, then per-user RBAC filtering runs on
// every request. Writes are deferred to a later phase; v1 is read-only.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger

	// MonitoringDisc is optional; when set, Phase-B endpoints use it to
	// query Prometheus for metric-backed cross-checks (mTLS posture) and
	// golden-signal metrics. Nil is a supported configuration — the
	// handler degrades to policy-only results.
	MonitoringDisc *monitoring.Discoverer

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cache      *cachedMeshData
	cacheGen   uint64 // incremented on invalidation; prevents stale writes

	// dynOverride, when non-nil, replaces K8sClient.BaseDynamicClient() for
	// cache-population reads. Exposed only to tests in this package.
	dynOverride dynamic.Interface

	// clientsetOverride is the typed-clientset test seam mirroring
	// dynOverride; used by HandleMTLSPosture when listing pods in tests.
	clientsetOverride kubernetes.Interface

	// promClientOverride lets tests inject a PrometheusClient without
	// standing up a monitoring.Discoverer.
	promClientOverride *monitoring.PrometheusClient
}

type cachedMeshData struct {
	routes    []TrafficRoute
	policies  []MeshedPolicy
	errors    map[string]string // "{mesh}/{Kind}" → error message
	fetchedAt time.Time
}

// namespacedResource is implemented by the mesh types so the RBAC filter can
// pull the item's namespace without reflection. Cluster-scoped resources
// return "" and are delegated to admin-only visibility.
type namespacedResource interface {
	getNamespace() string
	getMesh() MeshType
	getKind() string
}

func (r TrafficRoute) getNamespace() string { return r.Namespace }
func (r TrafficRoute) getMesh() MeshType    { return r.Mesh }
func (r TrafficRoute) getKind() string      { return r.Kind }

func (p MeshedPolicy) getNamespace() string { return p.Namespace }
func (p MeshedPolicy) getMesh() MeshType    { return p.Mesh }
func (p MeshedPolicy) getKind() string      { return p.Kind }

// meshKindDispatch maps (mesh, kind) pairs to the Kubernetes API group,
// resource name, and GroupVersionResource needed for RBAC checks and
// dynamic-client reads. A missing entry means the kind is unknown and the
// caller should return 400 — never silently fall through, since an unknown
// kind would bypass the access check.
type meshKindEntry struct {
	APIGroup string
	Resource string
	GVR      schema.GroupVersionResource
}

var meshKindDispatch = map[string]meshKindEntry{
	// Istio routing (networking.istio.io)
	"istio/VirtualService":  {APIGroup: "networking.istio.io", Resource: "virtualservices", GVR: IstioVirtualServiceGVR},
	"istio/DestinationRule": {APIGroup: "networking.istio.io", Resource: "destinationrules", GVR: IstioDestinationRuleGVR},
	"istio/Gateway":         {APIGroup: "networking.istio.io", Resource: "gateways", GVR: IstioGatewayGVR},
	// Istio policy (security.istio.io)
	"istio/PeerAuthentication":  {APIGroup: "security.istio.io", Resource: "peerauthentications", GVR: IstioPeerAuthenticationGVR},
	"istio/AuthorizationPolicy": {APIGroup: "security.istio.io", Resource: "authorizationpolicies", GVR: IstioAuthorizationPolicyGVR},
	// Linkerd routing
	"linkerd/ServiceProfile": {APIGroup: "linkerd.io", Resource: "serviceprofiles", GVR: LinkerdServiceProfileGVR},
	"linkerd/Server":         {APIGroup: "policy.linkerd.io", Resource: "servers", GVR: LinkerdServerGVR},
	"linkerd/HTTPRoute":      {APIGroup: "policy.linkerd.io", Resource: "httproutes", GVR: LinkerdHTTPRouteGVR},
	// Linkerd policy
	"linkerd/AuthorizationPolicy":   {APIGroup: "policy.linkerd.io", Resource: "authorizationpolicies", GVR: LinkerdAuthorizationPolicyGVR},
	"linkerd/MeshTLSAuthentication": {APIGroup: "policy.linkerd.io", Resource: "meshtlsauthentications", GVR: LinkerdMeshTLSAuthenticationGVR},
}

// kindCodeLookup reverses the composite-ID shortcodes back to the Kind name.
// Keyed by "{mesh}:{code}" so "istio:ap" and "linkerd:ap" disambiguate.
var kindCodeLookup = map[string]string{
	"istio:vs":     "VirtualService",
	"istio:dr":     "DestinationRule",
	"istio:gw":     "Gateway",
	"istio:pa":     "PeerAuthentication",
	"istio:ap":     "AuthorizationPolicy",
	"linkerd:sp":   "ServiceProfile",
	"linkerd:srv":  "Server",
	"linkerd:hr":   "HTTPRoute",
	"linkerd:ap":   "AuthorizationPolicy",
	"linkerd:mtls": "MeshTLSAuthentication",
}

// resolveKind returns the dispatch entry for a composite-ID shortcode.
// Unknown mesh/code pairs return ok=false so handlers can emit 400.
func resolveKind(mesh, code string) (kind string, entry meshKindEntry, ok bool) {
	kind, kok := kindCodeLookup[mesh+":"+code]
	if !kok {
		return "", meshKindEntry{}, false
	}
	entry, eok := meshKindDispatch[mesh+"/"+kind]
	if !eok {
		return "", meshKindEntry{}, false
	}
	return kind, entry, true
}

// fetchData returns cached routes/policies, refreshing if stale.
// Concurrent callers coalesce via singleflight. The cache is populated with
// the service account (cluster-wide visibility); callers must filter by RBAC
// before exposing to users.
func (h *Handler) fetchData(ctx context.Context) (*cachedMeshData, error) {
	h.cacheMu.RLock()
	if h.cache != nil && time.Since(h.cache.fetchedAt) < meshCacheTTL {
		data := h.cache
		h.cacheMu.RUnlock()
		return data, nil
	}
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("mesh-fetch", func() (any, error) {
		return h.doFetch(ctx)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedMeshData), nil
}

// doFetch queries both mesh adapters based on discovery status and merges
// results. Kinds that return per-CRD errors (e.g., 403) are recorded in the
// errors map rather than failing the whole fetch — partial data is a better
// UX than a 500.
func (h *Handler) doFetch(ctx context.Context) (*cachedMeshData, error) {
	h.cacheMu.RLock()
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	dynClient := h.dynClient()
	if dynClient == nil {
		return &cachedMeshData{errors: map[string]string{}, fetchedAt: time.Now()}, nil
	}

	status := h.Discoverer.Status(ctx)

	var (
		istio   IstioListResult
		linkerd LinkerdListResult
		wg      sync.WaitGroup
	)

	if status.Istio != nil && status.Istio.Installed {
		wg.Go(func() {
			istio = ListIstio(ctx, dynClient, "")
		})
	}
	if status.Linkerd != nil && status.Linkerd.Installed {
		wg.Go(func() {
			linkerd = ListLinkerd(ctx, dynClient, "")
		})
	}
	wg.Wait()

	errs := map[string]string{}
	for kind, msg := range istio.Errors {
		errs["istio/"+kind] = msg
	}
	for kind, msg := range linkerd.Errors {
		errs["linkerd/"+kind] = msg
	}

	data := &cachedMeshData{
		routes:    append(append([]TrafficRoute(nil), istio.Routes...), linkerd.Routes...),
		policies:  append(append([]MeshedPolicy(nil), istio.Policies...), linkerd.Policies...),
		errors:    errs,
		fetchedAt: time.Now(),
	}

	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cache = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// InvalidateCache clears the cache so the next call re-fetches. Exported for
// CRD event handlers to hook into, mirroring gitops/policy.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cache = nil
	h.cacheGen++
	h.cacheMu.Unlock()
}

func (h *Handler) dynClient() dynamic.Interface {
	if h.dynOverride != nil {
		return h.dynOverride
	}
	if h.K8sClient == nil {
		return nil
	}
	return h.K8sClient.BaseDynamicClient()
}

// filterByRBAC returns only items in namespaces the user can list the
// corresponding CRD in. Cache key is per-(mesh/kind, namespace) so a single
// namespace visited by multiple kinds only triggers one SSAR per kind group.
// Cluster-scoped items (empty namespace) are admin-only.
func filterByRBAC[T namespacedResource](ctx context.Context, h *Handler, user *auth.User, items []T) []T {
	type accessKey struct {
		apiGroup  string
		resource  string
		namespace string
	}
	access := map[accessKey]bool{}
	out := make([]T, 0, len(items))

	for _, item := range items {
		ns := item.getNamespace()
		if ns == "" {
			if auth.IsAdmin(user) {
				out = append(out, item)
			}
			continue
		}

		entry, ok := meshKindDispatch[string(item.getMesh())+"/"+item.getKind()]
		if !ok {
			// Unknown kind: fail closed rather than leaking unchecked data.
			continue
		}
		key := accessKey{entry.APIGroup, entry.Resource, ns}
		allowed, checked := access[key]
		if !checked {
			can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", entry.APIGroup, entry.Resource, ns)
			allowed = err == nil && can
			access[key] = allowed
		}
		if allowed {
			out = append(out, item)
		}
	}
	return out
}

// MeshStatusResponse wraps MeshStatus under a `status` key so all
// /mesh/* endpoints share one envelope shape. Without this wrapper,
// /mesh/status returned MeshStatus flat while the routing, policies,
// mtls, and golden-signals endpoints all embed it under `status` —
// forcing clients to special-case the one endpoint.
type MeshStatusResponse struct {
	Status MeshStatus `json:"status"`
}

// HandleStatus returns detected mesh installations. Non-admin users see a
// stripped view without control-plane namespace details, matching the
// gitops/policy precedent.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())

	if !auth.IsAdmin(user) {
		if status.Istio != nil {
			stripped := *status.Istio
			stripped.Namespace = ""
			status.Istio = &stripped
		}
		if status.Linkerd != nil {
			stripped := *status.Linkerd
			stripped.Namespace = ""
			status.Linkerd = &stripped
		}
	}

	httputil.WriteData(w, MeshStatusResponse{Status: status})
}

// routingResponse is the envelope for GET /mesh/routing. When no mesh is
// installed, `Status.Detected == MeshNone` and `Routes` is an empty slice —
// never nil — so the frontend can treat this as a normal empty-state.
type routingResponse struct {
	Status MeshStatus        `json:"status"`
	Routes []TrafficRoute    `json:"routes"`
	Errors map[string]string `json:"errors,omitempty"`
}

type policiesResponse struct {
	Status   MeshStatus        `json:"status"`
	Policies []MeshedPolicy    `json:"policies"`
	Errors   map[string]string `json:"errors,omitempty"`
}

// HandleListRoutes returns RBAC-filtered traffic routes across both meshes.
// The optional ?namespace= query parameter further scopes the list.
func (h *Handler) HandleListRoutes(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())

	data, err := h.fetchData(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch mesh data", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch mesh data", "")
		return
	}

	routes := data.routes
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		scoped := make([]TrafficRoute, 0, len(routes))
		for _, tr := range routes {
			if tr.Namespace == ns {
				scoped = append(scoped, tr)
			}
		}
		routes = scoped
	}

	routes = filterByRBAC(r.Context(), h, user, routes)
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Namespace != routes[j].Namespace {
			return routes[i].Namespace < routes[j].Namespace
		}
		if routes[i].Kind != routes[j].Kind {
			return routes[i].Kind < routes[j].Kind
		}
		return routes[i].Name < routes[j].Name
	})

	if routes == nil {
		routes = []TrafficRoute{}
	}

	httputil.WriteData(w, routingResponse{
		Status: status,
		Routes: routes,
		Errors: data.errors,
	})
}

// HandleListPolicies returns RBAC-filtered mesh-security CRDs across both meshes.
func (h *Handler) HandleListPolicies(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())

	data, err := h.fetchData(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch mesh data", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch mesh data", "")
		return
	}

	policies := data.policies
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		scoped := make([]MeshedPolicy, 0, len(policies))
		for _, p := range policies {
			if p.Namespace == ns {
				scoped = append(scoped, p)
			}
		}
		policies = scoped
	}

	policies = filterByRBAC(r.Context(), h, user, policies)
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Namespace != policies[j].Namespace {
			return policies[i].Namespace < policies[j].Namespace
		}
		if policies[i].Kind != policies[j].Kind {
			return policies[i].Kind < policies[j].Kind
		}
		return policies[i].Name < policies[j].Name
	})

	if policies == nil {
		policies = []MeshedPolicy{}
	}

	httputil.WriteData(w, policiesResponse{
		Status:   status,
		Policies: policies,
		Errors:   data.errors,
	})
}

// HandleGetRoute returns a single normalized route by composite ID.
// The ID format is "{mesh}:{namespace}:{kindCode}:{name}", URL-encoded on the wire.
// Uses user impersonation for the fetch, so Kubernetes RBAC is authoritative.
func (h *Handler) HandleGetRoute(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	mesh, ns, code, name, err := parseMeshCompositeID(chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid route ID", err.Error())
		return
	}

	kind, entry, ok := resolveKind(mesh, code)
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "unknown mesh or kind", mesh+":"+code)
		return
	}

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "get", entry.APIGroup, entry.Resource, ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to view this resource", "")
		return
	}

	dynClient, derr := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if derr != nil {
		h.Logger.Error("failed to create impersonating client", "error", derr)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	obj, gerr := dynClient.Resource(entry.GVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if gerr != nil {
		if apierrors.IsNotFound(gerr) {
			httputil.WriteError(w, http.StatusNotFound, "mesh resource not found", "")
			return
		}
		h.Logger.Error("failed to get mesh resource", "mesh", mesh, "kind", kind, "namespace", ns, "name", name, "error", gerr)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch mesh resource", "")
		return
	}

	route := normalizeRouteByMesh(MeshType(mesh), kind, obj)
	httputil.WriteData(w, route)
}

// normalizeRouteByMesh dispatches to the per-mesh normalizer. Callers must
// ensure mesh/kind are valid (handled upstream via resolveKind).
func normalizeRouteByMesh(mesh MeshType, kind string, obj *unstructured.Unstructured) TrafficRoute {
	switch mesh {
	case MeshIstio:
		return normalizeIstioRoute(obj, kind)
	case MeshLinkerd:
		return normalizeLinkerdRoute(obj, kind)
	}
	return TrafficRoute{
		Mesh:      mesh,
		Kind:      kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Raw:       obj.Object,
	}
}

// userClient returns an impersonating Kubernetes clientset scoped to the
// authenticated user. All request-time read paths in this handler MUST
// go through this helper — using BaseClientset() (the service account's
// own credentials) would violate the impersonation rule in CLAUDE.md
// and mis-attribute the call in the Kubernetes audit log.
//
// The clientsetOverride test seam is preserved for unit tests; fake
// clientsets stand in for a real impersonating client in table tests.
func (h *Handler) userClient(user *auth.User) (kubernetes.Interface, error) {
	if h.clientsetOverride != nil {
		return h.clientsetOverride, nil
	}
	if h.K8sClient == nil {
		return nil, errors.New("no kubernetes client configured")
	}
	return h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
}

// promClient returns the Prometheus client if monitoring is configured.
// Nil is a supported state — handlers degrade gracefully without it.
func (h *Handler) promClient() *monitoring.PrometheusClient {
	if h.promClientOverride != nil {
		return h.promClientOverride
	}
	if h.MonitoringDisc == nil {
		return nil
	}
	return h.MonitoringDisc.PrometheusClient()
}

// HandleMTLSPosture returns per-workload mTLS posture. Optional
// ?namespace= query param scopes to a single namespace; omitted means
// cluster-wide (RBAC-filtered).
//
// The handler is read-only and degrades gracefully:
//   - no mesh → empty workloads slice with status.detected == "none"
//   - Prometheus offline → policy-only results
//   - cluster-wide request → Prom cross-check fires against an
//     unfiltered template that aggregates across all namespaces.
//     queryIstioMTLSRatios picks the scoped vs cluster-wide template
//     based on the namespace argument.
//
// Partial failure policy: pod-list and policy-fetch failures are
// accumulated into resp.Errors with user-safe messages and internal
// details in the log; the handler continues with whatever data it has
// rather than failing the request. System errors (RBAC check failed,
// impersonation failed) are 5xx; RBAC denial is 403.
func (h *Handler) HandleMTLSPosture(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	resp := MTLSPostureResponse{Status: status, Workloads: []WorkloadMTLS{}}

	if status.Detected == MeshNone {
		httputil.WriteData(w, resp)
		return
	}

	namespace := r.URL.Query().Get("namespace")
	errs := map[string]string{}

	// RBAC: a checker error is a system fault (500); a plain "no" is 403.
	// Conflating the two would mask infrastructure outages as access
	// denials.
	can, aerr := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "", "pods", namespace)
	if aerr != nil {
		h.Logger.Error("mTLS posture RBAC check failed", "user", user.KubernetesUsername, "namespace", namespace, "error", aerr)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}
	if !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to view mTLS posture for this scope", "")
		return
	}

	cs, cerr := h.userClient(user)
	if cerr != nil {
		h.Logger.Error("mTLS posture: impersonating client unavailable", "user", user.KubernetesUsername, "error", cerr)
		httputil.WriteError(w, http.StatusInternalServerError, "kubernetes client unavailable", "")
		return
	}

	pods, truncated, listErr := listNamespacePods(r.Context(), cs, namespace)
	if listErr != nil {
		h.Logger.Error("failed to list pods for mTLS posture", "user", user.KubernetesUsername, "namespace", namespace, "error", listErr)
		errs["pods"] = "failed to list pods"
		pods = nil
	} else if truncated {
		// Surface the silent-truncation hazard called out in the Phase B
		// review: a namespace (or cluster-wide request) with more than
		// meshListCap pods returns a partial posture table with no
		// Continue handling. Make the partial nature visible instead of
		// pretending the result is complete.
		if namespace == "" {
			errs["truncated"] = fmt.Sprintf("result capped at %d pods; pass ?namespace= to scope the request", meshListCap)
		} else {
			errs["truncated"] = fmt.Sprintf("result capped at %d pods in namespace %q", meshListCap, namespace)
		}
	}

	var peerAuths []peerAuthRef
	data, derr := h.fetchData(r.Context())
	if derr != nil {
		h.Logger.Error("failed to fetch mesh policies for mTLS posture", "user", user.KubernetesUsername, "error", derr)
		errs["policies"] = "failed to fetch mesh policies"
	} else {
		peerAuths = peerAuthsFromPolicies(data.policies)
	}

	// Scope PAs to the mesh root (always in scope) plus the requested
	// namespace when a filter is applied. Full-slice expression
	// (peerAuths[:0:0]) prevents the filter from aliasing into a
	// cache-backed backing array if peerAuthsFromPolicies ever returns a
	// view instead of a copy.
	if namespace != "" && len(peerAuths) > 0 {
		filtered := peerAuths[:0:0]
		for _, pa := range peerAuths {
			if pa.Namespace == namespace || pa.Namespace == istioMeshRootNamespace {
				filtered = append(filtered, pa)
			}
		}
		peerAuths = filtered
	}

	// rsOwners makes WorkloadKind authoritative for RS-owned pods. The
	// list call uses the same impersonating client and budget as the
	// pod list, but degrades silently — RBAC denial, timeout, or any
	// other failure leaves the map nil and workloadKey falls back to
	// the alphabet heuristic with WorkloadKindConfident=false. We do
	// not surface this as a partial-failure error key because the
	// fallback still produces a usable response; clients can read
	// WorkloadKindConfident on each row.
	var rsOwners map[string]rsOwner
	if rss, rerr := listReplicaSetControllers(r.Context(), cs, namespace); rerr != nil {
		h.Logger.Debug("rs owner lookup unavailable; workload kinds derived heuristically", "user", user.KubernetesUsername, "namespace", namespace, "error", rerr)
	} else {
		rsOwners = rss
	}

	postures := computePodPostures(pods, peerAuths, rsOwners)
	workloads := aggregateWorkloads(postures)

	// Prom cross-check fires for both scoped and cluster-wide requests.
	// queryIstioMTLSRatios switches between the namespace-filtered and
	// cluster-wide PromQL templates based on the namespace argument; the
	// cross-check still degrades silently to policy-only when Prometheus
	// is offline. Skip the call when workloads is empty: applyMTLSMetricOverrides
	// is a no-op on an empty slice, so the Prom round-trip would have no
	// consumer (covers both pod-list-failure and zero-meshed-pods cases).
	if pc := h.promClient(); pc != nil && len(workloads) > 0 {
		ratios, perr := queryIstioMTLSRatios(r.Context(), pc, namespace)
		if perr != nil {
			h.Logger.Warn("mTLS metric cross-check failed; falling back to policy-only", "user", user.KubernetesUsername, "namespace", namespace, "error", perr)
			errs["prometheus-cross-check"] = "metric cross-check unavailable; posture derived from policies only"
		} else {
			workloads = applyMTLSMetricOverrides(workloads, ratios)
		}
	}

	if data != nil {
		maps.Copy(errs, data.errors)
	}
	if len(errs) > 0 {
		resp.Errors = errs
	}
	resp.Workloads = workloads
	httputil.WriteData(w, resp)
}

// GoldenSignalsResponse envelopes GoldenSignals with the common
// MeshStatus so clients can disambiguate "no mesh installed" from
// "service not meshed" without a separate probe. Exported so external
// consumers get a compile-time signal on wire-shape changes, mirroring
// MTLSPostureResponse.
type GoldenSignalsResponse struct {
	Status  MeshStatus    `json:"status"`
	Signals GoldenSignals `json:"signals"`
}

// HandleGoldenSignals returns RPS, error rate, and p50/p95/p99 latency
// for a single mesh-managed service.
//
// Required query params: namespace, service, mesh=istio|linkerd. The
// mesh param is explicit in v1; auto-detection based on sidecar
// annotations is deferred to Phase D when the frontend can pass
// workload context. Input values are re-validated through the existing
// monitoring.QueryTemplate render path — the handler never concatenates
// user input into PromQL.
//
// Error semantics mirror HandleMTLSPosture: RBAC checker failure is a
// 500 (system fault), RBAC denial is a 403, invalid param is a 400 with
// a user-safe message. Internal render errors are logged in full but
// never echoed to the response body.
func (h *Handler) HandleGoldenSignals(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	namespace := r.URL.Query().Get("namespace")
	service := r.URL.Query().Get("service")
	meshParam := r.URL.Query().Get("mesh")

	if namespace == "" || service == "" {
		httputil.WriteError(w, http.StatusBadRequest, "namespace and service are required", "")
		return
	}

	mesh, merr := resolveMeshParam(meshParam, status)
	if merr != nil {
		httputil.WriteError(w, http.StatusBadRequest, merr.Error(), "")
		return
	}

	// RBAC: require pod-list in the target namespace. Golden signals name
	// the service, but the underlying workload lives in the namespace; if
	// the user can't read pods there, the metric breakdown would leak
	// workload names they shouldn't see.
	can, aerr := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "list", "", "pods", namespace)
	if aerr != nil {
		h.Logger.Error("golden signals RBAC check failed", "user", user.KubernetesUsername, "namespace", namespace, "error", aerr)
		httputil.WriteError(w, http.StatusInternalServerError, "permission check failed", "")
		return
	}
	if !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to view metrics for this namespace", "")
		return
	}

	pc := h.promClient()
	signals, gerr := goldenSignalsForService(r.Context(), pc, mesh, namespace, service)
	if gerr != nil {
		// Validation errors come from the monitoring package's k8s-name
		// guard inside QueryTemplate.Render. The full cause goes to the
		// log; the response says only what the caller needs to retry.
		h.Logger.Warn("golden signals render failed", "user", user.KubernetesUsername, "namespace", namespace, "service", service, "mesh", mesh, "error", gerr)
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace or service name", "")
		return
	}

	httputil.WriteData(w, GoldenSignalsResponse{Status: status, Signals: signals})
}

// resolveMeshParam validates the `?mesh=` query parameter against the
// discovered mesh installation. Empty param + exactly one mesh installed
// → that mesh. Empty param + none or both installed → error (ambiguous
// or impossible). Explicit param must match an installed mesh.
func resolveMeshParam(param string, status MeshStatus) (MeshType, error) {
	switch param {
	case string(MeshIstio):
		if status.Istio == nil || !status.Istio.Installed {
			return MeshNone, errors.New("mesh=istio requested but istio is not installed")
		}
		return MeshIstio, nil
	case string(MeshLinkerd):
		if status.Linkerd == nil || !status.Linkerd.Installed {
			return MeshNone, errors.New("mesh=linkerd requested but linkerd is not installed")
		}
		return MeshLinkerd, nil
	case "":
		switch status.Detected {
		case MeshIstio:
			return MeshIstio, nil
		case MeshLinkerd:
			return MeshLinkerd, nil
		case MeshBoth:
			return MeshNone, errors.New("both meshes installed — pass ?mesh=istio or ?mesh=linkerd")
		}
		return MeshNone, errors.New("no service mesh detected")
	}
	return MeshNone, fmt.Errorf("unsupported mesh %q", param)
}

// parseMeshCompositeID splits "{mesh}:{namespace}:{kindCode}:{name}" into
// its four parts. The id may arrive URL-encoded from chi.URLParam.
func parseMeshCompositeID(id string) (mesh, namespace, code, name string, err error) {
	decoded, uerr := url.PathUnescape(id)
	if uerr != nil {
		decoded = id
	}
	parts := strings.SplitN(decoded, ":", 4)
	if len(parts) != 4 {
		return "", "", "", "", fmt.Errorf("invalid composite ID %q (expected mesh:namespace:kind:name)", decoded)
	}
	if slices.Contains(parts, "") {
		return "", "", "", "", errors.New("composite ID parts must be non-empty")
	}
	return parts[0], parts[1], parts[2], parts[3], nil
}
