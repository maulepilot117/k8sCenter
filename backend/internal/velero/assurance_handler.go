package velero

// assurance_handler.go — Release F U35: the backup-assurance HTTP surface.
//
//	GET    /velero/assurance/status          any user; counts RBAC-filtered, runtime admin-only
//	GET    /velero/assurance/exceptions      any user; namespace + SSAR filtered, totals post-filter
//	GET    /velero/assurance/policies        admin
//	POST   /velero/assurance/policies        admin, rate-limited, audited
//	PUT    /velero/assurance/policies/{id}   admin, rate-limited, audited
//	DELETE /velero/assurance/policies/{id}   admin, rate-limited, audited, ?confirm=true
//
// Observation only. No handler here creates, patches or deletes a Kubernetes
// object: the policy writes touch PostgreSQL alone, and the only cluster read
// is the handler's existing service-account cache (fetchAll), used to say
// whether a schedule a policy or exception names still exists.
// TestAssuranceRoutes_ExposeNoMutatingClusterOperation pins both halves.
//
// Local cluster only (plan O-4): policies and exceptions exist for the
// cluster this process collects, so a remote selection is answered 501
// rather than with an empty list that would read as "no exceptions".
//
// Visibility (Design Decisions §6). A non-admin sees an exception only when
// it is not cluster-scoped and the SelfSubjectAccessReview for
// `list schedules.velero.io` in its subject namespace passes. That filter is
// applied INSIDE the store query (RestrictNamespaces), so metadata.total is
// the post-filter count and never an oracle for rows the caller cannot see.
// The privileged detail keys (storageLocation, bslMessage, failureReason)
// are projected for admins only; every other key outside a fixed allow-list
// is dropped for everyone.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const (
	// assuranceReasonDBUnavailable answers every endpoint when no PostgreSQL
	// is configured: 503 with a reason, never a 404 that would look like an
	// unregistered route.
	assuranceReasonDBUnavailable = "database_unavailable"
	// assuranceReasonRemote answers a non-local cluster selection.
	assuranceReasonRemote = "remote_assurance_unsupported"

	// assuranceStaleAfter is how old the last completed collection may be
	// before the status reports "stale": three missed ticks.
	assuranceStaleAfter = 3 * assuranceInterval

	// assurancePolicyBodyLimit bounds a policy write body; a policy is a
	// handful of scalars.
	assurancePolicyBodyLimit = 16 << 10

	// assuranceDefaultGrace is the grace a new policy gets when none is
	// given: one hour absorbs the largest DST discontinuity (§5).
	assuranceDefaultGrace = time.Hour

	// assuranceAuditKind is the audit ResourceKind for policy writes.
	assuranceAuditKind = "BackupAssurancePolicy"
)

// Collection states reported by the status endpoint: the R3 six-state
// vocabulary, of which this endpoint uses five (forbidden is a per-request
// state of the pages that read Velero directly, not of the collector).
const (
	AssuranceStateOK          = "ok"
	AssuranceStateStale       = "stale"
	AssuranceStateEmpty       = "empty"
	AssuranceStateUnknown     = "unknown"
	AssuranceStateUnavailable = "unavailable"
)

// Existence of the schedule a policy or exception names, as of the shared
// Velero read. unknown means the read failed or Velero is not detected: it is
// never reported as not_found.
const (
	assuranceScheduleFound    = "found"
	assuranceScheduleNotFound = "not_found"
	assuranceScheduleUnknown  = "unknown"

	assuranceScheduleNotFoundNote  = "schedule not found"
	assuranceExpectedRunUnknownTip = "not computable"
)

// RegisterAssuranceRoutes mounts the assurance endpoints on r, which the
// caller has already put behind authentication and CSRF. writeLimit is the
// rate limiter applied to the three policy writes. Kept here, beside the
// handlers, so the release-boundary test walks the real registration.
func (h *Handler) RegisterAssuranceRoutes(r chi.Router, writeLimit func(http.Handler) http.Handler) {
	r.Get("/status", h.HandleAssuranceStatus)
	r.Get("/exceptions", h.HandleListAssuranceExceptions)
	r.With(middleware.RequireAdmin).Get("/policies", h.HandleListAssurancePolicies)
	r.With(middleware.RequireAdmin, writeLimit).Post("/policies", h.HandleCreateAssurancePolicy)
	r.With(middleware.RequireAdmin, writeLimit).Put("/policies/{id}", h.HandleUpdateAssurancePolicy)
	// Method rather than chi's Delete helper: TestAssurance_NeverConstructsARestore
	// rejects any `Delete` identifier in an assurance file, so a Kubernetes
	// delete can never slip in under cover of a route registration.
	r.With(middleware.RequireAdmin, writeLimit).Method(http.MethodDelete, "/policies/{id}", http.HandlerFunc(h.HandleDeleteAssurancePolicy))
}

// ---------------------------------------------------------------------------
// Wire shapes
// ---------------------------------------------------------------------------

