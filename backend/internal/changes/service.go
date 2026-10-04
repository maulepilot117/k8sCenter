package changes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/store"
)

// receiptStore is the slice of *store.ChangeReceiptStore this package uses.
// Narrow and unexported so service tests run against an in-memory fake that
// injects a failure at any D3 step; the production constructor takes the
// concrete store.
type receiptStore interface {
	Insert(ctx context.Context, r store.ChangeReceipt) error
	MarkMutationStarted(ctx context.Context, id uuid.UUID) error
	AppendObject(ctx context.Context, id uuid.UUID, o store.ReceiptObject) error
	Finalize(ctx context.Context, id uuid.UUID, state store.ReceiptState) error
	SetVerification(ctx context.Context, id uuid.UUID, state store.VerificationState, payload json.RawMessage) error
	Get(ctx context.Context, id uuid.UUID) (*store.ChangeReceipt, error)
}

// recordWriteTimeout bounds each receipt write made after the cluster was
// touched. Those writes run on a context detached from the request's
// cancellation: a client that hangs up mid-apply must not leave the receipt
// lying about what happened, and the writes are small. The bound keeps a dead
// database from pinning the request goroutine.
const recordWriteTimeout = 5 * time.Second

// maxStoredErrorLen caps the per-object error text persisted for a non-Secret
// bundle. Admission messages can be long; the receipt needs the gist.
const maxStoredErrorLen = 1024

// Service coordinates tracked applies and their verification. It holds no
// Kubernetes client: the apply engine and the verification reads are supplied
// per call, already routed to the target cluster under the caller's identity.
type Service struct {
	receipts receiptStore
	logger   *slog.Logger
	now      func() time.Time
}

