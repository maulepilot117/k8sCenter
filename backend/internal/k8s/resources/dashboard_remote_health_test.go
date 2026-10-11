package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// ── Harness ────────────────────────────────────────────────────────────────

// stubControlPlane is a ControlPlaneChecker whose answer can change between
// requests: fixed states, an error, a panic, or a read that waits for its
// context to end.
type stubControlPlane struct {
	states ControlPlaneStates
	err    error
	panics bool
	blocks bool
}

func (s *stubControlPlane) ControlPlaneStatus(ctx context.Context, _ string) (ControlPlaneStates, error) {
	if s.panics {
		panic("boom")
	}
	if s.blocks {
		<-ctx.Done()
		return ControlPlaneStates{}, ctx.Err()
	}
	return s.states, s.err
}

// stubCertExpiry is a CertExpiryCounter answering a fixed CertExpiry for the
// remote cluster and the fixture's complete counts for the local one, so a
// failure injected for the remote path never reaches the local oracle.
type stubCertExpiry struct {
	remote CertExpiry
	panics bool
}

func (s *stubCertExpiry) ExpiringCountsForCluster(_ context.Context, clusterID string, _ *auth.User) (CertExpiry, error) {
	if k8s.IsLocalClusterID(clusterID) {
		return CertExpiry{Warning: 3, Critical: 1}, nil
	}
	if s.panics {
		panic("boom")
	}
	return s.remote, nil
}

// healthHarness serves one object set from both the local informers and the
// remote fake clientset, with every provider answering the same values on
// both paths, so the local summary is the oracle for the remote score (R5).
// Failures are injected per request through the mutable fields; requests are
// sequential, and every worker is joined before a handler returns.
type healthHarness struct {
	h      *Handler
	remote *fake.Clientset
	ra     *fakeRemoteAlerts
	cp     *stubControlPlane
	cert   *stubCertExpiry

	mu        sync.Mutex
	failing   map[string]error // list resource → error every remote list of it returns
	panicking map[string]bool  // list resource → the remote list panics
	truncate  map[string]bool  // list resource → every page claims there is more
}

func newHealthHarness(t *testing.T, objs ...runtime.Object) *healthHarness {
	t.Helper()
	h, remote := remoteDashboardHandler(t, objs, objs...)
	hh := &healthHarness{
		h:      h,
		remote: remote,
		ra:     &fakeRemoteAlerts{active: 5, critical: 2},
		cp: &stubControlPlane{states: ControlPlaneStates{
			SchedulerState: ComponentUp, ControllerManagerState: ComponentUnscraped, EtcdState: ComponentDown,
		}},
		cert:      &stubCertExpiry{remote: CertExpiry{Warning: 3, Critical: 1}},
		failing:   map[string]error{},
		panicking: map[string]bool{},
		truncate:  map[string]bool{},
	}
	// The local store answers the same counts as the cluster's Alertmanager.
	h.Alerts = &fakeAlertCounter{active: 5, critical: 2}
	h.RemoteAlerts = hh.ra
	h.ControlPlane = hh.cp
	h.CertExpiry = hh.cert
	remote.PrependReactor("list", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		res := a.GetResource().Resource
		hh.mu.Lock()
		err, fails := hh.failing[res]
		panics := hh.panicking[res]
		hh.mu.Unlock()
		switch {
		case panics:
			panic("boom")
		case fails:
			return true, nil, err
		}
		return false, nil, nil
	})
	return hh
}

// reset clears every injected failure and restores the providers' answers.
func (hh *healthHarness) reset() {
	hh.mu.Lock()
	hh.failing = map[string]error{}
	hh.panicking = map[string]bool{}
	hh.mu.Unlock()
	hh.ra.err, hh.ra.panics, hh.ra.blocks = nil, false, false
	hh.cp.err, hh.cp.panics, hh.cp.blocks = nil, false, false
	hh.cert.remote, hh.cert.panics = CertExpiry{Warning: 3, Critical: 1}, false
	hh.h.AccessChecker = NewAlwaysAllowAccessChecker()
}

func (hh *healthHarness) failList(resource string, err error) {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	hh.failing[resource] = err
}

