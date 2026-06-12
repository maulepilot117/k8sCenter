package resources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
)

// DashboardSummary is the response payload for GET /api/v1/cluster/dashboard-summary.
type DashboardSummary struct {
	Nodes    NodeSummary    `json:"nodes"`
	Pods     PodSummary     `json:"pods"`
	Services ServiceCount   `json:"services"`
	Alerts   AlertSummary   `json:"alerts"`
	CPU      *Utilization   `json:"cpu"`
	Memory   *Utilization   `json:"memory"`
	Health   *ClusterHealth `json:"health"` // always present in new-backend responses (no omitempty)
}

// NodeSummary contains node counts.
type NodeSummary struct {
	Total int `json:"total"`
	Ready int `json:"ready"`
}

// PodSummary contains pod phase counts.
type PodSummary struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

// ServiceCount contains the total number of services.
type ServiceCount struct {
	Total int `json:"total"`
}

// AlertSummary contains active alert counts.
type AlertSummary struct {
	Active   int `json:"active"`
	Critical int `json:"critical"`
}

// Utilization holds resource utilization data including requests and limits.
type Utilization struct {
	Percentage float64 `json:"percentage"`
	Used       string  `json:"used"`
	Total      string  `json:"total"`
	Requests   string  `json:"requests"`
	Limits     string  `json:"limits"`
}

// DashboardTrends holds short historical series (one value per sample step) that
// back the sparklines on the dashboard metric cards. Each slice is ordered
// oldest→newest. A slice may be empty when Prometheus or kube-state-metrics is
// unavailable; the frontend renders no sparkline in that case rather than a
// misleading flat line. Window and Step describe the range that produced the
// series (e.g. "1h" / "2m").
type DashboardTrends struct {
	Nodes    []float64 `json:"nodes"`
	Pods     []float64 `json:"pods"`
	Services []float64 `json:"services"`
	Alerts   []float64 `json:"alerts"`
	// CPU and Memory are cluster-wide utilization percentages (0–100), one
	// value per step — the historical companions to the instant percentages
	// in DashboardSummary.CPU/Memory.
	CPU    []float64 `json:"cpu"`
	Memory []float64 `json:"memory"`
	Window string    `json:"window"`
	Step   string    `json:"step"`
}

