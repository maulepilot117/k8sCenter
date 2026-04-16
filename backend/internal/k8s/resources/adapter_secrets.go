package resources

import (
	"context"
	"encoding/json"
	"errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// errSecretsNotCached is returned by ListFromCache/GetFromCache because secrets
// are intentionally excluded from the informer cache for security reasons.
// The existing custom handlers in secrets.go handle list/get via impersonated clients.
var errSecretsNotCached = errors.New("secrets are not cached in the informer — use the dedicated secret handlers")

type secretAdapter struct{}

func (secretAdapter) Kind() string        { return "secrets" }
func (secretAdapter) APIResource() string { return "secrets" }
func (secretAdapter) DisplayName() string { return "Secret" }
func (secretAdapter) ClusterScoped() bool { return false }

func (secretAdapter) ListFromCache(_ *k8s.InformerManager, _ string, _ labels.Selector) ([]any, error) {
	return nil, errSecretsNotCached
}

func (secretAdapter) GetFromCache(_ *k8s.InformerManager, _, _ string) (any, error) {
	return nil, errSecretsNotCached
}

func (secretAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj corev1.Secret
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.CoreV1().Secrets(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (secretAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj corev1.Secret
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.CoreV1().Secrets(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (secretAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(secretAdapter{}) }
