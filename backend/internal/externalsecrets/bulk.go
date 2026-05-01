package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// maxBulkTargets caps the per-job target count. Above this the dialog
// rejects the request with 413 and instructs the operator to use a
// per-namespace refresh instead.
const maxBulkTargets = 5000

// BulkScopeTarget is one entry in a resolved-scope target list.
type BulkScopeTarget struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// BulkNamespaceCount is one entry in the per-namespace breakdown.
type BulkNamespaceCount struct {
	Namespace string `json:"namespace"`
	Count     int    `json:"count"`
}

// BulkScopeResponse is the GET refresh-scope payload. The frontend dialog
// uses this to render "47 ESes across 6 namespaces" and the per-namespace
// breakdown.
//
// Restricted is true when the requesting user can refresh fewer ESes than
// the service-account view sees. It drives the UI's RBAC notice.
type BulkScopeResponse struct {
	Action          store.BulkRefreshAction `json:"action"`
	ScopeTarget     string                  `json:"scopeTarget"`
	TotalCount      int                     `json:"totalCount"`
	TotalNamespaces int                     `json:"totalNamespaces"`
	VisibleCount    int                     `json:"visibleCount"`
	Restricted      bool                    `json:"restricted"`
	Targets         []BulkScopeTarget       `json:"targets"`
	ByNamespace     []BulkNamespaceCount    `json:"byNamespace"`
}

// bulkRefreshRequest is the optional POST body. When TargetUIDs is non-empty
// the server checks the requested UIDs against the live RBAC-filtered scope
// and rejects with 409 scope_changed on mismatch — gives the dialog a chance
// to re-confirm without overwriting outside the user's intent.
type bulkRefreshRequest struct {
	TargetUIDs []string `json:"targetUIDs,omitempty"`
}

// errBulkScopeChanged is returned when client TargetUIDs disagree with the
// freshly-resolved scope.
var errBulkScopeChanged = errors.New("scope_changed")

// HandleResolveStoreScope handles
//
//	GET /externalsecrets/stores/{namespace}/{name}/refresh-scope
func (h *Handler) HandleResolveStoreScope(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
		return
	}
	ns, name := chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	resp, err := h.resolveScope(r.Context(), user, store.BulkRefreshActionStore, ns+"/"+name)
	if err != nil {
		h.Logger.Error("resolve store scope", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to resolve scope", "")
		return
	}
	httputil.WriteData(w, resp)
}

// HandleResolveClusterStoreScope handles
//
//	GET /externalsecrets/clusterstores/{name}/refresh-scope
func (h *Handler) HandleResolveClusterStoreScope(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
		return
	}
	name := chi.URLParam(r, "name")
	resp, err := h.resolveScope(r.Context(), user, store.BulkRefreshActionClusterStore, name)
	if err != nil {
		h.Logger.Error("resolve cluster store scope", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to resolve scope", "")
		return
	}
	httputil.WriteData(w, resp)
}

// HandleResolveNamespaceScope handles
//
//	GET /externalsecrets/refresh-namespace/{namespace}/refresh-scope
func (h *Handler) HandleResolveNamespaceScope(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
		return
	}
	ns := chi.URLParam(r, "namespace")
	resp, err := h.resolveScope(r.Context(), user, store.BulkRefreshActionNamespace, ns)
	if err != nil {
		h.Logger.Error("resolve namespace scope", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to resolve scope", "")
		return
	}
	httputil.WriteData(w, resp)
}

