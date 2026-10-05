package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/diagnostics"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const (
	testNS       = "payments"
	testName     = "web"
	testUID      = "dep-uid-0001"
	testCanary   = "CANARY-7f3a-plaintext-secret"
	testEnvPlain = "PLAIN-ENV-CANARY-91ac"
)

var testUser = &auth.User{ID: "u1", KubernetesUsername: "alice", KubernetesGroups: []string{"dev"}}

// stubSource is a Source whose behaviour is the supplied function.
type stubSource struct {
	id string
	fn func(ctx context.Context, req CaptureRequest) (SourceResult, error)
}

func (s stubSource) ID() string { return s.id }
func (s stubSource) Collect(ctx context.Context, req CaptureRequest) (SourceResult, error) {
	return s.fn(ctx, req)
}

// completeItem is a minimal valid snapshot item a stub source can emit.
func completeItem(id string) Evidence {
	return Evidence{
		EvidenceKind: EvidenceKindObjectSummary,
		Mode:         ModeSnapshot,
		Source:       SourceRef{ClusterID: k8s.LocalClusterID, Resource: "pods", Kind: "Pod", Namespace: testNS, Name: id, UID: "uid-" + id},
		Completeness: CompletenessComplete,
		Payload:      json.RawMessage(`{"kind":"Pod","metadata":{"name":"` + id + `"}}`),
	}
}

func completeSource(id string, items ...Evidence) Source {
	return stubSource{id: id, fn: func(context.Context, CaptureRequest) (SourceResult, error) {
		return SourceResult{Items: items, Completeness: CompletenessComplete}, nil
	}}
}

// blockingSource waits for ctx and returns its error: it models a hung adapter.
func blockingSource(id string) Source {
	return stubSource{id: id, fn: func(ctx context.Context, _ CaptureRequest) (SourceResult, error) {
		<-ctx.Done()
		return SourceResult{}, ctx.Err()
	}}
}

// steadySource completes unless it is handed a context that is already
// over, in which case it fails: it is the sibling that must survive another
// source's trouble. Queued behind that source under MaxConcurrency 1, it
// starts after the trouble happened, so a group whose context a failure
// cancelled (or a source context shared with the trouble) would hand it a
// dead context. It does no timed work, so a slow runner cannot flip it.
func steadySource(id string, items ...Evidence) Source {
	return stubSource{id: id, fn: func(ctx context.Context, _ CaptureRequest) (SourceResult, error) {
		if err := ctx.Err(); err != nil {
			return SourceResult{}, fmt.Errorf("steady source started cancelled: %w", err)
		}
		return SourceResult{Items: items, Completeness: CompletenessComplete}, nil
	}}
}

// serialLimits runs sources one at a time so ordering is deterministic.
func serialLimits() Limits {
	l := testLimits()
	l.MaxConcurrency = 1
	return l
}

// testLimits gives every source a deadline no test runner can reach, so a
// test that asserts what a source fetched (a page count, the newest event)
// never depends on how fast the runner is (go test -race on a loaded CI
// host is several times slower than a local run). A test about a deadline
// sets its own short timeout and drives the expiry deterministically: a
// source that waits on its context, or a sleep that starts after the
// deadline was armed and outlasts it.
func testLimits() Limits {
	l := DefaultLimits()
	l.SourceTimeout = 30 * time.Second
	l.CaptureTimeout = time.Minute
	return l
}

// captureTimeoutCut is the capture deadline for tests that need it to pass
// while a context-ignoring source is stuck. It is short enough to keep the
// tests quick and long enough that a source which returns at once has
// reported before it on any runner.
const captureTimeoutCut = 250 * time.Millisecond

// captureWithin runs Capture and fails the test if it has not returned
// within d: the hard-deadline tests would otherwise hang until the package
// timeout when the deadline is not enforced.
func captureWithin(t *testing.T, c *Collector, req CaptureRequest, d time.Duration) CaptureReport {
	t.Helper()
	type result struct {
		rep CaptureReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := c.Capture(context.Background(), req)
		done <- result{rep, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Capture: %v", r.err)
		}
		return r.rep
	case <-time.After(d):
		t.Fatalf("Capture did not return within %s: the capture deadline is not enforced", d)
		return CaptureReport{}
	}
}

func newTestCollector(t *testing.T, limits Limits, sources ...Source) *Collector {
	t.Helper()
	c, err := NewCollector(sources, limits, slog.Default())
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c
}

func localRequest(sources ...string) CaptureRequest {
	return CaptureRequest{
		ClusterID: k8s.LocalClusterID,
		User:      testUser,
		Target: TargetRef{
			APIGroup: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment",
			Namespace: testNS, Name: testName,
		},
		Sources: sources,
	}
}

func sourceReport(t *testing.T, rep CaptureReport, id string) SourceReport {
	t.Helper()
	for _, s := range rep.Sources {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no report for source %q in %+v", id, rep.Sources)
	return SourceReport{}
}

func itemsOfKind(items []Evidence, kind string, mode EvidenceMode) []Evidence {
	var out []Evidence
	for _, it := range items {
		if it.EvidenceKind == kind && (mode == "" || it.Mode == mode) {
			out = append(out, it)
		}
	}
	return out
}

// fakeClients is the impersonated-client seam over fake clientsets.
type fakeClients struct {
	typed    kubernetes.Interface
	dyn      dynamic.Interface
	typedErr error
	dynErr   error
}

func (f fakeClients) ClientForUser(string, []string) (kubernetes.Interface, error) {
	return f.typed, f.typedErr
}
func (f fakeClients) DynamicClientForUser(string, []string) (dynamic.Interface, error) {
	return f.dyn, f.dynErr
}

// eventPager makes a typed fake honour Limit and Continue in name order, as
// the apiserver does (so a naive Limit keeps the OLDEST events), and records
// every list call's field selector. The namespace's events are read from
// the tracker and sorted once, on the first call; each page then copies
// only its own slice (the continue token is the offset), so a page costs
// O(page) however many events the fixture holds. Fixtures add every event
// before the first list.
type eventPager struct {
	mu        sync.Mutex
	selectors []string
	pages     int
	expireAt  int // 1-based page whose continue token has expired (0: never)
	sorted    []corev1.Event
	loaded    bool
}

func installEventPaging(cs *kfake.Clientset) *eventPager {
	p := &eventPager{}
	cs.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		la := action.(k8stesting.ListActionImpl)
		p.mu.Lock()
		p.selectors = append(p.selectors, la.ListOptions.FieldSelector)
		p.pages++
		page, expireAt := p.pages, p.expireAt
		p.mu.Unlock()
		if expireAt > 0 && page == expireAt {
			return true, nil, apierrors.NewResourceExpired("The provided continue parameter is too old")
		}
		all, err := p.snapshot(cs, la.GetNamespace())
		if err != nil {
			return true, nil, err
		}
		offset := 0
		if la.ListOptions.Continue != "" {
			offset, _ = strconv.Atoi(la.ListOptions.Continue)
		}
		limit := int(la.ListOptions.Limit)
		if limit <= 0 {
			limit = len(all)
		}
		end := min(offset+limit, len(all))
		out := &corev1.EventList{Items: make([]corev1.Event, 0, end-offset)}
		for i := offset; i < end; i++ {
			out.Items = append(out.Items, *all[i].DeepCopy())
		}
		if end < len(all) {
			out.Continue = strconv.Itoa(end)
		}
		return true, out, nil
	})
	return p
}

// snapshot returns the namespace's events in name order, reading and
// sorting the tracker only on the first call.
func (p *eventPager) snapshot(cs *kfake.Clientset, namespace string) ([]corev1.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.loaded {
		obj, err := cs.Tracker().List(corev1.SchemeGroupVersion.WithResource("events"), corev1.SchemeGroupVersion.WithKind("Event"), namespace)
		if err != nil {
			return nil, err
		}
		p.sorted = obj.(*corev1.EventList).Items
		sort.Slice(p.sorted, func(i, j int) bool { return p.sorted[i].Name < p.sorted[j].Name })
		p.loaded = true
	}
	return p.sorted, nil
}

// countingGets records every dynamic GET the adapters issue.
func countingGets(dyn *dynfake.FakeDynamicClient) *atomic.Int32 {
	var n atomic.Int32
	dyn.PrependReactor("get", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		n.Add(1)
		return false, nil, nil
	})
	return &n
}

// deadlineEvents wraps a clientset so one event list call (1-based) meets
// its context's end the way a real client does, with no wall-clock race:
// failAt waits for the context to end and fails with its error (the
// apiserver was still answering when the deadline hit); holdPageAt fetches
// its page and returns it only once the context has ended (the page arrived
// as the deadline hit). Calls after the deadline reach the fake unchanged.
type deadlineEvents struct {
	kubernetes.Interface
	failAt     int
	holdPageAt int

	mu    sync.Mutex
	calls int
}

func (d *deadlineEvents) CoreV1() typedcorev1.CoreV1Interface {
	return deadlineCore{CoreV1Interface: d.Interface.CoreV1(), d: d}
}

type deadlineCore struct {
	typedcorev1.CoreV1Interface
	d *deadlineEvents
}

func (c deadlineCore) Events(namespace string) typedcorev1.EventInterface {
	return deadlineEventList{EventInterface: c.CoreV1Interface.Events(namespace), d: c.d}
}

type deadlineEventList struct {
	typedcorev1.EventInterface
	d *deadlineEvents
}

func (e deadlineEventList) List(ctx context.Context, opts metav1.ListOptions) (*corev1.EventList, error) {
	e.d.mu.Lock()
	e.d.calls++
	call := e.d.calls
	e.d.mu.Unlock()
	if call == e.d.failAt {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	list, err := e.EventInterface.List(ctx, opts)
	if call == e.d.holdPageAt {
		<-ctx.Done()
	}
	return list, err
}

// sarAccessChecker is a real AccessChecker whose SARs are answered by decide
// (allowed, error) on the SAR's resource: the only way to make one check
// error while another succeeds. sleep delays every SAR.
func sarAccessChecker(decide func(verb, group, resource string) (bool, error), sleep time.Duration) *resources.AccessChecker {
	cs := kfake.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		time.Sleep(sleep)
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		ra := sar.Spec.ResourceAttributes
		allowed, err := decide(ra.Verb, ra.Group, ra.Resource)
		if err != nil {
			return true, nil, err
		}
		out := sar.DeepCopy()
		out.Status.Allowed = allowed
		return true, out, nil
	})
	return resources.NewAccessChecker(k8s.NewFakeClientFactory(cs), slog.Default())
}

// fakeLister is a topology.ResourceLister over fixed slices.
type fakeLister struct {
	deployments []*appsv1.Deployment
	replicaSets []*appsv1.ReplicaSet
	pods        []*corev1.Pod
	err         error // returned by ListDeployments when set
}

