package resources

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type serviceAccountAdapter struct{ ReadOnlyAdapter }

func (serviceAccountAdapter) Kind() string        { return "serviceaccounts" }
func (serviceAccountAdapter) APIResource() string { return "serviceaccounts" }
func (serviceAccountAdapter) DisplayName() string { return "ServiceAccount" }
func (serviceAccountAdapter) ClusterScoped() bool { return false }

func (serviceAccountAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.ServiceAccount
	var err error
	if ns != "" {
		items, err = inf.ServiceAccounts().ServiceAccounts(ns).List(sel)
	} else {
		items, err = inf.ServiceAccounts().List(sel)
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

func (serviceAccountAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.ServiceAccounts().ServiceAccounts(ns).Get(name)
}

// ListDirect implements ResourceAdapter.
func (serviceAccountAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.CoreV1().ServiceAccounts(ns).List(ctx, opts)
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
func (serviceAccountAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.CoreV1().ServiceAccounts(ns).Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(serviceAccountAdapter{}) }
