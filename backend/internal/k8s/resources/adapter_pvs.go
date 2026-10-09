package resources

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type pvAdapter struct{ ReadOnlyAdapter }

func (pvAdapter) Kind() string        { return "pvs" }
func (pvAdapter) APIResource() string { return "persistentvolumes" }
func (pvAdapter) DisplayName() string { return "PersistentVolume" }
func (pvAdapter) ClusterScoped() bool { return true }

func (pvAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.PersistentVolumes().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (pvAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.PersistentVolumes().Get(name)
}

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (pvAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.CoreV1().PersistentVolumes().List(ctx, opts)
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
func (pvAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(pvAdapter{}) }
