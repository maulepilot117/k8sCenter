package resources

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ── Fixture builders ──────────────────────────────────────────────────────────

func i32(v int32) *int32 { return &v }

func strp(s string) *string { return &s }

func nodeWith(name string, ready corev1.ConditionStatus, pressure ...corev1.NodeConditionType) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	n.Status.Conditions = append(n.Status.Conditions, corev1.NodeCondition{Type: corev1.NodeReady, Status: ready})
	for _, p := range pressure {
		n.Status.Conditions = append(n.Status.Conditions, corev1.NodeCondition{Type: p, Status: corev1.ConditionTrue})
	}
	return n
}

func dep(name string, replicas *int32, available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: replicas},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: available},
	}
}

func depProgressing(name string, replicas, available int32, reason string) *appsv1.Deployment {
	d := dep(name, i32(replicas), available)
	d.Status.Conditions = []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: reason,
	}}
	return d
}

func sts(name string, replicas, ready int32, current, update string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       appsv1.StatefulSetSpec{Replicas: i32(replicas)},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: ready, CurrentRevision: current, UpdateRevision: update,
		},
	}
}

func ds(name string, desired, ready int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: desired, NumberReady: ready},
	}
}

func podWaiting(name string, phase corev1.PodPhase, reason string, init bool) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if reason != "" {
		cs := []corev1.ContainerStatus{{
			Name:  "c",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}},
		}}
		if init {
			p.Status.InitContainerStatuses = cs
		} else {
			p.Status.ContainerStatuses = cs
		}
	}
	return p
}

func pdbWith(name string, current, desired int32) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status:     policyv1.PodDisruptionBudgetStatus{CurrentHealthy: current, DesiredHealthy: desired},
	}
}

func storageClass(name string, mode storagev1.VolumeBindingMode, isDefault bool) *storagev1.StorageClass {
	sc := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}, VolumeBindingMode: &mode}
	if isDefault {
		sc.Annotations = map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}
	}
	return sc
}

func pvc(name string, class *string, phase corev1.PersistentVolumeClaimPhase, selectedNode bool) *corev1.PersistentVolumeClaim {
	p := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: class},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: phase},
	}
	if selectedNode {
		p.Annotations = map[string]string{"volume.kubernetes.io/selected-node": "node-a"}
	}
	return p
}

// fixtureNodes: one ready, one ready with memory pressure, one not ready with
// PID pressure. A pressure condition reported False is not counted.
func fixtureNodes() []runtime.Object {
	n1 := nodeWith("node-a", corev1.ConditionTrue)
	n1.Status.Conditions = append(n1.Status.Conditions, corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse})
	return []runtime.Object{
		n1,
		nodeWith("node-b", corev1.ConditionTrue, corev1.NodeMemoryPressure),
		nodeWith("node-c", corev1.ConditionFalse, corev1.NodePIDPressure),
	}
}

