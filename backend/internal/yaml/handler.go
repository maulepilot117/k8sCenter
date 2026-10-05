package yaml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/changes"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// clusterTargeter is the subset of *k8s.ClusterRouter the YAML handlers use.
// It is an interface rather than the concrete router only so remote_test.go
// can inject a remote TargetSchema: from outside package k8s a remote target
// cannot be pointed at a test server (the SSRF policy blocks loopback and
// TargetSchemaFor needs a live cluster store) — the same reason k8s's
// clusterGetter and server's clusterRecordGetter exist. Production always
// assigns a *k8s.ClusterRouter.
type clusterTargeter interface {
	TargetFor(ctx context.Context, clusterID, username string, groups []string) (*k8s.ClientPair, *k8s.TargetSchema, error)
}

// Handler provides HTTP handlers for YAML operations.
type Handler struct {
	ClusterRouter clusterTargeter
	AuditLogger   audit.Logger
	Logger        *slog.Logger
	// Changes runs tracked applies (?trackedOperationId=). Nil, or a service
	// without a receipt store, makes a tracked apply answer 503 having applied
	// nothing; the untracked path never consults it.
	Changes *changes.Service
}

// HandleValidate validates YAML against the cluster's schema using dry-run apply.
// POST /api/v1/yaml/validate
func (h *Handler) HandleValidate(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := readYAMLBody(w, r)
	if err != nil {
		return
	}

	docs, err := ParseMultiDoc(data)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid YAML: "+err.Error(), "")
		return
	}

	// F#7 / F#19 — the client and the RESTMapper both come from the header's
	// cluster in one call, so a CRD that exists only remotely resolves
	// against the cluster the dry-run executes on.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}
	dynClient := pair.Dynamic
	mapper := newReadSchemaRefresh(target).mapper()

	type validationError struct {
		Field   string `json:"field,omitempty"`
		Message string `json:"message"`
	}
	type docResult struct {
		Index     int               `json:"index"`
		Kind      string            `json:"kind"`
		Name      string            `json:"name"`
		Namespace string            `json:"namespace,omitempty"`
		Valid     bool              `json:"valid"`
		Errors    []validationError `json:"errors,omitempty"`
	}
	type validateResponse struct {
		Documents []docResult `json:"documents"`
		Valid     bool        `json:"valid"`
		targetPin
	}

	resp := validateResponse{
		Documents: make([]docResult, 0, len(docs)),
		Valid:     true,
		targetPin: pinFor(target),
	}

	for i, obj := range docs {
		dr := docResult{
			Index:     i,
			Kind:      obj.GetKind(),
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
			Valid:     true,
		}

		// Dry-run apply validates against the cluster's schema
		diffResp := DiffDocuments(r.Context(), dynClient, mapper, docs[i:i+1], h.Logger)
		if len(diffResp.Documents) > 0 && diffResp.Documents[0].Error != "" {
			dr.Valid = false
			dr.Errors = []validationError{{
				Message: diffResp.Documents[0].Error,
			}}
			resp.Valid = false
		}

		resp.Documents = append(resp.Documents, dr)
	}

	httputil.WriteData(w, resp)
}

