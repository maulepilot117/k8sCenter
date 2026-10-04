package velero

// assurance_service_test.go — hermetic coverage for the Release F U34b
// background assurance collector. Every test here runs without PostgreSQL:
// the store is an in-memory fake that honours the real store's contracts
// (one open row per condition identity, one intent per transition, claims
// that spend attempts), the notification service is a recorder, and the
// Velero reads come from the same fake dynamic client the handler tests
// use. The PostgreSQL-backed AE8 and replica tests live in
// assurance_service_db_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Fake store
// ---------------------------------------------------------------------------

// fakeAssuranceStore is an in-memory assuranceStore. It keeps the two
// uniqueness rules the real schema enforces (one open row per identity, one
// intent per transition) so the collector's reconcile and drain are tested
// against the same contract they meet in production.
type fakeAssuranceStore struct {
	mu sync.Mutex

	leaseErr     error                     // when set, AcquireOrRenewLease returns it
	acquireHook  func(ctx context.Context) // runs inside AcquireOrRenewLease, outside the lock; blocking injection
	lease        store.AssuranceLease
	acquireCalls int
	releaseCalls []releaseCall

	policies         []store.BackupAssurancePolicy
	listPoliciesErr  error
	listPoliciesHook func() // runs inside ListPolicies; panic injection
	listPoliciesCall int

	// Per-method failure and interposition hooks. An error field makes that
	// method return it; a before* hook runs outside the lock just before the
	// method's own logic, so a test can change the store underneath the
	// collector exactly where a second replica would.
	listOpenErr      error
	openErr          error
	beforeOpen       func()
	observeErr       error
	resolveErr       error
	beforeResolve    func(id uuid.UUID)
	claimErr         error
	markDeliveredErr error
	markFailedErr    error
	pruneErr         error

	exceptions map[uuid.UUID]*store.BackupAssuranceException
	deliveries map[uuid.UUID]*store.AssuranceDelivery
	pruneCalls int
}

func newFakeAssuranceStore() *fakeAssuranceStore {
	return &fakeAssuranceStore{
		exceptions: map[uuid.UUID]*store.BackupAssuranceException{},
		deliveries: map[uuid.UUID]*store.AssuranceDelivery{},
	}
}

