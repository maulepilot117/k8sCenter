package topology

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// InformerLister adapts *k8s.InformerManager to the ResourceLister interface,
// keeping the topology package decoupled from informer internals.
type InformerLister struct {
	im *k8s.InformerManager
}

// NewInformerLister creates a ResourceLister backed by the given InformerManager.
func NewInformerLister(im *k8s.InformerManager) *InformerLister {
	return &InformerLister{im: im}
}

func (l *InformerLister) ListPods(_ context.Context, namespace string) ([]*corev1.Pod, error) {
	return l.im.Pods().Pods(namespace).List(labels.Everything())
}

func (l *InformerLister) ListServices(_ context.Context, namespace string) ([]*corev1.Service, error) {
	return l.im.Services().Services(namespace).List(labels.Everything())
}

func (l *InformerLister) ListDeployments(_ context.Context, namespace string) ([]*appsv1.Deployment, error) {
	return l.im.Deployments().Deployments(namespace).List(labels.Everything())
}

func (l *InformerLister) ListReplicaSets(_ context.Context, namespace string) ([]*appsv1.ReplicaSet, error) {
	return l.im.ReplicaSets().ReplicaSets(namespace).List(labels.Everything())
}

func (l *InformerLister) ListStatefulSets(_ context.Context, namespace string) ([]*appsv1.StatefulSet, error) {
	return l.im.StatefulSets().StatefulSets(namespace).List(labels.Everything())
}

func (l *InformerLister) ListDaemonSets(_ context.Context, namespace string) ([]*appsv1.DaemonSet, error) {
	return l.im.DaemonSets().DaemonSets(namespace).List(labels.Everything())
}

func (l *InformerLister) ListJobs(_ context.Context, namespace string) ([]*batchv1.Job, error) {
	return l.im.Jobs().Jobs(namespace).List(labels.Everything())
}

func (l *InformerLister) ListCronJobs(_ context.Context, namespace string) ([]*batchv1.CronJob, error) {
	return l.im.CronJobs().CronJobs(namespace).List(labels.Everything())
}

func (l *InformerLister) ListIngresses(_ context.Context, namespace string) ([]*networkingv1.Ingress, error) {
	return l.im.Ingresses().Ingresses(namespace).List(labels.Everything())
}

func (l *InformerLister) ListConfigMaps(_ context.Context, namespace string) ([]*corev1.ConfigMap, error) {
	return l.im.ConfigMaps().ConfigMaps(namespace).List(labels.Everything())
}

func (l *InformerLister) ListPVCs(_ context.Context, namespace string) ([]*corev1.PersistentVolumeClaim, error) {
	return l.im.PersistentVolumeClaims().PersistentVolumeClaims(namespace).List(labels.Everything())
}

func (l *InformerLister) ListHPAs(_ context.Context, namespace string) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	return l.im.HorizontalPodAutoscalers().HorizontalPodAutoscalers(namespace).List(labels.Everything())
}
