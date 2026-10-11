package resources

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
)

// Health input shaping and assembly, shared by the local and remote dashboard
// summary paths (KTD4). Everything in this file is pure: no informers, no
// permission checks, no clock, no network. A mapper turns its reads into one
// outcome per input (gatherHealthInputs is the local mapper), and
// assembleHealthInputs turns those outcomes into HealthInputs under one set of
// rules, so the two paths differ only in how reads become outcomes.

// Waiting reason predicates copied from diagnostics/rules.go — exact string
// match required so dashboard and diagnostics page never disagree on the same pod.
const (
	waitingReasonCrashLoopBackOff = "CrashLoopBackOff"
	waitingReasonImagePullBackOff = "ImagePullBackOff"
	waitingReasonErrImagePull     = "ErrImagePull"
)

// Unavailable-reason texts shared by both mappers and the assembly. They are
// wire-visible (HealthSignal.Reason) and must not change.
const (
	reasonInsufficientPermissions = "insufficient permissions"
	reasonCacheSyncing            = "cache syncing"
	reasonNoNodesVisible          = "no nodes visible"
)

// isCrashloopOrImagePull returns true when the waiting reason indicates a
// CrashLoopBackOff, ImagePullBackOff, or ErrImagePull condition.
func isCrashloopOrImagePull(reason string) bool {
	return reason == waitingReasonCrashLoopBackOff ||
		reason == waitingReasonImagePullBackOff ||
		reason == waitingReasonErrImagePull
}

// hasCrashloopWaiting returns true when any container in the slice is in a
// CrashLoopBackOff/ImagePullBackOff/ErrImagePull waiting state.
func hasCrashloopWaiting(statuses []corev1.ContainerStatus) bool {
	for _, cs := range statuses {
		if cs.State.Waiting != nil && isCrashloopOrImagePull(cs.State.Waiting.Reason) {
			return true
		}
	}
	return false
}

// isProgressingDeployment returns true when the Deployment has an active
// Progressing condition with reason ReplicaSetUpdated or NewReplicaSetCreated.
// These deployments are excluded from workload availability scoring to prevent
// routine chart installs and rolling updates from flashing the health ring.
func isProgressingDeployment(dep *appsv1.Deployment) bool {
	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionTrue {
			r := cond.Reason
			if r == "ReplicaSetUpdated" || r == "NewReplicaSetCreated" {
				return true
			}
		}
	}
	return false
}

// isProgressingStatefulSet returns true when the StatefulSet has an in-flight
// rolling update (updateRevision differs from currentRevision).
func isProgressingStatefulSet(sts *appsv1.StatefulSet) bool {
	return sts.Status.UpdateRevision != "" &&
		sts.Status.CurrentRevision != "" &&
		sts.Status.UpdateRevision != sts.Status.CurrentRevision
}

// defaultStorageClassName returns the name of the default StorageClass (the one
// annotated with storageclass.kubernetes.io/is-default-class=true). Returns ""
// when no default exists.
func defaultStorageClassName(classes []*storagev1.StorageClass) string {
	for _, sc := range classes {
		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return sc.Name
		}
	}
	return ""
}

// isWFFCPending returns true when a Pending PVC should be excluded from the
// health signal because it is waiting for a consumer to be scheduled
// (WaitForFirstConsumer binding mode) and the scheduler has not yet selected
// a node. PVCs in this state are not actionable; their pending status is
// intentional by design and clears automatically once a pod is scheduled.
func isWFFCPending(pvc *corev1.PersistentVolumeClaim, scByName map[string]*storagev1.StorageClass, defaultSC string) bool {
	if pvc.Status.Phase == corev1.ClaimBound {
		return false // not pending
	}
	// Resolve the StorageClass for this PVC.
	// Only fall back to the default class when StorageClassName is nil (unset).
	// An explicit "" means static binding — never WFFC — so do not substitute
	// the default class in that case.
	scName := ""
	if pvc.Spec.StorageClassName != nil {
		scName = *pvc.Spec.StorageClassName
	} else {
		scName = defaultSC
	}
	sc, ok := scByName[scName]
	if !ok {
		return false
	}
	if sc.VolumeBindingMode == nil || *sc.VolumeBindingMode != storagev1.VolumeBindingWaitForFirstConsumer {
		return false
	}
	// WFFC AND no selected-node annotation → truly waiting for a consumer.
	_, hasSelectedNode := pvc.Annotations["volume.kubernetes.io/selected-node"]
	return !hasSelectedNode
}