// fixtureNonNodes is everything else in the representative fixture.
//
// Hand-derived shaping:
//
//	deployments  desired 2+4+2+1+1 = 10, available 2+1+2+0+1 = 6, progressing 1
//	statefulsets desired 1, available 1, progressing 1
//	daemonsets   desired 3, available 2
//	pods         eligible 5 (running, crash, init, errpull, creating), crashloop 3
//	pdbs         1 violation
//	pvcs         4 pending (wffc+selected-node, fast, static "", unknown class)
func fixtureNonNodes() []runtime.Object {
	deleting := podWaiting("p-deleting", corev1.PodRunning, waitingReasonCrashLoopBackOff, false)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{"example.com/hold"}

	return []runtime.Object{
		// Deployments
		dep("dep-ok", i32(2), 2),
		dep("dep-degraded", i32(4), 1),
		dep("dep-surge", i32(2), 3),                                  // available clamped to desired
		dep("dep-nilreplicas", nil, 0),                               // desired defaults to 1
		depProgressing("dep-rolling", 3, 0, "ReplicaSetUpdated"),     // excluded, progressing
		depProgressing("dep-rolled", 1, 1, "NewReplicaSetAvailable"), // counted
		func() *appsv1.Deployment { d := dep("dep-paused", i32(2), 0); d.Spec.Paused = true; return d }(), // excluded
		dep("dep-zero", i32(0), 0), // excluded
		// StatefulSets
		sts("sts-ok", 1, 1, "r1", "r1"),
		sts("sts-rolling", 2, 0, "r1", "r2"), // excluded, progressing
		sts("sts-zero", 0, 0, "", ""),        // excluded
		// DaemonSets
		ds("ds-partial", 3, 2),
		ds("ds-empty", 0, 0), // excluded
		// Pods
		podWaiting("p-running", corev1.PodRunning, "", false),
		podWaiting("p-crash", corev1.PodRunning, waitingReasonCrashLoopBackOff, false),
		podWaiting("p-init", corev1.PodPending, waitingReasonImagePullBackOff, true),
		podWaiting("p-errpull", corev1.PodPending, waitingReasonErrImagePull, false),
		podWaiting("p-creating", corev1.PodRunning, "ContainerCreating", false),
		podWaiting("p-succeeded", corev1.PodSucceeded, waitingReasonCrashLoopBackOff, false),
		podWaiting("p-failed", corev1.PodFailed, "", false),
		deleting,
		// PDBs
		pdbWith("pdb-violated", 0, 1),
		pdbWith("pdb-ok", 2, 2),
		// Storage
		storageClass("wffc", storagev1.VolumeBindingWaitForFirstConsumer, true),
		storageClass("fast", storagev1.VolumeBindingImmediate, false),
		pvc("pvc-default-wffc", nil, corev1.ClaimPending, false),          // excluded
		pvc("pvc-wffc-selected", strp("wffc"), corev1.ClaimPending, true), // counted
		pvc("pvc-fast", strp("fast"), corev1.ClaimPending, false),         // counted
		pvc("pvc-bound", strp("fast"), corev1.ClaimBound, false),          // excluded
		pvc("pvc-static", strp(""), corev1.ClaimPending, false),           // counted
		pvc("pvc-unknown", strp("gone"), corev1.ClaimPending, false),      // counted
	}
}

// fixtureInputs is the hand-derived HealthInputs for the full fixture with
// every provider present and every check allowed.
func fixtureInputs() HealthInputs {
	return HealthInputs{
		NodesAvailable: true, TotalNodes: 3, ReadyNodes: 2, PressureNodes: 2,
		WorkloadsAvailable: true, WorkloadsDesired: 14, WorkloadsActuallyAvailable: 9, ProgressingCount: 2,
		PodsAvailable: true, CrashloopPods: 3, EligiblePods: 5,
		AlertsAvailable: true, AlertsActive: 5, AlertsCritical: 2,
		CertsAvailable: true, CertCritical: 1,
		StorageAvailable: true, PendingPVCs: 4,
		ControlPlaneAvailable: true,
		ControlPlane: ControlPlaneStates{
			SchedulerState: ComponentUp, ControllerManagerState: ComponentUnscraped, EtcdState: ComponentDown,
		},
		PDBAvailable: true, PDBViolations: 1,
	}
}

// fixtureHandler builds a handler over objs with every optional provider set
// to the fixture's values.
func fixtureHandler(t *testing.T, objs []runtime.Object) *Handler {
	t.Helper()
	h, _ := testHandler(t, objs...)
	h.Alerts = &fakeAlertCounter{active: 5, critical: 2}
	h.CertExpiry = &fakeCertExpiry{warn: 3, crit: 1}
	h.ControlPlane = &fakeControlPlane{states: ControlPlaneStates{
		SchedulerState: ComponentUp, ControllerManagerState: ComponentUnscraped, EtcdState: ComponentDown,
	}}
	return h
}

