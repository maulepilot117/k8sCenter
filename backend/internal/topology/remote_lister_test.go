package topology

import (
	"context"
	"errors"
	"log/slog"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

func om(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

// Each method returns exactly the namespace's objects of its kind, as the
// pointer slice type InformerLister returns.
func TestRemoteLister_ListsEachKindInNamespace(t *testing.T) {
	var objs []runtime.Object
	for _, ns := range []string{"team-a", "team-b"} {
		objs = append(objs,
			&corev1.Pod{ObjectMeta: om(ns, "pod-"+ns)},
			&corev1.Service{ObjectMeta: om(ns, "svc-"+ns)},
			&appsv1.Deployment{ObjectMeta: om(ns, "dep-"+ns)},
			&appsv1.ReplicaSet{ObjectMeta: om(ns, "rs-"+ns)},
			&appsv1.StatefulSet{ObjectMeta: om(ns, "sts-"+ns)},
			&appsv1.DaemonSet{ObjectMeta: om(ns, "ds-"+ns)},
			&batchv1.Job{ObjectMeta: om(ns, "job-"+ns)},
			&batchv1.CronJob{ObjectMeta: om(ns, "cj-"+ns)},
			&networkingv1.Ingress{ObjectMeta: om(ns, "ing-"+ns)},
			&corev1.ConfigMap{ObjectMeta: om(ns, "cm-"+ns)},
			&corev1.PersistentVolumeClaim{ObjectMeta: om(ns, "pvc-"+ns)},
			&autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: om(ns, "hpa-"+ns)},
		)
	}
	l := NewRemoteLister(kfake.NewSimpleClientset(objs...), slog.Default())
	ctx := context.Background()

	names := func(prefix string, n int, err error, name func(int) string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", prefix, err)
		}
		if n != 1 || name(0) != prefix+"-team-a" {
			t.Errorf("%s: got %d items, want only %s-team-a", prefix, n, prefix)
		}
	}

	pods, err := l.ListPods(ctx, "team-a")
	names("pod", len(pods), err, func(i int) string { return pods[i].Name })
	svcs, err := l.ListServices(ctx, "team-a")
	names("svc", len(svcs), err, func(i int) string { return svcs[i].Name })
	deps, err := l.ListDeployments(ctx, "team-a")
	names("dep", len(deps), err, func(i int) string { return deps[i].Name })
	rss, err := l.ListReplicaSets(ctx, "team-a")
	names("rs", len(rss), err, func(i int) string { return rss[i].Name })
	stss, err := l.ListStatefulSets(ctx, "team-a")
	names("sts", len(stss), err, func(i int) string { return stss[i].Name })
	dss, err := l.ListDaemonSets(ctx, "team-a")
	names("ds", len(dss), err, func(i int) string { return dss[i].Name })
	jobs, err := l.ListJobs(ctx, "team-a")
	names("job", len(jobs), err, func(i int) string { return jobs[i].Name })
	cjs, err := l.ListCronJobs(ctx, "team-a")
	names("cj", len(cjs), err, func(i int) string { return cjs[i].Name })
	ings, err := l.ListIngresses(ctx, "team-a")
	names("ing", len(ings), err, func(i int) string { return ings[i].Name })
	cms, err := l.ListConfigMaps(ctx, "team-a")
	names("cm", len(cms), err, func(i int) string { return cms[i].Name })
	pvcs, err := l.ListPVCs(ctx, "team-a")
	names("pvc", len(pvcs), err, func(i int) string { return pvcs[i].Name })
	hpas, err := l.ListHPAs(ctx, "team-a")
	names("hpa", len(hpas), err, func(i int) string { return hpas[i].Name })
}

func listActions(cs *kfake.Clientset, resource string) int {
	n := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

// One request lists a kind once per namespace, however many readers ask.
func TestRemoteLister_MemoizesPerKindAndNamespace(t *testing.T) {
	cs := kfake.NewSimpleClientset(&corev1.Pod{ObjectMeta: om("team-a", "p")})
	l := NewRemoteLister(cs, slog.Default())
	ctx := context.Background()

	first, err := l.ListPods(ctx, "team-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.ListPods(ctx, "team-a")
	if err != nil {
		t.Fatal(err)
	}
	if n := listActions(cs, "pods"); n != 1 {
		t.Errorf("pod list actions = %d, want 1", n)
	}
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Errorf("memoized result differs: %v vs %v", first, second)
	}

	if _, err := l.ListPods(ctx, "team-b"); err != nil {
		t.Fatal(err)
	}
	if n := listActions(cs, "pods"); n != 2 {
		t.Errorf("pod list actions after a second namespace = %d, want 2", n)
	}
}

// A list still carrying a continue token at the page cap yields the items
// read and a *TruncatedError naming the kind.
func TestRemoteLister_TruncationReturnsItemsAndTypedError(t *testing.T) {
	cs := kfake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		items := make([]corev1.Pod, k8s.RemoteListPageSize)
		return true, &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "more"}, Items: items}, nil
	})
	l := NewRemoteLister(cs, slog.Default())

	pods, err := l.ListPods(context.Background(), "team-a")

	var te *TruncatedError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want *TruncatedError", err)
	}
	want := k8s.RemoteListPageSize * k8s.RemoteListMaxPages
	if te.Kind != "pods" || te.Read != want || len(pods) != want {
		t.Errorf("truncation = %+v with %d items, want pods/%d", te, len(pods), want)
	}
	if n := listActions(cs, "pods"); n != k8s.RemoteListMaxPages {
		t.Errorf("pod list pages = %d, want %d", n, k8s.RemoteListMaxPages)
	}
}

