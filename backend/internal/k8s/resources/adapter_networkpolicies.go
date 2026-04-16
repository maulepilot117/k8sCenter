package resources

import (
	"context"
	"encoding/json"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type networkPolicyAdapter struct{}

func (networkPolicyAdapter) Kind() string        { return "networkpolicies" }
func (networkPolicyAdapter) APIResource() string { return "networkpolicies" }
func (networkPolicyAdapter) DisplayName() string { return "NetworkPolicy" }
func (networkPolicyAdapter) ClusterScoped() bool { return false }

func (networkPolicyAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*networkingv1.NetworkPolicy
	var err error
	if ns != "" {
		items, err = inf.NetworkPolicies().NetworkPolicies(ns).List(sel)
	} else {
		items, err = inf.NetworkPolicies().List(sel)
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

func (networkPolicyAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.NetworkPolicies().NetworkPolicies(ns).Get(name)
}

func (networkPolicyAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj networkingv1.NetworkPolicy
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.NetworkingV1().NetworkPolicies(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (networkPolicyAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj networkingv1.NetworkPolicy
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.NetworkingV1().NetworkPolicies(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (networkPolicyAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.NetworkingV1().NetworkPolicies(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(networkPolicyAdapter{}) }
