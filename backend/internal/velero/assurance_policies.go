package velero

// assurance_policies.go — Release F U35: admin-managed backup-assurance
// policies (list, create, update, delete). Split from assurance_handler.go,
// which holds the shared gates and the read endpoints. Every write here
// touches PostgreSQL only; none reaches Kubernetes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const (
	// assurancePolicyBodyLimit bounds a policy write body; a policy is a
	// handful of scalars.
	assurancePolicyBodyLimit = 16 << 10

	// assuranceDefaultGrace is the grace a new policy gets when none is
	// given: one hour absorbs the largest DST discontinuity (section 5).
	assuranceDefaultGrace = time.Hour

	// assuranceAuditKind is the audit ResourceKind for policy writes.
	assuranceAuditKind = "BackupAssurancePolicy"
)

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

	var schedules *assuranceScheduleSet
	if slices.ContainsFunc(policies, func(p store.BackupAssurancePolicy) bool { return p.ScopeKind == store.ScopeSchedule }) {
		schedules = h.assuranceSchedules(r.Context())
	}
	views := make([]AssurancePolicyView, 0, len(policies))
	for _, p := range policies {
		views = append(views, policyView(p, schedules))
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: views, Metadata: &api.Metadata{Total: len(views)}})
}

// policyView builds the wire view. schedules comes from assuranceSchedules;
// nil (not read) is only valid for non-schedule scopes.
func policyView(p store.BackupAssurancePolicy, schedules *assuranceScheduleSet) AssurancePolicyView {
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
		// A policy names a schedule by namespace and name, not uid.
		v.ScheduleStatus, v.ScheduleNote = schedules.existence(p.ScopeNamespace, p.ScopeName, "")
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

	// The write has committed: a failed read-back must not turn it into a
	// 500 that a retrying client would then meet as 409 policy_exists.
	// Answer from what was written; only the database timestamp is approximate.
	created, err := t.st.GetPolicy(r.Context(), t.clusterID, p.ID)
	if err != nil {
		h.Logger.Warn("backup assurance: created policy could not be read back; answering from the write", "error", err)
		p.CreatedAt, p.Revision = time.Now().UTC(), 1
		created = &p
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

	// As in create: the update committed (revision is now Revision+1), so a
	// failed read-back answers from the write rather than with a 500.
	updated, err := t.st.GetPolicy(r.Context(), t.clusterID, id)
	if err != nil {
		h.Logger.Warn("backup assurance: updated policy could not be read back; answering from the write", "error", err)
		now := time.Now().UTC()
		next.Revision, next.UpdatedAt = next.Revision+1, &now
		updated = &next
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
		return policyView(p, nil)
	}
	return policyView(p, h.assuranceSchedules(ctx))
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
