package topology

import (
	"context"
	"errors"
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
	"github.com/kubecenter/kubecenter/internal/recoverutil"
)

// TruncatedError reports that a remote list of Kind still had more items
// after the k8s.PageList cap. A RemoteLister method returning it also returns
// the Read items it did read; the caller decides whether a partial list may
// be used. Kind is the plural API resource, one of the Kind* constants.
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

	mu   sync.Mutex // guards memo lookups and inserts only, never a read
	memo map[memoKey]*memoEntry
}

type memoKey struct{ kind, namespace string }

// memoEntry is one (kind, namespace) list. The goroutine that inserts it
// performs the read, fills items and err, then closes done; every other
// caller waits on done. items is a []*T whose T is fixed by the kind: each
// kind is listed by exactly one method, so the assertion in remoteList
// cannot see another type.
type memoEntry struct {
	done  chan struct{}
	items any
	err   error
}

// errListAborted is what waiters see when the goroutine performing a list
// panicked before recording a result.
var errListAborted = errors.New("remote list aborted")

var (
	_ ResourceLister = (*RemoteLister)(nil)
	_ Prefetcher     = (*RemoteLister)(nil)
)

// NewRemoteLister returns a RemoteLister reading through cs.
func NewRemoteLister(cs kubernetes.Interface, logger *slog.Logger) *RemoteLister {
	if logger == nil {
		logger = slog.Default()
	}
	return &RemoteLister{cs: cs, logger: logger, memo: map[memoKey]*memoEntry{}}
}

