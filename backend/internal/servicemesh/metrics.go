package servicemesh

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/prometheus/common/model"

	"github.com/kubecenter/kubecenter/internal/monitoring"
)

// promQueryTimeout bounds every PromQL query originating from this
// package's handlers. The plan explicitly forbids blocking the UI on
// Prometheus, so we degrade to "unavailable" on timeout.
//
// The underlying monitoring.PrometheusClient.Query wraps the caller's
// context with its own 10s WithTimeout. context.WithTimeout selects the
// earlier deadline, so this 2s budget is authoritative regardless of the
// client's internal value. Callers in this package rely on that
// ordering — do not raise this above 10s without also reviewing the
// client.
const promQueryTimeout = 2 * time.Second

// GoldenSignals carries RPS + latency quantiles + error rate for a
// single service. All values are zero when no traffic was observed —
// Available stays true so the UI can distinguish "silent service" from
// "metrics subsystem offline".
//
// MissingQueries names any of the six PromQL fan-out queries that
// failed (timed out, returned no scalar, or errored). It's omitted on a
// fully-successful response so callers can treat absence as success.
// When non-empty AND Available=true the response represents partial
// data; the UI surfaces this so a heavily-degraded Prometheus answering
// only one query with zeros isn't indistinguishable from a silent
// meshed service.
type GoldenSignals struct {
	Mesh      MeshType `json:"mesh"`
	Namespace string   `json:"namespace"`
	Service   string   `json:"service"`

	Available      bool     `json:"available"`
	Reason         string   `json:"reason,omitempty"`         // populated only when Available=false
	MissingQueries []string `json:"missingQueries,omitempty"` // populated when partial-success

	RPS       float64 `json:"rps"`
	ErrorRate float64 `json:"errorRate"` // fraction 0..1
	P50Ms     float64 `json:"p50Ms"`
	P95Ms     float64 `json:"p95Ms"`
	P99Ms     float64 `json:"p99Ms"`
}

// goldenSignalsTemplates are the six PromQL templates needed per mesh.
// They are declared per-mesh because Istio's destination-centric labels
// (destination_service_name) and Linkerd's inbound-authority labels
// (authority=...) are incompatible shapes — no shared template set.
type goldenSignalsTemplates struct {
	rps       monitoring.QueryTemplate
	errorNum  monitoring.QueryTemplate
	errorDen  monitoring.QueryTemplate
	latencyP  func(q string) monitoring.QueryTemplate
}

// istioTemplates use the v1.18+ Istio standard metric names. Labels
// follow the "destination_service_*" convention documented in Istio's
// metrics reference and the plan's Phase B approach section.
var istioTemplates = goldenSignalsTemplates{
	rps: monitoring.QueryTemplate{
		Query:     `sum(rate(istio_requests_total{destination_service_name="$svc",destination_service_namespace="$ns"}[2m]))`,
		Variables: []string{"svc", "ns"},
	},
	errorNum: monitoring.QueryTemplate{
		Query:     `sum(rate(istio_requests_total{destination_service_name="$svc",destination_service_namespace="$ns",response_code=~"5.."}[2m]))`,
		Variables: []string{"svc", "ns"},
	},
	errorDen: monitoring.QueryTemplate{
		Query:     `sum(rate(istio_requests_total{destination_service_name="$svc",destination_service_namespace="$ns"}[2m]))`,
		Variables: []string{"svc", "ns"},
	},
	latencyP: func(quantile string) monitoring.QueryTemplate {
		return monitoring.QueryTemplate{
			Query:     fmt.Sprintf(`histogram_quantile(%s, sum by (le) (rate(istio_request_duration_milliseconds_bucket{destination_service_name="$svc",destination_service_namespace="$ns"}[2m])))`, quantile),
			Variables: []string{"svc", "ns"},
		}
	},
}

// linkerdTemplates use the linkerd-proxy metric names with
// direction=inbound so we count traffic terminated by the destination
// workload, not egress from peers. `authority` is the Host header and
// matches the default <svc>.<ns>.svc.cluster.local form.
var linkerdTemplates = goldenSignalsTemplates{
	rps: monitoring.QueryTemplate{
		Query:     `sum(rate(request_total{direction="inbound",authority="$svc.$ns.svc.cluster.local"}[2m]))`,
		Variables: []string{"svc", "ns"},
	},
	errorNum: monitoring.QueryTemplate{
		Query:     `sum(rate(response_total{direction="inbound",authority="$svc.$ns.svc.cluster.local",classification="failure"}[2m]))`,
		Variables: []string{"svc", "ns"},
	},
	errorDen: monitoring.QueryTemplate{
		Query:     `sum(rate(response_total{direction="inbound",authority="$svc.$ns.svc.cluster.local"}[2m]))`,
		Variables: []string{"svc", "ns"},
	},
	latencyP: func(quantile string) monitoring.QueryTemplate {
		return monitoring.QueryTemplate{
			Query:     fmt.Sprintf(`histogram_quantile(%s, sum by (le) (rate(response_latency_ms_bucket{direction="inbound",authority="$svc.$ns.svc.cluster.local"}[2m])))`, quantile),
			Variables: []string{"svc", "ns"},
		}
	},
}

