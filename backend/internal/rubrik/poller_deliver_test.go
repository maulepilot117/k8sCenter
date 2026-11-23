package rubrik

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
)

// fakeNamespaces answers Get from created; a name missing from it is not
// found, and err, when set, answers every Get.
type fakeNamespaces struct {
	created map[string]time.Time
	err     error
}

func (f *fakeNamespaces) Get(name string) (*corev1.Namespace, error) {
	if f.err != nil {
		return nil, f.err
	}
	t, ok := f.created[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, name)
	}
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.NewTime(t)}}, nil
}

// fakeNotifier records what it is asked to emit and answers with result,
// or with err when set; byTitle overrides both for one title.
type fakeNotifier struct {
	mu      sync.Mutex
	sent    []notifications.Notification
	result  notifications.EmitResult
	err     error
	byTitle map[string]error
	onEmit  func() // runs before each emit answers
}

func (f *fakeNotifier) EmitSync(_ context.Context, n notifications.Notification) (notifications.EmitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	if f.onEmit != nil {
		f.onEmit()
	}
	if err, ok := f.byTitle[n.Title]; ok {
		return notifications.EmitFailed, err
	}
	if f.err != nil {
		return notifications.EmitFailed, f.err
	}
	return f.result, nil
}

func (f *fakeNotifier) emitted() []notifications.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// memDeliveries is a DeliveryStore over memPollerStore's events, one intent
// per recorded event, with the store's claim and mark rules: a claim is an
// attempt, a delivered or failed intent is never claimed again, and neither
// is a pending one whose attempts reached the limit. Like the database, it
// refuses a call on a finished context.
type memDeliveries struct {
	db        *memPollerStore
	mu        sync.Mutex
	attempts  map[string]int
	state     map[string]string
	lastError map[string]string
	claims    int
	notPend   map[string]bool  // marks that report ErrRubrikDeliveryNotPending
	markErr   map[string]error // marks that fail with a store error
}

func newMemDeliveries(db *memPollerStore) *memDeliveries {
	return &memDeliveries{db: db, attempts: map[string]int{}, state: map[string]string{},
		lastError: map[string]string{}, notPend: map[string]bool{}, markErr: map[string]error{}}
}

// markRefusal is the error a mark of id answers with before it changes
// anything, if any.
func (m *memDeliveries) markRefusal(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.notPend[id] {
		return store.ErrRubrikDeliveryNotPending
	}
	return m.markErr[id]
}

func (m *memDeliveries) ClaimPollerDeliveries(_ context.Context, clusterID string, maxAttempts, limit int) ([]store.RubrikPollerDeliveryJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claims++
	var out []store.RubrikPollerDeliveryJob
	for _, e := range m.db.allEvents() {
		id := "d-" + e.ID
		if e.ClusterID != clusterID || m.state[id] != "" || m.attempts[id] >= maxAttempts || len(out) == limit {
			continue
		}
		m.attempts[id]++
		out = append(out, store.RubrikPollerDeliveryJob{
			Delivery: store.RubrikPollerDelivery{ID: id, EventID: e.ID, State: store.RubrikDeliveryPending, Attempts: m.attempts[id]},
			Event:    e,
		})
	}
	return out, nil
}

func (m *memDeliveries) MarkPollerDelivered(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.markRefusal(ctx, id); err != nil {
		return err
	}
	m.state[id] = store.RubrikDeliveryDelivered
	return nil
}

func (m *memDeliveries) MarkPollerDeliveryFailed(ctx context.Context, id, errMsg string, maxAttempts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.markRefusal(ctx, id); err != nil {
		return err
	}
	m.lastError[id] = errMsg
	if m.attempts[id] >= maxAttempts {
		m.state[id] = store.RubrikDeliveryFailed
	}
	return nil
}

func (m *memDeliveries) claimCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claims
}

// fakeSweep is a SweepStore and an operationRefresher.
type fakeSweep struct {
	mu        sync.Mutex
	ops       []store.RubrikOperation
	listErr   error
	graces    []time.Duration
	refreshed []string
}

func (f *fakeSweep) ExpireApplyingOperations(_ context.Context, _ string, grace time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.graces = append(f.graces, grace)
	return 0, nil
}

