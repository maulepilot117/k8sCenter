package externalsecrets

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
)

// promQuerier is the minimum subset of *monitoring.PrometheusClient this
// package needs. Declared as an interface so tests inject a fake without
// standing up a full Prometheus client.
type promQuerier interface {
	Query(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error)
}

// metricsResponse is the JSON shape returned by the per-store metrics
// endpoints. RatePerMin and Last24h are pointers so the frontend can
// distinguish "metric is genuinely zero" from "Prometheus is offline" —
// callers MUST NOT fabricate a zero on degradation per R25.
type metricsResponse struct {
	RatePerMin *float64      `json:"ratePerMin"`         // last-5m sum(rate(...)) projected to per-minute
	Last24h    *float64      `json:"last24h"`            // sum_over_time over the last 24h, requests
	Cost       *CostEstimate `json:"cost,omitempty"`     // nil when self-hosted or metrics unavailable
	Error      string        `json:"error,omitempty"`    // populated on degradation; HTTP is still 200
	WindowEnd  time.Time     `json:"windowEnd,omitzero"` // sample timestamp; useful when Prom is far behind clock
}

// HandleGetStoreMetrics returns request-rate and cost estimates for a single
// namespaced SecretStore. RBAC enforced at the secretstore level (not the
// secret level): a user who can `get secretstore` here gets the metrics for
// it, even if they couldn't compose the equivalent PromQL via the generic
// monitoring/query endpoint.
//
// GET /externalsecrets/stores/{namespace}/{name}/metrics
func (h *Handler) HandleGetStoreMetrics(w http.ResponseWriter, r *http.Request) {
	h.handleGetStoreMetrics(w, r, "Namespaced")
}

// HandleGetClusterStoreMetrics is the cluster-scoped twin.
// GET /externalsecrets/clusterstores/{name}/metrics
func (h *Handler) HandleGetClusterStoreMetrics(w http.ResponseWriter, r *http.Request) {
	h.handleGetStoreMetrics(w, r, "Cluster")
}

func (h *Handler) handleGetStoreMetrics(w http.ResponseWriter, r *http.Request, scope string) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
		return
	}

	name := chi.URLParam(r, "name")
	ns := ""
	resource := "clustersecretstores"
	gvr := ClusterSecretStoreGVR
	if scope == "Namespaced" {
		ns = chi.URLParam(r, "namespace")
		resource = "secretstores"
		gvr = SecretStoreGVR
	}

	if !isMetricLabelSafe(name) || (ns != "" && !isMetricLabelSafe(ns)) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid name or namespace", "")
		return
	}

	if !h.canAccess(r.Context(), user, "get", resource, ns) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	store, err := h.fetchStoreImpersonated(r.Context(), user, gvr, ns, name, scope)
	if err != nil {
		writeStoreFetchError(w, h.Logger, err, scope, ns, name)
		return
	}

	pc := h.promQuerierClient()
	if pc == nil {
		httputil.WriteData(w, metricsResponse{Error: "rate metrics offline"})
		return
	}

	// Resolve dependent ExternalSecrets via the service-account cache.
	// ESO emits sync metrics keyed by (namespace, name) of the
	// ExternalSecret — there is no `store` label — so per-store rates
	// aggregate the metric across every ES that references this store.
	// Using the cache (vs an impersonated list) is intentional: the rate
	// is a property of the store itself, and the caller has already
	// passed the get-secretstore RBAC check.
	depMatchers, err := h.dependentMatchers(r.Context(), store)
	if err != nil {
		h.Logger.Error("resolve dependent ESes for metrics", "scope", scope, "namespace", ns, "name", name, "error", err)
		httputil.WriteData(w, metricsResponse{Error: "rate metrics offline"})
		return
	}
	if len(depMatchers) == 0 {
		// No dependent ESes — surface zero deterministically. Cost is
		// suppressed via the last24h-nil branch below.
		httputil.WriteData(w, metricsResponse{
			RatePerMin: nil,
			Last24h:    nil,
			WindowEnd:  time.Now(),
		})
		return
	}

	rate, last24h, sampleAt, err := queryStoreMetrics(r.Context(), pc, depMatchers)
	if err != nil {
		// Prometheus reachable but query failed (parse error, no such
		// metric yet, etc.). Caller gets the error string, not a 500.
		httputil.WriteData(w, metricsResponse{Error: "rate metrics offline"})
		return
	}

	// Suppress the cost block when we have no usage data — paid-tier stores
	// otherwise render "$0.00" indistinguishable from "actually free".
	// Frontend treats nil cost as "no card"; explicit zero with samples
	// still renders correctly.
	var cost *CostEstimate
	if last24h != nil {
		billing := ResolveBillingProvider(store.Provider, store.ProviderSpec)
		cost = EstimateCost(billing, *last24h, 24*time.Hour)
	}

	httputil.WriteData(w, metricsResponse{
		RatePerMin: rate,
		Last24h:    last24h,
		Cost:       cost,
		WindowEnd:  sampleAt,
	})
}

