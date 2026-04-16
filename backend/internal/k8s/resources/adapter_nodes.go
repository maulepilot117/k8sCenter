package resources

import (
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type nodeAdapter struct{ ReadOnlyAdapter }

func (nodeAdapter) Kind() string        { return "nodes" }
func (nodeAdapter) APIResource() string { return "nodes" }
func (nodeAdapter) DisplayName() string { return "Node" }
func (nodeAdapter) ClusterScoped() bool { return true }

func (nodeAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.Nodes().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (nodeAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.Nodes().Get(name)
}

func init() { Register(nodeAdapter{}) }
