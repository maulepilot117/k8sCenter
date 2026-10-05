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

// fuzzSecretSources are source-resource spellings that must select the
// Secret projection; fuzzOtherSources must not. The source is chosen by
// kindSel%3 (secrets when 0) and kindSel/3 picks the spelling, so a seed's
// source is a pure function of kindSel. The oracle re-derives the
// normalization in fuzzIsSecretsSource rather than calling the production
// predicate.
var (
	fuzzSecretSources = []string{"secrets", " Secrets ", "v1/secrets", "core/secrets"}
	fuzzOtherSources  = []string{"widgets", "pods", "secret", "apps/secrets"}
)

// fuzzShape selects which single Secret reference fuzzObject plants when
// planted is true (names in fuzzShapeNames).
func fuzzShape(kindSel uint8) int { return int(kindSel >> 4) }

var fuzzShapeNames = []string{
	"containers.env.secretKeyRef", "volumes.secret", "volumes.csi.nodePublishSecretRef", "volumes.rbd.secretRef",
	"volumes.azureFile.secretName", "volumes.projected.sources.secret", "imagePullSecrets", "initContainers.envFrom.secretRef",
	"ephemeralContainers.env.secretKeyRef", "containers.envFrom.secretRef", "volumes.cinder.secretRef", "volumes.cephfs.secretRef",
	"volumes.flexVolume.secretRef", "volumes.iscsi.secretRef", "volumes.scaleIO.secretRef", "volumes.storageos.secretRef",
}

// fuzzSoleReferenceSeeds are the seeds whose planted reference is the only
// thing that can mark the object derived: every entry has a non-secrets
// source and a non-Secret kind. The shape, kind and source columns are what
// the author intends; TestFuzzSeedSelectors proves the selector arithmetic
// produces exactly them, so a wrong selector or a stale comment cannot
// silently weaken a seed. Every shape appears at least once as a Pod.
var fuzzSoleReferenceSeeds = []struct {
	sel    uint8
	shape  string
	kind   string
	source string
	raw    string
}{
	{2, "containers.env.secretKeyRef", "Pod", "widgets", ""},
	{16, "volumes.secret", "Pod", "pods", ""},
	{37, "volumes.csi.nodePublishSecretRef", "Pod", "widgets", "apiVersion: v1\nkind: Pod\nspec:\n  volumes:\n  - name: tls\n    secret:\n      secretName: leaked\n"}, // raw volumes are discarded
	{38, "volumes.csi.nodePublishSecretRef", "CronJob", "widgets", ""},
	{58, "volumes.rbd.secretRef", "Pod", "apps/secrets", ""},
	{50, "volumes.rbd.secretRef", "Deployment", "widgets", ""},
	{65, "volumes.azureFile.secretName", "Pod", "pods", ""},
	{71, "volumes.azureFile.secretName", "Deployment", "apps/secrets", ""},
	{86, "volumes.projected.sources.secret", "Pod", "widgets", ""},
	{100, "imagePullSecrets", "Pod", "pods", ""},
	{121, "initContainers.envFrom.secretRef", "Pod", "widgets", ""},
	{128, "ephemeralContainers.env.secretKeyRef", "Pod", "secret", ""},
	{134, "ephemeralContainers.env.secretKeyRef", "Deployment", "widgets", ""},
	{149, "containers.envFrom.secretRef", "Pod", "pods", ""},
	{163, "volumes.cinder.secretRef", "Pod", "secret", ""},
	{184, "volumes.cephfs.secretRef", "Pod", "pods", ""},
	{205, "volumes.flexVolume.secretRef", "Pod", "widgets", ""},
	{212, "volumes.iscsi.secretRef", "Pod", "secret", ""},
	{211, "volumes.iscsi.secretRef", "Deployment", "secret", ""},
	{226, "volumes.scaleIO.secretRef", "Pod", "apps/secrets", ""},
	{247, "volumes.storageos.secretRef", "Pod", "secret", ""},
}

