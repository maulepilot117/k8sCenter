package changes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/diagnostics"
	"github.com/kubecenter/kubecenter/internal/store"
)

// verificationWindow bounds observation from receipt completion. Past this,
// unsatisfied checks freeze as inconclusive/"window_expired" and the state is
// final.
const verificationWindow = 120 * time.Second

// verifyRetryAfterSeconds is the poll hint returned while verifying.
const verifyRetryAfterSeconds = 5

// VerifyOnce performs exactly one live read per verifiable object through dyn
// (the caller's own impersonated client, already routed to the receipt's
// cluster) and, when opts.Persist is set, persists the resulting verdict. It
// is called from a request goroutine and starts no background work, so no
// user credential outlives the request that supplied it. There is no watcher
// and no retry loop: the client polls while the result says verifying.
//
// Rules (plan D6):
//
//   - a receipt whose verification is already final returns the stored verdict
//     untouched: it is frozen, no read is made, Persist or not;
//   - a receipt that has not completed returns pending without reading;
//   - every document index in 0..DocumentCount-1 yields exactly one check. A
//     document with no recorded outcome (an interrupted original) is
//     inconclusive/outcome_unrecorded without a read, so such a receipt can
//     never aggregate to verified;
//   - objects whose apply failed are skipped, never verified, EXCEPT those
//     whose failure is indeterminate (the request was cut off and the server
//     may have committed it): those are verified like a success;
//   - an object with no recorded UID is inconclusive/identity_unknown without
//     a read: a live object cannot be bound to the one that was applied (R1),
//     and no live UID is stamped as evidence;
//   - an unsupported kind is inconclusive/kind_not_supported, never a pass; a
//     supported kind under an OnDelete strategy is
//     inconclusive/strategy_not_supported, never a pass;
//   - not found is fail/not_found; a different UID is
//     inconclusive/target_recreated; forbidden is inconclusive/read_forbidden;
//   - a satisfied postcondition is pass; an unsatisfied one is
//     warn/rollout_in_progress while the 120s window is open, and
//     inconclusive/window_expired once it closed;
//   - overall: any fail -> verification_failed; any retryable check inside
//     the window -> verifying (retry in 5s); all pass -> verified; otherwise
//     (nothing to check, or a terminal inconclusive) -> inconclusive.
//
// Persistence: the store never replaces a final verdict (the guard is in its
// UPDATE), so the first final verdict wins atomically; a refused write returns
// the stored verdict rather than an error. With Persist false the computed
// result is returned and nothing is written; that is how a grantee or admin
// reads a live evaluation without replacing the owner's verdict.
func (s *Service) VerifyOnce(ctx context.Context, r *store.ChangeReceipt, dyn dynamic.Interface, opts VerifyOptions) (*VerificationResult, error) {
	if !s.Available() {
		return nil, &StoreUnavailableError{Step: "verification", Err: errors.New("no receipt store configured")}
	}
	if r == nil {
		return nil, fmt.Errorf("%w: receipt is required", ErrInvalidRequest)
	}
	if dyn == nil {
		return nil, fmt.Errorf("%w: dynamic client is required", ErrInvalidRequest)
	}
	if r.VerificationState.IsFinal() {
		return storedVerdict(r), nil
	}
	if r.CompletedAt == nil || !r.State.IsTerminal() {
		return &VerificationResult{State: store.VerifyPending, Checks: []CheckResult{}}, nil
	}

	now := s.now()
	checks := make([]CheckResult, 0, r.DocumentCount)
	recorded := make(map[int]bool, len(r.Objects))
	for _, o := range r.Objects {
		recorded[o.Index] = true
		if !verifiable(o) {
			continue
		}
		checks = append(checks, s.verifyObject(ctx, dyn, r.ClusterID, o, now))
	}
	for i := 0; i < r.DocumentCount; i++ {
		if !recorded[i] {
			checks = append(checks, unrecordedOutcome(r, i, now))
		}
	}
	if now.Sub(*r.CompletedAt) > verificationWindow {
		for i := range checks {
			if isRetryable(checks[i]) {
				checks[i] = freezeExpired(checks[i])
			}
		}
	}
	state := aggregateVerification(checks)
	res := &VerificationResult{State: state, Checks: checks}
	if state == store.VerifyVerifying {
		res.RetryAfterSeconds = verifyRetryAfterSeconds
	}
	if !opts.Persist {
		return res, nil
	}

	payload, err := json.Marshal(checks)
	if err != nil {
		return nil, fmt.Errorf("encode verification checks: %w", err)
	}
	if err := s.receipts.SetVerification(ctx, r.ID, state, payload); err != nil {
		if errors.Is(err, store.ErrReceiptAlreadyFinal) {
			// Reconciled, or a concurrent poll's final verdict landed first.
			current, getErr := s.receipts.Get(ctx, r.ID)
			if getErr != nil || current == nil {
				return nil, &StoreUnavailableError{Step: "verification", Err: firstErr(getErr, errors.New("receipt disappeared"))}
			}
			return storedVerdict(current), nil
		}
		return nil, &StoreUnavailableError{Step: "verification", Err: err}
	}
	return res, nil
}

