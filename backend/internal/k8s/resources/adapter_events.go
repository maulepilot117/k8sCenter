package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type eventAdapter struct{ ReadOnlyAdapter }

func (eventAdapter) Kind() string        { return "events" }
func (eventAdapter) APIResource() string { return "events" }
func (eventAdapter) DisplayName() string { return "Event" }
func (eventAdapter) ClusterScoped() bool { return false }

func (eventAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.Event
	var err error
	if ns != "" {
		items, err = inf.Events().Events(ns).List(sel)
	} else {
		items, err = inf.Events().List(sel)
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

func (eventAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Events().Events(ns).Get(name)
}

func init() { Register(eventAdapter{}) }
