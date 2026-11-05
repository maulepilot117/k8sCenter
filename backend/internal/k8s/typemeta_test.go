package k8s

import (
	"io"
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStampTypeMeta(t *testing.T) {
	cases := []struct {
		name string
		obj  runtime.Object
		want schema.GroupVersionKind
	}{
		{"apps group", &appsv1.Deployment{}, appsv1.SchemeGroupVersion.WithKind("Deployment")},
		{"core group", &corev1.ConfigMap{}, corev1.SchemeGroupVersion.WithKind("ConfigMap")},
		{"already set is kept", &corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v9", Kind: "Odd"}},
			schema.GroupVersionKind{Version: "v9", Kind: "Odd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			StampTypeMeta(tc.obj)
			if got := tc.obj.GetObjectKind().GroupVersionKind(); got != tc.want {
				t.Errorf("GVK = %v, want %v", got, tc.want)
			}
		})
	}
}

// A type the scheme does not know keeps its empty TypeMeta and does not panic.
func TestStampTypeMeta_UnknownTypeLeftAlone(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	StampTypeMeta(u)
	if gvk := u.GroupVersionKind(); !gvk.Empty() {
		t.Errorf("GVK = %v, want empty", gvk)
	}
	StampTypeMeta(nil)
}

// An informer event carries a stamped copy, and the cache's own object is not
// written to.
func TestEmitEvent_StampsCopyNotOriginal(t *testing.T) {
	m := NewInformerManager(fake.NewSimpleClientset(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	orig := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}}

	var got any
	m.emitEvent(func(_, _, _, _ string, obj any) { got = obj }, "MODIFIED", "deployments", orig)

	dep, ok := got.(*appsv1.Deployment)
	if !ok {
		t.Fatalf("event object = %T, want *appsv1.Deployment", got)
	}
	if dep.APIVersion != "apps/v1" || dep.Kind != "Deployment" {
		t.Errorf("event object apiVersion=%q kind=%q, want apps/v1 Deployment", dep.APIVersion, dep.Kind)
	}
	if gvk := orig.GroupVersionKind(); !gvk.Empty() {
		t.Errorf("original object was mutated: GVK = %v", gvk)
	}
}
