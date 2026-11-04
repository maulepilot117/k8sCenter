package loki

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// allowedSteps is the set of valid step durations for volume queries.
var allowedSteps = map[string]bool{
	"15s": true, "30s": true, "1m": true, "5m": true,
	"15m": true, "30m": true, "1h": true, "6h": true, "1d": true,
}

// Handler serves Loki log HTTP endpoints. Every read resolves the Loki of
// the request's cluster through Resolver; a remote cluster is never answered
// from the local Loki.
type Handler struct {
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger
	// Resolver answers which Loki serves a cluster. When it is nil, a
	// resolver over Discoverer alone is used: the local cluster is served
	// and every remote cluster is unsupported.
	Resolver *Resolver

	fallbackOnce sync.Once
	fallback     *Resolver
}

func (h *Handler) resolver() *Resolver {
	if h.Resolver != nil {
		return h.Resolver
	}
	h.fallbackOnce.Do(func() { h.fallback = NewResolver(h.Discoverer, nil, nil, h.Logger) })
	return h.fallback
}

// ResolveFor returns the Loki serving clusterID for user, for a caller that
// holds it, such as a live tail. The caller must Close the result. A
// failure is an *UnavailableError.
func (h *Handler) ResolveFor(ctx context.Context, clusterID string, user *auth.User) (*Resolved, error) {
	return h.resolver().Resolve(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
}

// resolve returns the Loki of the request's cluster, for its user. When
// there is none it writes the unavailable answer and returns nil. The
// caller must Close the result.
func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) *Resolved {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return nil
	}
	clusterID := middleware.ClusterIDFromContext(r.Context())
	res, err := h.resolver().Resolve(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.writeUnavailable(w, clusterID, err)
		return nil
	}
	return res
}

// writeUnavailable answers a cluster whose Loki cannot be used, with the
// reason's fixed status and message.
func (h *Handler) writeUnavailable(w http.ResponseWriter, clusterID string, err error) {
	ue := unavailableOf(err)
	h.Logger.Warn("loki unavailable", "cluster", clusterID, "reason", ue.Reason, "error", ue.Err)
	var extra map[string]any
	if ue.Retryable {
		extra = map[string]any{"retryable": true}
	}
	httputil.WriteErrorWithReason(w, ue.Status, ue.Message, ue.Reason, extra)
}

// queryFailed answers a Loki request that failed: as Loki being
// unavailable when it is, else as a failed query with the fixed message.
func (h *Handler) queryFailed(w http.ResponseWriter, r *http.Request, res *Resolved, err error, message string) {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if ue := res.Failure(err); ue != nil {
		h.writeUnavailable(w, clusterID, ue)
		return
	}
	h.Logger.Warn("loki request failed", "cluster", clusterID, "error", err)
	httputil.WriteError(w, http.StatusBadGateway, message, "")
}

// unavailableOf returns err's *UnavailableError. The resolver returns no
// other kind; anything else reads as Loki being unreachable.
func unavailableOf(err error) *UnavailableError {
	var ue *UnavailableError
	if errors.As(err, &ue) {
		return ue
	}
	return errLokiUnreachable(err)
}

// HandleStatus reports the Loki of the request's cluster. The local cluster
// answers its discovery result. A remote cluster answers where its Loki is,
// found without connecting to it: the configured URL, or the namespace and
// Service the port-forward path chose. When it has none, Detected is false
// with the reason and its message; clients treat "not detected" as Loki
// being unavailable.
// GET /api/v1/logs/status
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if k8s.IsLocalClusterID(clusterID) {
		if h.Discoverer == nil {
			httputil.WriteData(w, LokiStatus{LastChecked: time.Now().UTC().Format(time.RFC3339), Reason: ReasonLokiNotFound})
			return
		}
		httputil.WriteData(w, h.Discoverer.Status())
		return
	}
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	loc, err := h.resolver().Locate(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		ue := unavailableOf(err)
		h.Logger.Warn("remote loki status unavailable", "cluster", clusterID, "reason", ue.Reason, "error", ue.Err)
		httputil.WriteData(w, LokiStatus{LastChecked: now, Reason: ue.Reason, Message: ue.Message})
		return
	}
	httputil.WriteData(w, LokiStatus{
		Detected:    true,
		URL:         withoutUserinfo(loc.URL),
		DetectedVia: string(loc.Source),
		Namespace:   loc.Namespace,
		Service:     loc.Service,
		LastChecked: now,
	})
}

// withoutUserinfo drops any user:password from a URL, so a status answer
// never echoes credentials embedded in a configured URL.
func withoutUserinfo(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

// HandleQuery proxies a LogQL range query to Loki with namespace enforcement.
// GET /api/v1/logs/query?query=...&start=...&end=...&limit=...&direction=...&namespace=...
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
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

	res := h.resolve(w, r)
	if res == nil {
		return
	}
	defer res.Close()
	result, err := res.Client.QueryRange(r.Context(), enforced, start, end, limit, direction)
	if err != nil {
		h.queryFailed(w, r, res, err, "Log query failed")
		return
	}

	httputil.WriteData(w, result)
}