func (f *fakeSweep) OpenOperations(context.Context, string) ([]store.RubrikOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ops), f.listErr
}

func (f *fakeSweep) refreshOperation(_ context.Context, op store.RubrikOperation) (store.RubrikOperation, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshed = append(f.refreshed, op.ID)
	return op, ""
}

// withFakes gives p a delivery store, a notifier and a sweep over fakes.
func withFakes(p *Poller, d *memDeliveries, n *fakeNotifier, s *fakeSweep) {
	p.WithDelivery(d, n, &fakeNamespaces{})
	p.sweepStore, p.refresher = s, s
}

func TestNotificationFor_FixedWordingPerKind(t *testing.T) {
	kinds := []string{store.RubrikEventSnapshotFailed, store.RubrikEventComplianceLost, store.RubrikEventComplianceRestored,
		store.RubrikEventConnectionFailing, store.RubrikEventConnectionRestored, store.RubrikEventMonitoringGap}
	if len(pollerNotices) != len(kinds) {
		t.Fatalf("pollerNotices has %d kinds, want %d", len(pollerNotices), len(kinds))
	}
	gapStart, gapEnd := pollerStart.Add(-30*time.Hour), pollerStart.Add(-24*time.Hour)
	titles := map[string]bool{}
	for _, kind := range kinds {
		e := store.RubrikPollerEvent{ID: "ev-" + kind, ClusterID: "remote-1", Kind: kind, RSCObjectID: idNS,
			Namespace: "team-secret", DedupKey: "k", OccurredAt: pollerStart,
			Detail: detail(map[string]any{"objectId": idNS, "gapStart": gapStart, "gapEnd": gapEnd})}
		n := notificationFor(e)
		want := pollerNotices[kind]
		if n.Title != want.title || n.Severity != want.severity || titles[n.Title] {
			t.Errorf("%s: title %q severity %s; want its own fixed wording %q", kind, n.Title, n.Severity, want.title)
		}
		titles[n.Title] = true
		for _, leak := range []string{"team-secret", idNS, "remote-1"} {
			if strings.Contains(n.Title, leak) || strings.Contains(n.Message, leak) {
				t.Errorf("%s: title or message names %q: %q / %q", kind, leak, n.Title, n.Message)
			}
		}
		if n.Source != notifications.SourceRubrik || n.ResourceKind != pollerResourceKind || n.ResourceNS != "team-secret" ||
			n.ResourceName != "" || n.ResourceUID != e.ID || n.ClusterID != "remote-1" || !n.SuppressResourceFields {
			t.Errorf("%s: notification = %+v; want the namespace and cluster in the row, the event id as identity, fields suppressed", kind, n)
		}
	}
	gap := notificationFor(store.RubrikPollerEvent{ID: "g", Kind: store.RubrikEventMonitoringGap,
		Detail: detail(map[string]any{"gapStart": gapStart, "gapEnd": gapEnd})})
	if !strings.Contains(gap.Message, gapStart.Format(time.RFC3339)) || !strings.Contains(gap.Message, gapEnd.Format(time.RFC3339)) {
		t.Errorf("gap message %q does not state the gap (%s to %s)", gap.Message, gapStart, gapEnd)
	}
	if odd := notificationFor(store.RubrikPollerEvent{ID: "x", Kind: "something_new", Namespace: "team-secret"}); strings.Contains(odd.Title+odd.Message, "team-secret") || odd.Title == "" {
		t.Errorf("an unknown kind got %q / %q; want fixed generic wording", odd.Title, odd.Message)
	}
}

func deliveryFixture(t *testing.T) (*pollerTest, *Poller, *memDeliveries, *fakeNotifier, *fakeSweep) {
	t.Helper()
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	d, n, s := newMemDeliveries(fx.db), &fakeNotifier{result: notifications.EmitPersisted}, &fakeSweep{}
	withFakes(p, d, n, s)
	return fx, p, d, n, s
}

