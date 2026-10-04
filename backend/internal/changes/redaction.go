package changes

// The pure Q1 read policy for change receipts (Release E U29a): the envelope
// gate and the per-object read-time redaction. Nothing here does I/O or
// touches HTTP; redaction_fuzz_test.go drives these functions directly and
// handler.go wires them to the request.

import (
	"strings"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/gitops"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/store"
)

// Redaction reasons carried by ReceiptObjectView.Reason and
// CheckView.RedactionReason. Stable wire values.
const (
	// RedactionForbidden: the caller does not currently hold `get` on the
	// object's resource in its namespace on the receipt's cluster, or that
	// could not be determined (an access-check error fails closed).
	RedactionForbidden = "forbidden"
	// RedactionSecretFiltered: the object is a Secret and the caller does not
	// currently hold `get` on secrets in its namespace.
	RedactionSecretFiltered = "secret-filtered"
)

// ReceiptObjectView is the wire form of one recorded object. Every field
// except Index, Redacted and Reason is dropped when Redacted is true.
type ReceiptObjectView struct {
	Index     int    `json:"index"`
	Redacted  bool   `json:"redacted,omitempty"`
	Reason    string `json:"reason,omitempty"` // RedactionForbidden | RedactionSecretFiltered
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
	Action    string `json:"action,omitempty"`
	// Error is the stored error text. Never emitted for a Secret-bearing
	// receipt or a Secret object: admission and validation messages echo
	// submitted values. ErrorClass is emitted instead (and alongside Error
	// when the stored text carries a recognizable class prefix).
	Error      string `json:"error,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
}

// CheckView is the wire form of one verification check. A check on an object
// the caller may not read keeps only its identity-free fields (checkId,
// status, severity, reason, observedAt) and says so; message, detail,
// remediation, source and evidence all name the object and are dropped.
type CheckView struct {
	CheckID         string            `json:"checkId"`
	Status          CheckStatus       `json:"status"`
	Severity        Severity          `json:"severity"`
	Reason          string            `json:"reason"`
	Redacted        bool              `json:"redacted,omitempty"`
	RedactionReason string            `json:"redactionReason,omitempty"`
	Message         string            `json:"message,omitempty"`
	Detail          string            `json:"detail,omitempty"`
	Remediation     string            `json:"remediation,omitempty"`
	Source          *SourceRef        `json:"source,omitempty"`
	Evidence        map[string]string `json:"evidence,omitempty"`
	ObservedAt      timeStamp         `json:"observedAt"`
}

// OwnershipView is one stored ownership entry after read-time
// re-authorization. RedactedApps counts confirming applications removed
// because the caller may not currently get them. When every confirming
// application was removed the verdict is collapsed too (controller none,
// confidence forbidden, reason OwnershipReasonAppsRedacted): a caller who may
// not get the application is not told that it claims the object.
type OwnershipView struct {
	gitops.OwnershipResult
	RedactedApps int `json:"redactedApps,omitempty"`
}

// OwnershipReasonAppsRedacted is the OwnershipResult.Reason of a collapsed
// entry. Stable wire value, in the gitops reason vocabulary.
const OwnershipReasonAppsRedacted = "apps-redacted"

// ReceiptSummary is the historical outcome count of a receipt, computed from
// every recorded object BEFORE redaction: counts leak no identity and the
// record must not change with the reader. NotRecorded is the number of
// submitted documents without a recorded outcome.
type ReceiptSummary struct {
	Total       int `json:"total"`
	Created     int `json:"created"`
	Configured  int `json:"configured"`
	Unchanged   int `json:"unchanged"`
	Failed      int `json:"failed"`
	NotRecorded int `json:"notRecorded"`
}

// ReadDecision is the envelope-level gate: ownership, explicit grant, admin.
type ReadDecision int

const (
	ReadDenied ReadDecision = iota
	ReadAsOwner
	ReadAsGrantee
	ReadAsAdmin
)

// String is the wire value of ReceiptDetail.Access.
func (d ReadDecision) String() string {
	switch d {
	case ReadAsOwner:
		return "owner"
	case ReadAsGrantee:
		return "grantee"
	case ReadAsAdmin:
		return "admin"
	default:
		return ""
	}
}

// authorizeReceiptRead decides envelope visibility only. It never inspects
// object contents; per-object visibility is redactObjects' job. A caller
// without an identity is denied whatever the grants say, so an empty grantee
// id can never match.
func authorizeReceiptRead(user *auth.User, r *store.ChangeReceipt, grants []string) ReadDecision {
	if user == nil || r == nil || user.ID == "" {
		return ReadDenied
	}
	if r.OwnerID == user.ID {
		return ReadAsOwner
	}
	if auth.IsAdmin(user) {
		return ReadAsAdmin
	}
	for _, g := range grants {
		if g == user.ID {
			return ReadAsGrantee
		}
	}
	return ReadDenied
}

// mayPersistVerdict is the write side of the policy: only the receipt's
// OWNER records a verification verdict. Everyone else, admins included, gets
// a live evaluation under their own identity that is never stored, so a
// reader whose Kubernetes identity cannot read the workload can never freeze
// the owner's receipt as inconclusive. (The k8sCenter admin role is
// app-level; every cluster call impersonates the caller, so admin does not
// imply `get`.)
func mayPersistVerdict(d ReadDecision) bool {
	return d == ReadAsOwner
}

// mayReachCluster is middleware.ClusterContext's rule applied to a receipt's
// cluster rather than the request header's: live access to a remote cluster
// (SARs, verification reads, generation lookups, ownership resolution) needs
// the admin role. A receipt read must not become a way around that gate.
func mayReachCluster(user *auth.User, clusterID string) bool {
	return k8s.IsLocalClusterID(clusterID) || auth.IsAdmin(user)
}

// objectAccess reports whether the caller CURRENTLY holds `get` on
// (group, resource, namespace) in the receipt's cluster. Any doubt is false.
type objectAccess func(group, resource, namespace string) bool

// denyAll is the objectAccess used when there is no authorization source.
func denyAll(string, string, string) bool { return false }

// objectRedaction returns why an object must be hidden from the caller, or ""
// when it may be shown. A Secret needs `get` on core secrets in its namespace
// first (RedactionSecretFiltered); every object then needs `get` on its own
// resource (RedactionForbidden). An object without a recorded resource cannot
// be re-authorized and is hidden.
func objectRedaction(kind, group, resource, namespace string, allowed objectAccess) string {
	if allowed == nil {
		allowed = denyAll
	}
	if isSecretKind(kind) && !allowed("", "secrets", namespace) {
		return RedactionSecretFiltered
	}
	if resource == "" || !allowed(group, resource, namespace) {
		return RedactionForbidden
	}
	return ""
}

// redactObjects applies Q1's read-time re-authorization. allowed reports the
// caller's CURRENT k8s authorization; containsSecret triggers the stricter
// Secret filtering. A hidden object keeps ONLY its index and the reason. The
// output has one view per input, in input order.
func redactObjects(objs []store.ReceiptObject, containsSecret bool, allowed objectAccess) ([]ReceiptObjectView, int) {
	out := make([]ReceiptObjectView, 0, len(objs))
	redacted := 0
	for _, o := range objs {
		if reason := objectRedaction(o.Kind, o.Group, o.Resource, o.Namespace, allowed); reason != "" {
			out = append(out, ReceiptObjectView{Index: o.Index, Redacted: true, Reason: reason})
			redacted++
			continue
		}
		v := ReceiptObjectView{
			Index: o.Index, Group: o.Group, Version: o.Version, Resource: o.Resource,
			Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID, Action: o.Action,
		}
		if o.Error != "" || o.ErrorClass != "" {
			v.ErrorClass = errorClassOf(o.ErrorClass, o.Error)
			if containsSecret || isSecretKind(o.Kind) {
				// Never the text, whatever the store holds. Classification is
				// derived here so it does not depend on the store's own
				// sanitizing having run.
				if v.ErrorClass == "" {
					v.ErrorClass = ErrorClassOther
				}
			} else {
				v.Error = o.Error
			}
		}
		out = append(out, v)
	}
	return out, redacted
}

// errorClassOf returns the stored class when it is one the service defines;
// otherwise it recognizes the "<class>: ..." prefix sanitizedError writes
// (rows recorded before the class column existed). Any other input is
// unclassified (""). The closed set is restated here rather than derived from
// a shared slice so a value outside it can never reach the wire;
// TestErrorClassOf_RoundTripsEveryClass pins it to the ErrorClass* constants.
func errorClassOf(stored, errText string) string {
	if isErrorClass(stored) {
		return stored
	}
	class, _, ok := strings.Cut(errText, ":")
	if ok && isErrorClass(class) {
		return class
	}
	return ""
}

func isErrorClass(s string) bool {
	switch s {
	case ErrorClassConflict, ErrorClassForbidden, ErrorClassInvalid, ErrorClassNotFound, ErrorClassIndeterminate, ErrorClassOther:
		return true
	}
	return false
}

// redactChecks applies the per-object rule to verification checks: a check's
// Source names the object it observed and its Evidence maps Kind/Name to a
// UID, so a check on a hidden object is reduced to a stub.
func redactChecks(checks []CheckResult, allowed objectAccess) ([]CheckView, int) {
	out := make([]CheckView, 0, len(checks))
	redacted := 0
	for _, c := range checks {
		v := CheckView{CheckID: c.CheckID, Status: c.Status, Severity: c.Severity, Reason: c.Reason, ObservedAt: c.ObservedAt}
		src := c.Source
		if reason := objectRedaction(src.Kind, src.Group, src.Resource, src.Namespace, allowed); reason != "" {
			v.Redacted = true
			v.RedactionReason = reason
			redacted++
		} else {
			v.Message = c.Message
			v.Detail = c.Detail
			v.Remediation = c.Remediation
			v.Source = &src
			v.Evidence = c.Evidence
		}
		out = append(out, v)
	}
	return out, redacted
}

// objectKey identifies an object the way ownership results do: without
// resource, version or uid, which the ownership path may leave empty.
type objectKey struct{ group, kind, namespace, name string }

// redactOwnership keeps a stored ownership entry only when its object is
// among the objects the caller may currently read, and within it only the
// confirming applications the caller may currently get (Argo Applications,
// Flux Kustomizations and HelmReleases are checked by their own GVR in their
// namespace; an application of any other kind cannot be re-authorized and is
// removed). Evidence naming a removed application goes with it, and an entry
// left with no application collapses to controller none / confidence
// forbidden / reason apps-redacted. The rule is the same for owner, grantee
// and admin: an application the caller can no longer list is not disclosed
// through a receipt they wrote earlier.
//
// objs are the receipt's recorded objects and views their redacted
// counterparts from redactObjects, aligned by position: a hidden view carries
// no identity, so the object's key has to come from the source. A key is
// shown only when EVERY recorded object carrying it is visible: two documents
// for the same object (or the same kind with and without a resolved resource)
// share a key, and the hidden one must not borrow its twin's visibility.
func redactOwnership(stored []gitops.OwnershipResult, objs []store.ReceiptObject, views []ReceiptObjectView, allowed objectAccess) ([]OwnershipView, int) {
	if allowed == nil {
		allowed = denyAll
	}
	shown := make(map[objectKey]bool, len(objs))
	if len(views) == len(objs) {
		for i, o := range objs {
			k := objectKey{o.Group, o.Kind, o.Namespace, o.Name}
			if views[i].Redacted {
				shown[k] = false
			} else if _, seen := shown[k]; !seen {
				shown[k] = true
			}
		}
	}
	out := make([]OwnershipView, 0, len(stored))
	redacted := 0
	for _, res := range stored {
		o := res.Object
		if !shown[objectKey{o.Group, o.Kind, o.Namespace, o.Name}] {
			redacted++
			continue
		}
		v := OwnershipView{OwnershipResult: res}
		v.Apps = nil
		kept := map[string]bool{}
		for _, app := range res.Apps {
			group, resource, ok := appResource(app.Kind)
			if !ok || !allowed(group, resource, app.Namespace) {
				v.RedactedApps++
				continue
			}
			v.Apps = append(v.Apps, app)
			kept[app.AppID] = true
		}
		v.Evidence = nil
		for _, ev := range res.Evidence {
			if ev.AppID == "" || kept[ev.AppID] {
				v.Evidence = append(v.Evidence, ev)
			}
		}
		if len(res.Apps) > 0 && len(v.Apps) == 0 {
			v.Controller = gitops.OwnedByNone
			v.Confidence = gitops.ConfidenceForbidden
			v.Reason = OwnershipReasonAppsRedacted
		}
		out = append(out, v)
	}
	return out, redacted
}

// appResource maps a confirming application's kind to the (group, resource)
// its `get` check runs against. An unknown kind cannot be re-authorized.
func appResource(kind string) (group, resource string, ok bool) {
	switch kind {
	case "Application":
		return gitops.ArgoApplicationGVR.Group, gitops.ArgoApplicationGVR.Resource, true
	case "Kustomization":
		return gitops.FluxKustomizationGVR.Group, gitops.FluxKustomizationGVR.Resource, true
	case "HelmRelease":
		return gitops.FluxHelmReleaseGVR.Group, gitops.FluxHelmReleaseGVR.Resource, true
	}
	return "", "", false
}

// summarize counts every recorded object, before redaction.
func summarize(r *store.ChangeReceipt) ReceiptSummary {
	s := ReceiptSummary{Total: r.DocumentCount}
	for _, o := range r.Objects {
		switch o.Action {
		case ActionCreated:
			s.Created++
		case ActionConfigured:
			s.Configured++
		case ActionUnchanged:
			s.Unchanged++
		default:
			s.Failed++
		}
	}
	if n := r.DocumentCount - len(r.Objects); n > 0 {
		s.NotRecorded = n
	}
	return s
}
