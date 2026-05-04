package externalsecrets

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/common/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/monitoring"
)

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

	pc := h.promClient()
	if pc == nil {
		httputil.WriteData(w, metricsResponse{Error: "rate metrics offline"})
		return
	}

	rate, last24h, sampleAt, err := queryStoreMetrics(r.Context(), pc, store)
	if err != nil {
		// Prometheus reachable but query failed (parse error, no such
		// metric yet, etc.). Caller gets the error string, not a 500.
		httputil.WriteData(w, metricsResponse{Error: "rate metrics offline"})
		return
	}

	billing := ResolveBillingProvider(store.Provider, store.ProviderSpec)
	var requestVolume float64
	if last24h != nil {
		requestVolume = *last24h
	}
	cost := EstimateCost(billing, requestVolume, 24*time.Hour)

	httputil.WriteData(w, metricsResponse{
		RatePerMin: rate,
		Last24h:    last24h,
		Cost:       cost,
		WindowEnd:  sampleAt,
	})
}

// promClient returns the active Prometheus client, or nil when monitoring is
// not wired or hasn't discovered Prometheus yet.
func (h *Handler) promClient() *monitoring.PrometheusClient {
	if h.MonitoringDisc == nil {
		return nil
	}
	return h.MonitoringDisc.PrometheusClient()
}

// queryStoreMetrics runs the per-store PromQL pair and returns rate-per-minute
// and 24h count. Either may be nil if Prometheus has no series yet for this
// store; the caller treats nil as "no data" and EstimateCost returns
// cost-zero (still a valid card with the rate-card snapshot date).
func queryStoreMetrics(ctx context.Context, pc *monitoring.PrometheusClient, store SecretStore) (rate, last24h *float64, sampleAt time.Time, err error) {
	// store.Name + store.Namespace already gated by isMetricLabelSafe in
	// the handler, so direct interpolation is safe. The metric name is a
	// constant. ClusterSecretStores have empty namespace; ESO still emits
	// the label, so we pass the empty string and the matcher behaves
	// correctly.
	rateQuery := fmt.Sprintf(
		`sum(rate(%s{store=%q,namespace=%q}[5m]))`,
		MetricSyncCallsTotal, store.Name, store.Namespace,
	)
	countQuery := fmt.Sprintf(
		`sum(increase(%s{store=%q,namespace=%q}[24h]))`,
		MetricSyncCallsTotal, store.Name, store.Namespace,
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
