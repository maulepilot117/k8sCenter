package incidents

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
)

const (
	canaryValue   = "CANARY-9f1e4c7b-secret-value"
	canaryKeyName = "PROD_DB_PASSWORD"
)

func mustRedactor(t *testing.T, maxBytes int) *Redactor {
	t.Helper()
	r, err := NewRedactor(maxBytes)
	if err != nil {
		t.Fatalf("NewRedactor(%d): %v", maxBytes, err)
	}
	return r
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func metadata(name, ns string, extra map[string]any) map[string]any {
	m := map[string]any{"name": name, "namespace": ns, "uid": "uid-" + name}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// secretFixture is the shape maskedSecret receives: a Secret with Data,
// StringData, and the kubectl last-applied annotation carrying stringData in
// plaintext.
func secretFixture() map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata": metadata("db-creds", "payments", map[string]any{
			"annotations": map[string]any{
				LastAppliedConfigAnnotation: `{"apiVersion":"v1","kind":"Secret","stringData":{"` + canaryKeyName + `":"` + canaryValue + `"}}`,
				"kubecenter.io/note":        "kept",
			},
			"labels": map[string]any{"app": "db"},
		}),
		"data":       map[string]any{canaryKeyName: canaryValue, "username": "admin"},
		"stringData": map[string]any{"token": canaryValue},
	}
}

func deploymentFixture(podSpec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   metadata("api", "payments", nil),
		"spec": map[string]any{
			"replicas": float64(3),
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "api"}},
				"spec":     podSpec,
			},
		},
	}
}

func podFixture(podSpec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   metadata("api-1", "payments", nil),
		"spec":       podSpec,
	}
}

func containerWithSecretEnv() map[string]any {
	return map[string]any{
		"name":  "api",
		"image": "ghcr.io/acme/api:1.2.3",
		"env": []any{
			map[string]any{"name": "DB_PASSWORD", "valueFrom": map[string]any{
				"secretKeyRef": map[string]any{"name": "db-creds", "key": canaryKeyName},
			}},
			map[string]any{"name": "PLAIN", "value": canaryValue},
		},
	}
}

func hasRule(meta RedactionMeta, rule string) bool { return slices.Contains(meta.Rules, rule) }

func TestNewRedactorValidatesMaxBytes(t *testing.T) {
	for _, n := range []int{0, -1, MinMaxBytes - 1} {
		if _, err := NewRedactor(n); err == nil {
			t.Errorf("NewRedactor(%d): want error", n)
		}
	}
	if _, err := NewRedactor(MinMaxBytes); err != nil {
		t.Errorf("NewRedactor(MinMaxBytes): %v", err)
	}
	if DefaultMaxBytes != 1<<20 {
		t.Errorf("DefaultMaxBytes = %d; want the 1 MiB per-item ceiling (P14)", DefaultMaxBytes)
	}
}

// Parity with resources.maskedSecret's observable guarantees: values never
// survive, StringData is covered as well as Data, last-applied is stripped.
// This package is stricter: key names do not survive either (P11.3).
func TestRedactSecretMasksDataAndStringData(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	out, meta := r.RedactObject(secretFixture(), "secrets")
	js := marshal(t, out)

	if strings.Contains(js, canaryValue) {
		t.Fatalf("secret value survived: %s", js)
	}
	if strings.Contains(js, canaryKeyName) || strings.Contains(js, "username") || strings.Contains(js, "token") {
		t.Fatalf("secret key name survived: %s", js)
	}
	for _, k := range []string{"data", "stringData", "type"} {
		if _, ok := out[k]; ok {
			t.Errorf("output still has %q", k)
		}
	}
	md, _ := out["metadata"].(map[string]any)
	if md["name"] != "db-creds" || md["namespace"] != "payments" {
		t.Errorf("metadata identity not projected: %v", md)
	}
	ann, _ := md["annotations"].(map[string]any)
	if _, ok := ann[LastAppliedConfigAnnotation]; ok {
		t.Errorf("last-applied survived on Secret")
	}
	if ann["kubecenter.io/note"] != "kept" {
		t.Errorf("allowlisted annotation dropped: %v", ann)
	}
	if !meta.SecretDerived {
		t.Error("SecretDerived = false for a Secret")
	}
	if !meta.Applied || !hasRule(meta, RuleSecretValues) || !hasRule(meta, RuleLastAppliedConfig) {
		t.Errorf("meta = %+v; want applied with secret-values and last-applied-config", meta)
	}
}