// HandleApply applies YAML via server-side apply.
// POST /api/v1/yaml/apply?force=true[&trackedOperationId=<uuidv4>[&repairOf=<uuidv4>]]
//
// Without trackedOperationId the request takes the legacy path, byte for
// byte. With it, the apply runs through changes.Service.TrackedApply (plan
// D3/D4): intent is recorded before the cluster is touched, each outcome as
// it lands, and a retry under the same id replays the recorded outcome
// instead of applying again. The response then carries an additive
// data.tracking block; results and summary keep their legacy meaning.
func (h *Handler) HandleApply(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := readYAMLBody(w, r)
	if err != nil {
		return
	}

	docs, err := ParseMultiDoc(data)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid YAML: "+err.Error(), "")
		return
	}

	query := r.URL.Query()
	force := query.Get("force") == "true"

	// Opt-in tracked execution. Presence, not value, opts in: an empty or
	// malformed id is refused rather than silently applied untracked. Input
	// and availability are both settled before the pin checks and routing, so
	// a refusal here has touched no cluster.
	var tracked *trackedParams
	if query.Has(trackedOperationIDParam) {
		tp, ok := parseTrackedParams(w, query)
		if !ok {
			return
		}
		if !h.Changes.Available() {
			writeRecordingUnavailable(w, recordingStepNoStore)
			return
		}
		tracked = &tp
	}

	// D4 / AE2 — a pinned apply runs only against the cluster the operator
	// previewed. The cluster half of the pin is checked before routing, so a
	// mismatch costs no remote round trip; the generation half needs the
	// resolved target. Both refusals happen before any document is applied.
	clusterID := middleware.ClusterIDFromContext(r.Context())
	pin := parseTargetPin(query)
	if pin.TargetCluster != "" && k8s.NormalizedClusterID(pin.TargetCluster) != k8s.NormalizedClusterID(clusterID) {
		h.refusePin(w, r, user, clusterID, "the cluster you previewed is not the cluster this request targets",
			"cluster_pin_mismatch", map[string]any{
				"pinnedClusterId":  k8s.NormalizedClusterID(pin.TargetCluster),
				"requestClusterId": k8s.NormalizedClusterID(clusterID),
			})
		return
	}

	// F#7 / F#19 — see HandleValidate.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}
	if pin.TargetGeneration != "" && pin.TargetGeneration != target.Generation {
		h.refusePin(w, r, user, clusterID, "the cluster you previewed has been re-registered since the preview",
			"cluster_generation_mismatch", map[string]any{
				"pinnedGeneration": pin.TargetGeneration,
				"targetGeneration": target.Generation,
			})
		return
	}
	dynClient := pair.Dynamic
	mapper := newApplySchemaRefresh(target).mapper()

	if tracked != nil {
		h.applyTracked(w, r, user, target, trackedApplyEngine(r.Context(), dynClient, mapper, docs, force, h.Logger),
			changes.TrackedApplyRequest{
				OperationID: tracked.operationID,
				RepairOf:    tracked.repairOf,
				User:        user,
				ClusterID:   target.ClusterID,
				ClusterGen:  target.Generation,
				RawBody:     data,
				Docs:        docs,
				Force:       force,
			})
		return
	}

	resp := ApplyDocuments(r.Context(), dynClient, mapper, docs, force, h.Logger)
	h.auditApplyResults(r, user, target.ClusterID, resp.Results, "")
	httputil.WriteData(w, resp)
}

// trackedOperationIDParam opts an apply into tracked execution.
const trackedOperationIDParam = "trackedOperationId"

// repairOfParam links a tracked apply to the receipt it repairs. Recorded
// only; it grants nothing, and an untracked request ignores it as before.
const repairOfParam = "repairOf"

type trackedParams struct {
	operationID uuid.UUID
	repairOf    *uuid.UUID
}

// parseTrackedParams validates the tracked-apply query parameters and writes
// the 400 itself on failure.
func parseTrackedParams(w http.ResponseWriter, q url.Values) (trackedParams, bool) {
	opID, ok := parseUUIDv4(q.Get(trackedOperationIDParam))
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "invalid trackedOperationId",
			"trackedOperationId must be a UUIDv4 in canonical 8-4-4-4-12 form")
		return trackedParams{}, false
	}
	tp := trackedParams{operationID: opID}
	if q.Has(repairOfParam) {
		repairOf, ok := parseUUIDv4(q.Get(repairOfParam))
		if !ok {
			httputil.WriteError(w, http.StatusBadRequest, "invalid repairOf",
				"repairOf must be the UUIDv4 operation id of the receipt being repaired")
			return trackedParams{}, false
		}
		tp.repairOf = &repairOf
	}
	return tp, true
}

// parseUUIDv4 accepts only the 36-character 8-4-4-4-12 form of a version 4
// UUID (uuid.Parse alone also takes braced, urn: and dashless spellings).
// Letter case is accepted and canonicalized: from here on the operation is
// identified by the parsed uuid.UUID, so the receipt id, tracking.operationId,
// receiptUrl and the audit "op=" all carry its lowercase String() form, and
// an uppercase and a lowercase spelling of one id are the same operation.
func parseUUIDv4(s string) (uuid.UUID, bool) {
	if len(s) != 36 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil || id.Version() != 4 {
		return uuid.Nil, false
	}
	return id, true
}

