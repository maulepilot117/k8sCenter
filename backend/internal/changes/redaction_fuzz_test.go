package changes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/gitops"
	"github.com/kubecenter/kubecenter/internal/store"
)

// The Q1 redaction contract, re-derived here from its wire literals rather
// than read from the production constants, so a widened view, a renamed
// reason or a dropped guard fails this oracle instead of agreeing with it.
const (
	fuzzReasonForbidden      = "forbidden"
	fuzzReasonSecretFiltered = "secret-filtered"
	fuzzReasonAppsRedacted   = "apps-redacted"
	fuzzCheckID              = "workload.rollout-complete"
)

// fuzzErrorClasses is the closed set a Secret-bearing receipt may emit in
// place of error text.
var fuzzErrorClasses = map[string]bool{
	"conflict": true, "forbidden": true, "invalid": true, "not_found": true, "indeterminate": true, "other": true,
}

// fuzzAppGVRs is the oracle's own statement of which application kinds can be
// re-authorized and against what. Any other kind must be removed.
var fuzzAppGVRs = map[string][2]string{
	"Application":   {"argoproj.io", "applications"},
	"Kustomization": {"kustomize.toolkit.fluxcd.io", "kustomizations"},
	"HelmRelease":   {"helm.toolkit.fluxcd.io", "helmreleases"},
}

// fuzzAppKinds is what the generator cycles through: the three known kinds
// plus two that are not. fuzzAppTools is the tool each kind belongs to, as
// gitops labels them.
var fuzzAppKinds = []string{"Application", "Kustomization", "HelmRelease", "Widget", ""}

var fuzzAppTools = map[string]gitops.Tool{
	"Application": gitops.ToolArgoCD, "Kustomization": gitops.ToolFluxCD, "HelmRelease": gitops.ToolFluxCD,
}

// expectedVerdict is the oracle's own statement of the recomputed verdict for
// an entry that lost at least one application: from the tools of the kept
// applications alone.
func expectedVerdict(kept []gitops.OwnedByApp) (controller, confidence, reason string) {
	var argo, flux bool
	for _, a := range kept {
		argo = argo || a.Tool == "argocd"
		flux = flux || a.Tool == "fluxcd"
	}
	switch {
	case argo && flux:
		return "both", "conflicting", "both-claim"
	case argo:
		return "argocd", "confirmed", "confirmed-argo-status"
	case flux:
		return "fluxcd", "confirmed", "confirmed-flux-inventory"
	default:
		return "none", "forbidden", fuzzReasonAppsRedacted
	}
}

// Fixed, non-attacker-controlled check fields. A check's status and reason
// are the verifier's, never the object's, so the fuzzer only picks among them.
var (
	fuzzStatuses   = []CheckStatus{CheckPass, CheckWarn, CheckFail, CheckInconclusive}
	fuzzReasons    = []string{ReasonRolloutComplete, ReasonRolloutInProgress, ReasonKindNotSupported, ReasonNotFound, ReasonTargetRecreated, ReasonReadForbidden, ReasonWindowExpired}
	fuzzSeverities = []Severity{"info", "warning", "critical"}
	fuzzObservedAt = time.Date(2026, 9, 10, 13, 15, 0, 0, time.UTC)
)

// receiptFromFuzz decodes records ('\n'-separated) of '|'-separated fields:
// group|version|resource|kind|namespace|name|uid|action|error|errorClass.
// Missing fields are empty; extra fields are ignored. Index is the record
// position.
func receiptFromFuzz(data []byte) []store.ReceiptObject {
	var objs []store.ReceiptObject
	for i, rec := range bytes.Split(data, []byte{'\n'}) {
		if i >= 32 {
			break
		}
		f := strings.Split(string(rec), "|")
		field := func(n int) string {
			if n < len(f) {
				return f[n]
			}
			return ""
		}
		objs = append(objs, store.ReceiptObject{
			Index: i, Group: field(0), Version: field(1), Resource: field(2), Kind: field(3),
			Namespace: field(4), Name: field(5), UID: field(6), Action: field(7), Error: field(8),
			ErrorClass: field(9), RecordedAt: fuzzObservedAt,
		})
	}
	return objs
}

// checksFromFuzz builds one verification check per object, with the
// object's identity in Source and Evidence exactly as the verifier records it.
func checksFromFuzz(objs []store.ReceiptObject) []CheckResult {
	checks := make([]CheckResult, 0, len(objs))
	for i, o := range objs {
		src := SourceRef{ClusterID: "local", Group: o.Group, Version: o.Version, Resource: o.Resource,
			Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID}
		checks = append(checks, CheckResult{
			CheckID:    fuzzCheckID,
			Status:     fuzzStatuses[i%len(fuzzStatuses)],
			Severity:   fuzzSeverities[i%len(fuzzSeverities)],
			Reason:     fuzzReasons[i%len(fuzzReasons)],
			Message:    "rollout not complete for " + o.Kind + " " + o.Name,
			Detail:     "observed in " + o.Namespace + ": " + o.Error,
			Source:     src,
			ObservedAt: fuzzObservedAt,
			Evidence:   map[string]string{o.Kind + "/" + o.Name: o.UID},
		})
	}
	return checks
}