func (l *fakeLister) ListPods(context.Context, string) ([]*corev1.Pod, error) { return l.pods, nil }
func (l *fakeLister) ListServices(context.Context, string) ([]*corev1.Service, error) {
	return nil, nil
}
func (l *fakeLister) ListDeployments(context.Context, string) ([]*appsv1.Deployment, error) {
	return l.deployments, l.err
}
func (l *fakeLister) ListReplicaSets(context.Context, string) ([]*appsv1.ReplicaSet, error) {
	return l.replicaSets, nil
}
func (l *fakeLister) ListStatefulSets(context.Context, string) ([]*appsv1.StatefulSet, error) {
	return nil, nil
}
func (l *fakeLister) ListDaemonSets(context.Context, string) ([]*appsv1.DaemonSet, error) {
	return nil, nil
}
func (l *fakeLister) ListJobs(context.Context, string) ([]*batchv1.Job, error) { return nil, nil }
func (l *fakeLister) ListCronJobs(context.Context, string) ([]*batchv1.CronJob, error) {
	return nil, nil
}
func (l *fakeLister) ListIngresses(context.Context, string) ([]*networkingv1.Ingress, error) {
	return nil, nil
}
func (l *fakeLister) ListConfigMaps(context.Context, string) ([]*corev1.ConfigMap, error) {
	return nil, nil
}
func (l *fakeLister) ListPVCs(context.Context, string) ([]*corev1.PersistentVolumeClaim, error) {
	return nil, nil
}
func (l *fakeLister) ListHPAs(context.Context, string) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	return nil, nil
}

// typedDeployment is the informer-cache view of the target: it references a
// Secret through env, so every adapter must mark its rows secret-derived.
func typedDeployment(replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNS, UID: types.UID(testUID), ResourceVersion: "100"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "app", Image: "example/app:1",
				Env: []corev1.EnvVar{{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "db"}, Key: "password"},
				}}},
			}}}},
		},
		Status: appsv1.DeploymentStatus{Replicas: replicas, ReadyReplicas: 0, AvailableReplicas: 0},
	}
}

// unstructuredDeployment is the live object the impersonated GET returns. It
// carries the canary in every place the projection must drop it.
func unstructuredDeployment(rv string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name": testName, "namespace": testNS, "uid": testUID, "resourceVersion": rv,
			"annotations": map[string]any{
				LastAppliedConfigAnnotation: `{"stringData":{"password":"` + testCanary + `"}}`,
				"team.example.com/owner":    testCanary,
				"kubecenter.io/pinned":      "true",
			},
			"labels": map[string]any{"app": testName},
		},
		"spec": map[string]any{
			"replicas": float64(2),
			"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{
					"name": "app", "image": "example/app:1",
					"env": []any{
						map[string]any{"name": "PLAIN", "value": testEnvPlain},
						map[string]any{"name": "DB_PASSWORD", "valueFrom": map[string]any{
							"secretKeyRef": map[string]any{"name": "db", "key": "password"},
						}},
					},
				}},
			}},
		},
		"status": map[string]any{"replicas": float64(2), "readyReplicas": float64(0)},
	}}
}

func targetEvent(name, reason, message string, count int32, at time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS, Annotations: map[string]string{"note": testCanary}},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Deployment", Namespace: testNS, Name: testName, UID: types.UID(testUID), APIVersion: "apps/v1",
		},
		Type: corev1.EventTypeWarning, Reason: reason, Message: message, Count: count,
		FirstTimestamp: metav1.NewTime(at.Add(-time.Hour)), LastTimestamp: metav1.NewTime(at),
		Source: corev1.EventSource{Component: "deployment-controller"},
	}
}

// adapterFixture wires the three real adapters over fakes.
type adapterFixture struct {
	lister *fakeLister
	typed  *kfake.Clientset
	dyn    *dynfake.FakeDynamicClient
	pager  *eventPager
}

func newAdapterFixture(objRV string, events ...runtime.Object) *adapterFixture {
	f := &adapterFixture{
		lister: &fakeLister{deployments: []*appsv1.Deployment{typedDeployment(2)}},
		typed:  kfake.NewSimpleClientset(events...),
		dyn:    dynfake.NewSimpleDynamicClient(runtime.NewScheme(), unstructuredDeployment(objRV)),
	}
	f.pager = installEventPaging(f.typed)
	return f
}

func (f *adapterFixture) sources(t *testing.T, access *resources.AccessChecker, maxBytes int) []Source {
	t.Helper()
	r, err := NewRedactor(maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	clients := fakeClients{typed: f.typed, dyn: f.dyn}
	return []Source{
		NewDiagnosticsSource(f.lister, access, r, slog.Default()),
		NewObjectSource(clients, nil, access, r, slog.Default()),
		NewEventsSource(clients, nil, access, r, slog.Default()),
	}
}

func capture(t *testing.T, c *Collector, req CaptureRequest) CaptureReport {
	t.Helper()
	rep, err := c.Capture(context.Background(), req)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	return rep
}

// ---------------------------------------------------------------------------
// Collector rules (stub sources)
// ---------------------------------------------------------------------------

func TestCaptureOneSourceTimeoutYieldsPartialAndKeepsOthers(t *testing.T) {
	l := serialLimits()
	l.SourceTimeout = 40 * time.Millisecond // only "slow" waits for it
	c := newTestCollector(t, l,
		completeSource("fast", completeItem("a")),
		blockingSource("slow"),
		steadySource("steady", completeItem("b")),
	)
	rep := capture(t, c, localRequest())

	if rep.Completeness != CompletenessPartial {
		t.Fatalf("completeness = %q, want partial", rep.Completeness)
	}
	if got := sourceReport(t, rep, "slow"); got.Completeness != CompletenessTimedOut {
		t.Errorf("slow = %+v, want timed_out", got)
	}
	for _, id := range []string{"fast", "steady"} {
		if got := sourceReport(t, rep, id); got.Completeness != CompletenessComplete || got.Items != 1 {
			t.Errorf("%s = %+v, want complete with 1 item", id, got)
		}
	}
	if len(rep.Items) != 2 {
		t.Fatalf("items = %d, want 2 (the timed-out source must not discard the others)", len(rep.Items))
	}
}

func TestCaptureOneSourceFailureNeverCancelsSiblings(t *testing.T) {
	c := newTestCollector(t, serialLimits(),
		stubSource{id: "broken", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
			return SourceResult{}, errors.New("apiserver said no for " + testName)
		}},
		steadySource("steady", completeItem("b")),
	)
	rep := capture(t, c, localRequest())
	if got := sourceReport(t, rep, "broken"); got.Completeness != CompletenessFailed || strings.Contains(got.Detail, testName) {
		t.Errorf("broken = %+v, want failed without the target name", got)
	}
	if got := sourceReport(t, rep, "steady"); got.Completeness != CompletenessComplete || got.Items != 1 {
		t.Errorf("steady = %+v, want complete", got)
	}
}

func TestCaptureOneSourcePanicIsRecoveredAndReportedFailed(t *testing.T) {
	c := newTestCollector(t, serialLimits(),
		stubSource{id: "boom", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
			var m map[string]any
			m["x"] = 1 // nil map write: a real adapter bug shape
			return SourceResult{}, nil
		}},
		steadySource("steady", completeItem("b")),
	)
	rep := capture(t, c, localRequest())
	if got := sourceReport(t, rep, "boom"); got.Completeness != CompletenessFailed {
		t.Errorf("boom = %+v, want failed", got)
	}
	if got := sourceReport(t, rep, "steady"); got.Completeness != CompletenessComplete || got.Items != 1 {
		t.Errorf("steady = %+v, want complete (a sibling's panic must not cancel it)", got)
	}
	if rep.Completeness != CompletenessPartial {
		t.Errorf("completeness = %q, want partial", rep.Completeness)
	}
}

func TestCaptureRespectsMaxConcurrency(t *testing.T) {
	const limit = 3
	var running, peak atomic.Int32
	var sources []Source
	for i := range 9 {
		sources = append(sources, stubSource{id: fmt.Sprintf("s%d", i), fn: func(context.Context, CaptureRequest) (SourceResult, error) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			running.Add(-1)
			return SourceResult{Completeness: CompletenessComplete}, nil
		}})
	}
	l := testLimits()
	l.MaxConcurrency = limit
	rep := capture(t, newTestCollector(t, l, sources...), localRequest())
	if p := peak.Load(); p > limit || p == 0 {
		t.Fatalf("peak concurrency = %d, want 1..%d", p, limit)
	}
	if rep.Completeness != CompletenessComplete {
		t.Errorf("completeness = %q", rep.Completeness)
	}
}

func TestCaptureCancellationReturnsContextErrorAndNoItems(t *testing.T) {
	started := make(chan struct{})
	c := newTestCollector(t, testLimits(),
		completeSource("fast", completeItem("a")),
		stubSource{id: "waits", fn: func(ctx context.Context, _ CaptureRequest) (SourceResult, error) {
			close(started)
			<-ctx.Done()
			return SourceResult{}, ctx.Err()
		}},
	)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	rep, err := c.Capture(ctx, localRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(rep.Items) != 0 || len(rep.Sources) != 0 {
		t.Fatalf("report = %+v, want empty: a cancelled capture must give the caller nothing to persist", rep)
	}
}

func TestCaptureCompletenessAggregation(t *testing.T) {
	cases := []struct {
		name  string
		other Completeness
		want  Completeness
	}{
		{"all complete", CompletenessComplete, CompletenessComplete},
		{"one forbidden", CompletenessForbidden, CompletenessPartial},
		{"one failed", CompletenessFailed, CompletenessPartial},
		{"one partial", CompletenessPartial, CompletenessPartial},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			other := stubSource{id: "other", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
				return SourceResult{Completeness: tc.other}, nil
			}}
			rep := capture(t, newTestCollector(t, testLimits(), completeSource("ok", completeItem("a")), other), localRequest())
			if rep.Completeness != tc.want {
				t.Errorf("completeness = %q, want %q", rep.Completeness, tc.want)
			}
		})
	}
}

func TestCaptureRejectsRemoteClusterBeforeAnySourceRuns(t *testing.T) {
	var ran atomic.Bool
	c := newTestCollector(t, testLimits(), stubSource{id: "spy", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
		ran.Store(true)
		return SourceResult{Completeness: CompletenessComplete}, nil
	}})
	req := localRequest()
	req.ClusterID = "prod-east"
	_, err := c.Capture(context.Background(), req)
	if !errors.Is(err, ErrRemoteCaptureUnsupported) {
		t.Fatalf("err = %v, want ErrRemoteCaptureUnsupported", err)
	}
	if ran.Load() {
		t.Fatal("a source ran for a remote cluster")
	}
}

func TestCaptureRejectsUnknownSourceAndDedupesDuplicates(t *testing.T) {
	var calls atomic.Int32
	c := newTestCollector(t, testLimits(), stubSource{id: "one", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
		calls.Add(1)
		return SourceResult{Completeness: CompletenessComplete}, nil
	}})

	_, err := c.Capture(context.Background(), localRequest("one", "nope"))
	var unknown *UnknownSourceError
	if !errors.As(err, &unknown) || unknown.ID != "nope" || !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("err = %v, want *UnknownSourceError{nope}", err)
	}

	rep := capture(t, c, localRequest("one", "one"))
	if calls.Load() != 1 || len(rep.Sources) != 1 {
		t.Fatalf("calls = %d, reports = %d; want a duplicate id to run once", calls.Load(), len(rep.Sources))
	}
}