// trackedApplyEngine adapts ApplyDocumentsObserved to changes.ApplyFunc.
// Every document the engine attempts is reported in Attempted, including one
// whose recording then fails (it was sent to the cluster); the recorder's
// error stops the engine. The engine's own ApplyResponse is not used: the
// service assembles the results from Attempted so the response, the receipt
// and the not-attempted accounting cannot disagree.
func trackedApplyEngine(ctx context.Context, dynClient dynamic.Interface, mapper meta.RESTMapper,
	docs []*unstructured.Unstructured, force bool, logger *slog.Logger,
) changes.ApplyFunc {
	return func(record changes.ApplyObserverFunc) changes.TrackedApplyOutcome {
		var out changes.TrackedApplyOutcome
		ApplyDocumentsObserved(ctx, dynClient, mapper, docs, force, logger, func(_ context.Context, obs ApplyObservation) error {
			co := changeObservation(obs)
			out.Attempted = append(out.Attempted, co)
			if err := record(co); err != nil {
				out.Stopped = err
				return err
			}
			return nil
		})
		return out
	}
}

// changeObservation converts the engine's observation into the changes
// package's plain-data form.
func changeObservation(obs ApplyObservation) changes.ApplyObservation {
	co := changes.ApplyObservation{
		Index:      obs.Result.Index,
		Kind:       obs.Result.Kind,
		Namespace:  obs.Result.Namespace,
		Name:       obs.Result.Name,
		UID:        string(obs.UID),
		Action:     obs.Result.Action,
		Error:      obs.Result.Error,
		ErrorClass: obs.ErrorClass,
	}
	if obs.Mapping != nil {
		co.Group = obs.Mapping.Resource.Group
		co.Version = obs.Mapping.Resource.Version
		co.Resource = obs.Mapping.Resource.Resource
	}
	return co
}

// applyTracked runs one tracked apply and writes its response.
//
// Audit: a live tracked apply writes exactly the entries an untracked one
// does (one per result, same fields), with the operation id appended to
// Detail. A replay applied nothing, so it writes no apply entries: the
// original request already audited every object it touched. An idempotency
// refusal (409) is audited once, like a pin refusal, because a reused or
// foreign operation id is what an operator looks for. A 400 or 503 attempted
// nothing and is not audited, matching the other input and availability
// refusals on this endpoint.
//
// Secrets: the live response carries the engine's error text unchanged, as
// the untracked path does; the service stores only a digest, references and
// (for a Secret-bearing bundle) sanitized error classes.
//
// Reading the result: results and summary describe what was applied, and
// tracking describes what was recorded. They can legitimately disagree in
// emphasis: when recording fails on the last document, or finalization fails,
// summary.failed can be 0 while tracking.state is unknown with a warning. A
// replay's error text comes from the receipt (sanitized for Secret bundles,
// truncated otherwise). Tracked clients key off action and tracking, never
// results[].error prose or summary alone.
//
// Which component owns "documents never sent are reported failed": the
// service (changes.assembleResults) builds the tracked response. The engine's
// own fill-in in ApplyDocumentsObserved is what keeps that exported function's
// summary.total == len(docs) contract for any caller, and uses the same
// constants under the same rule (NotAppliedError after a recording failure,
// NotAttemptedError after the request context ended); tracked_apply_test.go
// pins that the two agree.
func (h *Handler) applyTracked(w http.ResponseWriter, r *http.Request, user *auth.User, target *k8s.TargetSchema,
	engine changes.ApplyFunc, req changes.TrackedApplyRequest,
) {
	opID := req.OperationID.String()
	res, err := h.Changes.TrackedApply(r.Context(), req, engine)
	if err != nil {
		h.writeTrackedApplyError(w, r, user, target.ClusterID, opID, err)
		return
	}

	resp := &ApplyResponse{Results: make([]ApplyResult, 0, len(res.Results))}
	for _, o := range res.Results {
		resp.Results = append(resp.Results, ApplyResult{
			Index:     o.Index,
			Kind:      o.Kind,
			Name:      o.Name,
			Namespace: o.Namespace,
			Action:    o.Action,
			Error:     o.Error,
		})
	}
	c := res.Counts()
	resp.Summary = ApplySummary{
		Total: c.Total, Created: c.Created, Configured: c.Configured, Unchanged: c.Unchanged, Failed: c.Failed,
	}
	tracking := res.Tracking
	resp.Tracking = &tracking

	if !tracking.Replayed {
		h.auditApplyResults(r, user, target.ClusterID, resp.Results, opID)
	}
	httputil.WriteData(w, resp)
}

