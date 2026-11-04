package server

// The Loki binding admin API (remote logs and shells U13, KTD8). An admin
// sets a remote cluster's Loki URL, with an optional bearer token and
// tenant, and log search for that cluster then uses that URL instead of
// discovering the cluster's in-cluster Loki (R6, AE3). The routes mirror the
// metrics binding's: admin-only, the same URL rule, and pointer semantics for
// the token, which is pinned to its URL's origin and never leaves the store.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/loki"
	"github.com/kubecenter/kubecenter/internal/store"
)

// clusterLogsStore is the subset of *store.ClusterLogsStore the Loki
// binding handlers use, so tests can substitute a fake.
type clusterLogsStore interface {
	Get(ctx context.Context, clusterID string) (store.LokiBinding, string, error)
	Upsert(ctx context.Context, clusterID string, w store.LokiBindingWrite) (store.LokiBinding, error)
	Delete(ctx context.Context, clusterID string) error
}

// lokiResolverEvicter is the part of *loki.Resolver the binding handlers
// need: dropping a cluster's cached Loki discovery after its binding is
// written or deleted.
type lokiResolverEvicter interface {
	Evict(clusterID string)
}

// reasonLokiNotConfigured marks a remote cluster with no Loki binding. Log
// search still works for it through the automatic in-cluster path.
const reasonLokiNotConfigured = "loki_not_configured"

// Fixed client-facing messages. Raw errors are logged, never returned.
const (
	msgLokiNoDatabase     = "Loki bindings require a database"
	msgLokiNotConfigured  = "a Loki URL is not configured for the selected cluster"
	msgLokiLocalCluster   = "the local cluster uses in-cluster Loki discovery"
	msgLokiStoreFailed    = "the Loki binding could not be read or saved"
	msgLokiTokenReentry   = "the token must be re-entered when the Loki URL changes host"
	msgLokiBindingChanged = "the Loki binding was changed by another save; reload it and try again"
	msgLokiTenantInvalid  = "tenantId may hold at most 150 letters, digits and the characters !-_.*'() and | between tenants"
)

// maxLokiTenantBytes is Loki's own limit on a tenant ID.
const maxLokiTenantBytes = 150

