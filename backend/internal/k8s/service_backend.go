package k8s

// Service backend selection (remote logs and shells U9, KTD6). A remote
// in-cluster service (Hubble Relay, Loki) is reached by port-forward to one
// pod behind its Service, chosen on the server and as the requesting user,
// so no pod address is stored or taken from a client.

import (
	"context"
	"errors"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

var (
	// ErrServiceNoSelector means the Service selects no pods by label (an
	// ExternalName Service, or one backed by hand-made Endpoints). It is not
	// widened to every pod in the namespace.
	ErrServiceNoSelector = errors.New("service has no pod selector")
	// ErrNoReadyBackend means no pod behind the Service is ready to serve.
	ErrNoReadyBackend = errors.New("no ready pod backs the service")
	// ErrBackendPortUndeclared means the chosen pod declares no container
	// port matching the Service port's target.
	ErrBackendPortUndeclared = errors.New("service target port is not declared by its pod")
)

// ServiceBackend is the pod and container port a tunnel to a Service is
// opened to.
type ServiceBackend struct {
	Namespace string
	Pod       string
	Port      int32
}

// SelectServiceBackend lists svc's pods with cs, which must be the
// requesting user's client, and picks the one ready longest and the
// container port behind sp. It fails with ErrServiceNoSelector before
// listing anything, ErrNoReadyBackend, ErrBackendPortUndeclared, or the
// list error itself.
func SelectServiceBackend(ctx context.Context, cs kubernetes.Interface, svc *corev1.Service, sp corev1.ServicePort) (ServiceBackend, error) {
	if len(svc.Spec.Selector) == 0 {
		return ServiceBackend{}, ErrServiceNoSelector
	}
	pods, err := cs.CoreV1().Pods(svc.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
	})
	if err != nil {
		return ServiceBackend{}, err
	}
	pod := longestReadyPod(pods.Items)
	if pod == nil {
		return ServiceBackend{}, ErrNoReadyBackend
	}
	port, ok := containerPortFor(pod, sp)
	if !ok {
		return ServiceBackend{}, ErrBackendPortUndeclared
	}
	return ServiceBackend{Namespace: svc.Namespace, Pod: pod.Name, Port: port}, nil
}

// containerPortFor resolves a Service port's targetPort to a port the pod
// declares. A port the pod does not declare is not resolved.
func containerPortFor(pod *corev1.Pod, sp corev1.ServicePort) (int32, bool) {
	want := sp.TargetPort
	if want.Type == intstr.Int && want.IntVal == 0 {
		want = intstr.FromInt32(sp.Port)
	}
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.Protocol != "" && p.Protocol != corev1.ProtocolTCP {
				continue
			}
			if (want.Type == intstr.String && p.Name == want.StrVal) || (want.Type == intstr.Int && p.ContainerPort == want.IntVal) {
				return p.ContainerPort, true
			}
		}
	}
	return 0, false
}

// longestReadyPod is the Running, Ready, non-terminating pod that has been
// ready longest, or nil when there is none. Ties go to the lower name.
func longestReadyPod(pods []corev1.Pod) *corev1.Pod {
	type candidate struct {
		pod   *corev1.Pod
		since time.Time
	}
	var ready []candidate
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = append(ready, candidate{pod: p, since: c.LastTransitionTime.Time})
				break
			}
		}
	}
	if len(ready) == 0 {
		return nil
	}
	sort.Slice(ready, func(i, j int) bool {
		if !ready[i].since.Equal(ready[j].since) {
			return ready[i].since.Before(ready[j].since)
		}
		return ready[i].pod.Name < ready[j].pod.Name
	})
	return ready[0].pod
}
