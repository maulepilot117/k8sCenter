package resources

import (
	"context"
	"encoding/json"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type hpaAdapter struct{}

func (hpaAdapter) Kind() string        { return "hpas" }
func (hpaAdapter) APIResource() string { return "horizontalpodautoscalers" }
func (hpaAdapter) DisplayName() string { return "HorizontalPodAutoscaler" }
func (hpaAdapter) ClusterScoped() bool { return false }

func (hpaAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*autoscalingv2.HorizontalPodAutoscaler
	var err error
	if ns != "" {
		items, err = inf.HorizontalPodAutoscalers().HorizontalPodAutoscalers(ns).List(sel)
	} else {
		items, err = inf.HorizontalPodAutoscalers().List(sel)
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

func (hpaAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.HorizontalPodAutoscalers().HorizontalPodAutoscalers(ns).Get(name)
}

func (hpaAdapter) Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj autoscalingv2.HorizontalPodAutoscaler
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Create(ctx, &obj, metav1.CreateOptions{})
}

func (hpaAdapter) Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj autoscalingv2.HorizontalPodAutoscaler
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Update(ctx, &obj, metav1.UpdateOptions{})
}

func (hpaAdapter) Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	return cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func init() { Register(hpaAdapter{}) }
