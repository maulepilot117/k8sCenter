package diagnostics

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func int32Ptr(i int32) *int32 { return &i }

func TestCheckCrashLoop_Failing(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Deployment",
		Name:      "test-deploy",
		Namespace: "default",
		Pods: []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "app",
							RestartCount: 5,
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "CrashLoopBackOff",
								},
							},
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-2", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "app",
							RestartCount: 3,
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "CrashLoopBackOff",
								},
							},
						},
					},
				},
			},
		},
	}

	result := checkCrashLoop(context.Background(), target)
	if result.Status != "fail" {
		t.Errorf("expected status 'fail', got %q", result.Status)
	}
	if len(result.Links) != 2 {
		t.Errorf("expected 2 links, got %d", len(result.Links))
	}
}

func TestCheckCrashLoop_Passing(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Deployment",
		Name:      "test-deploy",
		Namespace: "default",
		Pods: []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:  "app",
							Ready: true,
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{},
							},
						},
					},
				},
			},
		},
	}

	result := checkCrashLoop(context.Background(), target)
	if result.Status != "pass" {
		t.Errorf("expected status 'pass', got %q", result.Status)
	}
}

func TestCheckImagePull_Failing(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Deployment",
		Name:      "test-deploy",
		Namespace: "default",
		Pods: []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:  "app",
							Image: "registry.example.com/app:v1.0",
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "ImagePullBackOff",
								},
							},
						},
					},
				},
			},
		},
	}

	result := checkImagePull(context.Background(), target)
	if result.Status != "fail" {
		t.Errorf("expected status 'fail', got %q", result.Status)
	}
	if result.Detail == "" {
		t.Error("expected non-empty detail")
	}
}

func TestCheckPendingPod_Failing(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Deployment",
		Name:      "test-deploy",
		Namespace: "default",
		Pods: []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					Conditions: []corev1.PodCondition{
						{
							Type:    corev1.PodScheduled,
							Status:  corev1.ConditionFalse,
							Message: "0/3 nodes are available: insufficient cpu",
						},
					},
				},
			},
		},
	}

	result := checkPendingPod(context.Background(), target)
	if result.Status != "fail" {
		t.Errorf("expected status 'fail', got %q", result.Status)
	}
	if result.Remediation == "" {
		t.Error("expected non-empty remediation")
	}
}

func TestCheckReplicaMismatch_Failing(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Deployment",
		Name:      "test-deploy",
		Namespace: "default",
		Object: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(3),
			},
			Status: appsv1.DeploymentStatus{
				ReadyReplicas: 0,
			},
		},
	}

	result := checkReplicaMismatch(context.Background(), target)
	if result.Status != "fail" {
		t.Errorf("expected status 'fail', got %q", result.Status)
	}
}

func TestCheckReplicaMismatch_Passing(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Deployment",
		Name:      "test-deploy",
		Namespace: "default",
		Object: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(3),
			},
			Status: appsv1.DeploymentStatus{
				ReadyReplicas: 3,
			},
		},
	}

	result := checkReplicaMismatch(context.Background(), target)
	if result.Status != "pass" {
		t.Errorf("expected status 'pass', got %q", result.Status)
	}
}

func TestCheckZeroEndpoints_Failing(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Service",
		Name:      "test-svc",
		Namespace: "default",
		Object: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
			},
		},
		Pods: []*corev1.Pod{}, // empty — no matching pods
	}

	result := checkZeroEndpoints(context.Background(), target)
	if result.Status != "warn" {
		t.Errorf("expected status 'warn', got %q", result.Status)
	}
}

func TestCheckPendingPVC_Failing(t *testing.T) {
	sc := "standard"
	target := &DiagnosticTarget{
		Kind:      "PersistentVolumeClaim",
		Name:      "test-pvc",
		Namespace: "default",
		Object: &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pvc", Namespace: "default"},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: &sc,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			Status: corev1.PersistentVolumeClaimStatus{
				Phase: corev1.ClaimPending,
			},
		},
	}

	result := checkPendingPVC(context.Background(), target)
	if result.Status != "warn" {
		t.Errorf("expected status 'warn', got %q", result.Status)
	}
	if result.Detail == "" {
		t.Error("expected non-empty detail with storage class info")
	}
}