// NewService builds the service. receipts may be nil (no PostgreSQL); the
// service then reports Available() == false and every call fails closed.
func NewService(receipts *store.ChangeReceiptStore, logger *slog.Logger) *Service {
	s := &Service{logger: logger, now: time.Now}
	if receipts != nil {
		s.receipts = receipts
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

// newServiceWith is the test seam: any receiptStore and any clock.
func newServiceWith(receipts receiptStore, logger *slog.Logger, now func() time.Time) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Service{receipts: receipts, logger: logger, now: now}
}

// Available reports whether tracked execution can run at all. False when there
// is no receipt store; callers return 503 and leave the untracked path alone.
func (s *Service) Available() bool {
	return s != nil && s.receipts != nil
}

// TrackedApply runs the durable-intent protocol (plan D3):
//
//  1. validate, digest the raw body, detect Secrets; nothing persisted
//  2. Insert the receipt (state applying)       -> abort 503, nothing applied
//  3. MarkMutationStarted                       -> abort 503, nothing applied
//     (the row is finalized as failed on a best-effort basis so the id does
//     not sit in flight until reconciliation)
//  4. apply, recording each outcome as it lands -> a recording failure stops
//     the engine; the rest are reported as never attempted
//  5. Finalize                                  -> a failure is reported as
//     state unknown plus a warning, never as an error
//
// An Insert collision is the idempotency protocol (plan D4): the row decides
// whether this is a replay, an in-flight duplicate, a reused id or another
// owner's id. apply is called at most once, and never on any of those
// branches. The returned Results always has len(req.Docs) entries.
//
// TrackedApply writes no audit entries; the yaml handler's per-result audit
// loop stays the single source of apply audit rows.
func (s *Service) TrackedApply(ctx context.Context, req TrackedApplyRequest, apply ApplyFunc) (*TrackedApplyResult, error) {
	if !s.Available() {
		return nil, &StoreUnavailableError{Step: "insert", Err: errors.New("no receipt store configured")}
	}
	if err := validateRequest(req, apply); err != nil {
		return nil, err
	}
	clusterID := k8s.NormalizedClusterID(req.ClusterID)
	digest := computeDigest(req.RawBody)
	containsSecret := bundleContainsSecret(req.Docs)

	// Step 2: record intent.
	err := s.receipts.Insert(ctx, store.ChangeReceipt{
		ID:                req.OperationID,
		OwnerID:           req.User.ID,
		OwnerUsername:     req.User.Username,
		ClusterID:         clusterID,
		ClusterGeneration: req.ClusterGen,
		ContentDigest:     digest,
		DocumentCount:     len(req.Docs),
		Force:             req.Force,
		ContainsSecret:    containsSecret,
		RepairOf:          req.RepairOf,
		State:             store.ReceiptApplying,
		Ownership:         json.RawMessage(req.Ownership),
	})
	switch {
	case errors.Is(err, store.ErrReceiptExists):
		return s.resolveExisting(ctx, req, clusterID, digest)
	case errors.Is(err, store.ErrReceiptInvalid):
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	case err != nil:
		return nil, &StoreUnavailableError{Step: "insert", Err: err}
	}

	// Step 3: open the mutation window. This stamp is what tells a crash
	// before the cluster was touched from a crash during the apply.
	switch err := s.receipts.MarkMutationStarted(ctx, req.OperationID); {
	case errors.Is(err, store.ErrReceiptAlreadyFinal):
		// Reconciled between insert and mark. Nothing was applied; the id is
		// spent.
		return nil, &OperationConflictError{
			Reason:  ReasonOperationIDReused,
			Message: "this operation id was already finalized; generate a new operation id",
		}
	case err != nil:
		// Nothing was applied. Close the row as failed so a same-id retry is
		// told that (a replay of a failed receipt) instead of being told the
		// operation is in flight until ReconcileOrphans reaps it. Best effort:
		// if the store is down this fails too and reconciliation takes over.
		if ferr := s.finalize(ctx, req.OperationID, store.ReceiptFailed); ferr != nil {
			s.logger.Warn("tracked apply: could not close never-started receipt",
				"operationId", req.OperationID, "error", ferr)
		}
		return nil, &StoreUnavailableError{Step: "mark", Err: err}
	}

	// Step 4: apply, recording as we go.
	rec := &recorder{
		svc: s, parent: ctx, id: req.OperationID, containsSecret: containsSecret, docCount: len(req.Docs),
		refs: []TrackedObjectRef{}, // never null on the wire
	}
	outcome := apply(rec.observe)

	// A document the engine never reached is "not applied" either because
	// recording failed (D5 wording) or because the engine stopped on its own.
	notReached := NotAttemptedError
	if rec.err != nil {
		notReached = NotAppliedError
	}
	results, notAttempted := assembleResults(req.Docs, outcome.Attempted, notReached)
	warnings := []string{}
	var finalState store.ReceiptState
	// Recording failed when an append errored, or when the engine attempted
	// more than was recorded (it skipped the observer for a document). Either
	// way the receipt is incomplete about something that was attempted.
	recordingFailed := rec.err != nil || rec.recorded < len(outcome.Attempted)
	if recordingFailed {
		// The receipt holds a truthful prefix; the cluster may hold more. That
		// is exactly what "unknown" means, and it is what reconciliation would
		// have written had the process died here.
		finalState = store.ReceiptUnknown
		warnings = append(warnings, fmt.Sprintf(
			"change recording failed after %d recorded object(s); outcomes after that are not in the receipt", rec.recorded))
		s.logger.Warn("tracked apply: change recording failed",
			"operationId", req.OperationID, "recorded", rec.recorded,
			"attempted", len(outcome.Attempted), "error", firstErr(rec.err, outcome.Stopped))
	} else {
		finalState = stateFor(results)
	}

	// Step 5: finalize. Failure here is reported, not returned: the caller
	// still gets the truthful per-document outcome, and the row stays
	// recovery-visible (completed_at IS NULL) for ReconcileOrphans.
	reportedState := finalState
	if err := s.finalize(ctx, req.OperationID, finalState); err != nil {
		reportedState = store.ReceiptUnknown
		if errors.Is(err, store.ErrReceiptAlreadyFinal) {
			warnings = append(warnings, "receipt was finalized by reconciliation while the apply was running")
		} else {
			warnings = append(warnings, "receipt finalization failed")
		}
		s.logger.Error("tracked apply: finalize failed",
			"operationId", req.OperationID, "state", finalState, "error", err)
	}

	tracking := ApplyTracking{
		OperationID:       req.OperationID.String(),
		ReceiptURL:        receiptURL(req.OperationID),
		State:             reportedState,
		ClusterID:         clusterID,
		ClusterGeneration: req.ClusterGen,
		ContentDigest:     digest,
		RecordedThrough:   rec.recorded,
		NotAttempted:      notAttempted,
		ContainsSecret:    containsSecret,
		Objects:           rec.refs,
		Verification:      VerificationLink{State: store.VerifyPending, URL: verificationURL(req.OperationID)},
		Warnings:          warnings,
	}
	if req.RepairOf != nil {
		tracking.RepairOf = req.RepairOf.String()
	}
	return &TrackedApplyResult{Results: results, Tracking: tracking}, nil
}

// validateRequest rejects input the protocol cannot act on. Everything here
// is a 400: the row does not exist yet and nothing was applied.
func validateRequest(req TrackedApplyRequest, apply ApplyFunc) error {
	switch {
	case req.OperationID == uuid.Nil:
		return fmt.Errorf("%w: operation id is required", ErrInvalidRequest)
	case req.OperationID.Version() != 4:
		return fmt.Errorf("%w: operation id must be a UUIDv4", ErrInvalidRequest)
	case req.User == nil || req.User.ID == "":
		return fmt.Errorf("%w: authenticated user is required", ErrInvalidRequest)
	case len(req.Docs) == 0:
		return fmt.Errorf("%w: no documents to apply", ErrInvalidRequest)
	case apply == nil:
		return fmt.Errorf("%w: apply engine is required", ErrInvalidRequest)
	}
	for i, d := range req.Docs {
		if d == nil {
			return fmt.Errorf("%w: document %d is nil", ErrInvalidRequest, i)
		}
	}
	return nil
}

// resolveExisting is plan D4: the primary key collided, so the stored row
// decides what this request is. apply is never called from here.
//
// Branch order: owner first (nothing else about the row may be disclosed to
// a different owner), then content/cluster (a mismatch is a client bug
// whether or not the row is finished), then in-flight, then replay.
func (s *Service) resolveExisting(ctx context.Context, req TrackedApplyRequest, clusterID, digest string) (*TrackedApplyResult, error) {
	existing, err := s.receipts.Get(ctx, req.OperationID)
	if err != nil {
		return nil, &StoreUnavailableError{Step: "read", Err: err}
	}
	if existing == nil {
		// Inserted a moment ago by someone, gone now (retention or a manual
		// delete). Refusing is the only safe answer; the client may retry.
		return nil, &StoreUnavailableError{Step: "read", Err: errors.New("receipt disappeared after insert conflict")}
	}
	if existing.OwnerID != req.User.ID {
		// Nothing about the other owner's receipt is disclosed: not its
		// cluster, digest, state or object names.
		return nil, &OperationConflictError{
			Reason:  ReasonOperationIDConflict,
			Message: "operation id is already in use",
		}
	}
	if existing.ContentDigest != digest || existing.ClusterID != clusterID {
		return nil, &OperationConflictError{
			Reason:  ReasonOperationIDReused,
			Message: "generate a new operation id for new content",
		}
	}
	if !existing.State.IsTerminal() {
		return nil, &OperationConflictError{
			Reason:    ReasonOperationInFlight,
			Message:   "this operation is still in progress; poll the receipt instead of resubmitting",
			ReceiptID: existing.ID,
		}
	}
	return replay(existing, req), nil
}

// replay renders a terminal receipt into the result shape without applying
// anything. Documents the receipt has no outcome for are reported failed, and
// how they are counted depends on what the receipt can prove:
//
//   - state unknown (the original was interrupted after the mutation window
//     opened): the cluster may hold them. They are counted in
//     tracking.unrecorded with NotRecordedError and class indeterminate.
//   - any other terminal state (never started, or finalized by a live process
//     that knew the engine stopped before them): counted in
//     tracking.notAttempted with NotAttemptedError.
//
// Either way the legacy summary reads failed > 0. Error text comes from the
// receipt, so a Secret-bearing replay carries the sanitized class text where
// the live response carried the raw message.
func replay(r *store.ChangeReceipt, req TrackedApplyRequest) *TrackedApplyResult {
	results := make([]ApplyObservation, len(req.Docs))
	recorded := make([]bool, len(req.Docs))
	refs := make([]TrackedObjectRef, 0, len(r.Objects))
	for _, o := range r.Objects {
		if o.Index < 0 || o.Index >= len(results) {
			continue
		}
		results[o.Index] = ApplyObservation{
			Index: o.Index, Group: o.Group, Version: o.Version, Resource: o.Resource,
			Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID,
			Action: o.Action, Error: o.Error, ErrorClass: o.ErrorClass,
		}
		recorded[o.Index] = true
		refs = append(refs, TrackedObjectRef{Index: o.Index, Group: o.Group, Version: o.Version, Resource: o.Resource, UID: o.UID})
	}

	interrupted := r.State == store.ReceiptUnknown
	holes := 0
	for i, d := range req.Docs {
		if recorded[i] {
			continue
		}
		holes++
		results[i] = ApplyObservation{
			Index: i, Kind: d.GetKind(), Namespace: d.GetNamespace(), Name: d.GetName(),
			Action: ActionFailed, Error: NotAttemptedError, ErrorClass: ErrorClassOther,
		}
		if interrupted {
			results[i].Error, results[i].ErrorClass = NotRecordedError, ErrorClassIndeterminate
		}
	}

	warnings := []string{}
	if interrupted && holes > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"the original apply was interrupted; %d document outcome(s) were not recorded and the cluster may hold them", holes))
	}
	if generationChanged(r.ClusterGeneration, req.ClusterGen) {
		warnings = append(warnings, "target cluster registration changed since this operation ran; the receipt describes the previous registration")
	}
	tracking := ApplyTracking{
		OperationID:       r.ID.String(),
		ReceiptURL:        receiptURL(r.ID),
		State:             r.State,
		ClusterID:         r.ClusterID,
		ClusterGeneration: r.ClusterGeneration,
		ContentDigest:     r.ContentDigest,
		RecordedThrough:   len(refs),
		Replayed:          true,
		ContainsSecret:    r.ContainsSecret,
		Objects:           refs,
		Verification:      VerificationLink{State: r.VerificationState, URL: verificationURL(r.ID)},
		Warnings:          warnings,
	}
	if interrupted {
		tracking.Unrecorded = holes
	} else {
		tracking.NotAttempted = holes
	}
	if r.RepairOf != nil {
		tracking.RepairOf = r.RepairOf.String()
	}
	return &TrackedApplyResult{Results: results, Tracking: tracking}
}

