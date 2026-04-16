package resources

import (
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"

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

func init() { Register(roleAdapter{}) }
