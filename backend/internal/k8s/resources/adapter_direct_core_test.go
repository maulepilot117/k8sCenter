package resources

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type directCoreReader interface {
	ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error)
	GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error)
}

func directCoreName(t *testing.T, obj any) string {
	t.Helper()
	m, ok := obj.(metav1.Object)
	if !ok {
		t.Fatalf("item %T is not a metav1.Object", obj)
	}
	return m.GetName()
}

func TestDirectCoreAdapters(t *testing.T) {
	meta := func(ns, name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	tests := []struct {
		kind          string
		clusterScoped bool
		objA, objB    runtime.Object
		// wantType asserts the concrete pointer type of a returned item.
		wantType func(any) bool
	}{
		{"pods", false, &corev1.Pod{ObjectMeta: meta("a", "one")}, &corev1.Pod{ObjectMeta: meta("b", "two")},
			func(v any) bool { _, ok := v.(*corev1.Pod); return ok }},
		{"configmaps", false, &corev1.ConfigMap{ObjectMeta: meta("a", "one")}, &corev1.ConfigMap{ObjectMeta: meta("b", "two")},
			func(v any) bool { _, ok := v.(*corev1.ConfigMap); return ok }},
		{"serviceaccounts", false, &corev1.ServiceAccount{ObjectMeta: meta("a", "one")}, &corev1.ServiceAccount{ObjectMeta: meta("b", "two")},
			func(v any) bool { _, ok := v.(*corev1.ServiceAccount); return ok }},
		{"events", false, &corev1.Event{ObjectMeta: meta("a", "one")}, &corev1.Event{ObjectMeta: meta("b", "two")},
			func(v any) bool { _, ok := v.(*corev1.Event); return ok }},
		{"resourcequotas", false, &corev1.ResourceQuota{ObjectMeta: meta("a", "one")}, &corev1.ResourceQuota{ObjectMeta: meta("b", "two")},
			func(v any) bool { _, ok := v.(*corev1.ResourceQuota); return ok }},
		{"limitranges", false, &corev1.LimitRange{ObjectMeta: meta("a", "one")}, &corev1.LimitRange{ObjectMeta: meta("b", "two")},
			func(v any) bool { _, ok := v.(*corev1.LimitRange); return ok }},
		{"nodes", true, &corev1.Node{ObjectMeta: meta("", "one")}, &corev1.Node{ObjectMeta: meta("", "two")},
			func(v any) bool { _, ok := v.(*corev1.Node); return ok }},
		{"namespaces", true, &corev1.Namespace{ObjectMeta: meta("", "one")}, &corev1.Namespace{ObjectMeta: meta("", "two")},
			func(v any) bool { _, ok := v.(*corev1.Namespace); return ok }},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			ctx := context.Background()
			cs := fake.NewSimpleClientset(tc.objA, tc.objB)
			r, ok := GetAdapter(tc.kind).(directCoreReader)
			if !ok {
				t.Fatalf("registered %s adapter does not implement ListDirect/GetDirect", tc.kind)
			}

			ns := "a"
			if tc.clusterScoped {
				ns = "ignored"
			}
			items, _, err := r.ListDirect(ctx, cs, ns, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect: %v", err)
			}
			wantN := 1
			if tc.clusterScoped {
				wantN = 2
			}
			if len(items) != wantN {
				t.Fatalf("ListDirect(%q) = %d items, want %d", ns, len(items), wantN)
			}
			for _, it := range items {
				if !tc.wantType(it) {
					t.Fatalf("item has type %T", it)
				}
			}
			if !tc.clusterScoped && directCoreName(t, items[0]) != "one" {
				t.Fatalf("namespace a list returned %q", directCoreName(t, items[0]))
			}

			all, _, err := r.ListDirect(ctx, cs, "", metav1.ListOptions{})
			if err != nil || len(all) != 2 {
				t.Fatalf("ListDirect(all) = %d items, err %v; want 2", len(all), err)
			}

			got, err := r.GetDirect(ctx, cs, "a", "one")
			if err != nil {
				t.Fatalf("GetDirect: %v", err)
			}
			if directCoreName(t, got) != "one" {
				t.Fatalf("GetDirect returned %q", directCoreName(t, got))
			}
			if _, err := r.GetDirect(ctx, cs, "a", "missing"); !apierrors.IsNotFound(err) {
				t.Fatalf("GetDirect(missing) err = %v, want NotFound", err)
			}
		})
	}
}

func TestDirectCoreContinueTokenPassedThrough(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "next"}}, nil
	})
	r, ok := GetAdapter("pods").(directCoreReader)
	if !ok {
		t.Fatal("pods adapter does not implement ListDirect/GetDirect")
	}
	_, token, err := r.ListDirect(context.Background(), cs, "a", metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if token != "next" {
		t.Fatalf("continue token = %q, want next", token)
	}
}

func TestDirectCoreSecretsNeverServed(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r, ok := GetAdapter("secrets").(directCoreReader)
	if !ok {
		t.Fatal("secrets adapter does not implement ListDirect/GetDirect")
	}
	if _, _, err := r.ListDirect(context.Background(), cs, "a", metav1.ListOptions{}); err != errSecretsNotCached {
		t.Fatalf("ListDirect err = %v, want errSecretsNotCached", err)
	}
	if _, err := r.GetDirect(context.Background(), cs, "a", "x"); err != errSecretsNotCached {
		t.Fatalf("GetDirect err = %v, want errSecretsNotCached", err)
	}
}
