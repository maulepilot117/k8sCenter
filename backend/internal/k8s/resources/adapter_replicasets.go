package resources

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type replicaSetAdapter struct{ ReadOnlyAdapter }

func (replicaSetAdapter) Kind() string        { return "replicasets" }
func (replicaSetAdapter) APIResource() string { return "replicasets" }
func (replicaSetAdapter) DisplayName() string { return "ReplicaSet" }
func (replicaSetAdapter) ClusterScoped() bool { return false }

func (replicaSetAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*appsv1.ReplicaSet
	var err error
	if ns != "" {
		items, err = inf.ReplicaSets().ReplicaSets(ns).List(sel)
	} else {
		items, err = inf.ReplicaSets().List(sel)
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

func (replicaSetAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.ReplicaSets().ReplicaSets(ns).Get(name)
}

// ListDirect implements ResourceAdapter.
func (replicaSetAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.AppsV1().ReplicaSets(ns).List(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	out := make([]any, len(list.Items))
	for i := range list.Items {
		out[i] = &list.Items[i]
	}
	return out, list.Continue, nil
}

// GetDirect implements ResourceAdapter.
func (replicaSetAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.AppsV1().ReplicaSets(ns).Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(replicaSetAdapter{}) }