func TestRedactSecretByKindAloneIsMetadataOnly(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := map[string]any{
		"kind":     "Secret",
		"metadata": metadata("s", "ns", nil),
		"spec":     map[string]any{"replicas": float64(1), "containers": []any{map[string]any{"name": "x", "image": "y"}}},
		"status":   map[string]any{"phase": "Bound"},
	}
	out, meta := r.RedactObject(obj, "")
	if _, ok := out["spec"]; ok {
		t.Errorf("Secret projection carried spec: %v", out)
	}
	if _, ok := out["status"]; ok {
		t.Errorf("Secret projection carried status: %v", out)
	}
	if !meta.SecretDerived || !hasRule(meta, RuleSecretValues) {
		t.Errorf("meta = %+v", meta)
	}
}

func TestRedactStripsLastAppliedConfigOnEveryKind(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	for _, kind := range []string{"Deployment", "ConfigMap", "Widget", ""} {
		obj := map[string]any{
			"apiVersion": "example.io/v1",
			"kind":       kind,
			"metadata": metadata("x", "ns", map[string]any{
				"annotations": map[string]any{
					LastAppliedConfigAnnotation: `{"stringData":{"k":"` + canaryValue + `"}}`,
				},
			}),
		}
		out, meta := r.RedactObject(obj, "")
		js := marshal(t, out)
		if strings.Contains(js, canaryValue) || strings.Contains(js, LastAppliedConfigAnnotation) {
			t.Errorf("kind %q: last-applied survived: %s", kind, js)
		}
		if !hasRule(meta, RuleLastAppliedConfig) {
			t.Errorf("kind %q: rule %s not recorded: %+v", kind, RuleLastAppliedConfig, meta)
		}
		if meta.SecretDerived {
			t.Errorf("kind %q: SecretDerived should be false", kind)
		}
	}
}

func TestRedactUsesAllowlistNotDenylist(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := map[string]any{
		"apiVersion": "example.io/v1",
		"kind":       "Widget",
		"metadata":   metadata("w", "ns", map[string]any{"managedFields": []any{"x"}}),
		"spec": map[string]any{
			"replicas":    float64(2),
			"credentials": map[string]any{"password": canaryValue},
			"endpoint":    "https://" + canaryValue,
		},
		"status":     map[string]any{"phase": "Ready", "lastToken": canaryValue},
		"unexpected": canaryValue,
	}
	out, meta := r.RedactObject(obj, "widgets")
	js := marshal(t, out)
	if strings.Contains(js, canaryValue) {
		t.Fatalf("unknown field survived projection: %s", js)
	}
	spec, _ := out["spec"].(map[string]any)
	if spec["replicas"] != float64(2) {
		t.Errorf("spec.replicas not kept: %v", out)
	}
	st, _ := out["status"].(map[string]any)
	if st["phase"] != "Ready" {
		t.Errorf("status.phase not kept: %v", out)
	}
	if !hasRule(meta, RuleFieldAllowlist) || meta.FieldsRemoved == 0 {
		t.Errorf("meta = %+v", meta)
	}
}

func TestRedactMarksSecretDerivedForSecretKeyRef(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := deploymentFixture(map[string]any{"containers": []any{containerWithSecretEnv()}})
	out, meta := r.RedactObject(obj, "deployments")
	if !meta.SecretDerived {
		t.Fatalf("SecretDerived = false; meta = %+v", meta)
	}
	js := marshal(t, out)
	if strings.Contains(js, canaryValue) || strings.Contains(js, canaryKeyName) || strings.Contains(js, "env") {
		t.Errorf("env survived projection: %s", js)
	}
}

