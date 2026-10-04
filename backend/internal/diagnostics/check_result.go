package diagnostics

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
)

// CheckStatus is the normalized tri-state. Unlike the legacy Result.Status
// ("pass"/"warn"/"fail"), it can say "we could not tell". Legacy "warn" and
// "fail" both normalize to CheckStatusFail; Severity still tells them apart.
type CheckStatus string

const (
	CheckStatusPass         CheckStatus = "pass"
	CheckStatusFail         CheckStatus = "fail"
	CheckStatusInconclusive CheckStatus = "inconclusive"
)

// InconclusiveReason is the stable machine code for why a check could not tell.
// Consumers branch on it, so a value is never renamed or reused.
type InconclusiveReason string

const (
	// ReasonPermissionDenied: the user may not read what the check needs.
	ReasonPermissionDenied InconclusiveReason = "permission_denied"
	// ReasonSourceUnavailable: reading what the check needs failed, or the
	// result was not one this contract understands.
	ReasonSourceUnavailable InconclusiveReason = "source_unavailable"
	// ReasonTimedOut: the check hit its time limit.
	ReasonTimedOut InconclusiveReason = "timed_out"
	// ReasonNoRelatedObjects is reserved for checks that need related objects
	// that do not exist. No check here emits it yet.
	ReasonNoRelatedObjects InconclusiveReason = "no_related_objects"
)

// CheckID is the stable identity of a check across releases, formatted
// "<producer>/<slug>". It lets incident evidence (Release D) and change
// verification (Release E) reference the same check without depending on a
// display name.
type CheckID string

// checkIDForRule maps each registered rule to its CheckID.
//
// These ids are a FROZEN WIRE CONTRACT: incidents and change receipts persist
// them. Renaming a rule must not change its CheckID, and a new rule needs an
// entry here (TestCheckIDsAreStableAndUnique fails without one).
var checkIDForRule = map[string]CheckID{
	"CrashLoopBackOff": "diagnostics/crashloopbackoff",
	"ImagePullBackOff": "diagnostics/imagepullbackoff",
	"PendingPod":       "diagnostics/pendingpod",
	"ReplicaMismatch":  "diagnostics/replicamismatch",
	"ZeroEndpoints":    "diagnostics/zeroendpoints",
	"PendingPVC":       "diagnostics/pendingpvc",
}

// kindAPIGroup is the API group of each kind diagnostics can resolve. The core
// group is the empty string. It sits beside kindToResource, which names the
// plural resource for the same kinds.
var kindAPIGroup = map[string]string{
	"Deployment":            "apps",
	"StatefulSet":           "apps",
	"DaemonSet":             "apps",
	"Pod":                   "",
	"Service":               "",
	"PersistentVolumeClaim": "",
}

// CheckSourceRef identifies the object a check observed. UID is empty when the
// object was not read: an identity is never invented, and a recreated
// same-name object carries a different UID so it cannot inherit old evidence.
type CheckSourceRef struct {
	ClusterID string `json:"clusterId"`
	APIGroup  string `json:"apiGroup"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// CheckEvidenceItem is one object a check points at.
type CheckEvidenceItem struct {
	Label    string            `json:"label"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	UID      string            `json:"uid,omitempty"`
	Observed map[string]string `json:"observed,omitempty"` // for evidence adapters; Normalize leaves it empty
}

// CheckResult is the reusable outcome of one diagnostic check, shared by
// incident evidence and change verification.
type CheckResult struct {
	CheckID      CheckID             `json:"checkId"`
	Status       CheckStatus         `json:"status"`
	Severity     Severity            `json:"severity"`
	Summary      string              `json:"summary"`
	Detail       string              `json:"detail,omitempty"`
	Remediation  string              `json:"remediation,omitempty"`
	Source       CheckSourceRef      `json:"source"`
	ObservedAt   time.Time           `json:"observedAt"`
	Evidence     []CheckEvidenceItem `json:"evidence,omitempty"`
	Inconclusive InconclusiveReason  `json:"inconclusive,omitempty"`

	// legacyRuleName and legacyStatus keep the pre-normalization wire values so
	// Denormalize is exact. They are unexported on purpose: they are a
	// compatibility detail and never part of the JSON surface.
	legacyRuleName string
	legacyStatus   string
}

// timedOutMessage is the message runSafeCheck gives a rule that hit its limit.
// Normalize recognizes a timeout by it; TestNormalizeMarksTimedOutCheckInconclusive
// builds the result with runSafeCheck, so a change to either side fails there.
func timedOutMessage(rule string) string {
	return fmt.Sprintf("Rule %q timed out after 5s", rule)
}

