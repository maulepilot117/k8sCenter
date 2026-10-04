package diagnostics

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
)

// CheckStatus is the normalized outcome of one check. Unlike the legacy
// Result.Status ("pass"/"warn"/"fail"), it can say "we could not tell".
//
// The type, its constants, SourceRef and the JSON keys of CheckResult follow the
// "Interface contract with Release D U20" section of the Release E plan, which
// Release E persists in change_receipts.verification: renaming a key or changing
// a type is a data migration there.
type CheckStatus string

const (
	CheckPass         CheckStatus = "pass"
	CheckWarn         CheckStatus = "warn"
	CheckFail         CheckStatus = "fail"
	CheckInconclusive CheckStatus = "inconclusive"
)

// Reason codes for CheckResult.Reason. They are stable machine codes that
// consumers branch on, so a value is never renamed or reused. They are plain
// strings so a consumer can set its own (Release E uses "kind_not_supported").
const (
	// ReasonOK is the reason of a pass.
	ReasonOK = "ok"
	// ReasonFinding is the reason of a warn or fail the check supports with
	// what it observed.
	ReasonFinding = "finding"
	// ReasonPermissionDenied: the user may not read what the check needs.
	ReasonPermissionDenied = "permission_denied"
	// ReasonSourceUnavailable: reading what the check needs failed, or the
	// result was not one this contract understands.
	ReasonSourceUnavailable = "source_unavailable"
	// ReasonTimedOut: the check hit its time limit.
	ReasonTimedOut = "timed_out"
	// ReasonInternalError: the check itself failed (it panicked), so it says
	// nothing about the target.
	ReasonInternalError = "internal_error"
)

// checkIDForRule maps each registered rule to its CheckResult.CheckID, formatted
// "<producer>/<slug>". The id lets incident evidence (Release D) and change
// verification (Release E) reference one check without depending on a display
// name.
//
// These ids are a FROZEN WIRE CONTRACT: incidents and change receipts persist
// them. Renaming a rule must not change its id, and a new rule needs an entry
// here (TestCheckIDsAreStableAndUnique fails without one).
var checkIDForRule = map[string]string{
	"CrashLoopBackOff": "diagnostics/crashloopbackoff",
	"ImagePullBackOff": "diagnostics/imagepullbackoff",
	"PendingPod":       "diagnostics/pendingpod",
	"ReplicaMismatch":  "diagnostics/replicamismatch",
	"ZeroEndpoints":    "diagnostics/zeroendpoints",
	"PendingPVC":       "diagnostics/pendingpvc",
}

// groupVersion is the API group and version of a kind diagnostics can resolve.
// The core group is the empty string.
type groupVersion struct{ Group, Version string }

// kindGroupVersion sits beside kindToResource, which names the plural resource
// for the same kinds.
var kindGroupVersion = map[string]groupVersion{
	"Deployment":            {"apps", "v1"},
	"StatefulSet":           {"apps", "v1"},
	"DaemonSet":             {"apps", "v1"},
	"Pod":                   {"", "v1"},
	"Service":               {"", "v1"},
	"PersistentVolumeClaim": {"", "v1"},
}