// recordingStepNoStore is the pseudo-step writeRecordingUnavailable is given
// when there is no receipt store at all (the Available() pre-check). The
// other steps are changes.StoreUnavailableError.Step values.
const recordingStepNoStore = ""

// writeRecordingUnavailable answers a tracked apply that could not be
// recorded. Nothing was applied by THIS request in every case, and extra says
// so explicitly ("applied": false) so clients need not parse the message.
// Whether the client should keep the operation id is carried as
// extra.retrySameOperationId and depends on where recording failed:
//
//   - "insert" -> true. Insert reports a collision only on an actual
//     unique violation, so an unreachable or timed-out database surfaces here
//     even when this request is a RETRY of a send that already applied.
//     Reusing the id is always safe: if the insert never committed, the retry
//     proceeds normally; if it did, the retry is told the operation is in
//     flight and then replays it. A new id could apply twice.
//   - "read" -> true. Reading the existing receipt failed while resolving an
//     id collision, i.e. on a retry; the original may have been applied.
//   - "mark" -> false. The row was inserted by this very request (a mark
//     failure cannot happen on a retry, which collides at insert) and is
//     finalized as failed, so the id is spent: a same-id retry would only
//     replay that failure. Start a new attempt or apply untracked.
//   - no store -> false. Tracking is not configured; the id is irrelevant.
//     Apply without tracking.
func writeRecordingUnavailable(w http.ResponseWriter, step string) {
	var message string
	var retrySame bool
	switch step {
	case "insert":
		message = "change recording is unavailable; nothing was applied by this request. Retry with the same operation id or apply without tracking"
		retrySame = true
	case "read":
		message = "could not read the existing record for this operation; retry with the same operation id"
		retrySame = true
	case recordingStepNoStore:
		message = "change recording is not configured; nothing was applied. Apply without tracking"
	default: // "mark", or any step this handler does not know: never reuse
		message = "change recording is unavailable; nothing was applied. Start a new attempt or apply without tracking"
	}
	httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, message, changes.ReasonReceiptStoreUnavailable,
		map[string]any{"applied": false, "retrySameOperationId": retrySame})
}

// writeTrackedApplyError maps TrackedApply's typed errors. Every one of them
// means no document was applied by this request.
func (h *Handler) writeTrackedApplyError(w http.ResponseWriter, r *http.Request, user *auth.User, clusterID, opID string, err error) {
	var conflict *changes.OperationConflictError
	var unavailable *changes.StoreUnavailableError
	switch {
	case errors.As(err, &conflict):
		h.auditRefusal(r, user, clusterID, conflict.Reason+" op="+opID)
		httputil.WriteErrorWithReason(w, http.StatusConflict, conflict.Message, conflict.Reason, conflict.Extra())
	case errors.As(err, &unavailable):
		h.Logger.Error("tracked apply: receipt store unavailable", "operationId", opID, "step", unavailable.Step, "error", err)
		writeRecordingUnavailable(w, unavailable.Step)
	case errors.Is(err, changes.ErrInvalidRequest):
		httputil.WriteError(w, http.StatusBadRequest, "invalid tracked apply request", err.Error())
	default:
		h.Logger.Error("tracked apply failed", "operationId", opID, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "tracked apply failed", "")
	}
}

// auditApplyResults writes one audit entry per applied document. F#6 — the
// cluster is the request's resolved target, not a static per-handler value,
// so the row points at the cluster the apply actually targeted. A tracked
// apply appends its operation id to Detail; every other field is unchanged.
func (h *Handler) auditApplyResults(r *http.Request, user *auth.User, clusterID string, results []ApplyResult, operationID string) {
	for _, result := range results {
		auditResult := audit.ResultSuccess
		if result.Action == "failed" {
			auditResult = audit.ResultFailure
		}
		detail := result.Action
		if operationID != "" {
			detail += " op=" + operationID
		}
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp:         time.Now(),
			ClusterID:         clusterID,
			User:              user.Username,
			SourceIP:          r.RemoteAddr,
			Action:            audit.ActionApply,
			ResourceKind:      result.Kind,
			ResourceNamespace: result.Namespace,
			ResourceName:      result.Name,
			Result:            auditResult,
			Detail:            detail,
		})
	}
}