// recorder is the step-4 observer state for one TrackedApply call.
type recorder struct {
	svc            *Service
	parent         context.Context
	id             uuid.UUID
	containsSecret bool
	docCount       int

	recorded int                // successful appends
	refs     []TrackedObjectRef // references of the recorded objects, in order
	err      error              // first append failure; sticky
}

// observe is the ApplyObserverFunc. It appends one outcome and returns an
// error (which the engine must honor by stopping) when the append fails. The
// error is sticky: once recording has failed nothing further is recorded, so
// the receipt stays a truthful ordered prefix.
func (r *recorder) observe(obs ApplyObservation) error {
	if r.err != nil {
		return r.err
	}
	if obs.Index < 0 || obs.Index >= r.docCount {
		r.err = fmt.Errorf("apply engine reported document index %d outside 0..%d", obs.Index, r.docCount-1)
		return r.err
	}
	ctx, cancel := r.svc.recordContext(r.parent)
	defer cancel()
	o := receiptObjectFor(obs, r.containsSecret, r.svc.now())
	if err := r.svc.receipts.AppendObject(ctx, r.id, o); err != nil {
		r.err = fmt.Errorf("record change outcome %d: %w", obs.Index, err)
		return r.err
	}
	r.recorded++
	r.refs = append(r.refs, TrackedObjectRef{Index: obs.Index, Group: obs.Group, Version: obs.Version, Resource: obs.Resource, UID: obs.UID})
	return nil
}

