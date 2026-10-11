package resources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	corev1 "k8s.io/api/core/v1"
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
	// Coverage is set only on the opt-in remote path (?coverage=1). omitempty
	// keeps the local response byte-identical to its pre-coverage shape.
	Coverage []SectionCoverage `json:"coverage,omitempty"`
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
	// CPU and Memory are cluster-wide utilization percentages (0–100), one
	// value per step — the historical companions to the instant percentages
	// in DashboardSummary.CPU/Memory.
	CPU    []float64 `json:"cpu"`
	Memory []float64 `json:"memory"`
	// NetworkRx and NetworkTx are cluster-wide network throughput in Mbps
	// (megabits/sec), one value per step. They back the Network I/O metric
	// tile's RX/TX sparklines; the frontend derives the displayed p95 from
	// these series so it tracks the selected time window automatically.
	NetworkRx []float64 `json:"networkRx"`
	NetworkTx []float64 `json:"networkTx"`
	Window    string    `json:"window"`
	Step      string    `json:"step"`
}

// gatherHealthInputs is the local health mapper: it turns informer reads and
// local permission checks into one outcome per health input, which
// assembleHealthInputs (health_shaping.go) turns into HealthInputs. The local
// mapper never records a failed read; its degrade-but-score states (cache
// syncing, an alert query error, a cold or failing certificate read) are
// unavailable outcomes, so a local score is always drawn from what resolved.
//
// canListNodes is the pre-resolved result of h.canList(ctx, user, "nodes", ""),
// hoisted by HandleDashboardSummary so a single SAR decision is reused across
// the nodes signal, the workloads guard, and the control-plane gate. nodes and
// pods are the slices the tile counts were computed from (R8).
//
// The control-plane goroutine is dispatched immediately after the nodes decision
// so it runs concurrently with remaining per-signal SARs rather than after them.
// It joins the caller-provided promCtx (the 1-second shared Prometheus budget)
// and WaitGroup wg; the caller must Wait() on wg and then record the
// control-plane outcome from cpResult and cpErr.
//
// Readiness is consulted per resource through h.isSyncedFn(), which tests
// override via h.isSynced (see Handler field).
func (h *Handler) gatherHealthInputs(
	ctx context.Context,
	user *auth.User,
	canListNodes bool,
	nodes []*corev1.Node,
	pods []*corev1.Pod,
	wg *sync.WaitGroup,
	promCtx context.Context,
	cpResult *ControlPlaneStates,
	cpErr *error,
) healthOutcomes {
	synced := h.isSyncedFn()
	var o healthOutcomes

	// ── Nodes ────────────────────────────────────────────────────────────────
	switch {
	case !canListNodes:
		o.nodes.setDenied()
	case !synced("nodes"):
		o.nodes.setUnavailable(SignalStatusUnknown, reasonCacheSyncing)
	default:
		o.nodes.setOK(nodes)
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
			// gatherHealthInputs runs on the local path only (HandleDashboardSummary
			// sends remote clusters to handleRemoteDashboardSummary first).
			states, err := h.ControlPlane.ControlPlaneStatus(promCtx, k8s.LocalClusterID)
			*cpErr = err
			*cpResult = states
		}()
	}

	// ── Pods ─────────────────────────────────────────────────────────────────
	switch {
	case !h.canList(ctx, user, "pods", ""):
		o.pods.setDenied()
	case !synced("pods"):
		o.pods.setUnavailable(SignalStatusUnknown, reasonCacheSyncing)
	default:
		o.pods.setOK(pods)
	}

	// ── Workloads ────────────────────────────────────────────────────────────
	// The workload SARs run only when nodes are listable; the assembly gates
	// the workloads signal on the nodes permission. Any workload informer
	// still syncing holds back every kind the user may list.
	if canListNodes {
		canDeploy := h.canList(ctx, user, "deployments", "")
		canSTS := h.canList(ctx, user, "statefulsets", "")
		canDS := h.canList(ctx, user, "daemonsets", "")
		syncing := !synced("deployments") || !synced("statefulsets") || !synced("daemonsets")

		o.deployments = localListOutcome(canDeploy, syncing, SignalStatusUnknown, h.Informers.Deployments().List)
		o.statefulSets = localListOutcome(canSTS, syncing, SignalStatusUnknown, h.Informers.StatefulSets().List)
		o.daemonSets = localListOutcome(canDS, syncing, SignalStatusUnknown, h.Informers.DaemonSets().List)
	}

	// ── PDBs ─────────────────────────────────────────────────────────────────
	o.pdbs = localListOutcome(
		h.canList(ctx, user, "poddisruptionbudgets", ""),
		!synced("poddisruptionbudgets"),
		SignalStatusSkipped,
		h.Informers.PodDisruptionBudgets().List,
	)

	// ── Storage ──────────────────────────────────────────────────────────────
	// The StorageClass read is service-account classification metadata, so it
	// has no per-user gate locally. Either informer still syncing holds back both.
	storageSyncing := !synced("persistentvolumeclaims") || !synced("storageclasses")
	o.pvcs = localListOutcome(
		h.canList(ctx, user, "persistentvolumeclaims", ""),
		storageSyncing,
		SignalStatusSkipped,
		h.Informers.PersistentVolumeClaims().List,
	)
	o.storageClasses = localListOutcome(true, storageSyncing, SignalStatusSkipped, h.Informers.StorageClasses().List)

	// ── Alerts ───────────────────────────────────────────────────────────────
	switch {
	case h.Alerts == nil:
		o.alerts.setNoSource("alerting not configured")
	case !h.canList(ctx, user, "alertmanagers", ""):
		o.alerts.setDenied()
	default:
		active, critical, err := h.Alerts.ActiveAlertCountsExcluding(ctx, "Watchdog", "DeadMansSwitch")
		if err != nil {
			// A query error maps to unknown rather than skipped.
			o.alerts.setUnavailable(SignalStatusUnknown, "alerting unavailable")
		} else {
			o.alerts.setOK(alertCounts{active: active, critical: critical})
		}
	}

	// ── Certificates ─────────────────────────────────────────────────────────
	if h.CertExpiry == nil {
		o.certificates.setNoSource("cert-manager not configured")
	} else {
		// Only the critical bucket feeds health; the warning count is intentionally
		// ignored here so certs that are still valid (8–30 days out) don't degrade
		// the cluster. The observatory page surfaces the warning bucket separately.
		expiry, certErr := h.CertExpiry.ExpiringCountsForCluster(ctx, k8s.LocalClusterID, user)
		switch {
		case certErr == nil:
			o.certificates.setOK(expiry.Critical)
		case errors.Is(certErr, ErrCertManagerNotInstalled):
			o.certificates.setNoSource("cert-manager not installed")
		case errors.Is(certErr, ErrCertCacheNotWarm):
			o.certificates.setUnavailable(SignalStatusSkipped, "cert cache warming")
		default:
			// Do not leak internal error text into reasons (R3 / CLAUDE.md security rule).
			o.certificates.setUnavailable(SignalStatusSkipped, "certificate data unavailable")
		}
	}

	return o
}

