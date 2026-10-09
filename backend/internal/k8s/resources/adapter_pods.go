package resources

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type podAdapter struct{}

func (podAdapter) Kind() string        { return "pods" }
func (podAdapter) APIResource() string { return "pods" }
func (podAdapter) DisplayName() string { return "Pod" }
func (podAdapter) ClusterScoped() bool { return false }

func (podAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.Pod
	var err error
	if ns != "" {
		items, err = inf.Pods().Pods(ns).List(sel)
	} else {
		items, err = inf.Pods().List(sel)
	}
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (podAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Pods().Pods(ns).Get(name)
}

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (podAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.CoreV1().Pods(ns).List(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	out := make([]any, len(list.Items))
	for i := range list.Items {
		out[i] = &list.Items[i]
	}
	return out, list.Continue, nil
}

// GetDirect reads one object from the API server as the caller. ns is ignored
// for cluster-scoped kinds.
func (podAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
}

// Create is not supported for pods (they are created by controllers).
func (podAdapter) Create(_ context.Context, _ kubernetes.Interface, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

// Update is not supported for pods (they are created by controllers).
func (podAdapter) Update(_ context.Context, _ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

func (podAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(podAdapter{}) }
