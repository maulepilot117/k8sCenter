package resources

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type directNetStorReader interface {
	ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error)
	GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error)
}

func directNetStorMeta(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

func directNetStorName(t *testing.T, obj any) string {
	t.Helper()
	switch o := obj.(type) {
	case *corev1.Service:
		return o.Name
	case *corev1.Endpoints:
		return o.Name
	case *discoveryv1.EndpointSlice:
		return o.Name
	case *networkingv1.Ingress:
		return o.Name
	case *networkingv1.NetworkPolicy:
		return o.Name
	case *corev1.PersistentVolumeClaim:
		return o.Name
	case *corev1.PersistentVolume:
		return o.Name
	case *storagev1.StorageClass:
		return o.Name
	}
	t.Fatalf("unexpected item type %T", obj)
	return ""
}

func TestDirectNetStorAdapters(t *testing.T) {
	tests := []struct {
		kind          string
		clusterScoped bool
		objs          []runtime.Object
		nameA, nameB  string
	}{
		{"services", false, []runtime.Object{
			&corev1.Service{ObjectMeta: directNetStorMeta("a", "x")},
			&corev1.Service{ObjectMeta: directNetStorMeta("b", "y")}}, "x", "y"},
		{"endpoints", false, []runtime.Object{
			&corev1.Endpoints{ObjectMeta: directNetStorMeta("a", "x")},
			&corev1.Endpoints{ObjectMeta: directNetStorMeta("b", "y")}}, "x", "y"},
		{"endpointslices", false, []runtime.Object{
			&discoveryv1.EndpointSlice{ObjectMeta: directNetStorMeta("a", "x")},
			&discoveryv1.EndpointSlice{ObjectMeta: directNetStorMeta("b", "y")}}, "x", "y"},
		{"ingresses", false, []runtime.Object{
			&networkingv1.Ingress{ObjectMeta: directNetStorMeta("a", "x")},
			&networkingv1.Ingress{ObjectMeta: directNetStorMeta("b", "y")}}, "x", "y"},
		{"networkpolicies", false, []runtime.Object{
			&networkingv1.NetworkPolicy{ObjectMeta: directNetStorMeta("a", "x")},
			&networkingv1.NetworkPolicy{ObjectMeta: directNetStorMeta("b", "y")}}, "x", "y"},
		{"pvcs", false, []runtime.Object{
			&corev1.PersistentVolumeClaim{ObjectMeta: directNetStorMeta("a", "x")},
			&corev1.PersistentVolumeClaim{ObjectMeta: directNetStorMeta("b", "y")}}, "x", "y"},
		{"pvs", true, []runtime.Object{
			&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "x"}},
			&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "y"}}}, "x", "y"},
		{"storageclasses", true, []runtime.Object{
			&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "x"}},
			&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "y"}}}, "x", "y"},
	}
	ctx := context.Background()
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			ad, ok := GetAdapter(tc.kind).(directNetStorReader)
			if !ok {
				t.Fatalf("adapter %q does not implement ListDirect/GetDirect", tc.kind)
			}
			cs := fake.NewSimpleClientset(tc.objs...)

			scoped := "a"
			if tc.clusterScoped {
				scoped = "ignored"
			}
			items, _, err := ad.ListDirect(ctx, cs, scoped, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect(%q): %v", scoped, err)
			}
			if tc.clusterScoped {
				if len(items) != 2 {
					t.Fatalf("cluster-scoped ListDirect returned %d items, want 2", len(items))
				}
			} else {
				if len(items) != 1 || directNetStorName(t, items[0]) != tc.nameA {
					t.Fatalf("ListDirect(a) = %d items, want only %q", len(items), tc.nameA)
				}
			}

			all, _, err := ad.ListDirect(ctx, cs, "", metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect(all): %v", err)
			}
			if len(all) != 2 {
				t.Fatalf("ListDirect(\"\") returned %d items, want 2", len(all))
			}

			ns := "a"
			if tc.clusterScoped {
				ns = ""
			}
			got, err := ad.GetDirect(ctx, cs, ns, tc.nameA)
			if err != nil {
				t.Fatalf("GetDirect: %v", err)
			}
			if n := directNetStorName(t, got); n != tc.nameA {
				t.Fatalf("GetDirect name = %q, want %q", n, tc.nameA)
			}
			if _, err := ad.GetDirect(ctx, cs, ns, "missing"); !apierrors.IsNotFound(err) {
				t.Fatalf("GetDirect(missing) err = %v, want NotFound", err)
			}
		})
	}
}

func TestDirectNetStorContinuePassthrough(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.ServiceList{ListMeta: metav1.ListMeta{Continue: "next"}}, nil
	})
	ad := GetAdapter("services").(directNetStorReader)
	_, cont, err := ad.ListDirect(context.Background(), cs, "a", metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cont != "next" {
		t.Fatalf("continue = %q, want next", cont)
	}
}