// countPDBViolations counts PodDisruptionBudgets where currentHealthy < desiredHealthy.
//
// Note on progressing-workload interaction: the plan says to skip PDB checks for
// workloads that are actively progressing "only if cheaply determinable". The check
// requires matching PDB label selectors to pods and then to progressing workloads,
// which is O(pods × PDBs × workloads) and complex. Per the plan's acceptable
// simplification: we count PDB violations normally. Progressing workloads were
// already excluded from the availability signal, so the overall cluster status is
// not double-penalised — the PDB violation may produce a spurious degraded reason
// during a rollout but resolves as soon as the rollout completes.
func countPDBViolations(pdbs []*policyv1.PodDisruptionBudget) int {
	count := 0
	for _, pdb := range pdbs {
		if pdb.Status.CurrentHealthy < pdb.Status.DesiredHealthy {
			count++
		}
	}
	return count
}

// ── Per-signal shaping ────────────────────────────────────────────────────────

// nodeShape is the nodes signal's input: total nodes, Ready nodes, and nodes
// with any pressure condition (Memory/Disk/PID/NetworkUnavailable) True.
type nodeShape struct {
	total, ready, pressure int
}

// shapeNodes counts total, Ready and under-pressure nodes.
func shapeNodes(nodes []*corev1.Node) nodeShape {
	s := nodeShape{total: len(nodes)}
	for _, n := range nodes {
		isReady := false
		hasPressure := false
		for _, c := range n.Status.Conditions {
			switch c.Type {
			case corev1.NodeReady:
				if c.Status == corev1.ConditionTrue {
					isReady = true
				}
			case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure, corev1.NodeNetworkUnavailable:
				if c.Status == corev1.ConditionTrue {
					hasPressure = true
				}
			}
		}
		if isReady {
			s.ready++
		}
		if hasPressure {
			s.pressure++
		}
	}
	return s
}

// podShape is the pods signal's input.
type podShape struct {
	// eligible counts pods with phase Running or Pending and no deletion
	// timestamp; crashloop counts the eligible pods with a container or init
	// container waiting in CrashLoopBackOff, ImagePullBackOff or ErrImagePull.
	eligible, crashloop int
}

// shapePods counts eligible and crash-looping pods.
func shapePods(pods []*corev1.Pod) podShape {
	var s podShape
	for _, p := range pods {
		if p.DeletionTimestamp != nil {
			continue
		}
		if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodPending {
			continue
		}
		s.eligible++
		if hasCrashloopWaiting(p.Status.ContainerStatuses) ||
			hasCrashloopWaiting(p.Status.InitContainerStatuses) {
			s.crashloop++
		}
	}
	return s
}

// workloadShape is one workload kind's contribution to the workloads signal.
type workloadShape struct {
	// desired sums desired replicas over eligible workloads; available sums
	// min(available, desired) over the same set; progressing counts the
	// workloads excluded because a rollout is in flight.
	desired, available, progressing int
}

func (w workloadShape) plus(o workloadShape) workloadShape {
	return workloadShape{
		desired:     w.desired + o.desired,
		available:   w.available + o.available,
		progressing: w.progressing + o.progressing,
	}
}

// addReplicas adds one eligible workload, clamping available to desired
// (surge protection).
func (w *workloadShape) addReplicas(desired, available int) {
	if available > desired {
		available = desired
	}
	w.desired += desired
	w.available += available
}

// shapeDeployments excludes paused, zero-replica and progressing Deployments.
// A nil spec.replicas means one desired replica.
func shapeDeployments(deps []*appsv1.Deployment) workloadShape {
	var s workloadShape
	for _, dep := range deps {
		if dep.Spec.Paused {
			continue
		}
		desired := 1
		if dep.Spec.Replicas != nil {
			desired = int(*dep.Spec.Replicas)
		}
		if desired == 0 {
			continue
		}
		if isProgressingDeployment(dep) {
			s.progressing++
			continue
		}
		s.addReplicas(desired, int(dep.Status.AvailableReplicas))
	}
	return s
}

// shapeStatefulSets excludes zero-replica and progressing StatefulSets and
// scores ready replicas. A nil spec.replicas means one desired replica.
func shapeStatefulSets(stss []*appsv1.StatefulSet) workloadShape {
	var s workloadShape
	for _, sts := range stss {
		desired := 1
		if sts.Spec.Replicas != nil {
			desired = int(*sts.Spec.Replicas)
		}
		if desired == 0 {
			continue
		}
		if isProgressingStatefulSet(sts) {
			s.progressing++
			continue
		}
		s.addReplicas(desired, int(sts.Status.ReadyReplicas))
	}
	return s
}

