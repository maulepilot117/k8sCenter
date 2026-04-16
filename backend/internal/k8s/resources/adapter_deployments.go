package resources

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type deploymentAdapter struct{}

func (deploymentAdapter) Kind() string        { return "deployments" }
func (deploymentAdapter) APIResource() string { return "deployments" }
func (deploymentAdapter) DisplayName() string { return "Deployment" }
func (deploymentAdapter) ClusterScoped() bool { return false }

func (deploymentAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*appsv1.Deployment
	var err error
	if ns != "" {
		items, err = inf.Deployments().Deployments(ns).List(sel)
	} else {
		items, err = inf.Deployments().List(sel)
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

func (deploymentAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.Deployments().Deployments(ns).Get(name)
}

func (deploymentAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj appsv1.Deployment
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.AppsV1().Deployments(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (deploymentAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj appsv1.Deployment
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.AppsV1().Deployments(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (deploymentAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// Scale implements Scalable.
func (deploymentAdapter) Scale(ctx context.Context, cs kubernetes.Interface, ns, name string, replicas int32) (any, error) {
	scale := &autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       autoscalingv1.ScaleSpec{Replicas: replicas},
	}
	return cs.AppsV1().Deployments(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
}

// Restart implements Restartable.
func (deploymentAdapter) Restart(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error) {
	return cs.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, restartPatch(), metav1.PatchOptions{})
}

// Rollback implements Rollbackable. It finds the ReplicaSet matching the target
// revision and patches the Deployment's pod template with that RS's template.
func (a deploymentAdapter) Rollback(ctx context.Context, cs kubernetes.Interface, ns, name string, revision int64) (any, error) {
	// Get the deployment to scope the RS list query
	dep, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// List ReplicaSets owned by this Deployment
	rsList, err := cs.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(dep.Spec.Selector),
	})
	if err != nil {
		return nil, err
	}

	var targetRS *appsv1.ReplicaSet
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if rs.Annotations["deployment.kubernetes.io/revision"] != fmt.Sprintf("%d", revision) {
			continue
		}
		for _, ownerRef := range rs.OwnerReferences {
			if ownerRef.Kind == "Deployment" && ownerRef.Name == name {
				targetRS = rs
				break
			}
		}
		if targetRS != nil {
			break
		}
	}

	if targetRS == nil {
		return nil, fmt.Errorf("revision %d not found for deployment %s", revision, name)
	}

	// Patch the Deployment with the target RS's pod template
	templateBytes, err := json.Marshal(targetRS.Spec.Template)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal template: %w", err)
	}

	patchData := fmt.Sprintf(`{"spec":{"template":%s}}`, string(templateBytes))
	return cs.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
}

// Compile-time interface assertions.
var (
	_ Scalable     = deploymentAdapter{}
	_ Restartable  = deploymentAdapter{}
	_ Rollbackable = deploymentAdapter{}
)

func init() { Register(deploymentAdapter{}) }