// ownershipFromFuzz builds one stored ownership entry per object. Entry i
// carries i%3 confirming applications (so some entries are hints-only), each
// of a kind cycled from fuzzAppKinds and namespaced from the object's fields,
// plus one evidence row per app naming it and one hint row that names none.
func ownershipFromFuzz(objs []store.ReceiptObject) []gitops.OwnershipResult {
	out := make([]gitops.OwnershipResult, 0, len(objs))
	for i, o := range objs {
		res := gitops.OwnershipResult{
			Object:        gitops.ObjectRef{Group: o.Group, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name},
			Controller:    gitops.OwnedByArgoCD,
			Confidence:    gitops.ConfidenceConfirmed,
			Reason:        "confirmed-argo-status",
			IdentityBasis: "group-kind-namespace-name",
			Evidence:      []gitops.OwnershipEvidence{{Kind: gitops.EvidenceInstanceLabel, RawValue: o.Name}},
		}
		for a := 0; a < i%3; a++ {
			kind := fuzzAppKinds[(i+a)%len(fuzzAppKinds)]
			id := fmt.Sprintf("app:%d:%d:%s", i, a, o.Name)
			res.Apps = append(res.Apps, gitops.OwnedByApp{
				AppID: id, Tool: fuzzAppTools[kind], Kind: kind, Namespace: o.Namespace + "-gitops", Name: o.Name + "-app",
				Source: gitops.AppSource{RepoURL: "repo://" + id},
			})
			res.Evidence = append(res.Evidence, gitops.OwnershipEvidence{Kind: gitops.EvidenceArgoStatusResource, AppID: id})
		}
		out = append(out, res)
	}
	return out
}

// accessFromFuzz derives a deterministic, deny-biased predicate from the
// fuzzed bits: a tuple is allowed only when two bits selected by its hash are
// both set (about one tuple in four for random bits; 0 denies everything and
// ^0 allows everything).
func accessFromFuzz(bits uint64) objectAccess {
	return func(group, resource, namespace string) bool {
		h := fnv.New32a()
		_, _ = h.Write([]byte(group + "\x00" + resource + "\x00" + namespace))
		x := h.Sum32()
		return bits&(1<<(x%64)) != 0 && bits&(1<<((x>>6)%64)) != 0
	}
}

// expectedRedaction is the oracle's own statement of the per-object rule.
func expectedRedaction(kind, group, resource, namespace string, allowed objectAccess) string {
	if strings.EqualFold(kind, "Secret") && !allowed("", "secrets", namespace) {
		return fuzzReasonSecretFiltered
	}
	if resource == "" || !allowed(group, resource, namespace) {
		return fuzzReasonForbidden
	}
	return ""
}