// HandleDiff performs a dry-run apply and returns current vs proposed YAML.
// POST /api/v1/yaml/diff
func (h *Handler) HandleDiff(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := readYAMLBody(w, r)
	if err != nil {
		return
	}

	docs, err := ParseMultiDoc(data)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid YAML: "+err.Error(), "")
		return
	}

	// Block diff for Secrets to prevent leaking unmasked values
	for _, doc := range docs {
		if strings.EqualFold(doc.GetKind(), "Secret") {
			httputil.WriteError(w, http.StatusUnprocessableEntity,
				"Secrets cannot be diffed via YAML to prevent accidental data exposure. Use the Secrets management interface.",
				"")
			return
		}
	}

	// F#7 / F#19 — see HandleValidate. The Secret refusal above stays ahead
	// of routing so it holds identically for every target.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}

	mapper := newReadSchemaRefresh(target).mapper()
	resp := DiffDocuments(r.Context(), pair.Dynamic, mapper, docs, h.Logger)
	httputil.WriteData(w, struct {
		*DiffResponse
		targetPin
	}{resp, pinFor(target)})
}

// HandleExport exports a resource as clean, reapply-ready YAML.
// GET /api/v1/yaml/export/{kind}/{namespace}/{name}
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	kind := chi.URLParam(r, "kind")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if kind == "" || name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "kind and name are required", "")
		return
	}

	// Validate URL params to prevent injection (matches resource route validation)
	if !resources.ValidateK8sName(name) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid resource name: "+name, "")
		return
	}
	if namespace != "_" && !resources.ValidateK8sName(namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace: "+namespace, "")
		return
	}

	// Block secret export to prevent data loss (D1)
	if strings.EqualFold(kind, "secrets") || strings.EqualFold(kind, "secret") {
		httputil.WriteError(w, http.StatusUnprocessableEntity,
			"Secrets cannot be exported via YAML to prevent accidental data loss. Use the Secrets management interface with audit-logged reveal.",
			"")
		return
	}

	// Use "_" for cluster-scoped resources (no namespace)
	if namespace == "_" {
		namespace = ""
	}

	// F#7 / F#19 — the GVR resolves through the header cluster's discovery,
	// so a CRD that exists only remotely is visible to the export.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}
	dynClient := pair.Dynamic

	refresh := newReadSchemaRefresh(target)
	gvr, err := resolveGVR(target.Discovery, kind)
	if err != nil && refresh.once() {
		gvr, err = resolveGVR(target.Discovery, kind)
	}
	if errors.Is(err, errKindNotServed) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("unknown resource kind: %s", kind), err.Error())
		return
	}
	if err != nil {
		// Discovery failed or came back incomplete, so the kind can be
		// neither confirmed nor ruled out: the target cluster is at fault,
		// not the caller's request.
		httputil.WriteError(w, http.StatusBadGateway, "failed to discover resource kinds on the target cluster", err.Error())
		return
	}

	var obj *unstructured.Unstructured
	if namespace != "" {
		obj, err = dynClient.Resource(gvr).Namespace(namespace).Get(r.Context(), name, metav1.GetOptions{})
	} else {
		obj, err = dynClient.Resource(gvr).Get(r.Context(), name, metav1.GetOptions{})
	}
	if err != nil {
		if apierrors.IsNotFound(err) {
			httputil.WriteError(w, http.StatusNotFound, fmt.Sprintf("%s '%s' not found", kind, name), "")
		} else if apierrors.IsForbidden(err) {
			httputil.WriteError(w, http.StatusForbidden, fmt.Sprintf("permission denied for %s '%s'", kind, name), "")
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get %s '%s'", kind, name), err.Error())
		}
		return
	}

	// ?expectUID= lets a caller that already holds an object's identity
	// refuse a same-name replacement: the export strips metadata.uid, so the
	// caller could not tell otherwise. Absent, nothing changes.
	if !uidMatchesExpected(obj, r.URL.Query().Get("expectUID")) {
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			fmt.Sprintf("%s '%s' was replaced by a different object with the same name", kind, name),
			"uid_mismatch", nil)
		return
	}

	yamlBytes, err := ExportToYAML(obj)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to export YAML", err.Error())
		return
	}

	// Return YAML as a string inside the standard JSON envelope so
	// the frontend api() wrapper (which calls res.json()) works correctly.
	httputil.WriteData(w, string(yamlBytes))
}

// --- Helpers ---