// finalize is step 5 on a recording context.
func (s *Service) finalize(parent context.Context, id uuid.UUID, state store.ReceiptState) error {
	ctx, cancel := s.recordContext(parent)
	defer cancel()
	return s.receipts.Finalize(ctx, id, state)
}

// recordContext detaches a receipt write from the request's cancellation and
// bounds it. Values (request id, identity for logging) are kept.
func (s *Service) recordContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), recordWriteTimeout)
}

// receiptObjectFor converts an engine observation into the stored outcome.
// For a Secret-bearing bundle the error text is replaced by its class plus a
// fixed message: admission and validation messages echo submitted values, and
// the store is the one place a Secret value must never reach. The class is
// always kept: the verifier reads it.
func receiptObjectFor(obs ApplyObservation, containsSecret bool, now time.Time) store.ReceiptObject {
	o := store.ReceiptObject{
		Index: obs.Index, Group: obs.Group, Version: obs.Version, Resource: obs.Resource,
		Kind: obs.Kind, Namespace: obs.Namespace, Name: obs.Name, UID: obs.UID,
		Action: obs.Action, RecordedAt: now.UTC(),
	}
	if obs.Action != ActionFailed {
		return o
	}
	o.ErrorClass = obs.ErrorClass
	if o.ErrorClass == "" {
		o.ErrorClass = ErrorClassOther
	}
	if obs.Error == "" {
		return o
	}
	if containsSecret || isSecretKind(obs.Kind) {
		o.Error = sanitizedError(o.ErrorClass)
	} else {
		o.Error = truncate(obs.Error, maxStoredErrorLen)
	}
	return o
}

