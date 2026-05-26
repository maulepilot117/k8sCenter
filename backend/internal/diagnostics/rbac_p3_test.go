package diagnostics

import (
	"context"
	"log/slog"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

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

// TestResolveRelatedRBAC_AllowVsDeny locks the P3-3 contract on
// resolveRelatedRBAC: when the user has every related-resource permission,
// the returned RelatedRBAC reflects that; when denied (or on transport
// error treated as denial per REL-003/adv-5 fix), the zero-value flows back
// and the diagnostic resolver short-circuits the pod traversal. The two
// always-permit/always-deny AccessChecker fakes deterministically exercise
// both ends of the contract without requiring fake SAR arg capture.
func TestResolveRelatedRBAC_AllowVsDeny(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		kind           string
		accessChecker  *resources.AccessChecker
		wantPods       bool
		wantRS         bool
	}{
		{"Deployment + allow → both true", "Deployment", resources.NewAlwaysAllowAccessChecker(), true, true},
		{"Deployment + deny → both false (graceful)", "Deployment", resources.NewAlwaysDenyAccessChecker(), false, false},
		{"StatefulSet + allow → pods true, RS false (no RS chain)", "StatefulSet", resources.NewAlwaysAllowAccessChecker(), true, false},
		{"StatefulSet + deny → both false", "StatefulSet", resources.NewAlwaysDenyAccessChecker(), false, false},
		{"PVC + allow → no SARs, both false", "PersistentVolumeClaim", resources.NewAlwaysAllowAccessChecker(), false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := &Handler{
				AccessChecker: c.accessChecker,
				Logger:        slog.Default(),
			}
			user := &auth.User{KubernetesUsername: "alice", KubernetesGroups: []string{"ops"}}
			got := h.resolveRelatedRBAC(context.Background(), user, "local", c.kind, "team-a")
			if got == nil {
				t.Fatal("resolveRelatedRBAC must not return nil — graceful-degradation contract")
			}
			if got.Pods != c.wantPods {
				t.Errorf("Pods=%v want=%v", got.Pods, c.wantPods)
			}
			if got.ReplicaSets != c.wantRS {
				t.Errorf("ReplicaSets=%v want=%v", got.ReplicaSets, c.wantRS)
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