// verifiable reports whether a recorded outcome says anything may exist on
// the cluster: a success, or a failure the API server may have committed.
func verifiable(o store.ReceiptObject) bool {
	return o.Action != ActionFailed || o.ErrorClass == ErrorClassIndeterminate
}

// unrecordedOutcome is the check for a document the receipt holds nothing
// about. The receipt stores no identity for it, so Source carries only the
// cluster and Detail names the document position. The wording follows what
// the receipt's state proves (the same split replay() makes): an `unknown`
// receipt was interrupted after the mutation window opened, so the object may
// or may not exist; any other terminal state means the apply never reached
// the document (it never started, or the engine stopped before it) and
// nothing was applied for it. The verdict is inconclusive either way.
func unrecordedOutcome(r *store.ChangeReceipt, index int, now time.Time) CheckResult {
	msg := "no outcome was recorded for this document; the apply stopped before reaching it and nothing was applied for it"
	if r.State == store.ReceiptUnknown {
		msg = "no outcome was recorded for this document; the original apply was interrupted and the object may or may not exist"
	}
	return newCheck(SourceRef{ClusterID: r.ClusterID}, now, CheckInconclusive, ReasonOutcomeUnrecorded, diagnostics.SeverityWarning,
		msg, fmt.Sprintf("document index %d", index))
}

// verifyObject does the one live read for o and evaluates it.
func (s *Service) verifyObject(ctx context.Context, dyn dynamic.Interface, clusterID string, o store.ReceiptObject, now time.Time) CheckResult {
	src := SourceRef{
		ClusterID: clusterID, Group: o.Group, Version: o.Version, Resource: o.Resource,
		Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID,
	}
	if !supportedWorkload(o.Group, o.Kind) {
		return unsupportedKind(src, now)
	}
	if o.Resource == "" || o.Version == "" {
		c := unsupportedKind(src, now)
		c.Detail = "no resource mapping was recorded for this object"
		return c
	}
	if o.UID == "" {
		return newCheck(src, now, CheckInconclusive, ReasonIdentityUnknown, diagnostics.SeverityInfo,
			"the receipt holds no identity for this object, so a live read cannot be bound to what was applied", "")
	}

	gvr := schema.GroupVersionResource{Group: o.Group, Version: o.Version, Resource: o.Resource}
	var ri dynamic.ResourceInterface = dyn.Resource(gvr)
	if o.Namespace != "" {
		ri = dyn.Resource(gvr).Namespace(o.Namespace)
	}
	live, err := ri.Get(ctx, o.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return newCheck(src, now, CheckFail, ReasonNotFound, diagnostics.SeverityCritical,
			"object no longer exists", "")
	case apierrors.IsForbidden(err):
		return newCheck(src, now, CheckInconclusive, ReasonReadForbidden, diagnostics.SeverityInfo,
			"you may no longer read this object; its state could not be verified", "")
	case err != nil:
		return newCheck(src, now, CheckInconclusive, ReasonReadFailed, diagnostics.SeverityInfo,
			"reading the object failed; its state could not be verified", "")
	}
	liveUID := string(live.GetUID())
	if liveUID != "" && liveUID != o.UID {
		c := newCheck(src, now, CheckInconclusive, ReasonTargetRecreated, diagnostics.SeverityWarning,
			"a different object now exists under this name; the receipt's evidence does not apply to it", "")
		c.Evidence = map[string]string{o.Kind + "/" + o.Name: liveUID}
		return c
	}
	return CheckRollout(live, src, now)
}

// evaluation is what a kind's evaluator returns: the clauses to test, or a
// reason the configured update strategy cannot be observed (unsupported is a
// non-empty detail string; the reason is always strategy_not_supported).
type evaluation struct {
	preds       []predicate
	unsupported string
}

// workloadEvaluators is the single source of truth for which kinds have a
// rollout postcondition and what it is. A kind absent here is unsupported;
// CheckRollout also treats an evaluation with no predicates as unsupported,
// so adding a kind without clauses cannot produce a pass.
var workloadEvaluators = map[string]func(*unstructured.Unstructured) evaluation{
	"Deployment":  evaluateDeployment,
	"StatefulSet": evaluateStatefulSet,
	"DaemonSet":   evaluateDaemonSet,
}