// AssurancePolicyView is one policy on the wire.
type AssurancePolicyView struct {
	ID             string     `json:"id"`
	ScopeKind      string     `json:"scopeKind"`
	ScopeNamespace string     `json:"scopeNamespace"`
	ScopeName      string     `json:"scopeName"`
	MaxAgeSeconds  int64      `json:"maxAgeSeconds"`
	GraceSeconds   int64      `json:"graceSeconds"`
	TreatPartialAs string     `json:"treatPartialAs"`
	AlertOnPaused  bool       `json:"alertOnPaused"`
	Enabled        bool       `json:"enabled"`
	CreatedBy      string     `json:"createdBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedBy      string     `json:"updatedBy"`
	UpdatedAt      *time.Time `json:"updatedAt"`
	Revision       int64      `json:"revision"`
	// ScheduleStatus is set for schedule-scope policies only: found,
	// not_found or unknown. A schedule that is gone produces no findings at
	// all, so without this a policy would silently watch nothing.
	ScheduleStatus *string `json:"scheduleStatus"`
	ScheduleNote   string  `json:"scheduleNote,omitempty"`
}

// AssuranceSubjectView is what an exception is about.
type AssuranceSubjectView struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// AssuranceDetailView is the projected evidence. It is decoded into this
// struct from the privileged JSONB, so a key not named here can never reach
// a caller; the three privileged fields are cleared for non-admins.
type AssuranceDetailView struct {
	LastOutcome      string     `json:"lastOutcome,omitempty"`
	LastSuccessAt    *time.Time `json:"lastSuccessAt,omitempty"`
	ExpectedRunAt    *time.Time `json:"expectedRunAt,omitempty"`
	ExpectedRunKnown bool       `json:"expectedRunKnown"`
	CronParseError   string     `json:"cronParseError,omitempty"`
	SuppressedBy     string     `json:"suppressedBy,omitempty"`
	ResolutionReason string     `json:"resolutionReason,omitempty"`
	// Admin only.
	StorageLocation string `json:"storageLocation,omitempty"`
	BSLMessage      string `json:"bslMessage,omitempty"`
	FailureReason   string `json:"failureReason,omitempty"`
}

// AssuranceExceptionView is one exception on the wire.
type AssuranceExceptionView struct {
	ID               string               `json:"id"`
	PolicyID         string               `json:"policyId"`
	Subject          AssuranceSubjectView `json:"subject"`
	Condition        string               `json:"condition"`
	State            string               `json:"state"`
	Severity         string               `json:"severity"`
	OpenedAt         time.Time            `json:"openedAt"`
	LastObservedAt   time.Time            `json:"lastObservedAt"`
	ResolvedAt       *time.Time           `json:"resolvedAt"`
	ObservationCount int64                `json:"observationCount"`
	LastSuccessAt    *time.Time           `json:"lastSuccessAt"`
	// SubjectStatus is set for schedule subjects only: whether a schedule
	// with this namespace, name AND uid exists now. A schedule recreated
	// under the same name has a new uid and is not_found for the old row.
	SubjectStatus *string `json:"subjectStatus"`
	SubjectNote   string  `json:"subjectNote,omitempty"`
	// ExpectedRunNote is "not computable" when detail.expectedRunKnown is
	// false, so no client renders a missing expected run as "none due".
	ExpectedRunNote string              `json:"expectedRunNote,omitempty"`
	Detail          AssuranceDetailView `json:"detail"`
}

// AssuranceOpenCounts is the per-condition count of open exceptions the
// caller may see. Every known condition is present, zero included, so a
// client never has to infer a zero from an absent key.
type AssuranceOpenCounts struct {
	Total       int            `json:"total"`
	ByCondition map[string]int `json:"byCondition"`
}

// AssuranceStatusView is the body of GET /velero/assurance/status.
type AssuranceStatusView struct {
	Enabled bool `json:"enabled"`
	// Collection is ok, stale, empty, unknown or unavailable.
	Collection string `json:"collection"`
	// LastCollection is the raw outcome of this replica's last collecting
	// run ("ok", "failed", or "" before the first).
	LastCollection string              `json:"lastCollection"`
	LastRunAt      *time.Time          `json:"lastRunAt"`
	PolicyCount    int                 `json:"policyCount"`
	Open           AssuranceOpenCounts `json:"open"`
	// Runtime is admin-only and omitted for everyone else.
	Runtime *AssuranceRuntimeView `json:"runtime,omitempty"`
}

// AssuranceRuntimeView is the collector's operational state, for admins.
type AssuranceRuntimeView struct {
	Holder          string               `json:"holder"`
	LastTickAt      *time.Time           `json:"lastTickAt"`
	FindingCount    int                  `json:"findingCount"`
	LastError       string               `json:"lastError,omitempty"`
	LastErrorAt     *time.Time           `json:"lastErrorAt"`
	LeaseHeld       bool                 `json:"leaseHeld"`
	Lease           *AssuranceLeaseView  `json:"lease"`
	DeliveryBacklog AssuranceBacklogView `json:"deliveryBacklog"`
}

// AssuranceLeaseView is the cluster-wide collector lease as the database
// holds it; null when no replica has ever held it.
type AssuranceLeaseView struct {
	Holder    string    `json:"holder"`
	Fence     int64     `json:"fence"`
	ExpiresAt time.Time `json:"expiresAt"`
	Expired   bool      `json:"expired"`
}

// AssuranceBacklogView counts undelivered notification intents.
type AssuranceBacklogView struct {
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

// assuranceConditions is every condition in a stable order.
var assuranceConditions = []store.AssuranceCondition{
	store.ConditionOverdue, store.ConditionFailed, store.ConditionPartiallyFailed,
	store.ConditionPaused, store.ConditionNeverRun, store.ConditionLocationUnavailable,
	store.ConditionCollectionUnknown,
}

// ---------------------------------------------------------------------------
// Shared gates
// ---------------------------------------------------------------------------

// assuranceTarget is what every handler needs once its gates have passed.
type assuranceTarget struct {
	user      *auth.User
	st        *store.BackupAssuranceStore
	clusterID string
}

// assuranceGate runs the gates every assurance endpoint shares, in order:
// 401 without a user, 501 for a remote cluster, 503 without a database or a
// wired collector. It writes the response and reports false when one fails.
func (h *Handler) assuranceGate(w http.ResponseWriter, r *http.Request) (assuranceTarget, bool) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return assuranceTarget{}, false
	}
	if h.AssuranceStore == nil || h.Assurance == nil {
		writeAssuranceUnavailable(w)
		return assuranceTarget{}, false
	}
	clusterID := h.Assurance.clusterID
	if reqCluster := middleware.ClusterIDFromContext(r.Context()); !k8s.IsLocalClusterID(reqCluster) && reqCluster != clusterID {
		httputil.WriteErrorWithReason(w, http.StatusNotImplemented,
			"backup assurance is collected for the local cluster only", assuranceReasonRemote, nil)
		return assuranceTarget{}, false
	}
	if clusterID == "" {
		// The collector refuses to start without a cluster id, and no row can
		// be written without one: report it as the unavailable capability.
		writeAssuranceUnavailable(w)
		return assuranceTarget{}, false
	}
	return assuranceTarget{user: user, st: h.AssuranceStore, clusterID: clusterID}, true
}

func writeAssuranceUnavailable(w http.ResponseWriter) {
	httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
		"backup assurance unavailable", assuranceReasonDBUnavailable,
		map[string]any{"capability": "backup-assurance"})
}

// assuranceFail logs err and answers 500 with msg, without the error text
// (CWE-209). A cancelled request is logged at debug only, but still gets the
// error status: returning without writing would let net/http send an empty
// 200, which reads as success.
func (h *Handler) assuranceFail(w http.ResponseWriter, r *http.Request, err error, msg string) {
	if r.Context().Err() != nil {
		h.Logger.Debug("backup assurance: request cancelled: "+msg, "error", err)
	} else {
		h.Logger.Error("backup assurance: "+msg, "error", err)
	}
	httputil.WriteError(w, http.StatusInternalServerError, msg, "")
}

// assuranceVisibleNamespaces returns the namespaces whose exceptions user may
// see, or admin=true for no restriction. Mirrors the shape of
// notifications.Handler.accessibleNamespaces, with the SAR as the authority:
// a namespace is visible when `list schedules.velero.io` there is allowed,
// which also implies the caller holds a role in it.
//
// The candidates are the policies' scope namespaces: every exception's
// subject namespace is its opening policy's scope namespace, and exceptions
// cascade with their policy. A row outside that set would simply not be
// returned to a non-admin; the restriction fails closed. A check that cannot
// be made fails the request rather than shortening the answer, so "you may
// not see it" and "we could not tell" are never conflated.
func (h *Handler) assuranceVisibleNamespaces(r *http.Request, t assuranceTarget) (namespaces []string, admin bool, err error) {
	if auth.IsAdmin(t.user) {
		return nil, true, nil
	}
	policies, err := t.st.ListPolicies(r.Context(), t.clusterID)
	if err != nil {
		return nil, false, err
	}
	candidates := make([]string, 0, len(policies))
	for _, p := range policies {
		if p.ScopeNamespace != "" {
			candidates = append(candidates, p.ScopeNamespace)
		}
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)

	visible := make([]string, 0, len(candidates))
	for _, ns := range candidates {
		can, err := h.canAccess(r, t.user, "list", "schedules", ns)
		if err != nil {
			return nil, false, fmt.Errorf("access check for namespace %q: %w", ns, err)
		}
		if can {
			visible = append(visible, ns)
		}
	}
	return visible, false, nil
}

// exceptionVisible applies the read filter to one row: cluster-scoped rows
// are admin-only, every other row needs its namespace in the visible set.
func exceptionVisible(e store.BackupAssuranceException, admin bool, visible []string) bool {
	if admin {
		return true
	}
	if e.SubjectKind == store.ScopeCluster || e.SubjectNamespace == "" {
		return false
	}
	return slices.Contains(visible, e.SubjectNamespace)
}

// assuranceSchedules returns the local schedules' uids keyed by
// "namespace/name", or ok=false when Velero is not detected or the shared
// read failed. Reads the handler's service-account cache; no write.
func (h *Handler) assuranceSchedules(ctx context.Context) (map[string]string, bool) {
	if h.Discoverer == nil || !h.Discoverer.Status(ctx).Detected {
		return nil, false
	}
	data, err := h.fetchAll(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.Logger.Warn("backup assurance: velero read failed; schedule existence is unknown", "error", err)
		}
		return nil, false
	}
	out := make(map[string]string, len(data.schedules))
	for _, s := range data.schedules {
		out[s.Namespace+"/"+s.Name] = s.UID
	}
	return out, true
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// HandleAssuranceStatus reports the collector's state and the caller's
// visible open-exception counts. Holder, lease, last error and delivery
// backlog are admin-only.
func (h *Handler) HandleAssuranceStatus(w http.ResponseWriter, r *http.Request) {
	t, ok := h.assuranceGate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	policies, err := t.st.ListPolicies(ctx, t.clusterID)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to read backup assurance status")
		return
	}
	visible, admin, err := h.assuranceVisibleNamespaces(r, t)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to check access")
		return
	}
	open, err := t.st.ListOpenExceptions(ctx, t.clusterID)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to read backup assurance status")
		return
	}

	snap := h.Assurance.Snapshot()
	view := AssuranceStatusView{
		Enabled:        snap.Enabled,
		Collection:     assuranceCollectionState(snap, len(policies), time.Now()),
		LastCollection: string(snap.LastCollection),
		LastRunAt:      timePtr(snap.LastRunAt),
		PolicyCount:    len(policies),
		Open:           countOpen(open, admin, visible),
	}

	if admin {
		rt := &AssuranceRuntimeView{
			Holder:       snap.Holder,
			LastTickAt:   timePtr(snap.LastTickAt),
			FindingCount: snap.FindingCount,
			LastError:    snap.LastError,
			LastErrorAt:  timePtr(snap.LastErrorAt),
			LeaseHeld:    snap.LeaseHeld,
		}
		lease, err := t.st.GetLease(ctx, t.clusterID)
		switch {
		case err == nil:
			rt.Lease = &AssuranceLeaseView{Holder: lease.Holder, Fence: lease.Fence, ExpiresAt: lease.ExpiresAt, Expired: lease.Expired}
		case errors.Is(err, store.ErrLeaseNotFound):
		default:
			h.assuranceFail(w, r, err, "failed to read backup assurance status")
			return
		}
		pending, failed, err := t.st.CountPendingDeliveries(ctx, t.clusterID)
		if err != nil {
			h.assuranceFail(w, r, err, "failed to read backup assurance status")
			return
		}
		rt.DeliveryBacklog = AssuranceBacklogView{Pending: pending, Failed: failed}
		view.Runtime = rt
	}

	httputil.WriteData(w, view)
}

// assuranceCollectionState maps the runtime snapshot onto the status
// vocabulary. The hard rule (§5) at the API layer: anything other than a
// recent ok collection is never reported as ok, and a collection that failed
// or never happened is unknown, never "no backups".
func assuranceCollectionState(snap AssuranceRuntimeStatus, policyCount int, now time.Time) string {
	switch {
	case !snap.Enabled:
		return AssuranceStateUnavailable
	case policyCount == 0:
		// No policy means no evaluation: install creates none (O-2).
		return AssuranceStateEmpty
	case snap.LastCollection != CollectionOK:
		// Failed, or not yet collected by this replica.
		return AssuranceStateUnknown
	case snap.LastRunAt.IsZero() || now.Sub(snap.LastRunAt) > assuranceStaleAfter:
		return AssuranceStateStale
	}
	return AssuranceStateOK
}

// countOpen counts the open exceptions the caller may see, per condition.
func countOpen(open []store.BackupAssuranceException, admin bool, visible []string) AssuranceOpenCounts {
	out := AssuranceOpenCounts{ByCondition: make(map[string]int, len(assuranceConditions))}
	for _, c := range assuranceConditions {
		out.ByCondition[string(c)] = 0
	}
	for _, e := range open {
		if !exceptionVisible(e, admin, visible) {
			continue
		}
		out.ByCondition[string(e.Condition)]++
		out.Total++
	}
	return out
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ---------------------------------------------------------------------------
// Exceptions
// ---------------------------------------------------------------------------

// HandleListAssuranceExceptions returns one page of the exceptions the
// caller may see, newest first.
//
//	GET /velero/assurance/exceptions?state=open|resolved&limit=50&offset=0
func (h *Handler) HandleListAssuranceExceptions(w http.ResponseWriter, r *http.Request) {
	t, ok := h.assuranceGate(w, r)
	if !ok {
		return
	}
	q, msg := parseExceptionQuery(r)
	if msg != "" {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, msg, "invalid_query", nil)
		return
	}

	visible, admin, err := h.assuranceVisibleNamespaces(r, t)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to check access")
		return
	}
	if !admin {
		// The whole RBAC filter runs in the query, so the store's total is
		// the post-filter count. An empty set matches nothing.
		q.RestrictNamespaces = true
		q.Namespaces = visible
	}

	rows, total, err := t.st.ListExceptions(r.Context(), t.clusterID, q)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to list backup assurance exceptions")
		return
	}

	// Belt and braces: re-apply the row filter in process. It cannot remove
	// anything the query admitted; if it ever did, the total is recounted
	// from what is returned rather than reporting a count for unseen rows.
	views := make([]AssuranceExceptionView, 0, len(rows))
	var schedules map[string]string
	var schedulesKnown, schedulesRead, dropped bool
	for _, e := range rows {
		if !exceptionVisible(e, admin, visible) {
			dropped = true
			continue
		}
		v := h.projectException(e, admin)
		if e.SubjectKind == store.ScopeSchedule {
			if !schedulesRead {
				schedules, schedulesKnown = h.assuranceSchedules(r.Context())
				schedulesRead = true
			}
			status := assuranceScheduleUnknown
			if schedulesKnown {
				status = assuranceScheduleNotFound
				if uid, ok := schedules[e.SubjectNamespace+"/"+e.SubjectName]; ok && (e.SubjectUID == "" || uid == e.SubjectUID) {
					status = assuranceScheduleFound
				}
			}
			v.SubjectStatus = &status
			if status == assuranceScheduleNotFound {
				v.SubjectNote = assuranceScheduleNotFoundNote
			}
		}
		views = append(views, v)
	}
	if dropped {
		h.Logger.Error("backup assurance: store returned a row outside the caller's namespace restriction")
		total = len(views)
	}

	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: views, Metadata: &api.Metadata{Total: total}})
}

// parseExceptionQuery reads state, limit and offset. A malformed value is a
// 400, never silently a default: a client asking for state=opne must not
// receive every row.
func parseExceptionQuery(r *http.Request) (store.AssuranceExceptionQuery, string) {
	var q store.AssuranceExceptionQuery
	v := r.URL.Query()
	switch s := v.Get("state"); s {
	case "", store.AssuranceStateOpen, store.AssuranceStateResolved:
		q.State = s
	default:
		return q, `state must be "open" or "resolved"`
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > store.AssuranceExceptionMaxLimit {
			return q, fmt.Sprintf("limit must be an integer from 1 to %d", store.AssuranceExceptionMaxLimit)
		}
		q.Limit = n
	}
	if s := v.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return q, "offset must be a non-negative integer"
		}
		q.Offset = n
	}
	return q, ""
}

// projectException builds the wire view, projecting detail through the
// allow-list.
func (h *Handler) projectException(e store.BackupAssuranceException, admin bool) AssuranceExceptionView {
	v := AssuranceExceptionView{
		ID:       e.ID.String(),
		PolicyID: e.PolicyID.String(),
		Subject: AssuranceSubjectView{
			Kind: string(e.SubjectKind), Namespace: e.SubjectNamespace, Name: e.SubjectName, UID: e.SubjectUID,
		},
		Condition:        string(e.Condition),
		State:            e.State,
		Severity:         e.Severity,
		OpenedAt:         e.OpenedAt,
		LastObservedAt:   e.LastObservedAt,
		ResolvedAt:       e.ResolvedAt,
		ObservationCount: e.ObservationCount,
		LastSuccessAt:    e.LastSuccessAt,
		Detail:           projectDetail(e.Detail, admin),
	}
	if !v.Detail.ExpectedRunKnown {
		v.ExpectedRunNote = assuranceExpectedRunUnknownTip
	}
	return v
}

// projectDetail decodes the privileged JSONB into the allow-listed view.
// Undecodable detail projects to an empty view (expected run not known): the
// row is still shown, without evidence, rather than failing the whole page.
func projectDetail(raw []byte, admin bool) AssuranceDetailView {
	var d AssuranceDetailView
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &d); err != nil {
			return AssuranceDetailView{}
		}
	}
	if !admin {
		d.StorageLocation, d.BSLMessage, d.FailureReason = "", "", ""
	}
	return d
}

// ---------------------------------------------------------------------------
// Policies (admin)
// ---------------------------------------------------------------------------

// HandleListAssurancePolicies returns every policy for the local cluster,
// with whether each schedule-scope policy's schedule still exists.
func (h *Handler) HandleListAssurancePolicies(w http.ResponseWriter, r *http.Request) {
	t, ok := h.assuranceGate(w, r)
	if !ok {
		return
	}
	policies, err := t.st.ListPolicies(r.Context(), t.clusterID)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to list backup assurance policies")
		return
	}

	var schedules map[string]string
	var known bool
	if slices.ContainsFunc(policies, func(p store.BackupAssurancePolicy) bool { return p.ScopeKind == store.ScopeSchedule }) {
		schedules, known = h.assuranceSchedules(r.Context())
	}
	views := make([]AssurancePolicyView, 0, len(policies))
	for _, p := range policies {
		views = append(views, policyView(p, schedules, known))
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: views, Metadata: &api.Metadata{Total: len(views)}})
}

// policyView builds the wire view. schedules/known come from
// assuranceSchedules; pass nil,false when existence was not read.
func policyView(p store.BackupAssurancePolicy, schedules map[string]string, known bool) AssurancePolicyView {
	v := AssurancePolicyView{
		ID:             p.ID.String(),
		ScopeKind:      string(p.ScopeKind),
		ScopeNamespace: p.ScopeNamespace,
		ScopeName:      p.ScopeName,
		MaxAgeSeconds:  int64(p.MaxAge / time.Second),
		GraceSeconds:   int64(p.Grace / time.Second),
		TreatPartialAs: p.TreatPartialAs,
		AlertOnPaused:  p.AlertOnPaused,
		Enabled:        p.Enabled,
		CreatedBy:      p.CreatedBy,
		CreatedAt:      p.CreatedAt,
		UpdatedBy:      p.UpdatedBy,
		UpdatedAt:      p.UpdatedAt,
		Revision:       p.Revision,
	}
	if p.ScopeKind == store.ScopeSchedule {
		status := assuranceScheduleUnknown
		if known {
			status = assuranceScheduleNotFound
			if _, ok := schedules[p.ScopeNamespace+"/"+p.ScopeName]; ok {
				status = assuranceScheduleFound
			}
		}
		v.ScheduleStatus = &status
		if status == assuranceScheduleNotFound {
			v.ScheduleNote = assuranceScheduleNotFoundNote
		}
	}
	return v
}

// assuranceFieldError names one invalid field of a policy write.
type assuranceFieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func writeAssuranceFieldErrors(w http.ResponseWriter, errs []assuranceFieldError) {
	httputil.WriteErrorWithReason(w, http.StatusBadRequest,
		"invalid policy: "+errs[0].Field+" "+errs[0].Message, "invalid_policy",
		map[string]any{"fieldErrors": errs})
}

// assurancePolicyCreateRequest is the POST body. Pointers distinguish an
// omitted field (defaulted) from a zero one (validated).
type assurancePolicyCreateRequest struct {
	ScopeKind      string  `json:"scopeKind"`
	ScopeNamespace string  `json:"scopeNamespace"`
	ScopeName      string  `json:"scopeName"`
	MaxAgeSeconds  *int64  `json:"maxAgeSeconds"`
	GraceSeconds   *int64  `json:"graceSeconds"`
	TreatPartialAs *string `json:"treatPartialAs"`
	AlertOnPaused  *bool   `json:"alertOnPaused"`
	Enabled        *bool   `json:"enabled"`
}

// assurancePolicyUpdateRequest is the PUT body. Scope is immutable: the
// scope fields are accepted only when they repeat the stored scope, so a
// client echoing the policy back works and a client trying to move it gets
// a clear refusal instead of a silent no-op. Omitted thresholds keep their
// stored value.
type assurancePolicyUpdateRequest struct {
	Revision       *int64  `json:"revision"`
	ScopeKind      *string `json:"scopeKind"`
	ScopeNamespace *string `json:"scopeNamespace"`
	ScopeName      *string `json:"scopeName"`
	MaxAgeSeconds  *int64  `json:"maxAgeSeconds"`
	GraceSeconds   *int64  `json:"graceSeconds"`
	TreatPartialAs *string `json:"treatPartialAs"`
	AlertOnPaused  *bool   `json:"alertOnPaused"`
	Enabled        *bool   `json:"enabled"`
}

// decodeAssuranceBody decodes a bounded JSON body, rejecting unknown fields
// and trailing data.
func decodeAssuranceBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, assurancePolicyBodyLimit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "invalid request body", "invalid_body", nil)
		return false
	}
	if dec.More() {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "invalid request body", "invalid_body", nil)
		return false
	}
	return true
}

// validateScope checks a create request's scope.
func validateScope(kind, ns, name string) []assuranceFieldError {
	var errs []assuranceFieldError
	checkNS := func(required bool) {
		switch {
		case ns == "" && required:
			errs = append(errs, assuranceFieldError{"scopeNamespace", "is required"})
		case ns != "" && !validateDNSLabel(ns):
			errs = append(errs, assuranceFieldError{"scopeNamespace", "must be a valid namespace name"})
		}
	}
	switch store.AssuranceScopeKind(kind) {
	case store.ScopeSchedule:
		checkNS(true)
		switch {
		case name == "":
			errs = append(errs, assuranceFieldError{"scopeName", "is required for schedule scope"})
		case !resources.ValidateK8sName(name):
			errs = append(errs, assuranceFieldError{"scopeName", "must be a valid schedule name"})
		}
	case store.ScopeNamespace:
		checkNS(true)
		if name != "" {
			errs = append(errs, assuranceFieldError{"scopeName", "must be empty for namespace scope"})
		}
	case store.ScopeCluster:
		if ns != "" {
			errs = append(errs, assuranceFieldError{"scopeNamespace", "must be empty for cluster scope"})
		}
		if name != "" {
			errs = append(errs, assuranceFieldError{"scopeName", "must be empty for cluster scope"})
		}
	default:
		errs = append(errs, assuranceFieldError{"scopeKind", `must be "schedule", "namespace" or "cluster"`})
	}
	return errs
}

// validateThresholds checks the mutable fields of a policy. A nil maxAge is
// reported as missing. The floors mirror the migration's CHECKs so the
// caller learns which field is wrong rather than receiving a constraint name.
func validateThresholds(maxAge *int64, grace int64, treatPartialAs string) []assuranceFieldError {
	var errs []assuranceFieldError
	minAge := int64(store.AssuranceMinMaxAge / time.Second)
	switch {
	case maxAge == nil:
		errs = append(errs, assuranceFieldError{"maxAgeSeconds", "is required"})
	case *maxAge < minAge:
		errs = append(errs, assuranceFieldError{"maxAgeSeconds", fmt.Sprintf("must be at least %d", minAge)})
	case *maxAge > math.MaxInt32:
		errs = append(errs, assuranceFieldError{"maxAgeSeconds", fmt.Sprintf("must be at most %d", math.MaxInt32)})
	}
	switch {
	case grace < 0:
		errs = append(errs, assuranceFieldError{"graceSeconds", "must not be negative"})
	case grace > math.MaxInt32:
		errs = append(errs, assuranceFieldError{"graceSeconds", fmt.Sprintf("must be at most %d", math.MaxInt32)})
	}
	switch treatPartialAs {
	case store.AssuranceTreatPartialAsSuccess, store.AssuranceTreatPartialAsFailure:
	default:
		errs = append(errs, assuranceFieldError{"treatPartialAs", `must be "success" or "failure"`})
	}
	return errs
}

// HandleCreateAssurancePolicy creates a policy. Defaults: grace 3600 s,
// treatPartialAs "failure", alertOnPaused and enabled true.
func (h *Handler) HandleCreateAssurancePolicy(w http.ResponseWriter, r *http.Request) {
	t, ok := h.assuranceGate(w, r)
	if !ok {
		return
	}
	var req assurancePolicyCreateRequest
	if !decodeAssuranceBody(w, r, &req) {
		return
	}

	grace := int64(assuranceDefaultGrace / time.Second)
	if req.GraceSeconds != nil {
		grace = *req.GraceSeconds
	}
	treat := store.AssuranceTreatPartialAsFailure
	if req.TreatPartialAs != nil {
		treat = *req.TreatPartialAs
	}
	errs := append(validateScope(req.ScopeKind, req.ScopeNamespace, req.ScopeName),
		validateThresholds(req.MaxAgeSeconds, grace, treat)...)
	if len(errs) > 0 {
		writeAssuranceFieldErrors(w, errs)
		return
	}

	p := store.BackupAssurancePolicy{
		ID:             uuid.New(),
		ClusterID:      t.clusterID,
		ScopeKind:      store.AssuranceScopeKind(req.ScopeKind),
		ScopeNamespace: req.ScopeNamespace,
		ScopeName:      req.ScopeName,
		MaxAge:         time.Duration(*req.MaxAgeSeconds) * time.Second,
		Grace:          time.Duration(grace) * time.Second,
		TreatPartialAs: treat,
		AlertOnPaused:  boolOr(req.AlertOnPaused, true),
		Enabled:        boolOr(req.Enabled, true),
		CreatedBy:      t.user.Username,
	}
	if err := t.st.InsertPolicy(r.Context(), p); err != nil {
		h.auditAssurance(r, t.user, audit.ActionCreate, p, audit.ResultFailure, "")
		h.writePolicyStoreError(w, r, err, "failed to create backup assurance policy")
		return
	}
	h.auditAssurance(r, t.user, audit.ActionCreate, p, audit.ResultSuccess, "")

	created, err := t.st.GetPolicy(r.Context(), t.clusterID, p.ID)
	if err != nil {
		h.assuranceFail(w, r, err, "policy created but could not be read back")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: h.policyViewWithSchedule(r.Context(), *created)})
}

// HandleUpdateAssurancePolicy changes a policy's thresholds and switches.
// The body must carry the revision the client read; a stale one is 409.
func (h *Handler) HandleUpdateAssurancePolicy(w http.ResponseWriter, r *http.Request) {
	t, ok := h.assuranceGate(w, r)
	if !ok {
		return
	}
	id, ok := assurancePolicyID(w, r)
	if !ok {
		return
	}
	var req assurancePolicyUpdateRequest
	if !decodeAssuranceBody(w, r, &req) {
		return
	}
	if req.Revision == nil {
		writeAssuranceFieldErrors(w, []assuranceFieldError{{"revision", "is required"}})
		return
	}

	current, err := t.st.GetPolicy(r.Context(), t.clusterID, id)
	if err != nil {
		h.writePolicyStoreError(w, r, err, "failed to read backup assurance policy")
		return
	}
	if scopeChanged(req.ScopeKind, string(current.ScopeKind)) ||
		scopeChanged(req.ScopeNamespace, current.ScopeNamespace) ||
		scopeChanged(req.ScopeName, current.ScopeName) {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			"a policy's scope cannot be changed; create a new policy for a different scope", "scope_immutable", nil)
		return
	}

	next := *current
	maxAge := int64(current.MaxAge / time.Second)
	if req.MaxAgeSeconds != nil {
		maxAge = *req.MaxAgeSeconds
	}
	grace := int64(current.Grace / time.Second)
	if req.GraceSeconds != nil {
		grace = *req.GraceSeconds
	}
	if req.TreatPartialAs != nil {
		next.TreatPartialAs = *req.TreatPartialAs
	}
	if errs := validateThresholds(&maxAge, grace, next.TreatPartialAs); len(errs) > 0 {
		writeAssuranceFieldErrors(w, errs)
		return
	}
	next.MaxAge = time.Duration(maxAge) * time.Second
	next.Grace = time.Duration(grace) * time.Second
	next.AlertOnPaused = boolOr(req.AlertOnPaused, current.AlertOnPaused)
	next.Enabled = boolOr(req.Enabled, current.Enabled)
	next.Revision = *req.Revision
	next.UpdatedBy = t.user.Username

	if err := t.st.UpdatePolicy(r.Context(), next); err != nil {
		h.auditAssurance(r, t.user, audit.ActionUpdate, next, audit.ResultFailure, "")
		h.writePolicyStoreError(w, r, err, "failed to update backup assurance policy")
		return
	}
	h.auditAssurance(r, t.user, audit.ActionUpdate, next, audit.ResultSuccess, "")

	updated, err := t.st.GetPolicy(r.Context(), t.clusterID, id)
	if err != nil {
		h.assuranceFail(w, r, err, "policy updated but could not be read back")
		return
	}
	httputil.WriteData(w, h.policyViewWithSchedule(r.Context(), *updated))
}

// HandleDeleteAssurancePolicy deletes a policy and, by cascade, every
// exception it opened, open or resolved, with their delivery intents. That
// discards history, so the request must say so explicitly with
// ?confirm=true; without it the answer is 400 confirmation_required carrying
// the number of open exceptions the delete would discard.
func (h *Handler) HandleDeleteAssurancePolicy(w http.ResponseWriter, r *http.Request) {
	t, ok := h.assuranceGate(w, r)
	if !ok {
		return
	}
	id, ok := assurancePolicyID(w, r)
	if !ok {
		return
	}
	current, err := t.st.GetPolicy(r.Context(), t.clusterID, id)
	if err != nil {
		h.writePolicyStoreError(w, r, err, "failed to read backup assurance policy")
		return
	}
	open, err := t.st.ListOpenExceptions(r.Context(), t.clusterID)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to read backup assurance exceptions")
		return
	}
	openForPolicy := 0
	for _, e := range open {
		if e.PolicyID == id {
			openForPolicy++
		}
	}

	if r.URL.Query().Get("confirm") != "true" {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			"deleting a policy discards every exception it opened, open and resolved; repeat the request with confirm=true",
			"confirmation_required", map[string]any{"openExceptions": openForPolicy})
		return
	}

	detail := fmt.Sprintf("cascaded exceptions (open at delete: %d)", openForPolicy)
	if err := t.st.DeletePolicy(r.Context(), t.clusterID, id); err != nil {
		h.auditAssurance(r, t.user, audit.ActionDelete, *current, audit.ResultFailure, detail)
		h.writePolicyStoreError(w, r, err, "failed to delete backup assurance policy")
		return
	}
	h.auditAssurance(r, t.user, audit.ActionDelete, *current, audit.ResultSuccess, detail)
	httputil.WriteData(w, map[string]any{"id": id.String(), "deleted": true, "discardedOpenExceptions": openForPolicy})
}

// policyViewWithSchedule is policyView with schedule existence read now.
func (h *Handler) policyViewWithSchedule(ctx context.Context, p store.BackupAssurancePolicy) AssurancePolicyView {
	if p.ScopeKind != store.ScopeSchedule {
		return policyView(p, nil, false)
	}
	schedules, known := h.assuranceSchedules(ctx)
	return policyView(p, schedules, known)
}

// assurancePolicyID parses {id}. resources.ValidateURLParams covers only
// {name}/{namespace}, so the id is checked here: anything that is not a
// UUID is 400 before the store sees it.
func assurancePolicyID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	// uuid.Parse also accepts the urn: and braced forms; insist on the
	// canonical 36-character form so one policy has one URL.
	if err != nil || len(raw) != 36 || id == uuid.Nil {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "policy id must be a UUID", "invalid_id", nil)
		return uuid.Nil, false
	}
	return id, true
}

// scopeChanged reports whether an update names a scope field that differs
// from the stored value.
func scopeChanged(sent *string, stored string) bool {
	return sent != nil && *sent != stored
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// writePolicyStoreError maps store sentinels onto responses. Anything else
// is logged and answered without its text (CWE-209).
func (h *Handler) writePolicyStoreError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	switch {
	case errors.Is(err, store.ErrAssurancePolicyNotFound):
		httputil.WriteErrorWithReason(w, http.StatusNotFound, "backup assurance policy not found", "policy_not_found", nil)
	case errors.Is(err, store.ErrAssurancePolicyExists):
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			"a policy already exists for this scope", "policy_exists", nil)
	case errors.Is(err, store.ErrAssuranceRevisionConflict):
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			"the policy was changed by someone else; reload it and try again", "revision_conflict", nil)
	case errors.Is(err, store.ErrAssurancePolicyInvalid):
		// The handler validates first; this is a schema CHECK the handler
		// did not anticipate. Say which request is wrong, not which
		// constraint fired.
		h.Logger.Warn("backup assurance: policy rejected by the store", "error", err)
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "invalid policy", "invalid_policy", nil)
	default:
		h.assuranceFail(w, r, err, msg)
	}
}

// auditAssurance records a policy write. ResourceName is the policy id (the
// only unique name a namespace- or cluster-scope policy has); the scope goes
// in Detail.
func (h *Handler) auditAssurance(r *http.Request, user *auth.User, action audit.Action, p store.BackupAssurancePolicy, result audit.Result, extra string) {
	if h.AuditLogger == nil {
		return
	}
	scope := string(p.ScopeKind)
	if target := strings.Trim(p.ScopeNamespace+"/"+p.ScopeName, "/"); target != "" {
		scope += " " + target
	}
	detail := "scope=" + scope
	if extra != "" {
		detail += "; " + extra
	}
	_ = h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         middleware.ClusterIDFromContext(r.Context()),
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      assuranceAuditKind,
		ResourceNamespace: p.ScopeNamespace,
		ResourceName:      p.ID.String(),
		Result:            result,
		Detail:            detail,
	})
}
