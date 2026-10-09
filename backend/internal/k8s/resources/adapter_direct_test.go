package resources

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// directSeedRows maps every registered kind except secrets to a constructor
// that builds a minimal object from the given ObjectMeta. Secrets are refused
// by the direct path and covered by TestAdapterDirect_SecretsRefused.
var directSeedRows = []struct {
	kind string
	mk   func(metav1.ObjectMeta) runtime.Object
}{
	{"deployments", func(m metav1.ObjectMeta) runtime.Object { return &appsv1.Deployment{ObjectMeta: m} }},
	{"statefulsets", func(m metav1.ObjectMeta) runtime.Object { return &appsv1.StatefulSet{ObjectMeta: m} }},
	{"daemonsets", func(m metav1.ObjectMeta) runtime.Object { return &appsv1.DaemonSet{ObjectMeta: m} }},
	{"replicasets", func(m metav1.ObjectMeta) runtime.Object { return &appsv1.ReplicaSet{ObjectMeta: m} }},
	{"jobs", func(m metav1.ObjectMeta) runtime.Object { return &batchv1.Job{ObjectMeta: m} }},
	{"cronjobs", func(m metav1.ObjectMeta) runtime.Object { return &batchv1.CronJob{ObjectMeta: m} }},
	{"hpas", func(m metav1.ObjectMeta) runtime.Object { return &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: m} }},
	{"pdbs", func(m metav1.ObjectMeta) runtime.Object { return &policyv1.PodDisruptionBudget{ObjectMeta: m} }},
	{"pods", func(m metav1.ObjectMeta) runtime.Object { return &corev1.Pod{ObjectMeta: m} }},
	{"nodes", func(m metav1.ObjectMeta) runtime.Object { return &corev1.Node{ObjectMeta: m} }},
	{"namespaces", func(m metav1.ObjectMeta) runtime.Object { return &corev1.Namespace{ObjectMeta: m} }},
	{"configmaps", func(m metav1.ObjectMeta) runtime.Object { return &corev1.ConfigMap{ObjectMeta: m} }},
	{"serviceaccounts", func(m metav1.ObjectMeta) runtime.Object { return &corev1.ServiceAccount{ObjectMeta: m} }},
	{"events", func(m metav1.ObjectMeta) runtime.Object { return &corev1.Event{ObjectMeta: m} }},
	{"resourcequotas", func(m metav1.ObjectMeta) runtime.Object { return &corev1.ResourceQuota{ObjectMeta: m} }},
	{"limitranges", func(m metav1.ObjectMeta) runtime.Object { return &corev1.LimitRange{ObjectMeta: m} }},
	{"services", func(m metav1.ObjectMeta) runtime.Object { return &corev1.Service{ObjectMeta: m} }},
	{"endpoints", func(m metav1.ObjectMeta) runtime.Object { return &corev1.Endpoints{ObjectMeta: m} }},
	{"endpointslices", func(m metav1.ObjectMeta) runtime.Object { return &discoveryv1.EndpointSlice{ObjectMeta: m} }},
	{"ingresses", func(m metav1.ObjectMeta) runtime.Object { return &networkingv1.Ingress{ObjectMeta: m} }},
	{"networkpolicies", func(m metav1.ObjectMeta) runtime.Object { return &networkingv1.NetworkPolicy{ObjectMeta: m} }},
	{"pvcs", func(m metav1.ObjectMeta) runtime.Object { return &corev1.PersistentVolumeClaim{ObjectMeta: m} }},
	{"pvs", func(m metav1.ObjectMeta) runtime.Object { return &corev1.PersistentVolume{ObjectMeta: m} }},
	{"storageclasses", func(m metav1.ObjectMeta) runtime.Object { return &storagev1.StorageClass{ObjectMeta: m} }},
	{"roles", func(m metav1.ObjectMeta) runtime.Object { return &rbacv1.Role{ObjectMeta: m} }},
	{"clusterroles", func(m metav1.ObjectMeta) runtime.Object { return &rbacv1.ClusterRole{ObjectMeta: m} }},
	{"rolebindings", func(m metav1.ObjectMeta) runtime.Object { return &rbacv1.RoleBinding{ObjectMeta: m} }},
	{"clusterrolebindings", func(m metav1.ObjectMeta) runtime.Object { return &rbacv1.ClusterRoleBinding{ObjectMeta: m} }},
	{"validatingwebhookconfigurations", func(m metav1.ObjectMeta) runtime.Object {
		return &admissionv1.ValidatingWebhookConfiguration{ObjectMeta: m}
	}},
	{"mutatingwebhookconfigurations", func(m metav1.ObjectMeta) runtime.Object {
		return &admissionv1.MutatingWebhookConfiguration{ObjectMeta: m}
	}},
}