// shapeDaemonSets scores numberReady against desiredNumberScheduled, skipping
// DaemonSets scheduled nowhere. DaemonSets have no separate progressing
// condition; they roll gradually and are scored like other workloads.
func shapeDaemonSets(dss []*appsv1.DaemonSet) workloadShape {
	var s workloadShape
	for _, ds := range dss {
		desired := int(ds.Status.DesiredNumberScheduled)
		if desired == 0 {
			continue
		}
		s.addReplicas(desired, int(ds.Status.NumberReady))
	}
	return s
}

// countPendingPVCs counts unbound PVCs, excluding those waiting on a
// WaitForFirstConsumer class for a consumer to be scheduled. A PVC with no
// class name resolves to the default StorageClass.
func countPendingPVCs(pvcs []*corev1.PersistentVolumeClaim, classes []*storagev1.StorageClass) int {
	scByName := make(map[string]*storagev1.StorageClass, len(classes))
	for _, sc := range classes {
		scByName[sc.Name] = sc
	}
	defaultSC := defaultStorageClassName(classes)
	pending := 0
	for _, pvc := range pvcs {
		if pvc.Status.Phase == corev1.ClaimBound {
			continue
		}
		if isWFFCPending(pvc, scByName, defaultSC) {
			continue
		}
		pending++
	}
	return pending
}

// ── Per-signal outcomes ───────────────────────────────────────────────────────

// healthReadState is how one health input's read resolved.
type healthReadState int

const (
	// healthReadUnset is the zero value: the mapper recorded nothing. The
	// assembly treats it as a read that did not complete, so an input a mapper
	// forgot can never produce a confident score.
	healthReadUnset healthReadState = iota
	// healthReadOK: the read completed; the outcome carries its value.
	healthReadOK
	// healthReadDenied: the permission check refused the read.
	healthReadDenied
	// healthReadNoSource: there is nothing to read (no alert source, no
	// control-plane source, cert-manager absent).
	healthReadNoSource
	// healthReadUnavailable: the local mapper's degrade-but-score states (cache
	// syncing, an alert query error, a cold certificate cache, a control-plane
	// timeout). The signal is skipped or unknown and a score is still drawn.
	healthReadUnavailable
	// healthReadFailed: an allowed read did not complete. Only the remote
	// mapper records it, and it withholds the score.
	healthReadFailed
)

// healthCause names why a failed read did not complete (refused, truncated,
// timed out, …). The remote mapper owns the vocabulary; the assembly only
// carries it into the missing-input list. It is a fixed class, never error text.
type healthCause string

// healthCauseNotRead is the cause reported for an input left unset.
const healthCauseNotRead healthCause = "not read"

// healthRead is the payload-free part of an outcome.
type healthRead struct {
	state healthReadState
	// status and reason describe a healthReadUnavailable outcome (status is
	// SignalStatusUnknown or SignalStatusSkipped); reason also carries the
	// text for healthReadNoSource. The status reaches the response only for
	// alerts, the one signal the scorer lets resolve either way; every other
	// signal's unavailable status is fixed by computeClusterHealth.
	status SignalStatus
	reason string
	// cause is set for healthReadFailed.
	cause healthCause
}

// healthOutcome is one health input's outcome; value is meaningful only when
// state is healthReadOK.
type healthOutcome[T any] struct {
	healthRead
	value T
}

func (o *healthOutcome[T]) setOK(v T) {
	*o = healthOutcome[T]{healthRead: healthRead{state: healthReadOK}, value: v}
}

func (o *healthOutcome[T]) setDenied() {
	*o = healthOutcome[T]{healthRead: healthRead{state: healthReadDenied}}
}

func (o *healthOutcome[T]) setNoSource(reason string) {
	*o = healthOutcome[T]{healthRead: healthRead{state: healthReadNoSource, reason: reason}}
}

func (o *healthOutcome[T]) setUnavailable(status SignalStatus, reason string) {
	*o = healthOutcome[T]{healthRead: healthRead{state: healthReadUnavailable, status: status, reason: reason}}
}

func (o *healthOutcome[T]) setFailed(cause healthCause) {
	*o = healthOutcome[T]{healthRead: healthRead{state: healthReadFailed, cause: cause}}
}