func TestRemoteLister_ForbiddenPassesThrough(t *testing.T) {
	cs := kfake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: om("team-a", "d")})
	cs.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("denied"))
	})
	l := NewRemoteLister(cs, slog.Default())

	items, err := l.ListDeployments(context.Background(), "team-a")

	if !apierrors.IsForbidden(err) {
		t.Fatalf("err = %v, want forbidden", err)
	}
	if items != nil {
		t.Errorf("items = %v, want none on a refusal", items)
	}
}

// blockingList is a remoteList list function that counts its calls and
// blocks until release is closed. remoteList is driven directly because the
// fake clientset runs every reactor under one lock, which would serialise
// the very concurrency these tests observe.
func blockingList(calls *atomic.Int32, release <-chan struct{}, name string) func(context.Context, metav1.ListOptions) ([]corev1.Pod, string, error) {
	return func(context.Context, metav1.ListOptions) ([]corev1.Pod, string, error) {
		calls.Add(1)
		<-release
		return []corev1.Pod{{ObjectMeta: om("team-a", name)}}, "", nil
	}
}

// Concurrent readers of one key share a single list, and a reader of another
// key is not held behind it.
func TestRemoteLister_ConcurrentReadersShareOneList(t *testing.T) {
	l := NewRemoteLister(kfake.NewSimpleClientset(), slog.Default())
	ctx := context.Background()
	release := make(chan struct{})
	var calls atomic.Int32

	const readers = 8
	var wg sync.WaitGroup
	results := make([][]*corev1.Pod, readers)
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pods, err := remoteList(ctx, l, KindPods, "team-a", blockingList(&calls, release, "p"))
			if err != nil {
				t.Errorf("reader %d: %v", i, err)
			}
			results[i] = pods
		}()
	}

	// While the team-a list is held, another key's list still completes.
	var otherCalls atomic.Int32
	open := make(chan struct{})
	close(open)
	if pods, err := remoteList(ctx, l, KindPods, "team-b", blockingList(&otherCalls, open, "q")); err != nil || len(pods) != 1 {
		t.Errorf("team-b = %v, %v while team-a was blocked", pods, err)
	}
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Errorf("team-a lists = %d, want 1 shared by %d readers", n, readers)
	}
	for i, pods := range results {
		if len(pods) != 1 || pods[0] != results[0][0] {
			t.Errorf("reader %d got %v, want the shared result", i, pods)
		}
	}
}

// A reader waiting on another's list gives up when its own context ends, and
// the list's result is still remembered for later readers.
func TestRemoteLister_WaiterHonoursItsContext(t *testing.T) {
	l := NewRemoteLister(kfake.NewSimpleClientset(), slog.Default())
	release := make(chan struct{})
	var calls atomic.Int32
	list := blockingList(&calls, release, "p")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = remoteList(context.Background(), l, KindPods, "team-a", list)
	}()
	for calls.Load() == 0 {
		goruntime.Gosched()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := remoteList(ctx, l, KindPods, "team-a", list); !errors.Is(err, context.Canceled) {
		t.Errorf("waiter err = %v, want context.Canceled", err)
	}
	close(release)
	<-done

	if pods, err := remoteList(context.Background(), l, KindPods, "team-a", list); err != nil || len(pods) != 1 || calls.Load() != 1 {
		t.Errorf("after the list finished: %v, %v with %d lists, want the remembered pod from 1 list", pods, err, calls.Load())
	}
}

// A list that panics still releases its waiters, with an error.
func TestRemoteLister_PanicReleasesWaiters(t *testing.T) {
	l := NewRemoteLister(kfake.NewSimpleClientset(), slog.Default())
	started, release := make(chan struct{}), make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		_, _ = remoteList(context.Background(), l, KindPods, "team-a", func(context.Context, metav1.ListOptions) ([]corev1.Pod, string, error) {
			close(started)
			<-release
			panic("boom")
		})
	}()
	<-started

	errc := make(chan error, 1)
	go func() {
		_, err := remoteList(context.Background(), l, KindPods, "team-a", blockingList(new(atomic.Int32), nil, "unused"))
		errc <- err
	}()
	close(release)
	if err := <-errc; !errors.Is(err, errListAborted) {
		t.Errorf("waiter err = %v, want errListAborted", err)
	}
}

// A remote graph lists every allowed kind exactly once: the prefetch warms
// the memo and the sequential build reads it back.
func TestRemoteLister_PrefetchedGraphListsEachKindOnce(t *testing.T) {
	cs := kfake.NewSimpleClientset(&corev1.Pod{ObjectMeta: om("team-a", "p")})
	b := NewBuilder(NewRemoteLister(cs, slog.Default()), nil, slog.Default())

	graph, err := b.BuildNamespaceGraph(context.Background(), "team-a", &auth.User{KubernetesUsername: "u"}, resources.NewAlwaysAllowAccessChecker())
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 1 || graph.Nodes[0].Name != "p" {
		t.Errorf("nodes = %+v, want pod p", graph.Nodes)
	}
	for _, kind := range graphKinds {
		if n := listActions(cs, kind); n != 1 {
			t.Errorf("%s lists = %d, want 1", kind, n)
		}
	}
}