func TestCaptureRejectsInvalidRequests(t *testing.T) {
	c := newTestCollector(t, testLimits(), completeSource("one"))
	for name, mutate := range map[string]func(*CaptureRequest){
		"no user":     func(r *CaptureRequest) { r.User = nil },
		"no username": func(r *CaptureRequest) { r.User = &auth.User{} },
		"no resource": func(r *CaptureRequest) { r.Target.Resource = "" },
		"no name":     func(r *CaptureRequest) { r.Target.Name = "" },
	} {
		t.Run(name, func(t *testing.T) {
			req := localRequest()
			mutate(&req)
			if _, err := c.Capture(context.Background(), req); !errors.Is(err, ErrInvalidCaptureRequest) {
				t.Fatalf("err = %v, want ErrInvalidCaptureRequest", err)
			}
		})
	}
}

func TestCaptureDropsOversizeItemsAndCapsItemCount(t *testing.T) {
	l := testLimits()
	l.MaxItemBytes = 256
	l.MaxItems = 3
	big := completeItem("big")
	big.Payload = json.RawMessage(`{"pad":"` + strings.Repeat("x", 300) + `"}`)
	c := newTestCollector(t, l,
		completeSource("sized", completeItem("a"), big),
		completeSource("many", completeItem("b"), completeItem("c"), completeItem("d"), completeItem("e")),
	)
	rep := capture(t, c, localRequest())
	if got := sourceReport(t, rep, "sized"); got.Completeness != CompletenessPartial || got.Items != 1 || !strings.Contains(got.Detail, "size") {
		t.Errorf("sized = %+v, want partial with 1 item and a size note", got)
	}
	if len(rep.Items) != l.MaxItems {
		t.Errorf("items = %d, want capped at %d", len(rep.Items), l.MaxItems)
	}
	if rep.Completeness != CompletenessPartial {
		t.Errorf("completeness = %q, want partial", rep.Completeness)
	}
	for _, it := range rep.Items {
		if len(it.Payload) > l.MaxItemBytes {
			t.Errorf("item %s exceeds the per-item bound", it.Source.Name)
		}
		if it.CaptureKey == "" || it.CollectedAt.IsZero() || it.PayloadBytes != len(it.Payload) {
			t.Errorf("item %s not finalized: %+v", it.Source.Name, it)
		}
	}
}

func TestCaptureDropsItemsTheStoreWouldReject(t *testing.T) {
	bad := completeItem("bad")
	bad.Payload = json.RawMessage(`{"nul":"\u0000"}`)
	rep := capture(t, newTestCollector(t, testLimits(), completeSource("src", completeItem("ok"), bad)), localRequest())
	if got := sourceReport(t, rep, "src"); got.Items != 1 || got.Completeness != CompletenessPartial {
		t.Fatalf("src = %+v, want 1 item and partial", got)
	}
	for _, it := range rep.Items {
		row, err := it.Row()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ValidateEvidenceRow(row); err != nil {
			t.Errorf("surviving item fails store validation: %v", err)
		}
	}
}

func TestLimitsValidate(t *testing.T) {
	if err := DefaultLimits().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	d := DefaultLimits()
	if d.MaxItemBytes != 1<<20 || d.MaxIncidentBytes != 10<<20 || d.MaxItems != 500 || d.MaxScopes != 20 ||
		d.CaptureTimeout != 20*time.Second || d.SourceTimeout != 5*time.Second || d.MaxConcurrency != 4 {
		t.Fatalf("defaults = %+v", d)
	}
	for name, mutate := range map[string]func(*Limits){
		"item bytes above ceiling":  func(l *Limits) { l.MaxItemBytes = store.EvidenceMaxItemBytesCeiling + 1 },
		"item bytes below redactor": func(l *Limits) { l.MaxItemBytes = MinMaxBytes - 1 },
		"zero items":                func(l *Limits) { l.MaxItems = 0 },
		"zero concurrency":          func(l *Limits) { l.MaxConcurrency = 0 },
		"zero source timeout":       func(l *Limits) { l.SourceTimeout = 0 },
		"zero capture timeout":      func(l *Limits) { l.CaptureTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			l := DefaultLimits()
			mutate(&l)
			if err := l.Validate(); err == nil {
				t.Fatal("want error")
			}
			if _, err := NewCollector(nil, l, nil); err == nil {
				t.Fatal("NewCollector accepted invalid limits")
			}
		})
	}
	el := DefaultLimits().EvidenceLimits()
	if el != (store.EvidenceLimits{MaxItemBytes: 1 << 20, MaxIncidentBytes: 10 << 20, MaxItems: 500, MaxScopes: 20}) {
		t.Fatalf("EvidenceLimits = %+v", el)
	}
}

// ---------------------------------------------------------------------------
// Adapters through the collector
// ---------------------------------------------------------------------------

func TestCaptureDeniedNamespaceProducesForbiddenWithoutLeak(t *testing.T) {
	var sarMu sync.Mutex
	var sars []string
	deny := resources.NewPredicateAccessChecker(func(verb, apiGroup, resource, namespace string) bool {
		sarMu.Lock()
		sars = append(sars, verb+" "+apiGroup+"/"+resource+" "+namespace)
		sarMu.Unlock()
		return namespace != testNS
	})
	f := newAdapterFixture("100", targetEvent("e1", "FailedCreate", "boom", 1, time.Now()))
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, deny, DefaultMaxBytes)...), localRequest())

	if len(rep.Items) != 0 {
		t.Fatalf("items = %d, want none for a denied namespace", len(rep.Items))
	}
	if rep.Completeness != CompletenessPartial {
		t.Errorf("completeness = %q", rep.Completeness)
	}
	for _, s := range rep.Sources {
		if s.Completeness != CompletenessForbidden {
			t.Errorf("%s = %+v, want forbidden", s.ID, s)
		}
		for _, leak := range []string{testNS, testName, "Deployment", "deployments", "events"} {
			if strings.Contains(s.Detail, leak) {
				t.Errorf("%s detail %q names %q", s.ID, s.Detail, leak)
			}
		}
	}
	sarMu.Lock()
	defer sarMu.Unlock()
	want := map[string]bool{"get apps/deployments " + testNS: false, "list /events " + testNS: false}
	for _, s := range sars {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for s, seen := range want {
		if !seen {
			t.Errorf("SAR %q was never issued (adapters: %v)", s, sars)
		}
	}
}

func TestCaptureSARErrorProducesFailedNotForbidden(t *testing.T) {
	erroring := resources.NewErroringAccessChecker(errors.New("webhook down for " + testNS))
	f := newAdapterFixture("100")
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, erroring, DefaultMaxBytes)...), localRequest())
	if len(rep.Items) != 0 {
		t.Fatalf("items = %d, want none when authorization cannot be determined", len(rep.Items))
	}
	for _, s := range rep.Sources {
		if s.Completeness != CompletenessFailed {
			t.Errorf("%s = %+v, want failed (an undetermined check is not a denial)", s.ID, s)
		}
		if strings.Contains(s.Detail, testNS) || strings.Contains(s.Detail, "webhook") {
			t.Errorf("%s detail %q leaks the check error", s.ID, s.Detail)
		}
	}
}

func TestCaptureRedactsSensitiveEnvAndAnnotations(t *testing.T) {
	f := newAdapterFixture("100", targetEvent("e1", "FailedCreate", "boom "+testCanary, 1, time.Now()))
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest())
	if rep.Completeness != CompletenessComplete {
		t.Fatalf("completeness = %q (%+v)", rep.Completeness, rep.Sources)
	}
	snaps := itemsOfKind(rep.Items, EvidenceKindObjectSummary, ModeSnapshot)
	if len(snaps) != 1 {
		t.Fatalf("object snapshots = %d", len(snaps))
	}
	p := string(snaps[0].Payload)
	if strings.Contains(p, testCanary) || strings.Contains(p, testEnvPlain) || strings.Contains(p, "env") || strings.Contains(p, "team.example.com") {
		t.Errorf("object payload leaks: %s", p)
	}
	if !strings.Contains(p, "kubecenter.io/pinned") {
		t.Errorf("allowlisted annotation missing: %s", p)
	}
	if !snaps[0].Redaction.SecretDerived || !snaps[0].Redaction.Applied {
		t.Errorf("redaction = %+v", snaps[0].Redaction)
	}
	// Event annotations are outside the event allowlist; the message is
	// controller text and survives sanitized.
	events := itemsOfKind(rep.Items, EvidenceKindEventList, ModeSnapshot)
	if len(events) != 1 {
		t.Fatalf("event lists = %d", len(events))
	}
	if ep := string(events[0].Payload); strings.Contains(ep, `"note"`) || !strings.Contains(ep, "FailedCreate") {
		t.Errorf("event payload: %s", ep)
	}
}

func TestCaptureRejectsMaliciousResourceText(t *testing.T) {
	hostile := "line\x00one\x1b[31m\xff\xfe<script>" + strings.Repeat("‮", 3)
	f := newAdapterFixture("100", targetEvent("e1", "Bad\x00Reason", hostile, 7, time.Now()))
	f.dyn = dynfake.NewSimpleDynamicClient(runtime.NewScheme(), func() *unstructured.Unstructured {
		u := unstructuredDeployment("100")
		md := u.Object["metadata"].(map[string]any)
		md["labels"] = map[string]any{"app": hostile}
		return u
	}())
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest())
	for _, it := range rep.Items {
		if it.Mode == ModeLiveLink {
			continue
		}
		row, err := it.Row()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ValidateEvidenceRow(row); err != nil {
			t.Errorf("%s/%s row rejected by the store: %v", it.EvidenceKind, it.Mode, err)
		}
		if strings.ContainsAny(string(it.Payload), "\x00\x1b") || strings.Contains(string(it.Payload), `\u0000`) || strings.Contains(string(it.Payload), `\u001b`) {
			t.Errorf("%s payload keeps control characters: %s", it.EvidenceKind, it.Payload)
		}
	}
	events := itemsOfKind(rep.Items, EvidenceKindEventList, "")
	if len(events) != 1 || !strings.Contains(events[0].Redaction.Rules[0]+strings.Join(events[0].Redaction.Rules, ","), RuleTextSanitized) {
		t.Errorf("event redaction = %+v, want text-sanitized", events)
	}
}

