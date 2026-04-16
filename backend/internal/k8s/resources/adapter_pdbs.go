package resources

import (
	"context"
	"encoding/json"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type pdbAdapter struct{}

func (pdbAdapter) Kind() string        { return "pdbs" }
func (pdbAdapter) APIResource() string { return "poddisruptionbudgets" }
func (pdbAdapter) DisplayName() string { return "PodDisruptionBudget" }
func (pdbAdapter) ClusterScoped() bool { return false }

func (pdbAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*policyv1.PodDisruptionBudget
	var err error
	if ns != "" {
		items, err = inf.PodDisruptionBudgets().PodDisruptionBudgets(ns).List(sel)
	} else {
		items, err = inf.PodDisruptionBudgets().List(sel)
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

func (pdbAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.PodDisruptionBudgets().PodDisruptionBudgets(ns).Get(name)
}

func (pdbAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj policyv1.PodDisruptionBudget
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.PolicyV1().PodDisruptionBudgets(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

// Update is not supported for PodDisruptionBudgets.
func (pdbAdapter) Update(_ context.Context, _ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

func (pdbAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.PolicyV1().PodDisruptionBudgets(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(pdbAdapter{}) }