// incomplete reports a read that did not complete (failed, or never recorded).
func (r healthRead) incomplete() bool {
	return r.state == healthReadFailed || r.state == healthReadUnset
}

// skipReason is the unavailable-reason text for a denied, no-source or
// unavailable outcome.
func (r healthRead) skipReason() string {
	if r.state == healthReadDenied {
		return reasonInsufficientPermissions
	}
	return r.reason
}

// skipStatus is the signal status for a denied, no-source or unavailable
// outcome: a denial or a missing source is skipped, an unavailable outcome
// keeps the status its mapper gave it.
func (r healthRead) skipStatus() SignalStatus {
	if r.state == healthReadUnavailable {
		return r.status
	}
	return SignalStatusSkipped
}

// alertCounts is the alerts input: active and critical alert counts after the
// mapper's exclusions.
type alertCounts struct {
	active, critical int
}

// healthOutcomes holds one outcome per health input.
type healthOutcomes struct {
	nodes          healthOutcome[[]*corev1.Node]
	deployments    healthOutcome[[]*appsv1.Deployment]
	statefulSets   healthOutcome[[]*appsv1.StatefulSet]
	daemonSets     healthOutcome[[]*appsv1.DaemonSet]
	pods           healthOutcome[[]*corev1.Pod]
	pdbs           healthOutcome[[]*policyv1.PodDisruptionBudget]
	pvcs           healthOutcome[[]*corev1.PersistentVolumeClaim]
	storageClasses healthOutcome[[]*storagev1.StorageClass]
	alerts         healthOutcome[alertCounts]
	// certificates carries the critical-bucket certificate count.
	certificates healthOutcome[int]
	controlPlane healthOutcome[ControlPlaneStates]
}

// Input names reported in the missing-input list. Kubernetes kinds use their
// RBAC resource names; the rest use their signal names.
const (
	healthInputNodes          = "nodes"
	healthInputDeployments    = "deployments"
	healthInputStatefulSets   = "statefulsets"
	healthInputDaemonSets     = "daemonsets"
	healthInputPods           = "pods"
	healthInputPDBs           = "poddisruptionbudgets"
	healthInputPVCs           = "persistentvolumeclaims"
	healthInputStorageClasses = "storageclasses"
)

// missingHealthInput names one input whose read did not complete.
type missingHealthInput struct {
	input string
	cause healthCause
}

// healthAssembly collects missing inputs while outcomes are resolved.
type healthAssembly struct {
	missing []missingHealthInput
}

// usable reports whether r completed. An incomplete read is recorded as
// missing; a denied, no-source or unavailable read is neither.
func (a *healthAssembly) usable(input string, r healthRead) bool {
	switch r.state {
	case healthReadOK:
		return true
	case healthReadFailed:
		a.missing = append(a.missing, missingHealthInput{input: input, cause: r.cause})
	case healthReadUnset:
		a.missing = append(a.missing, missingHealthInput{input: input, cause: healthCauseNotRead})
	}
	return false
}

