package resources

import (
	"k8s.io/apimachinery/pkg/labels"

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

func init() { Register(mutatingWebhookAdapter{}) }