func TestAdapterDirect(t *testing.T) {
	ctx := context.Background()
	const nameA, nameB = "one", "two"
	for _, tc := range directSeedRows {
		t.Run(tc.kind, func(t *testing.T) {
			ad := GetAdapter(tc.kind)
			if ad == nil {
				t.Fatalf("no adapter registered for %q", tc.kind)
			}
			clusterScoped := ad.ClusterScoped()
			nsA, nsB := "a", "b"
			if clusterScoped {
				nsA, nsB = "", ""
			}
			objA := tc.mk(metav1.ObjectMeta{Namespace: nsA, Name: nameA})
			objB := tc.mk(metav1.ObjectMeta{Namespace: nsB, Name: nameB})
			cs := fake.NewSimpleClientset(objA, objB)

			listNS, wantN := "a", 1
			if clusterScoped {
				listNS, wantN = "ignored", 2
			}
			items, _, err := ad.ListDirect(ctx, cs, listNS, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect(%q): %v", listNS, err)
			}
			if len(items) != wantN {
				t.Fatalf("ListDirect(%q) = %d items, want %d", listNS, len(items), wantN)
			}
			if !clusterScoped && directItemName(t, items[0]) != nameA {
				t.Fatalf("ListDirect(a) returned %q, want %q", directItemName(t, items[0]), nameA)
			}

			all, _, err := ad.ListDirect(ctx, cs, "", metav1.ListOptions{})
			if err != nil || len(all) != 2 {
				t.Fatalf("ListDirect(all) = %d items, err %v; want 2", len(all), err)
			}
			wantType := reflect.TypeOf(objA)
			for _, it := range all {
				if got := reflect.TypeOf(it); got != wantType {
					t.Fatalf("item type = %v, want %v", got, wantType)
				}
			}

			getNS := "a"
			if clusterScoped {
				getNS = ""
			}
			got, err := ad.GetDirect(ctx, cs, getNS, nameA)
			if err != nil {
				t.Fatalf("GetDirect: %v", err)
			}
			if n := directItemName(t, got); n != nameA {
				t.Fatalf("GetDirect name = %q, want %q", n, nameA)
			}
			if _, err := ad.GetDirect(ctx, cs, getNS, "missing"); !apierrors.IsNotFound(err) {
				t.Fatalf("GetDirect(missing) err = %v, want NotFound", err)
			}
		})
	}
}

func directItemName(t *testing.T, obj any) string {
	t.Helper()
	m, ok := obj.(metav1.Object)
	if !ok {
		t.Fatalf("item %T is not a metav1.Object", obj)
	}
	return m.GetName()
}

// A new adapter without a table row (or a stale row) fails here.
func TestAdapterDirect_CoversEveryRegisteredKind(t *testing.T) {
	var have, want []string
	for _, r := range directSeedRows {
		have = append(have, r.kind)
	}
	for _, k := range RegisteredKinds() {
		if k != "secrets" {
			want = append(want, k)
		}
	}
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		t.Fatalf("TestAdapterDirect rows = %v\nregistered kinds (minus secrets) = %v", have, want)
	}
}

func TestAdapterDirect_ContinueTokenPassedThrough(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "next"}}, nil
	})
	_, token, err := GetAdapter("pods").ListDirect(context.Background(), cs, "a", metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if token != "next" {
		t.Fatalf("continue token = %q, want next", token)
	}
}

func TestAdapterDirect_SecretsRefused(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ad := GetAdapter("secrets")
	if _, _, err := ad.ListDirect(context.Background(), cs, "a", metav1.ListOptions{}); !errors.Is(err, errSecretsNotCached) {
		t.Fatalf("ListDirect err = %v, want errSecretsNotCached", err)
	}
	if _, err := ad.GetDirect(context.Background(), cs, "a", "x"); !errors.Is(err, errSecretsNotCached) {
		t.Fatalf("GetDirect err = %v, want errSecretsNotCached", err)
	}
	if n := len(cs.Actions()); n != 0 {
		t.Fatalf("fake clientset received %d actions, want 0", n)
	}
}