// targetPin identifies the cluster schema a validate or diff response was
// computed against. The client stores it so a later apply can refuse to run
// against a different cluster (or a re-registered one under the same ID)
// than the one the operator reviewed.
type targetPin struct {
	TargetCluster    string `json:"targetCluster"`
	TargetGeneration string `json:"targetGeneration"`
}

func pinFor(target *k8s.TargetSchema) targetPin {
	return targetPin{TargetCluster: target.ClusterID, TargetGeneration: target.Generation}
}

// parseTargetPin reads the pin an apply echoes back from its preview. The
// apply body is raw YAML, so the pin travels as query parameters. Absent
// fields mean "unpinned" and keep the pre-pinning contract for existing
// clients, mobile included.
func parseTargetPin(q url.Values) targetPin {
	return targetPin{TargetCluster: q.Get("targetCluster"), TargetGeneration: q.Get("targetGeneration")}
}

// refusePin answers 409 for an apply whose pin disagrees with its target and
// audits the refusal: a refused apply is exactly what an operator looks for
// in the audit trail, and the 409 alone would leave no record.
func (h *Handler) refusePin(w http.ResponseWriter, r *http.Request, user *auth.User, clusterID, message, reason string, extra map[string]any) {
	h.auditRefusal(r, user, clusterID, reason)
	httputil.WriteErrorWithReason(w, http.StatusConflict, message, reason, extra)
}

// auditRefusal records one failed apply entry, with no resource, for an apply
// refused before any document was attempted.
func (h *Handler) auditRefusal(r *http.Request, user *auth.User, clusterID, detail string) {
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp: time.Now(),
		ClusterID: k8s.NormalizedClusterID(clusterID),
		User:      user.Username,
		SourceIP:  r.RemoteAddr,
		Action:    audit.ActionApply,
		Result:    audit.ResultFailure,
		Detail:    detail,
	})
}

// resolveTarget resolves the client pair and schema for the request's
// X-Cluster-ID in a single TargetFor call and writes the error response
// itself on failure. There is no local fallback: a remote target that cannot
// be resolved fails the request.
func (h *Handler) resolveTarget(w http.ResponseWriter, r *http.Request, user *auth.User) (*k8s.ClientPair, *k8s.TargetSchema, bool) {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	pair, target, err := h.ClusterRouter.TargetFor(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return nil, nil, false
	}
	return pair, target, true
}

// applyRESTMappingAttempts mirrors applyOne's RESTMapping retry count in
// applier.go; keep the two in sync. Apply may invalidate a GroupKind's schema
// once per attempt, because discovery on a real API server lags a CRD applied
// earlier in the same bundle and only a post-backoff refresh can see it.
const applyRESTMappingAttempts = 3

// applyMaxInvalidations caps one apply request's invalidations across all
// kinds, so a bundle naming many unserved kinds cannot multiply the per-kind
// budget. Twice the per-kind budget still covers a CRD-then-CR bundle.
const applyMaxInvalidations = 2 * applyRESTMappingAttempts

// schemaRefresh lets a request invalidate a remote target's cached discovery
// on a RESTMapping miss. The cached remote mapper never resets itself on a
// miss (memCacheClient.Fresh stays true once populated), so a CRD installed
// after the cache entry was built would otherwise stay unknown until the
// entry expires. A local target is never refreshed: its discovery is
// process-shared and TargetSchema.Invalidate is inert there.
//
// The budget is chosen by constructor: newReadSchemaRefresh (validate, diff,
// export) invalidates at most once per request; newApplySchemaRefresh
// invalidates once per miss, capped per GroupKind at applyRESTMappingAttempts
// and per request at applyMaxInvalidations.
type schemaRefresh struct {
	target  *k8s.TargetSchema
	apply   bool                     // apply budget; false is the once-per-request read budget
	done    bool                     // read budget spent
	perKind map[schema.GroupKind]int // apply: invalidations per GroupKind
	total   int                      // apply: invalidations this request
}

// newReadSchemaRefresh returns the once-per-request budget.
func newReadSchemaRefresh(target *k8s.TargetSchema) *schemaRefresh {
	return &schemaRefresh{target: target}
}

// newApplySchemaRefresh returns the per-attempt apply budget.
func newApplySchemaRefresh(target *k8s.TargetSchema) *schemaRefresh {
	return &schemaRefresh{target: target, apply: true, perKind: make(map[schema.GroupKind]int)}
}