// supportedWorkload reports whether CheckRollout defines a postcondition for
// the kind. The group test is deliberate: a CRD that happens to be called
// Deployment is not apps/v1.
func supportedWorkload(group, kind string) bool {
	if group != "apps" {
		return false
	}
	_, ok := workloadEvaluators[kind]
	return ok
}

func unsupportedKind(src SourceRef, now time.Time) CheckResult {
	kind := src.Kind
	if kind == "" {
		kind = "this kind"
	}
	return newCheck(src, now, CheckInconclusive, ReasonKindNotSupported, diagnostics.SeverityInfo,
		fmt.Sprintf("no rollout postcondition is defined for %s; the apply was accepted but not verified", kind), "")
}

// isRetryable reports whether another poll could change the check: a rollout
// still in progress, or a read that failed for a transient reason. Everything
// else is settled.
func isRetryable(c CheckResult) bool {
	switch {
	case c.Status == CheckWarn && c.Reason == ReasonRolloutInProgress:
		return true
	case c.Status == CheckInconclusive && c.Reason == ReasonReadFailed:
		return true
	}
	return false
}

// freezeExpired converts a retryable check into the terminal window_expired
// form, keeping what was last observed in Detail.
func freezeExpired(c CheckResult) CheckResult {
	detail := c.Message
	if c.Detail != "" {
		detail += " (" + c.Detail + ")"
	}
	c.Status = CheckInconclusive
	c.Reason = ReasonWindowExpired
	c.Severity = diagnostics.SeverityWarning
	c.Message = "the postcondition was not satisfied within the verification window"
	c.Detail = "last observation: " + detail
	return c
}

// aggregateVerification folds per-object checks into the receipt's
// verification state.
func aggregateVerification(checks []CheckResult) store.VerificationState {
	if len(checks) == 0 {
		return store.VerifyInconclusive
	}
	anyFail, anyRetryable, allPass := false, false, true
	for _, c := range checks {
		switch {
		case c.Status == CheckFail:
			anyFail = true
		case isRetryable(c):
			anyRetryable = true
		}
		if c.Status != CheckPass {
			allPass = false
		}
	}
	switch {
	case anyFail:
		return store.VerifyFailed
	case anyRetryable:
		return store.VerifyVerifying
	case allPass:
		return store.VerifyVerified
	default:
		return store.VerifyInconclusive
	}
}

// storedVerdict renders a final receipt's persisted verification. Undecodable
// evidence (there should be none) yields the state with no checks rather than
// an error: the state column is authoritative.
func storedVerdict(r *store.ChangeReceipt) *VerificationResult {
	checks := []CheckResult{}
	if len(r.Verification) > 0 {
		if err := json.Unmarshal(r.Verification, &checks); err != nil || checks == nil {
			checks = []CheckResult{}
		}
	}
	return &VerificationResult{State: r.VerificationState, Checks: checks}
}

