package resources

import (
	"context"
	"encoding/json"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type jobAdapter struct{}

func (jobAdapter) Kind() string        { return "jobs" }
func (jobAdapter) APIResource() string { return "jobs" }
func (jobAdapter) DisplayName() string { return "Job" }
func (jobAdapter) ClusterScoped() bool { return false }

func (jobAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*batchv1.Job
	var err error
	if ns != "" {
		items, err = inf.Jobs().Jobs(ns).List(sel)
	} else {
		items, err = inf.Jobs().List(sel)
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

func (jobAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Jobs().Jobs(ns).Get(name)
}

func (jobAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj batchv1.Job
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.BatchV1().Jobs(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

// Update is not supported for Jobs — they are immutable after creation.
func (jobAdapter) Update(_ context.Context, _ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

func (jobAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	propagation := metav1.DeletePropagationBackground
	return cs.BatchV1().Jobs(ns).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &propagation})
}

// Suspend implements Suspendable.
func (jobAdapter) Suspend(ctx context.Context, cs kubernetes.Interface, ns, name string, suspend bool) (any, error) {
	patchData := fmt.Sprintf(`{"spec":{"suspend":%v}}`, suspend)
	return cs.BatchV1().Jobs(ns).Patch(ctx, name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
}

// Compile-time interface assertion.
var _ Suspendable = jobAdapter{}

func init() { Register(jobAdapter{}) }