// promQuerierClient returns the active Prometheus querier, or nil when
// monitoring is not wired or hasn't discovered Prometheus yet. Honors
// promQuerierOverride so tests don't have to construct a real client.
func (h *Handler) promQuerierClient() promQuerier {
	if h.promQuerierOverride != nil {
		return h.promQuerierOverride
	}
	if h.MonitoringDisc == nil {
		return nil
	}
	pc := h.MonitoringDisc.PrometheusClient()
	if pc == nil {
		return nil
	}
	return pc
}

// dependentMatcher describes one ExternalSecret that contributes to a
// store's aggregated metric. Both fields are pre-validated as DNS-label safe
// (k8s API rejects non-conforming names on admission, and we additionally
// run them through `isMetricLabelSafe` before interpolation).
type dependentMatcher struct {
	Namespace string // empty for cross-namespace aggregation (ClusterSecretStore)
	Name      string
}

// dependentMatchers returns the (namespace, name) pairs of every
// ExternalSecret that references the given store. For namespaced
// SecretStores the result is constrained to the store's namespace
// (k8s scope rules forbid cross-namespace references). For
// ClusterSecretStores any namespace is fair game.
func (h *Handler) dependentMatchers(ctx context.Context, store SecretStore) ([]dependentMatcher, error) {
	data, err := h.getCached(ctx)
	if err != nil {
		return nil, err
	}
	wantKind := "SecretStore"
	if store.Scope == "Cluster" {
		wantKind = "ClusterSecretStore"
	}

	out := make([]dependentMatcher, 0, 16)
	for _, es := range data.externalSecrets {
		if es.StoreRef.Name != store.Name || es.StoreRef.Kind != wantKind {
			continue
		}
		if store.Scope == "Namespaced" && es.Namespace != store.Namespace {
			continue
		}
		// Defensive: skip names that wouldn't survive PromQL escaping.
		// In practice these never make it past k8s admission.
		if !isMetricLabelSafe(es.Name) || (es.Namespace != "" && !isMetricLabelSafe(es.Namespace)) {
			continue
		}
		out = append(out, dependentMatcher{Namespace: es.Namespace, Name: es.Name})
	}
	return out, nil
}

