package policy

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apimachineryjson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"
)

// loadFixture reads a YAML file from testdata/kyverno and parses it into an
// unstructured.Unstructured the same way client-go does for live CRD reads:
// YAML → JSON → apimachinery's int64-preserving json.Unmarshal. Going via the
// standard library's json package instead would turn every number into a
// float64 and break unstructured.NestedInt64 lookups on timestamps etc.
func loadFixture(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	path := filepath.Join("testdata", "kyverno", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	jsonBytes, err := yaml.YAMLToJSON(raw)
	if err != nil {
		t.Fatalf("yaml→json %s: %v", name, err)
	}
	obj := &unstructured.Unstructured{}
	if err := apimachineryjson.Unmarshal(jsonBytes, &obj.Object); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return obj
}

func TestNormalizeKyvernoPolicy_ModernClusterPolicy(t *testing.T) {
	obj := loadFixture(t, "clusterpolicy_disallow_capabilities.yaml")
	p := NormalizeKyvernoPolicy(obj, true)

	// Ready comes from status.conditions[type=Ready, status=True]
	if !p.Ready {
		t.Errorf("expected Ready=true from conditions[type=Ready], got false")
	}

	// Name is the title annotation; MatchKey is the raw k8s resource name
	if p.Name != "Disallow Capabilities" {
		t.Errorf("Name = %q, want %q", p.Name, "Disallow Capabilities")
	}
	if p.MatchKey != "disallow-capabilities" {
		t.Errorf("MatchKey = %q, want %q", p.MatchKey, "disallow-capabilities")
	}

	// ID, kind, engine
	if p.ID != "kyverno::disallow-capabilities" {
		t.Errorf("ID = %q, want kyverno::disallow-capabilities", p.ID)
	}
	if p.Kind != "ClusterPolicy" {
		t.Errorf("Kind = %q, want ClusterPolicy", p.Kind)
	}
	if p.Engine != EngineKyverno {
		t.Errorf("Engine = %q, want kyverno", p.Engine)
	}

	// validationFailureAction: Audit → Blocking = false
	if p.Blocking {
		t.Errorf("Blocking = true for Audit-mode policy, want false")
	}

	// Target kinds collected from match.any[].resources.kinds
	if len(p.TargetKinds) != 1 || p.TargetKinds[0] != "Pod" {
		t.Errorf("TargetKinds = %v, want [Pod]", p.TargetKinds)
	}

	// Severity + category from annotations
	if p.Severity != "medium" {
		t.Errorf("Severity = %q, want medium", p.Severity)
	}
	if p.Category != "Pod Security Standards (Baseline)" {
		t.Errorf("Category = %q, want Pod Security Standards (Baseline)", p.Category)
	}

	// Rule count
	if p.RuleCount != 1 {
		t.Errorf("RuleCount = %d, want 1", p.RuleCount)
	}

	// Pre-aggregation: ViolationCount is populated later in the handler
	if p.ViolationCount != 0 {
		t.Errorf("ViolationCount = %d at normalization time, want 0", p.ViolationCount)
	}
}

func TestNormalizeKyvernoPolicy_NotReady(t *testing.T) {
	// Synthetic: a ClusterPolicy whose Ready condition is False.
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "kyverno.io/v1",
		"kind":       "ClusterPolicy",
		"metadata":   map[string]interface{}{"name": "not-ready-policy"},
		"spec": map[string]interface{}{
			"validationFailureAction": "Enforce",
			"rules": []interface{}{map[string]interface{}{
				"name": "r",
				"match": map[string]interface{}{
					"all": []interface{}{
						map[string]interface{}{
							"resources": map[string]interface{}{
								"kinds": []interface{}{"Deployment", "StatefulSet"},
							},
						},
					},
				},
			}},
		},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "False"},
			},
		},
	}}
	p := NormalizeKyvernoPolicy(obj, true)

	if p.Ready {
		t.Errorf("Ready = true for conditions[Ready=False], want false")
	}
	if !p.Blocking {
		t.Errorf("Blocking = false for Enforce-mode policy, want true")
	}

	// match.all[] should also be collected
	sort.Strings(p.TargetKinds)
	got := p.TargetKinds
	if len(got) != 2 || got[0] != "Deployment" || got[1] != "StatefulSet" {
		t.Errorf("TargetKinds = %v, want [Deployment StatefulSet]", got)
	}
}