func TestRedactMarksSecretDerivedForImagePullSecrets(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := podFixture(map[string]any{
		"containers":       []any{map[string]any{"name": "a", "image": "i"}},
		"imagePullSecrets": []any{map[string]any{"name": "regcred"}},
	})
	_, meta := r.RedactObject(obj, "pods")
	if !meta.SecretDerived {
		t.Fatalf("SecretDerived = false; meta = %+v", meta)
	}
	// An empty list is not a reference.
	obj = podFixture(map[string]any{"imagePullSecrets": []any{}})
	if _, meta = r.RedactObject(obj, "pods"); meta.SecretDerived {
		t.Error("empty imagePullSecrets marked derived")
	}
}

func TestRedactMarksSecretDerivedInEveryPodSpecLocation(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	envFromSecret := map[string]any{"name": "c", "envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "s"}}}}
	secretVolume := map[string]any{"name": "v", "secret": map[string]any{"secretName": "s"}}
	projectedVolume := map[string]any{"name": "v", "projected": map[string]any{"sources": []any{
		map[string]any{"configMap": map[string]any{"name": "cm"}},
		map[string]any{"secret": map[string]any{"name": "s"}},
	}}}
	cases := map[string]struct {
		obj    map[string]any
		source string
		want   bool
	}{
		"pod initContainers envFrom":           {podFixture(map[string]any{"initContainers": []any{envFromSecret}}), "pods", true},
		"pod ephemeralContainers secretKeyRef": {podFixture(map[string]any{"ephemeralContainers": []any{containerWithSecretEnv()}}), "pods", true},
		"pod volumes secret":                   {podFixture(map[string]any{"volumes": []any{secretVolume}}), "pods", true},
		"pod projected volume secret source":   {podFixture(map[string]any{"volumes": []any{projectedVolume}}), "pods", true},
		"deployment template initContainers":   {deploymentFixture(map[string]any{"initContainers": []any{envFromSecret}}), "deployments", true},
		"deployment template volumes":          {deploymentFixture(map[string]any{"volumes": []any{secretVolume}}), "deployments", true},
		"cronjob jobTemplate": {map[string]any{"kind": "CronJob", "metadata": metadata("cj", "ns", nil), "spec": map[string]any{
			"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{containerWithSecretEnv()},
			}}}},
		}}, "cronjobs", true},
		"source resource secrets": {map[string]any{"kind": "Widget", "metadata": metadata("w", "ns", nil)}, "secrets", true},
		"kind Secret":             {map[string]any{"kind": "Secret", "metadata": metadata("s", "ns", nil)}, "", true},
		"plain pod":               {podFixture(map[string]any{"containers": []any{map[string]any{"name": "a", "image": "i"}}}), "pods", false},
		"configMap env only": {podFixture(map[string]any{"containers": []any{map[string]any{"name": "a", "env": []any{
			map[string]any{"name": "X", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "cm", "key": "k"}}},
		}}}}), "pods", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, meta := r.RedactObject(tc.obj, tc.source)
			if meta.SecretDerived != tc.want {
				t.Errorf("SecretDerived = %v; want %v", meta.SecretDerived, tc.want)
			}
		})
	}
}

// A malformed sibling must neither panic nor clear a detection made elsewhere
// in the same object.
func TestRedactSecretDerivedSurvivesMalformedShapes(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	cases := map[string]map[string]any{
		"containers is a string": podFixture(map[string]any{"containers": "oops", "volumes": []any{map[string]any{"secret": map[string]any{}}}}),
		"nil container entry":    podFixture(map[string]any{"containers": []any{nil, containerWithSecretEnv()}}),
		"env is a map":           podFixture(map[string]any{"containers": []any{map[string]any{"env": map[string]any{}}, containerWithSecretEnv()}}),
		"valueFrom nil":          podFixture(map[string]any{"containers": []any{map[string]any{"env": []any{map[string]any{"valueFrom": nil}}}}, "imagePullSecrets": []any{"x"}}),
		"template is a list":     deploymentFixture(nil),
		"volumes after bad projected": podFixture(map[string]any{"volumes": []any{
			map[string]any{"projected": map[string]any{"sources": "bad"}},
			map[string]any{"projected": map[string]any{"sources": []any{nil, map[string]any{"secret": map[string]any{}}}}},
		}}),
	}
	cases["template is a list"]["spec"].(map[string]any)["template"] = []any{"x"}
	cases["template is a list"]["spec"].(map[string]any)["containers"] = []any{containerWithSecretEnv()}
	for name, obj := range cases {
		t.Run(name, func(t *testing.T) {
			_, meta := r.RedactObject(obj, "")
			if !meta.SecretDerived {
				t.Errorf("SecretDerived cleared by a malformed sibling")
			}
		})
	}
}

