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

type serviceAdapter struct{}

func (serviceAdapter) Kind() string        { return "services" }
func (serviceAdapter) APIResource() string { return "services" }
func (serviceAdapter) DisplayName() string { return "Service" }
func (serviceAdapter) ClusterScoped() bool { return false }

func (serviceAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.Service
	var err error
	if ns != "" {
		items, err = inf.Services().Services(ns).List(sel)
	} else {
		items, err = inf.Services().List(sel)
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

func (serviceAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Services().Services(ns).Get(name)
}

func (serviceAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj corev1.Service
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.CoreV1().Services(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (serviceAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj corev1.Service
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.CoreV1().Services(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (serviceAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(serviceAdapter{}) }