func (f *fakeAssuranceStore) AcquireOrRenewLease(ctx context.Context, clusterID, holder string, ttl time.Duration) (store.AssuranceLease, error) {
	f.mu.Lock()
	f.acquireCalls++
	hook := f.acquireHook
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
		if err := ctx.Err(); err != nil {
			return store.AssuranceLease{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leaseErr != nil {
		return store.AssuranceLease{}, f.leaseErr
	}
	now := time.Now()
	if f.lease.Holder != holder {
		f.lease = store.AssuranceLease{ClusterID: clusterID, Holder: holder, Fence: f.lease.Fence + 1, AcquiredAt: now}
	}
	f.lease.RenewedAt = now
	f.lease.ExpiresAt = now.Add(ttl)
	return f.lease, nil
}

// releaseCall records the context ReleaseLease was given AS IT WAS at call
// time: the collector cancels it on return, so inspecting it later would
// always show a cancelled context.
type releaseCall struct {
	err         error
	hasDeadline bool
}

func (f *fakeAssuranceStore) ReleaseLease(ctx context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, hasDeadline := ctx.Deadline()
	f.releaseCalls = append(f.releaseCalls, releaseCall{err: ctx.Err(), hasDeadline: hasDeadline})
	return nil
}

func (f *fakeAssuranceStore) ListPolicies(ctx context.Context, clusterID string) ([]store.BackupAssurancePolicy, error) {
	if err := ctx.Err(); err != nil { // pgx fails fast on a done context
		return nil, err
	}
	f.mu.Lock()
	f.listPoliciesCall++
	hook, err := f.listPoliciesHook, f.listPoliciesErr
	var out []store.BackupAssurancePolicy
	for _, p := range f.policies {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (f *fakeAssuranceStore) ListOpenExceptions(ctx context.Context, clusterID string) ([]store.BackupAssuranceException, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listOpenErr != nil {
		return nil, f.listOpenErr
	}
	var out []store.BackupAssuranceException
	for _, e := range f.exceptions {
		if e.ClusterID == clusterID && e.State == store.AssuranceStateOpen {
			out = append(out, *e)
		}
	}
	return out, nil
}

func (f *fakeAssuranceStore) findOpenLocked(e store.BackupAssuranceException) *store.BackupAssuranceException {
	for _, x := range f.exceptions {
		if x.State == store.AssuranceStateOpen && x.ClusterID == e.ClusterID && x.SubjectKind == e.SubjectKind &&
			x.SubjectNamespace == e.SubjectNamespace && x.SubjectName == e.SubjectName && x.SubjectUID == e.SubjectUID && x.Condition == e.Condition {
			return x
		}
	}
	return nil
}

func (f *fakeAssuranceStore) enqueueLocked(exceptionID uuid.UUID, transition string) {
	id := uuid.NewSHA1(exceptionID, []byte(transition))
	f.deliveries[id] = &store.AssuranceDelivery{ID: id, ExceptionID: exceptionID, Transition: transition, State: store.AssuranceDeliveryPending, CreatedAt: time.Now()}
}

func (f *fakeAssuranceStore) OpenExceptionAndEnqueue(ctx context.Context, e store.BackupAssuranceException) (store.BackupAssuranceException, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.BackupAssuranceException{}, false, err
	}
	f.mu.Lock()
	hook, err := f.beforeOpen, f.openErr
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return store.BackupAssuranceException{}, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if e.ID == uuid.Nil || e.OpenedAt.IsZero() || (e.SubjectKind == store.ScopeSchedule && e.SubjectUID == "") {
		return store.BackupAssuranceException{}, false, store.ErrAssuranceExceptionInvalid
	}
	if len(e.Detail) > 0 && !json.Valid(e.Detail) {
		return store.BackupAssuranceException{}, false, store.ErrAssuranceExceptionInvalid
	}
	if !slices.ContainsFunc(f.policies, func(p store.BackupAssurancePolicy) bool { return p.ID == e.PolicyID && p.ClusterID == e.ClusterID }) {
		return store.BackupAssuranceException{}, false, store.ErrAssurancePolicyNotFound
	}
	if existing := f.findOpenLocked(e); existing != nil {
		return *existing, false, nil
	}
	e.State = store.AssuranceStateOpen
	e.ObservationCount = 1
	e.LastObservedAt = e.OpenedAt
	if len(e.Detail) == 0 {
		e.Detail = []byte(`{}`)
	}
	stored := e
	f.exceptions[e.ID] = &stored
	f.enqueueLocked(e.ID, store.AssuranceTransitionOpened)
	return stored, true, nil
}

// ObserveException mirrors the real store's rules: an unknown severity or a
// non-object detail is ErrAssuranceObservationInvalid before anything is
// written; a missing or resolved row is ErrAssuranceExceptionNotOpen; an
// observation older than the stored one is dropped with a nil error.
func (f *fakeAssuranceStore) ObserveException(_ context.Context, id uuid.UUID, observedAt time.Time, severity string, lastSuccessAt *time.Time, detail []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.observeErr != nil {
		return f.observeErr
	}
	switch severity {
	case store.AssuranceSeverityInfo, store.AssuranceSeverityWarning, store.AssuranceSeverityCritical:
	default:
		return fmt.Errorf("%w: unknown severity %q", store.ErrAssuranceObservationInvalid, severity)
	}
	if len(detail) == 0 {
		detail = []byte(`{}`)
	} else if trimmed := bytes.TrimSpace(detail); len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return fmt.Errorf("%w: detail must be a JSON object", store.ErrAssuranceObservationInvalid)
	}
	e, ok := f.exceptions[id]
	if !ok || e.State != store.AssuranceStateOpen {
		return store.ErrAssuranceExceptionNotOpen
	}
	if e.LastObservedAt.After(observedAt) {
		return nil // stale observation of a still-open row: dropped
	}
	e.ObservationCount++
	e.LastObservedAt = observedAt
	e.Severity = severity
	if lastSuccessAt != nil {
		e.LastSuccessAt = lastSuccessAt
	}
	e.Detail = detail
	return nil
}

func (f *fakeAssuranceStore) ResolveExceptionAndEnqueue(_ context.Context, id uuid.UUID, at time.Time, reason string) (bool, error) {
	f.mu.Lock()
	hook, err := f.beforeResolve, f.resolveErr
	f.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	if err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.exceptions[id]
	if !ok || e.State != store.AssuranceStateOpen {
		return false, nil
	}
	e.State = store.AssuranceStateResolved
	e.ResolvedAt = &at
	var detail map[string]any
	if json.Unmarshal(e.Detail, &detail) != nil || detail == nil {
		detail = map[string]any{}
	}
	detail["resolutionReason"] = reason
	e.Detail, _ = json.Marshal(detail)
	f.enqueueLocked(id, store.AssuranceTransitionResolved)
	return true, nil
}

func (f *fakeAssuranceStore) ClaimPendingDeliveries(_ context.Context, clusterID string, maxAttempts, limit int) ([]store.AssuranceDeliveryJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	var jobs []store.AssuranceDeliveryJob
	for _, d := range f.deliveries {
		e := f.exceptions[d.ExceptionID]
		if d.State != store.AssuranceDeliveryPending || d.Attempts >= maxAttempts || e == nil || e.ClusterID != clusterID {
			continue
		}
		jobs = append(jobs, store.AssuranceDeliveryJob{Delivery: *d, Exception: *e})
	}
	slices.SortFunc(jobs, func(a, b store.AssuranceDeliveryJob) int {
		if c := a.Delivery.CreatedAt.Compare(b.Delivery.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Delivery.ID.String(), b.Delivery.ID.String())
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	for i := range jobs {
		f.deliveries[jobs[i].Delivery.ID].Attempts++
		jobs[i].Delivery.Attempts++
	}
	return jobs, nil
}

func (f *fakeAssuranceStore) MarkDelivered(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markDeliveredErr != nil {
		return f.markDeliveredErr
	}
	d, ok := f.deliveries[id]
	if !ok || d.State != store.AssuranceDeliveryPending {
		return store.ErrAssuranceDeliveryNotPending
	}
	now := time.Now()
	d.State, d.DeliveredAt, d.LastError = store.AssuranceDeliveryDelivered, &now, ""
	return nil
}

func (f *fakeAssuranceStore) MarkDeliveryFailed(_ context.Context, id uuid.UUID, errMsg string, maxAttempts int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markFailedErr != nil {
		return f.markFailedErr
	}
	d, ok := f.deliveries[id]
	if !ok || d.State != store.AssuranceDeliveryPending {
		return store.ErrAssuranceDeliveryNotPending
	}
	d.LastError = errMsg
	if d.Attempts >= maxAttempts {
		d.State = store.AssuranceDeliveryFailed
	}
	return nil
}

func (f *fakeAssuranceStore) PruneResolved(_ context.Context, _ time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneCalls++
	if f.pruneErr != nil {
		return 0, f.pruneErr
	}
	return 0, nil
}

// --- fake store accessors ---------------------------------------------------

func (f *fakeAssuranceStore) exceptionsWhere(keep func(store.BackupAssuranceException) bool) []store.BackupAssuranceException {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.BackupAssuranceException
	for _, e := range f.exceptions {
		if keep(*e) {
			out = append(out, *e)
		}
	}
	return out
}

func (f *fakeAssuranceStore) open() []store.BackupAssuranceException {
	return f.exceptionsWhere(func(e store.BackupAssuranceException) bool { return e.State == store.AssuranceStateOpen })
}

func (f *fakeAssuranceStore) all() []store.BackupAssuranceException {
	return f.exceptionsWhere(func(store.BackupAssuranceException) bool { return true })
}

func (f *fakeAssuranceStore) deliveryList() []store.AssuranceDelivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.AssuranceDelivery
	for _, d := range f.deliveries {
		out = append(out, *d)
	}
	return out
}

func (f *fakeAssuranceStore) counts() (acquire, listPolicies, prune int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acquireCalls, f.listPoliciesCall, f.pruneCalls
}

func (f *fakeAssuranceStore) releases() []releaseCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.releaseCalls)
}

// seedOpen inserts an already-open exception for a schedule subject, as a
// previous tick (or a previous process) would have left it.
func (f *fakeAssuranceStore) seedOpen(t *testing.T, clusterID string, policyID uuid.UUID, ns, name, uid string, cond store.AssuranceCondition) uuid.UUID {
	t.Helper()
	got, opened, err := f.OpenExceptionAndEnqueue(context.Background(), store.BackupAssuranceException{
		ID: uuid.New(), ClusterID: clusterID, PolicyID: policyID, SubjectKind: store.ScopeSchedule,
		SubjectNamespace: ns, SubjectName: name, SubjectUID: uid, Condition: cond,
		Severity: store.AssuranceSeverityWarning, OpenedAt: time.Now().Add(-time.Hour),
	})
	if err != nil || !opened {
		t.Fatalf("seedOpen: opened=%v err=%v", opened, err)
	}
	// The seed's own intent is history: mark it delivered so tests count
	// only what the collector under test produces.
	if err := f.MarkDelivered(context.Background(), uuid.NewSHA1(got.ID, []byte(store.AssuranceTransitionOpened))); err != nil {
		t.Fatalf("seedOpen: mark delivered: %v", err)
	}
	return got.ID
}

// ---------------------------------------------------------------------------
// Fake emitter
// ---------------------------------------------------------------------------

type fakeEmitter struct {
	mu     sync.Mutex
	result notifications.EmitResult
	err    error
	sent   []notifications.Notification
}

func (f *fakeEmitter) EmitSync(_ context.Context, n notifications.Notification) (notifications.EmitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	if f.err != nil {
		return notifications.EmitFailed, f.err
	}
	return f.result, nil
}

func (f *fakeEmitter) notifications() []notifications.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const testAssuranceCluster = "test-assurance-cluster"

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// scheduleObj is a Velero Schedule with the identity fields the evaluator
// keys on: UID and creationTimestamp.
func asrSchedule(name, uid, cronExpr string, paused bool, created time.Time) *unstructured.Unstructured {
	u := obj("Schedule", name, map[string]any{"schedule": cronExpr, "paused": paused}, map[string]any{"phase": "Enabled"})
	u.SetUID(types.UID(uid))
	u.SetCreationTimestamp(metav1.NewTime(created))
	return u
}

// backupObj is a Backup produced by scheduleName that reached phase at done.
func asrBackup(name, scheduleName, phase string, done time.Time) *unstructured.Unstructured {
	u := obj("Backup", name, nil, map[string]any{
		"phase":               phase,
		"startTimestamp":      rfc3339(done.Add(-time.Minute)),
		"completionTimestamp": rfc3339(done),
	})
	u.SetUID(types.UID(name + "-uid"))
	u.SetLabels(map[string]string{scheduleNameLabel: scheduleName})
	u.SetCreationTimestamp(metav1.NewTime(done.Add(-time.Minute)))
	return u
}

func asrBSL(name string, def bool, phase, message string) *unstructured.Unstructured {
	return obj("BackupStorageLocation", name, map[string]any{"default": def, "provider": "aws"}, map[string]any{"phase": phase, "message": message})
}

func testSchedulePolicy(clusterID, name string) store.BackupAssurancePolicy {
	return store.BackupAssurancePolicy{
		ID: uuid.New(), ClusterID: clusterID, ScopeKind: store.ScopeSchedule, ScopeNamespace: veleroNamespace, ScopeName: name,
		MaxAge: 24 * time.Hour, Grace: time.Hour, TreatPartialAs: store.AssuranceTreatPartialAsFailure,
		AlertOnPaused: true, Enabled: true, CreatedBy: "local:test",
	}
}

// overdueCluster is a daily schedule whose last success is three days old,
// with an available default location: exactly one overdue finding.
func overdueCluster(now time.Time) []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		asrSchedule("daily", "uid-daily", "0 2 * * *", false, now.Add(-10*24*time.Hour)),
		asrBackup("daily-old", "daily", "Completed", now.Add(-3*24*time.Hour)),
		asrBSL("default", true, "Available", ""),
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type assuranceHarness struct {
	*harness
	svc  *AssuranceService
	st   *fakeAssuranceStore
	emit *fakeEmitter
	mu   sync.Mutex
	now  time.Time
}

// newAssuranceHarness builds a collector over the fake store and emitter,
// reading a local fake cluster that serves objs. detected=false makes the
// Discoverer report Velero absent.
func newAssuranceHarness(t *testing.T, detected bool, objs ...*unstructured.Unstructured) *assuranceHarness {
	t.Helper()
	hs := newLocalHarness(t, objs...)
	if !detected {
		hs.h.Discoverer = &Discoverer{logger: hs.h.Logger, status: VeleroStatus{Detected: false, LastChecked: time.Now().UTC()}}
	}
	ah := &assuranceHarness{harness: hs, st: newFakeAssuranceStore(), emit: &fakeEmitter{result: notifications.EmitPersisted}, now: time.Now().UTC()}
	ah.svc = newAssuranceServiceWith(hs.h, hs.h.Discoverer, ah.st, ah.emit, testAssuranceCluster, "holder-a", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ah.svc.now = func() time.Time {
		ah.mu.Lock()
		defer ah.mu.Unlock()
		return ah.now
	}
	return ah
}

func (ah *assuranceHarness) advance(d time.Duration) {
	ah.mu.Lock()
	ah.now = ah.now.Add(d)
	ah.mu.Unlock()
}

func (ah *assuranceHarness) addPolicy(p store.BackupAssurancePolicy) store.BackupAssurancePolicy {
	ah.st.mu.Lock()
	ah.st.policies = append(ah.st.policies, p)
	ah.st.mu.Unlock()
	return p
}

func (ah *assuranceHarness) resetCache() { resetLocalCache(ah.h) }

// resetLocalCache drops the handler's 30 s read cache so the next tick sees
// a changed fake cluster, exactly as afterWrite does after a handler write.
func resetLocalCache(h *Handler) {
	h.cacheMu.Lock()
	h.cacheGen++
	h.cachedData = nil
	h.cacheMu.Unlock()
}

func (ah *assuranceHarness) tick(t *testing.T) {
	t.Helper()
	ah.svc.tick(t.Context())
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// startInBackground runs Start on a short interval and returns a function
// that cancels it and waits for it to return.
func (ah *assuranceHarness) startInBackground(t *testing.T, ctx context.Context) (stop func()) {
	t.Helper()
	ah.svc.interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ah.svc.Start(ctx)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Start did not return after cancellation")
		}
	}
}

func conditionsOpen(es []store.BackupAssuranceException) []store.AssuranceCondition {
	var out []store.AssuranceCondition
	for _, e := range es {
		out = append(out, e.Condition)
	}
	slices.Sort(out)
	return out
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

func TestAssurance_StartReturnsImmediatelyWithNilStore(t *testing.T) {
	hs := newLocalHarness(t)
	svc := NewAssuranceService(hs.h, hs.h.Discoverer, nil, nil, testAssuranceCluster, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.Snapshot().Enabled {
		t.Fatal("Enabled = true with a nil store; want false")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Start(context.Background()) // never-cancelled: must still return
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked with a nil store")
	}
}

func TestAssurance_DefaultHolderIsHostAndPid(t *testing.T) {
	hs := newLocalHarness(t)
	svc := newAssuranceServiceWith(hs.h, hs.h.Discoverer, newFakeAssuranceStore(), &fakeEmitter{}, testAssuranceCluster, "", nil)
	h := svc.Snapshot().Holder
	if h == "" || !strings.HasSuffix(h, "-"+strconv.Itoa(os.Getpid())) {
		t.Fatalf("Holder = %q; want <hostname>-%d", h, os.Getpid())
	}
}

func TestAssurance_StartReturnsOnContextCancel(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	stop := ah.startInBackground(t, context.Background())
	waitFor(t, "first tick", func() bool { a, _, _ := ah.st.counts(); return a >= 1 })
	stop()
}

func TestAssurance_ShutdownReleasesLeaseOnBoundedContext(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	stop := ah.startInBackground(t, context.Background())
	waitFor(t, "first tick", func() bool { a, _, _ := ah.st.counts(); return a >= 1 })
	stop()
	rel := ah.st.releases()
	if len(rel) != 1 {
		t.Fatalf("ReleaseLease calls = %d, want 1", len(rel))
	}
	if rel[0].err != nil {
		t.Errorf("release ran on a cancelled context: %v", rel[0].err)
	}
	if !rel[0].hasDeadline {
		t.Errorf("release context has no deadline; want a bounded one")
	}
}

func TestAssurance_ShutdownWithAlreadyCancelledContextStillReleases(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ah.svc.Start(ctx)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return on an already-cancelled context")
	}
	if a, _, _ := ah.st.counts(); a != 0 {
		t.Errorf("tick ran %d times on a cancelled context; want 0", a)
	}
	rel := ah.st.releases()
	if len(rel) != 1 || rel[0].err != nil || !rel[0].hasDeadline {
		t.Fatalf("release calls = %+v; want exactly one on a live, bounded context", rel)
	}
}

func TestAssurance_PanicMidTickIsRecoveredAndLoopContinues(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.st.listPoliciesHook = func() {
		_, calls, _ := ah.st.counts()
		if calls == 1 {
			panic("injected: first tick panics")
		}
	}
	stop := ah.startInBackground(t, context.Background())
	waitFor(t, "ticks after the panic", func() bool { _, calls, _ := ah.st.counts(); return calls >= 3 })
	stop()
}

func TestAssurance_StoreErrorMidTickDoesNotPanicOrStopTheLoop(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.st.mu.Lock()
	ah.st.listPoliciesErr = errors.New("pg: connection reset")
	ah.st.mu.Unlock()

	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError == "" || !s.LastRunAt.IsZero() {
		t.Fatalf("after a failing tick: LastError=%q LastRunAt=%v; want an error and no completed run", s.LastError, s.LastRunAt)
	}
	if s := ah.svc.Snapshot(); strings.Contains(s.LastError, "connection reset") {
		t.Errorf("LastError %q carries the raw store error; want an operation label only", s.LastError)
	}

	ah.st.mu.Lock()
	ah.st.listPoliciesErr = nil
	ah.st.mu.Unlock()
	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError != "" || s.LastRunAt.IsZero() {
		t.Fatalf("after a recovering tick: LastError=%q LastRunAt=%v; want cleared error and a completed run", s.LastError, s.LastRunAt)
	}
}

func TestAssurance_LeaseLossSkipsWorkWithoutError(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.st.mu.Lock()
	ah.st.leaseErr = store.ErrLeaseHeldByOther
	ah.st.mu.Unlock()

	ah.tick(t)
	if _, lp, _ := ah.st.counts(); lp != 0 {
		t.Errorf("ListPolicies called %d times without the lease; want 0", lp)
	}
	if n := len(ah.localDyn().Actions()); n != 0 {
		t.Errorf("%d Kubernetes reads without the lease; want 0", n)
	}
	s := ah.svc.Snapshot()
	if s.LeaseHeld || s.LastError != "" || !s.LastRunAt.IsZero() || s.LastTickAt.IsZero() {
		t.Errorf("snapshot after lost lease = %+v; want LeaseHeld=false, no error, no run, a tick", s)
	}
}

func TestAssurance_LeaseAcquisitionErrorIsRecorded(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.st.mu.Lock()
	ah.st.leaseErr = errors.New("pg: down")
	ah.st.mu.Unlock()
	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError == "" || s.LeaseHeld {
		t.Errorf("snapshot = %+v; want an error and LeaseHeld=false", s)
	}
}

// ---------------------------------------------------------------------------
// Reconcile
// ---------------------------------------------------------------------------

func TestAssurance_NoPoliciesMeansNoExceptionsAndNoNotifications(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.tick(t)
	if n := len(ah.localDyn().Actions()); n != 0 {
		t.Errorf("%d Kubernetes reads with no policies; want 0", n)
	}
	if got := ah.st.all(); len(got) != 0 {
		t.Errorf("exceptions = %d, want 0", len(got))
	}
	if got := ah.emit.notifications(); len(got) != 0 {
		t.Errorf("notifications = %d, want 0", len(got))
	}
	s := ah.svc.Snapshot()
	if s.LastRunAt.IsZero() || s.LastCollection != "" || s.PolicyCount != 0 {
		t.Errorf("snapshot = %+v; want a completed run, no collection, PolicyCount 0", s)
	}
}

func TestAssurance_OverdueOpensOneExceptionAndDeliversOnce(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	p := ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))

	ah.tick(t)
	open := ah.st.open()
	if len(open) != 1 || open[0].Condition != store.ConditionOverdue {
		t.Fatalf("open = %+v; want one overdue", open)
	}
	e := open[0]
	if e.PolicyID != p.ID || e.SubjectUID != "uid-daily" || e.SubjectNamespace != veleroNamespace || e.SubjectName != "daily" || e.ObservationCount != 1 {
		t.Errorf("exception = %+v; want policy %s, uid-daily, velero/daily, 1 observation", e, p.ID)
	}
	var d Detail
	if err := json.Unmarshal(e.Detail, &d); err != nil || d.LastSuccessAt == nil || d.LastOutcome != OutcomeSuccess {
		t.Errorf("detail = %s (%v); want the finding's detail with lastSuccessAt", e.Detail, err)
	}
	if e.LastSuccessAt == nil {
		t.Errorf("LastSuccessAt not carried onto the row")
	}
	ds := ah.st.deliveryList()
	if len(ds) != 1 || ds[0].State != store.AssuranceDeliveryDelivered || ds[0].Transition != store.AssuranceTransitionOpened {
		t.Fatalf("deliveries = %+v; want one delivered 'opened'", ds)
	}
	sent := ah.emit.notifications()
	if len(sent) != 1 || sent[0].Title != TitleBackupOverdue || sent[0].Severity != notifications.SeverityWarning {
		t.Fatalf("notifications = %+v; want one %q warning", sent, TitleBackupOverdue)
	}
	s := ah.svc.Snapshot()
	if s.LastCollection != CollectionOK || s.PolicyCount != 1 || s.FindingCount != 1 || !s.LeaseHeld || s.LeaseHolder != "holder-a" {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestAssurance_RepeatedTicksObserveWithoutReEmitting(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	for range 4 {
		ah.advance(time.Minute)
		ah.tick(t)
	}
	all := ah.st.all()
	if len(all) != 1 || all[0].ObservationCount != 4 || all[0].State != store.AssuranceStateOpen {
		t.Fatalf("exceptions = %+v; want one open row observed 4 times", all)
	}
	if n := len(ah.emit.notifications()); n != 1 {
		t.Errorf("notifications = %d, want 1", n)
	}
	if n := len(ah.st.deliveryList()); n != 1 {
		t.Errorf("deliveries = %d, want 1", n)
	}
}

func TestAssurance_FreshSuccessResolvesAndEmitsResolution(t *testing.T) {
	now := time.Now()
	ah := newAssuranceHarness(t, true, overdueCluster(now)...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)

	if err := ah.localDyn().Tracker().Create(BackupGVR, asrBackup("daily-new", "daily", "Completed", now.Add(-10*time.Minute)), veleroNamespace); err != nil {
		t.Fatal(err)
	}
	ah.resetCache()
	ah.advance(time.Minute)
	ah.tick(t)

	all := ah.st.all()
	if len(all) != 1 || all[0].State != store.AssuranceStateResolved {
		t.Fatalf("exceptions = %+v; want the one row resolved", all)
	}
	var detail map[string]any
	_ = json.Unmarshal(all[0].Detail, &detail)
	if detail["resolutionReason"] != store.AssuranceResolutionConditionCleared {
		t.Errorf("resolutionReason = %v, want %q", detail["resolutionReason"], store.AssuranceResolutionConditionCleared)
	}
	sent := ah.emit.notifications()
	if len(sent) != 2 {
		t.Fatalf("notifications = %d, want 2 (opened + resolved): %+v", len(sent), sent)
	}
	res := sent[1]
	if res.Title != TitleBackupResolvedPrefix+TitleBackupOverdue || res.Severity != notifications.SeverityInfo {
		t.Errorf("resolution notification = %+v; want %q info", res, TitleBackupResolvedPrefix+TitleBackupOverdue)
	}
	if !strings.Contains(res.Message, "not demonstrated recoverability") {
		t.Errorf("resolution message %q lacks the honesty clause", res.Message)
	}
	if ah.st.open() != nil {
		t.Errorf("open rows remain after resolution")
	}
}

func TestAssurance_DeletedScheduleResolvesAsSubjectAbsent(t *testing.T) {
	now := time.Now()
	ah := newAssuranceHarness(t, true, overdueCluster(now)...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)

	if err := ah.localDyn().Tracker().Delete(ScheduleGVR, veleroNamespace, "daily"); err != nil {
		t.Fatal(err)
	}
	ah.resetCache()
	ah.advance(time.Minute)
	ah.tick(t)

	all := ah.st.all()
	if len(all) != 1 || all[0].State != store.AssuranceStateResolved {
		t.Fatalf("exceptions = %+v; want resolved", all)
	}
	var detail map[string]any
	_ = json.Unmarshal(all[0].Detail, &detail)
	if detail["resolutionReason"] != store.AssuranceResolutionSubjectAbsent {
		t.Errorf("resolutionReason = %v, want %q", detail["resolutionReason"], store.AssuranceResolutionSubjectAbsent)
	}
	if sent := ah.emit.notifications(); len(sent) != 2 || !strings.Contains(sent[1].Message, "no longer present") {
		t.Errorf("notifications = %+v; want a second, subject-absent resolution", sent)
	}
}

func TestAssurance_RecreatedScheduleOpensAFreshException(t *testing.T) {
	now := time.Now()
	ah := newAssuranceHarness(t, true, overdueCluster(now)...)
	p := ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	// The previous incarnation's exception is open under uid-old.
	ah.st.seedOpen(t, testAssuranceCluster, p.ID, veleroNamespace, "daily", "uid-old", store.ConditionOverdue)

	ah.tick(t)
	all := ah.st.all()
	if len(all) != 2 {
		t.Fatalf("exceptions = %d, want 2 (old resolved, new open): %+v", len(all), all)
	}
	for _, e := range all {
		switch e.SubjectUID {
		case "uid-old":
			if e.State != store.AssuranceStateResolved {
				t.Errorf("old-uid row state = %s, want resolved (subject absent)", e.State)
			}
		case "uid-daily":
			if e.State != store.AssuranceStateOpen {
				t.Errorf("new-uid row state = %s, want open", e.State)
			}
		default:
			t.Errorf("unexpected row %+v", e)
		}
	}
}

func TestAssurance_CollectionFailureLeavesOpenExceptionsOpen(t *testing.T) {
	ah := newAssuranceHarness(t, false) // Velero not detected
	p := ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	id := ah.st.seedOpen(t, testAssuranceCluster, p.ID, veleroNamespace, "daily", "uid-daily", store.ConditionOverdue)

	ah.tick(t)
	open := ah.st.open()
	if got := conditionsOpen(open); !slices.Equal(got, []store.AssuranceCondition{store.ConditionCollectionUnknown, store.ConditionOverdue}) {
		t.Fatalf("open conditions = %v; want the seeded overdue kept open plus collection_unknown", got)
	}
	for _, e := range open {
		if e.ID == id && e.State != store.AssuranceStateOpen {
			t.Errorf("seeded exception was resolved on a failed collection")
		}
	}
	if s := ah.svc.Snapshot(); s.LastCollection != CollectionFailed {
		t.Errorf("LastCollection = %q, want failed", s.LastCollection)
	}
}

func TestAssurance_CollectionFailureOpensOneClusterExceptionNotPerSubject(t *testing.T) {
	ah := newAssuranceHarness(t, false)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "weekly"))
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "monthly"))

	ah.tick(t)
	ah.tick(t)
	open := ah.st.open()
	if len(open) != 1 || open[0].Condition != store.ConditionCollectionUnknown || open[0].SubjectKind != store.ScopeCluster {
		t.Fatalf("open = %+v; want exactly one cluster-scoped collection_unknown", open)
	}
	if sent := ah.emit.notifications(); len(sent) != 1 || sent[0].Title != TitleBackupCollectionUnknown {
		t.Fatalf("notifications = %+v; want one %q", sent, TitleBackupCollectionUnknown)
	}
	if msg := ah.emit.notifications()[0].Message; strings.Contains(strings.ToLower(msg), "no backups") || !strings.Contains(msg, "unknown") {
		t.Errorf("collection_unknown message %q must say unknown, never 'no backups'", msg)
	}
}