func TestRedactProjectsContainersForPodsAndTemplates(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	pod := podFixture(map[string]any{
		"containers":     []any{map[string]any{"name": "a", "image": "img:1", "args": []any{canaryValue}}},
		"initContainers": []any{map[string]any{"name": "init", "image": "img:0"}},
	})
	out, _ := r.RedactObject(pod, "pods")
	spec, _ := out["spec"].(map[string]any)
	want := `{"containers":[{"image":"img:1","name":"a"}],"initContainers":[{"image":"img:0","name":"init"}]}`
	if got := marshal(t, spec); got != want {
		t.Errorf("pod spec projection = %s; want %s", got, want)
	}

	dep := deploymentFixture(map[string]any{
		"containers": []any{map[string]any{"name": "a", "image": "img:1"}, "not-a-container"},
	})
	out, meta := r.RedactObject(dep, "deployments")
	spec, _ = out["spec"].(map[string]any)
	want = `{"replicas":3,"template":{"spec":{"containers":[{"image":"img:1","name":"a"}]}}}`
	if got := marshal(t, spec); got != want {
		t.Errorf("deployment spec projection = %s; want %s", got, want)
	}
	if meta.FieldsRemoved == 0 {
		t.Errorf("non-map container entry and template.metadata should count as removed: %+v", meta)
	}
}

func TestRedactAnnotationAllowlist(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := map[string]any{
		"kind": "Deployment",
		"metadata": metadata("d", "ns", map[string]any{"annotations": map[string]any{
			"kubecenter.io/owner":               "team-a",
			"deployment.kubernetes.io/revision": "4",
			"kubectl.kubernetes.io/restartedAt": "2026-01-01",
			"example.com/token":                 canaryValue,
			"not-a-string":                      float64(1),
		}}),
	}
	out, meta := r.RedactObject(obj, "")
	ann, _ := out["metadata"].(map[string]any)["annotations"].(map[string]any)
	want := `{"deployment.kubernetes.io/revision":"4","kubecenter.io/owner":"team-a"}`
	if got := marshal(t, ann); got != want {
		t.Errorf("annotations = %s; want %s", got, want)
	}
	if !hasRule(meta, RuleAnnotationAllowlist) {
		t.Errorf("rule %s not recorded: %+v", RuleAnnotationAllowlist, meta)
	}
	if hasRule(meta, RuleLastAppliedConfig) {
		t.Errorf("last-applied-config recorded although absent: %+v", meta)
	}
}

func TestRedactOwnerReferencesKeepOnlyIdentity(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := map[string]any{
		"kind": "ReplicaSet",
		"metadata": metadata("rs", "ns", map[string]any{"ownerReferences": []any{
			map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "api", "uid": "u1", "controller": true, "blockOwnerDeletion": true},
			"junk",
		}}),
	}
	out, meta := r.RedactObject(obj, "")
	refs := out["metadata"].(map[string]any)["ownerReferences"]
	want := `[{"apiVersion":"apps/v1","controller":true,"kind":"Deployment","name":"api","uid":"u1"}]`
	if got := marshal(t, refs); got != want {
		t.Errorf("ownerReferences = %s; want %s", got, want)
	}
	if meta.FieldsRemoved != 2 {
		t.Errorf("FieldsRemoved = %d; want 2 (blockOwnerDeletion + junk entry)", meta.FieldsRemoved)
	}
}

