package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

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

func init() { Register(serviceAccountAdapter{}) }
