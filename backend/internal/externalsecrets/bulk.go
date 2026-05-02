package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
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
			// #355 item 3: matches Patch RBAC verb actually used by the worker.
			allowed = h.canAccess(ctx, user, "patch", "externalsecrets", es.Namespace)
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
		// #355 item 6: strings.Cut for the ns/name split.
		ns, name, ok := strings.Cut(scopeTarget, "/")
		if !ok || ns == "" || name == "" {
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
	if !rejectNonLocalClusterWrite(w, r) {
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

	// Mandatory scope-pin check. Callers MUST POST a body with the
	// `targetUIDs` array they obtained from the prior GET refresh-scope.
	// An empty body would let the server re-resolve scope freshly under
	// the operator without confirmation — defeating the pin contract that
	// guards against ES create/delete races between scope-resolve and
	// confirm. See todo #340.
	var body bulkRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest,
			"invalid body — expected {\"targetUIDs\":[...]}", "")
		return
	}
	if len(body.TargetUIDs) == 0 {
		httputil.WriteError(w, http.StatusBadRequest,
			"targetUIDs required — call GET refresh-scope first and pass the returned UIDs", "")
		return
	}
	added, removed := compareUIDs(body.TargetUIDs, scope.Targets)
	if len(added) > 0 || len(removed) > 0 {
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			"scope changed since last resolution", errBulkScopeChanged.Error(),
			map[string]any{"added": added, "removed": removed})
		return
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
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			"another bulk refresh is already in flight for this scope", "active_job_exists",
			map[string]any{"jobId": existing.ID.String()})
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
		// Race-loser: a concurrent POST inserted first. Surface the existing
		// job's id so the caller's recovery path is identical to the
		// FindActive 409 above. See todo #347.
		if errors.Is(err, store.ErrBulkJobActiveExists) {
			existing, _ := h.BulkJobStore.FindActive(r.Context(), clusterID, action, scopeTarget)
			extra := map[string]any{}
			if existing != nil {
				extra["jobId"] = existing.ID.String()
			}
			httputil.WriteErrorWithReason(w, http.StatusConflict,
				"another bulk refresh is already in flight for this scope", "active_job_exists", extra)
			return
		}
		h.Logger.Error("insert bulk job", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create job", "")
		return
	}

	if err := h.BulkWorker.Enqueue(BulkJobMessage{
		JobID:      job.ID,
		ClusterID:  clusterID,
		Action:     action,
		ScopeTarget:   scopeTarget,
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

	httputil.WriteData(w, toBulkRefreshJobResponse(job))
}

// BulkRefreshJobResponse is the wire shape returned by GET
// /externalsecrets/bulk-refresh-jobs/{jobId}. Typed (not map[string]any) so
// the Go-TS hash test catches future drift. See todo #351.
type BulkRefreshJobResponse struct {
	JobID       string                     `json:"jobId"`
	ClusterID   string                     `json:"clusterId"`
	RequestedBy string                     `json:"requestedBy"`
	Action      string                     `json:"action"`
	ScopeTarget string                     `json:"scopeTarget"`
	TargetCount int                        `json:"targetCount"`
	CreatedAt   time.Time                  `json:"createdAt"`
	CompletedAt *time.Time                 `json:"completedAt,omitempty"`
	Succeeded   []string                   `json:"succeeded"`
	Failed      []store.BulkRefreshOutcome `json:"failed"`
	Skipped     []store.BulkRefreshOutcome `json:"skipped"`
}

func toBulkRefreshJobResponse(j *store.ESOBulkRefreshJob) BulkRefreshJobResponse {
	return BulkRefreshJobResponse{
		JobID:       j.ID.String(),
		ClusterID:   j.ClusterID,
		RequestedBy: j.RequestedBy,
		Action:      string(j.Action),
		ScopeTarget: j.ScopeTarget,
		TargetCount: len(j.TargetUIDs),
		CreatedAt:   j.CreatedAt,
		CompletedAt: j.CompletedAt,
		Succeeded:   j.Succeeded,
		Failed:      j.Failed,
		Skipped:     j.Skipped,
	}
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
		"succeededCount":  len(job.Succeeded),
	}
	// Cluster-scoped actions inherently span tenants. Per-UID enumeration in
	// the Detail JSON would expose every namespace's ES UIDs to admins
	// reading audit (and to any future tenant-scoped audit export). Match
	// Phase D's notification SuppressResourceFields precedent: keep
	// aggregate counts + reason histograms; redact UIDs. See todo #348.
	if msg.Action == store.BulkRefreshActionClusterStore {
		detail["failed"] = anonymizeOutcomes(job.Failed)
		detail["skipped"] = anonymizeOutcomes(job.Skipped)
		detail["redacted"] = "cluster_scope_uid_redaction"
	} else {
		detail["failed"] = job.Failed
		detail["skipped"] = job.Skipped
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

// anonymizeOutcomes strips the UID field from each outcome and tallies the
// reason histogram. Used for cluster-scope audit Detail to avoid leaking
// per-namespace ES identity across tenant boundaries while preserving the
// aggregate diagnostic value operators need. See todo #348.
func anonymizeOutcomes(outs []store.BulkRefreshOutcome) map[string]any {
	if len(outs) == 0 {
		return map[string]any{"count": 0, "reasons": map[string]int{}}
	}
	hist := make(map[string]int, len(outs))
	for _, o := range outs {
		hist[o.Reason]++
	}
	return map[string]any{"count": len(outs), "reasons": hist}
}

// bulkAuditNamespace extracts a namespace for the audit row when the action
// is namespace-scoped or store-scoped (which has ns/name composite). Empty
// for cluster-store-scoped.
func bulkAuditNamespace(msg BulkJobMessage) string {
	switch msg.Action {
	case store.BulkRefreshActionNamespace:
		return msg.ScopeTarget
	case store.BulkRefreshActionStore:
		// #355 item 6: strings.Cut for the ns/name split.
		ns, _, _ := strings.Cut(msg.ScopeTarget, "/")
		return ns
	}
	return ""
}
