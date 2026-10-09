package resources

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type directReader interface {
	ListDirect(ctx context.Context, cs kubernetes.Interface, ns string, opts metav1.ListOptions) ([]any, string, error)
	GetDirect(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error)
}

func directMeta(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

func TestDirectWorkloadAdapters(t *testing.T) {
	tests := []struct {
		kind  string
		objA  runtime.Object
		objB  runtime.Object
		check func(any) string // asserts concrete pointer type, returns name
	}{
		{"deployments", &appsv1.Deployment{ObjectMeta: directMeta("a", "dep-a")}, &appsv1.Deployment{ObjectMeta: directMeta("b", "dep-b")},
			func(o any) string { return o.(*appsv1.Deployment).Name }},
		{"statefulsets", &appsv1.StatefulSet{ObjectMeta: directMeta("a", "sts-a")}, &appsv1.StatefulSet{ObjectMeta: directMeta("b", "sts-b")},
			func(o any) string { return o.(*appsv1.StatefulSet).Name }},
		{"daemonsets", &appsv1.DaemonSet{ObjectMeta: directMeta("a", "ds-a")}, &appsv1.DaemonSet{ObjectMeta: directMeta("b", "ds-b")},
			func(o any) string { return o.(*appsv1.DaemonSet).Name }},
		{"replicasets", &appsv1.ReplicaSet{ObjectMeta: directMeta("a", "rs-a")}, &appsv1.ReplicaSet{ObjectMeta: directMeta("b", "rs-b")},
			func(o any) string { return o.(*appsv1.ReplicaSet).Name }},
		{"jobs", &batchv1.Job{ObjectMeta: directMeta("a", "job-a")}, &batchv1.Job{ObjectMeta: directMeta("b", "job-b")},
			func(o any) string { return o.(*batchv1.Job).Name }},
		{"cronjobs", &batchv1.CronJob{ObjectMeta: directMeta("a", "cj-a")}, &batchv1.CronJob{ObjectMeta: directMeta("b", "cj-b")},
			func(o any) string { return o.(*batchv1.CronJob).Name }},
		{"hpas", &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: directMeta("a", "hpa-a")}, &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: directMeta("b", "hpa-b")},
			func(o any) string { return o.(*autoscalingv2.HorizontalPodAutoscaler).Name }},
		{"pdbs", &policyv1.PodDisruptionBudget{ObjectMeta: directMeta("a", "pdb-a")}, &policyv1.PodDisruptionBudget{ObjectMeta: directMeta("b", "pdb-b")},
			func(o any) string { return o.(*policyv1.PodDisruptionBudget).Name }},
	}
	ctx := context.Background()
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			a, ok := GetAdapter(tc.kind).(directReader)
			if !ok {
				t.Fatalf("adapter %q does not implement ListDirect/GetDirect", tc.kind)
			}
			cs := fake.NewSimpleClientset(tc.objA, tc.objB)

			items, _, err := a.ListDirect(ctx, cs, "a", metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect(a): %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("ListDirect(a) = %d items, want 1", len(items))
			}
			nameA := tc.check(items[0])

			all, _, err := a.ListDirect(ctx, cs, "", metav1.ListOptions{})
			if err != nil {
				t.Fatalf("ListDirect(all): %v", err)
			}
			if len(all) != 2 {
				t.Fatalf("ListDirect(all) = %d items, want 2", len(all))
			}

			got, err := a.GetDirect(ctx, cs, "a", nameA)
			if err != nil {
				t.Fatalf("GetDirect: %v", err)
			}
			if n := tc.check(got); n != nameA {
				t.Fatalf("GetDirect name = %q, want %q", n, nameA)
			}

			if _, err := a.GetDirect(ctx, cs, "a", "missing"); !apierrors.IsNotFound(err) {
				t.Fatalf("GetDirect(missing) err = %v, want NotFound", err)
			}
		})
	}
}

func TestDirectListPassesContinueToken(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &appsv1.DeploymentList{
			ListMeta: metav1.ListMeta{Continue: "next"},
			Items:    []appsv1.Deployment{{ObjectMeta: directMeta("a", "dep-a")}},
		}, nil
	})
	a := GetAdapter("deployments").(directReader)
	items, cont, err := a.ListDirect(context.Background(), cs, "a", metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cont != "next" || len(items) != 1 {
		t.Fatalf("got %d items, continue %q; want 1, \"next\"", len(items), cont)
	}
}