// CheckRollout evaluates the rollout postcondition for one live workload
// object (plan D6, tightened to kubectl's `rollout status` clauses so a
// rollout is complete only when no pod of a previous revision is left). Pure:
// no I/O, so it is table-tested for every predicate in both polarities.
//
//	apps/v1 Deployment:  observedGeneration >= generation,
//	                     updatedReplicas == spec.replicas,
//	                     status.replicas == updatedReplicas   (no old-RS pods),
//	                     availableReplicas >= updatedReplicas,
//	                     condition Available == True
//	apps/v1 StatefulSet: observedGeneration >= generation,
//	                     status.replicas == spec.replicas,
//	                     readyReplicas >= spec.replicas, then
//	                     RollingUpdate, partition == 0:
//	                       updatedReplicas == spec.replicas,
//	                       currentRevision == updateRevision
//	                     RollingUpdate, partition > 0 (kubectl's partitioned
//	                     clause; the revisions legitimately differ):
//	                       updatedReplicas >= spec.replicas - partition
//	apps/v1 DaemonSet:   observedGeneration >= generation,
//	                     updatedNumberScheduled == desiredNumberScheduled,
//	                     currentNumberScheduled == desiredNumberScheduled,
//	                     numberAvailable >= desiredNumberScheduled,
//	                     numberReady >= desiredNumberScheduled,
//	                     numberUnavailable == 0
//
// A StatefulSet or DaemonSet whose spec.updateStrategy.type is OnDelete never
// converges on its own (pods are replaced only when something deletes them),
// so it is CheckInconclusive/"strategy_not_supported" at once instead of
// holding the window open.
//
// DaemonSet surge is covered by the clauses above: the daemon controller
// derives every per-node counter (ready, available, updated) from the OLDEST
// scheduled pod on the node (updateDaemonSetStatus sorts by creation time and
// reads daemonPods[0]), so a node that still holds its old pod is counted as
// not updated until that pod is gone.
//
// Any other kind, or one of these kinds outside the apps group, is
// CheckInconclusive with Reason "kind_not_supported": a kind the check does
// not understand is never reported as a pass. A satisfied postcondition is
// CheckPass/"ok"; an unsatisfied one is CheckWarn/"rollout_in_progress" with
// the failing predicates in Detail. Evidence maps "<Kind>/<Name>" to the UID
// read from the object and never holds content.
func CheckRollout(obj *unstructured.Unstructured, src SourceRef, now time.Time) CheckResult {
	if obj == nil {
		return unsupportedKind(src, now)
	}
	gvk := obj.GroupVersionKind()
	if src.Kind == "" {
		src.Kind = gvk.Kind
	}
	if src.Group == "" && src.Version == "" {
		src.Group, src.Version = gvk.Group, gvk.Version
	}
	if src.Name == "" {
		src.Name = obj.GetName()
	}
	if src.Namespace == "" {
		src.Namespace = obj.GetNamespace()
	}
	if uid := string(obj.GetUID()); uid != "" {
		src.UID = uid
	}
	if !supportedWorkload(gvk.Group, gvk.Kind) {
		return unsupportedKind(src, now)
	}
	ev := workloadEvaluators[gvk.Kind](obj)
	evidence := map[string]string{gvk.Kind + "/" + obj.GetName(): src.UID}
	if ev.unsupported != "" {
		c := newCheck(src, now, CheckInconclusive, ReasonStrategyNotSupported, diagnostics.SeverityInfo,
			"the rollout postcondition cannot be observed under this update strategy; the apply was accepted but not verified", ev.unsupported)
		c.Evidence = evidence
		return c
	}
	if len(ev.preds) == 0 {
		return unsupportedKind(src, now)
	}

	var observed, failing []string
	for _, p := range ev.preds {
		observed = append(observed, p.detail)
		if !p.ok {
			failing = append(failing, p.detail)
		}
	}
	var c CheckResult
	if len(failing) == 0 {
		c = newCheck(src, now, CheckPass, ReasonRolloutComplete, diagnostics.SeverityInfo,
			"rollout complete", strings.Join(observed, "; "))
	} else {
		c = newCheck(src, now, CheckWarn, ReasonRolloutInProgress, diagnostics.SeverityWarning,
			"rollout not complete", strings.Join(failing, "; "))
	}
	c.Evidence = evidence
	return c
}

// predicate is one evaluated D6 clause. detail names the clause and the
// observed values (status counters, never spec content beyond replicas).
type predicate struct {
	ok     bool
	detail string
}

// onDeleteStrategy is the spec.updateStrategy.type value that never
// converges on its own.
const onDeleteStrategy = "OnDelete"

func evaluateDeployment(obj *unstructured.Unstructured) evaluation {
	gen := obj.GetGeneration()
	og := statusInt(obj, "observedGeneration")
	replicas := specReplicas(obj)
	total := statusInt(obj, "replicas")
	updated := statusInt(obj, "updatedReplicas")
	available := statusInt(obj, "availableReplicas")
	availCond := conditionStatus(obj, "Available")
	return evaluation{preds: []predicate{
		{og >= gen, fmt.Sprintf("observedGeneration %d >= generation %d", og, gen)},
		{updated == replicas, fmt.Sprintf("updatedReplicas %d == replicas %d", updated, replicas)},
		{total == updated, fmt.Sprintf("status.replicas %d == updatedReplicas %d", total, updated)},
		{available >= updated, fmt.Sprintf("availableReplicas %d >= updatedReplicas %d", available, updated)},
		{availCond == "True", fmt.Sprintf("condition Available=%s", orUnset(availCond))},
	}}
}

