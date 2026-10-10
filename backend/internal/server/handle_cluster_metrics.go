package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/store"
)

// clusterMetricsStore is the subset of *store.ClusterMetricsStore the
// metrics-binding handlers use. It is declared here so tests can substitute
// a fake for the PostgreSQL-backed store.
type clusterMetricsStore interface {
	Get(ctx context.Context, clusterID string) (store.MetricsBinding, string, error)
	Upsert(ctx context.Context, clusterID, prometheusURL, alertmanagerURL string, token *string) (store.MetricsBinding, error)
	Delete(ctx context.Context, clusterID string) error
}

// metricsResolverEvicter is the part of *monitoring.ClientResolver the
// binding handlers need: dropping a cluster's cached Prometheus client after
// its binding is written or deleted, or the cluster is deregistered.
type metricsResolverEvicter interface {
	Evict(clusterID string)
}

// Fixed client-facing messages. Raw errors (which can name addresses) are
// logged, never returned.
const (
	msgMetricsNoDatabase      = "metrics bindings require a database"
	msgMetricsNotConfigured   = "metrics are not configured for the selected cluster"
	msgMetricsLocalCluster    = "the local cluster uses in-cluster discovery"
	msgMetricsUnreachable     = "could not reach Prometheus at the given URL"
	msgMetricsStoreFailed     = "the metrics binding could not be read or saved"
	msgClusterRegistryFailure = "the cluster registry is unavailable"
)

// maxMetricsTokenBytes matches the cluster-registration token limit.
const maxMetricsTokenBytes = 65536

