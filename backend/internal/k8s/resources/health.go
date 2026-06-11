package resources

import (
	"fmt"
	"math"
)

// HealthStatus is the categorical cluster health status.
type HealthStatus string

const (
	HealthStatusHealthy  HealthStatus = "healthy"
	HealthStatusDegraded HealthStatus = "degraded"
	HealthStatusCritical HealthStatus = "critical"
	HealthStatusUnknown  HealthStatus = "unknown"
)

// SignalStatus is the tri-state resolution of a health signal.
type SignalStatus string

const (
	SignalStatusOk      SignalStatus = "ok"
	SignalStatusSkipped SignalStatus = "skipped"
	SignalStatusUnknown SignalStatus = "unknown"
)

// HealthSignal is the per-signal result in the health response.
type HealthSignal struct {
	Name   string       `json:"name"`
	Status SignalStatus `json:"status"`
	Score  *int         `json:"score"`
	Reason string       `json:"reason,omitempty"`
}

// ClusterHealth is the computed cluster health, embedded in DashboardSummary.
type ClusterHealth struct {
	Status  HealthStatus   `json:"status"`
	Score   *int           `json:"score"`
	Signals []HealthSignal `json:"signals"`
	Reasons []string       `json:"reasons"`
}

// ComponentState is the tri-state for a control-plane component.
type ComponentState int

const (
	ComponentUnscraped ComponentState = iota // not in Prometheus (e.g. k3s embedded)
	ComponentUp
	ComponentDown
)

// ControlPlaneStates holds per-component tri-state availability.
type ControlPlaneStates struct {
	SchedulerState         ComponentState
	ControllerManagerState ComponentState
	EtcdState              ComponentState
}

// HealthInputs holds pre-shaped plain values for the health calculator.
// All input shaping (progressing exclusion, WFFC PVC filtering, etc.) happens
// in the gathering step (U4); this struct receives already-shaped counts.
type HealthInputs struct {
	// --- Nodes signal ---
	// NodesAvailable is false when the nodes signal cannot be computed
	// (RBAC denied, cache unsynced, or 0 nodes visible despite list permission).
	NodesAvailable bool
	TotalNodes     int
	ReadyNodes     int
	PressureNodes  int // nodes with any pressure condition (Memory/Disk/PID/NetworkUnavailable)

	// --- Workloads signal ---
	// WorkloadsAvailable is false when the workloads signal cannot be computed
	// (RBAC denied, cache unsynced).
	WorkloadsAvailable bool
	// WorkloadsUnavailableReason carries the skipped/unknown reason when !WorkloadsAvailable.
	WorkloadsUnavailableReason string
	// WorkloadsDesired is the sum of desired replicas across all eligible workloads
	// (Deployments/StatefulSets/DaemonSets), after excluding: desired=0, spec.paused,
	// desiredNumberScheduled==0 DaemonSets, and actively-progressing workloads.
	WorkloadsDesired int
	// WorkloadsActuallyAvailable is the sum of min(available, desired) across eligible workloads.
	WorkloadsActuallyAvailable int
	// ProgressingCount is the count of actively-progressing workloads excluded from availability.
	// Non-zero → surfaced as a non-penalizing "N workloads rolling out" reason.
	ProgressingCount int

	// --- Pods signal ---
	// PodsAvailable is false when the pods signal cannot be computed.
	PodsAvailable bool
	// PodsUnavailableReason carries the skipped/unknown reason when !PodsAvailable.
	PodsUnavailableReason string
	// CrashloopPods is the count of pods in CrashLoopBackOff or ImagePull* waiting state.
	CrashloopPods int
	// EligiblePods is the count of pods with phase ∈ {Running, Pending} and nil deletionTimestamp.
	EligiblePods int

	// --- Alerts signal ---
	// AlertsAvailable is false when the alerts signal cannot be computed.
	AlertsAvailable bool
	// AlertsUnavailableReason carries the skipped/unknown reason when !AlertsAvailable.
	AlertsUnavailableReason string
	AlertsActive            int
	AlertsCritical          int

	// --- Certificates signal (flat deductions only; no signal score sub-weight) ---
	CertsAvailable bool
	// CertsUnavailableReason carries the skipped/unknown reason when !CertsAvailable.
	CertsUnavailableReason string
	CertWarning            int // certs in the warning expiry bucket
	CertCritical           int // certs in the critical expiry bucket

	// --- Storage signal (flat deductions only; no signal score sub-weight) ---
	StorageAvailable bool
	// StorageUnavailableReason carries the skipped/unknown reason when !StorageAvailable.
	StorageUnavailableReason string
	// PendingPVCs is the count of Pending PVCs after WFFC exclusion.
	PendingPVCs int

	// --- Control plane signal (flat deductions only; no signal score sub-weight) ---
	ControlPlaneAvailable bool
	// ControlPlaneUnavailableReason carries the skipped/unknown reason when !ControlPlaneAvailable.
	ControlPlaneUnavailableReason string
	ControlPlane                  ControlPlaneStates

	// --- PDB signal (feeds degraded only, no separate sub-score) ---
	PDBAvailable bool
	// PDBUnavailableReason carries the skipped/unknown reason when !PDBAvailable.
	PDBUnavailableReason string
	// PDBViolations is the count of PDBs with currentHealthy < desiredHealthy whose
	// workload is not actively progressing.
	PDBViolations int
}