func TestAssurance_CollectionRecoveryResolvesClusterUnknown(t *testing.T) {
	ah := newAssuranceHarness(t, false, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)
	if got := conditionsOpen(ah.st.open()); !slices.Equal(got, []store.AssuranceCondition{store.ConditionCollectionUnknown}) {
		t.Fatalf("open = %v, want [collection_unknown]", got)
	}

	ah.h.Discoverer = &Discoverer{logger: ah.h.Logger, status: VeleroStatus{Detected: true, LastChecked: time.Now().UTC()}}
	ah.svc.disc = ah.h.Discoverer
	ah.advance(time.Minute)
	ah.tick(t)
	if got := conditionsOpen(ah.st.open()); !slices.Equal(got, []store.AssuranceCondition{store.ConditionOverdue}) {
		t.Fatalf("open after recovery = %v, want [overdue] (unknown resolved)", got)
	}
}

func TestAssurance_HoldFindingObservesButNeverOpens(t *testing.T) {
	now := time.Now()
	paused := []*unstructured.Unstructured{
		asrSchedule("daily", "uid-daily", "0 2 * * *", true, now.Add(-10*24*time.Hour)),
		asrBackup("daily-old", "daily", "Completed", now.Add(-3*24*time.Hour)),
		asrBSL("default", true, "Available", ""),
	}
	ah := newAssuranceHarness(t, true, paused...)
	p := testSchedulePolicy(testAssuranceCluster, "daily")
	p.AlertOnPaused = false
	ah.addPolicy(p)

	ah.tick(t)
	if got := ah.st.all(); len(got) != 0 {
		t.Fatalf("a paused schedule opened %+v; a held finding must never open", got)
	}

	id := ah.st.seedOpen(t, testAssuranceCluster, p.ID, veleroNamespace, "daily", "uid-daily", store.ConditionOverdue)
	ah.advance(time.Minute)
	ah.tick(t)
	all := ah.st.all()
	if len(all) != 1 || all[0].ID != id || all[0].State != store.AssuranceStateOpen || all[0].ObservationCount != 2 {
		t.Fatalf("exceptions = %+v; want the seeded row still open and observed once more", all)
	}
	var d Detail
	_ = json.Unmarshal(all[0].Detail, &d)
	if d.SuppressedBy != SuppressedByPaused {
		t.Errorf("detail.suppressedBy = %q, want %q", d.SuppressedBy, SuppressedByPaused)
	}
}