// jsonString is s as it survives a JSON round trip (invalid UTF-8 becomes
// U+FFFD), for comparing against decoded output.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out string
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// FuzzReceiptRedaction guards the Q1 read-time redaction at its chokepoint.
//
// Oracle D (secret-leak masking): a view of an object the caller may not
// read marshals to exactly {"index":N,"redacted":true,"reason":R}; a check on
// such an object marshals to exactly its identity-free stub; a stored
// ownership entry for such an object is absent. Within a visible entry, an
// application the caller may not get (or of a kind that cannot be checked)
// is absent along with the evidence naming it, and an entry left with no
// application names no controller.
//
// Oracle C (enforcement): the decision to redact is re-derived from the
// oracle's own rule for every object and application (a Secret needs
// secrets-get; everything needs get on its own resource; no resource means
// hidden; an app needs get on its own GVR in its namespace), so a visible
// Secret is proof of a current secrets-get. For a Secret-bearing receipt, or
// any Secret object, no view carries "error"; a failed object carries only an
// "errorClass" from the closed set.
func FuzzReceiptRedaction(f *testing.F) {
	realistic := []byte("apps|v1|deployments|Deployment|prod|web|8b1c-uid|configured||\n" +
		"|v1|configmaps|ConfigMap|prod|cfg|cfg-uid|created||\n" +
		"|v1|secrets|Secret|prod|db-creds|sec-uid|configured||\n" +
		"apps|v1|deployments|Deployment|prod|api|api-uid|failed|invalid: error detail withheld for a Secret-bearing change|invalid")
	rawError := []byte("|v1|secrets|Secret|prod|db-creds|sec-uid|failed|admission webhook denied: data.password \"hunter2\" is too short|\n" +
		"apps|v1|deployments|Deployment|prod|web|w-uid|failed|Deployment.apps \"web\" is invalid: spec.replicas must be >= 0|\n" +
		"apps|v1|deployments|Deployment|prod|cut|c-uid|failed|context deadline exceeded|indeterminate\n" +
		"apps|v1|deployments|Deployment|prod|odd|o-uid|failed|conflict: stale|bogus-class")
	noResource := []byte("example.io|||Widget|prod|thing||failed|no matches for kind Widget|other\n" +
		"|v1||Secret|prod|orphan|o-uid|configured||")
	literalNames := []byte("index|true|redacted|forbidden|reason|secret-filtered|errorClass|configured|other|\n" +
		"apps|v1|deployments|redacted|index|forbidden|true|failed|forbidden: nope|\n" +
		"apps|v1|deployments|Deployment|apps-redacted|none|forbidden|configured||")
	clusterScoped := []byte("||namespaces|Namespace||team-a|ns-uid|created||\n" +
		"rbac.authorization.k8s.io|v1|clusterroles|ClusterRole||admin|cr-uid|configured||")
	for _, data := range [][]byte{realistic, rawError, noResource, literalNames, clusterScoped} {
		for _, bits := range []uint64{0, ^uint64(0), 0xA5A5A5A5A5A5A5A5, 0x0123456789ABCDEF} {
			f.Add(data, bits, false)
			f.Add(data, bits, true)
		}
	}
	f.Add([]byte{}, uint64(0), false)
	f.Add([]byte("\n\n|||"), ^uint64(0), true)
	// Regression seed (fuzzer-found): a hidden object next to a visible twin
	// with the same identity must not have its ownership entry shown.
	f.Add([]byte("||0\n"), ^uint64(0), true)
	f.Add([]byte("apps|v1|deployments|Deployment|prod|web|u1|configured||\napps|||Deployment|prod|web||failed|no mapping|other"), ^uint64(0), false)
	f.Add([]byte{0xff, 0xfe, '|', 0xc0, '\n', 0x80}, uint64(7), true)

	f.Fuzz(func(t *testing.T, data []byte, allowBits uint64, containsSecret bool) {
		objs := receiptFromFuzz(data)
		allowed := accessFromFuzz(allowBits)

		views, redacted := redactObjects(objs, containsSecret, allowed)
		if len(views) != len(objs) {
			t.Fatalf("%d views for %d objects", len(views), len(objs))
		}
		seen := 0
		for i, v := range views {
			o := objs[i]
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			want := expectedRedaction(o.Kind, o.Group, o.Resource, o.Namespace, allowed)
			if want != "" {
				seen++
				exact := fmt.Sprintf(`{"index":%d,"redacted":true,"reason":%q}`, o.Index, want)
				if string(raw) != exact {
					t.Fatalf("object %d must be hidden as %s, got %s (source %+v)", i, exact, raw, o)
				}
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if _, hidden := m["redacted"]; hidden {
				t.Fatalf("object %d is readable but marked redacted: %s", i, raw)
			}
			if _, has := m["reason"]; has {
				t.Fatalf("object %d is readable but carries a redaction reason: %s", i, raw)
			}
			if idx, _ := m["index"].(float64); int(idx) != o.Index {
				t.Fatalf("object %d index = %v", i, m["index"])
			}
			errText, hasErr := m["error"]
			class, hasClass := m["errorClass"]
			classStr, _ := class.(string)
			if hasClass && !fuzzErrorClasses[classStr] {
				t.Fatalf("object %d errorClass = %v outside the closed set: %s", i, class, raw)
			}
			if fuzzErrorClasses[o.ErrorClass] && classStr != o.ErrorClass {
				t.Fatalf("object %d must report its stored class %q, got %q", i, o.ErrorClass, classStr)
			}
			switch {
			case o.Error == "" && o.ErrorClass == "":
				if hasErr || hasClass {
					t.Fatalf("object %d has no error but emits one: %s", i, raw)
				}
			case containsSecret || strings.EqualFold(o.Kind, "Secret"):
				if hasErr {
					t.Fatalf("Secret rule: object %d emits error text: %s", i, raw)
				}
				if !hasClass {
					t.Fatalf("Secret rule: object %d must carry an errorClass: %s", i, raw)
				}
			default:
				if o.Error != "" && (!hasErr || errText != jsonString(t, o.Error)) {
					t.Fatalf("object %d must keep its error text: %s", i, raw)
				}
			}
		}
		if seen != redacted {
			t.Fatalf("redacted count = %d, oracle counted %d", redacted, seen)
		}

		checks := checksFromFuzz(objs)
		cviews, credacted := redactChecks(checks, allowed)
		if len(cviews) != len(checks) {
			t.Fatalf("%d check views for %d checks", len(cviews), len(checks))
		}
		seen = 0
		for i, v := range cviews {
			c := checks[i]
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			src := c.Source
			want := expectedRedaction(src.Kind, src.Group, src.Resource, src.Namespace, allowed)
			if want != "" {
				seen++
				exact := fmt.Sprintf(`{"checkId":%q,"status":%q,"severity":%q,"reason":%q,"redacted":true,"redactionReason":%q,"observedAt":%q}`,
					c.CheckID, string(c.Status), string(c.Severity), c.Reason, want, c.ObservedAt.Format(time.RFC3339Nano))
				if string(raw) != exact {
					t.Fatalf("check %d must be hidden as %s, got %s", i, exact, raw)
				}
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if _, hidden := m["redacted"]; hidden {
				t.Fatalf("check %d is readable but marked redacted: %s", i, raw)
			}
			if _, has := m["source"]; !has {
				t.Fatalf("check %d is readable but lost its source: %s", i, raw)
			}
		}
		if seen != credacted {
			t.Fatalf("redacted check count = %d, oracle counted %d", credacted, seen)
		}

		stored := ownershipFromFuzz(objs)
		oviews, oredacted := redactOwnership(stored, objs, views, allowed)
		// Which stored entries the oracle expects to see, in order: an entry is
		// shown only when every object sharing its (group, kind, namespace,
		// name) is visible, so a hidden object cannot borrow a twin's
		// visibility (found by the fuzzer: "||0\n" -- an unmappable document
		// next to a mapped one of the same empty identity).
		type identity struct{ group, kind, namespace, name string }
		hiddenKey := map[identity]bool{}
		for _, o := range objs {
			if expectedRedaction(o.Kind, o.Group, o.Resource, o.Namespace, allowed) != "" {
				hiddenKey[identity{o.Group, o.Kind, o.Namespace, o.Name}] = true
			}
		}
		var expect []int
		for i, o := range objs {
			if !hiddenKey[identity{o.Group, o.Kind, o.Namespace, o.Name}] {
				expect = append(expect, i)
			}
		}
		if len(oviews) != len(expect) || oredacted != len(objs)-len(expect) {
			t.Fatalf("ownership: %d views (redacted %d), oracle expects %d of %d", len(oviews), oredacted, len(expect), len(objs))
		}
		for n, v := range oviews {
			src := stored[expect[n]]
			if v.Object != src.Object {
				t.Fatalf("ownership view %d is for %+v, want %+v", n, v.Object, src.Object)
			}
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			keptIDs := map[string]bool{}
			wantKept := 0
			for _, app := range src.Apps {
				gvr, known := fuzzAppGVRs[app.Kind]
				if known && allowed(gvr[0], gvr[1], app.Namespace) {
					keptIDs[app.AppID] = true
					wantKept++
				}
			}
			if len(v.Apps) != wantKept || v.RedactedApps != len(src.Apps)-wantKept {
				t.Fatalf("ownership view %d apps = %+v (redacted %d), want %d kept of %d", n, v.Apps, v.RedactedApps, wantKept, len(src.Apps))
			}
			for _, app := range v.Apps {
				if !keptIDs[app.AppID] {
					t.Fatalf("ownership view %d discloses app %q the caller may not get: %s", n, app.AppID, raw)
				}
			}
			for _, ev := range v.Evidence {
				if ev.AppID != "" && !keptIDs[ev.AppID] {
					t.Fatalf("ownership view %d evidence names hidden app %q: %s", n, ev.AppID, raw)
				}
			}
			// Hidden app ids never appear anywhere in the entry, under any key.
			for _, app := range src.Apps {
				if !keptIDs[app.AppID] && strings.Contains(string(raw), jsonString(t, app.AppID)) {
					t.Fatalf("ownership view %d leaks hidden app id %q: %s", n, app.AppID, raw)
				}
			}
			if wantKept == 0 {
				if _, has := m["apps"]; has {
					t.Fatalf("ownership view %d has an apps key with nothing to show: %s", n, raw)
				}
			}
			if wantKept < len(src.Apps) {
				// Something was hidden: the verdict may describe only what remains.
				ctl, conf, reason := expectedVerdict(v.Apps)
				if m["controller"] != ctl || m["confidence"] != conf || m["reason"] != reason {
					t.Fatalf("ownership view %d lost an app; verdict must be %s/%s/%s: %s", n, ctl, conf, reason, raw)
				}
			} else if m["controller"] != string(src.Controller) || m["confidence"] != string(src.Confidence) || m["reason"] != src.Reason {
				t.Fatalf("ownership view %d lost nothing and must keep its verdict verbatim: %s", n, raw)
			}
		}
	})
}