// resolveScope reads the cached ES inventory, filters to the requested scope,
// and applies RBAC. The full-cluster (SA-view) totals are reported alongside
// the user-visible totals so the dialog can surface "Showing only resources
// you can refresh; N additional ESes are out of your visibility."
func (h *Handler) resolveScope(
	ctx context.Context, user *auth.User, action store.BulkRefreshAction, scopeTarget string,
) (BulkScopeResponse, error) {
	resp := BulkScopeResponse{
		Action:      action,
		ScopeTarget: scopeTarget,
		Targets:     []BulkScopeTarget{},
		ByNamespace: []BulkNamespaceCount{},
	}

	data, err := h.getCached(ctx)
	if err != nil {
		return resp, err
	}

	// Service-account view first — used for the "total" aggregates.
	saMatched := matchScope(data.externalSecrets, action, scopeTarget)
	resp.TotalCount = len(saMatched)
	resp.TotalNamespaces = countNamespaces(saMatched)

	// RBAC filter against the user's `update externalsecret` perm — write
	// permission, not read. A user can SEE an ES they can't refresh; the
	// scope must drop those.
	visible := make([]ExternalSecret, 0, len(saMatched))
	nsAllow := map[string]bool{}
	for _, es := range saMatched {
		allowed, ok := nsAllow[es.Namespace]
		if !ok {
			allowed = h.canAccess(ctx, user, "update", "externalsecrets", es.Namespace)
			nsAllow[es.Namespace] = allowed
		}
		if allowed {
			visible = append(visible, es)
		}
	}

	resp.VisibleCount = len(visible)
	resp.Restricted = resp.VisibleCount < resp.TotalCount

	for _, es := range visible {
		resp.Targets = append(resp.Targets, BulkScopeTarget{
			Namespace: es.Namespace, Name: es.Name, UID: es.UID,
		})
	}

	nsCounts := map[string]int{}
	for _, es := range visible {
		nsCounts[es.Namespace]++
	}
	for ns, count := range nsCounts {
		resp.ByNamespace = append(resp.ByNamespace, BulkNamespaceCount{Namespace: ns, Count: count})
	}
	sort.Slice(resp.ByNamespace, func(i, j int) bool {
		return resp.ByNamespace[i].Namespace < resp.ByNamespace[j].Namespace
	})
	sort.Slice(resp.Targets, func(i, j int) bool {
		if resp.Targets[i].Namespace != resp.Targets[j].Namespace {
			return resp.Targets[i].Namespace < resp.Targets[j].Namespace
		}
		return resp.Targets[i].Name < resp.Targets[j].Name
	})

	return resp, nil
}

// matchScope filters the cached ES list by the requested action+scope.
//
//   - refresh_store: ESes in scopeTarget="<ns>/<name>" with
//     storeRef.kind=SecretStore.
//   - refresh_cluster_store: any ES with storeRef.kind=ClusterSecretStore
//     and storeRef.name=scopeTarget.
//   - refresh_namespace: every ES in scopeTarget=<namespace>.
func matchScope(ess []ExternalSecret, action store.BulkRefreshAction, scopeTarget string) []ExternalSecret {
	out := make([]ExternalSecret, 0, len(ess))
	switch action {
	case store.BulkRefreshActionStore:
		var ns, name string
		for i := 0; i < len(scopeTarget); i++ {
			if scopeTarget[i] == '/' {
				ns, name = scopeTarget[:i], scopeTarget[i+1:]
				break
			}
		}
		if ns == "" || name == "" {
			return out
		}
		for _, es := range ess {
			if es.Namespace != ns {
				continue
			}
			if es.StoreRef.Kind != "SecretStore" || es.StoreRef.Name != name {
				continue
			}
			out = append(out, es)
		}
	case store.BulkRefreshActionClusterStore:
		for _, es := range ess {
			if es.StoreRef.Kind != "ClusterSecretStore" || es.StoreRef.Name != scopeTarget {
				continue
			}
			out = append(out, es)
		}
	case store.BulkRefreshActionNamespace:
		for _, es := range ess {
			if es.Namespace == scopeTarget {
				out = append(out, es)
			}
		}
	}
	return out
}

func countNamespaces(ess []ExternalSecret) int {
	seen := map[string]struct{}{}
	for _, es := range ess {
		seen[es.Namespace] = struct{}{}
	}
	return len(seen)
}

// HandleBulkRefreshStore handles
//
//	POST /externalsecrets/stores/{namespace}/{name}/refresh-all
func (h *Handler) HandleBulkRefreshStore(w http.ResponseWriter, r *http.Request) {
	ns, name := chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	h.handleBulkRefresh(w, r, store.BulkRefreshActionStore, ns+"/"+name)
}

// HandleBulkRefreshClusterStore handles
//
//	POST /externalsecrets/clusterstores/{name}/refresh-all
func (h *Handler) HandleBulkRefreshClusterStore(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.handleBulkRefresh(w, r, store.BulkRefreshActionClusterStore, name)
}