// Normalize converts the legacy Results RunDiagnostics produced for target into
// the reusable contract. observedAt comes from the caller because Result
// carries no timestamp; pass the time RunDiagnostics returned.
//
// It reports "we could not tell" where the legacy wire reports health:
//
//   - A result from a rule that depends on a related resolution target.Limitations
//     says was skipped is inconclusive when it is a pass (an empty list proves no
//     absence) or a finding that cites no evidence (ZeroEndpoints reads "zero
//     pods" from that same empty list). A finding that links the objects it
//     observed stands.
//   - A rule that timed out is inconclusive, not failed.
//   - A status this contract does not know is inconclusive, never a pass.
//
// The legacy Results are not altered, and Denormalize restores them exactly.
// A nil results slice yields nil.
func Normalize(clusterID string, target *DiagnosticTarget, observedAt time.Time, results []Result) []CheckResult {
	if results == nil {
		return nil
	}
	env := normalizeEnv{src: sourceRef(clusterID, target), observedAt: observedAt}
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
	src        CheckSourceRef
	observedAt time.Time
	limits     []Limitation
	podUIDs    map[string]string // pod name -> UID, for the pods the target observed
}

func normalizeResult(r Result, env normalizeEnv) CheckResult {
	c := CheckResult{
		CheckID:        checkIDForRule[r.RuleName],
		Severity:       r.Severity,
		Summary:        r.Message,
		Detail:         r.Detail,
		Remediation:    r.Remediation,
		Source:         env.src,
		ObservedAt:     env.observedAt,
		Evidence:       evidenceFromLinks(r.Links, env.podUIDs),
		legacyRuleName: r.RuleName,
		legacyStatus:   r.Status,
	}

	switch {
	case r.Status == "fail" && r.Message == timedOutMessage(r.RuleName):
		c.Status, c.Inconclusive = CheckStatusInconclusive, ReasonTimedOut
	case r.Status != "pass" && r.Status != "warn" && r.Status != "fail":
		c.Status, c.Inconclusive = CheckStatusInconclusive, ReasonSourceUnavailable
	default:
		c.Status = CheckStatusPass
		if r.Status != "pass" {
			c.Status = CheckStatusFail
		}
		// A pass asserts an absence and a finding with no links cites nothing, so
		// neither stands when an input the rule reads was never observed. A
		// finding that links the objects it observed does.
		lacksObservedEvidence := c.Status == CheckStatusPass || len(r.Links) == 0
		if reason, limited := limitedBy(ruleDependsOn(r.RuleName), env.limits); limited && lacksObservedEvidence {
			c.Status, c.Inconclusive = CheckStatusInconclusive, reason
		}
	}
	return c
}

// Denormalize is the compatibility adapter back to the legacy wire. For any rs
// RunDiagnostics produced, Denormalize(Normalize(..., rs)) deep-equals rs. It
// is only meaningful for values Normalize produced: a CheckResult decoded from
// JSON carries no legacy fields. A nil slice yields nil.
func Denormalize(cs []CheckResult) []Result {
	if cs == nil {
		return nil
	}
	out := make([]Result, len(cs))
	for i, c := range cs {
		r := Result{
			RuleName:    c.legacyRuleName,
			Status:      c.legacyStatus,
			Severity:    c.Severity,
			Message:     c.Summary,
			Detail:      c.Detail,
			Remediation: c.Remediation,
		}
		if c.Evidence != nil {
			r.Links = make([]Link, len(c.Evidence))
			for j, e := range c.Evidence {
				r.Links[j] = Link{Label: e.Label, Kind: e.Kind, Name: e.Name}
			}
		}
		out[i] = r
	}
	return out
}

// evidenceFromLinks keeps a nil slice nil and an empty one empty, which is what
// lets Denormalize round-trip exactly. A link to a pod the target observed
// carries that pod's UID, so a pod recreated under the same name is a different
// piece of evidence; a link to anything else gets none.
func evidenceFromLinks(links []Link, podUIDs map[string]string) []CheckEvidenceItem {
	if links == nil {
		return nil
	}
	items := make([]CheckEvidenceItem, len(links))
	for i, l := range links {
		items[i] = CheckEvidenceItem{Label: l.Label, Kind: l.Kind, Name: l.Name}
		if l.Kind == "Pod" {
			items[i].UID = podUIDs[l.Name]
		}
	}
	return items
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
func sourceRef(clusterID string, target *DiagnosticTarget) CheckSourceRef {
	ref := CheckSourceRef{ClusterID: clusterID}
	if target == nil {
		return ref
	}
	ref.Kind, ref.Namespace, ref.Name = target.Kind, target.Namespace, target.Name
	ref.Resource = kindToResource[target.Kind]
	ref.APIGroup = kindAPIGroup[target.Kind]
	if target.Object != nil {
		if obj, err := meta.Accessor(target.Object); err == nil {
			ref.UID = string(obj.GetUID())
		}
	}
	return ref
}

// ruleDependsOn returns the related resolutions the named rule reads. An
// unregistered name depends on nothing.
func ruleDependsOn(rule string) []string {
	for _, r := range rules {
		if r.name == rule {
			return r.dependsOn
		}
	}
	return nil
}

// limitedBy returns the reason of the first limitation that hit a resolution
// the rule depends on.
func limitedBy(dependsOn []string, limits []Limitation) (InconclusiveReason, bool) {
	for _, l := range limits {
		for _, d := range dependsOn {
			if l.Kind == d {
				return l.Reason, true
			}
		}
	}
	return "", false
}