func TestCaptureBoundsHugeEventList(t *testing.T) {
	const bound = 32 << 10
	var evs []runtime.Object
	now := time.Now().Truncate(time.Second)
	for i := range 1000 {
		evs = append(evs, targetEvent(fmt.Sprintf("e%04d", i), "Spam", strings.Repeat("m", 3000), int32(i), now.Add(time.Duration(i)*time.Second)))
	}
	f := newAdapterFixture("100", evs...)
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), bound)...), localRequest())
	events := itemsOfKind(rep.Items, EvidenceKindEventList, "")
	if len(events) != 1 {
		t.Fatalf("event lists = %d (%+v)", len(events), rep.Sources)
	}
	ev := events[0]
	if f.pager.pages != 5 {
		t.Errorf("pages = %d, want 5 (1000 events in pages of 200)", f.pager.pages)
	}
	if got := sourceReport(t, rep, SourceEvents); got.Completeness != CompletenessPartial || got.Detail != detailWindowCut+"; "+detailProjectionTooLarge {
		t.Errorf("events source = %+v, want partial naming the window cut and the size cut", got)
	}
	if ev.CompletenessDetail != detailWindowCut+"; "+detailProjectionTooLarge || ev.Completeness != CompletenessPartial {
		t.Errorf("item = %q %q, want the same causes on the item", ev.Completeness, ev.CompletenessDetail)
	}
	if rep.Completeness != CompletenessPartial {
		t.Errorf("capture completeness = %q, want partial when an item was cut", rep.Completeness)
	}
	if len(ev.Payload) > bound {
		t.Errorf("payload %d bytes exceeds bound %d", len(ev.Payload), bound)
	}
	if !ev.Redaction.Truncated {
		t.Error("truncation not flagged")
	}
	var payload struct {
		Events    []json.RawMessage `json:"events"`
		Observed  int               `json:"observed"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Truncated || payload.Observed != 200 || len(payload.Events) == 0 || len(payload.Events) >= 200 {
		t.Errorf("payload observed=%d kept=%d truncated=%v; want the 200-event cap then a size cut", payload.Observed, len(payload.Events), payload.Truncated)
	}
	if ev.SourceObservedAt == nil || !ev.SourceObservedAt.Equal(now.Add(999*time.Second)) {
		t.Errorf("sourceObservedAt = %v, want the newest event time", ev.SourceObservedAt)
	}
}

func TestCaptureDeletedTargetProducesFailedNotPanic(t *testing.T) {
	f := &adapterFixture{
		lister: &fakeLister{},
		typed:  kfake.NewSimpleClientset(),
		dyn:    dynfake.NewSimpleDynamicClient(runtime.NewScheme()),
	}
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest())
	for _, id := range []string{SourceDiagnostics, SourceObject} {
		got := sourceReport(t, rep, id)
		if got.Completeness != CompletenessFailed || !strings.Contains(got.Detail, "not found") || strings.Contains(got.Detail, testName) {
			t.Errorf("%s = %+v, want failed 'target not found' without the name", id, got)
		}
	}
	// Events for a vanished object are still a legitimate (empty) observation,
	// bound by name and kind because no UID could be read.
	ev := itemsOfKind(rep.Items, EvidenceKindEventList, "")
	if len(ev) != 1 || !ev[0].Source.IdentityWeak || ev[0].Source.UID != "" {
		t.Errorf("event item = %+v, want one identity-weak item", ev)
	}
}

func TestCaptureEmitsPairedSnapshotAndLiveLink(t *testing.T) {
	f := newAdapterFixture("100")
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceObject))
	snap := itemsOfKind(rep.Items, EvidenceKindObjectSummary, ModeSnapshot)
	live := itemsOfKind(rep.Items, EvidenceKindObjectSummary, ModeLiveLink)
	if len(snap) != 1 || len(live) != 1 {
		t.Fatalf("snapshot=%d live=%d (%+v)", len(snap), len(live), rep.Sources)
	}
	if live[0].Payload != nil || live[0].PayloadBytes != 0 {
		t.Errorf("live link carries a payload: %s", live[0].Payload)
	}
	if snap[0].Source != live[0].Source || snap[0].Source.UID != testUID || snap[0].Source.ResourceVersion != "100" || snap[0].Source.Resource != "deployments" {
		t.Errorf("sources differ or incomplete: %+v vs %+v", snap[0].Source, live[0].Source)
	}
	if snap[0].CaptureKey == live[0].CaptureKey {
		t.Error("snapshot and live link share a capture key")
	}
	if live[0].Redaction.SecretDerived != snap[0].Redaction.SecretDerived {
		t.Error("live link and snapshot disagree on secret derivation")
	}
	for _, it := range append(snap, live...) {
		row, err := it.Row()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ValidateEvidenceRow(row); err != nil {
			t.Errorf("%s row: %v", it.Mode, err)
		}
	}
}

func TestCaptureIsIdempotentForIdenticalObservation(t *testing.T) {
	at := time.Now().Truncate(time.Second)
	f := newAdapterFixture("100", targetEvent("e1", "FailedCreate", "boom", 3, at))
	c := newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...)
	first := capture(t, c, localRequest())
	time.Sleep(5 * time.Millisecond)
	second := capture(t, c, localRequest())
	if len(first.Items) == 0 || len(first.Items) != len(second.Items) {
		t.Fatalf("items %d vs %d", len(first.Items), len(second.Items))
	}
	keys := func(items []Evidence) map[string]bool {
		m := map[string]bool{}
		for _, it := range items {
			m[it.CaptureKey] = true
		}
		return m
	}
	k1, k2 := keys(first.Items), keys(second.Items)
	if len(k1) != len(first.Items) {
		t.Fatalf("keys collide within one capture: %v", k1)
	}
	for k := range k1 {
		if !k2[k] {
			t.Errorf("key %s from the first capture is absent from the second (observedAt must not enter the key)", k)
		}
	}
}

func TestCaptureNewObservationCreatesNewKey(t *testing.T) {
	at := time.Now().Truncate(time.Second)
	f := newAdapterFixture("100", targetEvent("e1", "FailedCreate", "boom", 3, at))
	allow := resources.NewAlwaysAllowAccessChecker()
	base := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), localRequest())

	// A new object version, an event that fired again, and a changed check
	// outcome (replica count now matches) are each a new observation.
	g := newAdapterFixture("101", targetEvent("e1", "FailedCreate", "boom", 4, at.Add(time.Minute)))
	g.lister.deployments[0].Status.ReadyReplicas = 2
	g.lister.deployments[0].Status.AvailableReplicas = 2
	changed := capture(t, newTestCollector(t, testLimits(), g.sources(t, allow, DefaultMaxBytes)...), localRequest())

	baseKeys := map[string]Evidence{}
	for _, it := range base.Items {
		baseKeys[it.CaptureKey] = it
	}
	for _, it := range changed.Items {
		if it.Mode == ModeLiveLink {
			if _, same := baseKeys[it.CaptureKey]; !same {
				t.Errorf("live link key changed although the identity did not")
			}
			continue
		}
		if it.EvidenceKind == EvidenceKindDiagnosticCheck && !strings.Contains(string(it.Payload), "replicamismatch") {
			continue // other checks observed the same outcome
		}
		if _, same := baseKeys[it.CaptureKey]; same {
			t.Errorf("%s/%s reused a key for a changed observation: %s", it.EvidenceKind, it.Mode, it.Payload)
		}
	}
}

func TestCaptureNeverPersistsSecretValues(t *testing.T) {
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "db", "namespace": testNS, "uid": "sec-uid", "resourceVersion": "7",
			"annotations": map[string]any{LastAppliedConfigAnnotation: testCanary}},
		"type":       "Opaque",
		"data":       map[string]any{"PROD_DB_PASSWORD": "c2VjcmV0"},
		"stringData": map[string]any{"token": testCanary},
	}}
	ev := targetEvent("e1", "Synced", "secret "+testCanary+" synced", 1, time.Now())
	ev.InvolvedObject = corev1.ObjectReference{Kind: "Secret", Namespace: testNS, Name: "db", UID: "sec-uid"}
	f := &adapterFixture{lister: &fakeLister{}, typed: kfake.NewSimpleClientset(ev), dyn: dynfake.NewSimpleDynamicClient(runtime.NewScheme(), secret)}
	req := localRequest(SourceObject, SourceEvents)
	req.Target = TargetRef{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: testNS, Name: "db"}
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), req)
	if rep.Completeness != CompletenessComplete || len(rep.Items) != 3 {
		t.Fatalf("completeness=%q items=%d (%+v)", rep.Completeness, len(rep.Items), rep.Sources)
	}
	for _, it := range rep.Items {
		row, err := it.Row()
		if err != nil {
			t.Fatal(err)
		}
		if !row.SecretDerived {
			t.Errorf("%s/%s row is not secret-derived", it.EvidenceKind, it.Mode)
		}
		blob := string(row.Payload) + string(row.Redaction) + row.CompletenessDetail
		for _, leak := range []string{"c2VjcmV0", "PROD_DB_PASSWORD", "stringData", `"data"`} {
			if strings.Contains(blob, leak) {
				t.Errorf("%s/%s persists %q: %s", it.EvidenceKind, it.Mode, leak, blob)
			}
		}
		// The event message is controller text; the object's own values are not.
		if it.EvidenceKind == EvidenceKindObjectSummary && strings.Contains(blob, testCanary) {
			t.Errorf("object row leaks the canary: %s", blob)
		}
	}
}

func TestSecretReferencingDeploymentRowsAreSecretDerived(t *testing.T) {
	f := newAdapterFixture("100", targetEvent("e1", "FailedCreate", "boom", 1, time.Now()))
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest())
	if rep.Completeness != CompletenessComplete {
		t.Fatalf("completeness = %q (%+v)", rep.Completeness, rep.Sources)
	}
	seen := map[string]bool{}
	for _, it := range rep.Items {
		seen[it.EvidenceKind] = true
		row, err := it.Row()
		if err != nil {
			t.Fatal(err)
		}
		if !row.SecretDerived {
			t.Errorf("%s/%s row secret_derived=false for a Secret-referencing Deployment", it.EvidenceKind, it.Mode)
		}
		if err := store.ValidateEvidenceRow(row); err != nil {
			t.Errorf("%s/%s: %v", it.EvidenceKind, it.Mode, err)
		}
	}
	for _, kind := range []string{EvidenceKindDiagnosticCheck, EvidenceKindObjectSummary, EvidenceKindEventList} {
		if !seen[kind] {
			t.Errorf("no %s item captured", kind)
		}
	}
}

func TestObjectSourcePassesPluralResourceToRedactor(t *testing.T) {
	// The fake answers the GET with an object that carries no kind, as an
	// informer-shaped typed object would: only the plural resource can tell
	// the redactor it is a Secret.
	dyn := dynfake.NewSimpleDynamicClient(runtime.NewScheme())
	dyn.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"metadata":   map[string]any{"name": "db", "namespace": testNS, "uid": "sec-uid", "resourceVersion": "1"},
			"data":       map[string]any{"k": "djE="},
		}}, nil
	})
	r, _ := NewRedactor(DefaultMaxBytes)
	src := NewObjectSource(fakeClients{typed: kfake.NewSimpleClientset(), dyn: dyn}, nil, resources.NewAlwaysAllowAccessChecker(), r, slog.Default())
	req := localRequest()
	req.Target = TargetRef{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: testNS, Name: "db"}
	res, err := src.Collect(context.Background(), req)
	if err != nil || res.Completeness != CompletenessComplete || len(res.Items) != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, it := range res.Items {
		if !it.Redaction.SecretDerived || strings.Contains(string(it.Payload), "djE=") {
			t.Errorf("%s: redaction=%+v payload=%s", it.Mode, it.Redaction, it.Payload)
		}
	}
}

// ---------------------------------------------------------------------------
// Stored scope == capture-time gate (review round 1, P1)
// ---------------------------------------------------------------------------

// crashLoopFixture adds a ReplicaSet and a crash-looping pod under the
// Deployment so the pod-derived checks have related content. podName is the
// pod's name as the lister returns it.
func crashLoopFixture(f *adapterFixture, podName string) {
	f.lister.replicaSets = []*appsv1.ReplicaSet{{ObjectMeta: metav1.ObjectMeta{
		Name: testName + "-rs", Namespace: testNS,
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: testName}},
	}}}
	f.lister.pods = []*corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNS, UID: "pod-uid-1",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: testName + "-rs"}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", RestartCount: 7,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		}}},
	}}
}

func checkID(t *testing.T, e Evidence) string {
	t.Helper()
	var c struct {
		CheckID string `json:"checkId"`
	}
	if err := json.Unmarshal(e.Payload, &c); err != nil {
		t.Fatal(err)
	}
	return c.CheckID
}

func TestStoredScopeIsTheResourceTheContentDerivesFrom(t *testing.T) {
	f := newAdapterFixture("100", targetEvent("e1", "FailedCreate", "boom", 1, time.Now()))
	crashLoopFixture(f, testName+"-rs-abc12")
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest())
	if rep.Completeness != CompletenessComplete {
		t.Fatalf("completeness = %q (%+v)", rep.Completeness, rep.Sources)
	}

	scopes := map[string]string{} // "<kind>/<mode>/<checkId>" -> "<group>/<resource>"
	for _, it := range rep.Items {
		key := it.EvidenceKind + "/" + string(it.Mode)
		if it.EvidenceKind == EvidenceKindDiagnosticCheck {
			key += "/" + checkID(t, it)
		}
		scopes[key] = it.Source.APIGroup + "/" + it.Source.Resource
		if it.Source.Kind != "Deployment" || it.Source.Name != testName || it.Source.UID != testUID {
			t.Errorf("%s lost the target's provenance: %+v", key, it.Source)
		}
	}
	want := map[string]string{
		"event_list/snapshot": "/events",
		"diagnostic_check/snapshot/diagnostics/crashloopbackoff": "/pods",
		"diagnostic_check/snapshot/diagnostics/imagepullbackoff": "/pods",
		"diagnostic_check/snapshot/diagnostics/pendingpod":       "/pods",
		"diagnostic_check/snapshot/diagnostics/replicamismatch":  "apps/deployments",
		"object_summary/snapshot":                                "apps/deployments",
		"object_summary/live_link":                               "apps/deployments",
	}
	for k, w := range want {
		if scopes[k] != w {
			t.Errorf("%s stored under %q, want %q", k, scopes[k], w)
		}
	}

	// Composition with U23a's read gate: a reader holding get on
	// deployments only, re-checked per stored row scope, sees exactly the
	// object rows and the target-only check; events and pod-derived checks
	// are withheld.
	reader := resources.NewPredicateAccessChecker(func(verb, apiGroup, resource, namespace string) bool {
		return verb == "get" && apiGroup == "apps" && resource == "deployments" && namespace == testNS
	})
	visible, withheld := map[string]bool{}, map[string]bool{}
	for _, it := range rep.Items {
		row, err := it.Row()
		if err != nil {
			t.Fatal(err)
		}
		ok, err := reader.CanAccessGroupResource(context.Background(), row.ClusterID, "reader", nil, "get", row.APIGroup, row.Resource, row.Namespace)
		if err != nil {
			t.Fatal(err)
		}
		key := it.EvidenceKind + "/" + string(it.Mode)
		if it.EvidenceKind == EvidenceKindDiagnosticCheck {
			key += "/" + checkID(t, it)
		}
		if ok {
			visible[key] = true
		} else {
			withheld[key] = true
		}
	}
	for _, k := range []string{"event_list/snapshot", "diagnostic_check/snapshot/diagnostics/crashloopbackoff"} {
		if !withheld[k] {
			t.Errorf("%s is readable by a deployments-only reader: the stored scope is laxer than the capture gate", k)
		}
	}
	for _, k := range []string{"object_summary/snapshot", "object_summary/live_link", "diagnostic_check/snapshot/diagnostics/replicamismatch"} {
		if !visible[k] {
			t.Errorf("%s withheld from a deployments-only reader", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Events: paging, identity, gating, selectors
// ---------------------------------------------------------------------------

func TestEventsPagesThroughContinueAndKeepsTheNewest(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mk := func(n int) []runtime.Object {
		var evs []runtime.Object
		for i := range n {
			evs = append(evs, targetEvent(fmt.Sprintf("e%04d", i), "Spam", "m", int32(i), now.Add(time.Duration(i)*time.Second)))
		}
		return evs
	}
	t.Run("three pages, count cut", func(t *testing.T) {
		f := newAdapterFixture("100", mk(450)...)
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceEvents))
		ev := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
		if f.pager.pages != 3 {
			t.Errorf("pages = %d, want 3", f.pager.pages)
		}
		if ev.SourceObservedAt == nil || !ev.SourceObservedAt.Equal(now.Add(449*time.Second)) {
			t.Errorf("observedAt = %v, want the newest event (last page)", ev.SourceObservedAt)
		}
		var payload struct {
			Events    []struct{ Count int32 } `json:"events"`
			Observed  int                     `json:"observed"`
			Truncated bool                    `json:"truncated"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Observed != 200 || !payload.Truncated || len(payload.Events) != 200 || payload.Events[0].Count != 449 || payload.Events[199].Count != 250 {
			t.Errorf("payload observed=%d kept=%d truncated=%v first=%d last=%d; want the newest 200", payload.Observed, len(payload.Events), payload.Truncated, payload.Events[0].Count, payload.Events[len(payload.Events)-1].Count)
		}
		if f.pager.selectors[0] != "involvedObject.uid="+testUID {
			t.Errorf("field selector = %q", f.pager.selectors[0])
		}
		if got := sourceReport(t, rep, SourceEvents); got.Detail != detailWindowCut || ev.CompletenessDetail != detailWindowCut {
			t.Errorf("details = %q / %q, want the window cut only", got.Detail, ev.CompletenessDetail)
		}
	})
	t.Run("newest beyond the fifth page is still found", func(t *testing.T) {
		// Name order is not time order: here the newest event has the
		// highest name, so it sits on the sixth page.
		f := newAdapterFixture("100", mk(1200)...)
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceEvents))
		ev := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
		if f.pager.pages != 6 || !ev.Redaction.Truncated || ev.SourceObservedAt == nil || !ev.SourceObservedAt.Equal(now.Add(1199*time.Second)) {
			t.Errorf("pages=%d truncated=%v observedAt=%v; want 6 pages, truncated (count), the true newest event", f.pager.pages, ev.Redaction.Truncated, ev.SourceObservedAt)
		}
		if got := sourceReport(t, rep, SourceEvents); got.Completeness != CompletenessPartial || got.Detail != detailWindowCut {
			t.Errorf("events = %+v, want partial for the window cut only", got)
		}
	})
	t.Run("hard page ceiling flags more", func(t *testing.T) {
		f := newAdapterFixture("100", mk(5200)...)
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceEvents))
		ev := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
		if f.pager.pages != 25 || !ev.Redaction.Truncated || ev.SourceObservedAt == nil || !ev.SourceObservedAt.Equal(now.Add(4999*time.Second)) {
			t.Errorf("pages=%d truncated=%v observedAt=%v; want 25 pages, truncated, newest fetched event", f.pager.pages, ev.Redaction.Truncated, ev.SourceObservedAt)
		}
		if got := sourceReport(t, rep, SourceEvents); got.Detail != detailListStopped+"; "+detailWindowCut || ev.CompletenessDetail != got.Detail {
			t.Errorf("details = %q / %q, want the ceiling stop then the window cut", got.Detail, ev.CompletenessDetail)
		}
	})
	t.Run("expired continue token keeps what was fetched and is partial", func(t *testing.T) {
		f := newAdapterFixture("100", mk(450)...)
		f.pager.expireAt = 2
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceEvents))
		got := sourceReport(t, rep, SourceEvents)
		if got.Completeness != CompletenessPartial || got.Detail != detailListInterrupted || got.Items != 1 {
			t.Fatalf("events = %+v, want partial with exactly the interrupted detail and its item", got)
		}
		ev := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
		if ev.CompletenessDetail != detailListInterrupted || ev.Completeness != CompletenessPartial {
			t.Errorf("item = %q %q", ev.Completeness, ev.CompletenessDetail)
		}
		if !ev.Redaction.Truncated || ev.SourceObservedAt == nil || !ev.SourceObservedAt.Equal(now.Add(199*time.Second)) {
			t.Errorf("item = truncated %v observedAt %v; want the first page's newest, flagged", ev.Redaction.Truncated, ev.SourceObservedAt)
		}
		if f.pager.expireAt = 1; true {
			g := newAdapterFixture("100", mk(10)...)
			g.pager.expireAt = 1
			rep := capture(t, newTestCollector(t, testLimits(), g.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceEvents))
			if got := sourceReport(t, rep, SourceEvents); got.Completeness != CompletenessFailed || got.Detail != detailListFailed {
				t.Errorf("first-page expiry = %+v, want failed (nothing was fetched)", got)
			}
		}
	})
	t.Run("source deadline mid-paging keeps what was fetched", func(t *testing.T) {
		// The deadline is reached by waiting on the source context, never
		// by racing a timer against the fake, so the outcome is the same on
		// any runner: exactly the first page is kept and the stop is named.
		// (On a runner slow enough that the deadline passes before the
		// second call, the loop's own check stops at the same place.)
		for _, tc := range []struct {
			name  string
			stall *deadlineEvents
		}{
			{"while the next page is in flight", &deadlineEvents{failAt: 2}},
			{"as the first page arrives", &deadlineEvents{holdPageAt: 1}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newAdapterFixture("100", mk(1000)...)
				tc.stall.Interface = f.typed
				r, err := NewRedactor(DefaultMaxBytes)
				if err != nil {
					t.Fatal(err)
				}
				src := NewEventsSource(fakeClients{typed: tc.stall, dyn: f.dyn}, nil, resources.NewAlwaysAllowAccessChecker(), r, slog.Default())
				l := testLimits()
				l.SourceTimeout = 50 * time.Millisecond
				rep := capture(t, newTestCollector(t, l, src), localRequest(SourceEvents))
				got := sourceReport(t, rep, SourceEvents)
				if got.Items != 1 || got.Completeness != CompletenessPartial || got.Detail != detailListStopped {
					t.Fatalf("events = %+v, want one item, partial for the deadline stop only (not a timeout)", got)
				}
				ev := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
				if ev.CompletenessDetail != got.Detail || !ev.Redaction.Truncated {
					t.Errorf("item = %q truncated=%v, want the deadline stop, flagged", ev.CompletenessDetail, ev.Redaction.Truncated)
				}
				if f.pager.pages != 1 || ev.SourceObservedAt == nil || !ev.SourceObservedAt.Equal(now.Add(199*time.Second)) {
					t.Errorf("pages=%d observedAt=%v; want the first page only, its newest event", f.pager.pages, ev.SourceObservedAt)
				}
			})
		}
	})
	t.Run("digest covers every fetched event", func(t *testing.T) {
		r, _ := NewRedactor(DefaultMaxBytes)
		base := make([]corev1.Event, 0, 250)
		for i := range 250 {
			base = append(base, *targetEvent(fmt.Sprintf("e%04d", i), "Spam", "m", int32(i), now.Add(time.Duration(i)*time.Second)))
		}
		same := projectEvents(r, append([]corev1.Event(nil), base...), nil, false, false)
		again := projectEvents(r, append([]corev1.Event(nil), base...), nil, false, false)
		changed := append([]corev1.Event(nil), base...)
		changed[0].Count = 99 // the oldest event, outside the kept 200
		other := projectEvents(r, changed, nil, false, false)
		if same.digest != again.digest || same.digest == other.digest {
			t.Errorf("digest same=%s again=%s other=%s", same.digest, again.digest, other.digest)
		}
	})
}

