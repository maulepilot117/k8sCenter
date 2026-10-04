// Package changes coordinates tracked applies (Release E).
//
// A tracked apply is an ordinary server-side apply whose intent and per-object
// outcome are recorded durably in change_receipts BEFORE, DURING and AFTER the
// cluster is touched, keyed by a client-supplied operation id. The receipt is
// what lets a client whose response was dropped ask "did my apply happen?" and
// get a truthful answer instead of applying twice.
//
// The package owns three things:
//
//   - Service.TrackedApply: the durable-intent protocol (plan D3), its
//     idempotency branches (D4), and the never-attempted contract (D5). The
//     apply engine itself is injected as a func, so the dependency direction is
//     yaml -> changes, never the reverse, and this package never reimplements
//     server-side apply.
//   - Service.VerifyOnce and CheckRollout: bounded postcondition verification
//     (D6). One live read per verifiable object, under the caller's own
//     impersonated client, from the request goroutine. No background work:
//     no user credential outlives the request that supplied it.
//   - The wire types the HTTP layer renders (D7).
//
// Nothing in this package stores manifest content. A receipt carries a sha256
// digest of the submitted bundle plus object references and outcomes, and a
// Secret-bearing bundle has its per-object error text replaced by a reason
// class before it reaches the store.
//
// This package starts no goroutines and must not: VerifyOnce is stateless
// polling by design, and reconciliation of orphaned receipts is the store's
// (ReconcileOrphans) and the boot path's job.
package changes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/diagnostics"
	"github.com/kubecenter/kubecenter/internal/store"
)

// The verification evidence types are the Release D U20 contract, consumed by
// alias so the JSON persisted in change_receipts.verification is exactly the
// shape diagnostics defines. Release E adds its own reason codes below; the
// reason set is open by contract.
type (
	// CheckResult is one verification check's outcome.
	CheckResult = diagnostics.CheckResult
	// CheckStatus is pass / warn / fail / inconclusive.
	CheckStatus = diagnostics.CheckStatus
	// SourceRef identifies the object a check observed.
	SourceRef = diagnostics.SourceRef
	// Severity is the check's severity.
	Severity = diagnostics.Severity
)

// Re-exported so callers in this package's orbit need not import diagnostics.
const (
	CheckPass         = diagnostics.CheckPass
	CheckWarn         = diagnostics.CheckWarn
	CheckFail         = diagnostics.CheckFail
	CheckInconclusive = diagnostics.CheckInconclusive
)

// CheckIDRolloutComplete is the stable id of the only check this package
// evaluates. It is persisted; never rename it.
const CheckIDRolloutComplete = "workload.rollout-complete"

// Reason codes this package adds to the open U20 set. Stable machine codes;
// the UI branches on them and tests assert them.
const (
	// ReasonRolloutComplete: the workload's rollout postcondition holds.
	ReasonRolloutComplete = diagnostics.ReasonOK
	// ReasonRolloutInProgress: the object was read but the rollout postcondition
	// does not hold yet. Retryable while the verification window is open.
	ReasonRolloutInProgress = "rollout_in_progress"
	// ReasonKindNotSupported: no postcondition is defined for this kind. Never a
	// pass: the receipt does not claim to have verified what it cannot check.
	ReasonKindNotSupported = "kind_not_supported"
	// ReasonNotFound: the object no longer exists.
	ReasonNotFound = "not_found"
	// ReasonTargetRecreated: a same-name object exists with a different UID. The
	// receipt's evidence does not transfer to it.
	ReasonTargetRecreated = "target_recreated"
	// ReasonReadForbidden: the caller may no longer read the object.
	ReasonReadForbidden = "read_forbidden"
	// ReasonWindowExpired: the verification window closed before the
	// postcondition was satisfied. Frozen.
	ReasonWindowExpired = "window_expired"
	// ReasonIdentityUnknown: the receipt holds no UID for the object, so a live
	// read could not be bound to the object that was applied (R1). The object
	// is not read and no live UID is stamped as evidence. Terminal.
	ReasonIdentityUnknown = "identity_unknown"
	// ReasonReadFailed: the live read failed for a reason other than not-found
	// or forbidden. Retryable while the verification window is open.
	ReasonReadFailed = diagnostics.ReasonSourceUnavailable
)