func TestAssurance_PolicyDeletedBetweenListAndOpenIsSkipped(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	p := ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	// Make the open fail with policy-not-found by hiding the policy from the
	// opener only: the hook removes it after ListPolicies has returned it.
	ah.st.listPoliciesHook = func() {
		ah.st.mu.Lock()
		ah.st.policies = nil
		ah.st.mu.Unlock()
	}
	_ = p
	ah.tick(t)
	if got := ah.st.all(); len(got) != 0 {
		t.Errorf("exceptions = %+v; want none for a vanished policy", got)
	}
	if s := ah.svc.Snapshot(); s.LastError != "" || s.LastRunAt.IsZero() {
		t.Errorf("snapshot = %+v; a vanished policy is not a tick failure", s)
	}
}

// ---------------------------------------------------------------------------
// Delivery
// ---------------------------------------------------------------------------

func TestAssurance_DeliveryFailureRetriesWithoutRegeneratingCondition(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.emit.err = errors.New("pg: insert failed on host db-internal.svc:5432")

	ah.tick(t)
	ah.advance(time.Minute)
	ah.tick(t)

	if all := ah.st.all(); len(all) != 1 || all[0].ObservationCount != 2 {
		t.Fatalf("exceptions = %+v; want one row, two observations (condition not regenerated)", all)
	}
	ds := ah.st.deliveryList()
	if len(ds) != 1 || ds[0].State != store.AssuranceDeliveryPending || ds[0].Attempts != 2 {
		t.Fatalf("deliveries = %+v; want one pending intent with 2 attempts", ds)
	}
	if ds[0].LastError == "" || strings.Contains(ds[0].LastError, "db-internal") {
		t.Errorf("last_error = %q; want a sanitized reason that does not echo the raw error (CWE-209)", ds[0].LastError)
	}
	if n := len(ah.emit.notifications()); n != 2 {
		t.Errorf("emit attempts = %d, want 2", n)
	}
}

