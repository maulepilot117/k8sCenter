package resources

import (
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type endpointSliceAdapter struct{ ReadOnlyAdapter }

func (endpointSliceAdapter) Kind() string        { return "endpointslices" }
func (endpointSliceAdapter) APIResource() string { return "endpointslices" }
func (endpointSliceAdapter) DisplayName() string { return "EndpointSlice" }
func (endpointSliceAdapter) ClusterScoped() bool { return false }

func (endpointSliceAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*discoveryv1.EndpointSlice
	var err error
	if ns != "" {
		items, err = inf.EndpointSlices().EndpointSlices(ns).List(sel)
	} else {
		items, err = inf.EndpointSlices().List(sel)
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

func (endpointSliceAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.EndpointSlices().EndpointSlices(ns).Get(name)
}

func init() { Register(endpointSliceAdapter{}) }
