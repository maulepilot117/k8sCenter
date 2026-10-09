package resources

import (
	"context"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type roleAdapter struct{ ReadOnlyAdapter }

func (roleAdapter) Kind() string        { return "roles" }
func (roleAdapter) APIResource() string { return "roles" }
func (roleAdapter) DisplayName() string { return "Role" }
func (roleAdapter) ClusterScoped() bool { return false }

func (roleAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*rbacv1.Role
	var err error
	if ns != "" {
		items, err = inf.Roles().Roles(ns).List(sel)
	} else {
		items, err = inf.Roles().List(sel)
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

func (roleAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Roles().Roles(ns).Get(name)
}

// ListDirect implements ResourceAdapter.
func (roleAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.RbacV1().Roles(ns).List(ctx, opts)
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
func (roleAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.RbacV1().Roles(ns).Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(roleAdapter{}) }