// once invalidates the remote target's schema under the once-per-request
// budget if this request has not done so yet, and reports whether it did (so
// the caller should retry). Called directly by HandleExport's resolveGVR
// fallback, which has no GroupKind to key a per-kind budget by, and via
// invalidateOnMiss for every read-policy schemaRefresh.
func (s *schemaRefresh) once() bool {
	if s.target.IsLocal || s.done {
		return false
	}
	s.done = true
	s.target.Invalidate()
	return true
}

// invalidateOnMiss invalidates the schema for a RESTMapping miss on gk under
// whichever budget this schemaRefresh was constructed with, and reports
// whether it did (so the caller should retry). Under the apply policy, both
// the per-GroupKind and the request-wide counters are charged for every
// invalidation, and either being exhausted refuses the next one.
func (s *schemaRefresh) invalidateOnMiss(gk schema.GroupKind) bool {
	if !s.apply {
		return s.once()
	}
	if s.target.IsLocal || s.perKind[gk] >= applyRESTMappingAttempts || s.total >= applyMaxInvalidations {
		return false
	}
	s.perKind[gk]++
	s.total++
	s.target.Invalidate()
	return true
}

// mapper returns the target's RESTMapper, wrapped for a remote target so a
// no-match refreshes the schema (under whichever budget this schemaRefresh
// was constructed with) and retries.
func (s *schemaRefresh) mapper() meta.RESTMapper {
	if s.target.IsLocal {
		return s.target.Mapper
	}
	return refreshingMapper{RESTMapper: s.target.Mapper, refresh: s}
}

// refreshingMapper retries RESTMapping once per call after a schema refresh.
// Only RESTMapping is wrapped: it is the one method DiffDocuments and
// applyOne use. For apply, applyOne's own outer retry loop supplies the
// repeated calls that let the per-GroupKind budget span multiple attempts.
type refreshingMapper struct {
	meta.RESTMapper
	refresh *schemaRefresh
}

func (m refreshingMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	mapping, err := m.RESTMapper.RESTMapping(gk, versions...)
	if meta.IsNoMatchError(err) && m.refresh.invalidateOnMiss(gk) {
		return m.RESTMapper.RESTMapping(gk, versions...)
	}
	return mapping, err
}

// uidMatchesExpected reports whether obj is the object the caller expects.
// An empty expectation matches anything, so callers that do not send one
// keep the endpoint's original behaviour.
func uidMatchesExpected(obj *unstructured.Unstructured, expected string) bool {
	return expected == "" || string(obj.GetUID()) == expected
}

// readYAMLBody reads and validates the raw YAML body from the request.
func readYAMLBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodySize)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			httputil.WriteError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds maximum size of %d bytes", MaxBodySize), "")
		} else {
			httputil.WriteError(w, http.StatusBadRequest, "failed to read request body", err.Error())
		}
		return nil, err
	}

	if err := CheckSecurity(data); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return nil, err
	}

	return data, nil
}

// errKindNotServed means discovery answered completely and no API group
// serves the requested resource: the caller named a kind that does not exist.
var errKindNotServed = errors.New("resource not served by the API server")

// resolveGVR resolves a plural resource name to a GroupVersionResource using
// the TARGET cluster's discovery. Passing the local ClientFactory's discovery
// here is the bug this signature change exists to prevent.
//
// A miss wraps errKindNotServed only when discovery was complete. When some
// API groups failed to load, a miss cannot tell "not installed" from "in a
// group we never saw", so it returns the discovery error instead.
func resolveGVR(disc discovery.DiscoveryInterface, kind string) (schema.GroupVersionResource, error) {
	kind = strings.ToLower(kind)

	_, apiResourceLists, discErr := disc.ServerGroupsAndResources()
	if discErr != nil && apiResourceLists == nil {
		// ServerGroupsAndResources may return partial results with an error
		// for unavailable API groups. Only fail outright if nothing loaded.
		return schema.GroupVersionResource{}, fmt.Errorf("discovering API resources: %w", discErr)
	}

	for _, list := range apiResourceLists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.EqualFold(r.Name, kind) {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name,
				}, nil
			}
		}
	}

	if discErr != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("resource %q not found and discovery was incomplete: %w", kind, discErr)
	}
	return schema.GroupVersionResource{}, fmt.Errorf("resource %q: %w", kind, errKindNotServed)
}