func (hh *healthHarness) panicList(resource string) {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	hh.panicking[resource] = true
}

// remoteBody serves the remote summary and returns the raw body and the
// decoded summary.
func (hh *healthHarness) remoteBody(t *testing.T) (string, DashboardSummary) {
	t.Helper()
	rr := callRemoteDashboard(t, hh.h, "coverage=1")
	if rr.Code != http.StatusOK {
		t.Fatalf("remote: status = %d: %s", rr.Code, rr.Body.String())
	}
	return rr.Body.String(), decodeDashboard(t, rr)
}

// healthJSON returns the raw "health" member of a summary body.
func healthJSON(t *testing.T, body string) string {
	t.Helper()
	var b struct {
		Data struct {
			Health json.RawMessage `json:"health"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return string(b.Data.Health)
}

// localHealthFor serves the local summary with the given denied resources and
// returns its raw health member: the oracle for the remote score.
func (hh *healthHarness) localHealthFor(t *testing.T, denied []string) string {
	t.Helper()
	prev := hh.h.AccessChecker
	hh.h.AccessChecker = NewDenyResourcesAccessChecker(denied...)
	defer func() { hh.h.AccessChecker = prev }()
	return string(localHealthJSON(t, hh.h))
}

// assertScored fails unless the remote summary drew a score with an ok
// health row.
func assertScored(t *testing.T, s DashboardSummary) {
	t.Helper()
	if s.Health == nil {
		t.Fatalf("Health = nil, want a score; health row %+v", coverageRow(t, s, "health"))
	}
	row := coverageRow(t, s, "health")
	if row.Status != "ok" || row.ReasonCode != "ok" || row.ObservedAt == "" || row.Detail != "" {
		t.Errorf("health row = %+v, want ok/ok with observedAt and no detail", row)
	}
}

// assertNoScore fails unless the remote summary withheld the score: an
// explicit null and an unavailable row with the given reason, whose detail
// names exactly the given inputs (by fragment, in order).
func assertNoScore(t *testing.T, body string, s DashboardSummary, reason string, wantInputs ...string) {
	t.Helper()
	if s.Health != nil {
		t.Errorf("Health = %+v, want nil: an incomplete read must never be scored", s.Health)
	}
	if !strings.Contains(body, `"health":null`) {
		t.Errorf("body lacks \"health\":null: %s", body)
	}
	row := coverageRow(t, s, "health")
	if row.Status != "unavailable" || row.ReasonCode != reason || row.ObservedAt != "" {
		t.Errorf("health row = %+v, want unavailable/%s without observedAt", row, reason)
	}
	assertDetailNames(t, row.Detail, wantInputs...)
}

// healthInputFragments is how the health detail names each input, in the
// fixed signal order. Each fragment is followed by the cause in parentheses.
var healthInputFragments = []string{
	"nodes (", "deployments (", "statefulsets (", "daemonsets (", "pods (",
	"pod disruption budgets (", "alerts (", "certificates (",
	"persistent volume claims (", "storage classes (", "control plane (",
}

// assertDetailNames fails unless detail names every wanted input in order
// and no other input.
func assertDetailNames(t *testing.T, detail string, want ...string) {
	t.Helper()
	wanted := map[string]bool{}
	pos := -1
	for _, w := range want {
		wanted[w] = true
		i := strings.Index(detail, w)
		if i < 0 {
			t.Errorf("health detail %q does not name %q", detail, w)
			continue
		}
		if i < pos {
			t.Errorf("health detail %q names %q out of signal order", detail, w)
		}
		pos = i
	}
	for _, f := range healthInputFragments {
		if !wanted[f] && strings.Contains(detail, f) {
			t.Errorf("health detail %q names %q, which did complete", detail, f)
		}
	}
}

// serverError is a list failure that is neither a refusal nor a deadline.
func serverError(resource string) error {
	return apierrors.NewInternalError(fmt.Errorf("etcd unavailable for %s", resource))
}

// healthFixture is the representative fixture: every signal resolves, with
// deductions on each.
func healthFixture() []runtime.Object {
	return append(fixtureNodes(), fixtureNonNodes()...)
}

// ── Scenarios ──────────────────────────────────────────────────────────────

func TestRemoteHealth_AllReadsCompleteMatchesLocalSignals(t *testing.T) {
	objs := healthFixture()
	// The oracle: the local path over the same objects, with its own alert
	// store answering differently from the cluster's Alertmanager.
	oracle, _ := testHandler(t, objs...)
	oracle.Alerts = &fakeAlertCounter{active: 9, critical: 7}
	oracle.CertExpiry = &stubCertExpiry{}
	oracle.ControlPlane = &stubControlPlane{states: ControlPlaneStates{
		SchedulerState: ComponentUp, ControllerManagerState: ComponentUnscraped, EtcdState: ComponentDown,
	}}
	var local ClusterHealth
	if err := json.Unmarshal(localHealthJSON(t, oracle), &local); err != nil {
		t.Fatalf("decode local health: %v", err)
	}

	// The remote handler's informers hold a different, unhealthy cluster, so
	// any informer read on the remote path shows in the score.
	h, _ := remoteDashboardHandler(t,
		[]runtime.Object{nodeWith("l-n1", corev1.ConditionFalse), podWaiting("l-p", corev1.PodRunning, waitingReasonCrashLoopBackOff, false)},
		objs...)
	h.Alerts = &fakeAlertCounter{active: 9, critical: 7}
	h.RemoteAlerts = &fakeRemoteAlerts{active: 2, critical: 1}
	h.CertExpiry = &stubCertExpiry{remote: CertExpiry{Warning: 3, Critical: 1}}
	h.ControlPlane = oracle.ControlPlane

	s := remoteSummaryOK(t, h)
	assertScored(t, s)

	if len(s.Health.Signals) != len(local.Signals) {
		t.Fatalf("signals = %+v, want the local set %+v", s.Health.Signals, local.Signals)
	}
	for i, got := range s.Health.Signals {
		want := local.Signals[i]
		if got.Name == signalAlerts {
			// Two active alerts, one critical: 100 - 10 - 3.
			want = HealthSignal{Name: signalAlerts, Status: SignalStatusOk, Score: intPtr(87)}
		}
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		if string(gj) != string(wj) {
			t.Errorf("signal %s = %s, want %s", got.Name, gj, wj)
		}
	}
	// The tile and the health input share the one Alertmanager read (R8).
	if s.Alerts != (AlertSummary{Active: 2, Critical: 1}) {
		t.Errorf("alerts tile = %+v, want the Alertmanager's 2/1", s.Alerts)
	}
}

func TestRemoteHealth_PDBDeniedScoresWithoutPDB(t *testing.T) {
	// AE2: a tenant who may not list PodDisruptionBudgets still gets a
	// score, exactly as the local path draws one.
	hh := newHealthHarness(t, healthFixture()...)
	want := hh.localHealthFor(t, []string{"poddisruptionbudgets"})
	hh.h.AccessChecker = NewDenyResourcesAccessChecker("poddisruptionbudgets")

	body, s := hh.remoteBody(t)
	assertScored(t, s)
	if got := healthJSON(t, body); got != want {
		t.Errorf("remote health differs from local\n got: %s\nwant: %s", got, want)
	}
	// The fixture's one violated PDB is not counted.
	for _, r := range s.Health.Reasons {
		if strings.Contains(r, "PDB") {
			t.Errorf("reason %q counts PDBs the user may not list", r)
		}
	}
}

func TestRemoteHealth_StorageDeniedSkipsStorage(t *testing.T) {
	for _, denied := range []string{"persistentvolumeclaims", "storageclasses"} {
		t.Run(denied, func(t *testing.T) {
			hh := newHealthHarness(t, healthFixture()...)
			hh.h.AccessChecker = NewDenyResourcesAccessChecker(denied)

			_, s := hh.remoteBody(t)
			assertScored(t, s)
			got := signalByName(t, *s.Health, signalStorage)
			if got.Status != SignalStatusSkipped || got.Reason != "insufficient permissions" {
				t.Errorf("storage signal = %+v, want skipped for insufficient permissions", got)
			}
		})
	}
}

func TestRemoteHealth_StorageClassListRefusedFailsHealth(t *testing.T) {
	// The permission check allowed it, but the API server refused the list:
	// that is an allowed read that did not complete (R7), not a denial.
	hh := newHealthHarness(t, healthFixture()...)
	hh.failList("storageclasses", apierrors.NewForbidden(storagev1.Resource("storageclasses"), "", errors.New("denied")))

	body, s := hh.remoteBody(t)
	// Every failure is a refusal, so the reason is forbidden.
	assertNoScore(t, body, s, "forbidden", "storage classes (")
}

func TestRemoteHealth_WorkloadKindsDenied(t *testing.T) {
	t.Run("one kind denied: the others are scored", func(t *testing.T) {
		hh := newHealthHarness(t, healthFixture()...)
		hh.h.AccessChecker = NewDenyResourcesAccessChecker("statefulsets")

		_, s := hh.remoteBody(t)
		assertScored(t, s)
		// Deployments 6 of 10 plus daemonsets 2 of 3: 8/13 = 61.5%.
		got := signalByName(t, *s.Health, signalWorkloads)
		if got.Status != SignalStatusOk || got.Score == nil || *got.Score != 62 {
			t.Errorf("workloads signal = %+v, want ok/62 over deployments and daemonsets", got)
		}
	})
	t.Run("every kind denied: unknown, matching local", func(t *testing.T) {
		denied := []string{"deployments", "statefulsets", "daemonsets"}
		hh := newHealthHarness(t, healthFixture()...)
		want := hh.localHealthFor(t, denied)
		hh.h.AccessChecker = NewDenyResourcesAccessChecker(denied...)

		body, s := hh.remoteBody(t)
		assertScored(t, s)
		got := signalByName(t, *s.Health, signalWorkloads)
		if got.Status != SignalStatusUnknown || got.Reason != "insufficient permissions" {
			t.Errorf("workloads signal = %+v, want unknown for insufficient permissions", got)
		}
		if g := healthJSON(t, body); g != want {
			t.Errorf("remote health differs from local\n got: %s\nwant: %s", g, want)
		}
	})
}

func TestRemoteHealth_TruncatedPodReadFailsHealth(t *testing.T) {
	// AE3: more pods than the remote list cap.
	h, remote := remoteDashboardHandler(t, nil, append(fixtureNodes(), fixtureNonNodes()...)...)
	truncatingReactor(remote, "pods", func(n int) runtime.Object {
		return &corev1.PodList{
			ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", n)},
			Items:    []corev1.Pod{*runningPod("default", fmt.Sprintf("p-%d", n))},
		}
	})

	rr := callRemoteDashboard(t, h, "coverage=1")
	body := rr.Body.String()
	s := decodeDashboard(t, rr)
	assertNoScore(t, body, s, "unsupported_platform", "pods (")
	if d := coverageRow(t, s, "health").Detail; !strings.Contains(d, "pods (truncated") {
		t.Errorf("health detail %q does not say the pod read was truncated", d)
	}
	// The tile keeps the pods that were read, as a partial row.
	if s.Pods.Total != k8s.RemoteListMaxPages {
		t.Errorf("pods tile = %+v, want the %d pods read", s.Pods, k8s.RemoteListMaxPages)
	}
}

func TestRemoteHealth_Alerts(t *testing.T) {
	t.Run("unreachable Alertmanager fails health", func(t *testing.T) {
		hh := newHealthHarness(t, healthFixture()...)
		hh.ra.err = fmt.Errorf("%w: dial tcp 203.0.113.7:9093: connection refused", ErrAlertmanagerUnreachable)

		body, s := hh.remoteBody(t)
		assertNoScore(t, body, s, "unreachable", "alerts (")
		if strings.Contains(body, "203.0.113.7") {
			t.Errorf("response leaks the Alertmanager address: %s", body)
		}
	})
	t.Run("no Alertmanager URL skips alerts", func(t *testing.T) {
		hh := newHealthHarness(t, healthFixture()...)
		hh.ra.err = ErrNoAlertmanager

		_, s := hh.remoteBody(t)
		assertScored(t, s)
		got := signalByName(t, *s.Health, signalAlerts)
		if got.Status != SignalStatusSkipped || got.Reason != "alerting not configured" {
			t.Errorf("alerts signal = %+v, want skipped as not configured", got)
		}
	})
}

func TestRemoteHealth_ControlPlane(t *testing.T) {
	t.Run("query timeout with a binding fails health", func(t *testing.T) {
		prev := remoteDashboardBudget
		remoteDashboardBudget = 200 * time.Millisecond
		t.Cleanup(func() { remoteDashboardBudget = prev })

		hh := newHealthHarness(t, healthFixture()...)
		hh.cp.blocks = true

		body, s := hh.remoteBody(t)
		assertNoScore(t, body, s, "unreachable", "control plane (")
		if d := coverageRow(t, s, "health").Detail; !strings.Contains(d, "control plane (timed out)") {
			t.Errorf("health detail %q does not say the control-plane query timed out", d)
		}
	})
	t.Run("no binding skips the control plane", func(t *testing.T) {
		hh := newHealthHarness(t, healthFixture()...)
		hh.cp.err = ErrNoMetricsBinding

		_, s := hh.remoteBody(t)
		assertScored(t, s)
		if got := signalByName(t, *s.Health, signalControlPlane); got.Status != SignalStatusSkipped {
			t.Errorf("controlPlane signal = %+v, want skipped", got)
		}
	})
}

func TestRemoteHealth_Certificates(t *testing.T) {
	t.Run("a server error on the certificate list fails health", func(t *testing.T) {
		hh := newHealthHarness(t, healthFixture()...)
		hh.cert.remote = CertExpiry{State: CertExpiryIncomplete, Cause: serverError("certificates")}

		body, s := hh.remoteBody(t)
		assertNoScore(t, body, s, "unreachable", "certificates (")
	})
	t.Run("a refused ClusterIssuer list skips certificates", func(t *testing.T) {
		// The adapter reports any cert-manager list refused with 403 as a
		// denial (certmanager tests pin that mapping).
		hh := newHealthHarness(t, healthFixture()...)
		hh.cert.remote = CertExpiry{State: CertExpiryDenied}

		_, s := hh.remoteBody(t)
		assertScored(t, s)
		got := signalByName(t, *s.Health, signalCerts)
		if got.Status != SignalStatusSkipped || got.Reason != "insufficient permissions" {
			t.Errorf("certificates signal = %+v, want skipped for insufficient permissions", got)
		}
	})
	t.Run("cert-manager not installed skips certificates", func(t *testing.T) {
		hh := newHealthHarness(t, healthFixture()...)
		hh.cert.remote = CertExpiry{State: CertExpiryNotInstalled}

		_, s := hh.remoteBody(t)
		assertScored(t, s)
		got := signalByName(t, *s.Health, signalCerts)
		if got.Status != SignalStatusSkipped || got.Reason != "cert-manager not installed" {
			t.Errorf("certificates signal = %+v, want skipped as not installed", got)
		}
	})
}

// warmingCertExpiry models the remote certificate snapshot: the first read
// finds none, so its fetch outlasts the request's budget, and fills the
// snapshot that later reads are served from.
type warmingCertExpiry struct {
	mu    sync.Mutex
	warm  bool
	calls int
}

func (w *warmingCertExpiry) ExpiringCountsForCluster(ctx context.Context, _ string, _ *auth.User) (CertExpiry, error) {
	w.mu.Lock()
	w.calls++
	warm := w.warm
	w.warm = true
	w.mu.Unlock()
	if warm {
		return CertExpiry{Warning: 3, Critical: 1}, nil
	}
	<-ctx.Done()
	return CertExpiry{State: CertExpiryIncomplete, Cause: ErrCertCacheNotWarm}, nil
}

func TestRemoteHealth_ColdCertificateFetch(t *testing.T) {
	prev := remoteDashboardBudget
	remoteDashboardBudget = 200 * time.Millisecond
	t.Cleanup(func() { remoteDashboardBudget = prev })

	hh := newHealthHarness(t, healthFixture()...)
	wc := &warmingCertExpiry{}
	hh.h.CertExpiry = wc

	body, s := hh.remoteBody(t)
	assertNoScore(t, body, s, "unreachable", "certificates (")

	// The next poll is served from the snapshot the cold fetch filled.
	_, s = hh.remoteBody(t)
	assertScored(t, s)
	if got := signalByName(t, *s.Health, signalCerts); got.Status != SignalStatusOk {
		t.Errorf("certificates signal = %+v, want ok from the snapshot", got)
	}
	if wc.calls != 2 {
		t.Errorf("certificate source read %d times, want once per request", wc.calls)
	}
}

func TestRemoteHealth_TwoFailuresFirstInOrderWins(t *testing.T) {
	h, remote := remoteDashboardHandler(t, nil, healthFixture()...)
	truncatingReactor(remote, "pods", func(n int) runtime.Object {
		return &corev1.PodList{ListMeta: metav1.ListMeta{Continue: fmt.Sprintf("tok-%d", n)}}
	})
	remote.PrependReactor("list", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, serverError("persistentvolumeclaims")
	})

	rr := callRemoteDashboard(t, h, "coverage=1")
	body := rr.Body.String()
	s := decodeDashboard(t, rr)
	// Pods come before persistent volume claims in signal order.
	assertNoScore(t, body, s, "unsupported_platform", "pods (", "persistent volume claims (")
}

func TestRemoteHealth_ZeroNodesIsUnknownWithOKRow(t *testing.T) {
	objs := fixtureNonNodes()
	hh := newHealthHarness(t, objs...)
	want := hh.localHealthFor(t, nil)

	body, s := hh.remoteBody(t)
	assertScored(t, s)
	if s.Health.Status != HealthStatusUnknown || s.Health.Score != nil {
		t.Errorf("health = %s/%v, want unknown without a score", s.Health.Status, s.Health.Score)
	}
	if got := healthJSON(t, body); got != want {
		t.Errorf("remote health differs from local\n got: %s\nwant: %s", got, want)
	}
}

func TestRemoteHealth_PanicInAnyInputWithholdsScore(t *testing.T) {
	hh := newHealthHarness(t, healthFixture()...)
	cases := map[string]func(){
		"nodes":                  func() { hh.panicList("nodes") },
		"pods":                   func() { hh.panicList("pods") },
		"deployments":            func() { hh.panicList("deployments") },
		"statefulsets":           func() { hh.panicList("statefulsets") },
		"daemonsets":             func() { hh.panicList("daemonsets") },
		"poddisruptionbudgets":   func() { hh.panicList("poddisruptionbudgets") },
		"persistentvolumeclaims": func() { hh.panicList("persistentvolumeclaims") },
		"storageclasses":         func() { hh.panicList("storageclasses") },
		"alerts":                 func() { hh.ra.panics = true },
		"certificates":           func() { hh.cert.panics = true },
		"controlPlane":           func() { hh.cp.panics = true },
	}
	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			hh.reset()
			inject()
			body, s := hh.remoteBody(t)
			if s.Health != nil || !strings.Contains(body, `"health":null`) {
				t.Errorf("Health = %+v, want null after a panic in %s", s.Health, name)
			}
			if row := coverageRow(t, s, "health"); row.Status != "unavailable" {
				t.Errorf("health row = %+v, want unavailable", row)
			}
		})
	}
}

// ── StorageClass permission group ──────────────────────────────────────────

// TestAccessChecker_StorageClassesUseStorageGroup runs a real permission
// check against a fake API server whose RBAC grants StorageClasses only in
// storage.k8s.io, the group they live in.
func TestAccessChecker_StorageClassesUseStorageGroup(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		review := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		attrs := review.Spec.ResourceAttributes
		review.Status.Allowed = attrs.Group == "storage.k8s.io" && attrs.Resource == "storageclasses" && attrs.Verb == "list"
		return true, review, nil
	})
	ac := NewAccessChecker(staticFactory{cs}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	allowed, err := ac.CanAccess(context.Background(), k8s.LocalClusterID, "alice", nil, "list", "storageclasses", "")
	if err != nil || !allowed {
		t.Errorf("CanAccess(list storageclasses) = (%v, %v), want allowed by the storage.k8s.io grant", allowed, err)
	}
}

// staticFactory hands every user the same clientset.
type staticFactory struct{ cs kubernetes.Interface }

func (f staticFactory) ClientForUser(string, []string) (kubernetes.Interface, error) {
	return f.cs, nil
}