// SourceRef identifies the object a check observed. UID is empty when the object
// was not read: an identity is never invented, and a recreated same-name object
// carries a different UID so it cannot inherit old evidence.
type SourceRef struct {
	ClusterID string `json:"clusterId"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// CheckResult is the reusable outcome of one diagnostic check, shared by
// incident evidence and change verification.
//
// Evidence maps "<Kind>/<Name>" to the UID of each object the check points at
// (empty when the UID was not observed), never to object content. Remediation is
// an addition to the Release E contract: its decoder ignores it.
type CheckResult struct {
	CheckID     string            `json:"checkId"`
	Status      CheckStatus       `json:"status"`
	Severity    Severity          `json:"severity"`
	Reason      string            `json:"reason"`
	Message     string            `json:"message"`
	Detail      string            `json:"detail,omitempty"`
	Remediation string            `json:"remediation,omitempty"`
	Source      SourceRef         `json:"source"`
	ObservedAt  time.Time         `json:"observedAt"`
	Evidence    map[string]string `json:"evidence,omitempty"`

	// legacy is the Result this was normalized from. It is unexported on
	// purpose: it is a compatibility detail, never part of the JSON surface, and
	// it is what makes Denormalize exact.
	legacy Result
}

// timedOutMessage is the message runSafeCheck gives a rule that hit its limit.
// Normalize recognizes a timeout by it, and runSafeCheck builds it through this
// function, so the two cannot drift.
func timedOutMessage(rule string) string {
	return fmt.Sprintf("Rule %q timed out after 5s", rule)
}

// internalErrorMessage is the message runSafeCheck gives a rule that panicked.
// Normalize recognizes it the same way it recognizes timedOutMessage.
func internalErrorMessage(rule string) string {
	return fmt.Sprintf("Rule %q encountered an internal error", rule)
}

// Normalize converts the legacy Results RunDiagnostics produced for target into
// the reusable contract. observedAt comes from the caller because Result
// carries no timestamp; pass the time RunDiagnostics returned.
//
// It reports "we could not tell" where the legacy wire reports health or
// failure:
//
//   - A result from a rule that depends on a related resolution target.Limitations
//     says was skipped is inconclusive when it is a pass (an empty list proves no
//     absence) or a finding that cites no evidence (ZeroEndpoints reads "zero
//     pods" from that same empty list). A finding that links the objects it
//     observed stands.
//   - A rule that timed out or panicked is inconclusive, not failed.
//   - A status this contract does not know is inconclusive, never a pass.
//
// The legacy Results are not altered, and Denormalize restores them exactly.
// A nil results slice yields nil. A Result from a rule that is not in
// checkIDForRule gets an empty CheckID, so only pass it Results RunDiagnostics
// produced.
func Normalize(clusterID string, target *DiagnosticTarget, observedAt time.Time, results []Result) []CheckResult {
	if results == nil {
		return nil
	}
	env := normalizeEnv{src: sourceRef(clusterID, target), observedAt: observedAt, target: target}
	if target != nil {
		env.limits = target.Limitations
		env.podUIDs = podUIDsByName(target.Pods)
	}

	out := make([]CheckResult, len(results))
	for i, r := range results {
		out[i] = normalizeResult(r, env)
	}
	return out
}

// normalizeEnv is what every result in one Normalize call shares.
type normalizeEnv struct {
	src        SourceRef
	observedAt time.Time
	target     *DiagnosticTarget // nil when Normalize was given none
	limits     []Limitation
	podUIDs    map[string]string // pod name -> UID, for the pods the target observed
}

func normalizeResult(r Result, env normalizeEnv) CheckResult {
	c := CheckResult{
		CheckID:     checkIDForRule[r.RuleName],
		Severity:    r.Severity,
		Message:     r.Message,
		Detail:      r.Detail,
		Remediation: r.Remediation,
		Source:      env.src,
		ObservedAt:  env.observedAt,
		Evidence:    evidenceFromLinks(r.Links, env.podUIDs),
		legacy:      cloneResult(r),
	}

	switch {
	case r.Status == "fail" && r.Message == timedOutMessage(r.RuleName):
		c.Status, c.Reason = CheckInconclusive, ReasonTimedOut
	case r.Status == "fail" && r.Message == internalErrorMessage(r.RuleName):
		c.Status, c.Reason = CheckInconclusive, ReasonInternalError
	case r.Status != "pass" && r.Status != "warn" && r.Status != "fail":
		c.Status, c.Reason = CheckInconclusive, ReasonSourceUnavailable
	default:
		c.Status, c.Reason = CheckPass, ReasonOK
		switch r.Status {
		case "warn":
			c.Status, c.Reason = CheckWarn, ReasonFinding
		case "fail":
			c.Status, c.Reason = CheckFail, ReasonFinding
		}
		// A pass asserts an absence and a finding with no links cites nothing, so
		// neither stands when an input the rule reads was never observed. A
		// finding that links the objects it observed does.
		lacksObservedEvidence := r.Status == "pass" || len(r.Links) == 0
		if lacksObservedEvidence {
			if reason, limited := limitedBy(ruleDependsOn(r.RuleName, env.target), env.limits); limited {
				c.Status, c.Reason = CheckInconclusive, reason
			}
		}
	}
	return c
}

// Denormalize is the compatibility adapter back to the legacy wire. For any rs
// RunDiagnostics produced, Denormalize(Normalize(..., rs)) deep-equals rs. It
// returns the legacy Result each CheckResult was normalized from, so it is only
// meaningful for values Normalize produced: a CheckResult decoded from JSON
// carries none. A nil slice yields nil.
func Denormalize(cs []CheckResult) []Result {
	if cs == nil {
		return nil
	}
	out := make([]Result, len(cs))
	for i, c := range cs {
		out[i] = cloneResult(c.legacy)
	}
	return out
}

// cloneResult copies a Result so the original and the copy share no slice. A nil
// Links stays nil and an empty one stays empty, which is what lets Denormalize
// round-trip exactly.
func cloneResult(r Result) Result {
	if r.Links != nil {
		r.Links = append([]Link{}, r.Links...)
	}
	return r
}

// evidenceFromLinks maps each linked object to its observed UID. Only a link to
// a pod the target observed gets one, so a pod recreated under the same name is
// a different piece of evidence; a link to anything else maps to "".
func evidenceFromLinks(links []Link, podUIDs map[string]string) map[string]string {
	if len(links) == 0 {
		return nil
	}
	evidence := make(map[string]string, len(links))
	for _, l := range links {
		uid := ""
		if l.Kind == "Pod" {
			uid = podUIDs[l.Name]
		}
		evidence[l.Kind+"/"+l.Name] = uid
	}
	return evidence
}

// podUIDsByName indexes the observed pods by name. Pods without a UID are left
// out, so no identity is invented for them.
func podUIDsByName(pods []*corev1.Pod) map[string]string {
	uids := make(map[string]string, len(pods))
	for _, p := range pods {
		if p != nil && p.UID != "" {
			uids[p.Name] = string(p.UID)
		}
	}
	return uids
}

// sourceRef describes the checked object. Only its identity leaves the object:
// kind, name and namespace from the target, the UID from its metadata.
func sourceRef(clusterID string, target *DiagnosticTarget) SourceRef {
	ref := SourceRef{ClusterID: clusterID}
	if target == nil {
		return ref
	}
	ref.Kind, ref.Namespace, ref.Name = target.Kind, target.Namespace, target.Name
	ref.Resource = kindToResource[target.Kind]
	gv := kindGroupVersion[target.Kind]
	ref.Group, ref.Version = gv.Group, gv.Version
	if target.Object != nil {
		if obj, err := meta.Accessor(target.Object); err == nil {
			ref.UID = string(obj.GetUID())
		}
	}
	return ref
}

// ruleDependsOn returns the related resolutions the named rule reads for target.
// An unregistered name, a rule that reads only the target object, and a nil
// target depend on nothing.
func ruleDependsOn(rule string, target *DiagnosticTarget) []string {
	if target == nil {
		return nil
	}
	for _, r := range rules {
		if r.name == rule {
			if r.dependsOn == nil {
				return nil
			}
			return r.dependsOn(target)
		}
	}
	return nil
}

// limitedBy returns the reason of the first limitation that hit a resolution
// the rule depends on.
func limitedBy(dependsOn []string, limits []Limitation) (string, bool) {
	for _, l := range limits {
		for _, d := range dependsOn {
			if l.Kind == d {
				return l.Reason, true
			}
		}
	}
	return "", false
}