func TestAssurance_DeliveryFailureStopsAtMaxAttempts(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.emit.err = errors.New("boom")

	for range assuranceMaxDeliveryAttempts + 2 {
		ah.advance(time.Minute)
		ah.tick(t)
	}
	ds := ah.st.deliveryList()
	if len(ds) != 1 || ds[0].State != store.AssuranceDeliveryFailed || ds[0].Attempts != assuranceMaxDeliveryAttempts {
		t.Fatalf("deliveries = %+v; want one failed intent after %d attempts", ds, assuranceMaxDeliveryAttempts)
	}
	if n := len(ah.emit.notifications()); n != assuranceMaxDeliveryAttempts {
		t.Errorf("emit attempts = %d, want exactly %d (poison contained)", n, assuranceMaxDeliveryAttempts)
	}
	if all := ah.st.all(); len(all) != 1 || all[0].State != store.AssuranceStateOpen {
		t.Errorf("exceptions = %+v; the failed delivery must not touch the condition", all)
	}
}

func TestAssurance_DedupedEmitIsMarkedDelivered(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.emit.result = notifications.EmitDeduped
	ah.tick(t)
	ds := ah.st.deliveryList()
	if len(ds) != 1 || ds[0].State != store.AssuranceDeliveryDelivered {
		t.Fatalf("deliveries = %+v; a deduped emit means the feed carries it: want delivered", ds)
	}
}