// Waiting reason predicates copied from diagnostics/rules.go — exact string
// match required so dashboard and diagnostics page never disagree on the same pod.
const (
	waitingReasonCrashLoopBackOff = "CrashLoopBackOff"
	waitingReasonImagePullBackOff = "ImagePullBackOff"
	waitingReasonErrImagePull     = "ErrImagePull"
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

// gatherHealthInputs collects all signal inputs for computeClusterHealth from
// the informer cache and optional providers. Per-signal RBAC and sync gates
// are applied; each gated signal resolves to skipped/unknown rather than feeding
// zero into the score (R5, R9).
//
// canListNodes is the pre-resolved result of h.canList(ctx, user, "nodes", ""),
// hoisted by HandleDashboardSummary so a single SAR decision is reused across
// the nodes signal, the workloads guard, and the control-plane gate.
//
// The control-plane goroutine is dispatched immediately after the nodes decision
// so it runs concurrently with remaining per-signal SARs rather than after them.
// It joins the caller-provided promCtx (the 1-second shared Prometheus budget)
// and WaitGroup wg, which the caller must Wait() on before reading the inputs.
//
// isSyncedFn is consulted per-resource before reading the informer cache.
// It defaults to h.Informers.IsSynced but can be overridden in tests via
// h.isSynced (see Handler field). The caller should pass h.syncedFn() which
// selects the live or test override.
func (h *Handler) gatherHealthInputs(
	ctx context.Context,
	user *auth.User,
	canListNodes bool,
	nodes []*corev1.Node,
	pods []*corev1.Pod,
	wg *sync.WaitGroup,
	promCtx context.Context,
	inputs *HealthInputs,
	cpResult *ControlPlaneStates,
	cpErr *error,
) {
	// ── Nodes signal ────────────────────────────────────────────────────────
	if !canListNodes {
		inputs.NodesAvailable = false
		inputs.NodesUnavailableReason = "insufficient permissions"
		inputs.WorkloadsAvailable = false
		inputs.WorkloadsUnavailableReason = "insufficient permissions"
		inputs.ControlPlaneAvailable = false
		inputs.ControlPlaneUnavailableReason = "insufficient permissions"
	} else if !h.isSyncedFn()("nodes") {
		inputs.NodesAvailable = false
		inputs.NodesUnavailableReason = "cache syncing"
	} else if len(nodes) == 0 {
		// 0 nodes visible despite list permission — treat as unknown rather than
		// healthy with a 0-weighted node sub-score (plan veto table).
		inputs.NodesAvailable = false
		inputs.NodesUnavailableReason = "no nodes visible"
	} else {
		inputs.NodesAvailable = true
		inputs.TotalNodes = len(nodes)
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
				inputs.ReadyNodes++
			}
			if hasPressure {
				inputs.PressureNodes++
			}
		}
	}

	// ── Control-plane signal ─────────────────────────────────────────────────
	// Dispatched immediately after the nodes decision so the goroutine runs
	// concurrently with the remaining per-signal SAR round-trips rather than
	// after them. The 1-second promCtx budget starts at the top of
	// HandleDashboardSummary; dispatching here maximises the time the goroutine
	// has to complete before the WaitGroup is joined. Gated on canListNodes
	// (infrastructure visibility — same guard as the nodes signal).
	if canListNodes && h.ControlPlane != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			states, err := h.ControlPlane.ControlPlaneStatus(promCtx)
			*cpErr = err
			*cpResult = states
		}()
	}

	// ── Pods signal ──────────────────────────────────────────────────────────
	if !h.canList(ctx, user, "pods", "") {
		inputs.PodsAvailable = false
		inputs.PodsUnavailableReason = "insufficient permissions"
	} else if !h.isSyncedFn()("pods") {
		inputs.PodsAvailable = false
		inputs.PodsUnavailableReason = "cache syncing"
	} else {
		inputs.PodsAvailable = true
		for _, p := range pods {
			// Eligible pods: phase Running or Pending AND DeletionTimestamp nil.
			if p.DeletionTimestamp != nil {
				continue
			}
			if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodPending {
				continue
			}
			inputs.EligiblePods++
			// Walk ContainerStatuses and InitContainerStatuses for waiting reasons.
			if hasCrashloopWaiting(p.Status.ContainerStatuses) ||
				hasCrashloopWaiting(p.Status.InitContainerStatuses) {
				inputs.CrashloopPods++
			}
		}
	}

	// ── Workloads signal ─────────────────────────────────────────────────────
	// Guard: only computed when canListNodes is true (already checked above).
	if canListNodes {
		canDeploy := h.canList(ctx, user, "deployments", "")
		canSTS := h.canList(ctx, user, "statefulsets", "")
		canDS := h.canList(ctx, user, "daemonsets", "")

		if !canDeploy && !canSTS && !canDS {
			inputs.WorkloadsAvailable = false
			inputs.WorkloadsUnavailableReason = "insufficient permissions"
		} else if !h.isSyncedFn()("deployments") || !h.isSyncedFn()("statefulsets") || !h.isSyncedFn()("daemonsets") {
			inputs.WorkloadsAvailable = false
			inputs.WorkloadsUnavailableReason = "cache syncing"
		} else {
			inputs.WorkloadsAvailable = true
			var totalDesired, totalAvailable, progressingCount int

			// Deployments
			if canDeploy {
				if deps, err := h.Informers.Deployments().List(labels.Everything()); err == nil {
					for _, dep := range deps {
						// Exclude: spec.paused
						if dep.Spec.Paused {
							continue
						}
						desired := 1
						if dep.Spec.Replicas != nil {
							desired = int(*dep.Spec.Replicas)
						}
						// Exclude: desired == 0
						if desired == 0 {
							continue
						}
						// Exclude: actively progressing (rollout in flight)
						if isProgressingDeployment(dep) {
							progressingCount++
							continue
						}
						available := int(dep.Status.AvailableReplicas)
						// Clamp available to min(available, desired) — surge protection.
						if available > desired {
							available = desired
						}
						totalDesired += desired
						totalAvailable += available
					}
				}
			}

			// StatefulSets
			if canSTS {
				if stss, err := h.Informers.StatefulSets().List(labels.Everything()); err == nil {
					for _, sts := range stss {
						desired := 1
						if sts.Spec.Replicas != nil {
							desired = int(*sts.Spec.Replicas)
						}
						// Exclude: desired == 0
						if desired == 0 {
							continue
						}
						// Exclude: actively progressing (update in flight)
						if isProgressingStatefulSet(sts) {
							progressingCount++
							continue
						}
						available := int(sts.Status.ReadyReplicas)
						if available > desired {
							available = desired
						}
						totalDesired += desired
						totalAvailable += available
					}
				}
			}

			// DaemonSets
			if canDS {
				if dss, err := h.Informers.DaemonSets().List(labels.Everything()); err == nil {
					for _, ds := range dss {
						desired := int(ds.Status.DesiredNumberScheduled)
						// Exclude: desiredNumberScheduled == 0
						if desired == 0 {
							continue
						}
						// DaemonSets don't have a separate "progressing" condition;
						// they roll gradually. Exclude none here — they're scored via
						// numberReady vs desiredNumberScheduled like other workloads.
						available := int(ds.Status.NumberReady)
						if available > desired {
							available = desired
						}
						totalDesired += desired
						totalAvailable += available
					}
				}
			}

			inputs.WorkloadsDesired = totalDesired
			inputs.WorkloadsActuallyAvailable = totalAvailable
			inputs.ProgressingCount = progressingCount
		}
	}

	// ── PDB signal ───────────────────────────────────────────────────────────
	if !h.canList(ctx, user, "poddisruptionbudgets", "") {
		inputs.PDBAvailable = false
		inputs.PDBUnavailableReason = "insufficient permissions"
	} else if !h.isSyncedFn()("poddisruptionbudgets") {
		inputs.PDBAvailable = false
		inputs.PDBUnavailableReason = "cache syncing"
	} else {
		inputs.PDBAvailable = true
		if pdbs, err := h.Informers.PodDisruptionBudgets().List(labels.Everything()); err == nil {
			inputs.PDBViolations = countPDBViolations(pdbs)
		}
	}

	// ── Storage signal ───────────────────────────────────────────────────────
	// StorageClass read is SA-level classification metadata — no per-user gate.
	if !h.canList(ctx, user, "persistentvolumeclaims", "") {
		inputs.StorageAvailable = false
		inputs.StorageUnavailableReason = "insufficient permissions"
	} else if !h.isSyncedFn()("persistentvolumeclaims") || !h.isSyncedFn()("storageclasses") {
		inputs.StorageAvailable = false
		inputs.StorageUnavailableReason = "cache syncing"
	} else {
		inputs.StorageAvailable = true
		// Build StorageClass lookup map for WFFC exclusion predicate.
		scByName := make(map[string]*storagev1.StorageClass)
		defaultSC := ""
		if classes, err := h.Informers.StorageClasses().List(labels.Everything()); err == nil {
			for _, sc := range classes {
				scByName[sc.Name] = sc
			}
			defaultSC = defaultStorageClassName(classes)
		}
		if pvcs, err := h.Informers.PersistentVolumeClaims().List(labels.Everything()); err == nil {
			for _, pvc := range pvcs {
				if pvc.Status.Phase == corev1.ClaimBound {
					continue
				}
				// Exclude WFFC PVCs awaiting a consumer (intentional pending).
				if isWFFCPending(pvc, scByName, defaultSC) {
					continue
				}
				inputs.PendingPVCs++
			}
		}
	}

	// ── Alerts signal ────────────────────────────────────────────────────────
	if h.Alerts == nil {
		inputs.AlertsAvailable = false
		inputs.AlertsSkipped = true
		inputs.AlertsUnavailableReason = "alerting not configured"
	} else if !h.canList(ctx, user, "alertmanagers", "") {
		inputs.AlertsAvailable = false
		inputs.AlertsSkipped = true
		inputs.AlertsUnavailableReason = "insufficient permissions"
	} else {
		active, critical, err := h.Alerts.ActiveAlertCountsExcluding(ctx, "Watchdog", "DeadMansSwitch")
		if err != nil {
			inputs.AlertsAvailable = false
			// AlertsSkipped remains false — query error maps to unknown.
			inputs.AlertsUnavailableReason = "alerting unavailable"
		} else {
			inputs.AlertsAvailable = true
			inputs.AlertsActive = active
			inputs.AlertsCritical = critical
		}
	}

	// ── Certificates signal ──────────────────────────────────────────────────
	if h.CertExpiry == nil {
		inputs.CertsAvailable = false
		inputs.CertsUnavailableReason = "cert-manager not configured"
	} else {
		// Only the critical bucket feeds health; the warning count is intentionally
		// ignored here so certs that are still valid (8–30 days out) don't degrade
		// the cluster. The observatory page surfaces the warning bucket separately.
		_, crit, certErr := h.CertExpiry.ExpiringCounts(ctx, user)
		if certErr == nil {
			inputs.CertsAvailable = true
			inputs.CertCritical = crit
		} else {
			inputs.CertsAvailable = false
			switch {
			case errors.Is(certErr, ErrCertManagerNotInstalled):
				inputs.CertsUnavailableReason = "cert-manager not installed"
			case errors.Is(certErr, ErrCertCacheNotWarm):
				inputs.CertsUnavailableReason = "cert cache warming"
			default:
				// Do not leak internal error text into reasons (R3 / CLAUDE.md security rule).
				inputs.CertsUnavailableReason = "certificate data unavailable"
			}
		}
	}

}