func TestRedactTruncationIsFlagged(t *testing.T) {
	const maxBytes = 512
	r := mustRedactor(t, maxBytes)
	conds := make([]any, 0, 40)
	for i := 0; i < 40; i++ {
		conds = append(conds, map[string]any{"type": "Available", "status": "True", "message": strings.Repeat("m", 200)})
	}
	labels := map[string]any{}
	for i := 0; i < 100; i++ {
		labels["label-"+strings.Repeat("k", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26))] = strings.Repeat("v", 50)
	}
	obj := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   metadata(strings.Repeat("n", 5000), "ns", map[string]any{"labels": labels}),
		"status":     map[string]any{"conditions": conds},
	}
	out, meta := r.RedactObject(obj, "deployments")
	js := marshal(t, out)
	if len(js) > maxBytes {
		t.Errorf("output %d bytes exceeds maxBytes %d", len(js), maxBytes)
	}
	if !meta.Truncated || !hasRule(meta, RuleTruncated) {
		t.Errorf("truncation not flagged: %+v", meta)
	}

	// The same object under the default ceiling is still truncated: the name
	// exceeds the per-field bound, so the cut is flagged even without the
	// size ladder running.
	_, meta = mustRedactor(t, DefaultMaxBytes).RedactObject(obj, "deployments")
	if !meta.Truncated {
		t.Errorf("per-field cut not flagged: %+v", meta)
	}

	// Nothing cut: no flag.
	_, meta = mustRedactor(t, DefaultMaxBytes).RedactObject(podFixture(map[string]any{}), "pods")
	if meta.Truncated || hasRule(meta, RuleTruncated) {
		t.Errorf("spurious truncation: %+v", meta)
	}
}

func TestRedactOversizeFloorIsEmptyNeverPartial(t *testing.T) {
	r := mustRedactor(t, MinMaxBytes)
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   metadata(strings.Repeat("n", 300), strings.Repeat("s", 300), nil),
	}
	out, meta := r.RedactObject(obj, "pods")
	if js := marshal(t, out); len(js) > MinMaxBytes {
		t.Errorf("output %d bytes exceeds bound %d: %s", len(js), MinMaxBytes, js)
	}
	if !meta.Truncated {
		t.Errorf("floor not flagged: %+v", meta)
	}
}

func TestRedactHandlesInvalidUTF8AndControlChars(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	in := "ok\x00\x01\x1b[31mred\x7f\u0085\n\ttab\xff\xfeend"
	got, truncated := r.RedactText(in)
	// A run of invalid bytes collapses to one U+FFFD (strings.ToValidUTF8);
	// U+0085 is a C1 control and is removed.
	want := "ok[31mred\n\ttab�end"
	if got != want || truncated {
		t.Errorf("RedactText(%q) = %q, %v; want %q, false", in, got, truncated, want)
	}

	obj := map[string]any{
		"kind": "Pod",
		"metadata": metadata("p\x00od", "n\x1bs", map[string]any{
			"labels":      map[string]any{"ke\x01y": "va\u009flue\xff"},
			"annotations": map[string]any{"kubecenter.io/no\x02te": "x\x7fy"},
		}),
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "message": "boom\x00\x1b"}}},
	}
	out, meta := r.RedactObject(obj, "pods")
	js := marshal(t, out)
	want = `{"kind":"Pod","metadata":{"annotations":{"kubecenter.io/note":"xy"},"labels":{"key":"value�"},"name":"pod","namespace":"ns","uid":"uid-pod"},"status":{"conditions":[{"message":"boom","type":"Ready"}]}}`
	if js != want {
		t.Errorf("projection = %s\n       want %s", js, want)
	}
	if !hasRule(meta, RuleTextSanitized) {
		t.Errorf("rule %s not recorded: %+v", RuleTextSanitized, meta)
	}
}

func TestRedactTextTruncatesAtRuneBoundary(t *testing.T) {
	r := mustRedactor(t, MinMaxBytes)
	in := strings.Repeat("é", MinMaxBytes) // 2 bytes each, 2*MinMaxBytes total
	got, truncated := r.RedactText(in)
	if !truncated {
		t.Fatal("truncated = false")
	}
	if len(got) > MinMaxBytes || len(got) != MinMaxBytes {
		t.Errorf("len = %d; want %d", len(got), MinMaxBytes)
	}
	if strings.ToValidUTF8(got, "?") != got {
		t.Errorf("truncation split a rune: %q", got)
	}
	in = strings.Repeat("日", MinMaxBytes) // 3 bytes each; bound is not a multiple of 3
	got, _ = r.RedactText(in)
	if len(got) != MinMaxBytes-MinMaxBytes%3 || strings.ToValidUTF8(got, "?") != got {
		t.Errorf("3-byte rune truncation: len=%d %q", len(got), got)
	}
	if got, truncated := r.RedactText(""); got != "" || truncated {
		t.Errorf("RedactText(\"\") = %q, %v", got, truncated)
	}
}

