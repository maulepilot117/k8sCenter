package loki

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
)

// Handler serves Loki log HTTP endpoints.
type Handler struct {
	Discoverer *Discoverer
	Logger     *slog.Logger
}

// HandleStatus returns the current Loki discovery status.
// GET /api/v1/logs/status
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	httputil.WriteData(w, h.Discoverer.Status())
}

// HandleQuery proxies a LogQL range query to Loki with namespace enforcement.
// GET /api/v1/logs/query?query=...&start=...&end=...&limit=...&direction=...
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	client := h.Discoverer.Client()
	if client == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"Loki is not available",
			"Log aggregation has not been detected. Deploy Loki or configure KUBECENTER_LOKI_URL.")
		return
	}

	q := r.URL.Query()
	query := q.Get("query")
	if query == "" {
		httputil.WriteError(w, http.StatusBadRequest, "query parameter is required", "")
		return
	}

	// Enforce namespace scoping based on user permissions
	enforced, err := h.enforceQueryNamespaces(r, query)
	if err != nil {
		httputil.WriteError(w, http.StatusForbidden, "namespace access denied", err.Error())
		return
	}

	// Parse time range (default: last 1 hour)
	now := time.Now()
	start := now.Add(-1 * time.Hour)
	end := now
	if s := q.Get("start"); s != "" {
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid start parameter", err.Error())
			return
		}
		start = parsed
	}
	if e := q.Get("end"); e != "" {
		parsed, err := time.Parse(time.RFC3339, e)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid end parameter", err.Error())
			return
		}
		end = parsed
	}

	limit := 100
	if l := q.Get("limit"); l != "" {
		parsed, err := strconv.Atoi(l)
		if err != nil || parsed < 1 {
			httputil.WriteError(w, http.StatusBadRequest, "invalid limit parameter", "")
			return
		}
		if parsed > 5000 {
			parsed = 5000
		}
		limit = parsed
	}

	direction := "backward"
	if d := q.Get("direction"); d == "forward" {
		direction = "forward"
	}

	result, err := client.QueryRange(r.Context(), enforced, start, end, limit, direction)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Loki query failed", err.Error())
		return
	}

	httputil.WriteData(w, result)
}

// HandleLabels returns available label names from Loki.
// GET /api/v1/logs/labels?start=...&end=...
func (h *Handler) HandleLabels(w http.ResponseWriter, r *http.Request) {
	client := h.Discoverer.Client()
	if client == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "Loki is not available", "")
		return
	}

	q := r.URL.Query()
	var start, end time.Time
	if s := q.Get("start"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			start = parsed
		}
	}
	if e := q.Get("end"); e != "" {
		if parsed, err := time.Parse(time.RFC3339, e); err == nil {
			end = parsed
		}
	}

	labels, err := client.Labels(r.Context(), start, end)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Loki labels query failed", err.Error())
		return
	}

	httputil.WriteData(w, labels)
}

// HandleLabelValues returns values for a specific label.
// GET /api/v1/logs/labels/{name}/values?start=...&end=...&query=...
func (h *Handler) HandleLabelValues(w http.ResponseWriter, r *http.Request) {
	client := h.Discoverer.Client()
	if client == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "Loki is not available", "")
		return
	}

	// Extract label name from URL path
	name := ""
	parts := strings.Split(r.URL.Path, "/")
	for i, p := range parts {
		if p == "labels" && i+1 < len(parts) {
			name = parts[i+1]
			break
		}
	}
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "label name is required", "")
		return
	}

	q := r.URL.Query()
	var start, end time.Time
	if s := q.Get("start"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			start = parsed
		}
	}
	if e := q.Get("end"); e != "" {
		if parsed, err := time.Parse(time.RFC3339, e); err == nil {
			end = parsed
		}
	}

	// If a scoping query is provided, enforce namespaces on it
	scopeQuery := q.Get("query")
	if scopeQuery != "" {
		enforced, err := h.enforceQueryNamespaces(r, scopeQuery)
		if err != nil {
			httputil.WriteError(w, http.StatusForbidden, "namespace access denied", err.Error())
			return
		}
		scopeQuery = enforced
	}

	values, err := client.LabelValues(r.Context(), name, start, end, scopeQuery)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Loki label values query failed", err.Error())
		return
	}

	httputil.WriteData(w, values)
}

// HandleVolume returns log volume data for the histogram.
// GET /api/v1/logs/volume?query=...&start=...&end=...&step=...
func (h *Handler) HandleVolume(w http.ResponseWriter, r *http.Request) {
	client := h.Discoverer.Client()
	if client == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "Loki is not available", "")
		return
	}

	q := r.URL.Query()
	query := q.Get("query")
	if query == "" {
		httputil.WriteError(w, http.StatusBadRequest, "query parameter is required", "")
		return
	}

	enforced, err := h.enforceQueryNamespaces(r, query)
	if err != nil {
		httputil.WriteError(w, http.StatusForbidden, "namespace access denied", err.Error())
		return
	}

	now := time.Now()
	start := now.Add(-1 * time.Hour)
	end := now
	if s := q.Get("start"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			start = parsed
		}
	}
	if e := q.Get("end"); e != "" {
		if parsed, err := time.Parse(time.RFC3339, e); err == nil {
			end = parsed
		}
	}

	step := "1m"
	if s := q.Get("step"); s != "" {
		step = s
	}

	result, err := client.VolumeRange(r.Context(), enforced, start, end, step, []string{"namespace"})
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "Loki volume query failed", err.Error())
		return
	}

	httputil.WriteData(w, result)
}

// enforceQueryNamespaces applies RBAC namespace restrictions to a LogQL query.
// Admin users pass through unmodified. Non-admin users get their query rewritten
// to only access namespaces they have permission for.
func (h *Handler) enforceQueryNamespaces(r *http.Request, query string) (string, error) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		return "", errors.New("unauthorized")
	}

	// Admins have unrestricted access
	if auth.IsAdmin(user) {
		return EnforceNamespaces(query, nil)
	}

	// Non-admin: restrict to explicitly requested namespace (from query param or header).
	// The frontend always sends a namespace filter. If none provided, require one.
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		// Try to extract from the selected namespace header
		ns = r.Header.Get("X-Namespace")
	}
	if ns == "" {
		return "", errors.New("namespace parameter required for non-admin users")
	}

	return EnforceNamespaces(query, []string{ns})
}