// Error reason codes carried by OperationConflictError and
// StoreUnavailableError; the HTTP layer maps them with
// httputil.WriteErrorWithReason.
const (
	// ReasonOperationIDConflict (409): the operation id belongs to another
	// owner. No detail about that owner's receipt is disclosed.
	ReasonOperationIDConflict = "operation_id_conflict"
	// ReasonOperationInFlight (409): the same owner's apply under this id has
	// not finished; poll the receipt instead of resubmitting.
	ReasonOperationInFlight = "operation_in_flight"
	// ReasonOperationIDReused (409): the same owner submitted different content
	// or a different cluster under an id that was already used.
	ReasonOperationIDReused = "operation_id_reused"
	// ReasonReceiptStoreUnavailable (503): a receipt write failed before or
	// after the cluster was touched; see StoreUnavailableError.Step.
	ReasonReceiptStoreUnavailable = "receipt_store_unavailable"
)

// Error classes recorded for failed objects (store.ReceiptObject.ErrorClass).
// They are what a Secret-bearing receipt keeps instead of the raw error text,
// and what the verifier reads to decide whether a failed object may exist.
const (
	ErrorClassConflict  = "conflict"
	ErrorClassForbidden = "forbidden"
	ErrorClassInvalid   = "invalid"
	ErrorClassNotFound  = "not_found"
	// ErrorClassIndeterminate: the request was cut off (context cancelled or
	// deadline exceeded, a client- or server-side timeout) and the API server
	// MAY have committed it. The legacy action stays "failed" for wire
	// compatibility; VerifyOnce verifies such objects instead of skipping them.
	ErrorClassIndeterminate = "indeterminate"
	ErrorClassOther         = "other"
)

// ClassifyAPIError maps a Kubernetes API error to one of the ErrorClass*
// values. nil maps to "". The apply engine sets ApplyObservation.ErrorClass
// with it so the service never has to parse error prose.
func ClassifyAPIError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return ErrorClassIndeterminate
	case apierrors.IsConflict(err):
		return ErrorClassConflict
	case apierrors.IsForbidden(err):
		return ErrorClassForbidden
	case apierrors.IsInvalid(err):
		return ErrorClassInvalid
	case apierrors.IsNotFound(err):
		return ErrorClassNotFound
	default:
		return ErrorClassOther
	}
}

// Apply actions, as the yaml applier reports them. The set is the legacy
// ApplyResult.Action vocabulary; legacy clients compute success from it.
const (
	ActionCreated    = "created"
	ActionConfigured = "configured"
	ActionUnchanged  = "unchanged"
	ActionFailed     = "failed"
)

// NotAppliedError is the error text of a document the engine never attempted
// because change recording failed first (plan D5). The wording is part of the
// contract: a legacy client sees a failed result, a new client sees
// tracking.notAttempted.
const NotAppliedError = "not applied: change recording failed; re-preview and retry"

// NotAttemptedError is the error text of a document the engine never attempted
// for a reason other than a recording failure (it stopped early, or reported
// fewer documents than it was given). Also counted in tracking.notAttempted.
const NotAttemptedError = "not applied: the apply stopped before reaching this document; re-preview and retry"

// NotRecordedError is the error text used when a replayed receipt lacks an
// outcome for a document: the original apply was interrupted before the
// document's outcome was recorded.
const NotRecordedError = "outcome not recorded: the original apply was interrupted; re-preview and retry"

// ErrInvalidRequest is returned (wrapped, with detail) when TrackedApply or
// VerifyOnce is handed input it cannot act on. Maps to 400.
var ErrInvalidRequest = errors.New("invalid tracked apply request")

