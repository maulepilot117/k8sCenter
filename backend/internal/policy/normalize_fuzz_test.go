package policy

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// unstructuredFromFuzz decodes fuzz bytes into an *unstructured.Unstructured.
// Inputs that don't decode to a JSON/YAML object are skipped — the seed corpus
// carries the structural diversity and the mutator explores around it.
func unstructuredFromFuzz(data []byte) (*unstructured.Unstructured, bool) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return &unstructured.Unstructured{Object: m}, true
}

// FuzzPolicyNormalizers asserts every Kyverno/Gatekeeper normalizer and
// violation extractor is crash-safe on arbitrary/adversarial unstructured
// input. Oracle: no panic; zero-values/empty slices are fine.
func FuzzPolicyNormalizers(f *testing.F) {
	// ── Realistic valid seeds ─────────────────────────────────────────────────

	// Kyverno ClusterPolicy: spec.rules + status.conditions (Ready=True)
	f.Add([]byte(`
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: disallow-host-path
  annotations:
    policies.kyverno.io/title: Disallow hostPath
    policies.kyverno.io/category: Pod Security Standards (Baseline)
    policies.kyverno.io/severity: medium
    policies.kyverno.io/description: >-
      Disabling hostPath volumes limits the ability to access host filesystem paths.
spec:
  validationFailureAction: Enforce
  rules:
    - name: host-path
      match:
        any:
          - resources:
              kinds:
                - Pod
      validate:
        message: HostPath volumes are not permitted.
        pattern:
          spec:
            =(volumes):
              - =(hostPath): "null"
status:
  conditions:
    - type: Ready
      status: "True"
`))

	// Kyverno PolicyReport: results with fail + pass entries, scope-based resource ref
	f.Add([]byte(`
apiVersion: wgpolicyk8s.io/v1alpha2
kind: PolicyReport
metadata:
  name: cpol-report-abc
  namespace: default
scope:
  apiVersion: v1
  kind: Pod
  name: my-pod
  namespace: default
results:
  - policy: disallow-host-path
    rule: host-path
    result: fail
    severity: medium
    message: HostPath volumes are not permitted.
    timestamp:
      seconds: 1775817991
      nanos: 0
  - policy: require-labels
    rule: check-labels
    result: pass
    severity: low
    message: Required labels found.
    timestamp:
      seconds: 1775817991
      nanos: 0
`))

	// Gatekeeper constraint: spec.match.kinds + spec.enforcementAction + status.violations
	f.Add([]byte(`
apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequiredLabels
metadata:
  name: prod-must-have-owner
  annotations:
    metadata.gatekeeper.sh/title: Required Labels
    metadata.gatekeeper.sh/severity: high
    metadata.gatekeeper.sh/category: Governance
    description: All production namespaces must have an owner label.
spec:
  enforcementAction: deny
  match:
    kinds:
      - apiGroups: [""]
        kinds:
          - Namespace
status:
  violations:
    - enforcementAction: deny
      kind: Namespace
      name: staging
      namespace: ""
      message: Missing required label owner.
    - enforcementAction: deny
      kind: Namespace
      name: dev
      namespace: ""
      message: Missing required label owner.
`))

	// PolicyReport with legacy per-result resources[] array (ClusterPolicyReport style)
	f.Add([]byte(`
apiVersion: wgpolicyk8s.io/v1alpha2
kind: ClusterPolicyReport
metadata:
  name: cpolr-xyz
results:
  - policy: k8srequiredlabels
    rule: check-for-labels
    result: warn
    severity: low
    message: Label 'app.kubernetes.io/name' not found.
    resources:
      - apiVersion: v1
        kind: Namespace
        name: legacy-app
  - policy: k8srequiredlabels
    rule: check-for-labels
    result: pass
    severity: low
    message: All required labels present.
    resources:
      - apiVersion: v1
        kind: Namespace
        name: modern-app
`))

	// Kyverno Policy (namespaced, not cluster-scoped) with match.all
	f.Add([]byte(`
apiVersion: kyverno.io/v1
kind: Policy
metadata:
  name: require-requests
  namespace: team-a
  annotations:
    policies.kyverno.io/severity: high
spec:
  validationFailureAction: Audit
  rules:
    - name: check-requests
      match:
        all:
          - resources:
              kinds:
                - Deployment
                - StatefulSet
      validate:
        message: CPU and memory requests are required.
        pattern:
          spec:
            template:
              spec:
                containers:
                  - resources:
                      requests:
                        memory: "?*"
                        cpu: "?*"
status:
  conditions:
    - type: Ready
      status: "False"
`))

	// ── Malformed / adversarial seeds ─────────────────────────────────────────

	// Completely empty object
	f.Add([]byte(`{}`))

	// metadata is a scalar string instead of an object
	f.Add([]byte(`{"metadata":"oops"}`))

	// spec is a list, status is a scalar string
	f.Add([]byte(`{"spec":[],"status":"x"}`))

	// spec.rules is a string, not a list
	f.Add([]byte(`{"spec":{"rules":"notalist"}}`))

	// status.violations and results are wrong types
	f.Add([]byte(`{"status":{"violations":{},"results":"notalist"}}`))

	// Deeply nested but no useful keys
	f.Add([]byte(`{"a":{"b":{"c":{"d":{"e":"f"}}}}}`))

	// spec.match.kinds contains non-map items
	f.Add([]byte(`{"spec":{"match":{"kinds":["string","another"]}}}`))

	// results contains non-map items
	f.Add([]byte(`{"results":["not-a-map",42,true,null]}`))

	// timestamp with negative values
	f.Add([]byte(`{"results":[{"result":"fail","timestamp":{"seconds":-999999,"nanos":-1}}]}`))

	// Giant string values (no crash even with very long fields)
	f.Add([]byte(`{"metadata":{"name":"` + string(make([]byte, 512)) + `"}}`))

	// status.conditions wrong types
	f.Add([]byte(`{"status":{"conditions":"notalist"}}`))

	// spec.match.any contains items missing resources key
	f.Add([]byte(`{"spec":{"rules":[{"name":"r","match":{"any":[{"noResources":true}]}}]}}`))

	// scope is a list instead of an object
	f.Add([]byte(`{"scope":["invalid","scope"]}`))

	// annotations is a list instead of a map
	f.Add([]byte(`{"metadata":{"annotations":["list","not","map"]}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}

		// Kyverno normalizers: both clusterScoped variants
		_ = NormalizeKyvernoPolicy(u, false)
		_ = NormalizeKyvernoPolicy(u, true)

		// Kyverno violation extractor
		_ = extractKyvernoViolations(u)

		// Gatekeeper normalizer
		_ = NormalizeGatekeeperConstraint(u, "K8sRequiredLabels")

		// Gatekeeper violation extractor
		_ = extractGatekeeperViolations(u, "K8sRequiredLabels")
	})
}