// localHealthJSON serves the local dashboard summary and returns the raw
// "health" member of the response body.
func localHealthJSON(t *testing.T, h *Handler) json.RawMessage {
	t.Helper()
	rr := httptest.NewRecorder()
	h.HandleDashboardSummary(rr, requestWithUser("GET", "/api/v1/cluster/dashboard-summary", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Data struct {
			Health json.RawMessage `json:"health"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data.Health
}

// assertLocalHealthFrom asserts that the local response's health member is
// byte-identical to the score of the hand-derived inputs want.
func assertLocalHealthFrom(t *testing.T, h *Handler, want HealthInputs) ClusterHealth {
	t.Helper()
	got := localHealthJSON(t, h)
	wantHealth := computeClusterHealth(want)
	wantJSON, err := json.Marshal(wantHealth)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != string(wantJSON) {
		t.Fatalf("local health differs from the hand-derived inputs\n got: %s\nwant: %s", got, wantJSON)
	}
	return wantHealth
}

func syncedExcept(unsynced ...string) func(string) bool {
	set := make(map[string]bool, len(unsynced))
	for _, r := range unsynced {
		set[r] = true
	}
	return func(r string) bool { return !set[r] }
}

// ── Characterization of the local mapper ──────────────────────────────────────

// TestLocalHealthCharacterization_Fixture pins the full representative
// fixture: the response's health is exactly the score of the hand-derived
// inputs, and that score is the hand-computed 29 / degraded with these reasons.
func TestLocalHealthCharacterization_Fixture(t *testing.T) {
	h := fixtureHandler(t, append(fixtureNodes(), fixtureNonNodes()...))
	got := assertLocalHealthFrom(t, h, fixtureInputs())

	// nodes 2/3*100-4 = 62.67, workloads 9/14 = 64.29, pods 0, alerts 71;
	// 0.35*62.67 + 0.35*64.29 + 0.1*71 = 51.53, minus cert 10, pvc 3, etcd 10.
	if got.Status != HealthStatusDegraded || got.Score == nil || *got.Score != 29 {
		t.Fatalf("want degraded/29, got %s/%v", got.Status, got.Score)
	}
	wantReasons := []string{
		"1 of 3 nodes not ready",
		"2 node(s) with pressure conditions",
		"5 of 14 workload replicas unavailable",
		"3 pod(s) in crash loop or image pull failure",
		"1 PDB(s) with insufficient healthy pods",
		"4 PVC(s) pending",
		"1 certificate(s) expiring soon (critical)",
		"etcd scraped and down",
		"2 critical alert(s) firing",
		"2 workload(s) rolling out",
	}
	if len(got.Reasons) != len(wantReasons) {
		t.Fatalf("reasons: got %v, want %v", got.Reasons, wantReasons)
	}
	for i := range wantReasons {
		if got.Reasons[i] != wantReasons[i] {
			t.Errorf("reason %d: got %q, want %q", i, got.Reasons[i], wantReasons[i])
		}
	}
}

// TestLocalHealthCharacterization_Variants pins every local degrade, skip and
// denial branch: each produces today's reason text and signal status, and a
// score is still drawn wherever the nodes signal resolved.
func TestLocalHealthCharacterization_Variants(t *testing.T) {
	permDenied := "insufficient permissions"
	syncing := "cache syncing"

	tests := []struct {
		name      string
		noNodes   bool
		setup     func(h *Handler)
		want      func(in *HealthInputs)
		wantScore bool
	}{
		{
			name:  "nodes denied gates workloads and control plane",
			setup: func(h *Handler) { h.AccessChecker = NewDenyResourcesAccessChecker("nodes") },
			want: func(in *HealthInputs) {
				in.NodesAvailable, in.NodesUnavailableReason = false, permDenied
				in.TotalNodes, in.ReadyNodes, in.PressureNodes = 0, 0, 0
				in.WorkloadsAvailable, in.WorkloadsUnavailableReason = false, permDenied
				in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount = 0, 0, 0
				in.ControlPlaneAvailable, in.ControlPlaneUnavailableReason = false, permDenied
				in.ControlPlane = ControlPlaneStates{}
			},
		},
		{
			name:    "zero nodes visible",
			noNodes: true,
			want: func(in *HealthInputs) {
				in.NodesAvailable, in.NodesUnavailableReason = false, "no nodes visible"
				in.TotalNodes, in.ReadyNodes, in.PressureNodes = 0, 0, 0
			},
		},
		{
			name:  "nodes cache syncing",
			setup: func(h *Handler) { h.isSynced = syncedExcept("nodes") },
			want: func(in *HealthInputs) {
				in.NodesAvailable, in.NodesUnavailableReason = false, syncing
				in.TotalNodes, in.ReadyNodes, in.PressureNodes = 0, 0, 0
			},
		},
		{
			name:  "statefulsets denied: workloads over deployments and daemonsets",
			setup: func(h *Handler) { h.AccessChecker = NewDenyResourcesAccessChecker("statefulsets") },
			want: func(in *HealthInputs) {
				in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount = 13, 8, 1
			},
			wantScore: true,
		},
		{
			name: "all workload kinds denied",
			setup: func(h *Handler) {
				h.AccessChecker = NewDenyResourcesAccessChecker("deployments", "statefulsets", "daemonsets")
			},
			want: func(in *HealthInputs) {
				in.WorkloadsAvailable, in.WorkloadsUnavailableReason = false, permDenied
				in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount = 0, 0, 0
			},
			wantScore: true,
		},
		{
			name: "all workload kinds denied while syncing reads denied",
			setup: func(h *Handler) {
				h.AccessChecker = NewDenyResourcesAccessChecker("deployments", "statefulsets", "daemonsets")
				h.isSynced = syncedExcept("statefulsets")
			},
			want: func(in *HealthInputs) {
				in.WorkloadsAvailable, in.WorkloadsUnavailableReason = false, permDenied
				in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount = 0, 0, 0
			},
			wantScore: true,
		},
		{
			name:  "statefulsets cache syncing",
			setup: func(h *Handler) { h.isSynced = syncedExcept("statefulsets") },
			want: func(in *HealthInputs) {
				in.WorkloadsAvailable, in.WorkloadsUnavailableReason = false, syncing
				in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount = 0, 0, 0
			},
			wantScore: true,
		},
		{
			name: "a denied workload kind still syncing reads syncing",
			setup: func(h *Handler) {
				h.AccessChecker = NewDenyResourcesAccessChecker("statefulsets")
				h.isSynced = syncedExcept("statefulsets")
			},
			want: func(in *HealthInputs) {
				in.WorkloadsAvailable, in.WorkloadsUnavailableReason = false, syncing
				in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount = 0, 0, 0
			},
			wantScore: true,
		},
		{
			name:  "pods denied",
			setup: func(h *Handler) { h.AccessChecker = NewDenyResourcesAccessChecker("pods") },
			want: func(in *HealthInputs) {
				in.PodsAvailable, in.PodsUnavailableReason = false, permDenied
				in.CrashloopPods, in.EligiblePods = 0, 0
			},
			wantScore: true,
		},
		{
			name:  "pods cache syncing",
			setup: func(h *Handler) { h.isSynced = syncedExcept("pods") },
			want: func(in *HealthInputs) {
				in.PodsAvailable, in.PodsUnavailableReason = false, syncing
				in.CrashloopPods, in.EligiblePods = 0, 0
			},
			wantScore: true,
		},
		{
			name:  "pdb denied",
			setup: func(h *Handler) { h.AccessChecker = NewDenyResourcesAccessChecker("poddisruptionbudgets") },
			want: func(in *HealthInputs) {
				in.PDBAvailable, in.PDBUnavailableReason, in.PDBViolations = false, permDenied, 0
			},
			wantScore: true,
		},
		{
			name:  "pdb cache syncing",
			setup: func(h *Handler) { h.isSynced = syncedExcept("poddisruptionbudgets") },
			want: func(in *HealthInputs) {
				in.PDBAvailable, in.PDBUnavailableReason, in.PDBViolations = false, syncing, 0
			},
			wantScore: true,
		},
		{
			name:  "pvcs denied",
			setup: func(h *Handler) { h.AccessChecker = NewDenyResourcesAccessChecker("persistentvolumeclaims") },
			want: func(in *HealthInputs) {
				in.StorageAvailable, in.StorageUnavailableReason, in.PendingPVCs = false, permDenied, 0
			},
			wantScore: true,
		},
		{
			name:  "storageclasses cache syncing",
			setup: func(h *Handler) { h.isSynced = syncedExcept("storageclasses") },
			want: func(in *HealthInputs) {
				in.StorageAvailable, in.StorageUnavailableReason, in.PendingPVCs = false, syncing, 0
			},
			wantScore: true,
		},
		{
			name:      "storageclasses denied is not checked locally",
			setup:     func(h *Handler) { h.AccessChecker = NewDenyResourcesAccessChecker("storageclasses") },
			want:      func(in *HealthInputs) {},
			wantScore: true,
		},
		{
			name:  "no alert source",
			setup: func(h *Handler) { h.Alerts = nil },
			want: func(in *HealthInputs) {
				in.AlertsAvailable, in.AlertsSkipped, in.AlertsUnavailableReason = false, true, "alerting not configured"
				in.AlertsActive, in.AlertsCritical = 0, 0
			},
			wantScore: true,
		},
		{
			name:  "alerts denied",
			setup: func(h *Handler) { h.AccessChecker = NewDenyResourcesAccessChecker("alertmanagers") },
			want: func(in *HealthInputs) {
				in.AlertsAvailable, in.AlertsSkipped, in.AlertsUnavailableReason = false, true, permDenied
				in.AlertsActive, in.AlertsCritical = 0, 0
			},
			wantScore: true,
		},
		{
			name:  "alert query error",
			setup: func(h *Handler) { h.Alerts = &fakeAlertCounter{err: errors.New("store down")} },
			want: func(in *HealthInputs) {
				in.AlertsAvailable, in.AlertsSkipped, in.AlertsUnavailableReason = false, false, "alerting unavailable"
				in.AlertsActive, in.AlertsCritical = 0, 0
			},
			wantScore: true,
		},
		{
			name:  "no certificate source",
			setup: func(h *Handler) { h.CertExpiry = nil },
			want: func(in *HealthInputs) {
				in.CertsAvailable, in.CertsUnavailableReason, in.CertCritical = false, "cert-manager not configured", 0
			},
			wantScore: true,
		},
		{
			name:  "cert-manager not installed",
			setup: func(h *Handler) { h.CertExpiry = &fakeCertExpiry{err: ErrCertManagerNotInstalled} },
			want: func(in *HealthInputs) {
				in.CertsAvailable, in.CertsUnavailableReason, in.CertCritical = false, "cert-manager not installed", 0
			},
			wantScore: true,
		},
		{
			name:  "cold certificate cache",
			setup: func(h *Handler) { h.CertExpiry = &fakeCertExpiry{err: ErrCertCacheNotWarm} },
			want: func(in *HealthInputs) {
				in.CertsAvailable, in.CertsUnavailableReason, in.CertCritical = false, "cert cache warming", 0
			},
			wantScore: true,
		},
		{
			name:  "certificate read error",
			setup: func(h *Handler) { h.CertExpiry = &fakeCertExpiry{err: errors.New("dial tcp 10.0.0.1:443")} },
			want: func(in *HealthInputs) {
				in.CertsAvailable, in.CertsUnavailableReason, in.CertCritical = false, "certificate data unavailable", 0
			},
			wantScore: true,
		},
		{
			name:  "no control-plane source",
			setup: func(h *Handler) { h.ControlPlane = nil },
			want: func(in *HealthInputs) {
				in.ControlPlaneAvailable, in.ControlPlaneUnavailableReason = false, ""
				in.ControlPlane = ControlPlaneStates{}
			},
			wantScore: true,
		},
		{
			name:  "control-plane timeout",
			setup: func(h *Handler) { h.ControlPlane = &fakeControlPlane{err: errors.New("context deadline exceeded")} },
			want: func(in *HealthInputs) {
				in.ControlPlaneAvailable, in.ControlPlaneUnavailableReason = false, "prometheus timeout or unavailable"
				in.ControlPlane = ControlPlaneStates{}
			},
			wantScore: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := fixtureNonNodes()
			if !tt.noNodes {
				objs = append(fixtureNodes(), objs...)
			}
			h := fixtureHandler(t, objs)
			if tt.setup != nil {
				tt.setup(h)
			}
			want := fixtureInputs()
			tt.want(&want)
			got := assertLocalHealthFrom(t, h, want)
			if tt.wantScore && got.Score == nil {
				t.Errorf("want a score drawn, got none (status %s)", got.Status)
			}
			if !tt.wantScore && (got.Status != HealthStatusUnknown || got.Score != nil) {
				t.Errorf("want unknown without a score, got %s/%v", got.Status, got.Score)
			}
		})
	}
}

// ── Pure shaping ──────────────────────────────────────────────────────────────

// fixtureOutcomes returns every input of the representative fixture as a
// completed read, with the fixture's provider values.
func fixtureOutcomes() healthOutcomes {
	var o healthOutcomes
	var nodes []*corev1.Node
	for _, obj := range fixtureNodes() {
		nodes = append(nodes, obj.(*corev1.Node))
	}
	var deps []*appsv1.Deployment
	var stss []*appsv1.StatefulSet
	var dss []*appsv1.DaemonSet
	var pods []*corev1.Pod
	var pdbs []*policyv1.PodDisruptionBudget
	var pvcs []*corev1.PersistentVolumeClaim
	var classes []*storagev1.StorageClass
	for _, obj := range fixtureNonNodes() {
		switch v := obj.(type) {
		case *appsv1.Deployment:
			deps = append(deps, v)
		case *appsv1.StatefulSet:
			stss = append(stss, v)
		case *appsv1.DaemonSet:
			dss = append(dss, v)
		case *corev1.Pod:
			pods = append(pods, v)
		case *policyv1.PodDisruptionBudget:
			pdbs = append(pdbs, v)
		case *corev1.PersistentVolumeClaim:
			pvcs = append(pvcs, v)
		case *storagev1.StorageClass:
			classes = append(classes, v)
		}
	}
	o.nodes.setOK(nodes)
	o.deployments.setOK(deps)
	o.statefulSets.setOK(stss)
	o.daemonSets.setOK(dss)
	o.pods.setOK(pods)
	o.pdbs.setOK(pdbs)
	o.pvcs.setOK(pvcs)
	o.storageClasses.setOK(classes)
	o.alerts.setOK(alertCounts{active: 5, critical: 2})
	o.certificates.setOK(1)
	o.controlPlane.setOK(ControlPlaneStates{
		SchedulerState: ComponentUp, ControllerManagerState: ComponentUnscraped, EtcdState: ComponentDown,
	})
	return o
}

func TestShapeNodes(t *testing.T) {
	o := fixtureOutcomes()
	if got, want := shapeNodes(o.nodes.value), (nodeShape{total: 3, ready: 2, pressure: 2}); got != want {
		t.Errorf("shapeNodes = %+v, want %+v", got, want)
	}
	if got := shapeNodes(nil); got != (nodeShape{}) {
		t.Errorf("shapeNodes(nil) = %+v, want zero", got)
	}
}

func TestShapePods(t *testing.T) {
	o := fixtureOutcomes()
	// Eligible: running, crash, init, errpull, creating. Crash-looping: crash,
	// init (init container), errpull. Succeeded, failed and deleting excluded.
	if got, want := shapePods(o.pods.value), (podShape{eligible: 5, crashloop: 3}); got != want {
		t.Errorf("shapePods = %+v, want %+v", got, want)
	}
}

func TestShapeWorkloads(t *testing.T) {
	o := fixtureOutcomes()
	tests := []struct {
		name string
		got  workloadShape
		want workloadShape
	}{
		// ok 2/2, degraded 4/1, surge 2/2 (clamped), nil replicas 1/0, rolled 1/1;
		// rolling excluded as progressing; paused and zero excluded.
		{"deployments", shapeDeployments(o.deployments.value), workloadShape{desired: 10, available: 6, progressing: 1}},
		// ok 1/1; rolling excluded as progressing; zero excluded.
		{"statefulsets", shapeStatefulSets(o.statefulSets.value), workloadShape{desired: 1, available: 1, progressing: 1}},
		// partial 3/2; scheduled-nowhere excluded.
		{"daemonsets", shapeDaemonSets(o.daemonSets.value), workloadShape{desired: 3, available: 2}},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, tt.got, tt.want)
		}
	}
}

func TestCountPendingPVCs(t *testing.T) {
	wffcDefault := storageClass("wffc", storagev1.VolumeBindingWaitForFirstConsumer, true)
	fast := storageClass("fast", storagev1.VolumeBindingImmediate, false)
	classes := []*storagev1.StorageClass{wffcDefault, fast}

	tests := []struct {
		name string
		pvc  *corev1.PersistentVolumeClaim
		want int
	}{
		{"pending on a WFFC class with no consumer is not counted", pvc("a", strp("wffc"), corev1.ClaimPending, false), 0},
		{"no class name resolves to the WFFC default and is not counted", pvc("b", nil, corev1.ClaimPending, false), 0},
		{"WFFC with a selected node is counted", pvc("c", strp("wffc"), corev1.ClaimPending, true), 1},
		{"pending on an Immediate class is counted", pvc("d", strp("fast"), corev1.ClaimPending, false), 1},
		{"explicit empty class is static binding and counted", pvc("e", strp(""), corev1.ClaimPending, false), 1},
		{"bound is not counted", pvc("f", strp("fast"), corev1.ClaimBound, false), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countPendingPVCs([]*corev1.PersistentVolumeClaim{tt.pvc}, classes); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}

	// Without a default class, an unset class name matches nothing and counts.
	if got := countPendingPVCs([]*corev1.PersistentVolumeClaim{pvc("g", nil, corev1.ClaimPending, false)}, []*storagev1.StorageClass{fast}); got != 1 {
		t.Errorf("unset class with no default: got %d, want 1", got)
	}
}

// ── Assembly ──────────────────────────────────────────────────────────────────

func assembleOK(t *testing.T, o healthOutcomes) HealthInputs {
	t.Helper()
	in, missing := assembleHealthInputs(o)
	if len(missing) != 0 {
		t.Fatalf("unexpected missing inputs: %+v", missing)
	}
	return in
}

func TestAssembleHealthInputs_AllComplete(t *testing.T) {
	if got, want := assembleOK(t, fixtureOutcomes()), fixtureInputs(); got != want {
		t.Errorf("assembled inputs differ\n got: %+v\nwant: %+v", got, want)
	}
}

func TestAssembleHealthInputs_NodesDeniedGatesWorkloadsAndControlPlane(t *testing.T) {
	o := fixtureOutcomes()
	o.nodes.setDenied()
	// Gated inputs are ignored whatever their own outcome, even a failure.
	o.controlPlane.setFailed("timed out")

	in := assembleOK(t, o)
	if in.NodesAvailable || in.NodesUnavailableReason != "insufficient permissions" {
		t.Errorf("nodes: available=%v reason=%q", in.NodesAvailable, in.NodesUnavailableReason)
	}
	if in.WorkloadsAvailable || in.WorkloadsUnavailableReason != "insufficient permissions" || in.WorkloadsDesired != 0 {
		t.Errorf("workloads must not be computed: %+v", in)
	}
	if in.ControlPlaneAvailable || in.ControlPlaneUnavailableReason != "insufficient permissions" {
		t.Errorf("control plane: available=%v reason=%q", in.ControlPlaneAvailable, in.ControlPlaneUnavailableReason)
	}
	if h := computeClusterHealth(in); h.Status != HealthStatusUnknown || h.Score != nil {
		t.Errorf("want unknown without a score, got %s/%v", h.Status, h.Score)
	}
}

func TestAssembleHealthInputs_WorkloadKindsDenied(t *testing.T) {
	t.Run("statefulsets denied: deployments and daemonsets only", func(t *testing.T) {
		o := fixtureOutcomes()
		o.statefulSets.setDenied()
		in := assembleOK(t, o)
		// deployments 10/6/1 + daemonsets 3/2/0.
		if !in.WorkloadsAvailable || in.WorkloadsDesired != 13 || in.WorkloadsActuallyAvailable != 8 || in.ProgressingCount != 1 {
			t.Errorf("got available=%v desired=%d avail=%d progressing=%d",
				in.WorkloadsAvailable, in.WorkloadsDesired, in.WorkloadsActuallyAvailable, in.ProgressingCount)
		}
	})
	t.Run("all kinds denied: unknown for insufficient permissions", func(t *testing.T) {
		o := fixtureOutcomes()
		o.deployments.setDenied()
		o.statefulSets.setDenied()
		o.daemonSets.setDenied()
		in := assembleOK(t, o)
		if in.WorkloadsAvailable || in.WorkloadsUnavailableReason != "insufficient permissions" {
			t.Errorf("got available=%v reason=%q", in.WorkloadsAvailable, in.WorkloadsUnavailableReason)
		}
	})
}

func TestAssembleHealthInputs_ZeroNodes(t *testing.T) {
	o := fixtureOutcomes()
	o.nodes.setOK(nil)
	in := assembleOK(t, o)
	if in.NodesAvailable || in.NodesUnavailableReason != "no nodes visible" {
		t.Errorf("nodes: available=%v reason=%q", in.NodesAvailable, in.NodesUnavailableReason)
	}
	// Zero visible nodes is not a denial: workloads are still computed.
	if !in.WorkloadsAvailable || in.WorkloadsDesired != 14 {
		t.Errorf("workloads: available=%v desired=%d", in.WorkloadsAvailable, in.WorkloadsDesired)
	}
	if h := computeClusterHealth(in); h.Status != HealthStatusUnknown || h.Score != nil {
		t.Errorf("want unknown without a score, got %s/%v", h.Status, h.Score)
	}
}

func TestAssembleHealthInputs_UnavailablePassesReasonAndStatus(t *testing.T) {
	t.Run("unknown status", func(t *testing.T) {
		o := fixtureOutcomes()
		o.alerts.setUnavailable(SignalStatusUnknown, "alerting unavailable")
		h := computeClusterHealth(assembleOK(t, o))
		if sig := signalByName(t, h, signalAlerts); sig.Status != SignalStatusUnknown || sig.Reason != "alerting unavailable" {
			t.Errorf("alerts signal = %+v", sig)
		}
		if h.Score == nil {
			t.Error("an unavailable outcome must still score")
		}
	})
	t.Run("skipped status", func(t *testing.T) {
		o := fixtureOutcomes()
		o.alerts.setUnavailable(SignalStatusSkipped, "alerting paused")
		o.certificates.setUnavailable(SignalStatusSkipped, "cert cache warming")
		h := computeClusterHealth(assembleOK(t, o))
		if sig := signalByName(t, h, signalAlerts); sig.Status != SignalStatusSkipped || sig.Reason != "alerting paused" {
			t.Errorf("alerts signal = %+v", sig)
		}
		if sig := signalByName(t, h, signalCerts); sig.Status != SignalStatusSkipped || sig.Reason != "cert cache warming" {
			t.Errorf("certificates signal = %+v", sig)
		}
		if h.Score == nil {
			t.Error("an unavailable outcome must still score")
		}
	})
}

func TestAssembleHealthInputs_DeniedAndNoSourceSkip(t *testing.T) {
	o := fixtureOutcomes()
	o.pdbs.setDenied()
	o.storageClasses.setDenied()
	o.alerts.setNoSource("alerting not configured")
	o.certificates.setDenied()
	o.controlPlane.setNoSource("metrics not configured")

	in := assembleOK(t, o)
	if in.PDBAvailable || in.PDBViolations != 0 {
		t.Errorf("pdb: available=%v violations=%d", in.PDBAvailable, in.PDBViolations)
	}
	h := computeClusterHealth(in)
	for _, want := range []HealthSignal{
		{Name: signalStorage, Status: SignalStatusSkipped, Reason: "insufficient permissions"},
		{Name: signalAlerts, Status: SignalStatusSkipped, Reason: "alerting not configured"},
		{Name: signalCerts, Status: SignalStatusSkipped, Reason: "insufficient permissions"},
		{Name: signalControlPlane, Status: SignalStatusSkipped, Reason: "metrics not configured"},
	} {
		got := signalByName(t, h, want.Name)
		if got.Status != want.Status || got.Reason != want.Reason {
			t.Errorf("%s signal = %+v, want status %s reason %q", want.Name, got, want.Status, want.Reason)
		}
	}
	if h.Score == nil {
		t.Error("denied and no-source inputs must not withhold the score")
	}
}

func TestAssembleHealthInputs_FailedWithholdsScore(t *testing.T) {
	o := fixtureOutcomes()
	o.controlPlane.setFailed("timed out")
	o.pods.setFailed("truncated")
	o.storageClasses.setFailed("refused")

	in, missing := assembleHealthInputs(o)
	if in != (HealthInputs{}) {
		t.Errorf("no inputs may be returned for scoring, got %+v", in)
	}
	want := []missingHealthInput{
		{input: "pods", cause: "truncated"},
		{input: "storageclasses", cause: "refused"},
		{input: "controlPlane", cause: "timed out"},
	}
	if len(missing) != len(want) {
		t.Fatalf("missing = %+v, want %+v", missing, want)
	}
	for i := range want {
		if missing[i] != want[i] {
			t.Errorf("missing[%d] = %+v, want %+v", i, missing[i], want[i])
		}
	}
}

func TestAssembleHealthInputs_UnsetInputIsMissing(t *testing.T) {
	o := fixtureOutcomes()
	o.daemonSets = healthOutcome[[]*appsv1.DaemonSet]{}

	_, missing := assembleHealthInputs(o)
	if len(missing) != 1 || missing[0] != (missingHealthInput{input: "daemonsets", cause: healthCauseNotRead}) {
		t.Errorf("missing = %+v, want daemonsets not read", missing)
	}
}
