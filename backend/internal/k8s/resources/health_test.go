package resources

import (
	"math"
	"strings"
	"testing"
)

// ── helpers ────────────────────────────────────────────────────────────────

// healthyInputs returns a fully-populated HealthInputs with all signals
// available and everything green (10 nodes all ready, 100 desired / 100
// available workload replicas, 0 crash pods out of 50 eligible, 0 alerts,
// 0 certs expiring, 0 pending PVCs, all control-plane components up).
func healthyInputs() HealthInputs {
	return HealthInputs{
		NodesAvailable:         true,
		TotalNodes:             10,
		ReadyNodes:             10,
		PressureNodes:          0,
		WorkloadsAvailable:     true,
		WorkloadsDesired:       100,
		WorkloadsActuallyAvailable: 100,
		ProgressingCount:       0,
		PodsAvailable:          true,
		EligiblePods:           50,
		CrashloopPods:          0,
		AlertsAvailable:        true,
		AlertsActive:           0,
		AlertsCritical:         0,
		CertsAvailable:         true,
		CertWarning:            0,
		CertCritical:           0,
		StorageAvailable:       true,
		PendingPVCs:            0,
		PDBAvailable:           true,
		PDBViolations:          0,
		ControlPlaneAvailable:  true,
		ControlPlane: ControlPlaneStates{
			SchedulerState:         ComponentUp,
			ControllerManagerState: ComponentUp,
			EtcdState:              ComponentUp,
		},
	}
}

// assertNoNaN sweeps every float field reachable from a ClusterHealth and
// fails the test if any NaN or ±Inf is found (R8).
func assertNoNaN(t *testing.T, h ClusterHealth) {
	t.Helper()
	if h.Score != nil {
		f := float64(*h.Score)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			t.Errorf("score is NaN/Inf: %v", *h.Score)
		}
	}
	for _, sig := range h.Signals {
		if sig.Score != nil {
			f := float64(*sig.Score)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				t.Errorf("signal %q score is NaN/Inf: %v", sig.Name, *sig.Score)
			}
		}
	}
}

// assertNoResourceNames checks that no reason string contains known fixture
// resource names — verifying R3 (counts/categories only).
func assertNoResourceNames(t *testing.T, h ClusterHealth, names ...string) {
	t.Helper()
	for _, r := range h.Reasons {
		for _, name := range names {
			if strings.Contains(r, name) {
				t.Errorf("reason %q contains resource name %q (violates R3)", r, name)
			}
		}
	}
	for _, sig := range h.Signals {
		for _, name := range names {
			if sig.Reason != "" && strings.Contains(sig.Reason, name) {
				t.Errorf("signal %q reason %q contains resource name %q (violates R3)", sig.Name, sig.Reason, name)
			}
		}
	}
}

