package resources

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type replicaSetAdapter struct{ ReadOnlyAdapter }

func (replicaSetAdapter) Kind() string        { return "replicasets" }
func (replicaSetAdapter) APIResource() string { return "replicasets" }
func (replicaSetAdapter) DisplayName() string { return "ReplicaSet" }
func (replicaSetAdapter) ClusterScoped() bool { return false }

func (replicaSetAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*appsv1.ReplicaSet
	var err error
	if ns != "" {
		items, err = inf.ReplicaSets().ReplicaSets(ns).List(sel)
	} else {
		items, err = inf.ReplicaSets().List(sel)
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

func (replicaSetAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.ReplicaSets().ReplicaSets(ns).Get(name)
}

func init() { Register(replicaSetAdapter{}) }