// HandleBulkRefreshNamespace handles
//
//	POST /externalsecrets/refresh-namespace/{namespace}
func (h *Handler) HandleBulkRefreshNamespace(w http.ResponseWriter, r *http.Request) {
	ns := chi.URLParam(r, "namespace")
	h.handleBulkRefresh(w, r, store.BulkRefreshActionNamespace, ns)
}

// handleBulkRefresh is the shared body for the three POST endpoints. Resolves
// scope, validates client-supplied TargetUIDs against the freshly-resolved
// scope, persists the job, and enqueues it for the worker.
func (h *Handler) handleBulkRefresh(
	w http.ResponseWriter, r *http.Request, action store.BulkRefreshAction, scopeTarget string,
) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if h.BulkJobStore == nil || h.BulkWorker == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "bulk refresh unavailable", "")
		return
	}
	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
		return
	}

	scope, err := h.resolveScope(r.Context(), user, action, scopeTarget)
	if err != nil {
		h.Logger.Error("bulk refresh resolve scope", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to resolve scope", "")
		return
	}
	if scope.VisibleCount == 0 {
		httputil.WriteError(w, http.StatusUnprocessableEntity, "scope is empty", "no ExternalSecrets are in scope or you have no refresh permission")
		return
	}
	if scope.VisibleCount > maxBulkTargets {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("scope too large (%d > %d); use per-namespace refresh", scope.VisibleCount, maxBulkTargets), "")
		return
	}

	// Optional client TargetUIDs match check.
	if r.ContentLength > 0 {
		var body bulkRefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid body", "")
			return
		}
		if len(body.TargetUIDs) > 0 {
			added, removed := compareUIDs(body.TargetUIDs, scope.Targets)
			if len(added) > 0 || len(removed) > 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"code":    http.StatusConflict,
						"message": "scope changed since last resolution",
						"reason":  string(errBulkScopeChanged.Error()),
						"added":   added,
						"removed": removed,
					},
				})
				return
			}
		}
	}

	// At-most-one-active-per-scope check.
	clusterID := middleware.ClusterIDFromContext(r.Context())
	existing, err := h.BulkJobStore.FindActive(r.Context(), clusterID, action, scopeTarget)
	if err != nil {
		h.Logger.Error("find active bulk job", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to check active jobs", "")
		return
	}
	if existing != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    http.StatusConflict,
				"message": "another bulk refresh is already in flight for this scope",
				"reason":  "active_job_exists",
				"jobId":   existing.ID.String(),
			},
		})
		return
	}

	uids := make([]string, 0, len(scope.Targets))
	for _, t := range scope.Targets {
		uids = append(uids, t.UID)
	}

	job := store.ESOBulkRefreshJob{
		ID:          uuid.New(),
		ClusterID:   clusterID,
		RequestedBy: user.Username,
		Action:      action,
		ScopeTarget: scopeTarget,
		TargetUIDs:  uids,
	}
	if err := h.BulkJobStore.Insert(r.Context(), job); err != nil {
		h.Logger.Error("insert bulk job", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create job", "")
		return
	}

	if err := h.BulkWorker.Enqueue(BulkJobMessage{
		JobID:      job.ID,
		ClusterID:  clusterID,
		Action:     action,
		ScopeTgt:   scopeTarget,
		Targets:    scope.Targets,
		Username:   user.KubernetesUsername,
		Groups:     user.KubernetesGroups,
		ActorName:  user.Username,
		SourceIP:   r.RemoteAddr,
		EnqueuedAt: time.Now().UTC(),
	}); err != nil {
		h.Logger.Error("enqueue bulk job", "error", err)
		// The row is in the DB but the worker won't pick it up. Mark it
		// completed with a synthetic failure outcome so it doesn't appear
		// stuck in the UI forever.
		_ = h.BulkJobStore.Complete(r.Context(), job.ID)
		httputil.WriteError(w, http.StatusServiceUnavailable, "worker queue full", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"jobId":      job.ID.String(),
			"targetCount": len(scope.Targets),
		},
	})
}