func TestRedactionMetaCountsRemovedFields(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":          "api",
			"namespace":     "ns",
			"generation":    float64(7), // 1
			"managedFields": []any{},    // 2
			"annotations": map[string]any{
				LastAppliedConfigAnnotation: "{}", // 3
				"example.com/x":             "y",  // 4
				"kubecenter.io/keep":        "yes",
			},
		},
		"spec": map[string]any{
			"replicas": float64(2),
			"selector": map[string]any{}, // 5
			"strategy": map[string]any{}, // 6
			"template": map[string]any{
				"metadata": map[string]any{}, // 7
				"spec": map[string]any{
					"serviceAccountName": "sa", // 8
					"containers": []any{
						map[string]any{"name": "a", "image": "i", "ports": []any{}, "env": []any{}}, // 9, 10
					},
				},
			},
		},
		"status": map[string]any{
			"replicas":           float64(2),
			"observedGeneration": float64(7), // 11
			"conditions": []any{
				map[string]any{"type": "Available", "status": "True", "lastUpdateTime": "t"}, // 12
			},
		},
	}
	out, meta := r.RedactObject(obj, "deployments")
	if meta.FieldsRemoved != 12 {
		t.Errorf("FieldsRemoved = %d; want 12 (%s)", meta.FieldsRemoved, marshal(t, out))
	}
	wantRules := []string{RuleLastAppliedConfig, RuleAnnotationAllowlist, RuleFieldAllowlist}
	if !slices.Equal(meta.Rules, wantRules) {
		t.Errorf("Rules = %v; want %v", meta.Rules, wantRules)
	}
	if meta.Truncated || meta.SecretDerived || !meta.Applied {
		t.Errorf("meta = %+v", meta)
	}
}

func TestRedactionRulesMatchWhatWasApplied(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	clean := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   metadata("p", "ns", map[string]any{"labels": map[string]any{"app": "p"}}),
		"spec":       map[string]any{"containers": []any{map[string]any{"name": "a", "image": "i"}}},
		"status":     map[string]any{"phase": "Running"},
	}
	out, meta := r.RedactObject(clean, "pods")
	if meta.Applied || len(meta.Rules) != 0 || meta.FieldsRemoved != 0 || meta.Truncated || meta.SecretDerived {
		t.Errorf("clean object produced rules: %+v", meta)
	}
	if marshal(t, out) != marshal(t, clean) {
		t.Errorf("clean object changed by projection:\n got %s\nwant %s", marshal(t, out), marshal(t, clean))
	}

	// Each rule fires alone when only its trigger is present.
	only := func(obj map[string]any, rule string) {
		t.Helper()
		_, meta := r.RedactObject(obj, "pods")
		if !slices.Equal(meta.Rules, []string{rule}) {
			t.Errorf("rules for %s trigger = %v; want [%s]", rule, meta.Rules, rule)
		}
	}
	only(map[string]any{"kind": "Pod", "metadata": metadata("p", "ns", map[string]any{"annotations": map[string]any{LastAppliedConfigAnnotation: "x"}})}, RuleLastAppliedConfig)
	only(map[string]any{"kind": "Pod", "metadata": metadata("p", "ns", map[string]any{"annotations": map[string]any{"a/b": "x"}})}, RuleAnnotationAllowlist)
	only(map[string]any{"kind": "Pod", "metadata": metadata("p", "ns", nil), "extra": 1}, RuleFieldAllowlist)
	only(map[string]any{"kind": "Pod", "metadata": metadata("p\x00", "ns", nil)}, RuleTextSanitized)
	only(map[string]any{"kind": "Pod", "metadata": metadata(strings.Repeat("n", 5000), "ns", nil)}, RuleTruncated)
	only(map[string]any{"kind": "Secret", "metadata": metadata("s", "ns", nil)}, RuleSecretValues)
}

