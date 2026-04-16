package resources

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type pvcAdapter struct{}

func (pvcAdapter) Kind() string        { return "pvcs" }
func (pvcAdapter) APIResource() string { return "persistentvolumeclaims" }
func (pvcAdapter) DisplayName() string { return "PVC" }
func (pvcAdapter) ClusterScoped() bool { return false }

func (pvcAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.PersistentVolumeClaim
	var err error
	if ns != "" {
		items, err = inf.PersistentVolumeClaims().PersistentVolumeClaims(ns).List(sel)
	} else {
		items, err = inf.PersistentVolumeClaims().List(sel)
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

func (pvcAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.PersistentVolumeClaims().PersistentVolumeClaims(ns).Get(name)
}

func (pvcAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj corev1.PersistentVolumeClaim
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.CoreV1().PersistentVolumeClaims(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

// Update is not supported for PVCs (immutable after creation).
func (pvcAdapter) Update(_ context.Context, _ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

func (pvcAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(pvcAdapter{}) }
