package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/policy"
	"github.com/kubecenter/kubecenter/internal/store"
)

// complianceIdentityStore is the subset of *store.ComplianceIdentityStore the
// identity routes use. It is declared here so tests can substitute a fake.
type complianceIdentityStore interface {
	Get(ctx context.Context, clusterID string) (store.ComplianceIdentity, error)
	Upsert(ctx context.Context, clusterID, username string, groups []string, updatedBy string) (store.ComplianceIdentity, error)
	Delete(ctx context.Context, clusterID string) error
}

// complianceSnapshotter is the part of *policy.RemoteComplianceRecorder the
// PUT uses to take the first snapshot at once. It returns immediately.
type complianceSnapshotter interface {
	SnapshotNow(ctx context.Context, clusterID string)
}

const (
	reasonImpersonationDenied = "impersonation_denied"

	maxComplianceUsernameLen = 256
	maxComplianceGroups      = 32
	maxComplianceGroupLen    = 256
)

// Fixed client-facing messages. Raw errors are logged, never returned.
const (
	msgComplianceNoDatabase    = "compliance identities require a database"
	msgComplianceLocalCluster  = "the local cluster records compliance history without an identity"
	msgComplianceStoreFailed   = "the compliance identity could not be read or saved"
	msgComplianceUsername      = "username is required"
	msgComplianceUsernameLong  = "username is too long"
	msgComplianceBlankGroup    = "groups must not contain blank entries"
	msgComplianceTooManyGroups = "too many groups"
	msgComplianceGroupLong     = "a group name is too long"
	msgComplianceDeniedUser    = "the cluster credential is not allowed to impersonate this user"
	msgComplianceDeniedSA      = "the cluster credential is not allowed to impersonate this service account"
	msgComplianceDeniedGroup   = "the cluster credential is not allowed to impersonate one of these groups"
)