// signalByName returns the HealthSignal with the given name, or fails.
func signalByName(t *testing.T, h ClusterHealth, name string) HealthSignal {
	t.Helper()
	for _, s := range h.Signals {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("signal %q not found in output", name)
	return HealthSignal{}
}

// ── test cases ─────────────────────────────────────────────────────────────

// TestComputeClusterHealth_HappyPath: all signals ok, everything green.
func TestComputeClusterHealth_HappyPath(t *testing.T) {
	h := computeClusterHealth(healthyInputs())

	assertNoNaN(t, h)

	if h.Status != HealthStatusHealthy {
		t.Errorf("want healthy, got %q", h.Status)
	}
	if h.Score == nil {
		t.Fatal("want non-nil score")
	}
	if *h.Score != 100 {
		t.Errorf("want score 100, got %d", *h.Score)
	}
	if len(h.Reasons) != 0 {
		t.Errorf("want empty reasons for healthy, got %v", h.Reasons)
	}
	if len(h.Signals) != 7 {
		t.Errorf("want 7 signals, got %d", len(h.Signals))
	}
	for _, sig := range h.Signals {
		switch sig.Name {
		case signalNodes, signalWorkloads, signalPods, signalAlerts, signalCerts, signalStorage, signalControlPlane:
			// expected
		default:
			t.Errorf("unexpected signal name %q", sig.Name)
		}
	}
}

// TestComputeClusterHealth_VetoPrecedence_NodesUnavailableWinsOverCritical:
// nodes unavailable + workloads at 40% → unknown (not critical).
func TestComputeClusterHealth_VetoPrecedence_NodesUnavailableWinsOverCritical(t *testing.T) {
	in := healthyInputs()
	in.NodesAvailable = false
	in.WorkloadsDesired = 10
	in.WorkloadsActuallyAvailable = 4 // 40% → would be critical if nodes were available

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusUnknown {
		t.Errorf("want unknown (nodes unavailable takes precedence), got %q", h.Status)
	}
	if h.Score != nil {
		t.Errorf("want nil score when unknown, got %d", *h.Score)
	}
	if len(h.Reasons) == 0 {
		t.Error("want at least one reason for unknown status")
	}
}

// TestComputeClusterHealth_VetoPrecedence_CriticalBeforeDegraded:
// ready ratio 0.5 + one crashloop pod → critical (not degraded).
func TestComputeClusterHealth_VetoPrecedence_CriticalBeforeDegraded(t *testing.T) {
	in := healthyInputs()
	in.TotalNodes = 6
	in.ReadyNodes = 3 // ratio 0.5 < 2/3 → critical
	in.CrashloopPods = 1

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusCritical {
		t.Errorf("want critical, got %q", h.Status)
	}
}

// ── Degraded rows ───────────────────────────────────────────────────────────

// TestComputeClusterHealth_Degraded_NotReadyNode: any NotReady node.
func TestComputeClusterHealth_Degraded_NotReadyNode(t *testing.T) {
	in := healthyInputs()
	in.TotalNodes = 5
	in.ReadyNodes = 4 // 1 not ready

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "not ready") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a 'not ready' reason, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_Degraded_NodePressure: any node pressure condition.
func TestComputeClusterHealth_Degraded_NodePressure(t *testing.T) {
	in := healthyInputs()
	in.PressureNodes = 1

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "pressure") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a pressure reason, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_Degraded_WorkloadAvailabilityBelow95:
// workload availability just below 95%.
func TestComputeClusterHealth_Degraded_WorkloadAvailabilityBelow95(t *testing.T) {
	in := healthyInputs()
	in.WorkloadsDesired = 100
	in.WorkloadsActuallyAvailable = 94 // 94% < 95%

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded at 94%% availability, got %q", h.Status)
	}
}

// TestComputeClusterHealth_Degraded_CrashloopPod: any crashloop/image-pull pod.
func TestComputeClusterHealth_Degraded_CrashloopPod(t *testing.T) {
	in := healthyInputs()
	in.CrashloopPods = 1
	in.EligiblePods = 20

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "crash") || strings.Contains(r, "image pull") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a crash/image-pull reason, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_Degraded_PDBViolation.
func TestComputeClusterHealth_Degraded_PDBViolation(t *testing.T) {
	in := healthyInputs()
	in.PDBViolations = 1

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "PDB") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a PDB reason, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_Degraded_PendingPVC.
func TestComputeClusterHealth_Degraded_PendingPVC(t *testing.T) {
	in := healthyInputs()
	in.PendingPVCs = 2

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "PVC") || strings.Contains(r, "pending") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a pending PVC reason, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_Degraded_CertWarning.
func TestComputeClusterHealth_Degraded_CertWarning(t *testing.T) {
	in := healthyInputs()
	in.CertWarning = 1

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
}

// TestComputeClusterHealth_Degraded_CertCritical.
func TestComputeClusterHealth_Degraded_CertCritical(t *testing.T) {
	in := healthyInputs()
	in.CertCritical = 1

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
}

// TestComputeClusterHealth_Degraded_ControlPlaneDown: any scraped-and-down
// component → degraded.
func TestComputeClusterHealth_Degraded_ControlPlaneDown(t *testing.T) {
	in := healthyInputs()
	in.ControlPlane.SchedulerState = ComponentDown

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "scheduler") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a scheduler reason, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_Degraded_CriticalAlert.
func TestComputeClusterHealth_Degraded_CriticalAlert(t *testing.T) {
	in := healthyInputs()
	in.AlertsCritical = 1
	in.AlertsActive = 1

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded, got %q", h.Status)
	}
}

// ── Control-plane deductions ────────────────────────────────────────────────

