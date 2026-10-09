package resources

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type storageClassAdapter struct{ ReadOnlyAdapter }

func (storageClassAdapter) Kind() string        { return "storageclasses" }
func (storageClassAdapter) APIResource() string { return "storageclasses" }
func (storageClassAdapter) DisplayName() string { return "StorageClass" }
func (storageClassAdapter) ClusterScoped() bool { return true }

func (storageClassAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.StorageClasses().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (storageClassAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.StorageClasses().Get(name)
}

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (storageClassAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.StorageV1().StorageClasses().List(ctx, opts)
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
func (storageClassAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(storageClassAdapter{}) }
