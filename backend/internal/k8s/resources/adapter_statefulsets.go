package resources

import (
	"context"
	"encoding/json"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type statefulSetAdapter struct{}

func (statefulSetAdapter) Kind() string        { return "statefulsets" }
func (statefulSetAdapter) APIResource() string { return "statefulsets" }
func (statefulSetAdapter) DisplayName() string { return "StatefulSet" }
func (statefulSetAdapter) ClusterScoped() bool { return false }

func (statefulSetAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*appsv1.StatefulSet
	var err error
	if ns != "" {
		items, err = inf.StatefulSets().StatefulSets(ns).List(sel)
	} else {
		items, err = inf.StatefulSets().List(sel)
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

func (statefulSetAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.StatefulSets().StatefulSets(ns).Get(name)
}

func (statefulSetAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj appsv1.StatefulSet
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.AppsV1().StatefulSets(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (statefulSetAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj appsv1.StatefulSet
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.AppsV1().StatefulSets(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (statefulSetAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// Scale implements Scalable.
func (statefulSetAdapter) Scale(ctx context.Context, cs kubernetes.Interface, ns, name string, replicas int32) (any, error) {
	scale := &autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       autoscalingv1.ScaleSpec{Replicas: replicas},
	}
	return cs.AppsV1().StatefulSets(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
}

// Restart implements Restartable.
func (statefulSetAdapter) Restart(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, restartPatch(), metav1.PatchOptions{})
}

// Compile-time interface assertions.
var (
	_ Scalable    = statefulSetAdapter{}
	_ Restartable = statefulSetAdapter{}
)

func init() { Register(statefulSetAdapter{}) }