// TestComputeClusterHealth_ControlPlane_EachDownMinus10:
// etcd + scheduler both down → −20, status still degraded.
func TestComputeClusterHealth_ControlPlane_EachDownMinus10(t *testing.T) {
	// Perfect healthy inputs first.
	baseIn := healthyInputs()
	base := computeClusterHealth(baseIn)
	if base.Score == nil {
		t.Fatal("base score is nil")
	}
	baseScore := *base.Score

	in := healthyInputs()
	in.ControlPlane.EtcdState = ComponentDown
	in.ControlPlane.SchedulerState = ComponentDown

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusDegraded {
		t.Errorf("want degraded (two components down), got %q", h.Status)
	}
	if h.Score == nil {
		t.Fatal("score is nil")
	}
	want := baseScore - 20
	if want < 0 {
		want = 0
	}
	if *h.Score != want {
		t.Errorf("want score %d (base %d − 20), got %d", want, baseScore, *h.Score)
	}
}

// TestComputeClusterHealth_ControlPlane_Skipped_NoEffect:
// control plane skipped → no effect on status or score.
func TestComputeClusterHealth_ControlPlane_Skipped_NoEffect(t *testing.T) {
	inBase := healthyInputs()
	base := computeClusterHealth(inBase)

	in := healthyInputs()
	in.ControlPlaneAvailable = false
	in.ControlPlaneUnavailableReason = "control plane metrics unavailable"

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusHealthy {
		t.Errorf("want healthy when CP skipped, got %q", h.Status)
	}
	if h.Score == nil || base.Score == nil {
		t.Fatal("nil score")
	}
	if *h.Score != *base.Score {
		t.Errorf("CP skipped should not change score: base=%d got=%d", *base.Score, *h.Score)
	}
}

// ── Renormalization ──────────────────────────────────────────────────────────

// TestComputeClusterHealth_Renormalization_AlertsSkipped:
// alerts signal skipped → remaining weights scale to 1.0.
func TestComputeClusterHealth_Renormalization_AlertsSkipped(t *testing.T) {
	in := healthyInputs()
	in.AlertsAvailable = false
	in.AlertsUnavailableReason = "alertmanager not installed"

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusHealthy {
		t.Errorf("want healthy, got %q", h.Status)
	}
	if h.Score == nil {
		t.Fatal("score nil")
	}
	// With all remaining signals at 100, score should still be 100.
	if *h.Score != 100 {
		t.Errorf("want score 100 with renormalization over healthy signals, got %d", *h.Score)
	}
	alertSig := signalByName(t, h, signalAlerts)
	if alertSig.Status != SignalStatusUnknown {
		t.Errorf("alerts signal want unknown, got %q", alertSig.Status)
	}
}

// TestComputeClusterHealth_Renormalization_OnlyNodesAndWorkloads:
// pods + alerts unavailable → score from nodes+workloads only.
func TestComputeClusterHealth_Renormalization_OnlyNodesAndWorkloads(t *testing.T) {
	in := healthyInputs()
	in.PodsAvailable = false
	in.PodsUnavailableReason = "pods cache not synced"
	in.AlertsAvailable = false
	in.AlertsUnavailableReason = "alertmanager not installed"

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Score == nil {
		t.Fatal("score nil")
	}
	// nodes(100) + workloads(100) renormalized = 100.
	if *h.Score != 100 {
		t.Errorf("want score 100 from nodes+workloads only, got %d", *h.Score)
	}
}

// ── Nullable score ───────────────────────────────────────────────────────────

// TestComputeClusterHealth_NullScore_WhenUnknown.
func TestComputeClusterHealth_NullScore_WhenUnknown(t *testing.T) {
	in := healthyInputs()
	in.NodesAvailable = false

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusUnknown {
		t.Errorf("want unknown, got %q", h.Status)
	}
	if h.Score != nil {
		t.Errorf("want nil score for unknown, got %d", *h.Score)
	}
}

// TestComputeClusterHealth_NullScore_AllSignalsUnavailable.
func TestComputeClusterHealth_NullScore_AllSignalsUnavailable(t *testing.T) {
	// Nodes unavailable → unknown regardless of other signals.
	in := HealthInputs{
		NodesAvailable:     false,
		WorkloadsAvailable: false,
		PodsAvailable:      false,
		AlertsAvailable:    false,
		CertsAvailable:     false,
		StorageAvailable:   false,
		PDBAvailable:       false,
		ControlPlaneAvailable: false,
	}

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusUnknown {
		t.Errorf("want unknown, got %q", h.Status)
	}
	if h.Score != nil {
		t.Errorf("want nil score, got %d", *h.Score)
	}
	if len(h.Reasons) == 0 {
		t.Error("want at least one reason when all unavailable")
	}
}

// ── Boundary values ──────────────────────────────────────────────────────────