// metricsBindingResponse is the wire shape of GET/PUT /clusters/{id}/metrics.
// The token is never part of it; hasToken only reports that one is stored.
type metricsBindingResponse struct {
	ClusterID       string    `json:"clusterId"`
	PrometheusURL   string    `json:"prometheusUrl"`
	HasToken        bool      `json:"hasToken"`
	AlertmanagerURL string    `json:"alertmanagerUrl"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func toMetricsBindingResponse(b store.MetricsBinding) metricsBindingResponse {
	return metricsBindingResponse{
		ClusterID:       b.ClusterID,
		PrometheusURL:   b.PrometheusURL,
		HasToken:        b.HasToken,
		AlertmanagerURL: b.AlertmanagerURL,
		UpdatedAt:       b.UpdatedAt,
	}
}

// metricsClusterGetter returns the registry reader used to confirm the
// cluster exists, or nil when no database is wired. Tests set
// metricsClusters; production reads s.ClusterStore (normalising a nil
// *store.ClusterStore to a nil interface).
func (s *Server) metricsClusterGetter() clusterRecordGetter {
	if s.metricsClusters != nil {
		return s.metricsClusters
	}
	if s.ClusterStore == nil {
		return nil
	}
	return s.ClusterStore
}

// evictMetricsClient drops the shared resolver's cached client for id. A nil
// resolver (no monitoring wired) is a no-op.
func (s *Server) evictMetricsClient(id string) {
	if s.MetricsResolver != nil {
		s.MetricsResolver.Evict(id)
	}
}

// metricsPreamble runs the checks every binding route shares: a database is
// wired, the id is not the local cluster, and the cluster is registered. It
// writes the response and returns false when a check fails.
func (s *Server) metricsPreamble(w http.ResponseWriter, r *http.Request, id string) bool {
	clusters := s.metricsClusterGetter()
	if s.ClusterMetricsStore == nil || clusters == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgMetricsNoDatabase, string(k8s.ReasonDBUnavailable), nil)
		return false
	}
	if k8s.IsLocalClusterID(id) {
		httputil.WriteError(w, http.StatusBadRequest, msgMetricsLocalCluster, "")
		return false
	}
	rec, err := clusters.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 404 cluster_unknown, "the selected cluster is not registered".
			httputil.WriteTargetError(w, err)
			return false
		}
		s.Logger.Error("metrics binding: reading cluster registry failed", "cluster", id, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgClusterRegistryFailure, string(k8s.ReasonDBUnavailable), nil)
		return false
	}
	if rec != nil && rec.IsLocal {
		httputil.WriteError(w, http.StatusBadRequest, msgMetricsLocalCluster, "")
		return false
	}
	return true
}

// handleGetClusterMetrics answers GET /clusters/{clusterID}/metrics.
func (s *Server) handleGetClusterMetrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")
	if !s.metricsPreamble(w, r, id) {
		return
	}

	b, _, err := s.ClusterMetricsStore.Get(r.Context(), id)
	if errors.Is(err, store.ErrMetricsBindingNotFound) {
		httputil.WriteErrorWithReason(w, http.StatusNotFound, msgMetricsNotConfigured, string(k8s.ReasonMetricsNotConfigured), nil)
		return
	}
	if err != nil {
		s.Logger.Error("metrics binding: read failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgMetricsStoreFailed, "")
		return
	}
	httputil.WriteData(w, toMetricsBindingResponse(b))
}

// metricsBindingRequest is the PUT body. Token is a pointer so an omitted
// (or null) token can be told apart from an explicit "": omitted keeps the
// stored token, "" clears it, anything else replaces it.
type metricsBindingRequest struct {
	PrometheusURL   string  `json:"prometheusUrl"`
	Token           *string `json:"token"`
	AlertmanagerURL string  `json:"alertmanagerUrl"`
}

// validateMetricsURL applies the remote API server URL rule to a binding
// URL: https, a host, no embedded credentials, and an address that is not
// private, loopback, link-local or otherwise reserved. It returns the fixed
// client message, or "" when the URL is acceptable.
func validateMetricsURL(field, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return field + " must be a valid HTTPS URL"
	}
	if u.User != nil {
		return field + " must not contain credentials; send the token separately"
	}
	if err := k8s.ValidateRemoteURL(raw); err != nil {
		return field + " resolves to a private or reserved address"
	}
	return ""
}

// handlePutClusterMetrics answers PUT /clusters/{clusterID}/metrics: it
// validates the binding, probes the Prometheus it names, then stores it.
func (s *Server) handlePutClusterMetrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB max
	var req metricsBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if !s.metricsPreamble(w, r, id) {
		return
	}

	req.PrometheusURL = strings.TrimSpace(req.PrometheusURL)
	req.AlertmanagerURL = strings.TrimSpace(req.AlertmanagerURL)
	if req.PrometheusURL == "" {
		httputil.WriteError(w, http.StatusBadRequest, "prometheusUrl is required", "")
		return
	}
	if msg := validateMetricsURL("prometheusUrl", req.PrometheusURL); msg != "" {
		httputil.WriteError(w, http.StatusBadRequest, msg, "")
		return
	}
	if req.AlertmanagerURL != "" {
		if msg := validateMetricsURL("alertmanagerUrl", req.AlertmanagerURL); msg != "" {
			httputil.WriteError(w, http.StatusBadRequest, msg, "")
			return
		}
	}
	if req.Token != nil && len(*req.Token) > maxMetricsTokenBytes {
		httputil.WriteError(w, http.StatusBadRequest, "token too large (max 64KB)", "")
		return
	}

	// The probe uses the token the binding will hold after the write: the
	// stored one when the request omits it.
	probeToken := ""
	if req.Token != nil {
		probeToken = *req.Token
	} else {
		_, stored, err := s.ClusterMetricsStore.Get(r.Context(), id)
		switch {
		case errors.Is(err, store.ErrMetricsBindingNotFound):
		case err != nil:
			s.Logger.Error("metrics binding: reading stored token failed", "cluster", id, "error", err)
			httputil.WriteError(w, http.StatusServiceUnavailable, msgMetricsStoreFailed, "")
			return
		default:
			probeToken = stored
		}
	}

	probe := s.probePrometheus
	if probe == nil {
		probe = monitoring.ProbePrometheus
	}
	if err := probe(r.Context(), req.PrometheusURL, probeToken); err != nil {
		s.Logger.Warn("metrics binding: prometheus probe failed", "cluster", id, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusBadGateway, msgMetricsUnreachable, string(k8s.ReasonUnreachable), nil)
		return
	}

	b, err := s.ClusterMetricsStore.Upsert(r.Context(), id, req.PrometheusURL, req.AlertmanagerURL, req.Token)
	if err != nil {
		s.Logger.Error("metrics binding: upsert failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgMetricsStoreFailed, "")
		return
	}
	s.evictMetricsClient(id)

	s.auditClusterMetrics(r, audit.ActionUpdate, id, "metrics binding saved")
	httputil.WriteData(w, toMetricsBindingResponse(b))
}

// handleDeleteClusterMetrics answers DELETE /clusters/{clusterID}/metrics.
func (s *Server) handleDeleteClusterMetrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")
	if !s.metricsPreamble(w, r, id) {
		return
	}

	err := s.ClusterMetricsStore.Delete(r.Context(), id)
	if errors.Is(err, store.ErrMetricsBindingNotFound) {
		httputil.WriteErrorWithReason(w, http.StatusNotFound, msgMetricsNotConfigured, string(k8s.ReasonMetricsNotConfigured), nil)
		return
	}
	if err != nil {
		s.Logger.Error("metrics binding: delete failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgMetricsStoreFailed, "")
		return
	}
	s.evictMetricsClient(id)

	s.auditClusterMetrics(r, audit.ActionDelete, id, "metrics binding removed")
	w.WriteHeader(http.StatusNoContent)
}

// auditClusterMetrics records a binding write. The URL and token are left
// out of the entry: a URL can carry identifying detail and the token is a
// secret.
func (s *Server) auditClusterMetrics(r *http.Request, action audit.Action, clusterID, detail string) {
	if s.AuditLogger == nil {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	if user == nil {
		return
	}
	entry := s.newAuditEntry(r, user.Username, action, audit.ResultSuccess)
	entry.ResourceKind = "cluster-metrics"
	entry.ResourceName = clusterID
	entry.Detail = detail
	s.AuditLogger.Log(r.Context(), entry)
}
