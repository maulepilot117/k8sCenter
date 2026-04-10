package monitoring

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kubecenter/kubecenter/internal/httputil"
)

// maxQueryLength is the maximum allowed PromQL query string length.
const maxQueryLength = 4096

// Handler serves monitoring HTTP endpoints.
type Handler struct {
	Discoverer *Discoverer
	Logger     *slog.Logger
}

// HandleStatus returns the current monitoring discovery status.
// GET /api/v1/monitoring/status
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	httputil.WriteData(w, h.Discoverer.Status())
}

// HandleRediscover forces an immediate re-discovery.
// POST /api/v1/monitoring/rediscover
func (h *Handler) HandleRediscover(w http.ResponseWriter, r *http.Request) {
	h.Discoverer.Discover(r.Context())
	httputil.WriteData(w, h.Discoverer.Status())
}

// HandleQuery proxies an instant PromQL query to Prometheus.
// GET /api/v1/monitoring/query?query=...&time=...
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	pc := h.Discoverer.PrometheusClient()
	if pc == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"Prometheus is not available",
			"Monitoring has not been configured. Deploy kube-prometheus-stack or configure an external Prometheus endpoint.")
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		httputil.WriteError(w, http.StatusBadRequest, "query parameter is required", "")
		return
	}
	if len(query) > maxQueryLength {
		httputil.WriteError(w, http.StatusBadRequest, "query exceeds maximum length", "")
		return
	}

	ts := time.Now()
	if t := r.URL.Query().Get("time"); t != "" {
		parsed, err := time.Parse(time.RFC3339, t)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid time parameter", err.Error())
			return
		}
		ts = parsed
	}

	result, warnings, err := pc.Query(r.Context(), query, ts)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Prometheus query failed", err.Error())
		return
	}

	httputil.WriteData(w, map[string]any{
		"resultType": result.Type(),
		"result":     result,
		"warnings":   warnings,
	})
}

// HandleQueryRange proxies a range PromQL query to Prometheus.
// GET /api/v1/monitoring/query_range?query=...&start=...&end=...&step=...
func (h *Handler) HandleQueryRange(w http.ResponseWriter, r *http.Request) {
	pc := h.Discoverer.PrometheusClient()
	if pc == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"Prometheus is not available", "")
		return
	}

	q := r.URL.Query()
	query := q.Get("query")
	if query == "" {
		httputil.WriteError(w, http.StatusBadRequest, "query parameter is required", "")
		return
	}
	if len(query) > maxQueryLength {
		httputil.WriteError(w, http.StatusBadRequest, "query exceeds maximum length", "")
		return
	}

	start, err := time.Parse(time.RFC3339, q.Get("start"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid start parameter", err.Error())
		return
	}
	end, err := time.Parse(time.RFC3339, q.Get("end"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid end parameter", err.Error())
		return
	}
	step, err := time.ParseDuration(q.Get("step"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid step parameter (use Go duration like 15s, 1m)", err.Error())
		return
	}

	result, warnings, err := pc.QueryRange(r.Context(), query, start, end, step)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Prometheus range query failed", err.Error())
		return
	}

	httputil.WriteData(w, map[string]any{
		"resultType": result.Type(),
		"result":     result,
		"warnings":   warnings,
	})
}

// HandleDashboards lists provisioned KubeCenter dashboards from Grafana.
// GET /api/v1/monitoring/dashboards
func (h *Handler) HandleDashboards(w http.ResponseWriter, r *http.Request) {
	gc := h.Discoverer.GrafanaAPIClient()
	if gc == nil {
		httputil.WriteData(w, []any{})
		return
	}

	results, err := gc.SearchDashboards(r.Context(), "kubecenter")
	if err != nil {
		h.Logger.Error("failed to search dashboards", "error", err)
		httputil.WriteData(w, []any{})
		return
	}

	httputil.WriteData(w, results)
}

// HandleResourceDashboard returns the dashboard mapping for a resource kind.
// GET /api/v1/monitoring/resource-dashboard?kind=pods
func (h *Handler) HandleResourceDashboard(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		httputil.WriteError(w, http.StatusBadRequest, "kind parameter is required", "")
		return
	}

	mapping, ok := ResourceDashboardMap[kind]
	if !ok {
		httputil.WriteData(w, map[string]any{"available": false})
		return
	}

	status := h.Discoverer.Status()
	httputil.WriteData(w, map[string]any{
		"available":      status.Grafana.Available,
		"dashboardUID":   mapping.UID,
		"varName":        mapping.VarName,
		"grafanaProxied": status.Grafana.Available && h.Discoverer.GrafanaProxy() != nil,
	})
}

// allowedGrafanaPathPrefixes are the only path prefixes allowed through the proxy.
var allowedGrafanaPathPrefixes = []string{
	"/d/",
	"/d-solo/",
	"/api/dashboards/",
	"/api/folders/",
	"/api/search",
	"/public/",
}

// GrafanaProxy handles all requests to /api/v1/monitoring/grafana/proxy/*.
func (h *Handler) GrafanaProxy(w http.ResponseWriter, r *http.Request) {
	proxy := h.Discoverer.GrafanaProxy()
	if proxy == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"Grafana is not available", "")
		return
	}

	// Extract the path after the proxy prefix
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/monitoring/grafana/proxy")
	if path == "" {
		path = "/"
	}

	// Block path traversal
	if strings.Contains(path, "..") || strings.Contains(path, "%2e") || strings.Contains(path, "%2E") {
		httputil.WriteError(w, http.StatusForbidden, "invalid path", "")
		return
	}

	// Allowlist path prefixes
	allowed := false
	for _, prefix := range allowedGrafanaPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		httputil.WriteError(w, http.StatusForbidden,
			"path not allowed through monitoring proxy", "")
		return
	}

	proxy.ServeHTTP(w, r)
}

// HandleTemplates returns the available PromQL query templates.
// GET /api/v1/monitoring/templates
func (h *Handler) HandleTemplates(w http.ResponseWriter, r *http.Request) {
	// Convert map to slice for consistent JSON output
	templates := make([]QueryTemplate, 0, len(QueryTemplates))
	for _, t := range QueryTemplates {
		templates = append(templates, t)
	}
	httputil.WriteData(w, templates)
}

// HandleTemplateQuery renders a named template with variables and executes it.
// GET /api/v1/monitoring/templates/query?name=pod_cpu_usage&namespace=default&pod=my-pod
func (h *Handler) HandleTemplateQuery(w http.ResponseWriter, r *http.Request) {
	pc := h.Discoverer.PrometheusClient()
	if pc == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"Prometheus is not available", "")
		return
	}

	name := r.URL.Query().Get("name")
	tmpl, ok := QueryTemplates[name]
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "unknown template: "+name, "")
		return
	}

	vars := make(map[string]string)
	for _, v := range tmpl.Variables {
		val := r.URL.Query().Get(v)
		if val == "" {
			httputil.WriteError(w, http.StatusBadRequest, "missing variable: "+v, "")
			return
		}
		vars[v] = val
	}

	query, err := tmpl.Render(vars)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	result, warnings, err := pc.Query(r.Context(), query, time.Now())
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Prometheus query failed", err.Error())
		return
	}

	httputil.WriteData(w, map[string]any{
		"template":   name,
		"query":      query,
		"resultType": result.Type(),
		"result":     result,
		"warnings":   warnings,
	})
}
