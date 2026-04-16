package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type endpointsAdapter struct{ ReadOnlyAdapter }

func (endpointsAdapter) Kind() string        { return "endpoints" }
func (endpointsAdapter) APIResource() string { return "endpoints" }
func (endpointsAdapter) DisplayName() string { return "Endpoints" }
func (endpointsAdapter) ClusterScoped() bool { return false }

func (endpointsAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.Endpoints
	var err error
	if ns != "" {
		items, err = inf.Endpoints().Endpoints(ns).List(sel)
	} else {
		items, err = inf.Endpoints().List(sel)
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

func (endpointsAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Endpoints().Endpoints(ns).Get(name)
}

func init() { Register(endpointsAdapter{}) }
