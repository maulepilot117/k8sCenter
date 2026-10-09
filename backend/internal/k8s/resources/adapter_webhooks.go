package resources

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// --- ValidatingWebhookConfiguration ---

type validatingWebhookAdapter struct{ ReadOnlyAdapter }

func (validatingWebhookAdapter) Kind() string        { return "validatingwebhookconfigurations" }
func (validatingWebhookAdapter) APIResource() string { return "validatingwebhookconfigurations" }
func (validatingWebhookAdapter) DisplayName() string { return "ValidatingWebhookConfiguration" }
func (validatingWebhookAdapter) ClusterScoped() bool { return true }

func (validatingWebhookAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.ValidatingWebhookConfigurations().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (validatingWebhookAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.ValidatingWebhookConfigurations().Get(name)
}

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (validatingWebhookAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, _ string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	out := make([]any, len(list.Items))
	for i := range list.Items {
		out[i] = &list.Items[i]
	}
	return out, list.Continue, nil
}

// GetDirect reads one object from the API server as the caller. ns is ignored
// for cluster-scoped kinds.
func (validatingWebhookAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, _, name string) (any, error) {
	return cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(validatingWebhookAdapter{}) }

// --- MutatingWebhookConfiguration ---

type mutatingWebhookAdapter struct{ ReadOnlyAdapter }

func (mutatingWebhookAdapter) Kind() string        { return "mutatingwebhookconfigurations" }
func (mutatingWebhookAdapter) APIResource() string { return "mutatingwebhookconfigurations" }
func (mutatingWebhookAdapter) DisplayName() string { return "MutatingWebhookConfiguration" }
func (mutatingWebhookAdapter) ClusterScoped() bool { return true }

func (mutatingWebhookAdapter) ListFromCache(inf *k8s.InformerManager, _ string, sel labels.Selector) ([]any, error) {
	items, err := inf.MutatingWebhookConfigurations().List(sel)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (mutatingWebhookAdapter) GetFromCache(inf *k8s.InformerManager, _, name string) (any, error) {
	return inf.MutatingWebhookConfigurations().Get(name)
}

// ListDirect lists one page from the API server as the caller, for a cluster
// that has no informers. ns is ignored for cluster-scoped kinds. The returned
// continue token is the API server's, passed through verbatim.
func (mutatingWebhookAdapter) ListDirect(ctx context.Context, cs kubernetes.Interface, _ string, opts metav1.ListOptions) ([]any, string, error) {
	list, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	out := make([]any, len(list.Items))
	for i := range list.Items {
		out[i] = &list.Items[i]
	}
	return out, list.Continue, nil
}

// GetDirect reads one object from the API server as the caller. ns is ignored
// for cluster-scoped kinds.
func (mutatingWebhookAdapter) GetDirect(ctx context.Context, cs kubernetes.Interface, _, name string) (any, error) {
	return cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, name, metav1.GetOptions{})
}

func init() { Register(mutatingWebhookAdapter{}) }