func TestRedactNilAndEmptyObjects(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	cases := map[string]map[string]any{
		"nil":                 nil,
		"empty":               {},
		"metadata string":     {"metadata": "oops"},
		"metadata nil":        {"metadata": nil},
		"spec list":           {"spec": []any{1, 2}},
		"status number":       {"status": float64(3)},
		"kind not a string":   {"kind": float64(1), "metadata": map[string]any{"name": float64(2)}},
		"labels not a map":    {"metadata": map[string]any{"labels": "x", "annotations": []any{}}},
		"conditions not list": {"status": map[string]any{"conditions": map[string]any{"type": "x"}}},
		"nested nils":         {"spec": map[string]any{"template": map[string]any{"spec": nil}, "containers": []any{nil}}},
	}
	for name, obj := range cases {
		t.Run(name, func(t *testing.T) {
			out, meta := r.RedactObject(obj, "")
			if out == nil {
				t.Fatal("nil projection")
			}
			b := marshal(t, out)
			if len(b) > DefaultMaxBytes {
				t.Errorf("oversize: %d", len(b))
			}
			if meta.SecretDerived || meta.Truncated {
				t.Errorf("meta = %+v", meta)
			}
		})
	}
	// Deep nesting in a non-allowlisted subtree is dropped without recursion.
	deep := map[string]any{}
	cur := deep
	for i := 0; i < 100000; i++ {
		next := map[string]any{}
		cur["x"] = next
		cur = next
	}
	out, meta := r.RedactObject(map[string]any{"kind": "Pod", "metadata": metadata("p", "ns", nil), "spec": map[string]any{"deep": deep}}, "pods")
	if _, ok := out["spec"].(map[string]any)["deep"]; ok {
		t.Error("deep subtree survived")
	}
	if meta.FieldsRemoved != 1 {
		t.Errorf("FieldsRemoved = %d; want 1", meta.FieldsRemoved)
	}
}

// A non-finite number cannot be marshalled; it is dropped as a field, not
// allowed to collapse the whole projection through the size ladder.
func TestRedactDropsNonFiniteNumbers(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	obj := map[string]any{
		"kind":     "Deployment",
		"metadata": metadata("d", "ns", nil),
		"spec":     map[string]any{"replicas": math.NaN()},
		"status":   map[string]any{"replicas": math.Inf(1), "readyReplicas": float64(2)},
	}
	out, meta := r.RedactObject(obj, "deployments")
	want := `{"kind":"Deployment","metadata":{"name":"d","namespace":"ns","uid":"uid-d"},"spec":{},"status":{"readyReplicas":2}}`
	if got := marshal(t, out); got != want {
		t.Errorf("projection = %s; want %s", got, want)
	}
	if meta.FieldsRemoved != 2 || meta.Truncated {
		t.Errorf("meta = %+v; want FieldsRemoved=2, Truncated=false", meta)
	}
}

func TestRedactObjectDoesNotMutateInput(t *testing.T) {
	r := mustRedactor(t, DefaultMaxBytes)
	in := secretFixture()
	before := marshal(t, in)
	r.RedactObject(in, "secrets")
	if after := marshal(t, in); after != before {
		t.Errorf("input mutated:\n before %s\n after  %s", before, after)
	}
}

func TestRedactionMetaWireFormat(t *testing.T) {
	meta := RedactionMeta{Applied: true, Rules: []string{RuleSecretValues}, FieldsRemoved: 2, Truncated: false, SecretDerived: true}
	want := `{"applied":true,"rules":["secret-values"],"fieldsRemoved":2,"truncated":false,"secretDerived":true}`
	if got := marshal(t, meta); got != want {
		t.Errorf("wire = %s; want %s", got, want)
	}
	want = `{"applied":false,"fieldsRemoved":0,"truncated":false,"secretDerived":false}`
	if got := marshal(t, RedactionMeta{}); got != want {
		t.Errorf("zero wire = %s; want %s", got, want)
	}
	for id, want := range map[string]string{
		RuleSecretValues: "secret-values", RuleLastAppliedConfig: "last-applied-config",
		RuleAnnotationAllowlist: "annotation-allowlist", RuleFieldAllowlist: "field-allowlist",
		RuleTextSanitized: "text-sanitized", RuleTruncated: "truncated",
	} {
		if id != want {
			t.Errorf("rule id %q; want %q", id, want)
		}
	}
}