// remoteList lists kind in namespace once per RemoteLister. The first caller
// for a key performs the read; concurrent callers for the same key wait for
// it (or for their own ctx), and callers for other keys are not blocked.
func remoteList[T any](
	ctx context.Context, l *RemoteLister, kind, namespace string,
	list func(context.Context, metav1.ListOptions) ([]T, string, error),
) ([]*T, error) {
	key := memoKey{kind, namespace}
	l.mu.Lock()
	e, found := l.memo[key]
	if !found {
		e = &memoEntry{done: make(chan struct{}), err: errListAborted}
		l.memo[key] = e
	}
	l.mu.Unlock()

	if found {
		select {
		case <-e.done:
			items, _ := e.items.([]*T)
			return items, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Closed even if list panics, so waiters see errListAborted rather than
	// blocking until their deadline.
	defer close(e.done)
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
	e.items, e.err = items, err
	return items, err
}

// prefetchConcurrency bounds the lists one Prefetch runs at once.
const prefetchConcurrency = 4

// Prefetch warms the memo for kinds (plural resources, the Kind* constants)
// in namespace, at most prefetchConcurrency lists at a time, and returns when
// all of them have finished. Results, errors included, are read back through
// the List methods; an unknown kind is ignored.
func (l *RemoteLister) Prefetch(ctx context.Context, namespace string, kinds ...string) {
	sem := make(chan struct{}, prefetchConcurrency)
	var wg sync.WaitGroup
	fetchers := l.fetchers()
	for _, kind := range kinds {
		fetch, ok := fetchers[kind]
		if !ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			recoverutil.Safe(l.logger, "topology.remote-prefetch."+kind, func() {
				_ = fetch(ctx, namespace)
			})
		}()
	}
	wg.Wait()
}

// fetchers maps each plural kind to the List method that reads it, discarding
// the items (Prefetch only warms the memo).
func (l *RemoteLister) fetchers() map[string]func(context.Context, string) error {
	return map[string]func(context.Context, string) error{
		KindPods:         func(ctx context.Context, ns string) error { _, err := l.ListPods(ctx, ns); return err },
		KindServices:     func(ctx context.Context, ns string) error { _, err := l.ListServices(ctx, ns); return err },
		KindDeployments:  func(ctx context.Context, ns string) error { _, err := l.ListDeployments(ctx, ns); return err },
		KindReplicaSets:  func(ctx context.Context, ns string) error { _, err := l.ListReplicaSets(ctx, ns); return err },
		KindStatefulSets: func(ctx context.Context, ns string) error { _, err := l.ListStatefulSets(ctx, ns); return err },
		KindDaemonSets:   func(ctx context.Context, ns string) error { _, err := l.ListDaemonSets(ctx, ns); return err },
		KindJobs:         func(ctx context.Context, ns string) error { _, err := l.ListJobs(ctx, ns); return err },
		KindCronJobs:     func(ctx context.Context, ns string) error { _, err := l.ListCronJobs(ctx, ns); return err },
		KindIngresses:    func(ctx context.Context, ns string) error { _, err := l.ListIngresses(ctx, ns); return err },
		KindConfigMaps:   func(ctx context.Context, ns string) error { _, err := l.ListConfigMaps(ctx, ns); return err },
		KindPVCs:         func(ctx context.Context, ns string) error { _, err := l.ListPVCs(ctx, ns); return err },
		KindHPAs:         func(ctx context.Context, ns string) error { _, err := l.ListHPAs(ctx, ns); return err },
	}
}

func (l *RemoteLister) ListPods(ctx context.Context, namespace string) ([]*corev1.Pod, error) {
	return remoteList(ctx, l, KindPods, namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.Pod, string, error) {
		r, err := l.cs.CoreV1().Pods(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListServices(ctx context.Context, namespace string) ([]*corev1.Service, error) {
	return remoteList(ctx, l, KindServices, namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.Service, string, error) {
		r, err := l.cs.CoreV1().Services(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListDeployments(ctx context.Context, namespace string) ([]*appsv1.Deployment, error) {
	return remoteList(ctx, l, KindDeployments, namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.Deployment, string, error) {
		r, err := l.cs.AppsV1().Deployments(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListReplicaSets(ctx context.Context, namespace string) ([]*appsv1.ReplicaSet, error) {
	return remoteList(ctx, l, KindReplicaSets, namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.ReplicaSet, string, error) {
		r, err := l.cs.AppsV1().ReplicaSets(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListStatefulSets(ctx context.Context, namespace string) ([]*appsv1.StatefulSet, error) {
	return remoteList(ctx, l, KindStatefulSets, namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.StatefulSet, string, error) {
		r, err := l.cs.AppsV1().StatefulSets(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListDaemonSets(ctx context.Context, namespace string) ([]*appsv1.DaemonSet, error) {
	return remoteList(ctx, l, KindDaemonSets, namespace, func(ctx context.Context, o metav1.ListOptions) ([]appsv1.DaemonSet, string, error) {
		r, err := l.cs.AppsV1().DaemonSets(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListJobs(ctx context.Context, namespace string) ([]*batchv1.Job, error) {
	return remoteList(ctx, l, KindJobs, namespace, func(ctx context.Context, o metav1.ListOptions) ([]batchv1.Job, string, error) {
		r, err := l.cs.BatchV1().Jobs(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListCronJobs(ctx context.Context, namespace string) ([]*batchv1.CronJob, error) {
	return remoteList(ctx, l, KindCronJobs, namespace, func(ctx context.Context, o metav1.ListOptions) ([]batchv1.CronJob, string, error) {
		r, err := l.cs.BatchV1().CronJobs(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListIngresses(ctx context.Context, namespace string) ([]*networkingv1.Ingress, error) {
	return remoteList(ctx, l, KindIngresses, namespace, func(ctx context.Context, o metav1.ListOptions) ([]networkingv1.Ingress, string, error) {
		r, err := l.cs.NetworkingV1().Ingresses(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListConfigMaps(ctx context.Context, namespace string) ([]*corev1.ConfigMap, error) {
	return remoteList(ctx, l, KindConfigMaps, namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.ConfigMap, string, error) {
		r, err := l.cs.CoreV1().ConfigMaps(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListPVCs(ctx context.Context, namespace string) ([]*corev1.PersistentVolumeClaim, error) {
	return remoteList(ctx, l, KindPVCs, namespace, func(ctx context.Context, o metav1.ListOptions) ([]corev1.PersistentVolumeClaim, string, error) {
		r, err := l.cs.CoreV1().PersistentVolumeClaims(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}

func (l *RemoteLister) ListHPAs(ctx context.Context, namespace string) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	return remoteList(ctx, l, KindHPAs, namespace, func(ctx context.Context, o metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, string, error) {
		r, err := l.cs.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, o)
		if err != nil {
			return nil, "", err
		}
		return r.Items, r.Continue, nil
	})
}
