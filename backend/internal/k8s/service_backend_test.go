package k8s

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

var backendLabels = map[string]string{"app": "loki"}

func backendService(selector map[string]string, target intstr.IntOrString) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "loki"},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: target}},
		},
	}
}

// backendPod is a pod in the Service's namespace with the given readiness.
// It declares an http container port 3100 over TCP and a UDP port 3100.
func backendPod(name string, phase corev1.PodPhase, ready bool, readySince time.Time) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: name, Labels: backendLabels},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "loki",
			Ports: []corev1.ContainerPort{
				{Name: "gossip", ContainerPort: 7946, Protocol: corev1.ProtocolUDP},
				{Name: "http", ContainerPort: 3100, Protocol: corev1.ProtocolTCP},
			},
		}}},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status, LastTransitionTime: metav1.NewTime(readySince)}},
		},
	}
}

func backendCluster(t *testing.T, objs ...kruntime.Object) *kfake.Clientset {
	t.Helper()
	return kfake.NewClientset(objs...)
}

func listedPods(cs *kfake.Clientset) bool {
	for _, a := range cs.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == "pods" {
			return true
		}
	}
	return false
}

func TestSelectServiceBackend_LongestReadyPodWins(t *testing.T) {
	now := time.Now()
	since := now.Add(-48 * time.Hour)
	terminating := backendPod("loki-0-terminating", corev1.PodRunning, true, now.Add(-100*time.Hour))
	terminating.DeletionTimestamp = &metav1.Time{Time: now}
	cs := backendCluster(t,
		backendPod("loki-new", corev1.PodRunning, true, now.Add(-time.Minute)),
		// Equal ready times: the lower name wins.
		backendPod("loki-old-b", corev1.PodRunning, true, since),
		backendPod("loki-old-a", corev1.PodRunning, true, since),
		// Never chosen however long ago their condition changed.
		backendPod("loki-0-unready", corev1.PodRunning, false, now.Add(-100*time.Hour)),
		backendPod("loki-0-pending", corev1.PodPending, true, now.Add(-100*time.Hour)),
		terminating,
	)
	svc := backendService(backendLabels, intstr.FromString("http"))

	got, err := SelectServiceBackend(context.Background(), cs, svc, svc.Spec.Ports[0])
	if err != nil {
		t.Fatalf("SelectServiceBackend: %v", err)
	}
	want := ServiceBackend{Namespace: "monitoring", Pod: "loki-old-a", Port: 3100}
	if got != want {
		t.Errorf("backend = %+v, want %+v", got, want)
	}
}

func TestSelectServiceBackend_NoReadyPod(t *testing.T) {
	cs := backendCluster(t, backendPod("loki-0", corev1.PodRunning, false, time.Now()))
	svc := backendService(backendLabels, intstr.FromString("http"))
	_, err := SelectServiceBackend(context.Background(), cs, svc, svc.Spec.Ports[0])
	if !errors.Is(err, ErrNoReadyBackend) {
		t.Errorf("err = %v, want ErrNoReadyBackend", err)
	}
}

func TestSelectServiceBackend_TargetPort(t *testing.T) {
	cs := backendCluster(t, backendPod("loki-0", corev1.PodRunning, true, time.Now().Add(-time.Hour)))
	cases := []struct {
		name     string
		port     corev1.ServicePort
		wantPort int32
		wantErr  error
	}{
		{"named target resolves to the declared port", corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("http")}, 3100, nil},
		{"numeric target resolves", corev1.ServicePort{Port: 80, TargetPort: intstr.FromInt32(3100)}, 3100, nil},
		{"unset target falls back to the service port", corev1.ServicePort{Port: 3100}, 3100, nil},
		{"undeclared numeric target is not found", corev1.ServicePort{Port: 80, TargetPort: intstr.FromInt32(9999)}, 0, ErrBackendPortUndeclared},
		{"undeclared named target is not found", corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("grpc")}, 0, ErrBackendPortUndeclared},
		{"a UDP port is not a TCP target", corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("gossip")}, 0, ErrBackendPortUndeclared},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := backendService(backendLabels, tc.port.TargetPort)
			got, err := SelectServiceBackend(context.Background(), cs, svc, tc.port)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got.Port != tc.wantPort {
				t.Errorf("port = %d, want %d", got.Port, tc.wantPort)
			}
		})
	}
}

func TestSelectServiceBackend_EmptySelectorListsNothing(t *testing.T) {
	cs := backendCluster(t, backendPod("loki-0", corev1.PodRunning, true, time.Now().Add(-time.Hour)))
	svc := backendService(nil, intstr.FromString("http"))
	_, err := SelectServiceBackend(context.Background(), cs, svc, svc.Spec.Ports[0])
	if !errors.Is(err, ErrServiceNoSelector) {
		t.Errorf("err = %v, want ErrServiceNoSelector", err)
	}
	if listedPods(cs) {
		t.Error("pods were listed for an empty selector")
	}
}

func TestSelectServiceBackend_ListsOnlySelectedPods(t *testing.T) {
	other := backendPod("grafana-0", corev1.PodRunning, true, time.Now().Add(-100*time.Hour))
	other.Labels = map[string]string{"app": "grafana"}
	cs := backendCluster(t, other, backendPod("loki-0", corev1.PodRunning, true, time.Now().Add(-time.Hour)))
	svc := backendService(backendLabels, intstr.FromString("http"))
	got, err := SelectServiceBackend(context.Background(), cs, svc, svc.Spec.Ports[0])
	if err != nil {
		t.Fatalf("SelectServiceBackend: %v", err)
	}
	if got.Pod != "loki-0" {
		t.Errorf("pod = %q, want loki-0 (the pod the selector matches)", got.Pod)
	}
}

func TestSelectServiceBackend_ListErrorIsReturned(t *testing.T) {
	cs := backendCluster(t)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("denied"))
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, kruntime.Object, error) {
		return true, nil, forbidden
	})
	svc := backendService(backendLabels, intstr.FromString("http"))
	_, err := SelectServiceBackend(context.Background(), cs, svc, svc.Spec.Ports[0])
	if !apierrors.IsForbidden(err) {
		t.Errorf("err = %v, want the Forbidden list error", err)
	}
}