// localListOutcome maps one informer-backed input to its outcome: denied,
// unavailable while its informer syncs (with the signal's status), or the
// cached list. A lister reads the informer cache; on the rare list error the
// kind contributes no items, as it always has on the local path.
func localListOutcome[T any](allowed, syncing bool, syncStatus SignalStatus, list func(labels.Selector) ([]T, error)) healthOutcome[[]T] {
	var o healthOutcome[[]T]
	switch {
	case !allowed:
		o.setDenied()
	case syncing:
		o.setUnavailable(syncStatus, reasonCacheSyncing)
	default:
		items, _ := list(labels.Everything())
		o.setOK(items)
	}
	return o
}

// localControlPlaneOutcome maps the control-plane goroutine's result to its
// outcome. With no source the reason is left empty: the signal then shows the
// scorer's default text, as it always has.
func localControlPlaneOutcome(haveSource bool, states ControlPlaneStates, err error) healthOutcome[ControlPlaneStates] {
	var o healthOutcome[ControlPlaneStates]
	switch {
	case !haveSource:
		o.setNoSource("")
	case err != nil:
		o.setUnavailable(SignalStatusSkipped, "prometheus timeout or unavailable")
	default:
		o.setOK(states)
	}
	return o
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

// resourceKind selects which capacity series utilizationFrom renders.
type resourceKind int

const (
	resourceKindCPU resourceKind = iota
	resourceKindMemory
)

// aggregateCounts is pure: no informers, no SARs, no clock, no network. It is
// shared by the local (informer) and remote (direct list) summary paths.
func aggregateCounts(nodes []*corev1.Node, pods []*corev1.Pod, serviceCount int) (NodeSummary, PodSummary, ServiceCount) {
	var ns NodeSummary
	ns.Total = len(nodes)
	for _, n := range nodes {
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				ns.Ready++
				break
			}
		}
	}

	var ps PodSummary
	ps.Total = len(pods)
	for _, p := range pods {
		switch p.Status.Phase {
		case corev1.PodRunning:
			ps.Running++
		case corev1.PodPending:
			ps.Pending++
		case corev1.PodFailed:
			ps.Failed++
		}
	}

	return ns, ps, ServiceCount{Total: serviceCount}
}

// capacityTotals holds allocatable capacity plus the requests and limits of
// non-terminal pods.
type capacityTotals struct {
	CPUAllocatable, CPURequests, CPULimits resource.Quantity
	MemAllocatable, MemRequests, MemLimits resource.Quantity
}