// templatesForMesh selects the mesh-specific template set. Callers must
// pre-validate that the mesh is one we support; returning an error here
// keeps the handler path narrow.
func templatesForMesh(mesh MeshType) (goldenSignalsTemplates, error) {
	switch mesh {
	case MeshIstio:
		return istioTemplates, nil
	case MeshLinkerd:
		return linkerdTemplates, nil
	}
	return goldenSignalsTemplates{}, fmt.Errorf("unsupported mesh %q", mesh)
}

// goldenSignalsForService runs the six queries in parallel, derives
// the ErrorRate ratio from its two components, and returns a populated
// GoldenSignals. Context-deadline breaches produce a degraded result
// with Available=false rather than an error — this matches the plan's
// "never block the UI" contract.
//
// Partial-failure policy: if any subset of queries succeeds, those
// results are used and Available is true. p99 histogram_quantile is
// reliably the slowest query — under load it is the most likely to
// time out. Discarding RPS and error-rate because p99 didn't return
// would hide the signals operators need most in that moment. Missing
// signals are zero-valued (matching the "silent service" convention);
// only a full fan-out failure flips Available to false.
func goldenSignalsForService(ctx context.Context, pc *monitoring.PrometheusClient, mesh MeshType, namespace, service string) (GoldenSignals, error) {
	result := GoldenSignals{Mesh: mesh, Namespace: namespace, Service: service}

	// Validate mesh first so unsupported values surface as a hard error —
	// a nil Prometheus client with a bogus mesh should not silently
	// "succeed" with an unavailable response.
	templates, err := templatesForMesh(mesh)
	if err != nil {
		return result, err
	}

	if pc == nil {
		result.Available = false
		result.Reason = "metrics_unavailable"
		return result, nil
	}

	vars := map[string]string{"svc": service, "ns": namespace}

	// Render all queries up front. Any render failure is a validation
	// error — the injected values failed the monitoring package's k8s-name
	// check — and should surface to the caller as a 400.
	queries := map[string]monitoring.QueryTemplate{
		"rps":      templates.rps,
		"errorNum": templates.errorNum,
		"errorDen": templates.errorDen,
		"p50":      templates.latencyP("0.50"),
		"p95":      templates.latencyP("0.95"),
		"p99":      templates.latencyP("0.99"),
	}

	rendered := make(map[string]string, len(queries))
	for name, q := range queries {
		rq, rerr := q.Render(vars)
		if rerr != nil {
			return result, fmt.Errorf("render %s: %w", name, rerr)
		}
		rendered[name] = rq
	}

	queryCtx, cancel := context.WithTimeout(ctx, promQueryTimeout)
	defer cancel()

	type outcome struct {
		name  string
		value float64
		ok    bool
		err   error
	}
	outcomes := make(chan outcome, len(rendered))
	now := time.Now()

	for name, q := range rendered {
		go func(name, q string) {
			val, _, qerr := pc.Query(queryCtx, q, now)
			if qerr != nil {
				outcomes <- outcome{name: name, err: qerr}
				return
			}
			scalar, ok := firstScalarValue(val)
			outcomes <- outcome{name: name, value: scalar, ok: ok}
		}(name, q)
	}

	results := make(map[string]float64, len(rendered))
	for i := 0; i < len(rendered); i++ {
		o := <-outcomes
		if o.err != nil || !o.ok {
			continue
		}
		results[o.name] = o.value
	}

	// Full fan-out failure is the only state where Available flips false.
	// Any partial success still counts — operators would rather see RPS
	// and error-rate without latency than a blank panel.
	if len(results) == 0 {
		result.Available = false
		result.Reason = "metrics_unavailable"
		return result, nil
	}

	result.Available = true
	result.RPS = results["rps"]
	den := results["errorDen"]
	if den > 0 {
		result.ErrorRate = results["errorNum"] / den
	}
	result.P50Ms = nanToZero(results["p50"])
	result.P95Ms = nanToZero(results["p95"])
	result.P99Ms = nanToZero(results["p99"])

	// Surface partial success so the UI can flag a degraded Prometheus
	// (one query answering with zeros vs all six answering) rather than
	// silently rendering zeros that look like an idle service. Iterate
	// in stable order so the response shape is deterministic.
	for _, name := range []string{"rps", "errorNum", "errorDen", "p50", "p95", "p99"} {
		if _, ok := results[name]; !ok {
			result.MissingQueries = append(result.MissingQueries, name)
		}
	}

	return result, nil
}

// nanToZero collapses NaN values from histogram_quantile (which can
// return NaN when there is no traffic) to a clean 0 so the JSON
// payload never carries a NaN.
func nanToZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// firstScalarValue pulls a single numeric value from a Prometheus
// instant-query result. Empty vectors (no traffic in the window)
// cleanly return (0, true) — "silent service" is normal state.
//
// pc.Query is an instant query; its result is always Scalar or Vector.
// We do not handle Matrix here because a Matrix response would indicate
// the client was swapped with QueryRange — a mismatch the caller should
// fix at the call site, not paper over by taking the last sample.
// Unknown types return (0, false) so the caller treats the query as
// having no usable value.
func firstScalarValue(v model.Value) (float64, bool) {
	switch val := v.(type) {
	case *model.Scalar:
		return float64(val.Value), true
	case model.Vector:
		if len(val) == 0 {
			return 0, true
		}
		return float64(val[0].Value), true
	}
	return 0, false
}