func TestEventsIdentityComesOnlyFromAGatedRead(t *testing.T) {
	at := time.Now().Truncate(time.Second)
	ev := targetEvent("e1", "FailedCreate", "boom", 2, at)

	t.Run("read allowed: one GET, strong identity", func(t *testing.T) {
		f := newAdapterFixture("100", ev)
		gets := countingGets(f.dyn)
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceEvents))
		it := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
		if gets.Load() != 1 || it.Source.IdentityWeak || it.Source.UID != testUID || it.Source.ResourceVersion != "100" {
			t.Errorf("gets=%d source=%+v", gets.Load(), it.Source)
		}
	})
	t.Run("target get denied: no GET, weak identity, conservative secret derivation", func(t *testing.T) {
		f := newAdapterFixture("100", ev)
		gets := countingGets(f.dyn)
		access := resources.NewPredicateAccessChecker(func(verb, apiGroup, resource, namespace string) bool {
			return resource == "events"
		})
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, access, DefaultMaxBytes)...), localRequest(SourceEvents))
		if got := sourceReport(t, rep, SourceEvents); got.Completeness != CompletenessComplete {
			t.Fatalf("events = %+v", got)
		}
		it := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
		if gets.Load() != 0 || !it.Source.IdentityWeak || it.Source.UID != "" || !it.Redaction.SecretDerived {
			t.Errorf("gets=%d source=%+v redaction=%+v", gets.Load(), it.Source, it.Redaction)
		}
		if f.pager.selectors[0] != "involvedObject.kind=Deployment,involvedObject.name="+testName {
			t.Errorf("field selector = %q", f.pager.selectors[0])
		}
		if !strings.Contains(string(it.Payload), "FailedCreate") {
			t.Errorf("payload = %s", it.Payload)
		}
	})
	t.Run("target get SAR errors: same as denied", func(t *testing.T) {
		f := newAdapterFixture("100", ev)
		gets := countingGets(f.dyn)
		access := sarAccessChecker(func(verb, group, resource string) (bool, error) {
			if resource == "deployments" {
				return false, errors.New("webhook down")
			}
			return true, nil
		}, 0)
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, access, DefaultMaxBytes)...), localRequest(SourceEvents))
		if got := sourceReport(t, rep, SourceEvents); got.Completeness != CompletenessComplete {
			t.Fatalf("events = %+v", got)
		}
		it := itemsOfKind(rep.Items, EvidenceKindEventList, "")[0]
		if gets.Load() != 0 || !it.Source.IdentityWeak || !it.Redaction.SecretDerived {
			t.Errorf("gets=%d source=%+v redaction=%+v", gets.Load(), it.Source, it.Redaction)
		}
	})
	t.Run("a Secret-free kind that cannot be read stays non-derived", func(t *testing.T) {
		if secretDerivedUnknown(TargetRef{Kind: "ConfigMap", Resource: "configmaps"}) {
			t.Error("configmaps marked derived")
		}
		for _, tr := range []TargetRef{{Kind: "Deployment", Resource: "deployments"}, {Kind: "CronJob", Resource: "cronjobs"}, {Kind: "Secret", Resource: "secrets"}, {Resource: "pods"}} {
			if !secretDerivedUnknown(tr) {
				t.Errorf("%+v not marked derived", tr)
			}
		}
	})
}

