package changes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/store"
)

// The Q1 redaction contract, re-derived here from its wire literals rather
// than read from the production constants, so a widened view, a renamed
// reason or a dropped guard fails this oracle instead of agreeing with it.
const (
	fuzzReasonForbidden      = "forbidden"
	fuzzReasonSecretFiltered = "secret-filtered"
	fuzzCheckID              = "workload.rollout-complete"
)

// fuzzErrorClasses is the closed set a Secret-bearing receipt may emit in
// place of error text.
var fuzzErrorClasses = map[string]bool{
	"conflict": true, "forbidden": true, "invalid": true, "not_found": true, "other": true,
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
// group|version|resource|kind|namespace|name|uid|action|error. Missing fields
// are empty; extra fields are ignored. Index is the record position.
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
			RecordedAt: fuzzObservedAt,
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
// such an object marshals to exactly its identity-free stub. Neither can carry
// a name, namespace, kind, uid, error, message, detail, source or evidence.
//
// Oracle C (enforcement): the decision to redact is re-derived from the
// oracle's own rule for every object (a Secret needs secrets-get; everything
// needs get on its own resource; no resource means hidden), so a visible
// Secret is proof of a current secrets-get. For a Secret-bearing receipt, or
// any Secret object, no view carries "error"; a failed object carries only an
// "errorClass" from the closed set.
func FuzzReceiptRedaction(f *testing.F) {
	realistic := []byte("apps|v1|deployments|Deployment|prod|web|8b1c-uid|configured|\n" +
		"|v1|configmaps|ConfigMap|prod|cfg|cfg-uid|created|\n" +
		"|v1|secrets|Secret|prod|db-creds|sec-uid|configured|\n" +
		"apps|v1|deployments|Deployment|prod|api|api-uid|failed|invalid: error detail withheld for a Secret-bearing change")
	rawError := []byte("|v1|secrets|Secret|prod|db-creds|sec-uid|failed|admission webhook denied: data.password \"hunter2\" is too short\n" +
		"apps|v1|deployments|Deployment|prod|web|w-uid|failed|Deployment.apps \"web\" is invalid: spec.replicas must be >= 0")
	noResource := []byte("example.io|||Widget|prod|thing||failed|no matches for kind Widget\n" +
		"|v1||Secret|prod|orphan|o-uid|configured|")
	literalNames := []byte("index|true|redacted|forbidden|reason|secret-filtered|errorClass|configured|other\n" +
		"apps|v1|deployments|redacted|index|forbidden|true|failed|forbidden: nope")
	clusterScoped := []byte("||namespaces|Namespace||team-a|ns-uid|created|\n" +
		"rbac.authorization.k8s.io|v1|clusterroles|ClusterRole||admin|cr-uid|configured|")
	for _, data := range [][]byte{realistic, rawError, noResource, literalNames, clusterScoped} {
		for _, bits := range []uint64{0, ^uint64(0), 0xA5A5A5A5A5A5A5A5, 0x0123456789ABCDEF} {
			f.Add(data, bits, false)
			f.Add(data, bits, true)
		}
	}
	f.Add([]byte{}, uint64(0), false)
	f.Add([]byte("\n\n|||"), ^uint64(0), true)
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
			switch {
			case o.Error == "":
				if hasErr || hasClass {
					t.Fatalf("object %d has no error but emits one: %s", i, raw)
				}
			case containsSecret || strings.EqualFold(o.Kind, "Secret"):
				if hasErr {
					t.Fatalf("Secret rule: object %d emits error text: %s", i, raw)
				}
				if c, _ := class.(string); !hasClass || !fuzzErrorClasses[c] {
					t.Fatalf("Secret rule: object %d errorClass = %v, want one of the closed set: %s", i, class, raw)
				}
			default:
				if !hasErr || errText != jsonString(t, o.Error) {
					t.Fatalf("object %d must keep its error text: %s", i, raw)
				}
				if hasClass {
					if c, _ := class.(string); !fuzzErrorClasses[c] {
						t.Fatalf("object %d errorClass = %v outside the closed set", i, class)
					}
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
	})
}