func TestAssurance_DrainStopsOnCancelledContextLeavingRowsClaimable(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ctx, cancel := context.WithCancel(context.Background())
	ah.svc.tick(ctx) // opens the exception and delivers; now seed a second intent
	cancel()
	ah.st.mu.Lock()
	for _, e := range ah.st.exceptions {
		ah.st.enqueueLocked(e.ID, store.AssuranceTransitionResolved)
	}
	ah.st.mu.Unlock()

	if err := ah.svc.drainDeliveries(ctx); err == nil {
		t.Fatal("drain on a cancelled context returned nil; want the context error")
	}
	for _, d := range ah.st.deliveryList() {
		if d.Transition == store.AssuranceTransitionResolved && d.State != store.AssuranceDeliveryPending {
			t.Errorf("delivery %+v was marked on a cancelled context; want pending", d)
		}
	}
	if n := len(ah.emit.notifications()); n != 1 {
		t.Errorf("emit calls = %d; want 1 (nothing sent after cancellation)", n)
	}
}

// ---------------------------------------------------------------------------
// Notification shape
// ---------------------------------------------------------------------------

func TestAssurance_NotificationCarriesSuppressResourceFields(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)
	sent := ah.emit.notifications()
	if len(sent) != 1 {
		t.Fatalf("notifications = %d, want 1", len(sent))
	}
	n := sent[0]
	if !n.SuppressResourceFields {
		t.Error("SuppressResourceFields = false; want true on every assurance notification")
	}
	open := ah.st.open()
	if len(open) != 1 {
		t.Fatalf("open = %d, want 1", len(open))
	}
	if n.Source != notifications.SourceVelero || n.ResourceKind != assuranceResourceKind || n.ResourceNS != veleroNamespace || n.ResourceName != "daily" || n.ClusterID != testAssuranceCluster {
		t.Errorf("notification identity = %+v", n)
	}
	if n.ResourceUID != open[0].ID.String() {
		t.Errorf("ResourceUID = %q; want the exception id %s (the dedup identity is the exception, not the schedule)", n.ResourceUID, open[0].ID)
	}
	if n.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestAssurance_NotificationMessageNeverContainsControllerText(t *testing.T) {
	now := time.Now()
	const bucket = "s3://prod-secret-bucket/velero"
	objs := []*unstructured.Unstructured{
		asrSchedule("daily", "uid-daily", "0 2 * * *", false, now.Add(-10*24*time.Hour)),
		asrBackup("daily-old", "daily", "Completed", now.Add(-10*time.Minute)),
		asrBSL("default", true, "Unavailable", "BackupStorageLocation \"default\" is unavailable: cannot reach "+bucket+": AccessDenied"),
	}
	ah := newAssuranceHarness(t, true, objs...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)

	open := ah.st.open()
	if got := conditionsOpen(open); !slices.Equal(got, []store.AssuranceCondition{store.ConditionLocationUnavailable}) {
		t.Fatalf("open = %v, want [location_unavailable]", got)
	}
	if !strings.Contains(string(open[0].Detail), bucket) {
		t.Fatalf("privileged detail should carry the BSL message for admins; got %s", open[0].Detail)
	}
	sent := ah.emit.notifications()
	if len(sent) != 1 {
		t.Fatalf("notifications = %d, want 1", len(sent))
	}
	for _, field := range []string{sent[0].Title, sent[0].Message} {
		if strings.Contains(field, bucket) || strings.Contains(field, "AccessDenied") {
			t.Errorf("notification text %q leaks controller text", field)
		}
		if strings.Contains(field, "daily") || strings.Contains(field, veleroNamespace) {
			t.Errorf("notification text %q names the subject; identity rides only in the structured fields", field)
		}
	}
}