func evaluateStatefulSet(obj *unstructured.Unstructured) evaluation {
	if strategy := updateStrategyType(obj); strategy == onDeleteStrategy {
		return evaluation{unsupported: "spec.updateStrategy.type=OnDelete: pods are replaced only when deleted"}
	}
	gen := obj.GetGeneration()
	og := statusInt(obj, "observedGeneration")
	replicas := specReplicas(obj)
	total := statusInt(obj, "replicas")
	updated := statusInt(obj, "updatedReplicas")
	ready := statusInt(obj, "readyReplicas")
	preds := []predicate{
		{og >= gen, fmt.Sprintf("observedGeneration %d >= generation %d", og, gen)},
		{total == replicas, fmt.Sprintf("status.replicas %d == replicas %d", total, replicas)},
		{ready >= replicas, fmt.Sprintf("readyReplicas %d >= replicas %d", ready, replicas)},
	}
	partition, _, _ := unstructured.NestedInt64(obj.Object, "spec", "updateStrategy", "rollingUpdate", "partition")
	if partition > 0 {
		// kubectl: a partitioned roll out is complete when the pods above the
		// partition are updated; the revisions legitimately stay different.
		want := replicas - partition
		preds = append(preds, predicate{updated >= want,
			fmt.Sprintf("updatedReplicas %d >= replicas %d - partition %d", updated, replicas, partition)})
		return evaluation{preds: preds}
	}
	current := statusString(obj, "currentRevision")
	update := statusString(obj, "updateRevision")
	preds = append(preds,
		predicate{updated == replicas, fmt.Sprintf("updatedReplicas %d == replicas %d", updated, replicas)},
		predicate{current == update, fmt.Sprintf("currentRevision %s == updateRevision %s", orUnset(current), orUnset(update))},
	)
	return evaluation{preds: preds}
}

func evaluateDaemonSet(obj *unstructured.Unstructured) evaluation {
	if strategy := updateStrategyType(obj); strategy == onDeleteStrategy {
		return evaluation{unsupported: "spec.updateStrategy.type=OnDelete: pods are replaced only when deleted"}
	}
	gen := obj.GetGeneration()
	og := statusInt(obj, "observedGeneration")
	desired := statusInt(obj, "desiredNumberScheduled")
	current := statusInt(obj, "currentNumberScheduled")
	updated := statusInt(obj, "updatedNumberScheduled")
	available := statusInt(obj, "numberAvailable")
	ready := statusInt(obj, "numberReady")
	unavailable := statusInt(obj, "numberUnavailable")
	return evaluation{preds: []predicate{
		{og >= gen, fmt.Sprintf("observedGeneration %d >= generation %d", og, gen)},
		{updated == desired, fmt.Sprintf("updatedNumberScheduled %d == desiredNumberScheduled %d", updated, desired)},
		{current == desired, fmt.Sprintf("currentNumberScheduled %d == desiredNumberScheduled %d", current, desired)},
		{available >= desired, fmt.Sprintf("numberAvailable %d >= desiredNumberScheduled %d", available, desired)},
		{ready >= desired, fmt.Sprintf("numberReady %d >= desiredNumberScheduled %d", ready, desired)},
		{unavailable == 0, fmt.Sprintf("numberUnavailable %d == 0", unavailable)},
	}}
}

// updateStrategyType reads spec.updateStrategy.type; "" when unset (the API
// default is RollingUpdate for both StatefulSet and DaemonSet).
func updateStrategyType(obj *unstructured.Unstructured) string {
	v, found, err := unstructured.NestedString(obj.Object, "spec", "updateStrategy", "type")
	if err != nil || !found {
		return ""
	}
	return v
}

// specReplicas reads spec.replicas with the API default of 1 when unset.
func specReplicas(obj *unstructured.Unstructured) int64 {
	v, found, err := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	if err != nil || !found {
		return 1
	}
	return v
}

// statusInt reads an integer status field; absent reads as 0, which is what
// the API server reports before the controller has observed the object.
func statusInt(obj *unstructured.Unstructured, field string) int64 {
	v, found, err := unstructured.NestedInt64(obj.Object, "status", field)
	if err != nil || !found {
		return 0
	}
	return v
}

func statusString(obj *unstructured.Unstructured, field string) string {
	v, found, err := unstructured.NestedString(obj.Object, "status", field)
	if err != nil || !found {
		return ""
	}
	return v
}

// conditionStatus returns the status of the named status.condition, or "".
func conditionStatus(obj *unstructured.Unstructured, condType string) string {
	conds, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, raw := range conds {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == condType {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

func orUnset(s string) string {
	if s == "" {
		return "<unset>"
	}
	return s
}

func newCheck(src SourceRef, now time.Time, status CheckStatus, reason string, sev Severity, message, detail string) CheckResult {
	return CheckResult{
		CheckID:    CheckIDRolloutComplete,
		Status:     status,
		Severity:   sev,
		Reason:     reason,
		Message:    message,
		Detail:     detail,
		Source:     src,
		ObservedAt: now.UTC(),
	}
}