// signal name constants — fixed ordered set, always present in Signals output.
const (
	signalNodes        = "nodes"
	signalWorkloads    = "workloads"
	signalPods         = "pods"
	signalAlerts       = "alerts"
	signalCerts        = "certificates"
	signalStorage      = "storage"
	signalControlPlane = "controlPlane"
)

// scoreWeight is a named sub-score weight entry used for renormalization.
type scoreWeight struct {
	name      string
	weight    float64
	available bool
	score     float64 // 0–100
}

// intPtr returns a pointer to the given int value.
func intPtr(v int) *int { return &v }

// reasonOr returns reason, or fallback when reason is empty.
func reasonOr(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	return reason
}

// flatDeductionSignal builds a HealthSignal for a signal that contributes only
// flat score deductions (certificates, storage, control plane): ok when
// available, otherwise skipped with reason (falling back to defaultReason).
func flatDeductionSignal(name string, available bool, reason, defaultReason string) HealthSignal {
	sig := HealthSignal{Name: name}
	if available {
		sig.Status = SignalStatusOk
	} else {
		sig.Status = SignalStatusSkipped
		sig.Reason = reasonOr(reason, defaultReason)
	}
	return sig
}

// safeRatio returns numerator/denominator, returning 0 for a zero denominator
// or any NaN/Inf result. Used wherever an availability ratio feeds the score or
// veto tiers so no NaN/±Inf can reach the output (R8).
func safeRatio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	r := float64(numerator) / float64(denominator)
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return 0
	}
	return r
}

