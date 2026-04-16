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

type cronJobAdapter struct{}

func (cronJobAdapter) Kind() string        { return "cronjobs" }
func (cronJobAdapter) APIResource() string { return "cronjobs" }
func (cronJobAdapter) DisplayName() string { return "CronJob" }
func (cronJobAdapter) ClusterScoped() bool { return false }

func (cronJobAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*batchv1.CronJob
	var err error
	if ns != "" {
		items, err = inf.CronJobs().CronJobs(ns).List(sel)
	} else {
		items, err = inf.CronJobs().List(sel)
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

func (cronJobAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.CronJobs().CronJobs(ns).Get(name)
}

func (cronJobAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj batchv1.CronJob
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.BatchV1().CronJobs(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

// Update is not supported for CronJobs via the adapter (use suspend/trigger actions instead).
func (cronJobAdapter) Update(_ context.Context, _ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

func (cronJobAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.BatchV1().CronJobs(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// Suspend implements Suspendable.
func (cronJobAdapter) Suspend(ctx context.Context, cs kubernetes.Interface, ns, name string, suspend bool) (any, error) {
	patchData := fmt.Sprintf(`{"spec":{"suspend":%v}}`, suspend)
	return cs.BatchV1().CronJobs(ns).Patch(ctx, name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
}

// Trigger implements Triggerable. It creates a Job from the CronJob's jobTemplate.
func (cronJobAdapter) Trigger(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	cronJob, err := cs.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Truncate prefix to stay within the 63-char name limit after adding "-manual-" suffix + generated chars
	prefix := name
	if len(prefix) > 43 {
		prefix = prefix[:43]
	}

	isController := true
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix + "-manual-",
			Namespace:    ns,
			Labels:       cronJob.Spec.JobTemplate.Labels,
			Annotations: map[string]string{
				"cronjob.kubernetes.io/instantiate": "manual",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "batch/v1",
					Kind:       "CronJob",
					Name:       cronJob.Name,
					UID:        cronJob.UID,
					Controller: &isController,
				},
			},
		},
		Spec: cronJob.Spec.JobTemplate.Spec,
	}

	return cs.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
}

// Compile-time interface assertions.
var (
	_ Suspendable  = cronJobAdapter{}
	_ Triggerable  = cronJobAdapter{}
)

func init() { Register(cronJobAdapter{}) }