// One full tick records an event and sends it in the same tick, with the
// namespace in the row and not in the title (U10).
func TestPoller_TickDeliversItsEvents(t *testing.T) {
	fx, p, d, n, s := deliveryFixture(t)
	s.ops = []store.RubrikOperation{{ID: "restore-1", State: store.RubrikOpRunning}}
	run := func() {
		t.Helper()
		fx.clock.advance(PollInterval)
		p.tick(context.Background())
	}
	run()
	if sent := n.emitted(); len(sent) != 0 {
		t.Fatalf("the first tick sent %+v", sent)
	}
	if len(s.refreshed) != 1 || s.refreshed[0] != "restore-1" {
		t.Errorf("the tick refreshed %v; want the running restore swept", s.refreshed)
	}
	fx.late()
	run()
	run()
	sent := n.emitted()
	if len(sent) != 1 {
		t.Fatalf("sent %d notifications, want the one compliance_lost", len(sent))
	}
	ev := fx.wantEvents(store.RubrikEventComplianceLost, 1)[0]
	got := sent[0]
	if got.Title != "Rubrik SLA compliance lost" || got.ResourceNS != "team-a" || strings.Contains(got.Title, "team-a") ||
		got.ResourceUID != ev.ID || got.ClusterID != "local" {
		t.Errorf("notification = %+v; want the fixed title, team-a in the row, the event id, cluster local", got)
	}
	if d.state["d-"+ev.ID] != store.RubrikDeliveryDelivered {
		t.Errorf("delivery state = %q, want delivered", d.state["d-"+ev.ID])
	}
	run()
	if len(n.emitted()) != 1 {
		t.Error("a delivered event was sent again")
	}
}

func TestPoller_DrainMarksByOutcome(t *testing.T) {
	fx, p, d, n, _ := deliveryFixture(t)
	base := store.RubrikPollerEvent{ClusterID: "local", RSCObjectID: idNS, DedupKey: "k", OccurredAt: pollerStart}
	add := func(id, kind string) {
		e := base
		e.ID, e.Kind = id, kind
		fx.db.mu.Lock()
		fx.db.events = append(fx.db.events, e)
		fx.db.mu.Unlock()
	}
	add("ok", store.RubrikEventSnapshotFailed)
	add("boom", store.RubrikEventConnectionFailing)
	add("raced", store.RubrikEventComplianceRestored)
	n.byTitle = map[string]error{"Rubrik connection failing": errors.New("db host 10.0.0.7 refused")}
	d.notPend["d-raced"] = true

	if err := p.drain(context.Background(), "local"); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if d.state["d-ok"] != store.RubrikDeliveryDelivered {
		t.Errorf("ok: state %q, want delivered", d.state["d-ok"])
	}
	if d.state["d-boom"] != "" || d.lastError["d-boom"] != "notification service error (see server log)" {
		t.Errorf("boom: state %q error %q; want pending with the fixed reason, not the raw error", d.state["d-boom"], d.lastError["d-boom"])
	}
	// The failed one is retried on later ticks and parked after the last attempt.
	for range pollerMaxDeliveryAttempts {
		_ = p.drain(context.Background(), "local")
	}
	if d.state["d-boom"] != store.RubrikDeliveryFailed || d.attempts["d-boom"] != pollerMaxDeliveryAttempts {
		t.Errorf("boom after retries: state %q attempts %d; want failed after %d", d.state["d-boom"], d.attempts["d-boom"], pollerMaxDeliveryAttempts)
	}

	// An emit that reports no delivery without an error is a failed attempt.
	n.byTitle, n.result = nil, notifications.EmitFailed
	add("odd", store.RubrikEventSnapshotFailed)
	_ = p.drain(context.Background(), "local")
	if d.state["d-odd"] == store.RubrikDeliveryDelivered || !strings.HasPrefix(d.lastError["d-odd"], "notification not delivered") {
		t.Errorf("odd: state %q error %q; want a failed attempt", d.state["d-odd"], d.lastError["d-odd"])
	}
}

// Every retry of an intent lands inside Notification Center's 15-minute dedup
// window, so a send whose mark did not commit is suppressed, not repeated.
func TestPoller_RetriesStayInsideTheDedupWindow(t *testing.T) {
	if last := time.Duration(pollerMaxDeliveryAttempts-1) * PollInterval; pollerMaxDeliveryAttempts < 2 || last >= 15*time.Minute {
		t.Errorf("%d attempts one poll apart span %s; want at least one retry, all inside 15m", pollerMaxDeliveryAttempts, last)
	}
}

