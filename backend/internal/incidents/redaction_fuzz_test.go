package incidents

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// The contract is re-derived here from its wire literals and numeric
// bounds rather than read from the production constants, so a renamed rule,
// a widened allowlist or a dropped guard fails this oracle instead of
// agreeing with it.
const (
	fuzzCanary        = "CANARY-5c2d8e1a-plaintext-secret"
	fuzzCanaryKey     = "PROD_DB_PASSWORD"
	fuzzLastApplied   = "kubectl.kubernetes.io/last-applied-configuration"
	fuzzRuleSecret    = "secret-values"
	fuzzRuleTruncated = "truncated"
	fuzzFieldBound    = 4096 // per-string bound before the size ladder runs
	fuzzMinBound      = 64
	fuzzDefaultBound  = 1 << 20
)

var fuzzKinds = []string{"Secret", "Deployment", "Pod", "CronJob", "ConfigMap", "Widget", ""}

// fuzzForbiddenKeys must never appear at a structural position of a
// projection (labels and annotations are user-named and are skipped).
var fuzzForbiddenKeys = map[string]bool{
	fuzzLastApplied: true, "data": true, "stringData": true, "env": true, "envFrom": true,
	"volumes": true, "imagePullSecrets": true, "ephemeralContainers": true,
}

// fuzzSanitize is the oracle's own statement of text sanitization: invalid
// UTF-8 runs become U+FFFD, then C0/C1 controls and DEL other than '\n' and
// '\t' are removed.
func fuzzSanitize(s string) string {
	s = strings.ToValidUTF8(s, "�")
	var sb strings.Builder
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// fuzzObject builds one object from the fuzz inputs. The canary is planted
// ONLY where it must not survive: data, stringData, the last-applied
// annotation (top-level and on the pod template), a non-allowlisted
// annotation, a plain env value, unknown spec fields, and an unknown
// condition field. Fuzz strings go into allowlisted locations (name,
// namespace, labels, a kubecenter.io annotation, condition message) after
// any accidental canary or key name is scrubbed, so a surviving canary is
// always a leak. It returns the object, whether a Secret reference was
// planted, whether raw contributed an arbitrary shape, and the longest
// sanitized string placed in an allowlisted location.
func fuzzObject(kindSel uint8, a, b, c string, raw []byte, planted bool) (obj map[string]any, secretRef, rawUsed bool, longest int) {
	// Bound the harness, not the target: a string beyond 64 KiB or a YAML
	// document beyond 16 KiB adds no coverage (the field bound is 4 KiB) and
	// only makes the YAML decode in this generator the slowest thing here.
	scrub := func(s string) string {
		s = strings.ReplaceAll(s, fuzzCanary, "x")
		s = strings.ReplaceAll(s, fuzzCanaryKey, "y")
		return truncateRunes(s, 64<<10)
	}
	a, b, c = scrub(a), scrub(b), scrub(c)
	if len(raw) > 16<<10 {
		raw = raw[:16<<10]
	}
	longest = max(len(fuzzSanitize(a)), len(fuzzSanitize(b)), len(fuzzSanitize(c)))
	kind := fuzzKinds[int(kindSel)%len(fuzzKinds)]

	obj = map[string]any{}
	if len(raw) > 0 && !bytes.Contains(raw, []byte(fuzzCanary)) && !bytes.Contains(raw, []byte(fuzzCanaryKey)) {
		// An arbitrary shape for crash safety. It cannot hold the canary, so
		// anything it contributes to the output is not a leak.
		var m map[string]any
		if err := yaml.Unmarshal(raw, &m); err == nil && m != nil {
			obj = m
			rawUsed = true
		}
	}
	obj["kind"] = kind
	obj["metadata"] = map[string]any{
		"name":      a,
		"namespace": b,
		"labels":    map[string]any{a: b, "app": c},
		"annotations": map[string]any{
			fuzzLastApplied:      `{"stringData":{"` + fuzzCanaryKey + `":"` + fuzzCanary + `"}}`,
			"example.com/" + a:   fuzzCanary,
			"kubecenter.io/" + b: c,
		},
	}

	container := map[string]any{"name": a, "image": b, "env": []any{
		map[string]any{"name": "PLAIN", "value": fuzzCanary},
	}}
	if planted {
		container["env"] = append(container["env"].([]any), map[string]any{"name": c, "valueFrom": map[string]any{
			"secretKeyRef": map[string]any{"name": a, "key": fuzzCanaryKey},
		}})
		secretRef = true
	}
	podSpec := map[string]any{
		"containers": []any{container},
		"volumes":    []any{map[string]any{"name": a, "projected": map[string]any{"sources": []any{}}}},
	}
	if planted && kindSel&0x80 != 0 {
		podSpec["volumes"] = []any{map[string]any{"name": a, "secret": map[string]any{"secretName": fuzzCanary}}}
	}

	spec := map[string]any{"replicas": float64(kindSel), "unknown": fuzzCanary, "hidden": map[string]any{c: fuzzCanary}}
	switch kind {
	case "Pod":
		for k, v := range podSpec {
			spec[k] = v
		}
	case "CronJob":
		spec["jobTemplate"] = map[string]any{"spec": map[string]any{"template": map[string]any{"spec": podSpec}}}
	default:
		spec["template"] = map[string]any{"spec": podSpec, "metadata": map[string]any{"annotations": map[string]any{fuzzLastApplied: fuzzCanary}}}
	}
	obj["spec"] = spec
	obj["status"] = map[string]any{
		"phase":      c,
		"conditions": []any{map[string]any{"type": a, "status": "True", "message": c, "secret": fuzzCanary}},
	}
	obj["data"] = map[string]any{fuzzCanaryKey: fuzzCanary, a: b}
	obj["stringData"] = map[string]any{fuzzCanaryKey + "-str": fuzzCanary}
	return obj, secretRef, rawUsed, longest
}

// forbiddenKeyIn returns the first forbidden key found at a structural
// position of the projection, or "".
func forbiddenKeyIn(v any, inUserMap bool) string {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if !inUserMap && fuzzForbiddenKeys[k] {
				return k
			}
			if found := forbiddenKeyIn(e, k == "labels" || k == "annotations"); found != "" {
				return found
			}
		}
	case []any:
		for _, e := range x {
			if found := forbiddenKeyIn(e, false); found != "" {
				return found
			}
		}
	}
	return ""
}

