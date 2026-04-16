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

type namespaceAdapter struct{}

func (namespaceAdapter) Kind() string        { return "namespaces" }
func (namespaceAdapter) APIResource() string { return "namespaces" }
func (namespaceAdapter) DisplayName() string { return "Namespace" }
func (namespaceAdapter) ClusterScoped() bool { return true }

func (namespaceAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.Namespaces().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (namespaceAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.Namespaces().Get(name)
}

func (namespaceAdapter) Create(ctx context.Context, cs kubernetes.Interface, _ string, body []byte) (any, error) {
	var obj corev1.Namespace
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	return cs.CoreV1().Namespaces().Create(ctx, &obj, metav1.CreateOptions{})
}

// Update is not supported for namespaces (use YAML apply for metadata changes).
func (namespaceAdapter) Update(_ context.Context, _ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

func (namespaceAdapter) Delete(ctx context.Context, cs kubernetes.Interface, _, name string) error {
	return cs.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(namespaceAdapter{}) }
