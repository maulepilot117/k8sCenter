package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type resourceQuotaAdapter struct{ ReadOnlyAdapter }

func (resourceQuotaAdapter) Kind() string        { return "resourcequotas" }
func (resourceQuotaAdapter) APIResource() string { return "resourcequotas" }
func (resourceQuotaAdapter) DisplayName() string { return "ResourceQuota" }
func (resourceQuotaAdapter) ClusterScoped() bool { return false }

func (resourceQuotaAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.ResourceQuota
	var err error
	if ns != "" {
		items, err = inf.ResourceQuotas().ResourceQuotas(ns).List(sel)
	} else {
		items, err = inf.ResourceQuotas().List(sel)
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

func (resourceQuotaAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.ResourceQuotas().ResourceQuotas(ns).Get(name)
}

func init() { Register(resourceQuotaAdapter{}) }