func TestAdapterReadFailuresAfterAPassingSAR(t *testing.T) {
	allow := resources.NewAlwaysAllowAccessChecker()
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, testName, errors.New("rbac"))

	t.Run("object GET forbidden", func(t *testing.T) {
		f := newAdapterFixture("100")
		f.dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbidden })
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), localRequest(SourceObject))
		if got := sourceReport(t, rep, SourceObject); got.Completeness != CompletenessForbidden || got.Detail != detailForbidden {
			t.Errorf("object = %+v", got)
		}
	})
	t.Run("object GET fails", func(t *testing.T) {
		f := newAdapterFixture("100")
		f.dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("etcd unavailable for " + testName)
		})
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), localRequest(SourceObject))
		if got := sourceReport(t, rep, SourceObject); got.Completeness != CompletenessFailed || got.Detail != detailReadFailed {
			t.Errorf("object = %+v", got)
		}
	})
	t.Run("events list forbidden", func(t *testing.T) {
		f := newAdapterFixture("100")
		f.typed.PrependReactor("list", "events", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbidden })
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), localRequest(SourceEvents))
		if got := sourceReport(t, rep, SourceEvents); got.Completeness != CompletenessForbidden || got.Detail != detailForbidden {
			t.Errorf("events = %+v", got)
		}
	})
	t.Run("events list fails", func(t *testing.T) {
		f := newAdapterFixture("100")
		f.typed.PrependReactor("list", "events", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("timeout talking to " + testNS)
		})
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), localRequest(SourceEvents))
		if got := sourceReport(t, rep, SourceEvents); got.Completeness != CompletenessFailed || got.Detail != detailListFailed {
			t.Errorf("events = %+v", got)
		}
	})
	t.Run("impersonated clients unavailable", func(t *testing.T) {
		r, _ := NewRedactor(DefaultMaxBytes)
		broken := fakeClients{typedErr: errors.New("no typed client"), dynErr: errors.New("no dynamic client")}
		c := newTestCollector(t, testLimits(),
			NewObjectSource(broken, nil, allow, r, nil),
			NewEventsSource(broken, nil, allow, r, nil),
		)
		rep := capture(t, c, localRequest())
		for _, id := range []string{SourceObject, SourceEvents} {
			if got := sourceReport(t, rep, id); got.Completeness != CompletenessFailed || got.Detail != detailClientUnavailable {
				t.Errorf("%s = %+v", id, got)
			}
		}
	})
	t.Run("diagnostics unsupported kind", func(t *testing.T) {
		f := newAdapterFixture("100")
		req := localRequest(SourceDiagnostics)
		req.Target.Kind, req.Target.Resource, req.Target.APIGroup = "ConfigMap", "configmaps", ""
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), req)
		if got := sourceReport(t, rep, SourceDiagnostics); got.Completeness != CompletenessFailed || got.Detail != detailUnsupportedKind {
			t.Errorf("diagnostics = %+v", got)
		}
	})
}

func TestObjectSourceResolvesVersionThroughTheMapper(t *testing.T) {
	allow := resources.NewAlwaysAllowAccessChecker()
	req := localRequest(SourceObject)
	req.Target.Version = ""

	t.Run("mapper supplies the version", func(t *testing.T) {
		f := newAdapterFixture("100")
		r, _ := NewRedactor(DefaultMaxBytes)
		mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "apps", Version: "v1"}})
		mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
		src := NewObjectSource(fakeClients{typed: f.typed, dyn: f.dyn}, mapper, allow, r, nil)
		rep := capture(t, newTestCollector(t, testLimits(), src), req)
		if got := sourceReport(t, rep, SourceObject); got.Completeness != CompletenessComplete || got.Items != 2 {
			t.Errorf("object = %+v", got)
		}
	})
	t.Run("no mapper, no version", func(t *testing.T) {
		f := newAdapterFixture("100")
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), req)
		if got := sourceReport(t, rep, SourceObject); got.Completeness != CompletenessFailed || got.Detail != detailUnresolvedVersion {
			t.Errorf("object = %+v", got)
		}
	})
}

func TestSARCutShortByTheSourceDeadlineIsTimedOut(t *testing.T) {
	// Every SAR starts after its source's deadline was armed and sleeps
	// three times past it, so the deadline has always passed when it
	// answers, however slow the runner.
	slow := sarAccessChecker(func(string, string, string) (bool, error) { return false, errors.New("too late") }, 120*time.Millisecond)
	f := newAdapterFixture("100")
	l := testLimits()
	l.SourceTimeout = 40 * time.Millisecond
	rep := capture(t, newTestCollector(t, l, f.sources(t, slow, DefaultMaxBytes)...), localRequest())
	for _, s := range rep.Sources {
		if s.Completeness != CompletenessTimedOut || s.Detail != detailTimedOut {
			t.Errorf("%s = %+v, want timed_out (the deadline, not the check, decided)", s.ID, s)
		}
	}
}

func TestDiagnosticsFreeTextGoesThroughTheRedactor(t *testing.T) {
	hostilePod := testName + "-rs-\x00\x1b[31m\xffabcde" + strings.Repeat("x", 200)
	f := newAdapterFixture("100")
	crashLoopFixture(f, hostilePod)
	l := testLimits()
	// A tiny redactor bound with the default item bound: text is cut without
	// the whole item being dropped.
	rep := capture(t, newTestCollector(t, l, f.sources(t, resources.NewAlwaysAllowAccessChecker(), MinMaxBytes)...), localRequest(SourceDiagnostics))
	var crash *Evidence
	for i := range rep.Items {
		if checkID(t, rep.Items[i]) == "diagnostics/crashloopbackoff" {
			crash = &rep.Items[i]
		}
	}
	if crash == nil {
		t.Fatalf("no crashloopbackoff item (%+v)", rep.Sources)
	}
	p := string(crash.Payload)
	if strings.Contains(p, `\u0000`) || strings.Contains(p, `\u001b`) || strings.Contains(p, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx") {
		t.Errorf("payload keeps control characters or uncut text: %s", p)
	}
	if !strings.Contains(p, "abcde") {
		t.Errorf("sanitized text lost: %s", p)
	}
	rules := strings.Join(crash.Redaction.Rules, ",")
	if !crash.Redaction.Truncated || !strings.Contains(rules, RuleTextSanitized) || !strings.Contains(rules, RuleTruncated) {
		t.Errorf("redaction = %+v", crash.Redaction)
	}
	if got := sourceReport(t, rep, SourceDiagnostics); got.Completeness != CompletenessPartial {
		t.Errorf("diagnostics = %+v, want partial when text was cut", got)
	}
}

func TestCaptureDeadlineIsHardAgainstAContextIgnoringSource(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	l := testLimits()
	l.CaptureTimeout = captureTimeoutCut
	l.SourceTimeout = captureTimeoutCut
	c := newTestCollector(t, l,
		completeSource("fast", completeItem("a")),
		stubSource{id: "stuck", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
			<-release // ignores ctx entirely
			return SourceResult{Items: []Evidence{completeItem("late")}, Completeness: CompletenessComplete}, nil
		}},
	)
	c.grace = 20 * time.Millisecond
	rep := captureWithin(t, c, localRequest(), 10*time.Second)
	if got := sourceReport(t, rep, "stuck"); got.Completeness != CompletenessTimedOut || got.Detail != detailCaptureCut || got.Items != 0 {
		t.Errorf("stuck = %+v", got)
	}
	if got := sourceReport(t, rep, "fast"); got.Completeness != CompletenessComplete || got.Items != 1 {
		t.Errorf("fast = %+v", got)
	}
	if rep.Completeness != CompletenessPartial || len(rep.Items) != 1 {
		t.Errorf("report = %q with %d items", rep.Completeness, len(rep.Items))
	}
}

