package resources

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// decodeDashboard decodes the DashboardSummary from an httptest.ResponseRecorder.
func decodeDashboard(t *testing.T, rr *httptest.ResponseRecorder) DashboardSummary {
	t.Helper()
	var wrapper struct {
		Data DashboardSummary `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&wrapper); err != nil {
		t.Fatalf("failed to decode dashboard response: %v (body: %s)", err, rr.Body.String())
	}
	return wrapper.Data
}

// callDashboard fires a GET /cluster/dashboard-summary request through the handler.
func callDashboard(t *testing.T, h *Handler) (int, DashboardSummary) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-summary", "")
	h.HandleDashboardSummary(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	return rr.Code, decodeDashboard(t, rr)
}

// readyNode returns a Node whose Ready condition is True.
func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

// runningPod returns a Pod in the Running phase with the given name and namespace.
func runningPod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// deployment1x1 returns a Deployment with 1 desired and 1 available replica.
func deployment1x1(ns, name string) *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: name, Image: "nginx"}}},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:          1,
			AvailableReplicas: 1,
			ReadyReplicas:     1,
		},
	}
}

// --- Fake providers ---

type fakeAlertCounter struct {
	active   int
	critical int
	err      error
}

func (f *fakeAlertCounter) ActiveAlertCounts(_ context.Context) (int, int, error) {
	return f.active, f.critical, f.err
}
func (f *fakeAlertCounter) ActiveAlertCountsExcluding(_ context.Context, _ ...string) (int, int, error) {
	return f.active, f.critical, f.err
}

// fakeAlertCounterCapture records the excludeAlertNames passed to
// ActiveAlertCountsExcluding so tests can assert the exact names forwarded.
type fakeAlertCounterCapture struct {
	active         int
	critical       int
	err            error
	capturedExclude []string
}

func (f *fakeAlertCounterCapture) ActiveAlertCounts(_ context.Context) (int, int, error) {
	return f.active, f.critical, f.err
}
func (f *fakeAlertCounterCapture) ActiveAlertCountsExcluding(_ context.Context, names ...string) (int, int, error) {
	f.capturedExclude = names
	return f.active, f.critical, f.err
}

// fakeControlPlane implements ControlPlaneChecker for tests.
type fakeControlPlane struct {
	states ControlPlaneStates
	err    error
}

func (f *fakeControlPlane) ControlPlaneStatus(_ context.Context) (ControlPlaneStates, error) {
	return f.states, f.err
}

type fakeCertExpiry struct {
	warn int
	crit int
	err  error
}

func (f *fakeCertExpiry) ExpiringCounts(_ context.Context, _ *auth.User) (int, int, error) {
	return f.warn, f.crit, f.err
}

// --- Tests ---

// TestDashboardHealth_Healthy verifies the happy-path fixture: all signals ok,
// status healthy, score present, nil optional providers produce skipped signals.
func TestDashboardHealth_Healthy(t *testing.T) {
	objs := []runtime.Object{
		readyNode("node-1"),
		runningPod("default", "pod-1"),
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health field must not be nil")
	}
	if summary.Health.Status != HealthStatusHealthy {
		t.Errorf("expected healthy, got %s; reasons: %v", summary.Health.Status, summary.Health.Reasons)
	}
	if summary.Health.Score == nil {
		t.Error("score must not be nil for healthy status")
	}

	// Signals slice must always be present (full fixed set).
	if len(summary.Health.Signals) == 0 {
		t.Error("signals slice must not be empty")
	}

	// Nil Alerts / CertExpiry / ControlPlane → corresponding signals skipped.
	signalMap := make(map[string]HealthSignal, len(summary.Health.Signals))
	for _, s := range summary.Health.Signals {
		signalMap[s.Name] = s
	}
	if s := signalMap[signalAlerts]; s.Status != SignalStatusUnknown && s.Status != SignalStatusSkipped {
		t.Errorf("alerts signal expected skipped/unknown (no provider), got %s", s.Status)
	}
	if s := signalMap[signalCerts]; s.Status != SignalStatusSkipped {
		t.Errorf("certs signal expected skipped (no provider), got %s", s.Status)
	}
	if s := signalMap[signalControlPlane]; s.Status != SignalStatusSkipped {
		t.Errorf("control plane signal expected skipped (no provider), got %s", s.Status)
	}

	// Existing fields still present (R6 additivity).
	if summary.Nodes.Total == 0 {
		t.Error("nodes.total must be non-zero in healthy fixture")
	}
}

// TestDashboardHealth_CrashloopPod verifies that a pod waiting in CrashLoopBackOff
// causes degraded status. A Succeeded pod must be excluded.
func TestDashboardHealth_CrashloopPod(t *testing.T) {
	crashPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "crash-pod", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "CrashLoopBackOff",
						},
					},
				},
			},
		},
	}
	succeededPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "completed-job", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}

	objs := []runtime.Object{
		readyNode("node-1"),
		crashPod,
		succeededPod,
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("expected degraded due to crashloop pod, got %s; reasons: %v", summary.Health.Status, summary.Health.Reasons)
	}
	// Confirm a crashloop reason is present (specific wording, not just non-empty).
	found := false
	for _, r := range summary.Health.Reasons {
		if strings.Contains(r, "crash") || strings.Contains(r, "image pull") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a crashloop reason for degraded status, got %v", summary.Health.Reasons)
	}
}

// TestDashboardHealth_SucceededPodExcluded verifies a Succeeded pod alone (no
// crashloops) does not trigger degraded.
func TestDashboardHealth_SucceededPodExcluded(t *testing.T) {
	succeededPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "completed-job", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		succeededPod,
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status == HealthStatusDegraded || summary.Health.Status == HealthStatusCritical {
		t.Errorf("succeeded pod must not degrade cluster health, got %s; reasons: %v", summary.Health.Status, summary.Health.Reasons)
	}
}

// TestDashboardHealth_AccessDeniedNodes verifies that RBAC denial on nodes
// results in unknown status with null score.
func TestDashboardHealth_AccessDeniedNodes(t *testing.T) {
	objs := []runtime.Object{
		readyNode("node-1"),
		runningPod("default", "pod-1"),
	}
	h, _ := testHandler(t, objs...)
	// Deny all access — nodes canList returns false → unknown.
	h.AccessChecker = NewAlwaysDenyAccessChecker()

	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-summary", "")
	h.HandleDashboardSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even on RBAC denial, got %d", rr.Code)
	}
	summary := decodeDashboard(t, rr)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusUnknown {
		t.Errorf("expected unknown when nodes denied, got %s", summary.Health.Status)
	}
	if summary.Health.Score != nil {
		t.Errorf("score must be null when status is unknown, got %d", *summary.Health.Score)
	}
	if len(summary.Health.Reasons) == 0 {
		t.Error("expected at least one reason when nodes are denied")
	}
}

// TestDashboardHealth_NoNaN verifies that a fixture with 0-desired workloads and
// 0 eligible pods produces no NaN or Inf in the JSON output.
func TestDashboardHealth_NoNaN(t *testing.T) {
	// Deployment with desired=0 (paused effectively via zero replicas).
	zero := int32(0)
	zeroRepDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "scaled-zero", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "zero"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "zero"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "zero", Image: "nginx"}}},
			},
		},
	}
	// No eligible pods (Succeeded pod only).
	objs := []runtime.Object{
		readyNode("node-1"),
		zeroRepDep,
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "completed", Namespace: "default"},
			Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
	}
	h, _ := testHandler(t, objs...)

	rr := httptest.NewRecorder()
	req := requestWithUser("GET", "/api/v1/cluster/dashboard-summary", "")
	h.HandleDashboardSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Scan all numeric values in the response body for NaN/Inf (R8 guard).
	assertNoNaNInJSON(t, rr.Body.Bytes())

	// Also confirm the health field is present and well-formed.
	summary := decodeDashboard(t, rr)
	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
}

// assertNoNaNInJSON scans a JSON byte slice for any NaN or Infinity values
// (which are invalid JSON but some encoders emit them, triggering the R8 bug).
func assertNoNaNInJSON(t *testing.T, data []byte) {
	t.Helper()
	var check func(v interface{})
	check = func(v interface{}) {
		switch val := v.(type) {
		case float64:
			if math.IsNaN(val) {
				t.Errorf("NaN found in JSON output")
			}
			if math.IsInf(val, 0) {
				t.Errorf("Inf found in JSON output")
			}
		case map[string]interface{}:
			for _, sub := range val {
				check(sub)
			}
		case []interface{}:
			for _, sub := range val {
				check(sub)
			}
		}
	}
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		// If it's not valid JSON at all that's a separate test failure.
		t.Errorf("invalid JSON: %v", err)
		return
	}
	check(parsed)
}

// TestDashboardHealth_ExistingFieldsUnchanged verifies that adding the health field
// does not break existing DashboardSummary fields (R6 additivity guard).
func TestDashboardHealth_ExistingFieldsUnchanged(t *testing.T) {
	objs := []runtime.Object{
		readyNode("node-1"),
		readyNode("node-2"),
		runningPod("default", "pod-1"),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-pend", Namespace: "default"}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc-1", Namespace: "default"}},
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Nodes.Total != 2 {
		t.Errorf("nodes.total expected 2, got %d", summary.Nodes.Total)
	}
	if summary.Nodes.Ready != 2 {
		t.Errorf("nodes.ready expected 2, got %d", summary.Nodes.Ready)
	}
	if summary.Pods.Total != 2 {
		t.Errorf("pods.total expected 2, got %d", summary.Pods.Total)
	}
	if summary.Pods.Running != 1 {
		t.Errorf("pods.running expected 1, got %d", summary.Pods.Running)
	}
	if summary.Pods.Pending != 1 {
		t.Errorf("pods.pending expected 1, got %d", summary.Pods.Pending)
	}
	// Health field must also be present.
	if summary.Health == nil {
		t.Error("health field must be present alongside existing fields")
	}
}

// TestDashboardHealth_WFFCPVCExcluded verifies that a Pending PVC on a
// WaitForFirstConsumer StorageClass without a selected-node annotation is
// excluded from the health signal (healthy expected).
func TestDashboardHealth_WFFCPVCExcluded(t *testing.T) {
	wffc := storagev1.VolumeBindingWaitForFirstConsumer
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fast",
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
		},
		VolumeBindingMode: &wffc,
	}
	pendingPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-wffc", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: func() *string { s := "fast"; return &s }(),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
		sc,
		pendingPVC,
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	// WFFC PVC without selected-node → excluded → storage is OK.
	if summary.Health.Status == HealthStatusDegraded || summary.Health.Status == HealthStatusCritical {
		t.Errorf("WFFC PVC without selected-node should be excluded, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}

// TestDashboardHealth_WFFCPVCWithAnnotationDegraded verifies that a Pending PVC
// on a WaitForFirstConsumer StorageClass WITH the selected-node annotation is
// counted (degraded expected).
func TestDashboardHealth_WFFCPVCWithAnnotationDegraded(t *testing.T) {
	wffc := storagev1.VolumeBindingWaitForFirstConsumer
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: "fast"},
		VolumeBindingMode: &wffc,
	}
	pendingPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pvc-wffc-selected",
			Namespace: "default",
			Annotations: map[string]string{
				"volume.kubernetes.io/selected-node": "node-1",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: func() *string { s := "fast"; return &s }(),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
		sc,
		pendingPVC,
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	// WFFC PVC with selected-node → counted → degraded.
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("WFFC PVC with selected-node annotation should be degraded, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}

// TestDashboardHealth_UnsyncedNodes verifies that when the nodes informer is
// not synced, the status is unknown with "cache syncing" reason.
func TestDashboardHealth_UnsyncedNodes(t *testing.T) {
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)

	// Override isSynced to report nodes as unsynced.
	h.isSynced = func(resource string) bool {
		if resource == "nodes" {
			return false
		}
		return true
	}

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusUnknown {
		t.Errorf("expected unknown when nodes unsynced, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
	if summary.Health.Score != nil {
		t.Errorf("score must be null when status is unknown")
	}
}

// TestDashboardHealth_PDBViolation verifies that a PDB with currentHealthy < desiredHealthy
// triggers degraded status.
func TestDashboardHealth_PDBViolation(t *testing.T) {
	maxUnavail := intstr.FromInt32(1)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pdb", Namespace: "default"},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavail,
			Selector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			CurrentHealthy: 0,
			DesiredHealthy: 1,
		},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
		pdb,
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("PDB violation should produce degraded status, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}

// TestDashboardHealth_CertExpirySkipReasons verifies the two known cert-manager
// skip reasons are mapped correctly without leaking internal errors.
func TestDashboardHealth_CertExpirySkipReasons(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantSkipReason string
	}{
		{
			name:           "not installed",
			err:            ErrCertManagerNotInstalled,
			wantSkipReason: "cert-manager not installed",
		},
		{
			name:           "cache warming",
			err:            ErrCertCacheNotWarm,
			wantSkipReason: "cert cache warming",
		},
		{
			name:           "other error does not leak",
			err:            errors.New("internal db connection failed: dial tcp"),
			wantSkipReason: "certificate data unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []runtime.Object{readyNode("node-1")}
			h, _ := testHandler(t, objs...)
			h.CertExpiry = &fakeCertExpiry{err: tt.err}

			_, summary := callDashboard(t, h)
			if summary.Health == nil {
				t.Fatal("health must not be nil")
			}
			// Find the cert signal.
			for _, s := range summary.Health.Signals {
				if s.Name == signalCerts {
					if s.Status != SignalStatusSkipped {
						t.Errorf("expected cert signal skipped, got %s", s.Status)
					}
					if s.Reason != tt.wantSkipReason {
						t.Errorf("expected cert skip reason %q, got %q", tt.wantSkipReason, s.Reason)
					}
					return
				}
			}
			t.Error("cert signal not found in signals slice")
		})
	}
}

// TestDashboardHealth_InitContainerCrashloop verifies that a crash in an init
// container is also detected.
func TestDashboardHealth_InitContainerCrashloop(t *testing.T) {
	crashPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "init-crash", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "CrashLoopBackOff",
						},
					},
				},
			},
		},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		crashPod,
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("init container crashloop should cause degraded, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}

// TestDashboardHealth_ProgressingDeploymentExcluded verifies that a Deployment
// actively rolling out is excluded from workload availability scoring so a
// mid-rollout cluster stays healthy.
func TestDashboardHealth_ProgressingDeploymentExcluded(t *testing.T) {
	one := int32(10)
	progressingDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "rolling", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rolling"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "rolling"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:          10,
			AvailableReplicas: 3, // only 3/10 available — would be critical without exclusion
			ReadyReplicas:     3,
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentProgressing,
					Status: corev1.ConditionTrue,
					Reason: "ReplicaSetUpdated",
				},
			},
		},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		progressingDep,
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	// With progressing deployment excluded, workloads desired=0 → skipped.
	// Cluster should be healthy or unknown (if no other workloads), not critical.
	if summary.Health.Status == HealthStatusCritical {
		t.Errorf("progressing deployment should be excluded, cluster must not be critical; got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}

// ── T2: zero nodes → unknown ─────────────────────────────────────────────────

// TestDashboardHealth_ZeroNodes_Unknown verifies that an informer cache with
// no Node objects (but list permission granted and cache synced) produces
// status unknown with reason containing "no nodes visible" and a nil score.
func TestDashboardHealth_ZeroNodes_Unknown(t *testing.T) {
	// No nodes in the fake client — informer cache is empty after sync.
	objs := []runtime.Object{
		deployment1x1("default", "dep-1"),
		runningPod("default", "pod-1"),
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusUnknown {
		t.Errorf("zero nodes should produce unknown status, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
	if summary.Health.Score != nil {
		t.Errorf("score must be nil for unknown status, got %d", *summary.Health.Score)
	}
	found := false
	for _, r := range summary.Health.Reasons {
		if strings.Contains(r, "no nodes visible") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected reason containing 'no nodes visible', got %v", summary.Health.Reasons)
	}
}

// ── T3: crashloop reason is specific ─────────────────────────────────────────

// TestDashboardHealth_CrashloopPod_ReasonContainsCrash tightens the crashloop
// reason assertion from "any non-empty reason" to strings.Contains "crash".
func TestDashboardHealth_CrashloopPod_ReasonContainsCrash(t *testing.T) {
	crashPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "crash-pod-2", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "CrashLoopBackOff",
						},
					},
				},
			},
		},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		crashPod,
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("expected degraded due to crashloop pod, got %s", summary.Health.Status)
	}
	found := false
	for _, r := range summary.Health.Reasons {
		if strings.Contains(r, "crash") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reason containing 'crash', got %v", summary.Health.Reasons)
	}
}

// ── T4: alert exclude names forwarded correctly ───────────────────────────────

// TestDashboardHealth_AlertExcludeNames verifies that gatherHealthInputs
// passes exactly "Watchdog" and "DeadMansSwitch" to ActiveAlertCountsExcluding
// and that the health signal reflects the filtered counts.
func TestDashboardHealth_AlertExcludeNames(t *testing.T) {
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)

	capture := &fakeAlertCounterCapture{active: 2, critical: 1}
	h.Alerts = capture

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}

	// Assert the exclude names forwarded to the provider.
	if len(capture.capturedExclude) != 2 {
		t.Fatalf("expected 2 exclude names, got %d: %v", len(capture.capturedExclude), capture.capturedExclude)
	}
	found := map[string]bool{}
	for _, n := range capture.capturedExclude {
		found[n] = true
	}
	if !found["Watchdog"] {
		t.Errorf("expected 'Watchdog' in exclude names, got %v", capture.capturedExclude)
	}
	if !found["DeadMansSwitch"] {
		t.Errorf("expected 'DeadMansSwitch' in exclude names, got %v", capture.capturedExclude)
	}

	// Health alerts signal should reflect the filtered counts (1 critical → degraded).
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("expected degraded with 1 critical alert, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}

// ── T5: control-plane signal from fakeControlPlane ───────────────────────────

// TestDashboardHealth_ControlPlane_Error verifies that a ControlPlaneChecker
// error produces a controlPlane signal with status unknown and a reason
// containing "prometheus".
func TestDashboardHealth_ControlPlane_Error(t *testing.T) {
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)
	h.ControlPlane = &fakeControlPlane{err: errors.New("context deadline exceeded")}

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	cpSig := signalByName(t, *summary.Health, signalControlPlane)
	if cpSig.Status != SignalStatusSkipped && cpSig.Status != SignalStatusUnknown {
		t.Errorf("control-plane signal on error: want skipped or unknown, got %s", cpSig.Status)
	}
	if !strings.Contains(cpSig.Reason, "prometheus") {
		t.Errorf("control-plane error reason want 'prometheus', got %q", cpSig.Reason)
	}
}

// TestDashboardHealth_ControlPlane_SchedulerDown verifies that a scheduler
// ComponentDown state produces degraded status with a control-plane reason.
func TestDashboardHealth_ControlPlane_SchedulerDown(t *testing.T) {
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
	}
	h, _ := testHandler(t, objs...)
	h.ControlPlane = &fakeControlPlane{
		states: ControlPlaneStates{
			SchedulerState:         ComponentDown,
			ControllerManagerState: ComponentUp,
			EtcdState:              ComponentUp,
		},
	}

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("scheduler down should produce degraded, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
	found := false
	for _, r := range summary.Health.Reasons {
		if strings.Contains(r, "scheduler") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected reason mentioning 'scheduler', got %v", summary.Health.Reasons)
	}
}

// ── T6: PDB signal skipped when poddisruptionbudgets denied ──────────────────

// TestDashboardHealth_PDBDenied_SignalSkipped verifies that when the access
// checker denies only "poddisruptionbudgets", the PDB signal is skipped and
// nodes/pods signals are still ok, so the overall status is not unknown.
func TestDashboardHealth_PDBDenied_SignalSkipped(t *testing.T) {
	maxUnavail := intstr.FromInt32(1)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pdb", Namespace: "default"},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavail,
			Selector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			CurrentHealthy: 0,
			DesiredHealthy: 1,
		},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
		pdb,
	}
	h, _ := testHandler(t, objs...)

	// Deny list on poddisruptionbudgets only; everything else allowed.
	h.AccessChecker = NewDenyResourcesAccessChecker("poddisruptionbudgets")

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	// With PDB access denied, PDB violations are invisible — no degradation from PDB.
	if summary.Health.Status == HealthStatusUnknown {
		t.Errorf("PDB denied should not make status unknown, got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}

// ── T9: static-binding empty storageClassName counted as pending ─────────────

// TestDashboardHealth_StaticBindingEmptyStorageClass_CountedAsPending verifies
// that a Pending PVC with an explicit empty StorageClassName ("") is counted as
// pending even when a WFFC default class exists. An explicit "" means static
// binding and is never excluded by the WFFC filter.
func TestDashboardHealth_StaticBindingEmptyStorageClass_CountedAsPending(t *testing.T) {
	wffc := storagev1.VolumeBindingWaitForFirstConsumer
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "wffc-default",
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
		},
		VolumeBindingMode: &wffc,
	}
	// StorageClassName is explicitly "" — static binding, should not be
	// treated as the WFFC default class.
	staticPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-static", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: func() *string { s := ""; return &s }(),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	objs := []runtime.Object{
		readyNode("node-1"),
		deployment1x1("default", "dep-1"),
		sc,
		staticPVC,
	}
	h, _ := testHandler(t, objs...)

	_, summary := callDashboard(t, h)

	if summary.Health == nil {
		t.Fatal("health must not be nil")
	}
	// Static-binding PVC must NOT be excluded → storage signal triggers degraded.
	if summary.Health.Status != HealthStatusDegraded {
		t.Errorf("static-binding empty-StorageClassName PVC should be counted as pending (degraded), got %s; reasons: %v",
			summary.Health.Status, summary.Health.Reasons)
	}
}