// TestComputeClusterHealth_Boundary_ReadyRatioExactly2Over3:
// ready ratio exactly 2/3 → NOT critical (boundary is exclusive of critical).
func TestComputeClusterHealth_Boundary_ReadyRatioExactly2Over3(t *testing.T) {
	in := healthyInputs()
	in.TotalNodes = 3
	in.ReadyNodes = 2 // ratio exactly 2/3

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status == HealthStatusCritical {
		t.Errorf("ratio exactly 2/3 should NOT be critical, got %q", h.Status)
	}
	// It is degraded because ReadyNodes < TotalNodes triggers degraded.
	if h.Status != HealthStatusDegraded {
		t.Errorf("ratio 2/3 with 1 not-ready node want degraded, got %q", h.Status)
	}
}

// TestComputeClusterHealth_Boundary_WorkloadAvailabilityExactly95:
// workload availability exactly 95% → NOT degraded.
func TestComputeClusterHealth_Boundary_WorkloadAvailabilityExactly95(t *testing.T) {
	in := healthyInputs()
	in.WorkloadsDesired = 100
	in.WorkloadsActuallyAvailable = 95 // exactly 95%

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status == HealthStatusDegraded || h.Status == HealthStatusCritical {
		t.Errorf("95%% availability should NOT be degraded/critical, got %q", h.Status)
	}
}

// TestComputeClusterHealth_Boundary_WorkloadAvailabilityExactly50:
// workload availability exactly 50% → NOT critical (boundary exclusive).
func TestComputeClusterHealth_Boundary_WorkloadAvailabilityExactly50(t *testing.T) {
	in := healthyInputs()
	in.WorkloadsDesired = 100
	in.WorkloadsActuallyAvailable = 50 // exactly 50%

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status == HealthStatusCritical {
		t.Errorf("50%% availability should NOT be critical (boundary exclusive), got %q", h.Status)
	}
}

// ── NaN guards ───────────────────────────────────────────────────────────────

// TestComputeClusterHealth_NaNGuard_ZeroNodes: 0 nodes.
func TestComputeClusterHealth_NaNGuard_ZeroNodes(t *testing.T) {
	in := healthyInputs()
	in.TotalNodes = 0
	in.ReadyNodes = 0

	h := computeClusterHealth(in)
	assertNoNaN(t, h)
}

// TestComputeClusterHealth_NaNGuard_ZeroDesiredReplicas: 0 desired replicas.
func TestComputeClusterHealth_NaNGuard_ZeroDesiredReplicas(t *testing.T) {
	in := healthyInputs()
	in.WorkloadsDesired = 0
	in.WorkloadsActuallyAvailable = 0

	h := computeClusterHealth(in)
	assertNoNaN(t, h)
}

// TestComputeClusterHealth_NaNGuard_ZeroEligiblePods: 0 eligible pods.
func TestComputeClusterHealth_NaNGuard_ZeroEligiblePods(t *testing.T) {
	in := healthyInputs()
	in.EligiblePods = 0
	in.CrashloopPods = 0

	h := computeClusterHealth(in)
	assertNoNaN(t, h)
}

// TestComputeClusterHealth_NaNGuard_AllZeros: comprehensive 0-value sweep.
func TestComputeClusterHealth_NaNGuard_AllZeros(t *testing.T) {
	in := HealthInputs{
		NodesAvailable:         true,
		TotalNodes:             0,
		ReadyNodes:             0,
		PressureNodes:          0,
		WorkloadsAvailable:     true,
		WorkloadsDesired:       0,
		WorkloadsActuallyAvailable: 0,
		PodsAvailable:          true,
		EligiblePods:           0,
		CrashloopPods:          0,
		AlertsAvailable:        true,
		AlertsActive:           0,
		AlertsCritical:         0,
		CertsAvailable:         true,
		CertWarning:            0,
		CertCritical:           0,
		StorageAvailable:       true,
		PendingPVCs:            0,
		PDBAvailable:           true,
		PDBViolations:          0,
		ControlPlaneAvailable:  true,
		ControlPlane: ControlPlaneStates{
			SchedulerState:         ComponentUnscraped,
			ControllerManagerState: ComponentUnscraped,
			EtcdState:              ComponentUnscraped,
		},
	}

	h := computeClusterHealth(in)
	assertNoNaN(t, h)
}

// ── Surge clamp ──────────────────────────────────────────────────────────────