func TestAssurance_ResourceKindNeverEncodesCondition(t *testing.T) {
	svc := &AssuranceService{now: time.Now}
	conds := []store.AssuranceCondition{store.ConditionOverdue, store.ConditionFailed, store.ConditionPartiallyFailed, store.ConditionPaused,
		store.ConditionNeverRun, store.ConditionLocationUnavailable, store.ConditionCollectionUnknown}
	for _, c := range conds {
		for _, tr := range []string{store.AssuranceTransitionOpened, store.AssuranceTransitionResolved} {
			job := store.AssuranceDeliveryJob{
				Delivery:  store.AssuranceDelivery{Transition: tr},
				Exception: store.BackupAssuranceException{Condition: c, Severity: store.AssuranceSeverityCritical, SubjectKind: store.ScopeSchedule, SubjectNamespace: "ns", SubjectName: "s", SubjectUID: "u"},
			}
			n := svc.notificationFor(job)
			if n.ResourceKind != assuranceResourceKind {
				t.Errorf("%s/%s: ResourceKind = %q, want %q", c, tr, n.ResourceKind, assuranceResourceKind)
			}
			if n.Title == "" || n.Message == "" {
				t.Errorf("%s/%s: empty title or message", c, tr)
			}
			if !n.SuppressResourceFields {
				t.Errorf("%s/%s: SuppressResourceFields = false", c, tr)
			}
			if tr == store.AssuranceTransitionResolved && n.Severity != notifications.SeverityInfo {
				t.Errorf("%s resolved: severity = %s, want info", c, n.Severity)
			}
			if tr == store.AssuranceTransitionOpened && n.Severity != notifications.SeverityCritical {
				t.Errorf("%s opened: severity = %s, want critical (from the row)", c, n.Severity)
			}
		}
	}
	bad := svc.notificationFor(store.AssuranceDeliveryJob{Exception: store.BackupAssuranceException{Condition: "made_up", Severity: "loud"}})
	if bad.Severity != notifications.SeverityWarning || bad.Title == "" {
		t.Errorf("unknown condition/severity = %+v; want a warning with a generic title", bad)
	}
}