// TestFuzzSeedSelectors pins the arithmetic behind fuzzSoleReferenceSeeds
// and the kind-less / secrets-source seeds used by FuzzIncidentRedaction.
func TestFuzzSeedSelectors(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range fuzzSoleReferenceSeeds {
		shape := fuzzShapeNames[fuzzShape(s.sel)]
		kind := fuzzKinds[int(s.sel)%len(fuzzKinds)]
		source := fuzzSourceFor(s.sel)
		if shape != s.shape || kind != s.kind || source != s.source {
			t.Errorf("seed %d: computes to shape=%q kind=%q source=%q; table says %q %q %q", s.sel, shape, kind, source, s.shape, s.kind, s.source)
		}
		if fuzzIsSecretsSource(source) || kind == "Secret" {
			t.Errorf("seed %d: not a sole-reference seed (kind=%q source=%q)", s.sel, kind, source)
		}
		if kind == "Pod" {
			seen[shape] = true
		}
	}
	for _, name := range fuzzShapeNames {
		if !seen[name] {
			t.Errorf("shape %q has no Pod sole-reference seed", name)
		}
	}
	// Kind-less and secrets-source seeds, as FuzzIncidentRedaction adds them.
	for sel, want := range map[uint8][2]string{27: {"", " Secrets "}, 34: {"", "apps/secrets"}, 3: {"CronJob", " Secrets "}, 66: {"CronJob", "v1/secrets"}} {
		kind := fuzzKinds[int(sel)%len(fuzzKinds)]
		if source := fuzzSourceFor(sel); kind != want[0] || source != want[1] {
			t.Errorf("seed %d: kind=%q source=%q; want %q %q", sel, kind, source, want[0], want[1])
		}
	}
}

func fuzzSourceFor(kindSel uint8) string {
	if kindSel%3 == 0 {
		return fuzzSecretSources[int(kindSel/3)%len(fuzzSecretSources)]
	}
	return fuzzOtherSources[int(kindSel/3)%len(fuzzOtherSources)]
}

func fuzzIsSecretsSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "secrets", "v1/secrets", "core/secrets", "/secrets":
		return true
	}
	return false
}

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
	podSpec := map[string]any{
		"containers": []any{container},
		"volumes":    []any{map[string]any{"name": a, "projected": map[string]any{"sources": []any{}}}},
	}
	if planted {
		// Exactly one Secret reference is planted, selected by fuzzShape, so
		// each shape is the sole deciding reference and a detection gap for
		// that shape fails the oracle. The raw-spec merge above never writes
		// the pod-spec locations, so nothing else can reference a Secret.
		secretRef = true
		keyRefEnv := map[string]any{"name": c, "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": a, "key": fuzzCanaryKey}}}
		secretRefVolume := func(source string) []any {
			return []any{map[string]any{"name": a, source: map[string]any{"secretRef": map[string]any{"name": fuzzCanary}}}}
		}
		switch fuzzShape(kindSel) {
		case 0: // containers[].env[].valueFrom.secretKeyRef
			container["env"] = append(container["env"].([]any), keyRefEnv)
		case 1: // volumes[].secret
			podSpec["volumes"] = []any{map[string]any{"name": a, "secret": map[string]any{"secretName": fuzzCanary}}}
		case 2: // volumes[].csi.nodePublishSecretRef
			podSpec["volumes"] = []any{map[string]any{"name": a, "csi": map[string]any{"driver": b, "nodePublishSecretRef": map[string]any{"name": fuzzCanary}}}}
		case 3: // volumes[].rbd.secretRef
			podSpec["volumes"] = secretRefVolume("rbd")
		case 4: // volumes[].azureFile.secretName
			podSpec["volumes"] = []any{map[string]any{"name": a, "azureFile": map[string]any{"secretName": fuzzCanary, "shareName": c}}}
		case 5: // volumes[].projected.sources[].secret
			podSpec["volumes"] = []any{map[string]any{"name": a, "projected": map[string]any{"sources": []any{map[string]any{"secret": map[string]any{"name": fuzzCanary}}}}}}
		case 6: // imagePullSecrets
			podSpec["imagePullSecrets"] = []any{map[string]any{"name": fuzzCanary}}
		case 7: // initContainers[].envFrom[].secretRef
			podSpec["initContainers"] = []any{map[string]any{"name": c, "image": b, "envFrom": []any{map[string]any{"secretRef": map[string]any{"name": fuzzCanary}}}}}
		case 8: // ephemeralContainers[].env[].valueFrom.secretKeyRef
			podSpec["ephemeralContainers"] = []any{map[string]any{"name": c, "image": b, "env": []any{keyRefEnv}}}
		case 9: // containers[].envFrom[].secretRef
			container["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": fuzzCanary}}}
		case 10:
			podSpec["volumes"] = secretRefVolume("cinder")
		case 11:
			podSpec["volumes"] = secretRefVolume("cephfs")
		case 12:
			podSpec["volumes"] = secretRefVolume("flexVolume")
		case 13:
			podSpec["volumes"] = secretRefVolume("iscsi")
		case 14:
			podSpec["volumes"] = secretRefVolume("scaleIO")
		case 15:
			podSpec["volumes"] = secretRefVolume("storageos")
		}
	}

	spec := map[string]any{"replicas": float64(kindSel), "unknown": fuzzCanary, "hidden": map[string]any{c: fuzzCanary}}
	// A raw spec contributes its arbitrary shape (wrong-typed replicas,
	// selectors, junk) except at the pod-spec locations, which the generator
	// owns so that exactly one Secret reference is ever present.
	if rawSpec, _ := obj["spec"].(map[string]any); rawSpec != nil {
		for k, v := range rawSpec {
			switch k {
			case "containers", "initContainers", "ephemeralContainers", "volumes", "imagePullSecrets", "template", "jobTemplate":
				continue
			}
			spec[k] = v
		}
	}
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
	f.Add(uint8(66), "p", "ns", "x", []byte("spec:\n  replicas: oops\n  selector: [1]\n"), uint16(0), true) // CronJob with source v1/secrets

	// Sole-reference seeds: one per table row; TestFuzzSeedSelectors proves
	// each row's shape, kind and source.
	for _, s := range fuzzSoleReferenceSeeds {
		f.Add(s.sel, "api-1", "payments", "x", []byte(s.raw), uint16(0), true)
	}
	// Kind-less Secret selected by source alone (27 → " Secrets ").
	f.Add(uint8(27), "db-creds", "payments", "x", []byte(""), uint16(0), false)
	// Kind-less object with a non-secrets source (34 → "apps/secrets") and
	// nothing planted: SecretDerived must be false.
	f.Add(uint8(34), "api-1", "payments", "x", []byte(""), uint16(0), false)
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
		source := fuzzSourceFor(kindSel)
		// The Secret projection is selected by kind OR source (a typed
		// informer object has no kind), re-derived here.
		isSecretObj := kind == "Secret" || fuzzIsSecretsSource(source)

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
		if isSecretObj {
			for _, k := range []string{"spec", "status", "type", "immutable"} {
				if _, ok := out[k]; ok {
					t.Fatalf("Secret projection (kind=%q source=%q) carries %q: %s", kind, source, k, js)
				}
			}
			if !hasRuleID(meta.Rules, fuzzRuleSecret) {
				t.Fatalf("Secret (kind=%q source=%q) without %s rule: %+v", kind, source, fuzzRuleSecret, meta)
			}
		} else if hasRuleID(meta.Rules, fuzzRuleSecret) {
			t.Fatalf("non-Secret (kind=%q source=%q) recorded %s: %+v", kind, source, fuzzRuleSecret, meta)
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
		// reference or a Secret object always marks it, and nothing else
		// does (the generator plants at most one reference and overwrites
		// metadata and spec, so a raw shape cannot smuggle one in).
		if wantDerived := secretRef || isSecretObj; meta.SecretDerived != wantDerived {
			t.Fatalf("SecretDerived=%v, want %v (planted=%v shape=%s kind=%q source=%q)", meta.SecretDerived, wantDerived, secretRef, fuzzShapeNames[fuzzShape(kindSel)], kind, source)
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