// compareUIDs returns (added, removed) — UIDs in scope but not in client list,
// and UIDs in client list but not in scope.
func compareUIDs(client []string, scope []BulkScopeTarget) (added, removed []string) {
	clientSet := map[string]struct{}{}
	for _, u := range client {
		clientSet[u] = struct{}{}
	}
	scopeSet := map[string]struct{}{}
	for _, t := range scope {
		scopeSet[t.UID] = struct{}{}
		if _, ok := clientSet[t.UID]; !ok {
			added = append(added, t.UID)
		}
	}
	for _, u := range client {
		if _, ok := scopeSet[u]; !ok {
			removed = append(removed, u)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// HandleGetBulkRefreshJob handles
//
//	GET /externalsecrets/bulk-refresh-jobs/{jobId}
//
// Returns the live job state — succeeded / failed / skipped arrays update as
// the worker patches each ES, so the dialog can poll every 2s.
func (h *Handler) HandleGetBulkRefreshJob(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if h.BulkJobStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "bulk refresh unavailable", "")
		return
	}

	rawID := chi.URLParam(r, "jobId")
	id, err := uuid.Parse(rawID)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid jobId", "")
		return
	}
	job, err := h.BulkJobStore.Get(r.Context(), id)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "job not found", "")
		return
	}

	// Visibility: the requesting user must be the original requester, OR
	// admin. Avoid leaking job state across users.
	if job.RequestedBy != user.Username && !auth.IsAdmin(user) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	httputil.WriteData(w, bulkJobResponse(job))
}

// bulkJobResponse shapes the wire payload. Mirrors store.ESOBulkRefreshJob
// minus internal fields.
func bulkJobResponse(j *store.ESOBulkRefreshJob) map[string]any {
	out := map[string]any{
		"jobId":       j.ID.String(),
		"clusterId":   j.ClusterID,
		"requestedBy": j.RequestedBy,
		"action":      string(j.Action),
		"scopeTarget": j.ScopeTarget,
		"targetCount": len(j.TargetUIDs),
		"createdAt":   j.CreatedAt,
		"succeeded":   j.Succeeded,
		"failed":      j.Failed,
		"skipped":     j.Skipped,
	}
	if j.CompletedAt != nil {
		out["completedAt"] = j.CompletedAt
	}
	return out
}

// auditBulkJob writes the single-row audit entry for a completed bulk job.
// The Detail JSON carries the full outcome shape so the audit-log viewer can
// render counts inline.
func (h *Handler) auditBulkJob(ctx context.Context, msg BulkJobMessage, job *store.ESOBulkRefreshJob) {
	if h.AuditLogger == nil || job == nil {
		return
	}
	action := audit.ActionESOBulkRefresh
	if msg.Action == store.BulkRefreshActionNamespace {
		action = audit.ActionESOBulkRefreshNamespace
	}

	detail := map[string]any{
		"jobId":           job.ID.String(),
		"action":          string(job.Action),
		"scope":           job.ScopeTarget,
		"requestedBy":     job.RequestedBy,
		"requestedCount":  len(job.TargetUIDs),
		"succeeded_count": len(job.Succeeded),
		"failed":          job.Failed,
		"skipped":         job.Skipped,
	}
	detailJSON, _ := json.Marshal(detail)

	result := audit.ResultSuccess
	if len(job.Failed) > 0 {
		result = audit.ResultFailure
	}

	_ = h.AuditLogger.Log(ctx, audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         job.ClusterID,
		User:              msg.ActorName,
		SourceIP:          msg.SourceIP,
		Action:            action,
		ResourceKind:      "ExternalSecret",
		ResourceNamespace: bulkAuditNamespace(msg),
		ResourceName:      job.ScopeTarget,
		Result:            result,
		Detail:            string(detailJSON),
	})
}

// bulkAuditNamespace extracts a namespace for the audit row when the action
// is namespace-scoped or store-scoped (which has ns/name composite). Empty
// for cluster-store-scoped.
func bulkAuditNamespace(msg BulkJobMessage) string {
	switch msg.Action {
	case store.BulkRefreshActionNamespace:
		return msg.ScopeTgt
	case store.BulkRefreshActionStore:
		for i := 0; i < len(msg.ScopeTgt); i++ {
			if msg.ScopeTgt[i] == '/' {
				return msg.ScopeTgt[:i]
			}
		}
	}
	return ""
}
