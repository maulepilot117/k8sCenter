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
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
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
	// CollectionSource says what Collection was judged from: "this_replica"
	// (this replica's own last run), "lease_holder" (another replica holds a
	// live lease; judged from the durable exceptions it maintains), or
	// "none" (nothing to judge: unavailable, empty, or never collected).
	CollectionSource string `json:"collectionSource"`
	// PolicyCount is every policy for an admin, and for anyone else the
	// policies whose scope namespace they may see (never cluster scope), so
	// the count reveals nothing about namespaces the caller cannot read.
	PolicyCount int                 `json:"policyCount"`
	Open        AssuranceOpenCounts `json:"open"`
	// Runtime is admin-only and omitted for everyone else.
	Runtime *AssuranceRuntimeView `json:"runtime,omitempty"`
}

// AssuranceRuntimeView is the collector's operational state, for admins.
type AssuranceRuntimeView struct {
	Holder     string     `json:"holder"`
	LastTickAt *time.Time `json:"lastTickAt"`
	// LastRunAt and LastCollection describe THIS replica's last completed
	// run ("ok", "failed", or "" before the first).
	LastRunAt       *time.Time           `json:"lastRunAt"`
	LastCollection  string               `json:"lastCollection"`
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
	// Any non-local selection is refused, including one that happens to equal
	// the collector's configured cluster id: the router treats that id as a
	// remote cluster, and answering it from the local store would serve local
	// data under a remote cluster's name.
	if !isLocal(r.Context()) {
		httputil.WriteErrorWithReason(w, http.StatusNotImplemented,
			"backup assurance is collected for the local cluster only", assuranceReasonRemote, nil)
		return assuranceTarget{}, false
	}
	if h.AssuranceStore == nil || h.Assurance == nil {
		writeAssuranceUnavailable(w)
		return assuranceTarget{}, false
	}
	clusterID := h.Assurance.clusterID
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
func (h *Handler) assuranceVisibleNamespaces(r *http.Request, t assuranceTarget, policies []store.BackupAssurancePolicy) (namespaces []string, admin bool, err error) {
	if auth.IsAdmin(t.user) {
		return nil, true, nil
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

// assuranceScheduleSet is the local schedule inventory as of one shared
// Velero read: uids keyed by "namespace/name". known=false means Velero was
// not detected or the read failed, and every answer is then unknown.
type assuranceScheduleSet struct {
	uids  map[string]string
	known bool
}

// assuranceSchedules reads the inventory through the handler's
// service-account cache; no write.
func (h *Handler) assuranceSchedules(ctx context.Context) *assuranceScheduleSet {
	if h.Discoverer == nil || !h.Discoverer.Status(ctx).Detected {
		return &assuranceScheduleSet{}
	}
	data, err := h.fetchAll(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.Logger.Warn("backup assurance: velero read failed; schedule existence is unknown", "error", err)
		}
		return &assuranceScheduleSet{}
	}
	out := &assuranceScheduleSet{uids: make(map[string]string, len(data.schedules)), known: true}
	for _, s := range data.schedules {
		out.uids[s.Namespace+"/"+s.Name] = s.UID
	}
	return out
}

// existence reports whether the schedule ns/name exists: found, not_found
// or unknown, with the "schedule not found" note for not_found. A non-empty
// uid must match too, so a schedule recreated under the same name is
// not_found for rows about its predecessor. A nil set is unknown.
func (s *assuranceScheduleSet) existence(ns, name, uid string) (*string, string) {
	status := assuranceScheduleUnknown
	if s != nil && s.known {
		status = assuranceScheduleNotFound
		if got, ok := s.uids[ns+"/"+name]; ok && (uid == "" || got == uid) {
			status = assuranceScheduleFound
		}
	}
	if status == assuranceScheduleNotFound {
		return &status, assuranceScheduleNotFoundNote
	}
	return &status, ""
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// HandleAssuranceStatus reports the collector's state and the caller's
// visible open-exception counts. This replica's runtime, the lease, the last
// error and the delivery backlog are admin-only.
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
	visible, admin, err := h.assuranceVisibleNamespaces(r, t, policies)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to check access")
		return
	}
	open, err := t.st.ListOpenExceptions(ctx, t.clusterID)
	if err != nil {
		h.assuranceFail(w, r, err, "failed to read backup assurance status")
		return
	}
	var lease *store.AssuranceLease
	switch l, err := t.st.GetLease(ctx, t.clusterID); {
	case err == nil:
		lease = &l
	case errors.Is(err, store.ErrLeaseNotFound):
	default:
		h.assuranceFail(w, r, err, "failed to read backup assurance status")
		return
	}

	policyCount := len(policies)
	if !admin {
		policyCount = 0
		for _, p := range policies {
			if p.ScopeNamespace != "" && slices.Contains(visible, p.ScopeNamespace) {
				policyCount++
			}
		}
	}
	collectionUnknownOpen := slices.ContainsFunc(open, func(e store.BackupAssuranceException) bool {
		return e.Condition == store.ConditionCollectionUnknown
	})

	snap := h.Assurance.Snapshot()
	state, source := assuranceCollectionState(snap, lease, policyCount, collectionUnknownOpen, time.Now())
	view := AssuranceStatusView{
		Enabled:          snap.Enabled,
		Collection:       state,
		CollectionSource: source,
		PolicyCount:      policyCount,
		Open:             countOpen(open, admin, visible),
	}

	if admin {
		rt := &AssuranceRuntimeView{
			Holder:         snap.Holder,
			LastTickAt:     timePtr(snap.LastTickAt),
			LastRunAt:      timePtr(snap.LastRunAt),
			LastCollection: string(snap.LastCollection),
			FindingCount:   snap.FindingCount,
			LastError:      snap.LastError,
			LastErrorAt:    timePtr(snap.LastErrorAt),
			LeaseHeld:      snap.LeaseHeld,
		}
		if lease != nil {
			rt.Lease = &AssuranceLeaseView{Holder: lease.Holder, Fence: lease.Fence, ExpiresAt: lease.ExpiresAt, Expired: lease.Expired}
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

// Values of AssuranceStatusView.CollectionSource.
const (
	assuranceSourceThisReplica = "this_replica"
	assuranceSourceLeaseHolder = "lease_holder"
	assuranceSourceNone        = "none"
)

// assuranceCollectionState maps the collector's evidence onto the status
// vocabulary and says where the verdict came from. The hard rule (section 5)
// at the API layer: anything other than a recent ok collection is never
// reported as ok, and a collection that failed or never happened is unknown,
// never "no backups".
//
// The runtime snapshot is per replica, and only the lease holder collects.
// When another replica holds a live lease, this replica's snapshot says
// nothing, so the verdict comes from durable state that holder maintains: a
// failed collection always leaves a cluster-scope collection_unknown
// exception open (the evaluator emits exactly one, and only an ok collection
// resolves it), so its presence is unknown and its absence, under a lease
// renewed within its TTL, is ok. An expired or absent lease falls back to
// this replica's own snapshot, which then reads unknown or stale.
func assuranceCollectionState(snap AssuranceRuntimeStatus, lease *store.AssuranceLease, policyCount int, collectionUnknownOpen bool, now time.Time) (state, source string) {
	switch {
	case !snap.Enabled:
		return AssuranceStateUnavailable, assuranceSourceNone
	case policyCount == 0:
		// No policy means no evaluation: install creates none (O-2).
		return AssuranceStateEmpty, assuranceSourceNone
	case !snap.LeaseHeld && lease != nil && !lease.Expired && lease.Holder != snap.Holder:
		if collectionUnknownOpen {
			return AssuranceStateUnknown, assuranceSourceLeaseHolder
		}
		return AssuranceStateOK, assuranceSourceLeaseHolder
	case snap.LastCollection == "":
		return AssuranceStateUnknown, assuranceSourceNone
	case snap.LastCollection != CollectionOK:
		return AssuranceStateUnknown, assuranceSourceThisReplica
	case snap.LastRunAt.IsZero() || now.Sub(snap.LastRunAt) > assuranceStaleAfter:
		return AssuranceStateStale, assuranceSourceThisReplica
	}
	return AssuranceStateOK, assuranceSourceThisReplica
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

	var policies []store.BackupAssurancePolicy
	if !auth.IsAdmin(t.user) {
		var err error
		if policies, err = t.st.ListPolicies(r.Context(), t.clusterID); err != nil {
			h.assuranceFail(w, r, err, "failed to list backup assurance exceptions")
			return
		}
	}
	visible, admin, err := h.assuranceVisibleNamespaces(r, t, policies)
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
	var schedules *assuranceScheduleSet
	dropped := false
	for _, e := range rows {
		if !exceptionVisible(e, admin, visible) {
			dropped = true
			continue
		}
		v := projectException(e, admin)
		if e.SubjectKind == store.ScopeSchedule {
			if schedules == nil {
				schedules = h.assuranceSchedules(r.Context())
			}
			v.SubjectStatus, v.SubjectNote = schedules.existence(e.SubjectNamespace, e.SubjectName, e.SubjectUID)
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
func projectException(e store.BackupAssuranceException, admin bool) AssuranceExceptionView {
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
	// The expected run only bears on the freshness conditions; on a failed,
	// paused, location or collection exception "not computable" would claim
	// an evaluation that never happened. Namespace and cluster scope keep the
	// note on overdue: they have no cron, so the overdue verdict fell back to
	// max age + grace, which is exactly what the note tells the reader (§5).
	if !v.Detail.ExpectedRunKnown && (e.Condition == store.ConditionOverdue || e.Condition == store.ConditionNeverRun) {
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