// TestComputeClusterHealth_SurgeClamp:
// deployment with available > desired contributes ratio 1.0, not >1.
func TestComputeClusterHealth_SurgeClamp(t *testing.T) {
	in := healthyInputs()
	// Simulate surge: available=12, desired=10 (surge of 2).
	in.WorkloadsDesired = 10
	in.WorkloadsActuallyAvailable = 12 // caller must clamp before passing, OR the function clamps

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	// Score must not exceed 100.
	if h.Score != nil && *h.Score > 100 {
		t.Errorf("score exceeded 100 due to surge: %d", *h.Score)
	}
	if h.Status != HealthStatusHealthy {
		t.Errorf("surge should not degrade health, got %q", h.Status)
	}
}

// ── Progressing exclusion ────────────────────────────────────────────────────

// TestComputeClusterHealth_ProgressingExclusion_StaysHealthy:
// mid-rollout fixture (3/10 available but marked progressing) stays healthy
// with a "rolling out" informational reason and no availability penalty.
func TestComputeClusterHealth_ProgressingExclusion_StaysHealthy(t *testing.T) {
	in := healthyInputs()
	// After excluding the progressing workload: 0 desired (rest are healthy).
	// ProgressingCount=1 surfaces as informational reason.
	in.WorkloadsDesired = 0
	in.WorkloadsActuallyAvailable = 0
	in.ProgressingCount = 1

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status == HealthStatusCritical || h.Status == HealthStatusDegraded {
		t.Errorf("progressing-excluded workload should not degrade status, got %q", h.Status)
	}

	// "rolling out" reason must be present.
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "rolling out") {
			found = true
		}
	}
	if !found {
		t.Errorf("want 'rolling out' informational reason, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_ProgressingExclusion_WithoutCondition_Trips Critical:
// the same fixture WITHOUT the progressing exclusion (3/10 available, desired=10)
// trips critical (<50%).
func TestComputeClusterHealth_ProgressingExclusion_WithoutCondition_TripsCritical(t *testing.T) {
	in := healthyInputs()
	in.WorkloadsDesired = 10
	in.WorkloadsActuallyAvailable = 3 // 30% < 50% → critical
	in.ProgressingCount = 0

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusCritical {
		t.Errorf("30%% availability without progressing exclusion want critical, got %q", h.Status)
	}
}

// ── Reason format ────────────────────────────────────────────────────────────

// TestComputeClusterHealth_ReasonFormat_NoResourceNames: no fixture resource
// names should appear in reason strings (R3 — counts/categories only).
func TestComputeClusterHealth_ReasonFormat_NoResourceNames(t *testing.T) {
	// Build a fixture with multiple degraded triggers. None of the reason strings
	// should contain the artificial resource names "worker-1", "my-deploy", "my-pvc".
	in := healthyInputs()
	in.TotalNodes = 5
	in.ReadyNodes = 4
	in.PressureNodes = 1
	in.CrashloopPods = 2
	in.EligiblePods = 20
	in.PendingPVCs = 1
	in.PDBViolations = 1
	in.CertWarning = 1
	in.CertCritical = 1
	in.AlertsCritical = 1
	in.AlertsActive = 3
	in.ControlPlane.SchedulerState = ComponentDown

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	// These names must NEVER appear in reasons (R3).
	assertNoResourceNames(t, h, "worker-1", "my-deploy", "my-pvc", "default", "kube-system")
}

// ── Score clamping ───────────────────────────────────────────────────────────

// TestComputeClusterHealth_ScoreClamping_CannotGoBelowZero:
// stacked flat deductions cannot push below 0.
func TestComputeClusterHealth_ScoreClamping_CannotGoBelowZero(t *testing.T) {
	in := healthyInputs()
	// Terrible state: only 20% ready nodes (score 20), plus everything deducted.
	in.TotalNodes = 10
	in.ReadyNodes = 2 // ~20% → node sub-score ≈ 20; also critical
	in.PressureNodes = 10
	in.CertWarning = 1   // −3
	in.CertCritical = 1  // −10
	in.PendingPVCs = 100 // −3
	in.ControlPlane.SchedulerState = ComponentDown         // −10
	in.ControlPlane.ControllerManagerState = ComponentDown // −10
	in.ControlPlane.EtcdState = ComponentDown              // −10

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Score != nil && *h.Score < 0 {
		t.Errorf("score must not be negative, got %d", *h.Score)
	}
}

// ── Signal fixed set ─────────────────────────────────────────────────────────

// TestComputeClusterHealth_SignalSet_AlwaysSevenSignals:
// the signals slice always carries exactly 7 named entries regardless of input.
func TestComputeClusterHealth_SignalSet_AlwaysSevenSignals(t *testing.T) {
	cases := []struct {
		name string
		in   HealthInputs
	}{
		{"all-available", healthyInputs()},
		{"nodes-unavailable", func() HealthInputs {
			in := healthyInputs()
			in.NodesAvailable = false
			return in
		}()},
		{"all-unavailable", HealthInputs{}},
	}

	expectedNames := []string{
		signalNodes, signalWorkloads, signalPods, signalAlerts,
		signalCerts, signalStorage, signalControlPlane,
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := computeClusterHealth(tc.in)
			if len(h.Signals) != 7 {
				t.Fatalf("want 7 signals, got %d", len(h.Signals))
			}
			for i, sig := range h.Signals {
				if sig.Name != expectedNames[i] {
					t.Errorf("signal[%d] name want %q, got %q", i, expectedNames[i], sig.Name)
				}
			}
		})
	}
}

