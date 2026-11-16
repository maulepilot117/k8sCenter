package rubrik

// writes.go - on-demand snapshot and SLA change, and the operation read
// (U7, KTD7, KTD8).
//
// Every write follows one sequence: the writes setting, the viewer (remote
// clusters are admin-only), the operation id's earlier row (a retry is
// answered from it and never reaches RSC), the read grant, the mapping, the
// object the request names, the action's own grant, then the durable
// operation row written as applying, the RSC mutation, and the row's final
// state. The decision is audited once, with the RSC job id.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// OperationStore is the part of store.RubrikStore the writes use.
type OperationStore interface {
	InsertOperation(ctx context.Context, op store.RubrikOperation) (store.RubrikOperation, error)
	GetOperation(ctx context.Context, id string) (store.RubrikOperation, error)
	UpdateOperation(ctx context.Context, id string, from []string, u store.RubrikOperationUpdate) (store.RubrikOperation, error)
}

// RSCWriter is the part of Operations the writes use: the mutations and the
// two ways of following the job one started.
type RSCWriter interface {
	CreateSnapshot(ctx context.Context, family Family, objectID, slaID string) (JobHandle, error)
	AssignSLA(ctx context.Context, objectIDs []string, slaID string) error
	Taskchain(ctx context.Context, taskchainID string) (TaskchainState, error)
	Jobs(ctx context.Context, f ActivityFilter) ([]Job, error)
}

// The production types implement the write interfaces, so NewHandler can
// take them from HandlerDeps.Store and HandlerDeps.RSC.
var (
	_ OperationStore = (*store.RubrikStore)(nil)
	_ RSCWriter      = (*Operations)(nil)
)

const (
	// writeRSCTimeout bounds one RSC mutation. It runs detached from the
	// request, so a client that hangs up does not turn a sent mutation
	// into an unrecorded one.
	writeRSCTimeout = 60 * time.Second
	// recordTimeout bounds the store write that records a mutation's
	// outcome, also detached from the request.
	recordTimeout = 5 * time.Second
	// jobStartSkew widens the activity-series window a job is looked up
	// in, for clock differences between k8sCenter and RSC.
	jobStartSkew = time.Minute
	// noJobGrace is how long an operation of unknown outcome may show no
	// RSC job before it is settled as failed.
	noJobGrace = 10 * time.Minute
	// maxJobMessage caps the RSC job text kept on an operation.
	maxJobMessage = 1024

	auditKindProtection = "rubrik-protection"
)

// Fixed operation messages. RSC error text is never stored or returned for
// a mutation; a job's own failure reason is RSC's text, as in job history.
const (
	msgWritesDisabled   = "Rubrik write actions are turned off"
	msgWritesNotWired   = "Rubrik write actions are not available"
	msgOperationReused  = "this operation id was already used for a different request"
	msgOperationTaken   = "this operation id belongs to another request"
	msgOperationRunning = "this operation is still being sent; try again shortly"
	msgObjectInFlight   = "another Rubrik operation on this object is still running"
	msgObjectNotFound   = "no Rubrik object you can act on protects this namespace"
	msgObjectRequired   = "more than one Rubrik object protects this namespace; choose one"
	msgOperationGone    = "operation not found"
	msgCanceled         = "the job was canceled in RSC"
	msgNoJob            = "RSC shows no job for this request"
	msgSLANotApplied    = "RSC shows a different SLA domain; the change was not applied"
	msgRejected         = "RSC refused the change; nothing was applied"
)

// writeRequest is the body of both writes. SLAID is required for an SLA
// change: an empty string removes protection, so a missing field can never
// remove it by accident.
type writeRequest struct {
	OperationID string  `json:"operationId"`
	ObjectID    string  `json:"objectId,omitempty"`
	SLAID       *string `json:"slaId,omitempty"`
}

// opRequest is what the operation row stores about the request.
type opRequest struct {
	Family Family `json:"family"`
	// SLAID is the SLA domain sent: for a snapshot the one it ran under,
	// for an SLA change the new domain, empty for a removal.
	SLAID string `json:"slaId"`
	// RequestedObjectID is the objectId the client named, empty when it
	// let the server choose the namespace's only object.
	RequestedObjectID string `json:"requestedObjectId,omitempty"`
	// StillProtected is set for an SLA removal: whether another object the
	// user could see still protected the namespace (R15). It is stored so a
	// replay and a later read answer it too.
	StillProtected *bool `json:"stillProtected,omitempty"`
}

