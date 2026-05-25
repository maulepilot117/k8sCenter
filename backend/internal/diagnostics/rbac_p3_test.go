package diagnostics

import "testing"

// TestRelatedRBAC_Allowances locks the contract for the P3-3 security audit:
// nil RelatedRBAC means "no gating" (legacy/test path); zero-valued non-nil
// RelatedRBAC denies every related resolution; explicit fields permit.
func TestRelatedRBAC_Allowances(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		r                 *RelatedRBAC
		wantPods          bool
		wantReplicaSets   bool
	}{
		{
			name:            "nil — legacy permissive",
			r:               nil,
			wantPods:        true,
			wantReplicaSets: true,
		},
		{
			name:            "zero value — fail-closed deny-all",
			r:               &RelatedRBAC{},
			wantPods:        false,
			wantReplicaSets: false,
		},
		{
			name:            "pods only",
			r:               &RelatedRBAC{Pods: true},
			wantPods:        true,
			wantReplicaSets: false,
		},
		{
			name:            "replicasets only — pods still denied (deployment chain unreachable)",
			r:               &RelatedRBAC{ReplicaSets: true},
			wantPods:        false,
			wantReplicaSets: true,
		},
		{
			name:            "both permitted",
			r:               &RelatedRBAC{Pods: true, ReplicaSets: true},
			wantPods:        true,
			wantReplicaSets: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.r.allowsPods(); got != c.wantPods {
				t.Errorf("allowsPods()=%v want=%v", got, c.wantPods)
			}
			if got := c.r.allowsReplicaSets(); got != c.wantReplicaSets {
				t.Errorf("allowsReplicaSets()=%v want=%v", got, c.wantReplicaSets)
			}
		})
	}
}

// TestKindRelatedResourceMaps locks the resource-dependency lookup tables the
// diagnostics handler uses to drive its SSAR checks. P3-3: regressions here
// would either over-broaden the SSAR (false denials) or skip a check entirely
// (security regression). The maps are tiny — assert every documented kind.
func TestKindRelatedResourceMaps(t *testing.T) {
	t.Parallel()

	// kindNeedsPods: every kind whose related-pod resolution actually lists pods.
	wantPods := map[string]bool{
		"Deployment":  true,
		"StatefulSet": true,
		"DaemonSet":   true,
		"Pod":         true,
		"Service":     true,
		// PersistentVolumeClaim deliberately excluded — no pod traversal.
	}
	for k, want := range wantPods {
		if kindNeedsPods[k] != want {
			t.Errorf("kindNeedsPods[%q]=%v want=%v", k, kindNeedsPods[k], want)
		}
	}
	if kindNeedsPods["PersistentVolumeClaim"] {
		t.Errorf("kindNeedsPods[PersistentVolumeClaim] should be false (no pod traversal)")
	}

	// kindNeedsReplicaSets: only Deployment walks the RS chain.
	if !kindNeedsReplicaSets["Deployment"] {
		t.Errorf("kindNeedsReplicaSets[Deployment] should be true")
	}
	for _, k := range []string{"StatefulSet", "DaemonSet", "Pod", "Service", "PersistentVolumeClaim"} {
		if kindNeedsReplicaSets[k] {
			t.Errorf("kindNeedsReplicaSets[%q] should be false — no RS chain", k)
		}
	}
}
