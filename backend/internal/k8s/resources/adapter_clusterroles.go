package resources

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type clusterRoleAdapter struct{ ReadOnlyAdapter }

func (clusterRoleAdapter) Kind() string        { return "clusterroles" }
func (clusterRoleAdapter) APIResource() string { return "clusterroles" }
func (clusterRoleAdapter) DisplayName() string { return "ClusterRole" }
func (clusterRoleAdapter) ClusterScoped() bool { return true }

func (clusterRoleAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.ClusterRoles().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (clusterRoleAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.ClusterRoles().Get(name)
}

// ListDirect implements ResourceAdapter.
func (clusterRoleAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, _ string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.RbacV1().ClusterRoles().List(ctx, opts)
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
func (clusterRoleAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, _, name string) (any, error) {
	return cs.RbacV1().ClusterRoles().Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(clusterRoleAdapter{}) }