func TestCaptureRunsSourcesInParallelUpToTheLimit(t *testing.T) {
	const limit = 3
	var running, peak atomic.Int32
	var sources []Source
	for i := range limit {
		sources = append(sources, stubSource{id: fmt.Sprintf("p%d", i), fn: func(ctx context.Context, _ CaptureRequest) (SourceResult, error) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			// Barrier: wait until every sibling is running or the deadline passes.
			for running.Load() < limit && ctx.Err() == nil {
				time.Sleep(time.Millisecond)
			}
			return SourceResult{Completeness: CompletenessComplete}, nil
		}})
	}
	l := testLimits()
	l.MaxConcurrency = limit
	l.SourceTimeout = 10 * time.Second // the barrier's bound, reached only when the limit is not honoured
	rep := capture(t, newTestCollector(t, l, sources...), localRequest())
	if peak.Load() != limit || rep.Completeness != CompletenessComplete {
		t.Fatalf("peak = %d, completeness = %q; want %d sources running together", peak.Load(), rep.Completeness, limit)
	}
}

func TestCaptureRejectsNodeTargets(t *testing.T) {
	c := newTestCollector(t, testLimits(), completeSource("one"))
	for _, tr := range []TargetRef{
		{Version: "v1", Resource: "nodes", Kind: "Node", Name: "worker-1"},
		{Version: "v1", Resource: "Nodes", Name: "worker-1"},
	} {
		req := localRequest()
		req.Target = tr
		if _, err := c.Capture(context.Background(), req); !errors.Is(err, ErrInvalidCaptureRequest) {
			t.Errorf("%+v: err = %v, want ErrInvalidCaptureRequest", tr, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Capture keys (D-2)
// ---------------------------------------------------------------------------

func TestCaptureKeyDerivation(t *testing.T) {
	src := SourceRef{ClusterID: "local", APIGroup: "apps", Resource: "deployments", Kind: "Deployment", Namespace: "a", Name: "b", UID: "u"}
	same := CaptureKey(EvidenceKindDiagnosticCheck, ModeSnapshot, src, []string{"diagnostics/pendingpod", "fail", "finding"})
	if same != CaptureKey(EvidenceKindDiagnosticCheck, ModeSnapshot, src, []string{"diagnostics/pendingpod", "fail", "finding"}) {
		t.Error("identical inputs differ")
	}
	if len(same) != 64 {
		t.Errorf("key %q is not hex sha256", same)
	}
	if same == CaptureKey(EvidenceKindDiagnosticCheck, ModeSnapshot, src, []string{"diagnostics/pendingpod", "pass", "ok"}) {
		t.Error("changed outcome keeps the key")
	}
	if same == CaptureKey(EvidenceKindDiagnosticCheck, ModeSnapshot, src, []string{"diagnostics/crashloopbackoff", "fail", "finding"}) {
		t.Error("two checks of one target share a key")
	}
	if CaptureKey(EvidenceKindObjectSummary, ModeSnapshot, src, []string{"100"}) == CaptureKey(EvidenceKindObjectSummary, ModeSnapshot, src, []string{"101"}) {
		t.Error("new resourceVersion keeps the key")
	}
	if CaptureKey(EvidenceKindObjectSummary, ModeSnapshot, src, []string{"100"}) == CaptureKey(EvidenceKindObjectSummary, ModeLiveLink, src, nil) {
		t.Error("snapshot and live link share a key")
	}

	// Field-boundary shifting: moving bytes between adjacent fields must
	// change the key (a naive "|" join would not).
	shifted := src
	shifted.Namespace, shifted.Name = "a|b", ""
	src2 := src
	src2.Namespace, src2.Name = "a", "|b"
	src3 := src
	src3.Namespace, src3.Name = "a\"", "b"
	for i, alt := range []SourceRef{shifted, src2, src3} {
		if CaptureKey(EvidenceKindObjectSummary, ModeLiveLink, src, nil) == CaptureKey(EvidenceKindObjectSummary, ModeLiveLink, alt, nil) {
			t.Errorf("variant %d collides with the base key", i)
		}
	}
	if CaptureKey(EvidenceKindObjectSummary, ModeSnapshot, src, []string{"1", "2"}) == CaptureKey(EvidenceKindObjectSummary, ModeSnapshot, src, []string{"12"}) ||
		CaptureKey(EvidenceKindObjectSummary, ModeSnapshot, src, []string{"1,2"}) == CaptureKey(EvidenceKindObjectSummary, ModeSnapshot, src, []string{"1", "2"}) {
		t.Error("discriminator boundaries are not encoded")
	}
}

// ---------------------------------------------------------------------------
// Evidence <-> row mapping
// ---------------------------------------------------------------------------

func TestEvidenceRowRoundTrip(t *testing.T) {
	observed := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	collected := observed.Add(time.Minute)
	e := Evidence{
		EvidenceKind: EvidenceKindObjectSummary,
		Mode:         ModeSnapshot,
		Source: SourceRef{ClusterID: "local", APIGroup: "apps", Resource: "deployments", Kind: "Deployment",
			Namespace: testNS, Name: testName, UID: testUID, ResourceVersion: "100"},
		SourceObservedAt:   &observed,
		CollectedAt:        collected,
		Completeness:       CompletenessPartial,
		CompletenessDetail: "one event dropped for size",
		Redaction:          RedactionMeta{Applied: true, Rules: []string{RuleFieldAllowlist}, FieldsRemoved: 3, Truncated: true, SecretDerived: true},
		Payload:            json.RawMessage(`{"kind":"Deployment"}`),
		CaptureKey:         strings.Repeat("ab", 32),
	}
	row, err := e.Row()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateEvidenceRow(row); err != nil {
		t.Fatalf("row invalid: %v", err)
	}
	if row.PayloadBytes != len(e.Payload) || !row.SecretDerived || row.SourceKind != "Deployment" || row.SourceUID != testUID ||
		row.ResourceVersion != "100" || row.Mode != "snapshot" || row.Completeness != "partial" || row.CaptureKey != e.CaptureKey ||
		row.SourceObservedAt == nil || !row.SourceObservedAt.Equal(observed) || !row.CollectedAt.Equal(collected) {
		t.Fatalf("row = %+v", row)
	}
	var meta RedactionMeta
	if err := json.Unmarshal(row.Redaction, &meta); err != nil || meta.FieldsRemoved != 3 || !meta.SecretDerived || !meta.Truncated {
		t.Fatalf("redaction json = %s (%v)", row.Redaction, err)
	}

	back, err := EvidenceFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	back.PayloadBytes = 0
	e.PayloadBytes = 0
	if back.Source != e.Source || back.Completeness != e.Completeness || back.CompletenessDetail != e.CompletenessDetail ||
		string(back.Payload) != string(e.Payload) || back.CaptureKey != e.CaptureKey || back.Mode != e.Mode || back.EvidenceKind != e.EvidenceKind ||
		!back.SourceObservedAt.Equal(*e.SourceObservedAt) || !back.CollectedAt.Equal(e.CollectedAt) {
		t.Errorf("round trip changed the item:\n%+v\n%+v", back, e)
	}
	if back.Redaction.FieldsRemoved != 3 || !back.Redaction.SecretDerived || len(back.Redaction.Rules) != 1 {
		t.Errorf("redaction = %+v", back.Redaction)
	}

	// A row without a UID is identity-weak on the way back.
	row.SourceUID = ""
	weak, err := EvidenceFromRow(row)
	if err != nil || !weak.Source.IdentityWeak {
		t.Errorf("weak = %+v err=%v", weak.Source, err)
	}
	// Live links come back without a payload.
	row.Mode = store.EvidenceModeLiveLink
	row.Payload = nil
	live, err := EvidenceFromRow(row)
	if err != nil || live.Payload != nil || live.PayloadBytes != 0 {
		t.Errorf("live = %+v err=%v", live, err)
	}
	if _, err := EvidenceFromRow(store.IncidentEvidenceRow{Redaction: json.RawMessage(`[`)}); err == nil {
		t.Error("malformed redaction accepted")
	}
}

func TestWithheldEvidenceCarriesNoScope(t *testing.T) {
	b, err := json.Marshal(WithheldEvidence{ID: "x", EvidenceKind: EvidenceKindObjectSummary, CollectedAt: time.Unix(0, 0).UTC(), Withheld: true, WithheldReason: WithheldForbidden})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "evidenceKind", "collectedAt", "withheld", "withheldReason"} {
		delete(m, k)
	}
	if len(m) != 0 {
		t.Errorf("withheld shape carries extra fields: %v", m)
	}
}

// ---------------------------------------------------------------------------
// Re-review round 2
// ---------------------------------------------------------------------------

// stuckSource ignores its context and blocks until release is closed.
func stuckSource(id string, release chan struct{}) Source {
	return stubSource{id: id, fn: func(context.Context, CaptureRequest) (SourceResult, error) {
		<-release
		return SourceResult{Items: []Evidence{completeItem("late-" + id)}, Completeness: CompletenessComplete}, nil
	}}
}

func TestCaptureDeadlineHoldsWhenTheLimitQueuesSources(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	l := testLimits()
	l.MaxConcurrency = 1
	l.CaptureTimeout = 40 * time.Millisecond
	l.SourceTimeout = 40 * time.Millisecond
	// The context-ignoring source is registered FIRST, so under a limit of
	// one it holds the only slot and the healthy sources never launch
	// before the deadline.
	c := newTestCollector(t, l,
		stuckSource("stuck", release),
		completeSource("healthy-1", completeItem("a")),
		completeSource("healthy-2", completeItem("b")),
	)
	c.grace = 20 * time.Millisecond
	rep := captureWithin(t, c, localRequest(), 10*time.Second)
	if got := sourceReport(t, rep, "stuck"); got.Completeness != CompletenessTimedOut || got.Detail != detailCaptureCut {
		t.Errorf("stuck = %+v", got)
	}
	// Never-launched sources are reported timed_out as well: nothing of
	// them was observed before the capture deadline, which is the honest
	// state (not failed, not forbidden).
	for _, id := range []string{"healthy-1", "healthy-2"} {
		if got := sourceReport(t, rep, id); got.Completeness != CompletenessTimedOut || got.Detail != detailCaptureCut || got.Items != 0 {
			t.Errorf("%s = %+v, want timed_out with the capture-deadline detail", id, got)
		}
	}
	if rep.Completeness != CompletenessPartial || len(rep.Items) != 0 {
		t.Errorf("report = %q with %d items", rep.Completeness, len(rep.Items))
	}
}

func TestCapturePanicIsFailedEvenWhenTheDeadlineCutsTheWait(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	l := testLimits()
	l.CaptureTimeout = captureTimeoutCut
	l.SourceTimeout = captureTimeoutCut
	c := newTestCollector(t, l,
		stubSource{id: "boom", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
			panic("adapter bug")
		}},
		stuckSource("stuck", release),
	)
	c.grace = 20 * time.Millisecond
	rep := captureWithin(t, c, localRequest(), 10*time.Second)
	if got := sourceReport(t, rep, "boom"); got.Completeness != CompletenessFailed || got.Detail != detailPanicked {
		t.Errorf("boom = %+v, want failed (a recorded panic, not a cut)", got)
	}
	if got := sourceReport(t, rep, "stuck"); got.Completeness != CompletenessTimedOut || got.Detail != detailCaptureCut {
		t.Errorf("stuck = %+v, want timed_out", got)
	}
}

func TestDiagnosticsResolveFailuresAreClassified(t *testing.T) {
	allow := resources.NewAlwaysAllowAccessChecker()
	t.Run("absent target is the not-found sentinel", func(t *testing.T) {
		f := &adapterFixture{lister: &fakeLister{}, typed: kfake.NewSimpleClientset(), dyn: dynfake.NewSimpleDynamicClient(runtime.NewScheme())}
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), localRequest(SourceDiagnostics))
		if got := sourceReport(t, rep, SourceDiagnostics); got.Completeness != CompletenessFailed || got.Detail != detailNotFound {
			t.Errorf("diagnostics = %+v", got)
		}
	})
	t.Run("lister error is a resolve failure, even when its text says not found", func(t *testing.T) {
		f := &adapterFixture{lister: &fakeLister{err: errors.New("informer not found in cache for " + testName)}, typed: kfake.NewSimpleClientset(), dyn: dynfake.NewSimpleDynamicClient(runtime.NewScheme())}
		rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, allow, DefaultMaxBytes)...), localRequest(SourceDiagnostics))
		if got := sourceReport(t, rep, SourceDiagnostics); got.Completeness != CompletenessFailed || got.Detail != detailResolveFailed {
			t.Errorf("diagnostics = %+v", got)
		}
	})
}

