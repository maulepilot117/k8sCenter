package resources

import (
	"context"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type directRBACReader interface {
	ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error)
	GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error)
}

func directRBACMeta(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

// directRBACName returns the metadata name of a typed pointer, failing the
// test for any other shape.
func directRBACName(t *testing.T, kind string, item any) string {
	t.Helper()
	switch v := item.(type) {
	case *rbacv1.Role:
		return v.Name
	case *rbacv1.ClusterRole:
		return v.Name
	case *rbacv1.RoleBinding:
		return v.Name
	case *rbacv1.ClusterRoleBinding:
		return v.Name
	case *admissionv1.ValidatingWebhookConfiguration:
		return v.Name
	case *admissionv1.MutatingWebhookConfiguration:
		return v.Name
	}
	t.Fatalf("%s: unexpected item type %T", kind, item)
	return ""
}

func TestDirectRBACAdapters(t *testing.T) {
	cases := []struct {
		kind    string
		scoped  bool // cluster-scoped
		objs    []runtime.Object
		nsA     string // namespace of the "a" object
		nameA   string
		nameB   string
		missing string
	}{
		{"roles", false, []runtime.Object{
			&rbacv1.Role{ObjectMeta: directRBACMeta("a", "ra")}, &rbacv1.Role{ObjectMeta: directRBACMeta("b", "rb")}}, "a", "ra", "rb", "nope"},
		{"rolebindings", false, []runtime.Object{
			&rbacv1.RoleBinding{ObjectMeta: directRBACMeta("a", "rba")}, &rbacv1.RoleBinding{ObjectMeta: directRBACMeta("b", "rbb")}}, "a", "rba", "rbb", "nope"},
		{"clusterroles", true, []runtime.Object{
			&rbacv1.ClusterRole{ObjectMeta: directRBACMeta("", "cra")}, &rbacv1.ClusterRole{ObjectMeta: directRBACMeta("", "crb")}}, "ignored", "cra", "crb", "nope"},
		{"clusterrolebindings", true, []runtime.Object{
			&rbacv1.ClusterRoleBinding{ObjectMeta: directRBACMeta("", "crba")}, &rbacv1.ClusterRoleBinding{ObjectMeta: directRBACMeta("", "crbb")}}, "ignored", "crba", "crbb", "nope"},
		{"validatingwebhookconfigurations", true, []runtime.Object{
			&admissionv1.ValidatingWebhookConfiguration{ObjectMeta: directRBACMeta("", "va")}, &admissionv1.ValidatingWebhookConfiguration{ObjectMeta: directRBACMeta("", "vb")}}, "ignored", "va", "vb", "nope"},
		{"mutatingwebhookconfigurations", true, []runtime.Object{
			&admissionv1.MutatingWebhookConfiguration{ObjectMeta: directRBACMeta("", "ma")}, &admissionv1.MutatingWebhookConfiguration{ObjectMeta: directRBACMeta("", "mb")}}, "ignored", "ma", "mb", "nope"},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			ctx := context.Background()
			cs := fake.NewSimpleClientset(tc.objs...)
			r, ok := GetAdapter(tc.kind).(directRBACReader)
			if !ok {
				t.Fatalf("registered adapter for %s does not implement ListDirect/GetDirect", tc.kind)
			}

			items, _, err := r.ListDirect(ctx, cs, tc.nsA, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect(%q): %v", tc.nsA, err)
			}
			if tc.scoped {
				if len(items) != 2 {
					t.Fatalf("cluster-scoped list = %d items, want 2", len(items))
				}
			} else {
				if len(items) != 1 || directRBACName(t, tc.kind, items[0]) != tc.nameA {
					t.Fatalf("namespace %q list = %v, want only %s", tc.nsA, items, tc.nameA)
				}
			}

			all, _, err := r.ListDirect(ctx, cs, "", metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect(all): %v", err)
			}
			if len(all) != 2 {
				t.Fatalf("all-namespaces list = %d items, want 2", len(all))
			}

			ns := tc.nsA
			got, err := r.GetDirect(ctx, cs, ns, tc.nameA)
			if err != nil {
				t.Fatalf("GetDirect: %v", err)
			}
			if n := directRBACName(t, tc.kind, got); n != tc.nameA {
				t.Fatalf("GetDirect name = %q, want %q", n, tc.nameA)
			}

			_, err = r.GetDirect(ctx, cs, ns, tc.missing)
			if !apierrors.IsNotFound(err) {
				t.Fatalf("GetDirect missing = %v, want NotFound", err)
			}
		})
	}
}

func TestDirectRBACContinueTokenPassedThrough(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "roles", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &rbacv1.RoleList{
			ListMeta: metav1.ListMeta{Continue: "next"},
			Items:    []rbacv1.Role{{ObjectMeta: directRBACMeta("a", "ra")}},
		}, nil
	})
	r := GetAdapter("roles").(directRBACReader)
	items, cont, err := r.ListDirect(context.Background(), cs, "a", metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cont != "next" || len(items) != 1 {
		t.Fatalf("continue = %q, items = %d; want \"next\", 1", cont, len(items))
	}
}
