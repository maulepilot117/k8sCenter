package resources

import (
	"k8s.io/apimachinery/pkg/labels"

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

func init() { Register(pvAdapter{}) }
