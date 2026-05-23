package monitoring

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// maxQueryLength is the maximum allowed PromQL query string length.
const maxQueryLength = 4096

// Query range caps — Finding P2-4 of the 2026-05-22 security audit.
const (
	maxQueryRangeDuration = 6 * time.Hour
	minQueryStep          = 10 * time.Second
	maxQuerySamples       = 11000
)

// Handler serves monitoring HTTP endpoints.
type Handler struct {
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger
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
// Requires admin role — see routes.go. Raw PromQL access is admin-only (P2-4).
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
// Requires admin role — see routes.go. Raw PromQL access is admin-only (P2-4).
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

	// Apply caps even for admin raw queries (P2-4).
	if err := validateRangeCaps(start, end, step); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
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

// HandleSlugQuery executes a named, server-owned PromQL template from the
// Registry. Non-admin users call this instead of the raw /query endpoints.
// RBAC is enforced per-slug using the RequiredGVR field.
// Finding P2-4 of the 2026-05-22 security audit.
//
// GET /api/v1/monitoring/queries/*
//   - {slug} path segment captured by chi wildcard (e.g. "pods/cpu").
//   - namespace: Kubernetes namespace (required for namespaced resources).
//   - name: Resource name used in the PromQL template.
//   - start / end / step: Optional; omitting runs an instant query at Now().
func (h *Handler) HandleSlugQuery(w http.ResponseWriter, r *http.Request) {
	pc := h.Discoverer.PrometheusClient()
	if pc == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"Prometheus is not available", "")
		return
	}

	// chi wildcard route: /monitoring/queries/* — slug is everything after the prefix.
	rawSlug := chi.URLParam(r, "*")
	if rawSlug == "" {
		httputil.WriteError(w, http.StatusNotFound, "slug is required", "")
		return
	}

	def, ok := Registry[rawSlug]
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "unknown query slug: "+rawSlug, "")
		return
	}

	q := r.URL.Query()
	namespace := q.Get("namespace")
	name := q.Get("name")

	// Validate supplied label values to prevent PromQL injection.
	if namespace != "" && !isValidK8sName(namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace value", "")
		return
	}
	if name != "" && !isValidK8sName(name) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid name value", "")
		return
	}

	// RBAC check: user must have the required verb on the resource in the
	// requested namespace. Cluster-scoped resources use namespace="".
	if def.RequiredGVR != "" && h.AccessChecker != nil {
		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			httputil.WriteError(w, http.StatusUnauthorized, "authentication required", "")
			return
		}

		// Split RequiredGVR into resource + apiGroup.
		// Format: "resource" (core group) or "resource.group" (named group).
		apiGroup := ""
		resource := def.RequiredGVR
		if idx := strings.Index(def.RequiredGVR, "."); idx != -1 {
			resource = def.RequiredGVR[:idx]
			apiGroup = def.RequiredGVR[idx+1:]
		}

		// For cluster-scoped resources, pass "" as namespace.
		rbacNS := namespace
		if clusterScopedGVRs[def.RequiredGVR] {
			rbacNS = ""
		}

		for _, verb := range def.RequiredVerbs {
			allowed, err := h.AccessChecker.CanAccessGroupResource(
				r.Context(),
				user.KubernetesUsername,
				user.KubernetesGroups,
				verb,
				apiGroup,
				resource,
				rbacNS,
			)
			if err != nil {
				h.Logger.Error("slug query RBAC check failed",
					"slug", rawSlug, "user", user.Username, "error", err)
				httputil.WriteError(w, http.StatusInternalServerError, "RBAC check failed", "")
				return
			}
			if !allowed {
				httputil.WriteError(w, http.StatusForbidden,
					"access denied: insufficient permissions for "+rawSlug, "")
				return
			}
		}
	}

	// Render the PromQL template.
	rendered, err := renderSlugTemplate(def.Template, namespace, name)
	if err != nil {
		h.Logger.Error("slug template render failed", "slug", rawSlug, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "template render failed", "")
		return
	}

	// Determine instant vs range query.
	startStr := q.Get("start")
	endStr := q.Get("end")
	stepStr := q.Get("step")

	if startStr == "" && endStr == "" {
		// Instant query.
		ts := time.Now()
		if t := q.Get("time"); t != "" {
			parsed, err := time.Parse(time.RFC3339, t)
			if err != nil {
				httputil.WriteError(w, http.StatusBadRequest, "invalid time parameter", err.Error())
				return
			}
			ts = parsed
		}
		result, warnings, err := pc.Query(r.Context(), rendered, ts)
		if err != nil {
			httputil.WriteError(w, http.StatusBadGateway, "Prometheus query failed", err.Error())
			return
		}
		httputil.WriteData(w, map[string]any{
			"slug":       rawSlug,
			"query":      rendered,
			"resultType": result.Type(),
			"result":     result,
			"warnings":   warnings,
		})
		return
	}

	// Range query — parse and apply caps.
	if stepStr == "" {
		stepStr = "60s"
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid start parameter", err.Error())
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid end parameter", err.Error())
		return
	}
	step, err := time.ParseDuration(stepStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid step parameter (use Go duration like 15s, 1m)", err.Error())
		return
	}

	if err := validateRangeCaps(start, end, step); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	result, warnings, err := pc.QueryRange(r.Context(), rendered, start, end, step)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Prometheus range query failed", err.Error())
		return
	}
	httputil.WriteData(w, map[string]any{
		"slug":       rawSlug,
		"query":      rendered,
		"resultType": result.Type(),
		"result":     result,
		"warnings":   warnings,
	})
}

// validateRangeCaps enforces the P2-4 time-range caps:
//   - max range = 6h
//   - min step  = 10s
//   - max samples ≤ 11000
func validateRangeCaps(start, end time.Time, step time.Duration) error {
	if end.Before(start) {
		return fmt.Errorf("end must be after start")
	}
	rangeDur := end.Sub(start)
	if rangeDur > maxQueryRangeDuration {
		return fmt.Errorf("time range %.1fh exceeds maximum 6h", rangeDur.Hours())
	}
	if step < minQueryStep {
		return fmt.Errorf("step %s is below minimum 10s", step)
	}
	samples := int64(rangeDur/step) + 1
	if samples > maxQuerySamples {
		return fmt.Errorf("query would return ~%d samples, exceeding maximum %d (reduce range or increase step)",
			samples, maxQuerySamples)
	}
	return nil
}

// slugTemplateVars carries the two supported substitution variables.
type slugTemplateVars struct {
	Namespace string
	Name      string
}

// renderSlugTemplate renders a QueryDef.Template using text/template.
// The template must only use {{.Namespace}} and {{.Name}}.
func renderSlugTemplate(tmplStr, namespace, name string) (string, error) {
	t, err := template.New("slug").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, slugTemplateVars{Namespace: namespace, Name: name}); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return buf.String(), nil
}