// isSyncedFn returns the active IsSynced function — the test override if set,
// or h.Informers.IsSynced otherwise. This allows tests to inject a fake sync
// state without modifying the real InformerManager.
func (h *Handler) isSyncedFn() func(string) bool {
	if h.isSynced != nil {
		return h.isSynced
	}
	return h.Informers.IsSynced
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

// HandleDashboardSummary returns aggregated cluster health data from the informer cache.
// Prometheus metrics (CPU/Memory) are fetched asynchronously with a 1-second timeout.
// Only available for the local cluster (informer cache is not available for remote clusters).
func (h *Handler) HandleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	// Check for local cluster only
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if clusterID != "" && clusterID != "local" {
		writeError(w, http.StatusBadRequest, "dashboard summary is only available for the local cluster", "")
		return
	}

	summary := DashboardSummary{}
	ctx := r.Context()

	// Resolve canListNodes once here so the same SAR decision is shared by the
	// informer fetch, the health-input gathering, and the control-plane signal
	// resolution below — preventing a second SAR call if the 60s cache expires
	// mid-request. FIX 7: single SAR decision per request.
	canListNodes := h.canList(ctx, user, "nodes", "")

	// Fetch nodes and pods once from informer cache — reused for both counts and utilization.
	var nodes []*corev1.Node
	var pods []*corev1.Pod

	if canListNodes {
		if n, err := h.Informers.Nodes().List(labels.Everything()); err == nil {
			nodes = n
		}
	}
	if h.canList(ctx, user, "pods", "") {
		if p, err := h.Informers.Pods().List(labels.Everything()); err == nil {
			pods = p
		}
	}

	// Node counts
	summary.Nodes.Total = len(nodes)
	for _, n := range nodes {
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				summary.Nodes.Ready++
				break
			}
		}
	}

	// Pod phase counts
	summary.Pods.Total = len(pods)
	for _, p := range pods {
		switch p.Status.Phase {
		case corev1.PodRunning:
			summary.Pods.Running++
		case corev1.PodPending:
			summary.Pods.Pending++
		case corev1.PodFailed:
			summary.Pods.Failed++
		}
	}

	// Service count
	if h.canList(ctx, user, "services", "") {
		if services, err := h.Informers.Services().List(labels.Everything()); err == nil {
			summary.Services.Total = len(services)
		}
	}

	// Alert counts
	if h.Alerts != nil {
		if h.canList(ctx, user, "alertmanagers", "") {
			active, critical, err := h.Alerts.ActiveAlertCounts(ctx)
			if err == nil {
				summary.Alerts.Active = active
				summary.Alerts.Critical = critical
			}
		}
	}

	// Aggregate resource requests/limits from cached node and pod lists.
	var cpuRequests, cpuLimits, memRequests, memLimits resource.Quantity
	var cpuAllocatable, memAllocatable resource.Quantity

	for _, n := range nodes {
		if cpu, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
			cpuAllocatable.Add(cpu)
		}
		if mem, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			memAllocatable.Add(mem)
		}
	}

	for _, p := range pods {
		if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodPending {
			continue
		}
		for _, c := range p.Spec.Containers {
			if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				cpuRequests.Add(req)
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
				cpuLimits.Add(lim)
			}
			if req, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				memRequests.Add(req)
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				memLimits.Add(lim)
			}
		}
	}

	// Format helpers
	formatCPU := func(q resource.Quantity) string {
		millis := q.MilliValue()
		if millis >= 1000 {
			return fmt.Sprintf("%.1f cores", float64(millis)/1000)
		}
		return fmt.Sprintf("%dm", millis)
	}
	formatMem := func(q resource.Quantity) string {
		bytes := q.Value()
		gi := float64(bytes) / (1024 * 1024 * 1024)
		if gi >= 1 {
			return fmt.Sprintf("%.1f Gi", gi)
		}
		mi := float64(bytes) / (1024 * 1024)
		return fmt.Sprintf("%.0f Mi", mi)
	}

	cpuTotalStr := formatCPU(cpuAllocatable)
	memTotalStr := formatMem(memAllocatable)

	// Run CPU/memory queries and the control-plane health check concurrently
	// within a shared 1-second Prometheus budget. We use sync.WaitGroup (not
	// errgroup) so a failure in one query does not cancel the others.
	// The block runs when either Utilization or ControlPlane is non-nil.
	var healthInputs HealthInputs
	var cpResult ControlPlaneStates
	var cpErr error

	if h.Utilization != nil || h.ControlPlane != nil {
		promCtx, promCancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer promCancel()

		var wg sync.WaitGroup
		var cpuPct, memPct float64
		var cpuErr, memErr error

		if h.Utilization != nil {
			wg.Add(2)
			go func() {
				defer wg.Done()
				cpuPct, cpuErr = h.Utilization.CPUPercent(promCtx)
			}()
			go func() {
				defer wg.Done()
				memPct, memErr = h.Utilization.MemoryPercent(promCtx)
			}()
		}

		// Gather health inputs — the control-plane goroutine (if any) is added
		// to wg inside gatherHealthInputs. canListNodes is pre-resolved above.
		h.gatherHealthInputs(ctx, user, canListNodes, nodes, pods, &wg, promCtx, &healthInputs, &cpResult, &cpErr)

		wg.Wait()

		if h.Utilization != nil {
			if cpuErr == nil {
				cpuUsedMillis := cpuPct / 100 * float64(cpuAllocatable.MilliValue())
				summary.CPU = &Utilization{
					Percentage: cpuPct,
					Used:       formatCPU(*resource.NewMilliQuantity(int64(cpuUsedMillis), resource.DecimalSI)),
					Total:      cpuTotalStr,
					Requests:   formatCPU(cpuRequests),
					Limits:     formatCPU(cpuLimits),
				}
			}
			if memErr == nil {
				memUsedBytes := memPct / 100 * float64(memAllocatable.Value())
				summary.Memory = &Utilization{
					Percentage: memPct,
					Used:       formatMem(*resource.NewQuantity(int64(memUsedBytes), resource.BinarySI)),
					Total:      memTotalStr,
					Requests:   formatMem(memRequests),
					Limits:     formatMem(memLimits),
				}
			}
		}
	} else {
		// No Prometheus-backed providers — still gather informer-based health inputs.
		// The WaitGroup and promCtx are unused for the control-plane goroutine because
		// h.ControlPlane is nil; pass a dummy context and WaitGroup.
		var wg sync.WaitGroup
		dummyCtx, dummyCancel := context.WithCancel(ctx)
		dummyCancel() // immediately cancelled — control-plane goroutine won't be started
		h.gatherHealthInputs(ctx, user, canListNodes, nodes, pods, &wg, dummyCtx, &healthInputs, &cpResult, &cpErr)
		wg.Wait()
	}

	// Resolve control-plane signal from the goroutine result (if it ran).
	// Reuse canListNodes resolved above — no second SAR call.
	if h.ControlPlane != nil && canListNodes {
		if cpErr != nil {
			healthInputs.ControlPlaneAvailable = false
			healthInputs.ControlPlaneUnavailableReason = "prometheus timeout or unavailable"
		} else {
			healthInputs.ControlPlaneAvailable = true
			healthInputs.ControlPlane = cpResult
		}
	}

	// If Prometheus was unavailable, still provide requests/limits/total from k8s API
	if summary.CPU == nil && cpuAllocatable.MilliValue() > 0 {
		summary.CPU = &Utilization{
			Percentage: 0,
			Used:       "N/A",
			Total:      cpuTotalStr,
			Requests:   formatCPU(cpuRequests),
			Limits:     formatCPU(cpuLimits),
		}
	}
	if summary.Memory == nil && memAllocatable.Value() > 0 {
		summary.Memory = &Utilization{
			Percentage: 0,
			Used:       "N/A",
			Total:      memTotalStr,
			Requests:   formatMem(memRequests),
			Limits:     formatMem(memLimits),
		}
	}

	// Compute and attach cluster health (always present, never nil).
	health := computeClusterHealth(healthInputs)
	summary.Health = &health

	writeJSON(w, http.StatusOK, api.Response{Data: summary})
}

// HandleDashboardTrends returns short historical series for the dashboard metric
// cards (node/pod/service/alert counts) sourced from Prometheus range queries.
// Kept separate from HandleDashboardSummary so its multi-second range queries do
// not eat into that endpoint's 1-second Prometheus budget. Local cluster only.
//
// When monitoring is unavailable (h.Trends == nil) or a query fails, the
// response carries empty series and HTTP 200 — the dashboard degrades to no
// sparklines rather than erroring.
func (h *Handler) HandleDashboardTrends(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	if clusterID != "" && clusterID != "local" {
		writeError(w, http.StatusBadRequest, "dashboard trends are only available for the local cluster", "")
		return
	}

	if h.Trends == nil {
		writeJSON(w, http.StatusOK, api.Response{Data: DashboardTrends{}})
		return
	}

	trends, err := h.Trends.DashboardTrends(r.Context())
	if err != nil {
		// Prometheus hiccups should not surface as a dashboard error — log and
		// return empty series so the cards still render their current counts.
		if h.Logger != nil {
			h.Logger.Warn("dashboard trends query failed", "error", err)
		}
		writeJSON(w, http.StatusOK, api.Response{Data: DashboardTrends{}})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{Data: trends})
}