// OperationConflictError is a plan D4 idempotency refusal. Maps to HTTP 409
// with Reason as the wire reason code and Extra() as the extra map.
type OperationConflictError struct {
	// Reason is one of ReasonOperationIDConflict, ReasonOperationInFlight,
	// ReasonOperationIDReused.
	Reason string
	// Message is safe to put on the wire as-is.
	Message string
	// ReceiptID is set for ReasonOperationInFlight only; it is the id the client
	// should poll.
	ReceiptID uuid.UUID
}

func (e *OperationConflictError) Error() string {
	return fmt.Sprintf("tracked apply refused (%s): %s", e.Reason, e.Message)
}

// Extra returns the wire extra map, or nil when there is nothing to add.
func (e *OperationConflictError) Extra() map[string]any {
	if e.ReceiptID == uuid.Nil {
		return nil
	}
	return map[string]any{"receiptId": e.ReceiptID.String()}
}

// StoreUnavailableError is returned when a receipt store operation fails and
// nothing else was done as a result. Step names where: "insert", "mark" and
// "read" come from TrackedApply and guarantee no document was applied by that
// call; "verification" comes from VerifyOnce, long after the apply, and means
// the verdict computed in that poll was not persisted (the client re-polls).
// Maps to HTTP 503 with reason ReasonReceiptStoreUnavailable. The underlying
// error is for logs only. A TrackedApply store failure AFTER mutation began
// never surfaces as this error: it is reported inside the result as state
// unknown plus a warning.
type StoreUnavailableError struct {
	Step string
	Err  error
}

func (e *StoreUnavailableError) Error() string {
	return fmt.Sprintf("change receipt store unavailable at %s: %v", e.Step, e.Err)
}

// Unwrap exposes the store error for errors.Is/As.
func (e *StoreUnavailableError) Unwrap() error { return e.Err }

// TrackedApplyRequest is everything TrackedApply needs that the HTTP layer
// already has. The apply engine (dynamic client, RESTMapper, force) is closed
// over by the injected apply func, not carried here.
type TrackedApplyRequest struct {
	// OperationID is the client-supplied UUIDv4 idempotency key and the
	// receipt's primary key.
	OperationID uuid.UUID
	// RepairOf links this apply to the receipt it repairs. Recorded only; it
	// grants nothing.
	RepairOf *uuid.UUID
	// User is the authenticated caller; User.ID becomes the receipt owner.
	User *auth.User
	// ClusterID is the normalized target cluster ("local" or a registry id).
	ClusterID string
	// ClusterGen is the target's generation as the yaml handler resolved it
	// (k8s.TargetSchema.Generation): "local" or the cluster record's created_at.
	ClusterGen string
	// RawBody is the exact submitted bundle. It is used ONLY to compute the
	// content digest and is never retained or stored.
	RawBody []byte
	// Docs are the parsed documents, in submission order. Only kind, name and
	// namespace are read from them (for never-attempted results and the
	// contains-secret flag); the engine applies them.
	Docs []*unstructured.Unstructured
	// Force is recorded on the receipt.
	Force bool
	// Ownership is the optional preview-time ownership snapshot (a JSON array
	// of gitops.OwnershipResult), stored verbatim. Empty stores [].
	Ownership []byte
}

// ApplyObservation is one document's outcome as the apply engine saw it. It is
// plain data so the engine (package yaml) and this package share no types
// beyond it. UID is empty when the apply failed before the object was read.
type ApplyObservation struct {
	Index     int
	Group     string
	Version   string
	Resource  string
	Kind      string
	Namespace string
	Name      string
	UID       string
	// Action is one of the Action* constants.
	Action string
	// Error is the engine's error text for a failed action. It may echo
	// submitted values; the service decides what reaches the store.
	Error string
	// ErrorClass is ClassifyAPIError(err) for a failed action, or "" when the
	// engine could not classify. It is what a Secret-bearing receipt keeps.
	ErrorClass string
}