// complianceIdentityResponse is the wire shape of GET/PUT
// /clusters/{id}/compliance-identity.
type complianceIdentityResponse struct {
	Username  string    `json:"username"`
	Groups    []string  `json:"groups"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toComplianceIdentityResponse(id store.ComplianceIdentity) complianceIdentityResponse {
	groups := id.Groups
	if groups == nil {
		groups = []string{}
	}
	return complianceIdentityResponse{Username: id.Username, Groups: groups, Revision: id.Revision, UpdatedAt: id.UpdatedAt}
}

// compliancePreamble runs the checks every identity route shares: a database
// is wired, the id is not the local cluster, and the cluster is registered.
// It writes the response and returns false when a check fails.
func (s *Server) compliancePreamble(w http.ResponseWriter, r *http.Request, id string) bool {
	clusters := s.metricsClusterGetter()
	if s.ComplianceIdentityStore == nil || clusters == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgComplianceNoDatabase, string(k8s.ReasonDBUnavailable), nil)
		return false
	}
	if k8s.IsLocalClusterID(id) {
		httputil.WriteError(w, http.StatusBadRequest, msgComplianceLocalCluster, "")
		return false
	}
	rec, err := clusters.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 404 cluster_unknown, "the selected cluster is not registered".
			httputil.WriteTargetError(w, err)
			return false
		}
		s.Logger.Error("compliance identity: reading cluster registry failed", "cluster", id, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgClusterRegistryFailure, string(k8s.ReasonDBUnavailable), nil)
		return false
	}
	if rec != nil && rec.IsLocal {
		httputil.WriteError(w, http.StatusBadRequest, msgComplianceLocalCluster, "")
		return false
	}
	return true
}

// handleGetComplianceIdentity answers GET /clusters/{clusterID}/compliance-identity.
func (s *Server) handleGetComplianceIdentity(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")
	if !s.compliancePreamble(w, r, id) {
		return
	}
	ident, err := s.ComplianceIdentityStore.Get(r.Context(), id)
	if errors.Is(err, store.ErrComplianceIdentityNotFound) {
		httputil.WriteErrorWithReason(w, http.StatusNotFound, policy.MsgHistoryNotConfigured, policy.ReasonHistoryNotConfigured, nil)
		return
	}
	if err != nil {
		s.Logger.Error("compliance identity: read failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgComplianceStoreFailed, "")
		return
	}
	httputil.WriteData(w, toComplianceIdentityResponse(ident))
}

type complianceIdentityRequest struct {
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
}

// normalizeComplianceIdentity trims, validates, sorts and de-duplicates the
// request. Sorting matters: the store compares groups in order to decide
// whether the revision moves. It returns a fixed client message on refusal.
func normalizeComplianceIdentity(req complianceIdentityRequest) (string, []string, string) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return "", nil, msgComplianceUsername
	}
	if len(username) > maxComplianceUsernameLen {
		return "", nil, msgComplianceUsernameLong
	}
	if len(req.Groups) > maxComplianceGroups {
		return "", nil, msgComplianceTooManyGroups
	}
	seen := make(map[string]struct{}, len(req.Groups))
	groups := make([]string, 0, len(req.Groups))
	for _, g := range req.Groups {
		g = strings.TrimSpace(g)
		if g == "" {
			return "", nil, msgComplianceBlankGroup
		}
		if len(g) > maxComplianceGroupLen {
			return "", nil, msgComplianceGroupLong
		}
		if _, dup := seen[g]; dup {
			continue
		}
		seen[g] = struct{}{}
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return username, groups, ""
}

// impersonationDeniedMessage names the grant kind the credential lacks,
// without echoing the API server's text.
func impersonationDeniedMessage(username string, err error) string {
	var denied *k8s.ImpersonationDeniedError
	switch {
	case errors.As(err, &denied) && denied.Resource == "groups":
		return msgComplianceDeniedGroup
	case strings.HasPrefix(username, k8s.ServiceAccountUserPrefix):
		return msgComplianceDeniedSA
	default:
		return msgComplianceDeniedUser
	}
}

// handlePutComplianceIdentity answers PUT /clusters/{clusterID}/compliance-identity.
func (s *Server) handlePutComplianceIdentity(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB max
	var req complianceIdentityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}
	if !s.compliancePreamble(w, r, id) {
		return
	}
	username, groups, msg := normalizeComplianceIdentity(req)
	if msg != "" {
		httputil.WriteError(w, http.StatusBadRequest, msg, "")
		return
	}
	if s.impersonationChecker == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgComplianceNoDatabase, string(k8s.ReasonDBUnavailable), nil)
		return
	}
	if err := s.impersonationChecker.CanImpersonate(r.Context(), id, username, groups); err != nil {
		if errors.Is(err, k8s.ErrImpersonationDenied) {
			httputil.WriteErrorWithReason(w, http.StatusUnprocessableEntity, impersonationDeniedMessage(username, err), reasonImpersonationDenied, nil)
			return
		}
		httputil.WriteTargetError(w, err)
		return
	}

	updatedBy := ""
	if user, _ := auth.UserFromContext(r.Context()); user != nil {
		updatedBy = user.Username
	}
	ident, err := s.ComplianceIdentityStore.Upsert(r.Context(), id, username, groups, updatedBy)
	if err != nil {
		s.Logger.Error("compliance identity: upsert failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgComplianceStoreFailed, "")
		return
	}
	if s.complianceSnapshotter != nil {
		// Returns immediately; the recorder detaches the request's cancellation.
		s.complianceSnapshotter.SnapshotNow(r.Context(), id)
	}

	s.auditComplianceIdentity(r, audit.ActionUpdate, id,
		"compliance identity saved: user "+username+", "+strconv.Itoa(len(groups))+" groups")
	httputil.WriteData(w, toComplianceIdentityResponse(ident))
}

// handleDeleteComplianceIdentity answers DELETE /clusters/{clusterID}/compliance-identity.
func (s *Server) handleDeleteComplianceIdentity(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "clusterID")
	if !s.compliancePreamble(w, r, id) {
		return
	}
	err := s.ComplianceIdentityStore.Delete(r.Context(), id)
	if errors.Is(err, store.ErrComplianceIdentityNotFound) {
		httputil.WriteErrorWithReason(w, http.StatusNotFound, policy.MsgHistoryNotConfigured, policy.ReasonHistoryNotConfigured, nil)
		return
	}
	if err != nil {
		s.Logger.Error("compliance identity: delete failed", "cluster", id, "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgComplianceStoreFailed, "")
		return
	}
	s.auditComplianceIdentity(r, audit.ActionDelete, id, "compliance identity removed")
	w.WriteHeader(http.StatusNoContent)
}

// auditComplianceIdentity records an identity write. The detail names the
// username and group count only.
func (s *Server) auditComplianceIdentity(r *http.Request, action audit.Action, clusterID, detail string) {
	if s.AuditLogger == nil {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	if user == nil {
		return
	}
	entry := s.newAuditEntry(r, user.Username, action, audit.ResultSuccess)
	entry.ResourceKind = "cluster-compliance-identity"
	entry.ResourceName = clusterID
	entry.Detail = detail
	s.AuditLogger.Log(r.Context(), entry)
}
