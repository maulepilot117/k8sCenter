package changes

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// unstructuredFromFuzz decodes fuzz bytes into an unstructured object. Inputs
// that don't decode to a JSON/YAML object are skipped: the seed corpus carries
// the structural diversity and the mutator explores around it. (Duplicated per
// package by convention; see docs/solutions/backend-resilience-conventions.md.)
func unstructuredFromFuzz(data []byte) (*unstructured.Unstructured, bool) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return &unstructured.Unstructured{Object: integralNumbers(m).(map[string]any)}, true
}

// integralNumbers mirrors what the dynamic client's JSON decoder does: a
// number without a fractional part arrives as int64, not float64. Without
// this every counter reads as 0 and the surge seed would be toothless.
func integralNumbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = integralNumbers(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = integralNumbers(val)
		}
		return x
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
		return x
	default:
		return v
	}
}

// FuzzCheckRollout asserts CheckRollout is crash-safe on arbitrary live-object
// shapes and keeps the D6 contract.
//
// Oracle A: no panic, whatever metadata/spec/status look like (wrong-typed
// fields, missing maps, conditions that are not objects). Contract: the
// result always carries CheckIDRolloutComplete and a non-zero ObservedAt; a
// kind outside apps/v1 {Deployment, StatefulSet, DaemonSet} is
// CheckInconclusive/kind_not_supported and NEVER CheckPass; a supported kind
// is pass/ok or warn/rollout_in_progress, nothing else; a pass is impossible
// while status.replicas (Deployment) differs from updatedReplicas, which is
// the old-ReplicaSet clause an earlier revision of this check lacked; and
// Evidence maps only "<Kind>/<Name>" to the object's UID, never content.
func FuzzCheckRollout(f *testing.F) {
	// Healthy Deployment: the only shape that may pass.
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"Deployment",
		"metadata":{"name":"web","namespace":"prod","uid":"u","generation":4},
		"spec":{"replicas":3},
		"status":{"observedGeneration":4,"replicas":3,"updatedReplicas":3,"availableReplicas":3,
		          "conditions":[{"type":"Available","status":"True"}]}}`))
	// Teeth: the surge state (old pod still serving). Must be warn, not pass.
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"Deployment",
		"metadata":{"name":"web","namespace":"prod","uid":"u","generation":2},
		"spec":{"replicas":1},
		"status":{"observedGeneration":2,"replicas":2,"updatedReplicas":1,"availableReplicas":1,
		          "conditions":[{"type":"Available","status":"True"}]}}`))
	// Teeth: a kind that looks healthy by every replica counter but is not a
	// supported workload. Must be inconclusive, not pass.
	f.Add([]byte(`{"apiVersion":"v1","kind":"ReplicationController",
		"metadata":{"name":"rc","uid":"u","generation":1},"spec":{"replicas":1},
		"status":{"observedGeneration":1,"replicas":1,"readyReplicas":1,"availableReplicas":1}}`))
	// Teeth: a Deployment in a foreign group.
	f.Add([]byte(`{"apiVersion":"acme.example.com/v1","kind":"Deployment",
		"metadata":{"name":"web","uid":"u"},"spec":{"replicas":1},
		"status":{"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Available","status":"True"}]}}`))
	// StatefulSet and DaemonSet, healthy.
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"StatefulSet",
		"metadata":{"name":"db","namespace":"prod","uid":"u","generation":2},"spec":{"replicas":3},
		"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":3,"readyReplicas":3,"currentRevision":"a","updateRevision":"a"}}`))
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"DaemonSet",
		"metadata":{"name":"agent","namespace":"kube-system","uid":"u","generation":1},
		"status":{"observedGeneration":1,"desiredNumberScheduled":5,"currentNumberScheduled":5,"updatedNumberScheduled":5,
		          "numberAvailable":5,"numberReady":5,"numberUnavailable":0}}`))
	// Malformed shapes: the type-assertion traps.
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"metadata":"oops"}`))
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","spec":[],"status":"x"}`))
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"generation":"four","uid":7},
		"spec":{"replicas":"3"},"status":{"observedGeneration":4.5,"replicas":[],"updatedReplicas":{},
		"availableReplicas":true,"conditions":"Available"}}`))
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","status":{"conditions":[1,"x",null,{"type":1,"status":true}]}}`))
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"StatefulSet","status":{"currentRevision":1,"updateRevision":[]}}`))
	f.Add([]byte(`{"apiVersion":"apps/v1","kind":"DaemonSet","status":{"desiredNumberScheduled":-1,"numberUnavailable":9223372036854775807}}`))

	now := time.Date(2026, 9, 10, 13, 15, 0, 0, time.UTC)

	f.Fuzz(func(t *testing.T, data []byte) {
		obj, ok := unstructuredFromFuzz(data)
		if !ok {
			return
		}
		got := CheckRollout(obj, SourceRef{ClusterID: "local"}, now) // Oracle A: must not panic

		if got.CheckID != CheckIDRolloutComplete || got.ObservedAt.IsZero() {
			t.Fatalf("envelope: %+v", got)
		}
		gvk := obj.GroupVersionKind()
		supported := gvk.Group == "apps" &&
			(gvk.Kind == "Deployment" || gvk.Kind == "StatefulSet" || gvk.Kind == "DaemonSet")
		if !supported {
			if got.Status != CheckInconclusive || got.Reason != ReasonKindNotSupported {
				t.Fatalf("unsupported %s/%s produced %s/%s", gvk.Group, gvk.Kind, got.Status, got.Reason)
			}
			return
		}
		switch {
		case got.Status == CheckPass && got.Reason == ReasonRolloutComplete:
		case got.Status == CheckWarn && got.Reason == ReasonRolloutInProgress:
		default:
			t.Fatalf("supported kind produced %s/%s", got.Status, got.Reason)
		}
		if gvk.Kind == "Deployment" && got.Status == CheckPass {
			// Re-derived independently of the production reader: a pass with
			// old-ReplicaSet pods left is the defect this check exists to catch.
			total, _, _ := unstructured.NestedInt64(obj.Object, "status", "replicas")
			updated, _, _ := unstructured.NestedInt64(obj.Object, "status", "updatedReplicas")
			if total != updated {
				t.Fatalf("Deployment passed with status.replicas=%d updatedReplicas=%d", total, updated)
			}
		}
		for k, v := range got.Evidence {
			if k != gvk.Kind+"/"+obj.GetName() || v != string(obj.GetUID()) {
				t.Fatalf("evidence is not identity-only: %q=%q", k, v)
			}
		}
	})
}
