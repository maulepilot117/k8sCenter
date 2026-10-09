package resources

import (
	"context"
	"encoding/json"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type ingressAdapter struct{}

func (ingressAdapter) Kind() string        { return "ingresses" }
func (ingressAdapter) APIResource() string { return "ingresses" }
func (ingressAdapter) DisplayName() string { return "Ingress" }
func (ingressAdapter) ClusterScoped() bool { return false }

func (ingressAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*networkingv1.Ingress
	var err error
	if ns != "" {
		items, err = inf.Ingresses().Ingresses(ns).List(sel)
	} else {
		items, err = inf.Ingresses().List(sel)
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

func (ingressAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Ingresses().Ingresses(ns).Get(name)
}

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (ingressAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.NetworkingV1().Ingresses(ns).List(ctx, opts)
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
func (ingressAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.NetworkingV1().Ingresses(ns).Get(ctx, name, metav1.GetOptions{})
}

func (ingressAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj networkingv1.Ingress
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.NetworkingV1().Ingresses(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (ingressAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj networkingv1.Ingress
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.NetworkingV1().Ingresses(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (ingressAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.NetworkingV1().Ingresses(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(ingressAdapter{}) }