func TestCheckScopePerRegisteredRule(t *testing.T) {
	selectorSvc := &diagnostics.DiagnosticTarget{Kind: "Service", Object: &corev1.Service{Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "web"}}}}
	headlessSvc := &diagnostics.DiagnosticTarget{Kind: "Service", Object: &corev1.Service{}}
	cases := []struct {
		rule   string
		kind   string
		target *diagnostics.DiagnosticTarget
		want   string // "<group>/<resource>"
	}{
		{"CrashLoopBackOff", "Deployment", &diagnostics.DiagnosticTarget{Kind: "Deployment"}, "/pods"},
		{"CrashLoopBackOff", "Pod", &diagnostics.DiagnosticTarget{Kind: "Pod"}, "/pods"},
		{"CrashLoopBackOff", "StatefulSet", &diagnostics.DiagnosticTarget{Kind: "StatefulSet"}, "/pods"},
		{"CrashLoopBackOff", "DaemonSet", &diagnostics.DiagnosticTarget{Kind: "DaemonSet"}, "/pods"},
		{"ImagePullBackOff", "Deployment", &diagnostics.DiagnosticTarget{Kind: "Deployment"}, "/pods"},
		{"PendingPod", "StatefulSet", &diagnostics.DiagnosticTarget{Kind: "StatefulSet"}, "/pods"},
		{"ReplicaMismatch", "Deployment", &diagnostics.DiagnosticTarget{Kind: "Deployment"}, "apps/deployments"},
		{"ReplicaMismatch", "StatefulSet", &diagnostics.DiagnosticTarget{Kind: "StatefulSet"}, "apps/statefulsets"},
		{"ReplicaMismatch", "DaemonSet", &diagnostics.DiagnosticTarget{Kind: "DaemonSet"}, "apps/daemonsets"},
		{"ZeroEndpoints", "Service", selectorSvc, "/pods"},
		{"ZeroEndpoints", "Service", headlessSvc, "/services"},
		{"PendingPVC", "PersistentVolumeClaim", &diagnostics.DiagnosticTarget{Kind: "PersistentVolumeClaim"}, "/persistentvolumeclaims"},
		{"NoSuchRule", "Deployment", &diagnostics.DiagnosticTarget{Kind: "Deployment"}, "apps/deployments"},
	}
	for _, tc := range cases {
		group, _, resource, ok := diagnostics.TargetResource(tc.kind)
		if !ok {
			t.Fatalf("%s is not a diagnostics kind", tc.kind)
		}
		deps := diagnostics.RuleDependsOn(tc.rule, tc.target)
		g, r := checkScope(group, resource, deps)
		if got := g + "/" + r; got != tc.want {
			t.Errorf("%s on %s: scope %q, want %q (deps %v)", tc.rule, tc.kind, got, tc.want, deps)
		}
		// No registered rule reads ReplicaSets without also reading pods, so
		// the replicasets-only branch of checkScope is unreachable today;
		// this pins that a future rule changing it is noticed.
		if slices.Contains(deps, "replicasets") && !slices.Contains(deps, "pods") {
			t.Errorf("%s reads replicasets without pods: the replicasets-only scope is now live, review its reader gate", tc.rule)
		}
	}
	if g, r := checkScope("apps", "deployments", []string{"replicasets"}); g != "apps" || r != "replicasets" {
		t.Errorf("replicasets-only branch = %s/%s", g, r)
	}
}

func TestRequiredReadScopesAddsTheTargetForRelatedScopedChecks(t *testing.T) {
	base := store.IncidentEvidenceRow{ClusterID: "local", Namespace: testNS, SourceKind: "Deployment"}
	podsRow := base
	podsRow.EvidenceKind, podsRow.APIGroup, podsRow.Resource = EvidenceKindDiagnosticCheck, "", "pods"
	got, err := RequiredReadScopes(podsRow)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.EvidenceScope{
		{ClusterID: "local", APIGroup: "", Resource: "pods", Namespace: testNS},
		{ClusterID: "local", APIGroup: "apps", Resource: "deployments", Namespace: testNS},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("pods-scoped check scopes = %+v, want %+v", got, want)
	}

	targetRow := base
	targetRow.EvidenceKind, targetRow.APIGroup, targetRow.Resource = EvidenceKindDiagnosticCheck, "apps", "deployments"
	if got, err := RequiredReadScopes(targetRow); err != nil || len(got) != 1 || got[0] != want[1] {
		t.Errorf("target-scoped check scopes = %+v (%v)", got, err)
	}

	eventsRow := base
	eventsRow.EvidenceKind, eventsRow.APIGroup, eventsRow.Resource = EvidenceKindEventList, "", "events"
	if got, err := RequiredReadScopes(eventsRow); err != nil || len(got) != 1 || got[0].Resource != "events" {
		t.Errorf("event_list scopes = %+v (%v): only the stored scope", got, err)
	}

	unknown := podsRow
	unknown.SourceKind = "Widget"
	if _, err := RequiredReadScopes(unknown); !errors.Is(err, ErrUnmappableTargetKind) {
		t.Errorf("unmappable kind err = %v", err)
	}

	// End to end: every diagnostic row the collector produces maps.
	f := newAdapterFixture("100")
	crashLoopFixture(f, testName+"-rs-x")
	rep := capture(t, newTestCollector(t, testLimits(), f.sources(t, resources.NewAlwaysAllowAccessChecker(), DefaultMaxBytes)...), localRequest(SourceDiagnostics))
	for _, it := range rep.Items {
		row, _ := it.Row()
		scopes, err := RequiredReadScopes(row)
		if err != nil {
			t.Errorf("%s: %v", checkID(t, it), err)
		}
		if row.Resource == "pods" && len(scopes) != 2 {
			t.Errorf("%s: pods-scoped row needs the target scope too, got %+v", checkID(t, it), scopes)
		}
	}
}

// ---------------------------------------------------------------------------
// Re-review round 3
// ---------------------------------------------------------------------------

func TestEventDetailsNameNoScope(t *testing.T) {
	for _, d := range []string{detailListInterrupted, detailListStopped, detailWindowCut, detailProjectionTooLarge} {
		for _, leak := range []string{testNS, testName, "Deployment", "deployments"} {
			if strings.Contains(d, leak) {
				t.Errorf("detail %q names %q", d, leak)
			}
		}
	}
}

func TestCaptureNeverStartsQueuedSourcesAfterReturning(t *testing.T) {
	release := make(chan struct{})
	var started atomic.Int32
	l := testLimits()
	l.MaxConcurrency = 1
	l.CaptureTimeout = 40 * time.Millisecond
	l.SourceTimeout = 40 * time.Millisecond
	counting := func(id string) Source {
		return stubSource{id: id, fn: func(context.Context, CaptureRequest) (SourceResult, error) {
			started.Add(1)
			return SourceResult{Completeness: CompletenessComplete}, nil
		}}
	}
	c := newTestCollector(t, l, stuckSource("stuck", release), counting("q1"), counting("q2"))
	c.grace = 20 * time.Millisecond
	rep := capture(t, c, localRequest())
	for _, id := range []string{"q1", "q2"} {
		if got := sourceReport(t, rep, id); got.Completeness != CompletenessTimedOut {
			t.Errorf("%s = %+v", id, got)
		}
	}
	// Freeing the slot after Capture returned must not start the queued
	// sources: they would run unobserved, against a dead context.
	close(release)
	time.Sleep(60 * time.Millisecond)
	if n := started.Load(); n != 0 {
		t.Fatalf("%d queued source(s) started after Capture returned", n)
	}
}

// ---------------------------------------------------------------------------
// Re-review round 4
// ---------------------------------------------------------------------------

func TestCaptureSkippedQueuedSourcesAreTimedOutNotPanicked(t *testing.T) {
	l := testLimits()
	l.MaxConcurrency = 1
	l.CaptureTimeout = 40 * time.Millisecond
	l.SourceTimeout = time.Second // the capture deadline, not the source's, ends the first source
	// The first source honours its context, so the group finishes inside
	// the grace: the queued sources were skipped, not left running, and
	// their empty slots must still read as "never observed", not "panicked".
	c := newTestCollector(t, l,
		blockingSource("polite"),
		completeSource("q1", completeItem("a")),
		completeSource("q2", completeItem("b")),
	)
	rep := capture(t, c, localRequest())
	if got := sourceReport(t, rep, "polite"); got.Completeness != CompletenessTimedOut {
		t.Errorf("polite = %+v, want timed_out", got)
	}
	for _, id := range []string{"q1", "q2"} {
		if got := sourceReport(t, rep, id); got.Completeness != CompletenessTimedOut || got.Detail != detailCaptureCut {
			t.Errorf("%s = %+v, want timed_out with the capture-deadline detail (never a panic)", id, got)
		}
	}
	if rep.Completeness != CompletenessPartial || len(rep.Items) != 0 {
		t.Errorf("report = %q with %d items", rep.Completeness, len(rep.Items))
	}
}

func TestCapturePanicAfterTheDeadlineIsStillRecorded(t *testing.T) {
	l := testLimits()
	l.CaptureTimeout = captureTimeoutCut
	c := newTestCollector(t, l,
		stubSource{id: "late-boom", fn: func(ctx context.Context, _ CaptureRequest) (SourceResult, error) {
			<-ctx.Done() // the capture deadline
			panic("adapter bug after the deadline")
		}},
		completeSource("fine", completeItem("a")),
	)
	// The group ends as soon as late-boom has panicked, so a long grace
	// costs nothing here; it only removes the race between the panic's
	// record and the end of the grace on a slow runner.
	c.grace = 10 * time.Second
	rep := captureWithin(t, c, localRequest(), 20*time.Second)
	if got := sourceReport(t, rep, "late-boom"); got.Completeness != CompletenessFailed || got.Detail != detailPanicked {
		t.Errorf("late-boom = %+v, want failed (a panic after entry is recorded, deadline or not)", got)
	}
	if got := sourceReport(t, rep, "fine"); got.Completeness != CompletenessComplete || got.Items != 1 {
		t.Errorf("fine = %+v", got)
	}
}