// lokiBindingResponse is the wire shape of GET/PUT /clusters/{id}/logs. The
// token is never part of it: hasToken reports that a usable one is stored,
// and tokenStale that a stored one was entered for another origin and must
// be entered again before it is used.
type lokiBindingResponse struct {
	ClusterID  string    `json:"clusterId"`
	URL        string    `json:"url"`
	TenantID   string    `json:"tenantId"`
	HasToken   bool      `json:"hasToken"`
	TokenStale bool      `json:"tokenStale"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func toLokiBindingResponse(b store.LokiBinding) lokiBindingResponse {
	return lokiBindingResponse{
		ClusterID:  b.ClusterID,
		URL:        b.URL,
		TenantID:   b.TenantID,
		HasToken:   b.HasToken,
		TokenStale: b.TokenStale,
		UpdatedAt:  b.UpdatedAt,
	}
}

// lokiBindingRequest is the PUT body. Token is a pointer so an omitted (or
// null) token can be told apart from an explicit "": omitted keeps the
// stored token, "" clears it, anything else replaces it.
type lokiBindingRequest struct {
	URL      string  `json:"url"`
	Token    *string `json:"token"`
	TenantID string  `json:"tenantId"`
}

// logsPreamble runs the checks every Loki binding route shares.
func (s *Server) logsPreamble(w http.ResponseWriter, r *http.Request, id string) bool {
	return s.clusterBindingPreamble(w, r, id, bindingRoute{
		wired:        s.ClusterLogsStore != nil,
		noDatabase:   msgLokiNoDatabase,
		localCluster: msgLokiLocalCluster,
		logPrefix:    "loki binding",
	})
}

// evictLokiResolver drops the Loki resolver's cached discovery for id, so
// the next log search reads the binding as it now is. A nil resolver is a
// no-op.
func (s *Server) evictLokiResolver(id string) {
	if s.LokiResolver != nil {
		s.LokiResolver.Evict(id)
	}
}

// validLokiTenant reports whether t is a tenant ID Loki accepts: at most 150
// bytes of letters, digits and !-_.*'(), with | between the tenants of a
// multi-tenant query. It goes out as the X-Scope-OrgID header, so nothing
// else, a control character least of all, may reach it.
func validLokiTenant(t string) bool {
	if len(t) > maxLokiTenantBytes {
		return false
	}
	for _, c := range t {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!-_.*'()|", c):
		default:
			return false
		}
	}
	return true
}

// handleGetClusterLogs answers GET /clusters/{clusterID}/logs.
func (s *Server) handleGetClusterLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")
	if !s.logsPreamble(w, r, id) {
		return
	}
	b, _, err := s.ClusterLogsStore.Get(r.Context(), id)
	if errors.Is(err, store.ErrLokiBindingNotFound) {
		httputil.WriteErrorWithReason(w, http.StatusNotFound, msgLokiNotConfigured, reasonLokiNotConfigured, nil)
		return
	}
	if err != nil {
		s.Logger.Error("loki binding: read failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgLokiStoreFailed, "")
		return
	}
	httputil.WriteData(w, toLokiBindingResponse(b))
}

// handlePutClusterLogs answers PUT /clusters/{clusterID}/logs: it validates
// the binding, refuses an edit that would carry a stored token to another
// origin, then stores it.
func (s *Server) handlePutClusterLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB max
	var req lokiBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if !s.logsPreamble(w, r, id) {
		return
	}

	req.URL = strings.TrimSpace(req.URL)
	req.TenantID = strings.TrimSpace(req.TenantID)
	if req.URL == "" {
		httputil.WriteError(w, http.StatusBadRequest, "url is required", "")
		return
	}
	if msg := validateMetricsURL("url", req.URL); msg != "" {
		httputil.WriteError(w, http.StatusBadRequest, msg, "")
		return
	}
	if !validLokiTenant(req.TenantID) {
		httputil.WriteError(w, http.StatusBadRequest, msgLokiTenantInvalid, "")
		return
	}
	if req.Token != nil && len(*req.Token) > maxMetricsTokenBytes {
		httputil.WriteError(w, http.StatusBadRequest, "token too large (max 64KB)", "")
		return
	}

	// An omitted token keeps the stored one, which is only ever sent to the
	// origin it was entered for: moving the URL to another host requires the
	// token again, and so does a stored token already stale. keptFrom is the
	// stored URL this check was made against; the write keeps the token only
	// while the binding still has it, so a save that moved the binding in
	// between fails with 409 rather than pair this URL with that save's
	// token.
	keptFrom := ""
	if req.Token == nil {
		current, _, err := s.ClusterLogsStore.Get(r.Context(), id)
		if err != nil && !errors.Is(err, store.ErrLokiBindingNotFound) {
			s.Logger.Error("loki binding: reading the stored binding failed", "cluster", id, "error", err)
			httputil.WriteError(w, http.StatusServiceUnavailable, msgLokiStoreFailed, "")
			return
		}
		if current.TokenStale || (current.HasToken && !sameOrigin(current.URL, req.URL)) {
			httputil.WriteError(w, http.StatusBadRequest, msgLokiTokenReentry, "")
			return
		}
		keptFrom = current.URL
	}

	b, err := s.ClusterLogsStore.Upsert(r.Context(), id, store.LokiBindingWrite{
		URL:      req.URL,
		TenantID: req.TenantID,
		Token:    req.Token,
		KeptFrom: keptFrom,
	})
	if errors.Is(err, store.ErrLokiBindingChanged) {
		httputil.WriteError(w, http.StatusConflict, msgLokiBindingChanged, "")
		return
	}
	if err != nil {
		s.Logger.Error("loki binding: upsert failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgLokiStoreFailed, "")
		return
	}
	s.evictLokiResolver(id)

	s.auditClusterBinding(r, "cluster-logs", audit.ActionUpdate, id, "loki binding saved")
	httputil.WriteData(w, toLokiBindingResponse(b))
}

// handleDeleteClusterLogs answers DELETE /clusters/{clusterID}/logs. Log
// search for the cluster goes back to discovering its in-cluster Loki.
func (s *Server) handleDeleteClusterLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")
	if !s.logsPreamble(w, r, id) {
		return
	}
	err := s.ClusterLogsStore.Delete(r.Context(), id)
	if errors.Is(err, store.ErrLokiBindingNotFound) {
		httputil.WriteErrorWithReason(w, http.StatusNotFound, msgLokiNotConfigured, reasonLokiNotConfigured, nil)
		return
	}
	if err != nil {
		s.Logger.Error("loki binding: delete failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgLokiStoreFailed, "")
		return
	}
	s.evictLokiResolver(id)

	s.auditClusterBinding(r, "cluster-logs", audit.ActionDelete, id, "loki binding removed")
	w.WriteHeader(http.StatusNoContent)
}

// The PostgreSQL store and the Loki resolver are what production wires in.
var (
	_ clusterLogsStore    = (*store.ClusterLogsStore)(nil)
	_ lokiResolverEvicter = (*loki.Resolver)(nil)
)