// aggregateCapacity is pure. Only Running and Pending pods contribute
// requests and limits; terminal pods no longer hold their reservations.
func aggregateCapacity(nodes []*corev1.Node, pods []*corev1.Pod) capacityTotals {
	var t capacityTotals
	for _, n := range nodes {
		if cpu, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
			t.CPUAllocatable.Add(cpu)
		}
		if mem, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			t.MemAllocatable.Add(mem)
		}
	}
	for _, p := range pods {
		if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodPending {
			continue
		}
		for _, c := range p.Spec.Containers {
			if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				t.CPURequests.Add(req)
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
				t.CPULimits.Add(lim)
			}
			if req, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				t.MemRequests.Add(req)
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				t.MemLimits.Add(lim)
			}
		}
	}
	return t
}

func formatCPU(q resource.Quantity) string {
	millis := q.MilliValue()
	if millis >= 1000 {
		return fmt.Sprintf("%.1f cores", float64(millis)/1000)
	}
	return fmt.Sprintf("%dm", millis)
}

func formatMem(q resource.Quantity) string {
	bytes := q.Value()
	gi := float64(bytes) / (1024 * 1024 * 1024)
	if gi >= 1 {
		return fmt.Sprintf("%.1f Gi", gi)
	}
	mi := float64(bytes) / (1024 * 1024)
	return fmt.Sprintf("%.0f Mi", mi)
}

// utilizationFrom renders capacity as a *Utilization. pct is the observed
// usage percentage, or nil when none is available (no Prometheus, a failed
// query, or a remote target). With pct == nil the result is the
// {Percentage: 0, Used: "N/A"} shape that web and mobile render as
// "unavailable", or nil when there is no allocatable capacity to report.
func utilizationFrom(t capacityTotals, kind resourceKind, pct *float64) *Utilization {
	allocatable, requests, limits := t.CPUAllocatable, t.CPURequests, t.CPULimits
	format := formatCPU
	hasCapacity := allocatable.MilliValue() > 0
	if kind == resourceKindMemory {
		allocatable, requests, limits = t.MemAllocatable, t.MemRequests, t.MemLimits
		format = formatMem
		hasCapacity = allocatable.Value() > 0
	}

	if pct == nil {
		if !hasCapacity {
			return nil
		}
		return &Utilization{
			Percentage: 0,
			Used:       "N/A",
			Total:      format(allocatable),
			Requests:   format(requests),
			Limits:     format(limits),
		}
	}

	var used resource.Quantity
	if kind == resourceKindMemory {
		used = *resource.NewQuantity(int64(*pct/100*float64(allocatable.Value())), resource.BinarySI)
	} else {
		used = *resource.NewMilliQuantity(int64(*pct/100*float64(allocatable.MilliValue())), resource.DecimalSI)
	}
	return &Utilization{
		Percentage: *pct,
		Used:       format(used),
		Total:      format(allocatable),
		Requests:   format(requests),
		Limits:     format(limits),
	}
}

