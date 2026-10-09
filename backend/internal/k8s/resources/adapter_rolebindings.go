package resources

import (
	"context"
	"encoding/json"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type roleBindingAdapter struct{}

func (roleBindingAdapter) Kind() string        { return "rolebindings" }
func (roleBindingAdapter) APIResource() string { return "rolebindings" }
func (roleBindingAdapter) DisplayName() string { return "RoleBinding" }
func (roleBindingAdapter) ClusterScoped() bool { return false }

func (roleBindingAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*rbacv1.RoleBinding
	var err error
	if ns != "" {
		items, err = inf.RoleBindings().RoleBindings(ns).List(sel)
	} else {
		items, err = inf.RoleBindings().List(sel)
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

func (roleBindingAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.RoleBindings().RoleBindings(ns).Get(name)
}

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (roleBindingAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.RbacV1().RoleBindings(ns).List(ctx, opts)
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
func (roleBindingAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.RbacV1().RoleBindings(ns).Get(ctx, name, metav1.GetOptions{})
}

func (roleBindingAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj rbacv1.RoleBinding
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.RbacV1().RoleBindings(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (roleBindingAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj rbacv1.RoleBinding
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.RbacV1().RoleBindings(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (roleBindingAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.RbacV1().RoleBindings(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(roleBindingAdapter{}) }