// ---------------------------------------------------------------------------
// Design constraints
// ---------------------------------------------------------------------------

func TestAssurance_PruneRunsAtMostHourly(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	prunes := func() int { _, _, p := ah.st.counts(); return p }

	ah.tick(t)
	if prunes() != 1 {
		t.Fatalf("prunes after first tick = %d, want 1", prunes())
	}
	ah.advance(30 * time.Minute)
	ah.tick(t)
	if prunes() != 1 {
		t.Fatalf("prunes after 30m = %d, want 1", prunes())
	}
	ah.advance(31 * time.Minute)
	ah.tick(t)
	if prunes() != 2 {
		t.Fatalf("prunes after 61m = %d, want 2", prunes())
	}
}

func TestAssurance_CreatesNoGoroutinesBeyondStart(t *testing.T) {
	// Static: the collector's own file has no go statement, channel,
	// errgroup or WaitGroup. The only concurrency it touches is inherited
	// from handler.fetchAll, which joins its workers before returning.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "assurance_service.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		if strings.Contains(imp.Path.Value, "errgroup") {
			t.Errorf("assurance_service.go imports %s", imp.Path.Value)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GoStmt:
			t.Errorf("go statement at %s", fset.Position(x.Pos()))
		case *ast.ChanType:
			t.Errorf("channel type at %s", fset.Position(x.Pos()))
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == "sync" && x.Sel.Name == "WaitGroup" {
				t.Errorf("sync.WaitGroup at %s", fset.Position(x.Pos()))
			}
		}
		return true
	})

	// Dynamic: a tick leaves no goroutine behind.
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t) // warm the fetch cache so the measured tick is representative
	baseline := runtime.NumGoroutine()
	ah.resetCache()
	ah.advance(time.Minute)
	ah.tick(t)
	waitFor(t, "goroutine count to return to baseline", func() bool { return runtime.NumGoroutine() <= baseline })
}

func TestAssurance_NeverConstructsARestore(t *testing.T) {
	files, err := filepath.Glob("assurance*.go")
	if err != nil || len(files) < 2 {
		t.Fatalf("glob assurance*.go: %v (%d files)", err, len(files))
	}
	// Identifiers, not text: a doc comment may say what the code must not
	// do. Any reference to the mutating GVRs or handlers, any
	// create/update/patch/delete/apply call, or a Kubernetes client import
	// in a non-test assurance file fails the release boundary.
	forbiddenIdent := map[string]bool{
		"RestoreGVR": true, "DeleteBackupRequestGVR": true, "DownloadRequestGVR": true,
		"HandleCreateRestore": true, "HandleCreateBackup": true, "HandleDeleteBackup": true,
		"HandleCreateSchedule": true, "HandleUpdateSchedule": true, "HandleDeleteSchedule": true, "HandleTriggerSchedule": true,
		"Create": true, "Update": true, "Patch": true, "Delete": true, "Apply": true, "DeleteCollection": true,
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if strings.HasPrefix(imp.Path.Value, `"k8s.io/client-go/`) {
				t.Errorf("%s imports %s; the collector reads only through handler.fetchAll", path, imp.Path.Value)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if forbiddenIdent[x.Name] {
					t.Errorf("%s references %s at %s; Release F is observation only", path, x.Name, fset.Position(x.Pos()))
				}
			}
			return true
		})
	}
}

func TestAssurance_SnapshotIsACopy(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	before := ah.svc.Snapshot()
	ah.tick(t)
	after := ah.svc.Snapshot()
	if !before.LastTickAt.IsZero() || after.LastTickAt.IsZero() {
		t.Errorf("before=%+v after=%+v; Snapshot must reflect ticks and not alias state", before, after)
	}
	if !after.Enabled || after.Holder != "holder-a" {
		t.Errorf("after = %+v", after)
	}
}
