package resources

import (
	"k8s.io/apimachinery/pkg/labels"

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

func init() { Register(clusterRoleAdapter{}) }
