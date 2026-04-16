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

type clusterRoleBindingAdapter struct{}

func (clusterRoleBindingAdapter) Kind() string        { return "clusterrolebindings" }
func (clusterRoleBindingAdapter) APIResource() string { return "clusterrolebindings" }
func (clusterRoleBindingAdapter) DisplayName() string { return "ClusterRoleBinding" }
func (clusterRoleBindingAdapter) ClusterScoped() bool { return true }

func (clusterRoleBindingAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.ClusterRoleBindings().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (clusterRoleBindingAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.ClusterRoleBindings().Get(name)
}

func (clusterRoleBindingAdapter) Create(ctx context.Context, cs kubernetes.Interface, _ string, body []byte) (any, error) {
	var obj rbacv1.ClusterRoleBinding
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	return cs.RbacV1().ClusterRoleBindings().Create(ctx, &obj, metav1.CreateOptions{})
}

func (clusterRoleBindingAdapter) Update(ctx context.Context, cs kubernetes.Interface, _, name string, body []byte) (any, error) {
	var obj rbacv1.ClusterRoleBinding
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Name = name
	return cs.RbacV1().ClusterRoleBindings().Update(ctx, &obj, metav1.UpdateOptions{})
}

func (clusterRoleBindingAdapter) Delete(ctx context.Context, cs kubernetes.Interface, _, name string) error {
	return cs.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(clusterRoleBindingAdapter{}) }