// ApplyObserverFunc is called by the engine exactly once per document, in
// index order, immediately after that document was applied (or failed). When
// it returns an error the engine MUST stop and attempt no further document;
// the service then reports the rest as never attempted.
type ApplyObserverFunc func(obs ApplyObservation) error

// TrackedApplyOutcome is the engine's report back to TrackedApply.
type TrackedApplyOutcome struct {
	// Attempted holds one observation per document the engine attempted, in
	// index order. It is a prefix of the request's documents.
	Attempted []ApplyObservation
	// Stopped is the observer error that made the engine stop early, or nil
	// when every document was attempted.
	Stopped error
}

// ApplyFunc is the injected apply engine. It applies the request's documents,
// calling obs after each one, and reports what it attempted. TrackedApply
// calls it at most once per request, and never on an idempotency branch.
type ApplyFunc func(obs ApplyObserverFunc) TrackedApplyOutcome

// TrackedApplyResult is what TrackedApply returns on success (including a
// replay). Results always has exactly one entry per request document, so the
// legacy summary invariants (total == len(docs), failed > 0 on any problem)
// hold for every path.
type TrackedApplyResult struct {
	Results  []ApplyObservation
	Tracking ApplyTracking
}

// ApplyCounts is the legacy summary shape computed from Results.
type ApplyCounts struct {
	Total      int
	Created    int
	Configured int
	Unchanged  int
	Failed     int
}

// Counts tallies Results into the legacy summary. Any action outside the
// Action* set counts as failed, so an unknown action can never read as success.
func (r *TrackedApplyResult) Counts() ApplyCounts {
	c := ApplyCounts{Total: len(r.Results)}
	for _, o := range r.Results {
		switch o.Action {
		case ActionCreated:
			c.Created++
		case ActionConfigured:
			c.Configured++
		case ActionUnchanged:
			c.Unchanged++
		default:
			c.Failed++
		}
	}
	return c
}

// ApplyTracking is the additive tracking block of a tracked apply response
// (plan D7). The yaml package copies it onto its own wire struct.
//
// Three counts describe the documents without a recorded success:
//
//   - RecordedThrough: outcomes the receipt holds.
//   - NotAttempted: documents provably never sent to the cluster (the engine
//     stopped before reaching them, or the receipt never opened its mutation
//     window). Safe to re-preview and retry.
//   - Unrecorded: documents whose outcome the receipt does NOT hold although
//     the mutation window was open (an interrupted original, replayed from an
//     `unknown` receipt). The cluster MAY hold them. Never retry blind.
//
// A live response only ever sets NotAttempted: the running service knows what
// the engine did. A replay of an `unknown` receipt only ever sets Unrecorded:
// the receipt cannot prove what the lost process did after its last record,
// so it claims "never attempted" for nothing (plan D3: "anything after them
// is unknown").
type ApplyTracking struct {
	OperationID       string             `json:"operationId"`
	ReceiptURL        string             `json:"receiptUrl"`
	State             store.ReceiptState `json:"state"`
	ClusterID         string             `json:"clusterId"`
	ClusterGeneration string             `json:"clusterGeneration"`
	ContentDigest     string             `json:"contentDigest"`
	RecordedThrough   int                `json:"recordedThrough"`
	NotAttempted      int                `json:"notAttempted"`
	Unrecorded        int                `json:"unrecorded"`
	Replayed          bool               `json:"replayed"`
	ContainsSecret    bool               `json:"containsSecret"`
	RepairOf          string             `json:"repairOf,omitempty"`
	Objects           []TrackedObjectRef `json:"objects"`
	Verification      VerificationLink   `json:"verification"`
	Warnings          []string           `json:"warnings"`
}

// TrackedObjectRef is the reference (never the content) of one recorded
// object in ApplyTracking.Objects.
type TrackedObjectRef struct {
	Index    int    `json:"index"`
	Group    string `json:"group,omitempty"`
	Version  string `json:"version,omitempty"`
	Resource string `json:"resource,omitempty"`
	UID      string `json:"uid,omitempty"`
}