// sanitizedError is the only error text a Secret-bearing receipt stores.
func sanitizedError(class string) string {
	if class == "" {
		class = ErrorClassOther
	}
	return class + ": error detail withheld for a Secret-bearing change"
}

// assembleResults lays the engine's attempted outcomes over the request's
// documents by index. Every document without an outcome is reported failed
// with notReached as its error text (plan D5) and counted as notAttempted.
// Out-of-range and duplicate indices from the engine are dropped, which
// leaves their documents reported as not attempted rather than trusting a
// second outcome for the same document.
func assembleResults(docs []*unstructured.Unstructured, attempted []ApplyObservation, notReached string) ([]ApplyObservation, int) {
	results := make([]ApplyObservation, len(docs))
	seen := make([]bool, len(docs))
	for _, o := range attempted {
		if o.Index < 0 || o.Index >= len(docs) || seen[o.Index] {
			continue
		}
		results[o.Index] = o
		seen[o.Index] = true
	}
	notAttempted := 0
	for i, d := range docs {
		if seen[i] {
			continue
		}
		notAttempted++
		results[i] = ApplyObservation{
			Index: i, Kind: d.GetKind(), Namespace: d.GetNamespace(), Name: d.GetName(),
			Action: ActionFailed, Error: notReached, ErrorClass: ErrorClassOther,
		}
	}
	return results, notAttempted
}

// stateFor is the terminal state of a fully recorded apply: applied when every
// document succeeded, failed when none did, partial otherwise. Unchanged is a
// success; an unknown action is a failure.
func stateFor(results []ApplyObservation) store.ReceiptState {
	ok, bad := 0, 0
	for _, r := range results {
		switch r.Action {
		case ActionCreated, ActionConfigured, ActionUnchanged:
			ok++
		default:
			bad++
		}
	}
	switch {
	case bad == 0:
		return store.ReceiptApplied
	case ok == 0:
		return store.ReceiptFailed
	default:
		return store.ReceiptPartial
	}
}

// computeDigest is the receipt's only trace of the request body:
// "sha256:" + hex over the exact submitted bytes (the store's documented
// format). It is deliberately unkeyed and is not a guessing oracle for Secret
// values: a tracked apply returns it only to the owner who just submitted
// byte-identical content, and the read path (U29a) renders a receipt only to
// its owner, an explicit grantee or an admin, applying the Q1 Secret filtering
// to what it shows.
func computeDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// bundleContainsSecret reports whether any document is a Secret, matching the
// yaml handler's own case-insensitive kind test.
func bundleContainsSecret(docs []*unstructured.Unstructured) bool {
	for _, d := range docs {
		if d != nil && isSecretKind(d.GetKind()) {
			return true
		}
	}
	return false
}

func isSecretKind(kind string) bool { return strings.EqualFold(kind, "Secret") }

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + "..."
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
