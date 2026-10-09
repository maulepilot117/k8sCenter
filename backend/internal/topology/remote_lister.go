package topology

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// TruncatedError reports that a remote list of Kind still had more items
// after the k8s.PageList cap. A RemoteLister method returning it also returns
// the Read items it did read; the caller decides whether a partial list may
// be used. Kind is the plural API resource ("pods", "replicasets").
type TruncatedError struct {
	Kind string
	Read int
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("%s list exceeded the remote read cap after %d items", e.Kind, e.Read)
}

// RemoteLister is the ResourceLister for a remote cluster: each method is a
// namespace-scoped direct list through cs, a client already impersonating the
// requesting user, paged and capped by k8s.PageList. It never reads the local
// cluster.
//
// A RemoteLister is built per request. It remembers each (kind, namespace)
// result, error included, so diagnostics resolution and the blast-radius
// graph of one request share a single list per kind. Forbidden and transport
// errors are returned as is, with no items: a refusal at a later page means
// the earlier pages may not be shown either.
type RemoteLister struct {
	cs     kubernetes.Interface
	logger *slog.Logger

	mu   sync.Mutex
	memo map[memoKey]memoEntry
}

type memoKey struct{ kind, namespace string }

type memoEntry struct {
	items any
	err   error
}

var _ ResourceLister = (*RemoteLister)(nil)

// NewRemoteLister returns a RemoteLister reading through cs.
func NewRemoteLister(cs kubernetes.Interface, logger *slog.Logger) *RemoteLister {
	if logger == nil {
		logger = slog.Default()
	}
	return &RemoteLister{cs: cs, logger: logger, memo: map[memoKey]memoEntry{}}
}

// remoteList lists kind in namespace once per RemoteLister. The lock is held
// across the read so concurrent callers for the same key share one list.
func remoteList[T any](
	ctx context.Context, l *RemoteLister, kind, namespace string,
	list func(context.Context, metav1.ListOptions) ([]T, string, error),
) ([]*T, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := memoKey{kind, namespace}
	if e, ok := l.memo[key]; ok {
		items, _ := e.items.([]*T)
		return items, e.err
	}

	values, truncated, err := k8s.PageList(ctx, metav1.ListOptions{}, list)
	var items []*T // stays nil on an error: partial pages are discarded
	if err == nil {
		items = make([]*T, len(values))
		for i := range values {
			items[i] = &values[i]
		}
		if truncated {
			l.logger.Warn("remote list truncated", "kind", kind, "namespace", namespace, "items", len(items))
			err = &TruncatedError{Kind: kind, Read: len(items)}
		}
	}
	l.memo[key] = memoEntry{items: items, err: err}
	return items, err
}

func (l *RemoteLister) ListPods(ctx context.Context, namespace string) ([]*corev1.Pod, error) {
	return remoteList(ctx, l, "pods", namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.Pod, string, error) {
		r, err := l.cs.CoreV1().Pods(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListServices(ctx context.Context, namespace string) ([]*corev1.Service, error) {
	return remoteList(ctx, l, "services", namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.Service, string, error) {
		r, err := l.cs.CoreV1().Services(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListDeployments(ctx context.Context, namespace string) ([]*appsv1.Deployment, error) {
	return remoteList(ctx, l, "deployments", namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.Deployment, string, error) {
		r, err := l.cs.AppsV1().Deployments(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListReplicaSets(ctx context.Context, namespace string) ([]*appsv1.ReplicaSet, error) {
	return remoteList(ctx, l, "replicasets", namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.ReplicaSet, string, error) {
		r, err := l.cs.AppsV1().ReplicaSets(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListStatefulSets(ctx context.Context, namespace string) ([]*appsv1.StatefulSet, error) {
	return remoteList(ctx, l, "statefulsets", namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.StatefulSet, string, error) {
		r, err := l.cs.AppsV1().StatefulSets(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListDaemonSets(ctx context.Context, namespace string) ([]*appsv1.DaemonSet, error) {
	return remoteList(ctx, l, "daemonsets", namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.DaemonSet, string, error) {
		r, err := l.cs.AppsV1().DaemonSets(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListJobs(ctx context.Context, namespace string) ([]*batchv1.Job, error) {
	return remoteList(ctx, l, "jobs", namespace, func(ctx context.Context, o metav1.ListOptions) ([]batchv1.Job, string, error) {
		r, err := l.cs.BatchV1().Jobs(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListCronJobs(ctx context.Context, namespace string) ([]*batchv1.CronJob, error) {
	return remoteList(ctx, l, "cronjobs", namespace, func(ctx context.Context, o metav1.ListOptions) ([]batchv1.CronJob, string, error) {
		r, err := l.cs.BatchV1().CronJobs(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListIngresses(ctx context.Context, namespace string) ([]*networkingv1.Ingress, error) {
	return remoteList(ctx, l, "ingresses", namespace, func(ctx context.Context, o metav1.ListOptions) ([]networkingv1.Ingress, string, error) {
		r, err := l.cs.NetworkingV1().Ingresses(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListConfigMaps(ctx context.Context, namespace string) ([]*corev1.ConfigMap, error) {
	return remoteList(ctx, l, "configmaps", namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.ConfigMap, string, error) {
		r, err := l.cs.CoreV1().ConfigMaps(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListPVCs(ctx context.Context, namespace string) ([]*corev1.PersistentVolumeClaim, error) {
	return remoteList(ctx, l, "persistentvolumeclaims", namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.PersistentVolumeClaim, string, error) {
		r, err := l.cs.CoreV1().PersistentVolumeClaims(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListHPAs(ctx context.Context, namespace string) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	return remoteList(ctx, l, "horizontalpodautoscalers", namespace, func(ctx context.Context, o metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, string, error) {
		r, err := l.cs.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}