// ── Additional edge cases ────────────────────────────────────────────────────

// TestComputeClusterHealth_Critical_NodeRatioBelowTwoThirds:
// ready ratio 0.59 (below 2/3) → critical.
func TestComputeClusterHealth_Critical_NodeRatioBelowTwoThirds(t *testing.T) {
	in := healthyInputs()
	in.TotalNodes = 17
	in.ReadyNodes = 10 // 10/17 ≈ 0.588 < 2/3

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusCritical {
		t.Errorf("want critical (ratio 10/17 < 2/3), got %q", h.Status)
	}
}

// TestComputeClusterHealth_Critical_WorkloadBelow50:
// workload availability 49% → critical.
func TestComputeClusterHealth_Critical_WorkloadBelow50(t *testing.T) {
	in := healthyInputs()
	in.WorkloadsDesired = 100
	in.WorkloadsActuallyAvailable = 49 // 49% < 50%

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusCritical {
		t.Errorf("want critical (49%% < 50%%), got %q", h.Status)
	}
}

// TestComputeClusterHealth_ControlPlane_Unscraped_NoEffect:
// components in ComponentUnscraped state (k3s) → no degraded status, no deduction.
func TestComputeClusterHealth_ControlPlane_Unscraped_NoEffect(t *testing.T) {
	in := healthyInputs()
	in.ControlPlane.SchedulerState = ComponentUnscraped
	in.ControlPlane.ControllerManagerState = ComponentUnscraped
	in.ControlPlane.EtcdState = ComponentUnscraped

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusHealthy {
		t.Errorf("unscraped components should not degrade health, got %q", h.Status)
	}
	if h.Score == nil || *h.Score != 100 {
		score := -1
		if h.Score != nil {
			score = *h.Score
		}
		t.Errorf("unscraped CP should not affect score: got %d", score)
	}
}

// TestComputeClusterHealth_Workloads_EmptyAfterExclusion_Skipped:
// WorkloadsDesired=0 after exclusions → signal skipped, not penalized.
func TestComputeClusterHealth_Workloads_EmptyAfterExclusion_Skipped(t *testing.T) {
	in := healthyInputs()
	in.WorkloadsDesired = 0
	in.WorkloadsActuallyAvailable = 0

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	wSig := signalByName(t, h, signalWorkloads)
	if wSig.Status != SignalStatusSkipped {
		t.Errorf("want workloads signal skipped when empty after exclusion, got %q", wSig.Status)
	}
}

// TestComputeClusterHealth_Alerts_ScoreFormula:
// score formula: 100 − 10*critical − 3*(active−critical), clamped.
func TestComputeClusterHealth_Alerts_ScoreFormula(t *testing.T) {
	in := healthyInputs()
	in.AlertsCritical = 2
	in.AlertsActive = 5 // 3 non-critical

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	// alerts sub-score = 100 − 20 − 9 = 71
	// weighted sum: nodes(100)*0.35 + workloads(100)*0.35 + pods(100)*0.20 + alerts(71)*0.10 = 97.1 → rounds to 97
	alertSig := signalByName(t, h, signalAlerts)
	if alertSig.Score == nil {
		t.Fatal("alerts signal score nil")
	}
	if *alertSig.Score != 71 {
		t.Errorf("alerts signal score: want 71, got %d", *alertSig.Score)
	}
}

