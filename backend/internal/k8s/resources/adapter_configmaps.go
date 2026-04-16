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

type configMapAdapter struct{}

func (configMapAdapter) Kind() string        { return "configmaps" }
func (configMapAdapter) APIResource() string { return "configmaps" }
func (configMapAdapter) DisplayName() string { return "ConfigMap" }
func (configMapAdapter) ClusterScoped() bool { return false }

func (configMapAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.ConfigMap
	var err error
	if ns != "" {
		items, err = inf.ConfigMaps().ConfigMaps(ns).List(sel)
	} else {
		items, err = inf.ConfigMaps().List(sel)
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

func (configMapAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.ConfigMaps().ConfigMaps(ns).Get(name)
}

func (configMapAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj corev1.ConfigMap
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.CoreV1().ConfigMaps(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (configMapAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj corev1.ConfigMap
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.CoreV1().ConfigMaps(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (configMapAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.CoreV1().ConfigMaps(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(configMapAdapter{}) }
