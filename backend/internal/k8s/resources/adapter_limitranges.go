package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type limitRangeAdapter struct{ ReadOnlyAdapter }

func (limitRangeAdapter) Kind() string        { return "limitranges" }
func (limitRangeAdapter) APIResource() string { return "limitranges" }
func (limitRangeAdapter) DisplayName() string { return "LimitRange" }
func (limitRangeAdapter) ClusterScoped() bool { return false }

func (limitRangeAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.LimitRange
	var err error
	if ns != "" {
		items, err = inf.LimitRanges().LimitRanges(ns).List(sel)
	} else {
		items, err = inf.LimitRanges().List(sel)
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

func (limitRangeAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.LimitRanges().LimitRanges(ns).Get(name)
}

func init() { Register(limitRangeAdapter{}) }
