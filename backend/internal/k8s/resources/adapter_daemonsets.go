package resources

import (
	"context"
	"encoding/json"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type daemonSetAdapter struct{}

func (daemonSetAdapter) Kind() string        { return "daemonsets" }
func (daemonSetAdapter) APIResource() string { return "daemonsets" }
func (daemonSetAdapter) DisplayName() string { return "DaemonSet" }
func (daemonSetAdapter) ClusterScoped() bool { return false }

func (daemonSetAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*appsv1.DaemonSet
	var err error
	if ns != "" {
		items, err = inf.DaemonSets().DaemonSets(ns).List(sel)
	} else {
		items, err = inf.DaemonSets().List(sel)
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

func (daemonSetAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.DaemonSets().DaemonSets(ns).Get(name)
}

func (daemonSetAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj appsv1.DaemonSet
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.AppsV1().DaemonSets(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (daemonSetAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj appsv1.DaemonSet
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.AppsV1().DaemonSets(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (daemonSetAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.AppsV1().DaemonSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// Restart implements Restartable.
func (daemonSetAdapter) Restart(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, restartPatch(), metav1.PatchOptions{})
}

// Compile-time interface assertion.
var _ Restartable = daemonSetAdapter{}

func init() { Register(daemonSetAdapter{}) }
