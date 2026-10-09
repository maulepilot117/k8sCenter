package resources

import (
	"context"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

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

// ListDirect implements ResourceAdapter.
func (endpointSliceAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.DiscoveryV1().EndpointSlices(ns).List(ctx, opts)
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
func (endpointSliceAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.DiscoveryV1().EndpointSlices(ns).Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(endpointSliceAdapter{}) }
