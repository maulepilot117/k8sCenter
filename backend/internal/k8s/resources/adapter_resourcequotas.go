package resources

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

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

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (resourceQuotaAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.CoreV1().ResourceQuotas(ns).List(ctx, opts)
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
func (resourceQuotaAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.CoreV1().ResourceQuotas(ns).Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(resourceQuotaAdapter{}) }