// HandleLabels returns available label names from Loki.
// GET /api/v1/logs/labels?start=...&end=...
//
// P3-5 (security audit 2026-05-22): admin-only at the route layer
// (middleware.RequireAdmin in registerLogRoutes). Loki's /labels endpoint
// surfaces global label names without reliable per-tenant scoping, so the
// previous best-effort `buildNamespaceScopeQuery` plumbing was discarded
// without effect. Non-admins receive 403 before this handler runs and can
// still enumerate label values inside their permitted namespace via
// /labels/{name}/values (which enforces a namespace-scoped LogQL selector).
func (h *Handler) HandleLabels(w http.ResponseWriter, r *http.Request) {
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

	res := h.resolve(w, r)
	if res == nil {
		return
	}
	defer res.Close()
	labels, err := res.Client.Labels(r.Context(), start, end)
	if err != nil {
		h.queryFailed(w, r, res, err, "Label query failed")
		return
	}

	httputil.WriteData(w, labels)
}

// HandleLabelValues returns values for a specific label.
// GET /api/v1/logs/labels/{name}/values?start=...&end=...&namespace=...
func (h *Handler) HandleLabelValues(w http.ResponseWriter, r *http.Request) {
	// P2-5 fix: use chi.URLParam instead of manual path parsing
	name := chi.URLParam(r, "name")
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

	// P1-2 fix: always scope label values for non-admin users, even without query param
	scopeQuery := q.Get("query")
	if scopeQuery != "" {
		enforced, err := h.enforceQueryNamespaces(r, scopeQuery)
		if err != nil {
			httputil.WriteError(w, http.StatusForbidden, "namespace access denied", err.Error())
			return
		}
		scopeQuery = enforced
	} else {
		// No query provided — build a namespace-scoped query for non-admin users
		built, err := h.buildNamespaceScopeQuery(r)
		if err != nil {
			httputil.WriteError(w, http.StatusForbidden, "namespace access denied", err.Error())
			return
		}
		scopeQuery = built
	}

	res := h.resolve(w, r)
	if res == nil {
		return
	}
	defer res.Close()
	values, err := res.Client.LabelValues(r.Context(), name, start, end, scopeQuery)
	if err != nil {
		h.queryFailed(w, r, res, err, "Label values query failed")
		return
	}

	httputil.WriteData(w, values)
}

// HandleVolume returns log volume data for the histogram.
// GET /api/v1/logs/volume?query=...&start=...&end=...&step=...&namespace=...
func (h *Handler) HandleVolume(w http.ResponseWriter, r *http.Request) {
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

	// P2-10 fix: validate step against allowlist
	step := "1m"
	if s := q.Get("step"); s != "" {
		if !allowedSteps[s] {
			httputil.WriteError(w, http.StatusBadRequest,
				"invalid step parameter",
				"allowed values: 15s, 30s, 1m, 5m, 15m, 30m, 1h, 6h, 1d")
			return
		}
		step = s
	}

	res := h.resolve(w, r)
	if res == nil {
		return
	}
	defer res.Close()
	result, err := res.Client.VolumeRange(r.Context(), enforced, start, end, step, []string{"namespace"})
	if err != nil {
		h.queryFailed(w, r, res, err, "Volume query failed")
		return
	}

	httputil.WriteData(w, result)
}

// enforceQueryNamespaces applies RBAC namespace restrictions to a LogQL query.
// Admin users pass through unmodified. Non-admin users get their query rewritten
// to only access namespaces they have RBAC permission for.
func (h *Handler) enforceQueryNamespaces(r *http.Request, query string) (string, error) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		return "", errors.New("unauthorized")
	}

	if auth.IsAdmin(user) {
		return EnforceNamespaces(query, nil)
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = r.Header.Get("X-Namespace")
	}
	if ns == "" {
		return "", errors.New("namespace parameter required for non-admin users")
	}

	// P1-3 fix: validate the requested namespace against Kubernetes RBAC
	if h.AccessChecker != nil {
		clusterID := middleware.ClusterIDFromContext(r.Context())
		allowed, err := h.AccessChecker.CanAccess(
			r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups,
			"list", "pods", ns,
		)
		if err != nil {
			return "", errors.New("permission check failed")
		}
		if !allowed {
			return "", errors.New("no permission to view logs in namespace " + ns)
		}
	}

	return EnforceNamespaces(query, []string{ns})
}

// buildNamespaceScopeQuery constructs a namespace-scoped LogQL selector for non-admin users.
// Returns empty string for admins (no scoping needed).
func (h *Handler) buildNamespaceScopeQuery(r *http.Request) (string, error) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		return "", errors.New("unauthorized")
	}

	if auth.IsAdmin(user) {
		return "", nil // no scoping needed
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = r.Header.Get("X-Namespace")
	}
	if ns == "" {
		return "", errors.New("namespace parameter required for non-admin users")
	}

	// Validate namespace against RBAC
	if h.AccessChecker != nil {
		clusterID := middleware.ClusterIDFromContext(r.Context())
		allowed, err := h.AccessChecker.CanAccess(
			r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups,
			"list", "pods", ns,
		)
		if err != nil {
			return "", errors.New("permission check failed")
		}
		if !allowed {
			return "", errors.New("no permission to view logs in namespace " + ns)
		}
	}

	return `{namespace="` + ns + `"}`, nil
}
