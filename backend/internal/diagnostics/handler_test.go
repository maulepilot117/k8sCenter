package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/topology"
)

// countingLister stands in for the local informers. It serves a fixed pod
// set and counts every list call; a remote diagnostics request must leave
// the count at zero.
type countingLister struct {
	pods  []*corev1.Pod
	calls atomic.Int64
}

func (l *countingLister) ListPods(context.Context, string) ([]*corev1.Pod, error) {
	l.calls.Add(1)
	return l.pods, nil
}

func (l *countingLister) ListServices(context.Context, string) ([]*corev1.Service, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListDeployments(context.Context, string) ([]*appsv1.Deployment, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListReplicaSets(context.Context, string) ([]*appsv1.ReplicaSet, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListStatefulSets(context.Context, string) ([]*appsv1.StatefulSet, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListDaemonSets(context.Context, string) ([]*appsv1.DaemonSet, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListJobs(context.Context, string) ([]*batchv1.Job, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListCronJobs(context.Context, string) ([]*batchv1.CronJob, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListIngresses(context.Context, string) ([]*networkingv1.Ingress, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListConfigMaps(context.Context, string) ([]*corev1.ConfigMap, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListPVCs(context.Context, string) ([]*corev1.PersistentVolumeClaim, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *countingLister) ListHPAs(context.Context, string) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	l.calls.Add(1)
	return nil, nil
}

// testPod returns pod "web" in namespace "team-a", crash-looping when
// crashing is true. A crash-looping target makes the CrashLoopBackOff rule
// fail, which is what drives the handler to emit a notification.
func testPod(crashing bool) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-web"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if crashing {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name:         "app",
			RestartCount: 5,
			State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		}}
	}
	return p
}

// newDiagHandler wires a handler whose Lister and TopoBuilder both read the
// counting lister. With tripwire set, NotifService has no store, so any Emit
// panics (Store.DedupExists dereferences its nil pool): reaching the
// notification step at all fails the test.
func newDiagHandler(lister *countingLister, tripwire bool) *Handler {
	h := &Handler{
		Lister:        lister,
		TopoBuilder:   topology.NewBuilder(lister, nil, slog.Default()),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}
	if tripwire {
		h.NotifService = notifications.NewService(nil, nil, nil, nil, slog.Default())
	}
	return h
}

// callDiag serves one request through a chi router carrying the production
// diagnostics route patterns, with clusterID on the context as
// middleware.ClusterContext would set it (empty = no header, implicit local).
func callDiag(t *testing.T, h *Handler, clusterID, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/diagnostics/{namespace}/summary", h.HandleNamespaceSummary)
	r.Get("/diagnostics/{namespace}/{kind}/{name}", h.HandleDiagnostics)

	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "alice", KubernetesGroups: []string{"ops"}})
	if clusterID != "" {
		ctx = middleware.WithClusterID(ctx, clusterID)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(ctx))
	return w
}

var diagPaths = map[string]string{
	"resource": "/diagnostics/team-a/Pod/web",
	"summary":  "/diagnostics/team-a/summary",
}

// TestDiagnostics_RemoteRefused pins #532 for diagnostics: a remote cluster
// selection gets 501 unsupported_platform from both routes, with no lister or
// blast-radius graph read and no notification emitted, even for a target the
// local cluster would report as failing.
func TestDiagnostics_RemoteRefused(t *testing.T) {
	for name, path := range diagPaths {
		t.Run(name, func(t *testing.T) {
			lister := &countingLister{pods: []*corev1.Pod{testPod(true)}}
			h := newDiagHandler(lister, true)
			// A real checker with no ClusterRouter: any SAR attempted for the
			// remote cluster errors, turning the 501 into a 500, so moving the
			// refusal below an access check fails this test.
			h.AccessChecker = resources.NewAccessChecker(nil, slog.Default())

			w := callDiag(t, h, "remote-cluster-1", path)

			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body: %s", w.Code, w.Body.String())
			}
			var body struct {
				Data  any `json:"data"`
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Reason  string `json:"reason"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v; body: %s", err, w.Body.String())
			}
			if body.Data != nil {
				t.Errorf("data = %v, want absent on a refusal", body.Data)
			}
			if body.Error.Code != http.StatusNotImplemented ||
				body.Error.Reason != string(k8s.ReasonUnsupportedPlatform) ||
				body.Error.Message != remoteUnsupportedMessage {
				t.Errorf("error = %+v, want 501 %q %q", body.Error, k8s.ReasonUnsupportedPlatform, remoteUnsupportedMessage)
			}
			if n := lister.calls.Load(); n != 0 {
				t.Errorf("lister/topology builder read %d times on a remote request, want 0", n)
			}
		})
	}
}

// TestDiagnostics_LocalUnchanged confirms both routes still answer from the
// lister for the implicit (no header) and explicit local cluster id.
func TestDiagnostics_LocalUnchanged(t *testing.T) {
	for _, clusterID := range []string{"", "local"} {
		for name, path := range diagPaths {
			t.Run(name+"/cluster="+clusterID, func(t *testing.T) {
				lister := &countingLister{pods: []*corev1.Pod{testPod(false)}}
				h := newDiagHandler(lister, false)

				w := callDiag(t, h, clusterID, path)

				if w.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
				}
				if lister.calls.Load() == 0 {
					t.Error("lister was never read on a local request")
				}
			})
		}
	}
}

// TestDiagnostics_NotificationTripwireHasTeeth proves the tripwire used by
// TestDiagnostics_RemoteRefused would catch an emission: the same failing
// target on the local cluster reaches Emit, which panics on the store-less
// service. Without this, a tripwire that never fires would pass vacuously.
func TestDiagnostics_NotificationTripwireHasTeeth(t *testing.T) {
	lister := &countingLister{pods: []*corev1.Pod{testPod(true)}}
	h := newDiagHandler(lister, true)

	defer func() {
		if recover() == nil {
			t.Fatal("local request for a failing target did not reach NotifService.Emit; the remote no-emission assertion is vacuous")
		}
	}()
	callDiag(t, h, "local", diagPaths["resource"])
}

// TestResolveNotFoundIsASentinel: an absent target is classified with
// errors.Is(err, ErrTargetNotFound), never by the message's wording, and the
// message itself is the historical one the endpoint has always returned.
func TestResolveNotFoundIsASentinel(t *testing.T) {
	_, err := Resolve(context.Background(), &countingLister{}, "payments", "Deployment", "web", nil)
	if !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("err = %v, want ErrTargetNotFound", err)
	}
	if got, want := err.Error(), `Deployment "web" not found in namespace "payments"`; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	if errors.Is(errors.New(`Deployment "web" not found in namespace "payments"`), ErrTargetNotFound) {
		t.Fatal("a same-text error must not classify as the sentinel")
	}
}

// TestDiagnostics_MissingTargetIs404WithTheHistoricalMessage pins the
// endpoint's contract for an absent target after the sentinel change: 404,
// and the message text byte-for-byte as it has always been.
func TestDiagnostics_MissingTargetIs404WithTheHistoricalMessage(t *testing.T) {
	h := newDiagHandler(&countingLister{}, false)
	w := callDiag(t, h, "local", "/diagnostics/team-a/Pod/ghost")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if want := `Pod "ghost" not found in namespace "team-a"`; body.Error.Code != 404 || body.Error.Message != want {
		t.Fatalf("error = %+v, want code 404 message %q", body.Error, want)
	}
}

// TestDiagnostics_ResultsCarryObservedAt pins #595: every check in the
// resource diagnostics response carries observedAt, the one server-clock
// instant its checks were evaluated at. It is RFC 3339 in UTC with exactly
// three fraction digits (the ECMAScript date-time format, so every browser
// parses it), identical across the checks of one response, and inside the
// request's time bounds. Every legacy key keeps its value, so the field is a
// pure addition.
func TestDiagnostics_ResultsCarryObservedAt(t *testing.T) {
	lister := &countingLister{pods: []*corev1.Pod{testPod(false)}}
	h := newDiagHandler(lister, false)

	// observedAt is truncated to the millisecond, so the lower bound is too.
	before := time.Now().UTC().Truncate(time.Millisecond)
	w := callDiag(t, h, "local", diagPaths["resource"])
	after := time.Now().UTC()

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Results []map[string]any `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; body: %s", err, w.Body.String())
	}
	got := body.Data.Results
	if len(got) < 2 {
		t.Fatalf("got %d results, want at least 2 to compare observedAt across checks", len(got))
	}

	var first string
	for i, r := range got {
		raw, ok := r["observedAt"].(string)
		if !ok {
			t.Fatalf("results[%d].observedAt = %#v, want a string", i, r["observedAt"])
		}
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			t.Fatalf("results[%d].observedAt = %q is not RFC 3339: %v", i, raw, err)
		}
		if !strings.HasSuffix(raw, "Z") || len(raw) != len("2006-01-02T15:04:05.000Z") {
			t.Errorf("results[%d].observedAt = %q, want UTC with exactly three fraction digits", i, raw)
		}
		if at.Before(before) || at.After(after) {
			t.Errorf("results[%d].observedAt = %s, want within [%s, %s]", i, raw, before.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
		}
		if i == 0 {
			first = raw
		} else if raw != first {
			t.Errorf("results[%d].observedAt = %q, want %q: one evaluation time per request", i, raw, first)
		}
	}

	// The legacy keys are exactly what RunDiagnostics produces for the same
	// target, encoded as the endpoint always encoded them.
	target, err := Resolve(context.Background(), lister, "team-a", "Pod", "web", &RelatedRBAC{Pods: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	legacyJSON, err := json.Marshal(RunDiagnostics(context.Background(), target))
	if err != nil {
		t.Fatal(err)
	}
	var want []map[string]any
	if err := json.Unmarshal(legacyJSON, &want); err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		delete(r, "observedAt")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("legacy fields changed:\n got  %v\n want %v", got, want)
	}
}

// TestResultsWire_KeepsNullAndEmptyShapes: no results still encode as null and
// an empty result set as [], the shapes the response had before observedAt.
func TestResultsWire_KeepsNullAndEmptyShapes(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 59, 30, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		results []Result
		want    string
	}{
		{"nil", nil, "null"},
		{"empty", []Result{}, "[]"},
	} {
		got, err := json.Marshal(resultsWire(Normalize("local", nil, at, tc.results)))
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.name, err)
		}
		if string(got) != tc.want {
			t.Errorf("%s: encoded %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestResultsWire_TruncatesObservedAtToMilliseconds: a sub-millisecond part is
// dropped, never rounded up past the stamp, the zone is converted to UTC, and
// the fraction always has three digits.
func TestResultsWire_TruncatesObservedAtToMilliseconds(t *testing.T) {
	plus2 := time.FixedZone("UTC+2", 2*60*60)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 10, 6, 11, 59, 30, 123_999_999, plus2), "2026-10-06T09:59:30.123Z"},
		{time.Date(2026, 10, 6, 9, 59, 30, 0, time.UTC), "2026-10-06T09:59:30.000Z"},
	} {
		got := resultsWire(Normalize("local", nil, tc.at, []Result{{RuleName: "PendingPod", Status: "pass"}}))
		if len(got) != 1 || got[0].ObservedAt != tc.want {
			t.Errorf("observedAt for %s = %+v, want %q", tc.at.Format(time.RFC3339Nano), got, tc.want)
		}
	}
}