// TestComputeClusterHealth_FullNaNSweep: exercise every output field for NaN/Inf.
func TestComputeClusterHealth_FullNaNSweep(t *testing.T) {
	cases := []HealthInputs{
		// zero everything
		{},
		// nodes available, nothing else
		{NodesAvailable: true, TotalNodes: 0, ReadyNodes: 0},
		// impossible surge: available >> desired
		{NodesAvailable: true, TotalNodes: 5, ReadyNodes: 5,
			WorkloadsAvailable: true, WorkloadsDesired: 1, WorkloadsActuallyAvailable: 999,
			PodsAvailable: true, EligiblePods: 1, CrashloopPods: 0,
			AlertsAvailable: true, AlertsActive: 0, AlertsCritical: 0},
		// alerts math: active=0, critical=0
		healthyInputs(),
		// many deductions stacked
		func() HealthInputs {
			in := healthyInputs()
			in.CertWarning = 100
			in.CertCritical = 100
			in.PendingPVCs = 100
			in.ControlPlane.SchedulerState = ComponentDown
			in.ControlPlane.ControllerManagerState = ComponentDown
			in.ControlPlane.EtcdState = ComponentDown
			return in
		}(),
	}

	for i, in := range cases {
		h := computeClusterHealth(in)
		assertNoNaN(t, h)
		if h.Score != nil && (*h.Score < 0 || *h.Score > 100) {
			t.Errorf("case %d: score %d out of [0,100]", i, *h.Score)
		}
	}
}

// ── T1: NodesUnavailableReason propagation ───────────────────────────────────

// TestComputeClusterHealth_NodesUnavailable_NoNodesVisible:
// NodesAvailable=false with reason "no nodes visible" → unknown status + nil score.
func TestComputeClusterHealth_NodesUnavailable_NoNodesVisible(t *testing.T) {
	in := healthyInputs()
	in.NodesAvailable = false
	in.NodesUnavailableReason = "no nodes visible"

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusUnknown {
		t.Errorf("want unknown, got %q", h.Status)
	}
	if h.Score != nil {
		t.Errorf("want nil score when unknown, got %d", *h.Score)
	}
	// Reason must propagate.
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "no nodes visible") {
			found = true
		}
	}
	if !found {
		t.Errorf("want reason containing 'no nodes visible', got %v", h.Reasons)
	}
	// Signal reason must also propagate.
	nodeSig := signalByName(t, h, signalNodes)
	if !strings.Contains(nodeSig.Reason, "no nodes visible") {
		t.Errorf("nodes signal reason want 'no nodes visible', got %q", nodeSig.Reason)
	}
}

// TestComputeClusterHealth_NodesUnavailable_InsufficientPermissions:
// reason "insufficient permissions" propagates to signal and top-level reasons.
func TestComputeClusterHealth_NodesUnavailable_InsufficientPermissions(t *testing.T) {
	in := healthyInputs()
	in.NodesAvailable = false
	in.NodesUnavailableReason = "insufficient permissions"

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusUnknown {
		t.Errorf("want unknown, got %q", h.Status)
	}
	nodeSig := signalByName(t, h, signalNodes)
	if !strings.Contains(nodeSig.Reason, "insufficient permissions") {
		t.Errorf("nodes signal reason want 'insufficient permissions', got %q", nodeSig.Reason)
	}
	found := false
	for _, r := range h.Reasons {
		if strings.Contains(r, "insufficient permissions") {
			found = true
		}
	}
	if !found {
		t.Errorf("want 'insufficient permissions' in top-level reasons, got %v", h.Reasons)
	}
}

// TestComputeClusterHealth_NodesUnavailable_CacheSyncing:
// reason "cache syncing" propagates to signal and top-level reasons.
func TestComputeClusterHealth_NodesUnavailable_CacheSyncing(t *testing.T) {
	in := healthyInputs()
	in.NodesAvailable = false
	in.NodesUnavailableReason = "cache syncing"

	h := computeClusterHealth(in)
	assertNoNaN(t, h)

	if h.Status != HealthStatusUnknown {
		t.Errorf("want unknown, got %q", h.Status)
	}
	nodeSig := signalByName(t, h, signalNodes)
	if !strings.Contains(nodeSig.Reason, "cache syncing") {
		t.Errorf("nodes signal reason want 'cache syncing', got %q", nodeSig.Reason)
	}
}
