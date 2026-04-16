package resources

import (
	"k8s.io/apimachinery/pkg/labels"

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

func init() { Register(storageClassAdapter{}) }