// computeClusterHealth derives the categorical health status and 0–100 score
// from pre-shaped HealthInputs. It is a pure function with no I/O.
//
// The veto table is evaluated top-down; first matching tier wins (R1).
// The numeric score is computed independently of status (R2).
// Reason strings carry counts/ratios/categories only, never resource names (R3).
// Each signal resolves to ok/skipped/unknown (R5).
// No NaN or ±Inf can appear in any output field (R8).
func computeClusterHealth(in HealthInputs) ClusterHealth {
	var reasons []string
	signals := make([]HealthSignal, 0, 7)

	// ── Nodes signal ─────────────────────────────────────────────────────────
	var nodesSig HealthSignal
	var nodesScore float64
	nodesSig.Name = signalNodes

	if !in.NodesAvailable {
		nodesSig.Status = SignalStatusUnknown
		nodesSig.Reason = "nodes unavailable"
	} else {
		readyRatio := safeRatio(in.ReadyNodes, in.TotalNodes)
		// Per-pressure bounded deduction: −2 per pressure node, capped at 20.
		pressureDeduction := math.Min(float64(in.PressureNodes)*2, 20)
		nodesScore = math.Max(0, readyRatio*100-pressureDeduction)
		nodesSig.Status = SignalStatusOk
		nodesSig.Score = intPtr(int(math.Round(nodesScore)))
	}
	signals = append(signals, nodesSig)

	// ── Workloads signal ─────────────────────────────────────────────────────
	var workloadsSig HealthSignal
	var workloadsScore float64
	workloadsSig.Name = signalWorkloads

	switch {
	case !in.WorkloadsAvailable:
		workloadsSig.Status = SignalStatusUnknown
		workloadsSig.Reason = reasonOr(in.WorkloadsUnavailableReason, "workloads unavailable")
	case in.WorkloadsDesired == 0:
		workloadsSig.Status = SignalStatusSkipped
		workloadsSig.Reason = "no workloads to evaluate"
		workloadsScore = 100 // skipped → excluded from renormalization below
	default:
		ratio := safeRatio(in.WorkloadsActuallyAvailable, in.WorkloadsDesired)
		workloadsScore = math.Min(ratio, 1.0) * 100
		workloadsSig.Status = SignalStatusOk
		workloadsSig.Score = intPtr(int(math.Round(workloadsScore)))
	}
	signals = append(signals, workloadsSig)

	// ── Pods signal ───────────────────────────────────────────────────────────
	var podsSig HealthSignal
	var podsScore float64
	podsSig.Name = signalPods

	if !in.PodsAvailable {
		podsSig.Status = SignalStatusUnknown
		podsSig.Reason = reasonOr(in.PodsUnavailableReason, "pods unavailable")
	} else if in.EligiblePods == 0 {
		// No eligible pods → skipped, not penalized.
		podsSig.Status = SignalStatusSkipped
		podsSig.Reason = "no eligible pods"
		podsScore = 100
	} else {
		crashFraction := safeRatio(in.CrashloopPods, in.EligiblePods)
		const amplification = 5.0
		podsScore = math.Max(0, 100-math.Min(100, crashFraction*100*amplification))
		podsSig.Status = SignalStatusOk
		podsSig.Score = intPtr(int(math.Round(podsScore)))
	}
	signals = append(signals, podsSig)

	// ── Alerts signal ─────────────────────────────────────────────────────────
	var alertsSig HealthSignal
	var alertsScore float64
	alertsSig.Name = signalAlerts

	if !in.AlertsAvailable {
		alertsSig.Status = SignalStatusUnknown
		alertsSig.Reason = reasonOr(in.AlertsUnavailableReason, "alerts unavailable")
	} else {
		alertsScore = math.Max(0, 100-float64(in.AlertsCritical)*10-float64(in.AlertsActive-in.AlertsCritical)*3)
		alertsSig.Status = SignalStatusOk
		alertsSig.Score = intPtr(int(math.Round(alertsScore)))
	}
	signals = append(signals, alertsSig)

	// ── Certificates / Storage / Control plane signals (flat deduction only) ──
	signals = append(signals,
		flatDeductionSignal(signalCerts, in.CertsAvailable, in.CertsUnavailableReason, "certificate data unavailable"),
		flatDeductionSignal(signalStorage, in.StorageAvailable, in.StorageUnavailableReason, "storage data unavailable"),
		flatDeductionSignal(signalControlPlane, in.ControlPlaneAvailable, in.ControlPlaneUnavailableReason, "control plane data unavailable"),
	)

	// ── Veto table (top-down, first match wins) ───────────────────────────────
	//
	// unknown tier: nodes signal unavailable
	if !in.NodesAvailable {
		reasons = append(reasons, nodesSig.Reason)
		return ClusterHealth{
			Status:  HealthStatusUnknown,
			Score:   nil,
			Signals: signals,
			Reasons: reasons,
		}
	}

	readyRatio := safeRatio(in.ReadyNodes, in.TotalNodes)

	// critical tier
	isCritical := false
	if in.TotalNodes > 0 && readyRatio < (2.0/3.0)-1e-9 {
		isCritical = true
		reasons = append(reasons, fmt.Sprintf("%d of %d nodes not ready", in.TotalNodes-in.ReadyNodes, in.TotalNodes))
	}
	if in.WorkloadsAvailable && in.WorkloadsDesired > 0 {
		wRatio := safeRatio(in.WorkloadsActuallyAvailable, in.WorkloadsDesired)
		if wRatio < 0.50-1e-9 {
			isCritical = true
			reasons = append(reasons, fmt.Sprintf("%d of %d workload replicas unavailable", in.WorkloadsDesired-in.WorkloadsActuallyAvailable, in.WorkloadsDesired))
		}
	}

	// degraded tier
	isDegraded := false
	var degradedReasons []string

	// any not-ready node
	if in.TotalNodes > 0 && in.ReadyNodes < in.TotalNodes {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d of %d nodes not ready", in.TotalNodes-in.ReadyNodes, in.TotalNodes))
	}
	// any pressure node
	if in.PressureNodes > 0 {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d node(s) with pressure conditions", in.PressureNodes))
	}
	// workload availability < 95%
	if in.WorkloadsAvailable && in.WorkloadsDesired > 0 {
		wRatio := safeRatio(in.WorkloadsActuallyAvailable, in.WorkloadsDesired)
		if wRatio < 0.95-1e-9 {
			isDegraded = true
			degradedReasons = append(degradedReasons, fmt.Sprintf("%d of %d workload replicas unavailable", in.WorkloadsDesired-in.WorkloadsActuallyAvailable, in.WorkloadsDesired))
		}
	}
	// any crashloop/image-pull pod
	if in.PodsAvailable && in.CrashloopPods > 0 {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d pod(s) in crash loop or image pull failure", in.CrashloopPods))
	}
	// PDB violations
	if in.PDBAvailable && in.PDBViolations > 0 {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d PDB(s) with insufficient healthy pods", in.PDBViolations))
	}
	// pending PVCs
	if in.StorageAvailable && in.PendingPVCs > 0 {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d PVC(s) pending", in.PendingPVCs))
	}
	// cert expiry buckets
	if in.CertsAvailable && in.CertWarning > 0 {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d certificate(s) expiring soon (warning)", in.CertWarning))
	}
	if in.CertsAvailable && in.CertCritical > 0 {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d certificate(s) expiring soon (critical)", in.CertCritical))
	}
	// control-plane component down
	if in.ControlPlaneAvailable {
		if in.ControlPlane.SchedulerState == ComponentDown {
			isDegraded = true
			degradedReasons = append(degradedReasons, "kube-scheduler scraped and down")
		}
		if in.ControlPlane.ControllerManagerState == ComponentDown {
			isDegraded = true
			degradedReasons = append(degradedReasons, "kube-controller-manager scraped and down")
		}
		if in.ControlPlane.EtcdState == ComponentDown {
			isDegraded = true
			degradedReasons = append(degradedReasons, "etcd scraped and down")
		}
	}
	// any critical alert
	if in.AlertsAvailable && in.AlertsCritical > 0 {
		isDegraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("%d critical alert(s) firing", in.AlertsCritical))
	}

	// ── Status determination ──────────────────────────────────────────────────
	// Critical reasons are already in `reasons`; degraded reasons merge in for both
	// the critical and degraded tiers (a critical cluster also surfaces its degraded
	// detail). The healthy tier carries no degraded reasons.
	var status HealthStatus
	switch {
	case isCritical:
		status = HealthStatusCritical
		reasons = append(reasons, degradedReasons...)
	case isDegraded:
		status = HealthStatusDegraded
		reasons = append(reasons, degradedReasons...)
	default:
		status = HealthStatusHealthy
	}

	// Progressing workloads: always an informational reason (never a penalty),
	// surfaced regardless of tier.
	if in.ProgressingCount > 0 {
		reasons = append(reasons, fmt.Sprintf("%d workload(s) rolling out", in.ProgressingCount))
	}

	// ── Score aggregation ─────────────────────────────────────────────────────
	// Four weighted sub-scores; weights renormalize over signals that resolved ok.
	subScores := []scoreWeight{
		{name: signalNodes, weight: 0.35, available: in.NodesAvailable, score: nodesScore},
		{name: signalWorkloads, weight: 0.35, available: in.WorkloadsAvailable && in.WorkloadsDesired > 0, score: workloadsScore},
		{name: signalPods, weight: 0.20, available: in.PodsAvailable && in.EligiblePods > 0, score: podsScore},
		{name: signalAlerts, weight: 0.10, available: in.AlertsAvailable, score: alertsScore},
	}

	// Compute total available weight for renormalization.
	totalWeight := 0.0
	for _, sw := range subScores {
		if sw.available {
			totalWeight += sw.weight
		}
	}

	var compositeScore float64
	if totalWeight > 0 {
		for _, sw := range subScores {
			if sw.available {
				compositeScore += (sw.weight / totalWeight) * sw.score
			}
		}
	} else {
		// No weighted signals available — cannot compute a score.
		return ClusterHealth{
			Status:  HealthStatusUnknown,
			Score:   nil,
			Signals: signals,
			Reasons: dedupReasons(reasons),
		}
	}

	// Flat deductions (applied after weighted sum).
	if in.CertsAvailable {
		if in.CertWarning > 0 {
			compositeScore -= 3
		}
		if in.CertCritical > 0 {
			compositeScore -= 10
		}
	}
	if in.StorageAvailable && in.PendingPVCs > 0 {
		compositeScore -= 3
	}
	if in.ControlPlaneAvailable {
		if in.ControlPlane.SchedulerState == ComponentDown {
			compositeScore -= 10
		}
		if in.ControlPlane.ControllerManagerState == ComponentDown {
			compositeScore -= 10
		}
		if in.ControlPlane.EtcdState == ComponentDown {
			compositeScore -= 10
		}
	}

	// Clamp to [0, 100].
	compositeScore = math.Max(0, math.Min(100, compositeScore))

	// Guard against NaN/Inf before rounding (defensive; should not occur after guards above).
	if math.IsNaN(compositeScore) || math.IsInf(compositeScore, 0) {
		compositeScore = 0
	}

	scoreVal := int(math.Round(compositeScore))

	// De-duplicate reasons (progressing reason may have been appended twice in critical path).
	finalReasons := dedupReasons(reasons)

	return ClusterHealth{
		Status:  status,
		Score:   intPtr(scoreVal),
		Signals: signals,
		Reasons: finalReasons,
	}
}

// dedupReasons returns reasons with duplicates removed, preserving order.
func dedupReasons(reasons []string) []string {
	seen := make(map[string]bool, len(reasons))
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