// HandleDashboardSummary returns aggregated cluster health data from the informer cache.
// Prometheus metrics (CPU/Memory) are fetched asynchronously with a 1-second timeout.
//
// Remote clusters have no informer cache. They are served only when the caller
// opts in with ?coverage=1 (handleRemoteDashboardSummary); without it the
// legacy 400 below is returned unchanged.
func (h *Handler) HandleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	if !k8s.IsLocalClusterID(clusterID) {
		if r.URL.Query().Get("coverage") != "1" {
			// Unchanged 400 — mobile's DashboardLocalOnlyError matches this
			// exact status + "local cluster" substring. Release C plan, D5.
			writeError(w, http.StatusBadRequest, "dashboard summary is only available for the local cluster", "")
			return
		}
		h.handleRemoteDashboardSummary(w, r, user, clusterID)
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

	// Service count
	serviceCount := 0
	if h.canList(ctx, user, "services", "") {
		if services, err := h.Informers.Services().List(labels.Everything()); err == nil {
			serviceCount = len(services)
		}
	}
	summary.Nodes, summary.Pods, summary.Services = aggregateCounts(nodes, pods, serviceCount)

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

	capacity := aggregateCapacity(nodes, pods)

	// Run CPU/memory queries and the control-plane health check concurrently
	// within a shared 1-second Prometheus budget. We use sync.WaitGroup (not
	// errgroup) so a failure in one query does not cancel the others.
	// The block runs when either Utilization or ControlPlane is non-nil.
	var outcomes healthOutcomes
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
				cpuPct, cpuErr = h.Utilization.CPUPercent(promCtx, clusterID)
			}()
			go func() {
				defer wg.Done()
				memPct, memErr = h.Utilization.MemoryPercent(promCtx, clusterID)
			}()
		}

		// Gather health inputs — the control-plane goroutine (if any) is added
		// to wg inside gatherHealthInputs. canListNodes is pre-resolved above.
		outcomes = h.gatherHealthInputs(ctx, user, canListNodes, nodes, pods, &wg, promCtx, &cpResult, &cpErr)

		wg.Wait()

		if h.Utilization != nil {
			if cpuErr == nil {
				summary.CPU = utilizationFrom(capacity, resourceKindCPU, &cpuPct)
			}
			if memErr == nil {
				summary.Memory = utilizationFrom(capacity, resourceKindMemory, &memPct)
			}
		}
	} else {
		// No Prometheus-backed providers — still gather informer-based health inputs.
		// The WaitGroup and promCtx are unused for the control-plane goroutine because
		// h.ControlPlane is nil; pass a dummy context and WaitGroup.
		var wg sync.WaitGroup
		dummyCtx, dummyCancel := context.WithCancel(ctx)
		dummyCancel() // immediately cancelled — control-plane goroutine won't be started
		outcomes = h.gatherHealthInputs(ctx, user, canListNodes, nodes, pods, &wg, dummyCtx, &cpResult, &cpErr)
		wg.Wait()
	}

	// Record the control-plane outcome from the goroutine result (if it ran).
	// Without the nodes permission it stays unset: the assembly gates the
	// control plane on the nodes decision resolved above.
	if canListNodes {
		outcomes.controlPlane = localControlPlaneOutcome(h.ControlPlane != nil, cpResult, cpErr)
	}

	// Without an observed percentage, still report requests/limits/total from
	// the k8s API (or nil when there is no allocatable capacity).
	if summary.CPU == nil {
		summary.CPU = utilizationFrom(capacity, resourceKindCPU, nil)
	}
	if summary.Memory == nil {
		summary.Memory = utilizationFrom(capacity, resourceKindMemory, nil)
	}

	// Compute and attach cluster health (always present, never nil). The local
	// mapper records no failed read, so nothing is ever missing here.
	healthInputs, _ := assembleHealthInputs(outcomes)
	health := computeClusterHealth(healthInputs)
	summary.Health = &health

	writeJSON(w, http.StatusOK, api.Response{Data: summary})
}

// dashboardTrendWindows maps the frontend's time-range tab (?range=) to a
// (window, step) pair. Each pair yields ~30 points so every range reads as an
// equally dense sparkline. Unknown or empty values fall back to the 1h default.
var dashboardTrendWindows = map[string]struct{ window, step time.Duration }{
	"15m": {15 * time.Minute, 30 * time.Second},
	"1h":  {time.Hour, 2 * time.Minute},
	"6h":  {6 * time.Hour, 12 * time.Minute},
	"24h": {24 * time.Hour, 48 * time.Minute},
}

// dashboardTrendRange resolves a ?range= tab value to its window and step,
// defaulting to 1h when the value is empty or unrecognized.
func dashboardTrendRange(rangeKey string) (window, step time.Duration) {
	if r, ok := dashboardTrendWindows[rangeKey]; ok {
		return r.window, r.step
	}
	def := dashboardTrendWindows["1h"]
	return def.window, def.step
}

// HandleDashboardTrends returns short historical series for the dashboard metric
// cards (node/pod/service/alert counts) sourced from Prometheus range queries.
// Kept separate from HandleDashboardSummary so its multi-second range queries do
// not eat into that endpoint's 1-second Prometheus budget.
//
// Local cluster: when monitoring is unavailable (h.Trends == nil) or a query
// fails, the response carries empty series and HTTP 200 — the dashboard
// degrades to no sparklines rather than erroring.
//
// Remote cluster: the series come from the cluster's own Prometheus through
// its metrics binding. Without a binding the answer is 404
// metrics_not_configured; a failure to resolve the binding is a fixed 502.
// The local Prometheus never answers for a remote cluster.
func (h *Handler) HandleDashboardTrends(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	remote := !k8s.IsLocalClusterID(clusterID)

	if h.Trends == nil {
		if remote {
			writeMetricsNotConfigured(w)
			return
		}
		writeJSON(w, http.StatusOK, api.Response{Data: DashboardTrends{}})
		return
	}

	window, step := dashboardTrendRange(r.URL.Query().Get("range"))
	trends, err := h.Trends.DashboardTrends(r.Context(), clusterID, window, step)
	if err != nil && remote {
		if errors.Is(err, ErrNoMetricsBinding) {
			writeMetricsNotConfigured(w)
			return
		}
		if h.Logger != nil {
			h.Logger.Warn("remote dashboard trends failed", "cluster", clusterID, "error", err)
		}
		writeError(w, http.StatusBadGateway, msgRemoteMetricsQueryFailed, "")
		return
	}
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