// assembleHealthInputs turns per-input outcomes into HealthInputs under the
// local scoring rules (KTD4):
//
//   - Workloads and the control plane are gated on the nodes permission: a
//     denied nodes read makes both unavailable for insufficient permissions,
//     whatever their own outcomes.
//   - A completed nodes read with zero nodes leaves the nodes signal
//     unavailable ("no nodes visible"), which makes the score unknown.
//   - Workloads are unavailable when every kind is denied; otherwise the first
//     unavailable kind's reason wins; otherwise denied kinds are omitted and
//     the rest are summed.
//   - Storage takes the PVC outcome's reason first, then the StorageClass one.
//   - A denied or no-source outcome is skipped; an unavailable outcome passes
//     its reason and status through.
//
// When any input that feeds the score did not complete, inputs is the zero
// value and must not be scored; missing lists every such input, with its
// cause, in fixed order: nodes, deployments, statefulsets, daemonsets, pods,
// poddisruptionbudgets, alerts, certificates, persistentvolumeclaims,
// storageclasses, controlPlane. Inputs gated off by a denied nodes read are
// never reported.
func assembleHealthInputs(o healthOutcomes) (inputs HealthInputs, missing []missingHealthInput) {
	var a healthAssembly
	var in HealthInputs

	// ── Nodes ────────────────────────────────────────────────────────────────
	nodesDenied := o.nodes.state == healthReadDenied
	switch {
	case !a.usable(healthInputNodes, o.nodes.healthRead):
		in.NodesUnavailableReason = o.nodes.skipReason()
	case len(o.nodes.value) == 0:
		in.NodesUnavailableReason = reasonNoNodesVisible
	default:
		s := shapeNodes(o.nodes.value)
		in.NodesAvailable = true
		in.TotalNodes, in.ReadyNodes, in.PressureNodes = s.total, s.ready, s.pressure
	}

	// ── Workloads ────────────────────────────────────────────────────────────
	if nodesDenied {
		in.WorkloadsUnavailableReason = reasonInsufficientPermissions
	} else {
		depOK := a.usable(healthInputDeployments, o.deployments.healthRead)
		stsOK := a.usable(healthInputStatefulSets, o.statefulSets.healthRead)
		dsOK := a.usable(healthInputDaemonSets, o.daemonSets.healthRead)

		kinds := []healthRead{o.deployments.healthRead, o.statefulSets.healthRead, o.daemonSets.healthRead}
		allDenied := true
		blocked, blockedReason := false, ""
		for _, k := range kinds {
			if k.state != healthReadDenied {
				allDenied = false
			}
			if !blocked && (k.state == healthReadUnavailable || k.state == healthReadNoSource) {
				blocked, blockedReason = true, k.reason
			}
		}

		switch {
		case allDenied:
			in.WorkloadsUnavailableReason = reasonInsufficientPermissions
		case blocked:
			in.WorkloadsUnavailableReason = blockedReason
		default:
			var s workloadShape
			if depOK {
				s = s.plus(shapeDeployments(o.deployments.value))
			}
			if stsOK {
				s = s.plus(shapeStatefulSets(o.statefulSets.value))
			}
			if dsOK {
				s = s.plus(shapeDaemonSets(o.daemonSets.value))
			}
			in.WorkloadsAvailable = true
			in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount = s.desired, s.available, s.progressing
		}
	}

	// ── Pods ─────────────────────────────────────────────────────────────────
	if a.usable(healthInputPods, o.pods.healthRead) {
		s := shapePods(o.pods.value)
		in.PodsAvailable = true
		in.EligiblePods, in.CrashloopPods = s.eligible, s.crashloop
	} else {
		in.PodsUnavailableReason = o.pods.skipReason()
	}

	// ── PDBs ─────────────────────────────────────────────────────────────────
	if a.usable(healthInputPDBs, o.pdbs.healthRead) {
		in.PDBAvailable = true
		in.PDBViolations = countPDBViolations(o.pdbs.value)
	} else {
		in.PDBUnavailableReason = o.pdbs.skipReason()
	}

	// ── Alerts ───────────────────────────────────────────────────────────────
	if a.usable(signalAlerts, o.alerts.healthRead) {
		in.AlertsAvailable = true
		in.AlertsActive, in.AlertsCritical = o.alerts.value.active, o.alerts.value.critical
	} else {
		in.AlertsSkipped = o.alerts.skipStatus() == SignalStatusSkipped
		in.AlertsUnavailableReason = o.alerts.skipReason()
	}

	// ── Certificates ─────────────────────────────────────────────────────────
	if a.usable(signalCerts, o.certificates.healthRead) {
		in.CertsAvailable = true
		in.CertCritical = o.certificates.value
	} else {
		in.CertsUnavailableReason = o.certificates.skipReason()
	}

	// ── Storage ──────────────────────────────────────────────────────────────
	pvcOK := a.usable(healthInputPVCs, o.pvcs.healthRead)
	scOK := a.usable(healthInputStorageClasses, o.storageClasses.healthRead)
	switch {
	case !pvcOK:
		in.StorageUnavailableReason = o.pvcs.skipReason()
	case !scOK:
		in.StorageUnavailableReason = o.storageClasses.skipReason()
	default:
		in.StorageAvailable = true
		in.PendingPVCs = countPendingPVCs(o.pvcs.value, o.storageClasses.value)
	}

	// ── Control plane ────────────────────────────────────────────────────────
	switch {
	case nodesDenied:
		in.ControlPlaneUnavailableReason = reasonInsufficientPermissions
	case a.usable(signalControlPlane, o.controlPlane.healthRead):
		in.ControlPlaneAvailable = true
		in.ControlPlane = o.controlPlane.value
	default:
		in.ControlPlaneUnavailableReason = o.controlPlane.skipReason()
	}

	if len(a.missing) > 0 {
		return HealthInputs{}, a.missing
	}
	return in, nil
}