// OperationView is an operation as the API returns it.
type OperationView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	ClusterID string `json:"clusterId"`
	Namespace string `json:"namespace"`
	ObjectID  string `json:"objectId"`
	// State is applying, running, succeeded, failed, partial or unknown.
	State     string `json:"state"`
	JobFamily string `json:"jobFamily,omitempty"`
	JobID     string `json:"jobId,omitempty"`
	// Message says why a failed, partial or unknown operation ended so.
	Message    string     `json:"message,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// Replayed is true when the request repeated an earlier one and was
	// answered from its record without reaching RSC.
	Replayed bool `json:"replayed,omitempty"`
	// StillProtected answers an SLA removal: whether another object you
	// can see still protects the namespace (R15).
	StillProtected *bool `json:"stillProtected,omitempty"`
	// RefreshError is the class of a failed job refresh; the state is then
	// the last one recorded.
	RefreshError string `json:"refreshError,omitempty"`
}

func operationView(op store.RubrikOperation) OperationView {
	out := OperationView{
		ID: op.ID, Kind: op.Kind, ClusterID: op.ClusterID, Namespace: op.Namespace, ObjectID: op.RSCObjectID,
		State: op.State, JobFamily: op.JobFamily, JobID: op.JobHandle, Message: op.FailureMessage,
		CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt, FinishedAt: op.FinishedAt,
	}
	if op.Kind == store.RubrikOpSLAAssign {
		var rec opRequest
		if json.Unmarshal(op.Request, &rec) == nil {
			out.StillProtected = rec.StillProtected
		}
	}
	return out
}

// writeKind ties an endpoint to its operation kind, grant and audit action.
type writeKind struct {
	kind   string
	action Action
	audit  audit.Action
}

var (
	kindSnapshot = writeKind{store.RubrikOpSnapshot, ActionSnapshot, audit.ActionRubrikSnapshot}
	kindSLA      = writeKind{store.RubrikOpSLAAssign, ActionChangeSLA, audit.ActionRubrikSLAAssign}
)

// writeCall is one write request as it moves through the sequence.
type writeCall struct {
	w    http.ResponseWriter
	r    *http.Request
	k    writeKind
	user *auth.User
	ns   string
	req  writeRequest
	id   string
	hash string
	rr   readRequest
	// object is the RSC object acted on, once chosen; it names the audit
	// entry.
	object string
}

// audit records the call's decision. Entries carry the operation id, the
// RSC object and job id and a reason class, never RSC error text.
func (c *writeCall) audit(h *Handler, result audit.Result, detail string) {
	if err := h.audit.Log(c.r.Context(), audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         c.rr.clusterID,
		User:              c.user.Username,
		SourceIP:          c.r.RemoteAddr,
		ConnectionIP:      middleware.SocketPeerFromContext(c.r.Context()),
		Action:            c.k.audit,
		ResourceKind:      auditKindProtection,
		ResourceNamespace: c.ns,
		ResourceName:      c.object,
		Result:            result,
		Detail:            "operation " + c.id + "; " + detail,
	}); err != nil {
		h.logger.Error("rubrik: audit write failed", "action", string(c.k.audit), "error", err)
	}
}

// refuse answers with a refusal and audits it as denied.
func (c *writeCall) refuse(h *Handler, status int, msg, reason string) {
	c.audit(h, audit.ResultDenied, reason)
	httputil.WriteErrorWithReason(c.w, status, msg, reason, nil)
}

// fail answers with an error that stopped the write and audits it as a
// failure. Nothing was sent to RSC.
func (c *writeCall) fail(h *Handler, status int, msg, reason string, extra map[string]any) {
	c.audit(h, audit.ResultFailure, reason)
	httputil.WriteErrorWithReason(c.w, status, msg, reason, extra)
}

// accessFailed answers a write whose access check could not be made: it is
// logged and audited as a failure, and nothing was sent to RSC.
func (c *writeCall) accessFailed(h *Handler, err error) {
	h.logger.Error("rubrik write: access check failed", "error", err)
	c.fail(h, http.StatusServiceUnavailable, msgAccessCheck, "access_check_failed", nil)
}

// HandleSnapshot answers POST /rubrik/namespaces/{namespace}/snapshots: an
// on-demand snapshot of the namespace's object (R14, KTD8).
func (h *Handler) HandleSnapshot(w http.ResponseWriter, r *http.Request) {
	h.handleWrite(w, r, kindSnapshot)
}

// HandleSLA answers PUT /rubrik/namespaces/{namespace}/sla: assign, change
// or remove (slaId "") the SLA domain of the namespace's object (R15). The
// change always retains existing snapshots (KTD8, AE5).
func (h *Handler) HandleSLA(w http.ResponseWriter, r *http.Request) {
	h.handleWrite(w, r, kindSLA)
}

func (h *Handler) handleWrite(w http.ResponseWriter, r *http.Request, k writeKind) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if h.ops == nil || h.writer == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgWritesNotWired, "writes_unavailable", nil)
		return
	}
	if user.ID == "" {
		httputil.WriteError(w, http.StatusForbidden, "your session has no user id", "")
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !validNamespaceName(ns) {
		httputil.WriteError(w, http.StatusNotFound, msgNamespaceGone, "")
		return
	}
	var req writeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	id, ok := parseOperationID(req.OperationID)
	if !ok {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "operationId must be a UUID", "invalid_operation_id", nil)
		return
	}
	if msg := validateWriteRequest(k, req); msg != "" {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, msg, "invalid_request", nil)
		return
	}
	clusterID := k8s.NormalizedClusterID(middleware.ClusterIDFromContext(r.Context()))
	c := &writeCall{
		w: w, r: r, k: k, user: user, ns: ns, req: req, id: id,
		hash: requestHash(k, clusterID, ns, req), rr: readRequest{clusterID: clusterID},
	}

	// AE8: with writes off nothing else happens, for admins too (R29).
	if !h.access.WritesEnabled() {
		c.refuse(h, http.StatusForbidden, msgWritesDisabled, string(RefusalWritesDisabled))
		return
	}
	v, err := h.access.Viewer(user, clusterID)
	switch {
	case errors.Is(err, ErrRemoteAdminOnly):
		c.refuse(h, http.StatusForbidden, msgRemoteAdmin, StateAdminOnly)
		return
	case err != nil:
		c.accessFailed(h, err)
		return
	}
	c.rr.viewer = v

	// A retry is answered from the earlier row before anything is checked
	// again, so it can never reach RSC twice.
	if done := h.answerEarlier(c); done {
		return
	}

	ctx := r.Context()
	if !v.Admin() {
		_, readable, err := v.readable(ctx, ns)
		if err != nil {
			c.accessFailed(h, err)
			return
		}
		if !readable {
			c.refuse(h, http.StatusNotFound, msgNamespaceGone, "no_read_grant")
			return
		}
	}

	t, err := h.service.Resolve(ctx, clusterID)
	if err != nil {
		// A setup problem refuses the write (R2); a store outage fails it.
		result := audit.ResultDenied
		if errors.Is(err, ErrStoreUnavailable) {
			result = audit.ResultFailure
		}
		c.audit(h, result, "setup: "+errorClass(err))
		h.writeReadError(w, c.rr, err)
		return
	}
	view, err := h.service.Objects(ctx, t)
	if err == nil && view.Freshness.Err != nil {
		// Writes act on current data only: a stale list may name an
		// object RSC no longer has.
		err = view.Freshness.Err
	}
	if err != nil {
		c.audit(h, audit.ResultFailure, "objects: "+errorClass(err))
		h.writeReadError(w, c.rr, err)
		return
	}

	candidates, err := h.writeCandidates(ctx, v, ns, view.Objects)
	if err != nil {
		c.accessFailed(h, err)
		return
	}
	o, ok := chooseObject(c, h, candidates)
	if !ok {
		return
	}
	c.object = o.ID

	refusal, err := v.CheckAction(ctx, t, k.action, o, "")
	if err != nil {
		c.accessFailed(h, err)
		return
	}
	if refusal != RefusalNone {
		c.refuse(h, refusalStatus(refusal), refusalMessage(refusal), string(refusal))
		return
	}

	rec := opRequest{Family: o.Family, RequestedObjectID: req.ObjectID}
	switch k.kind {
	case store.RubrikOpSnapshot:
		sla, ok := h.snapshotSLA(c, t, o)
		if !ok {
			return
		}
		rec.SLAID = sla
	case store.RubrikOpSLAAssign:
		rec.SLAID = *req.SLAID
		if rec.SLAID != "" && !h.knownSLA(c, t, rec.SLAID) {
			return
		}
		if rec.SLAID == "" {
			other := slices.ContainsFunc(candidates, func(x Object) bool { return x.ID != o.ID && x.Protecting() })
			rec.StillProtected = &other
		}
	}

	h.send(c, t, o, rec)
}

// answerEarlier answers a request whose operation id already has a row, in
// the order of docs/solutions/tracked-apply-durable-intent.md: another
// owner's id is a conflict that discloses nothing, a different request is
// operation_id_reused, a row still being sent is operation_in_flight, and
// anything else is replayed from the row. It reports whether it answered.
func (h *Handler) answerEarlier(c *writeCall) bool {
	op, err := h.ops.GetOperation(c.r.Context(), c.id)
	switch {
	case errors.Is(err, store.ErrRubrikOperationNotFound):
		return false
	case err != nil:
		h.logger.Error("rubrik write: reading the operation failed", "error", err)
		c.fail(h, http.StatusServiceUnavailable, msgStoreUnavailable, "store_unavailable",
			map[string]any{"applied": false, "retrySameOperationId": true})
		return true
	}
	h.replay(c, op)
	return true
}

// replay answers a request from the stored row of its operation id.
func (h *Handler) replay(c *writeCall, op store.RubrikOperation) {
	c.object = op.RSCObjectID
	switch {
	case op.Owner != c.user.ID:
		c.fail(h, http.StatusConflict, msgOperationTaken, "operation_id_conflict", nil)
	case op.Kind != c.k.kind || op.ClusterID != c.rr.clusterID || op.RequestHash != c.hash:
		c.fail(h, http.StatusConflict, msgOperationReused, "operation_id_reused", nil)
	case op.State == store.RubrikOpApplying:
		httputil.WriteErrorWithReason(c.w, http.StatusConflict, msgOperationRunning, "operation_in_flight",
			map[string]any{"operationId": op.ID})
	default:
		// The same rule as reading the operation: a retry after the read
		// grant was revoked learns nothing.
		visible, err := h.operationVisible(c.r.Context(), c.user, op)
		if err != nil {
			c.accessFailed(h, err)
			return
		}
		if !visible {
			c.refuse(h, http.StatusNotFound, msgNamespaceGone, "no_read_grant")
			return
		}
		// Not audited: nothing new was decided or sent.
		out := operationView(op)
		out.Replayed = true
		httputil.WriteData(c.w, out)
	}
}

// writeCandidates returns the objects a write on namespace ns may target:
// the viewer's visible, current objects covering ns. A relic, and every
// object of a namespace that no longer exists locally, is an earlier
// namespace's and never a target (KTD4).
func (h *Handler) writeCandidates(ctx context.Context, v *Viewer, ns string, objects []Object) ([]Object, error) {
	earlier, err := v.EarlierNamespace(ns)
	if err != nil || earlier {
		return nil, err
	}
	visible, err := v.VisibleObjects(ctx, objects)
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(visible))
	for _, o := range visible {
		if !o.Relic && o.Coverage.Covers(ns) {
			out = append(out, o)
		}
	}
	return out, nil
}

// chooseObject picks the object the request acts on (R15): the one it
// names, which must be a candidate, or the namespace's only candidate.
func chooseObject(c *writeCall, h *Handler, candidates []Object) (Object, bool) {
	if c.req.ObjectID != "" {
		for _, o := range candidates {
			if strings.EqualFold(o.ID, c.req.ObjectID) {
				return o, true
			}
		}
		c.object = c.req.ObjectID
		c.refuse(h, http.StatusNotFound, msgObjectNotFound, "object_not_found")
		return Object{}, false
	}
	switch len(candidates) {
	case 0:
		c.refuse(h, http.StatusNotFound, msgObjectNotFound, "object_not_found")
		return Object{}, false
	case 1:
		return candidates[0], true
	}
	ids := make([]string, 0, len(candidates))
	for _, o := range candidates {
		ids = append(ids, o.ID)
	}
	slices.Sort(ids)
	c.audit(h, audit.ResultDenied, "object_required")
	httputil.WriteErrorWithReason(c.w, http.StatusBadRequest, msgObjectRequired, "object_required",
		map[string]any{"objectIds": ids})
	return Object{}, false
}

// snapshotSLA returns the SLA domain a snapshot of o runs under (KTD8): the
// object's effective SLA, or the one the user picked when it has none. A
// pick that differs from the object's own SLA is refused rather than
// ignored.
func (h *Handler) snapshotSLA(c *writeCall, t Target, o Object) (string, bool) {
	picked := ""
	if c.req.SLAID != nil {
		picked = *c.req.SLAID
	}
	if own, has := o.SnapshotSLA(); has {
		if picked != "" && !strings.EqualFold(picked, own) {
			c.refuse(h, http.StatusBadRequest, "the snapshot runs under the object's own SLA domain", "sla_not_allowed")
			return "", false
		}
		return own, true
	}
	if picked == "" {
		c.refuse(h, http.StatusBadRequest, "the object has no SLA domain; choose one for the snapshot", "sla_required")
		return "", false
	}
	if !h.knownSLA(c, t, picked) {
		return "", false
	}
	return picked, true
}

// knownSLA reports whether slaID is an SLA domain the service account can
// see (R15), answering the request when it is not or cannot be told.
func (h *Handler) knownSLA(c *writeCall, t Target, slaID string) bool {
	domains, f, err := h.service.SLADomains(c.r.Context(), t)
	if err == nil && f.Err != nil {
		err = f.Err
	}
	if err != nil {
		c.audit(h, audit.ResultFailure, "sla domains: "+errorClass(err))
		h.writeReadError(c.w, c.rr, err)
		return false
	}
	if slices.ContainsFunc(domains, func(d SLADomain) bool { return strings.EqualFold(d.ID, slaID) }) {
		return true
	}
	c.refuse(h, http.StatusBadRequest, "unknown SLA domain", "unknown_sla")
	return false
}

// send records the operation, sends the mutation and records its outcome
// (KTD7). An outcome that cannot be told is unknown and is never resent.
func (h *Handler) send(c *writeCall, t Target, o Object, rec opRequest) {
	raw, err := json.Marshal(rec)
	if err != nil {
		c.fail(h, http.StatusInternalServerError, "the request could not be recorded", "internal", nil)
		return
	}
	op, err := h.ops.InsertOperation(c.r.Context(), store.RubrikOperation{
		ID: c.id, Owner: c.user.ID, ClusterID: t.ClusterID, Kind: c.k.kind, Namespace: c.ns,
		RSCObjectID: o.ID, RequestHash: c.hash, Request: raw,
	})
	switch {
	case errors.Is(err, store.ErrRubrikOperationExists):
		// A concurrent send of the same id won the insert.
		if existing, getErr := h.ops.GetOperation(c.r.Context(), c.id); getErr == nil {
			h.replay(c, existing)
			return
		}
		c.fail(h, http.StatusServiceUnavailable, msgStoreUnavailable, "store_unavailable",
			map[string]any{"applied": false, "retrySameOperationId": true})
		return
	case errors.Is(err, store.ErrRubrikOperationInFlight):
		c.fail(h, http.StatusConflict, msgObjectInFlight, "operation_in_flight", nil)
		return
	case err != nil:
		h.logger.Error("rubrik write: recording the operation failed", "error", err)
		c.fail(h, http.StatusServiceUnavailable, msgStoreUnavailable, "store_unavailable",
			map[string]any{"applied": false, "retrySameOperationId": true})
		return
	}

	// Detached from the request: a client that hangs up must not cut a
	// sent mutation short or leave its outcome unrecorded.
	mctx, cancel := context.WithTimeout(context.WithoutCancel(c.r.Context()), writeRSCTimeout)
	update := store.RubrikOperationUpdate{}
	switch c.k.kind {
	case store.RubrikOpSnapshot:
		var handle JobHandle
		handle, err = h.writer.CreateSnapshot(mctx, o.Family, o.ID, rec.SLAID)
		if err == nil {
			update.State = store.RubrikOpRunning
			update.JobFamily, update.JobHandle = jobRef(handle)
		}
	case store.RubrikOpSLAAssign:
		err = h.writer.AssignSLA(mctx, []string{o.ID}, rec.SLAID)
		if err == nil {
			update.State = store.RubrikOpSucceeded
		}
	}
	cancel()
	reason := ""
	if err != nil {
		_, reason, update.FailureMessage = classifyRSCError(err)
		if errors.Is(err, ErrRejected) {
			reason, update.FailureMessage = "rejected", msgRejected
		}
		update.State = store.RubrikOpUnknown
		if mutationRefused(err) {
			update.State = store.RubrikOpFailed
		}
		h.logger.Warn("rubrik write: mutation did not succeed", "kind", c.k.kind, "operation", c.id,
			"state", update.State, "reason", reason, "error", err)
	}
	if c.k.kind == store.RubrikOpSLAAssign && update.State != store.RubrikOpFailed {
		// The object list now shows the old SLA; drop it so reads catch up.
		h.EvictCluster(t.ClusterID)
	}

	recorded := op
	rctx, cancel := context.WithTimeout(context.WithoutCancel(c.r.Context()), recordTimeout)
	final, uerr := h.ops.UpdateOperation(rctx, op.ID, []string{store.RubrikOpApplying}, update)
	cancel()
	switch {
	case uerr == nil, errors.Is(uerr, store.ErrRubrikOperationStateChanged):
		recorded = final
	default:
		// The row stays applying; the poller settles it later. Report
		// what is known.
		h.logger.Error("rubrik write: recording the outcome failed", "operation", c.id, "error", uerr)
		recorded.State = update.State
		recorded.JobFamily, recorded.JobHandle = update.JobFamily, update.JobHandle
		recorded.FailureMessage = update.FailureMessage
	}

	detail := update.State
	if update.JobHandle != "" {
		detail += "; job " + update.JobHandle
	}
	if reason != "" {
		detail += "; " + reason
	}
	if c.k.kind == store.RubrikOpSLAAssign {
		if rec.SLAID == "" {
			detail += "; sla removed"
		} else {
			detail += "; sla " + rec.SLAID
		}
	}
	result := audit.ResultSuccess
	if update.State == store.RubrikOpFailed || update.State == store.RubrikOpUnknown {
		result = audit.ResultFailure
	}
	c.audit(h, result, detail)

	out := operationView(recorded)
	out.StillProtected = rec.StillProtected
	httputil.WriteJSON(c.w, http.StatusCreated, map[string]any{"data": out})
}

// mutationRefused reports whether a mutation error proves RSC applied
// nothing: it refused the request outright, or it was never sent. Anything
// else (a timeout, a 5xx, an unreadable answer, a GraphQL error) may have
// been applied, so the outcome is unknown (KTD7).
func mutationRefused(err error) bool {
	for _, target := range []error{
		ErrRejected, ErrInvalidRequest, ErrNotConfigured, ErrConnectionUnavailable,
		ErrInvalidTenantURL, ErrAuthFailed, ErrRateLimited,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// jobRef returns the stored family and handle of a started job.
func jobRef(h JobHandle) (family, handle string) {
	if h.TaskchainID != "" {
		return store.RubrikJobTaskchain, h.TaskchainID
	}
	return store.RubrikJobAsyncRequest, h.RequestID
}

// HandleOperation answers GET /rubrik/operations/{id}. Only the owner and
// admins see an operation, and a non-admin owner must still hold the read
// grant on its namespace, which must be the one the operation acted on
// (KTD7); anyone else gets 404, so ids cannot be probed. A running or
// unknown operation is refreshed from RSC first.
func (h *Handler) HandleOperation(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if h.ops == nil || h.writer == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgWritesNotWired, "writes_unavailable", nil)
		return
	}
	ctx := r.Context()
	op, err := h.ops.GetOperation(ctx, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, store.ErrRubrikOperationNotFound):
		httputil.WriteError(w, http.StatusNotFound, msgOperationGone, "")
		return
	case err != nil:
		h.logger.Error("rubrik operation: reading failed", "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgStoreUnavailable, "store_unavailable", nil)
		return
	}
	visible, err := h.operationVisible(ctx, user, op)
	if err != nil {
		h.logger.Error("rubrik operation: access check failed", "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgAccessCheck, "access_check_failed", nil)
		return
	}
	if !visible {
		httputil.WriteError(w, http.StatusNotFound, msgOperationGone, "")
		return
	}
	op, refreshErr := h.refreshOperation(ctx, op)
	out := operationView(op)
	out.RefreshError = refreshErr
	httputil.WriteData(w, out)
}

// operationVisible applies the operation read rule (KTD7, R6, R10).
func (h *Handler) operationVisible(ctx context.Context, user *auth.User, op store.RubrikOperation) (bool, error) {
	v, err := h.access.Viewer(user, op.ClusterID)
	if errors.Is(err, ErrRemoteAdminOnly) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if v.Admin() {
		return true, nil
	}
	if user.ID == "" || op.Owner != user.ID {
		return false, nil
	}
	st, readable, err := v.readable(ctx, op.Namespace)
	if err != nil {
		return false, err
	}
	// An operation older than the namespace acted on an earlier namespace
	// of the same name (R6).
	return st.exists && readable && !op.CreatedAt.Before(st.created), nil
}

// refreshOperation follows a running or unknown operation's job in RSC and
// records a change. It returns the operation as it now stands and, when RSC
// or the store could not answer, the failure's class. An applying row is
// left alone: its request may still be sending it.
func (h *Handler) refreshOperation(ctx context.Context, op store.RubrikOperation) (store.RubrikOperation, string) {
	if op.State != store.RubrikOpRunning && op.State != store.RubrikOpUnknown {
		return op, ""
	}
	var (
		u   store.RubrikOperationUpdate
		err error
	)
	if op.Kind == store.RubrikOpSLAAssign {
		u, err = h.settleSLA(ctx, op)
	} else {
		u, err = h.followJob(ctx, op)
	}
	if err != nil {
		_, reason, _ := classifyRSCError(err)
		switch {
		case errors.Is(err, ErrNotConfigured), errors.Is(err, ErrNotMapped), errors.Is(err, ErrMappingInvalid),
			errors.Is(err, ErrTargetMissing):
			reason = StateNotSetUp
		case errors.Is(err, ErrStoreUnavailable):
			reason = "store_unavailable"
		}
		h.logger.Warn("rubrik operation: refresh failed", "operation", op.ID, "reason", reason, "error", err)
		return op, reason
	}
	if u.State == "" || (u.State == op.State && u.JobHandle == "") {
		return op, ""
	}
	next, err := h.ops.UpdateOperation(ctx, op.ID, []string{op.State}, u)
	switch {
	case err == nil, errors.Is(err, store.ErrRubrikOperationStateChanged):
		return next, ""
	}
	h.logger.Error("rubrik operation: recording the refresh failed", "operation", op.ID, "error", err)
	return op, "store_unavailable"
}

// followJob reads the state of a snapshot's job: from its taskchain when it
// has one, otherwise from the object's activity series around the time the
// operation started. An empty update means nothing changed.
func (h *Handler) followJob(ctx context.Context, op store.RubrikOperation) (store.RubrikOperationUpdate, error) {
	if op.JobFamily == store.RubrikJobTaskchain && op.JobHandle != "" {
		st, err := h.writer.Taskchain(ctx, op.JobHandle)
		if err != nil {
			return store.RubrikOperationUpdate{}, err
		}
		switch st.Status {
		case JobRunning, JobUnknown:
			return store.RubrikOperationUpdate{}, nil
		case JobSucceeded:
			// A taskchain has no partial state; the activity series says
			// whether the job only partly succeeded. That is read only when
			// one job can be the operation's: with several, the taskchain's
			// own verdict stands.
			jobs, err := h.operationJobs(ctx, op)
			if err != nil {
				return store.RubrikOperationUpdate{}, err
			}
			if len(jobs) == 1 && jobs[0].Status == JobPartial {
				return store.RubrikOperationUpdate{State: store.RubrikOpPartial, FailureMessage: capMessage(jobs[0].FailureReason)}, nil
			}
			return store.RubrikOperationUpdate{State: store.RubrikOpSucceeded}, nil
		case JobCanceled:
			return store.RubrikOperationUpdate{State: store.RubrikOpFailed, FailureMessage: msgCanceled}, nil
		}
		return store.RubrikOperationUpdate{State: store.RubrikOpFailed, FailureMessage: capMessage(st.Error)}, nil
	}

	jobs, err := h.operationJobs(ctx, op)
	if err != nil {
		return store.RubrikOperationUpdate{}, err
	}
	if len(jobs) == 0 {
		if op.State == store.RubrikOpUnknown && time.Since(op.UpdatedAt) > noJobGrace {
			return store.RubrikOperationUpdate{State: store.RubrikOpFailed, FailureMessage: msgNoJob}, nil
		}
		return store.RubrikOperationUpdate{}, nil
	}
	if len(jobs) > 1 {
		// Another job on the object started in the same window (a
		// scheduled backup, another snapshot), so none of them can be told
		// to be this operation's. It stays as it is; nothing is guessed.
		return store.RubrikOperationUpdate{}, nil
	}
	job := jobs[0]
	switch job.Status {
	case JobSucceeded:
		return store.RubrikOperationUpdate{State: store.RubrikOpSucceeded}, nil
	case JobPartial:
		return store.RubrikOperationUpdate{State: store.RubrikOpPartial, FailureMessage: capMessage(job.FailureReason)}, nil
	case JobFailed:
		return store.RubrikOperationUpdate{State: store.RubrikOpFailed, FailureMessage: capMessage(job.FailureReason)}, nil
	case JobCanceled:
		return store.RubrikOperationUpdate{State: store.RubrikOpFailed, FailureMessage: msgCanceled}, nil
	}
	// Still running. An unknown operation whose job is now seen running is
	// running.
	if op.State == store.RubrikOpUnknown {
		return store.RubrikOperationUpdate{State: store.RubrikOpRunning}, nil
	}
	return store.RubrikOperationUpdate{}, nil
}

// operationJobs returns the jobs that may be the one the operation's
// mutation started: jobs of the operation's own object (RSC's answer is
// filtered again, as job history is, so a wrongly scoped answer never
// reaches the operation) that started within the window around its
// creation. The mutation was sent after the row was written, so the window
// opens a clock skew before created_at and closes noJobGrace after it.
func (h *Handler) operationJobs(ctx context.Context, op store.RubrikOperation) ([]Job, error) {
	from := op.CreatedAt.Add(-jobStartSkew)
	until := op.CreatedAt.Add(noJobGrace)
	jobs, err := h.writer.Jobs(ctx, ActivityFilter{ObjectIDs: []string{op.RSCObjectID}, StartedAfter: &from})
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, 1)
	for _, j := range jobs {
		if !strings.EqualFold(j.ObjectID, op.RSCObjectID) || j.StartTime == nil ||
			j.StartTime.Before(from) || j.StartTime.After(until) {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

// settleSLA settles an SLA change of unknown outcome from RSC's object list.
// A list fetched after the attempt that shows the requested SLA proves the
// change was applied. A list showing anything else proves it was not only
// once the change has had noJobGrace to land: a 5xx or timeout may still be
// applied late, and a fetch that finished just after the row was updated
// may have read the object before the mutation. Any fetch finishing
// noJobGrace after the update started after it, since a fetch is bounded by
// fetchTimeout, well inside that grace, which also absorbs clock skew
// between k8sCenter and PostgreSQL.
func (h *Handler) settleSLA(ctx context.Context, op store.RubrikOperation) (store.RubrikOperationUpdate, error) {
	var rec opRequest
	if err := json.Unmarshal(op.Request, &rec); err != nil {
		return store.RubrikOperationUpdate{}, nil
	}
	t, err := h.service.Resolve(ctx, op.ClusterID)
	if err != nil {
		return store.RubrikOperationUpdate{}, err
	}
	view, err := h.service.Objects(ctx, t)
	if err != nil {
		return store.RubrikOperationUpdate{}, err
	}
	if view.Freshness.Err != nil || !view.FetchedAt.After(op.UpdatedAt) {
		return store.RubrikOperationUpdate{}, nil
	}
	o, ok := view.Object(op.RSCObjectID)
	if !ok {
		return store.RubrikOperationUpdate{}, nil
	}
	applied := strings.EqualFold(o.ConfiguredSLA.ID, rec.SLAID)
	if rec.SLAID == "" {
		applied = !o.ConfiguredSLA.Protects()
	}
	switch {
	case applied:
		return store.RubrikOperationUpdate{State: store.RubrikOpSucceeded}, nil
	case view.FetchedAt.After(op.UpdatedAt.Add(noJobGrace)):
		return store.RubrikOperationUpdate{State: store.RubrikOpFailed, FailureMessage: msgSLANotApplied}, nil
	}
	return store.RubrikOperationUpdate{}, nil
}

// ---------------------------------------------------------------------------
// Helpers

// parseOperationID accepts exactly the 36-character hyphenated UUID form, so
// one id cannot be written several ways, and returns it in lower case.
func parseOperationID(raw string) (string, bool) {
	if len(raw) != 36 {
		return "", false
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return "", false
	}
	return id.String(), true
}

// validateWriteRequest checks the body's shape; it returns a message for a
// bad one.
func validateWriteRequest(k writeKind, req writeRequest) string {
	if req.ObjectID != "" && !validUUID(req.ObjectID) {
		return "objectId must be a UUID"
	}
	if k.kind == store.RubrikOpSLAAssign && req.SLAID == nil {
		return `slaId is required; send "" to remove protection`
	}
	if req.SLAID != nil && *req.SLAID != "" && !validUUID(*req.SLAID) {
		return "slaId must be a UUID"
	}
	return ""
}

// requestHash identifies a request, so a retry under the same operation id
// is a replay only when it repeats the request. Object and SLA ids are
// compared case-free.
func requestHash(k writeKind, clusterID, ns string, req writeRequest) string {
	sla := "-"
	if req.SLAID != nil {
		sla = "=" + strings.ToLower(*req.SLAID)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		k.kind, clusterID, ns, strings.ToLower(req.ObjectID), sla,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// errorClass is the fixed class of a read error, for an audit detail.
func errorClass(err error) string {
	switch {
	case errors.Is(err, ErrNotConfigured):
		return StateNotConfigured
	case errors.Is(err, ErrNotMapped):
		return StateNotMapped
	case errors.Is(err, ErrMappingInvalid):
		return StateMappingInvalid
	case errors.Is(err, ErrTargetMissing):
		return "rsc_cluster_missing"
	case errors.Is(err, ErrStoreUnavailable):
		return "store_unavailable"
	}
	_, reason, _ := classifyRSCError(err)
	return reason
}

// refusalStatus maps a CheckAction refusal to an HTTP status.
func refusalStatus(r Refusal) int {
	switch r {
	case RefusalMappingInvalid, RefusalNotActionable:
		return http.StatusConflict
	case RefusalInvalidTarget:
		return http.StatusBadRequest
	}
	return http.StatusForbidden
}

func refusalMessage(r Refusal) string {
	switch r {
	case RefusalWritesDisabled:
		return msgWritesDisabled
	case RefusalMappingInvalid:
		return msgNotSetUp
	case RefusalNotActionable:
		return "this Rubrik object cannot be acted on"
	case RefusalAdminOnly:
		return "only admins can act on this Rubrik object"
	case RefusalInvalidTarget:
		return "invalid target namespace"
	}
	return "you do not have the Rubrik permission for this action"
}

// capMessage bounds RSC job text kept on an operation.
func capMessage(s string) string {
	if len(s) <= maxJobMessage {
		return s
	}
	cut := maxJobMessage
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