// queryStoreMetrics runs PromQL aggregating the per-ExternalSecret sync metric
// across the given dependent set. Returns rate-per-minute and 24h count.
// Either may be nil if Prometheus has no series yet for the dependent set;
// the caller treats nil as "no data" and suppresses the cost block.
func queryStoreMetrics(ctx context.Context, pc promQuerier, deps []dependentMatcher) (rate, last24h *float64, sampleAt time.Time, err error) {
	// Build a single matcher set: namespace=~"ns1|ns2",name=~"es1|es2".
	// PromQL evaluates this as the cross-product, which would over-match
	// when two ESes in different namespaces share a name. Acceptable for
	// v1: ESO already constrains namespaced-store dependents to a single
	// namespace, and ClusterSecretStore name-collisions across namespaces
	// are rare in practice. A future revision can switch to the explicit
	// `or`-of-vectors form if collisions surface.
	nsRegex, nameRegex := buildDepRegexes(deps)
	rateQuery := fmt.Sprintf(
		`sum(rate(%s{namespace=~%q,name=~%q}[5m]))`,
		MetricSyncCallsTotal, nsRegex, nameRegex,
	)
	countQuery := fmt.Sprintf(
		`sum(increase(%s{namespace=~%q,name=~%q}[24h]))`,
		MetricSyncCallsTotal, nsRegex, nameRegex,
	)

	now := time.Now()
	rateVal, _, err := pc.Query(ctx, rateQuery, now)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	cntVal, _, err := pc.Query(ctx, countQuery, now)
	if err != nil {
		return nil, nil, time.Time{}, err
	}

	if r := scalarFromVector(rateVal); r != nil {
		// Convert per-second to per-minute for display.
		rpm := *r * 60
		rate = &rpm
	}
	if c := scalarFromVector(cntVal); c != nil {
		last24h = c
	}
	return rate, last24h, now, nil
}

// buildDepRegexes returns the namespace and name regex strings that match
// the dependent-ES set. Names are quoted with regexp.QuoteMeta so DNS-safe
// characters that have regex meaning ('.' and '-') match literally.
// Duplicate namespaces are deduped to keep the regex compact.
func buildDepRegexes(deps []dependentMatcher) (nsRegex, nameRegex string) {
	if len(deps) == 0 {
		return "", ""
	}
	nsSet := make(map[string]struct{}, len(deps))
	names := make([]string, 0, len(deps))
	for _, d := range deps {
		if d.Namespace != "" {
			nsSet[d.Namespace] = struct{}{}
		}
		names = append(names, regexp.QuoteMeta(d.Name))
	}
	if len(nsSet) == 0 {
		// Cross-namespace ClusterSecretStore aggregation — match any.
		nsRegex = ".+"
	} else {
		ns := make([]string, 0, len(nsSet))
		for k := range nsSet {
			ns = append(ns, regexp.QuoteMeta(k))
		}
		nsRegex = strings.Join(ns, "|")
	}
	nameRegex = strings.Join(names, "|")
	return nsRegex, nameRegex
}

// scalarFromVector pulls the single-sample value out of a Prometheus
// instant-vector response. Returns nil for empty vectors (no series yet) so
// the caller can distinguish missing data from explicit zero.
func scalarFromVector(v model.Value) *float64 {
	vec, ok := v.(model.Vector)
	if !ok {
		return nil
	}
	if len(vec) == 0 {
		return nil
	}
	val := float64(vec[0].Value)
	return &val
}

// fetchStoreImpersonated performs an impersonated Get and normalizes the
// result. Mirrors handleGetStore's read path so we share the same RBAC and
// error-mapping behaviour.
func (h *Handler) fetchStoreImpersonated(ctx context.Context, user *auth.User, gvr schema.GroupVersionResource, ns, name, scope string) (SecretStore, error) {
	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return SecretStore{}, err
	}
	obj, err := dynClient.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return SecretStore{}, err
	}
	return normalizeSecretStore(obj, scope), nil
}

// writeStoreFetchError maps an impersonated-Get error into the canonical
// 403/404/500 envelope used elsewhere in this handler.
func writeStoreFetchError(w http.ResponseWriter, logger *slog.Logger, err error, scope, ns, name string) {
	switch {
	case apierrors.IsForbidden(err):
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
	case apierrors.IsNotFound(err):
		httputil.WriteError(w, http.StatusNotFound, "store not found", "")
	default:
		if logger != nil {
			logger.Error("get store for metrics", "scope", scope, "namespace", ns, "name", name, "error", err)
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch store", "")
	}
}

// isMetricLabelSafe restricts ns/name characters to the same alphanumeric +
// '-' + '.' alphabet Kubernetes allows. Anything else would be a malformed
// resource name; rejecting early keeps the PromQL builder safe even though
// we use %q quoting downstream.
func isMetricLabelSafe(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '.':
		default:
			return false
		}
	}
	return true
}