func TestExtractKyvernoViolations_ScopeBasedResourceRef(t *testing.T) {
	obj := loadFixture(t, "policyreport_failing.yaml")
	violations := extractKyvernoViolations(obj)

	// The fixture has 5 fail results and 7 pass results.
	if len(violations) != 5 {
		t.Fatalf("extracted %d violations, want 5", len(violations))
	}

	// Every violation should carry the top-level scope (Pod cilium-envoy-dh92s
	// in kube-system) rather than empty kind/name.
	for i, v := range violations {
		if v.Kind != "Pod" {
			t.Errorf("violation[%d] Kind = %q, want Pod", i, v.Kind)
		}
		if v.Name != "cilium-envoy-dh92s" {
			t.Errorf("violation[%d] Name = %q, want cilium-envoy-dh92s", i, v.Name)
		}
		if v.Namespace != "kube-system" {
			t.Errorf("violation[%d] Namespace = %q, want kube-system", i, v.Namespace)
		}
		if v.Engine != EngineKyverno {
			t.Errorf("violation[%d] Engine = %q, want kyverno", i, v.Engine)
		}
		if !v.Blocking {
			t.Errorf("violation[%d] Blocking = false for fail result, want true", i)
		}
		// timestamp {seconds: 1775817991, nanos: 0} → RFC3339 in UTC.
		if v.Timestamp != "2026-04-10T10:46:31Z" {
			t.Errorf("violation[%d] Timestamp = %q, want 2026-04-10T10:46:31Z", i, v.Timestamp)
		}
	}

	// Policy references: confirm we see the 5 failing policies by raw k8s name
	// (the same string that would be compared against NormalizedPolicy.MatchKey).
	gotPolicies := make(map[string]bool)
	for _, v := range violations {
		gotPolicies[v.Policy] = true
	}
	wantPolicies := []string{
		"disallow-capabilities",
		"disallow-host-namespaces",
		"disallow-host-path",
		"disallow-host-ports",
		"disallow-selinux",
	}
	for _, want := range wantPolicies {
		if !gotPolicies[want] {
			t.Errorf("missing violation for policy %q", want)
		}
	}
}

func TestExtractKyvernoViolations_PassingReportYieldsNothing(t *testing.T) {
	obj := loadFixture(t, "policyreport_passing.yaml")
	violations := extractKyvernoViolations(obj)
	if len(violations) != 0 {
		t.Errorf("got %d violations from all-passing report, want 0", len(violations))
	}
}

func TestPopulateViolationCounts_JoinByMatchKey(t *testing.T) {
	// End-to-end: parse a failing report, then run the same helper the handler
	// uses to join violations to policies. Verifies MatchKey is the join glue.
	policyObj := loadFixture(t, "clusterpolicy_disallow_capabilities.yaml")
	policy := NormalizeKyvernoPolicy(policyObj, true)

	reportObj := loadFixture(t, "policyreport_failing.yaml")
	violations := extractKyvernoViolations(reportObj)

	result := populateViolationCounts([]NormalizedPolicy{policy}, violations)

	if len(result) != 1 {
		t.Fatalf("populateViolationCounts returned %d policies, want 1", len(result))
	}
	// The report has exactly one fail result for disallow-capabilities.
	if result[0].ViolationCount != 1 {
		t.Errorf("ViolationCount = %d, want 1", result[0].ViolationCount)
	}
	// Original slice must not be mutated.
	if policy.ViolationCount != 0 {
		t.Errorf("source policy mutated (ViolationCount = %d), want 0", policy.ViolationCount)
	}
}