func FuzzIncidentRedaction(f *testing.F) {
	secretYAML := []byte("apiVersion: v1\nkind: Secret\ntype: Opaque\nimmutable: true\n")
	deploymentYAML := []byte("apiVersion: apps/v1\nkind: Deployment\nspec:\n  selector:\n    matchLabels:\n      app: api\n  strategy:\n    type: RollingUpdate\n")
	podProjectedYAML := []byte("apiVersion: v1\nkind: Pod\nspec:\n  imagePullSecrets:\n  - name: regcred\n  volumes:\n  - name: tls\n    projected:\n      sources:\n      - secret:\n          name: tls\n")

	// Teeth-via-mutation seeds: realistic maskedSecret-style Secrets, a
	// Deployment with secretKeyRef env, a Pod with projected secret volumes,
	// then malformed variants (wrong types, nil interiors, control
	// characters, invalid UTF-8, oversize strings, tiny bounds).
	f.Add(uint8(0), "db-creds", "payments", "api", secretYAML, uint16(0), true)
	f.Add(uint8(1), "api", "payments", "ghcr.io/acme/api:1.2.3", deploymentYAML, uint16(0), true)
	f.Add(uint8(2), "api-1", "payments", "Running", podProjectedYAML, uint16(0), false)
	f.Add(uint8(3), "nightly", "batch", "CronJob", []byte(""), uint16(0), true)
	f.Add(uint8(4), "cfg", "default", "ConfigMap", []byte("data:\n  k: v\n"), uint16(0), false)
	f.Add(uint8(5), "w", "ns", "Widget", []byte("spec:\n  credentials:\n    password: hunter2\n"), uint16(0), false)
	f.Add(uint8(0), "", "", "", []byte("metadata: oops\n"), uint16(0), false)
	f.Add(uint8(2), "p", "ns", "x", []byte("spec: [1, 2]\nstatus: 7\n"), uint16(0), true)
	f.Add(uint8(1), "a\x00b", "n\x1bs", "m\u0085sg\xff", []byte("{\"metadata\":{\"labels\":\"x\",\"ownerReferences\":[null,\"junk\"]}}"), uint16(0), true)
	f.Add(uint8(2), strings.Repeat("n", 5000), "ns", strings.Repeat("m", 9000), []byte(""), uint16(0), false)
	f.Add(uint8(2), strings.Repeat("\x00", 5000), "ns", "x", []byte(""), uint16(0), false)
	f.Add(uint8(1), "api", "payments", "api", deploymentYAML, uint16(1), true)
	f.Add(uint8(2), "api-1", "payments", strings.Repeat("é", 300), podProjectedYAML, uint16(200), false)
	f.Add(uint8(0x82), "p", "ns", "x", []byte("spec:\n  containers: oops\n  initContainers:\n  - null\n  - env: {}\n"), uint16(0), true)
	f.Add(uint8(6), "k", "v", "z", []byte("spec:\n  template:\n    spec: null\n  jobTemplate: []\n"), uint16(0), true)
	f.Add(uint8(2), "env", "data", "stringData", []byte(""), uint16(0), false)

	f.Fuzz(func(t *testing.T, kindSel uint8, a, b, c string, raw []byte, boundSel uint16, planted bool) {
		maxBytes := fuzzDefaultBound
		if boundSel != 0 {
			maxBytes = fuzzMinBound + int(boundSel)
		}
		r, err := NewRedactor(maxBytes)
		if err != nil {
			t.Fatalf("NewRedactor(%d): %v", maxBytes, err)
		}
		obj, secretRef, rawUsed, longest := fuzzObject(kindSel, a, b, c, raw, planted)
		kind, _ := obj["kind"].(string)
		source := "widgets"
		if kindSel%3 == 0 {
			source = "secrets"
		}

		// Oracle A: no panic (an unrecovered panic fails the run).
		out, meta := r.RedactObject(obj, source)
		if out == nil {
			t.Fatal("nil projection")
		}
		js, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("projection does not marshal: %v", err)
		}

		// Oracle D: the canary never survives, key names never leak, and the
		// last-applied annotation and Secret payload keys are absent.
		if bytes.Contains(js, []byte(fuzzCanary)) {
			t.Fatalf("canary leaked: %s", js)
		}
		if bytes.Contains(js, []byte(fuzzCanaryKey)) {
			t.Fatalf("secret key name leaked: %s", js)
		}
		if k := forbiddenKeyIn(out, false); k != "" {
			t.Fatalf("forbidden key %q in projection: %s", k, js)
		}
		if kind == "Secret" {
			for _, k := range []string{"spec", "status", "type", "immutable"} {
				if _, ok := out[k]; ok {
					t.Fatalf("Secret projection carries %q: %s", k, js)
				}
			}
			if !hasRuleID(meta.Rules, fuzzRuleSecret) {
				t.Fatalf("Secret without %s rule: %+v", fuzzRuleSecret, meta)
			}
		}

		// Oracle B: output size <= maxBytes; RedactText is bounded, valid
		// UTF-8, a prefix of the sanitized input, and honest about cuts.
		if len(js) > maxBytes {
			t.Fatalf("projection is %d bytes, bound %d", len(js), maxBytes)
		}
		for _, s := range []string{a, b, c, string(raw)} {
			text, cut := r.RedactText(s)
			want := fuzzSanitize(s)
			if len(text) > maxBytes || !strings.HasPrefix(want, text) || strings.ToValidUTF8(text, "?") != text {
				t.Fatalf("RedactText(%q) = %q (%d bytes, bound %d)", s, text, len(text), maxBytes)
			}
			if cut != (len(want) > maxBytes) || (!cut && text != want) {
				t.Fatalf("RedactText cut=%v for sanitized length %d, bound %d", cut, len(want), maxBytes)
			}
		}

		// Oracle T: a cut is never silent, and nothing is reported cut when
		// nothing could have been. A sanitized string over the field bound in
		// an allowlisted location is always a cut; a projection that differs
		// from the same object's projection under the default ceiling was
		// reduced for size and is a cut; a small fixed-shape object under the
		// default ceiling is never cut.
		if longest > fuzzFieldBound && !meta.Truncated {
			t.Fatalf("string of %d bytes exceeded the field bound but Truncated is false", longest)
		}
		if meta.Truncated != hasRuleID(meta.Rules, fuzzRuleTruncated) {
			t.Fatalf("Truncated=%v disagrees with rules %v", meta.Truncated, meta.Rules)
		}
		if !rawUsed && longest <= 64 && maxBytes == fuzzDefaultBound && meta.Truncated {
			t.Fatalf("spurious truncation on a small object: %+v %s", meta, js)
		}
		if maxBytes != fuzzDefaultBound {
			wide, _ := NewRedactor(fuzzDefaultBound)
			ref, refMeta := wide.RedactObject(obj, source)
			refJS, _ := json.Marshal(ref)
			if !bytes.Equal(refJS, js) && !meta.Truncated {
				t.Fatalf("projection reduced from %d to %d bytes without Truncated", len(refJS), len(js))
			}
			if refMeta.SecretDerived != meta.SecretDerived || refMeta.FieldsRemoved != meta.FieldsRemoved {
				t.Fatalf("size bound changed non-size metadata: %+v vs %+v", refMeta, meta)
			}
		}

		// Secret derivation is decided on the original object: a planted
		// reference, a Secret kind, or a secrets source always marks it.
		if (secretRef || kind == "Secret" || source == "secrets") && !meta.SecretDerived {
			t.Fatalf("SecretDerived=false with planted=%v kind=%q source=%q", secretRef, kind, source)
		}

		// Envelope truthfulness and determinism.
		if meta.Applied != (len(meta.Rules) > 0) {
			t.Fatalf("Applied=%v with rules %v", meta.Applied, meta.Rules)
		}
		if meta.FieldsRemoved < 0 {
			t.Fatalf("negative FieldsRemoved: %+v", meta)
		}
		again, againMeta := r.RedactObject(obj, source)
		againJS, _ := json.Marshal(again)
		if !bytes.Equal(againJS, js) || againMeta.Truncated != meta.Truncated || againMeta.FieldsRemoved != meta.FieldsRemoved {
			t.Fatalf("non-deterministic: %s vs %s", js, againJS)
		}
	})
}

func hasRuleID(rules []string, id string) bool {
	for _, r := range rules {
		if r == id {
			return true
		}
	}
	return false
}