func TestPoller_DrainSpendsNoAttemptAfterItsDeadline(t *testing.T) {
	_, p, d, _, _ := deliveryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.drain(ctx, "local"); err == nil || d.claimCount() != 0 {
		t.Errorf("drain on a finished context: err %v, claims %d; want an error and no claim", err, d.claimCount())
	}
}

// addEvents records one local event per id, all of kind snapshot_failed.
func addEvents(fx *pollerTest, ids ...string) {
	fx.db.mu.Lock()
	defer fx.db.mu.Unlock()
	for _, id := range ids {
		fx.db.events = append(fx.db.events, store.RubrikPollerEvent{ID: id, ClusterID: "local", Kind: store.RubrikEventSnapshotFailed,
			RSCObjectID: idNS, DedupKey: id, OccurredAt: pollerStart})
	}
}

// A drain cut off mid-batch still marks every intent it claimed: the one
// whose emit the deadline cut off, and the ones it never sent. Otherwise an
// intent on its last attempt would stay pending, never claimed again.
func TestPoller_DrainMarksEveryClaimWhenItsDeadlinePasses(t *testing.T) {
	fx, p, d, n, _ := deliveryFixture(t)
	addEvents(fx, "a", "b", "c")
	for _, id := range []string{"d-a", "d-b", "d-c"} {
		d.attempts[id] = pollerMaxDeliveryAttempts - 1 // the claim below is the last attempt
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.onEmit, n.err = cancel, context.Canceled

	if err := p.drain(ctx, "local"); !errors.Is(err, context.Canceled) {
		t.Fatalf("drain: %v, want the deadline's error", err)
	}
	if got := len(n.emitted()); got != 1 {
		t.Errorf("emitted %d, want 1: nothing is sent after the deadline", got)
	}
	want := map[string]string{"d-a": "notification emit cancelled", "d-b": pollerUnsentReason, "d-c": pollerUnsentReason}
	for id, reason := range want {
		if d.state[id] != store.RubrikDeliveryFailed || d.lastError[id] != reason {
			t.Errorf("%s: state %q error %q; want failed with %q", id, d.state[id], d.lastError[id], reason)
		}
	}
}

// A mark that fails with a store error stops the drain; the intents after it
// are marked as not sent rather than left claimed with no outcome.
func TestPoller_DrainReleasesTheRestAfterAStoreError(t *testing.T) {
	fx, p, d, n, _ := deliveryFixture(t)
	addEvents(fx, "a", "b", "c")
	d.markErr["d-a"] = errors.New("connection reset")

	if err := p.drain(context.Background(), "local"); err == nil {
		t.Fatal("drain: nil error, want the store error")
	}
	if got := len(n.emitted()); got != 1 {
		t.Errorf("emitted %d, want 1", got)
	}
	for _, id := range []string{"d-b", "d-c"} {
		if d.state[id] != "" || d.lastError[id] != pollerUnsentReason {
			t.Errorf("%s: state %q error %q; want pending with %q", id, d.state[id], d.lastError[id], pollerUnsentReason)
		}
	}
	// Released intents go out on the next drain.
	delete(d.markErr, "d-a")
	if err := p.drain(context.Background(), "local"); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if d.state["d-b"] != store.RubrikDeliveryDelivered || d.state["d-c"] != store.RubrikDeliveryDelivered {
		t.Errorf("after the second drain: b %q, c %q; want delivered", d.state["d-b"], d.state["d-c"])
	}
}

// The gap message is built from the detail the tick writes, so a change to
// either side that drops the times fails here.
func TestNotificationFor_GapFromTheTicksOwnDetail(t *testing.T) {
	cursor := pollerStart.Add(-30 * time.Hour)
	tick := planTick(tickInput{Now: pollerStart, HasPrev: true, Prev: store.RubrikPollerState{ActivityCursor: &cursor}, Obs: &observation{}})
	var gap store.RubrikPollerEvent
	for _, e := range tick.Events {
		if e.Kind == store.RubrikEventMonitoringGap {
			gap = e
		}
	}
	if gap.Kind == "" {
		t.Fatal("the tick raised no monitoring gap")
	}
	msg := notificationFor(gap).Message
	if !strings.Contains(msg, cursor.UTC().Format(time.RFC3339)) || !strings.Contains(msg, pollerStart.Add(-pollerCatchUp).UTC().Format(time.RFC3339)) ||
		!strings.Contains(msg, pollerStart.UTC().Format(time.RFC3339)) || !strings.Contains(msg, "about 30 hours") {
		t.Errorf("gap message %q does not state the gap from the tick's detail", msg)
	}
}

// A row reaches a namespace's readers only if that namespace existed when
// the event was recorded: delivered after the namespace was recreated, or
// when it cannot be read, the row goes to admins only. A remote cluster's
// event is never checked against the local namespaces.
func TestPoller_ScopedDropsARecreatedNamespace(t *testing.T) {
	_, p, _, _, _ := deliveryFixture(t)
	recorded := pollerStart
	e := store.RubrikPollerEvent{ID: "e", ClusterID: "local", Kind: store.RubrikEventSnapshotFailed, Namespace: "team-a", CreatedAt: recorded}
	nss := &fakeNamespaces{created: map[string]time.Time{"team-a": recorded.Add(-time.Hour)}}
	p.namespaces = nss

	check := func(name string, ev store.RubrikPollerEvent, want string) {
		t.Helper()
		if got := p.scoped(notificationFor(ev), ev).ResourceNS; got != want {
			t.Errorf("%s: namespace %q, want %q", name, got, want)
		}
	}
	check("namespace older than the event", e, "team-a")
	nss.created["team-a"] = recorded.Add(time.Minute)
	check("namespace recreated after the event", e, "")
	remote := e
	remote.ClusterID = "remote-1"
	check("remote cluster", remote, "team-a")
	delete(nss.created, "team-a")
	check("namespace deleted", e, "team-a")
	nss.err = errors.New("informer not synced")
	check("namespace unreadable", e, "")
}

// The drain sends what scoped returns.
func TestPoller_DrainSendsTheScopedNotification(t *testing.T) {
	fx, p, _, n, _ := deliveryFixture(t)
	p.namespaces = &fakeNamespaces{created: map[string]time.Time{"team-a": pollerStart.Add(time.Minute)}}
	fx.db.mu.Lock()
	fx.db.events = append(fx.db.events, store.RubrikPollerEvent{ID: "e", ClusterID: "local", Kind: store.RubrikEventSnapshotFailed,
		RSCObjectID: idNS, Namespace: "team-a", DedupKey: "e", OccurredAt: pollerStart, CreatedAt: pollerStart})
	fx.db.mu.Unlock()
	if err := p.drain(context.Background(), "local"); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if sent := n.emitted(); len(sent) != 1 || sent[0].ResourceNS != "" {
		t.Errorf("sent %+v; want one admin-only row for the recreated namespace", sent)
	}
}

// Every running operation is refreshed within a few sweeps, however many
// stay running at the head of the list.
func TestPoller_SweepRotatesThroughTheOpenOperations(t *testing.T) {
	_, p, _, _, s := deliveryFixture(t)
	total := pollerSweepBatch + 5
	for i := range total {
		s.ops = append(s.ops, store.RubrikOperation{ID: fmt.Sprintf("op-%02d", i), State: store.RubrikOpRunning,
			CreatedAt: pollerStart.Add(time.Duration(i) * time.Second)})
	}
	p.sweep(context.Background(), "local")
	p.sweep(context.Background(), "local")
	if got, want := s.refreshed[pollerSweepBatch], fmt.Sprintf("op-%02d", pollerSweepBatch); got != want {
		t.Errorf("second sweep started at %s, want %s", got, want)
	}
	seen := map[string]bool{}
	for _, id := range s.refreshed {
		seen[id] = true
	}
	if len(seen) != total {
		t.Errorf("two sweeps refreshed %d distinct operations, want all %d", len(seen), total)
	}
	// The one the last sweep ended on settled: the next starts after where it stood.
	last := s.refreshed[len(s.refreshed)-1]
	s.ops = slices.DeleteFunc(s.ops, func(o store.RubrikOperation) bool { return o.ID == last })
	s.refreshed = nil
	p.sweep(context.Background(), "local")
	var lastIdx int
	_, _ = fmt.Sscanf(last, "op-%d", &lastIdx)
	if want := fmt.Sprintf("op-%02d", lastIdx+1); s.refreshed[0] != want {
		t.Errorf("after the last one settled, the sweep started at %s, want %s", s.refreshed[0], want)
	}
}

func TestPoller_AfterTickNeedsTheLease(t *testing.T) {
	fx, p, d, _, s := deliveryFixture(t)
	if _, err := fx.db.AcquireOrRenewPollerLease(context.Background(), "local", "replica-b", time.Minute); err != nil {
		t.Fatal(err)
	}
	s.ops = []store.RubrikOperation{{ID: "op-1", State: store.RubrikOpRunning}}
	p.afterTick(context.Background(), "local")
	if d.claimCount() != 0 || len(s.graces) != 0 || len(s.refreshed) != 0 {
		t.Errorf("without the lease: claims %d, expiries %d, refreshes %d; want none", d.claimCount(), len(s.graces), len(s.refreshed))
	}
}

func TestPoller_SweepRefreshesRunningAndUnknown(t *testing.T) {
	_, p, _, _, s := deliveryFixture(t)
	s.ops = []store.RubrikOperation{
		{ID: "applying", State: store.RubrikOpApplying},
		{ID: "running", State: store.RubrikOpRunning},
		{ID: "unknown", State: store.RubrikOpUnknown},
	}
	for i := range pollerSweepBatch + 5 {
		s.ops = append(s.ops, store.RubrikOperation{ID: "more-" + string(rune('a'+i)), State: store.RubrikOpRunning})
	}
	s.listErr = store.ErrRubrikOperationsTruncated // the oldest ones still come back
	p.sweep(context.Background(), "local")
	if len(s.graces) != 1 || s.graces[0] < store.RubrikApplyingMinGrace || s.graces[0] != pollerApplyingGrace {
		t.Errorf("applying expiry graces = %v; want one call with %s (at least %s)", s.graces, pollerApplyingGrace, store.RubrikApplyingMinGrace)
	}
	if len(s.refreshed) != pollerSweepBatch || s.refreshed[0] != "running" || s.refreshed[1] != "unknown" || slices.Contains(s.refreshed, "applying") {
		t.Errorf("refreshed %v; want the oldest %d running or unknown, never an applying one", s.refreshed, pollerSweepBatch)
	}
}

func TestPoller_SweepLeavesAnUnmappedClusterToExpiry(t *testing.T) {
	fx, p, _, _, s := deliveryFixture(t)
	m := fx.st.mappings["local"]
	m.ConnectionID = "another-connection"
	fx.st.mappings["local"] = m
	s.ops = []store.RubrikOperation{{ID: "running", State: store.RubrikOpRunning}}
	p.sweep(context.Background(), "local")
	if len(s.graces) != 1 || len(s.refreshed) != 0 {
		t.Errorf("unmapped cluster: expiries %d, refreshes %v; want the expiry and no RSC refresh", len(s.graces), s.refreshed)
	}
}

// The delivery and sweep path sends no RSC mutation either.
func TestPollerDeliver_HasNoWritePath(t *testing.T) {
	reads := map[string]bool{"Taskchain": true}
	readerType := reflect.TypeFor[RSCReader]()
	for i := range readerType.NumMethod() {
		reads[readerType.Method(i).Name] = true
	}
	src, err := os.ReadFile("poller_deliver.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	opsType := reflect.TypeFor[*Operations]()
	for i := range opsType.NumMethod() {
		if name := opsType.Method(i).Name; !reads[name] && strings.Contains(code, name+"(") {
			t.Errorf("poller_deliver.go calls the write method %s", name)
		}
	}
	for _, banned := range []string{"mutation ", ".writer", ".restorer", "*Operations", "*Client"} {
		if strings.Contains(code, banned) {
			t.Errorf("poller_deliver.go mentions %q", banned)
		}
	}
}