// VerificationLink tells the client where to poll verification and where it
// stands now.
type VerificationLink struct {
	State store.VerificationState `json:"state"`
	URL   string                  `json:"url"`
}

// VerifyOptions controls one VerifyOnce pass.
type VerifyOptions struct {
	// Persist writes the computed verdict to the receipt. The owner's poll sets
	// it; a grantee's or admin's read of a live evaluation leaves it false so
	// their (possibly permission-limited) view never replaces the owner's
	// verdict. A verdict that is already final is returned as stored either
	// way.
	Persist bool
}

// VerificationResult is one VerifyOnce pass. RetryAfterSeconds is non-zero
// only while State is verifying.
type VerificationResult struct {
	State             store.VerificationState `json:"state"`
	Checks            []CheckResult           `json:"checks"`
	RetryAfterSeconds int                     `json:"retryAfterSeconds,omitempty"`
}

// ReceiptView is the envelope of a receipt as the read endpoints render it.
// Objects are deliberately absent: per-object redaction is the handler's job
// and lands with it. TargetGenerationChanged is computed against the
// generation the caller resolved for the receipt's cluster now.
type ReceiptView struct {
	OperationID             string             `json:"operationId"`
	ReceiptURL              string             `json:"receiptUrl"`
	OwnerUsername           string             `json:"ownerUsername"`
	State                   store.ReceiptState `json:"state"`
	ClusterID               string             `json:"clusterId"`
	ClusterGeneration       string             `json:"clusterGeneration"`
	TargetGenerationChanged bool               `json:"targetGenerationChanged"`
	ContentDigest           string             `json:"contentDigest"`
	DocumentCount           int                `json:"documentCount"`
	RecordedThrough         int                `json:"recordedThrough"`
	Force                   bool               `json:"force"`
	ContainsSecret          bool               `json:"containsSecret"`
	RepairOf                string             `json:"repairOf,omitempty"`
	Verification            VerificationLink   `json:"verification"`
	CreatedAt               time.Time          `json:"createdAt"`
	MutationStartedAt       *time.Time         `json:"mutationStartedAt,omitempty"`
	CompletedAt             *time.Time         `json:"completedAt,omitempty"`
	VerifiedAt              *time.Time         `json:"verifiedAt,omitempty"`
}

// NewReceiptView renders r. currentGeneration is the target cluster's
// generation as resolved for this read ("local", or the record's created_at);
// an empty value means the caller could not resolve it and the flag stays
// false rather than guessing.
func NewReceiptView(r *store.ChangeReceipt, currentGeneration string) ReceiptView {
	id := r.ID.String()
	v := ReceiptView{
		OperationID:       id,
		ReceiptURL:        receiptURL(r.ID),
		OwnerUsername:     r.OwnerUsername,
		State:             r.State,
		ClusterID:         r.ClusterID,
		ClusterGeneration: r.ClusterGeneration,
		ContentDigest:     r.ContentDigest,
		DocumentCount:     r.DocumentCount,
		RecordedThrough:   len(r.Objects),
		Force:             r.Force,
		ContainsSecret:    r.ContainsSecret,
		Verification:      VerificationLink{State: r.VerificationState, URL: verificationURL(r.ID)},
		CreatedAt:         r.CreatedAt,
		MutationStartedAt: r.MutationStartedAt,
		CompletedAt:       r.CompletedAt,
		VerifiedAt:        r.VerifiedAt,
	}
	if r.RepairOf != nil {
		v.RepairOf = r.RepairOf.String()
	}
	v.TargetGenerationChanged = generationChanged(r.ClusterGeneration, currentGeneration)
	return v
}

// generationChanged reports whether the target cluster was re-registered
// since the receipt was written. Unknown on either side is not a change.
func generationChanged(recorded, current string) bool {
	return recorded != "" && current != "" && recorded != current
}

const receiptURLPrefix = "/v1/changes/"

func receiptURL(id uuid.UUID) string      { return receiptURLPrefix + id.String() }
func verificationURL(id uuid.UUID) string { return receiptURL(id) + "/verification" }
