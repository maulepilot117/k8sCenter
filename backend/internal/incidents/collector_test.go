package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
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

// steadySource finishes after d unless ctx is already over or ends first,
// in which case it fails: it is the sibling that must survive another
// source's trouble. Queued behind that source under MaxConcurrency 1, it
// starts after the trouble happened, so a group whose context a failure
// cancelled would hand it a dead context.
func steadySource(id string, d time.Duration, items ...Evidence) Source {
	return stubSource{id: id, fn: func(ctx context.Context, _ CaptureRequest) (SourceResult, error) {
		if err := ctx.Err(); err != nil {
			return SourceResult{}, fmt.Errorf("steady source started cancelled: %w", err)
		}
		select {
		case <-time.After(d):
			return SourceResult{Items: items, Completeness: CompletenessComplete}, nil
		case <-ctx.Done():
			return SourceResult{}, fmt.Errorf("steady source interrupted: %w", ctx.Err())
		}
	}}
}

// serialLimits runs sources one at a time so ordering is deterministic.
func serialLimits() Limits {
	l := testLimits()
	l.MaxConcurrency = 1
	return l
}

func testLimits() Limits {
	l := DefaultLimits()
	l.SourceTimeout = 40 * time.Millisecond
	l.CaptureTimeout = 3 * time.Second
	return l
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
	typed kubernetes.Interface
	dyn   dynamic.Interface
}

func (f fakeClients) ClientForUser(string, []string) (kubernetes.Interface, error) {
	return f.typed, nil
}
func (f fakeClients) DynamicClientForUser(string, []string) (dynamic.Interface, error) {
	return f.dyn, nil
}

// fakeLister is a topology.ResourceLister over fixed slices.
type fakeLister struct {
	deployments []*appsv1.Deployment
	replicaSets []*appsv1.ReplicaSet
	pods        []*corev1.Pod
}

func (l *fakeLister) ListPods(context.Context, string) ([]*corev1.Pod, error) { return l.pods, nil }
func (l *fakeLister) ListServices(context.Context, string) ([]*corev1.Service, error) {
	return nil, nil
}
func (l *fakeLister) ListDeployments(context.Context, string) ([]*appsv1.Deployment, error) {
	return l.deployments, nil
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
}

func newAdapterFixture(objRV string, events ...runtime.Object) *adapterFixture {
	return &adapterFixture{
		lister: &fakeLister{deployments: []*appsv1.Deployment{typedDeployment(2)}},
		typed:  kfake.NewSimpleClientset(events...),
		dyn:    dynfake.NewSimpleDynamicClient(runtime.NewScheme(), unstructuredDeployment(objRV)),
	}
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
	c := newTestCollector(t, serialLimits(),
		completeSource("fast", completeItem("a")),
		blockingSource("slow"),
		steadySource("steady", 5*time.Millisecond, completeItem("b")),
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
		steadySource("steady", 5*time.Millisecond, completeItem("b")),
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
		steadySource("steady", 5*time.Millisecond, completeItem("b")),
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

func TestEventsSourceUsesSuppliedUIDWithoutObjectRead(t *testing.T) {
	// A caller that already knows the UID gets no object GET at all; the
	// typed fake factory alone serves the adapter.
	at := time.Now().Truncate(time.Second)
	other := targetEvent("other", "Noise", "other object", 1, at)
	other.InvolvedObject.UID = "someone-else"
	other.InvolvedObject.Name = "elsewhere"
	factory := k8s.NewFakeClientFactory(kfake.NewSimpleClientset(targetEvent("e1", "FailedCreate", "boom", 2, at), other))
	r, _ := NewRedactor(DefaultMaxBytes)
	src := NewEventsSource(factory, nil, resources.NewAlwaysAllowAccessChecker(), r, slog.Default())
	req := localRequest()
	req.Target.UID = testUID
	res, err := src.Collect(context.Background(), req)
	if err != nil || res.Completeness != CompletenessComplete || len(res.Items) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	p := string(res.Items[0].Payload)
	if strings.Contains(p, "elsewhere") || !strings.Contains(p, "FailedCreate") {
		t.Errorf("payload = %s", p)
	}
	if res.Items[0].Source.IdentityWeak || res.Items[0].Source.UID != testUID {
		t.Errorf("source = %+v", res.Items[0].Source)
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
