package rubrik

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/store"
)

// testClock is a clock the test moves, shared by the services, the pollers
// and the store fake.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// memPollerStore is a PollerStore with the rules store.RubrikStore enforces
// in PostgreSQL (store/rubrik_poller_test.go tests those): a fenced lease
// judged on one clock, a commit refused unless the lease is still held with
// the same fence and the mapping is unchanged, compliance upserted and
// pruned, and events unique per (cluster, kind, dedup key).
type memPollerStore struct {
	mu         sync.Mutex
	clock      *testClock
	mappings   []store.RubrikClusterMapping
	leases     map[string]store.RubrikPollerLease
	states     map[string]store.RubrikPollerState
	compliance map[string]map[string]store.RubrikComplianceState
	events     []store.RubrikPollerEvent
	commits    int
	seq        int
}

func newMemPollerStore(clock *testClock, mappings ...store.RubrikClusterMapping) *memPollerStore {
	return &memPollerStore{
		clock: clock, mappings: mappings,
		leases:     map[string]store.RubrikPollerLease{},
		states:     map[string]store.RubrikPollerState{},
		compliance: map[string]map[string]store.RubrikComplianceState{},
	}
}

func (m *memPollerStore) ListMappings(_ context.Context, connectionID string) ([]store.RubrikClusterMapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.RubrikClusterMapping
	for _, mp := range m.mappings {
		if mp.ConnectionID == connectionID {
			out = append(out, mp)
		}
	}
	return out, nil
}

func (m *memPollerStore) AcquireOrRenewPollerLease(_ context.Context, clusterID, holder string, ttl time.Duration) (store.RubrikPollerLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	l, ok := m.leases[clusterID]
	switch {
	case ok && l.Holder != holder && l.ExpiresAt.After(now):
		return store.RubrikPollerLease{}, store.ErrRubrikPollerLeaseHeld
	case ok && l.Holder == holder && l.ExpiresAt.After(now):
		l.RenewedAt, l.ExpiresAt = now, now.Add(ttl)
	default:
		l = store.RubrikPollerLease{ClusterID: clusterID, Holder: holder, Fence: l.Fence + 1,
			AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(ttl)}
	}
	m.leases[clusterID] = l
	return l, nil
}

func (m *memPollerStore) ReleasePollerLease(_ context.Context, clusterID, holder string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.leases[clusterID]; ok && l.Holder == holder {
		l.ExpiresAt = m.clock.Now()
		m.leases[clusterID] = l
	}
	return nil
}

func (m *memPollerStore) GetPollerState(_ context.Context, clusterID string) (store.RubrikPollerState, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[clusterID]
	return s, ok, nil
}

func (m *memPollerStore) ListComplianceStates(_ context.Context, clusterID string) ([]store.RubrikComplianceState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.RubrikComplianceState, 0, len(m.compliance[clusterID]))
	for _, s := range m.compliance[clusterID] {
		out = append(out, s)
	}
	return out, nil
}

func (m *memPollerStore) CommitPollerTick(_ context.Context, lease store.RubrikPollerLease, tick store.RubrikPollerTick) ([]store.RubrikPollerEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	l, ok := m.leases[lease.ClusterID]
	if !ok || l.Holder != lease.Holder || l.Fence != lease.Fence || !l.ExpiresAt.After(now) {
		return nil, store.ErrRubrikPollerLeaseLost
	}
	mapped := slices.ContainsFunc(m.mappings, func(mp store.RubrikClusterMapping) bool {
		return mp.ClusterID == lease.ClusterID && mp.RSCClusterID == tick.RSCClusterID && mp.UpdatedAt.Equal(tick.MappingUpdatedAt)
	})
	if !mapped {
		return nil, store.ErrRubrikPollerMappingChanged
	}
	m.commits++
	m.seq++
	st := tick.State
	st.ClusterID, st.LastTickAt = lease.ClusterID, &now
	st.UpdatedAt = now.Add(time.Duration(m.seq)) // unique per commit, as NOW() per transaction
	m.states[lease.ClusterID] = st

	prev := m.compliance[lease.ClusterID]
	next := map[string]store.RubrikComplianceState{}
	if !tick.PruneCompliance {
		maps.Copy(next, prev)
	}
	for _, c := range tick.Compliance {
		k := c.RSCObjectID
		c.EvaluatedAt, c.ChangedAt = now, now
		if p, ok := prev[k]; ok && p.Status == c.Status {
			c.ChangedAt = p.ChangedAt
		}
		next[k] = c
	}
	m.compliance[lease.ClusterID] = next

	var recorded []store.RubrikPollerEvent
	for _, e := range tick.Events {
		dup := slices.ContainsFunc(m.events, func(x store.RubrikPollerEvent) bool {
			return x.ClusterID == lease.ClusterID && x.Kind == e.Kind && x.DedupKey == e.DedupKey
		})
		if dup {
			continue
		}
		e.ID, e.ClusterID, e.CreatedAt = fmt.Sprintf("ev-%d", len(m.events)+1), lease.ClusterID, now
		m.events = append(m.events, e)
		recorded = append(recorded, e)
	}
	return recorded, nil
}

func (m *memPollerStore) eventsOf(kind string) []store.RubrikPollerEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.RubrikPollerEvent
	for _, e := range m.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (m *memPollerStore) allEvents() []store.RubrikPollerEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.events)
}

func (m *memPollerStore) stateOf(objectID string) (store.RubrikComplianceState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.compliance["local"][objectID]
	return s, ok
}

func (m *memPollerStore) pollerState() store.RubrikPollerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states["local"]
}

// remap replaces the cluster's mapping as SetMapping does for a new target:
// the old row is deleted, which cascades the cluster's poller state,
// compliance states and events.
func (m *memPollerStore) remap(mp store.RubrikClusterMapping) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mappings = slices.DeleteFunc(m.mappings, func(x store.RubrikClusterMapping) bool { return x.ClusterID == mp.ClusterID })
	m.mappings = append(m.mappings, mp)
	delete(m.states, mp.ClusterID)
	delete(m.compliance, mp.ClusterID)
	m.events = slices.DeleteFunc(m.events, func(e store.RubrikPollerEvent) bool { return e.ClusterID == mp.ClusterID })
}

func (m *memPollerStore) commitCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commits
}

func (m *memPollerStore) expireLeases() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, l := range m.leases {
		l.ExpiresAt = m.clock.Now().Add(-time.Second)
		m.leases[id] = l
	}
}

// recordingReader records the activity filters the poller sends, and can
// fail the cluster list alone, so the service serves its cached objects
// marked stale while the other reads still answer.
type recordingReader struct {
	*fakeReader
	fmu       sync.Mutex
	filters   []ActivityFilter
	listError error
}

func (r *recordingReader) Clusters(ctx context.Context) ([]Cluster, error) {
	r.fmu.Lock()
	err := r.listError
	r.fmu.Unlock()
	if err != nil {
		return nil, err
	}
	return r.fakeReader.Clusters(ctx)
}

func (r *recordingReader) Jobs(ctx context.Context, f ActivityFilter) ([]Job, error) {
	r.fmu.Lock()
	r.filters = append(r.filters, f)
	r.fmu.Unlock()
	return r.fakeReader.Jobs(ctx, f)
}

func (r *recordingReader) jobCalls() []ActivityFilter {
	r.fmu.Lock()
	defer r.fmu.Unlock()
	return slices.Clone(r.filters)
}

var pollerStart = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

// pollerTest is the local cluster mapped to the RSC-native cluster idCluster,
// whose one object idNS protects team-a under idSLA, an SLA domain with an
// hourly schedule.
type pollerTest struct {
	t     *testing.T
	clock *testClock
	rsc   *recordingReader
	st    *fakeStore
	db    *memPollerStore
	quiet *slog.Logger
}

func newPollerTest(t *testing.T) *pollerTest {
	t.Helper()
	clock := &testClock{now: pollerStart}
	mapping := store.RubrikClusterMapping{ClusterID: "local", ConnectionID: testConn, RSCClusterID: idCluster,
		ClusterGeneration: "local", UpdatedAt: pollerStart.Add(-time.Hour)}
	fx := &pollerTest{
		t: t, clock: clock, rsc: &recordingReader{fakeReader: newFakeReader()},
		st:    &fakeStore{revision: 7, mappings: map[string]store.RubrikClusterMapping{"local": mapping}},
		db:    newMemPollerStore(clock, mapping),
		quiet: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	fx.rsc.clusters = []Cluster{{Family: FamilyNamespace, ID: idCluster}, {Family: FamilyProtectionSet, ID: idSet2}}
	fx.rsc.slas = []SLADomain{{ID: idSLA, Frequency: time.Hour}}
	fx.setNewest(pollerStart.Add(-10 * time.Minute))
	return fx
}

// replica is one k8sCenter replica: its own service and poller over the
// shared RSC and database.
func (fx *pollerTest) replica(holder string) *Poller {
	svc := NewService(fx.rsc, fx.st, fakeClusters{}, testConn, fx.quiet, nil)
	svc.now = fx.clock.Now
	p := NewPoller(svc, fx.db, holder)
	p.now = fx.clock.Now
	return p
}

// setNewest gives the team-a object a newest snapshot at the given time.
func (fx *pollerTest) setNewest(at time.Time) {
	fx.rsc.set(func(f *fakeReader) {
		f.objects[idCluster] = []Object{objNS(idNS, "team-a", idSLA, &at)}
	})
}

func (fx *pollerTest) onTime() { fx.setNewest(fx.clock.Now().Add(-10 * time.Minute)) }
func (fx *pollerTest) late()   { fx.setNewest(fx.clock.Now().Add(-2 * time.Hour)) }

// tick moves the clock one poll interval, then ticks the local cluster.
func (fx *pollerTest) tick(p *Poller) {
	fx.t.Helper()
	fx.clock.advance(PollInterval)
	if err := p.tickCluster(context.Background(), "local"); err != nil {
		fx.t.Fatalf("tickCluster: %v", err)
	}
}

func failedJob(id, objectID string, failed time.Time) Job {
	started := failed.Add(-10 * time.Minute)
	return Job{ID: id, ObjectID: objectID, Type: activityBackup, Status: JobFailed, StartTime: &started,
		LastUpdated: &failed, FailureReason: "tenant says the volume is gone", Message: "tenant message"}
}

func (fx *pollerTest) wantEvents(kind string, n int) []store.RubrikPollerEvent {
	fx.t.Helper()
	got := fx.db.eventsOf(kind)
	if len(got) != n {
		fx.t.Fatalf("%s events = %d, want %d (all: %+v)", kind, len(got), n, fx.db.allEvents())
	}
	return got
}

func (fx *pollerTest) wantStatus(status string) {
	fx.t.Helper()
	s, ok := fx.db.stateOf(idNS)
	if !ok || s.Status != status {
		fx.t.Fatalf("compliance state = (%+v, %v), want %s", s, ok, status)
	}
}

func TestPoller_FirstTickRecordsStateSilently(t *testing.T) {
	fx := newPollerTest(t)
	fx.late()
	fx.rsc.set(func(f *fakeReader) { f.jobs = []Job{failedJob("series-old", idNS, pollerStart.Add(-time.Hour))} })
	p := fx.replica("replica-a")
	fx.tick(p)

	if ev := fx.db.allEvents(); len(ev) != 0 {
		t.Fatalf("first tick raised %+v", ev)
	}
	if calls := fx.rsc.jobCalls(); len(calls) != 0 {
		t.Errorf("first tick read activity %+v; it has nothing to raise", calls)
	}
	fx.wantStatus(store.RubrikComplianceLate) // closed, seen once: no event
	st := fx.db.pollerState()
	if st.ActivityCursor == nil || !st.ActivityCursor.Equal(fx.clock.Now()) {
		t.Errorf("cursor = %v, want the tick time", st.ActivityCursor)
	}
	// An object out before monitoring began opens on the next tick, so its
	// later recovery closes a condition that was raised.
	fx.late()
	fx.tick(p)
	fx.wantEvents(store.RubrikEventComplianceLost, 1)
	fx.onTime()
	fx.tick(p)
	fx.wantEvents(store.RubrikEventComplianceRestored, 1)
}

func TestPoller_SnapshotFailureRaisedOnce(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	cursor := fx.clock.Now()

	failed := cursor.Add(2 * time.Minute)
	recovery := failedJob("series-restore", idNS, failed)
	recovery.Type = "Recovery"
	succeeded := failedJob("series-ok", idNS, failed)
	succeeded.Status = JobSucceeded
	before := failedJob("series-before", idNS, cursor.Add(-2*pollerFailureSlack))
	inSlack := failedJob("series-in-slack", idNS, cursor.Add(-pollerFailureSlack/2))
	other := failedJob("series-other", "99999999-9999-4999-8999-999999999999", failed)
	fx.rsc.set(func(f *fakeReader) {
		f.jobs = []Job{failedJob("SERIES-1", idNS, failed), recovery, succeeded, before, inSlack, other}
	})
	fx.tick(p)
	second := fx.clock.Now()
	raised := fx.wantEvents(store.RubrikEventSnapshotFailed, 2)
	if keys := []string{raised[0].DedupKey, raised[1].DedupKey}; !slices.Contains(keys, "series-1") || !slices.Contains(keys, "series-in-slack") {
		t.Errorf("raised %v; want series-1 and the failure inside the slack, not the one before it", keys)
	}
	ev := raised[slices.IndexFunc(raised, func(e store.RubrikPollerEvent) bool { return e.DedupKey == "series-1" })]
	if ev.RSCObjectID != idNS || ev.Namespace != "team-a" || !ev.OccurredAt.Equal(failed) {
		t.Errorf("event = %+v; want series-1 on team-a at %s", ev, failed)
	}
	if st := fx.db.pollerState(); st.ActivityCursor == nil || !st.ActivityCursor.Equal(second) {
		t.Errorf("cursor after the second tick = %v, want %s", st.ActivityCursor, second)
	}
	if s := string(ev.Detail); strings.Contains(s, "tenant") || strings.Contains(s, "team-a") {
		t.Errorf("detail %s carries tenant text or the namespace", s)
	}

	calls := fx.rsc.jobCalls()
	if len(calls) != 1 {
		t.Fatalf("activity calls = %+v, want 1", calls)
	}
	f := calls[0]
	// Literal values, so the window itself is pinned: a job started up to a
	// day before the cursor (less the minute of slack) may fail inside it.
	wantStarted := cursor.Add(-time.Minute - 24*time.Hour)
	if !slices.Equal(f.ObjectIDs, []string{idNS}) || !slices.Equal(f.Statuses, pollerFailureStatuses) ||
		f.StartedAfter == nil || !f.StartedAfter.Equal(wantStarted) || f.Limit != MaxSnapshotLimit {
		t.Errorf("filter = %+v; want object %s, failure statuses, started after %s", f, idNS, wantStarted)
	}

	fx.tick(p) // the same series again
	fx.wantEvents(store.RubrikEventSnapshotFailed, 2)
	// The third tick reads from the cursor the second one left.
	if calls := fx.rsc.jobCalls(); len(calls) != 2 || !calls[1].StartedAfter.Equal(second.Add(-pollerFailureSlack-pollerJobLookback)) {
		t.Errorf("third tick's filter = %+v; want it started from the second tick's cursor %s", calls, second)
	}
}

// Object ids go to the activity query in batches; every batch is read, and
// ids the filter cannot take are left out.
func TestPoller_ReadsFailuresInBatches(t *testing.T) {
	fx := newPollerTest(t)
	ids := make([]string, 250)
	objects := make([]Object, 0, len(ids)+2)
	at := pollerStart
	for i := range ids {
		ids[i] = fmt.Sprintf("%08x-0000-4000-8000-%012x", i+1, i+1)
		objects = append(objects, objNS(ids[i], fmt.Sprintf("ns-%d", i), idSLA, &at))
	}
	objects = append(objects,
		objNS(strings.ToUpper(ids[0]), "ns-dup", idSLA, &at), // the same object in another case
		objNS("not-a-uuid", "ns-odd", idSLA, &at))
	fx.rsc.set(func(f *fakeReader) { f.objects[idCluster] = objects })
	p := fx.replica("replica-a")
	fx.tick(p)
	failed := fx.clock.Now().Add(time.Minute)
	fx.rsc.set(func(f *fakeReader) {
		f.jobs = []Job{failedJob("series-b2", ids[150], failed), failedJob("series-b3", ids[230], failed)}
	})
	fx.tick(p)

	calls := fx.rsc.jobCalls()
	var sizes []int
	sent := map[string]int{}
	for _, c := range calls {
		sizes = append(sizes, len(c.ObjectIDs))
		for _, id := range c.ObjectIDs {
			sent[strings.ToLower(id)]++
		}
	}
	if !slices.Equal(sizes, []int{100, 100, 50}) {
		t.Errorf("batch sizes = %v, want [100 100 50]", sizes)
	}
	if sent["not-a-uuid"] != 0 || sent[ids[0]] != 1 || len(sent) != len(ids) {
		t.Errorf("sent %d distinct ids (non-uuid %d, %s %d times); want each of the %d uuids once",
			len(sent), sent["not-a-uuid"], ids[0], sent[ids[0]], len(ids))
	}
	fx.wantEvents(store.RubrikEventSnapshotFailed, 2)
}

// A re-map that lands between the mapping read and the state read is
// refused at commit: the previous target's state is not written back
// under the new mapping, and the new target's first tick stays silent.
func TestPoller_RemapMidTickWritesNothingBack(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	fx.rsc.set(func(f *fakeReader) { f.err = fmt.Errorf("%w: dial", ErrUnreachable) })
	for range pollerConnectionThreshold {
		fx.tick(p) // the old target's connection is failing
	}
	fx.wantEvents(store.RubrikEventConnectionFailing, 1)
	fx.rsc.set(func(f *fakeReader) { f.err = nil })

	remapped := store.RubrikClusterMapping{ClusterID: "local", ConnectionID: testConn, RSCClusterID: idSet2,
		ClusterGeneration: "local", UpdatedAt: fx.clock.Now()}
	p.store = &remapOnStateRead{memPollerStore: fx.db, remap: func() {
		fx.st.mu.Lock()
		fx.st.mappings["local"] = remapped
		fx.st.mu.Unlock()
		fx.db.remap(remapped)
	}}
	fx.tick(p)
	if _, found, _ := fx.db.GetPollerState(context.Background(), "local"); found {
		t.Errorf("the previous target's state was written back after the re-map: %+v", fx.db.pollerState())
	}
	fx.wantEvents(store.RubrikEventConnectionRestored, 0)
}

// remapOnStateRead re-maps the cluster right after the poller reads its
// state, as an admin saving a new mapping at that moment would.
type remapOnStateRead struct {
	*memPollerStore
	remap func()
	once  sync.Once
}

func (r *remapOnStateRead) GetPollerState(ctx context.Context, clusterID string) (store.RubrikPollerState, bool, error) {
	s, ok, err := r.memPollerStore.GetPollerState(ctx, clusterID)
	r.once.Do(r.remap)
	return s, ok, err
}

// A tenant too slow to read inside the cluster deadline counts against the
// connection, and the failed tick still commits.
func TestPoller_ClusterDeadlineCountsAsAFailure(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	gate := make(chan struct{})
	fx.rsc.set(func(f *fakeReader) { f.gate, f.started = gate, make(chan struct{}) })
	defer close(gate)

	fx.clock.advance(PollInterval)
	ctx, cancel := context.WithTimeoutCause(context.Background(), 50*time.Millisecond, errPollerClusterDeadline)
	defer cancel()
	if err := p.tickCluster(ctx, "local"); err != nil {
		t.Fatalf("tickCluster: %v", err)
	}
	if st := fx.db.pollerState(); st.ConnectionFailureStreak != 1 {
		t.Errorf("streak after a tick past its deadline = %d, want 1", st.ConnectionFailureStreak)
	}

	// Shutdown is not a failure: nothing is committed.
	commits := fx.db.commitCount()
	fx.clock.advance(PollInterval)
	stop, cancelStop := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancelStop() }()
	if err := p.tickCluster(stop, "local"); err != nil {
		t.Fatalf("tickCluster: %v", err)
	}
	if fx.db.commitCount() != commits {
		t.Error("a tick cut short by shutdown committed")
	}
}

func TestPoller_Hysteresis(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	fx.wantStatus(store.RubrikComplianceIn)

	fx.late()
	fx.tick(p) // one late tick
	fx.wantStatus(store.RubrikComplianceLate)
	fx.onTime()
	fx.tick(p) // back on time: never opened, nothing to resolve
	fx.wantStatus(store.RubrikComplianceIn)
	if ev := fx.db.allEvents(); len(ev) != 0 {
		t.Fatalf("a single late tick raised %+v", ev)
	}

	fx.late()
	fx.tick(p)
	fx.tick(p) // second consecutive late tick opens it
	fx.wantStatus(store.RubrikComplianceOut)
	lost := fx.wantEvents(store.RubrikEventComplianceLost, 1)[0]
	if lost.Namespace != "team-a" || lost.RSCObjectID != idNS {
		t.Errorf("compliance_lost = %+v; want team-a's object", lost)
	}
	fx.tick(p) // still out: nothing new
	fx.wantEvents(store.RubrikEventComplianceLost, 1)

	fx.onTime()
	fx.tick(p)
	fx.wantStatus(store.RubrikComplianceIn)
	fx.wantEvents(store.RubrikEventComplianceRestored, 1)

	fx.late() // a second outage is a new transition
	fx.tick(p)
	fx.tick(p)
	fx.wantEvents(store.RubrikEventComplianceLost, 2)
}

func TestPoller_HysteresisSurvivesLeaseHandover(t *testing.T) {
	fx := newPollerTest(t)
	a, b := fx.replica("replica-a"), fx.replica("replica-b")
	fx.tick(a)
	fx.late()
	fx.tick(a) // one late tick under replica-a
	fx.wantStatus(store.RubrikComplianceLate)

	fx.tick(b) // replica-a holds the lease: replica-b does nothing
	fx.wantStatus(store.RubrikComplianceLate)
	if st := fx.db.pollerState(); st.LastTickAt == nil || !st.LastTickAt.Before(fx.clock.Now()) {
		t.Fatalf("replica-b committed while replica-a held the lease")
	}

	fx.db.expireLeases()
	fx.tick(b) // replica-b takes over and judges late a second time
	fx.wantStatus(store.RubrikComplianceOut)
	fx.wantEvents(store.RubrikEventComplianceLost, 1)
}

// Covers AE9: three replicas sharing one database, each ticking every
// interval and handing the lease around, raise one notification per
// transition.
func TestPoller_ThreeReplicasRaiseOneEventPerTransition(t *testing.T) {
	fx := newPollerTest(t)
	replicas := []*Poller{fx.replica("replica-1"), fx.replica("replica-2"), fx.replica("replica-3")}
	round := func() {
		fx.clock.advance(PollInterval)
		var wg sync.WaitGroup
		for _, p := range replicas {
			wg.Go(func() {
				if err := p.tickCluster(context.Background(), "local"); err != nil {
					t.Errorf("tickCluster: %v", err)
				}
			})
		}
		wg.Wait()
		fx.db.expireLeases() // the next round may land on any replica
	}
	round()
	fx.late()
	for range 4 {
		round()
	}
	fx.wantStatus(store.RubrikComplianceOut)
	fx.wantEvents(store.RubrikEventComplianceLost, 1)
	if fx.db.commitCount() != 5 {
		t.Errorf("commits = %d, want one per round", fx.db.commitCount())
	}
}

func TestPoller_RelicAndSetEventsCarryNoNamespace(t *testing.T) {
	t.Run("relic", func(t *testing.T) {
		fx := newPollerTest(t)
		p := fx.replica("replica-a")
		fx.tick(p)
		at := fx.clock.Now()
		relic := objNS(idNS, "team-a", idSLA, &at)
		relic.Relic = true
		fx.rsc.set(func(f *fakeReader) {
			f.objects[idCluster] = []Object{relic}
			f.jobs = []Job{failedJob("series-relic", idNS, at.Add(time.Minute))}
		})
		fx.tick(p)
		if ev := fx.wantEvents(store.RubrikEventSnapshotFailed, 1)[0]; ev.Namespace != "" {
			t.Errorf("relic event namespace = %q, want none", ev.Namespace)
		}
		if _, ok := fx.db.stateOf(idNS); ok {
			t.Error("a relic kept a compliance state; it never counts toward compliance")
		}
	})
	t.Run("sets", func(t *testing.T) {
		fx := newPollerTest(t)
		fx.st.mappings["local"] = store.RubrikClusterMapping{ClusterID: "local", ConnectionID: testConn,
			RSCClusterID: idSet2, ClusterGeneration: "local", UpdatedAt: pollerStart}
		fx.db.mappings = []store.RubrikClusterMapping{fx.st.mappings["local"]}
		const (
			idNSSet      = "a1111111-1111-4111-8111-111111111111"
			idClusterSet = "a2222222-2222-4222-8222-222222222222"
			idPattern    = "a3333333-3333-4333-8333-333333333333"
		)
		at := pollerStart
		fx.rsc.set(func(f *fakeReader) {
			f.objects[idSet2] = []Object{
				objSet(idNSSet, "namespace", Coverage{Namespaces: []string{"team-b"}}, idSLA, &at),
				objSet(idClusterSet, "cluster", Coverage{All: true}, idSLA, &at),
				objSet(idPattern, "namespace", Coverage{Unresolved: true}, idSLA, &at),
			}
		})
		p := fx.replica("replica-a")
		fx.tick(p)
		failed := fx.clock.Now().Add(time.Minute)
		fx.rsc.set(func(f *fakeReader) {
			f.jobs = []Job{failedJob("s-ns", idNSSet, failed), failedJob("s-cluster", idClusterSet, failed), failedJob("s-pattern", idPattern, failed)}
		})
		fx.tick(p)
		got := map[string]string{}
		for _, e := range fx.wantEvents(store.RubrikEventSnapshotFailed, 3) {
			got[e.DedupKey] = e.Namespace
		}
		want := map[string]string{"s-ns": "team-b", "s-cluster": "", "s-pattern": ""}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("event namespaces = %v, want %v", got, want)
		}
	})
}

func TestPoller_RSCFailureNeitherOpensNorResolves(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	fx.late()
	fx.tick(p)
	fx.wantStatus(store.RubrikComplianceLate)

	before := fx.db.pollerState()
	fx.rsc.set(func(f *fakeReader) { f.err = fmt.Errorf("%w: dial", ErrUnreachable) })
	fx.tick(p) // would have opened the condition
	fx.wantStatus(store.RubrikComplianceLate)
	if ev := fx.db.allEvents(); len(ev) != 0 {
		t.Fatalf("a failed tick raised %+v", ev)
	}
	after := fx.db.pollerState()
	if after.ConnectionFailureStreak != 1 || !after.ActivityCursor.Equal(*before.ActivityCursor) {
		t.Errorf("state after failure = %+v; want streak 1 and the cursor kept", after)
	}

	fx.rsc.set(func(f *fakeReader) { f.err = nil })
	fx.tick(p) // reads again: the second late judgment
	fx.wantStatus(store.RubrikComplianceOut)
	fx.wantEvents(store.RubrikEventComplianceLost, 1)
}

// A cached object list whose latest fetch failed is served to pages as the
// last good answer (R13); the poller does not judge it.
func TestPoller_StaleObjectsAreAFailedTick(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	fx.late()
	fx.tick(p)
	fx.wantStatus(store.RubrikComplianceLate)

	fx.rsc.fmu.Lock()
	fx.rsc.listError = fmt.Errorf("%w: dial", ErrUnreachable)
	fx.rsc.fmu.Unlock()
	fx.tick(p) // the cached list still says late; judging it would open the condition
	fx.wantStatus(store.RubrikComplianceLate)
	fx.wantEvents(store.RubrikEventComplianceLost, 0)
	if st := fx.db.pollerState(); st.ConnectionFailureStreak != 1 {
		t.Errorf("streak = %d, want 1", st.ConnectionFailureStreak)
	}
}

func TestPoller_ConnectionStreak(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	fail := func(err error) {
		t.Helper()
		fx.rsc.set(func(f *fakeReader) { f.err = err })
		fx.tick(p)
	}
	unreachable := fmt.Errorf("%w: dial", ErrUnreachable)
	fail(unreachable)
	fail(fmt.Errorf("%w: wrong secret", ErrAuthFailed))
	fail(fmt.Errorf("%w: slow down", ErrRateLimited)) // neither counts nor resets
	if st := fx.db.pollerState(); st.ConnectionFailureStreak != 2 || st.ConnectionFailing {
		t.Fatalf("after two failures and a 429: %+v; want streak 2, not failing", st)
	}
	fx.wantEvents(store.RubrikEventConnectionFailing, 0)
	fail(unreachable)
	ev := fx.wantEvents(store.RubrikEventConnectionFailing, 1)[0]
	if ev.Namespace != "" || ev.RSCObjectID != "" {
		t.Errorf("connection event = %+v; want no namespace or object (admin-only)", ev)
	}
	fail(unreachable)
	fx.wantEvents(store.RubrikEventConnectionFailing, 1)

	fail(nil)
	fx.wantEvents(store.RubrikEventConnectionRestored, 1)
	if st := fx.db.pollerState(); st.ConnectionFailureStreak != 0 || st.ConnectionFailing {
		t.Errorf("after recovery: %+v; want the streak cleared", st)
	}
	fail(nil)
	fx.wantEvents(store.RubrikEventConnectionRestored, 1)

	for range pollerConnectionThreshold { // a second outage is a new transition
		fail(unreachable)
	}
	fail(nil)
	fx.wantEvents(store.RubrikEventConnectionFailing, 2)
	fx.wantEvents(store.RubrikEventConnectionRestored, 2)
}

func TestPoller_CatchUpAfterOutage(t *testing.T) {
	fx := newPollerTest(t)
	replicas := []*Poller{fx.replica("replica-1"), fx.replica("replica-2"), fx.replica("replica-3")}
	fx.tick(replicas[0])
	cursor := fx.clock.Now()
	fx.db.expireLeases()

	fx.clock.advance(30*time.Hour - PollInterval)
	now := fx.clock.Now().Add(PollInterval)
	fx.onTime()
	fx.rsc.set(func(f *fakeReader) {
		f.jobs = []Job{failedJob("series-28h", idNS, now.Add(-28*time.Hour)), failedJob("series-2h", idNS, now.Add(-2*time.Hour))}
	})
	fx.clock.advance(PollInterval)
	var wg sync.WaitGroup
	for _, p := range replicas {
		wg.Go(func() {
			if err := p.tickCluster(context.Background(), "local"); err != nil {
				t.Errorf("tickCluster: %v", err)
			}
		})
	}
	wg.Wait()

	if got := fx.wantEvents(store.RubrikEventSnapshotFailed, 1)[0]; got.DedupKey != "series-2h" {
		t.Errorf("raised %q; want only the failure inside the last 24 hours", got.DedupKey)
	}
	gap := fx.wantEvents(store.RubrikEventMonitoringGap, 1)[0]
	var d struct{ GapStart, GapEnd time.Time }
	if err := json.Unmarshal(gap.Detail, &d); err != nil || !d.GapStart.Equal(cursor) || !d.GapEnd.Equal(now.Add(-pollerCatchUp)) {
		t.Errorf("gap detail = %s (%v); want %s to %s", gap.Detail, err, cursor, now.Add(-pollerCatchUp))
	}
	if gap.Namespace != "" {
		t.Errorf("gap namespace = %q, want none (admin-only)", gap.Namespace)
	}
	f := fx.rsc.jobCalls()[0]
	if want := now.Add(-pollerCatchUp - pollerFailureSlack - pollerJobLookback); !f.StartedAfter.Equal(want) {
		t.Errorf("started after = %s, want %s", f.StartedAfter, want)
	}

	if st := fx.db.pollerState(); st.ActivityCursor == nil || !st.ActivityCursor.Equal(now) {
		t.Errorf("cursor after catch-up = %v, want %s", st.ActivityCursor, now)
	}
	fx.db.expireLeases()
	fx.tick(replicas[1]) // the next tick reads from the new cursor
	fx.wantEvents(store.RubrikEventMonitoringGap, 1)
	if f := fx.rsc.jobCalls()[1]; !f.StartedAfter.Equal(now.Add(-pollerFailureSlack - pollerJobLookback)) {
		t.Errorf("next tick started after %s; want it read from the new cursor %s", f.StartedAfter, now)
	}
}

func TestPoller_TwentyHourGapRaisesNoGap(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	fx.clock.advance(20*time.Hour - PollInterval)
	now := fx.clock.Now().Add(PollInterval)
	fx.onTime()
	fx.rsc.set(func(f *fakeReader) { f.jobs = []Job{failedJob("series-19h", idNS, now.Add(-19*time.Hour))} })
	fx.tick(p)
	fx.wantEvents(store.RubrikEventMonitoringGap, 0)
	fx.wantEvents(store.RubrikEventSnapshotFailed, 1)
}

func TestPoller_SkippedTicksCommitNothing(t *testing.T) {
	for name, set := range map[string]func(fx *pollerTest){
		"mapping invalid": func(fx *pollerTest) {
			m := fx.st.mappings["local"]
			m.ConnectionID = "another-connection"
			fx.st.mappings["local"] = m
		},
		"rsc cluster missing": func(fx *pollerTest) {
			fx.rsc.set(func(f *fakeReader) { f.clusters = nil })
		},
		"store unavailable": func(fx *pollerTest) {
			fx.rsc.set(func(f *fakeReader) { f.err = fmt.Errorf("%w: decrypt", ErrConnectionUnavailable) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newPollerTest(t)
			set(fx)
			fx.tick(fx.replica("replica-a"))
			if fx.db.commitCount() != 0 {
				t.Errorf("commits = %d, want 0", fx.db.commitCount())
			}
		})
	}
}

func TestPoller_UnprotectedObjectLosesItsState(t *testing.T) {
	fx := newPollerTest(t)
	p := fx.replica("replica-a")
	fx.tick(p)
	fx.wantStatus(store.RubrikComplianceIn)
	at := fx.clock.Now()
	fx.rsc.set(func(f *fakeReader) { f.objects[idCluster] = []Object{objNS(idNS, "team-a", "UNPROTECTED", &at)} })
	fx.tick(p)
	if s, ok := fx.db.stateOf(idNS); ok {
		t.Errorf("an unprotected object kept %+v", s)
	}
	if ev := fx.db.allEvents(); len(ev) != 0 {
		t.Errorf("losing protection raised %+v", ev)
	}
}

func TestPoller_ShutdownReleasesItsLeases(t *testing.T) {
	fx := newPollerTest(t)
	a := fx.replica("replica-a")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Start(ctx); close(done) }()
	deadline := time.After(5 * time.Second)
	for fx.db.commitCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("the first tick never committed")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	<-done
	if _, err := fx.db.AcquireOrRenewPollerLease(context.Background(), "local", "replica-b", time.Minute); err != nil {
		t.Errorf("replica-b after replica-a stopped: %v; want the lease free", err)
	}
}

func TestStepCompliance(t *testing.T) {
	st := func(status string, streak int) store.RubrikComplianceState {
		return store.RubrikComplianceState{Status: status, LateStreak: streak}
	}
	in, late, out, unknown := store.RubrikComplianceIn, store.RubrikComplianceLate, store.RubrikComplianceOut, store.RubrikComplianceUnknown
	for _, tc := range []struct {
		name     string
		prev     *store.RubrikComplianceState
		judgment Compliance
		want     store.RubrikComplianceState
		event    string
	}{
		{"new in", nil, ComplianceInCompliance, st(in, 0), ""},
		{"new out", nil, ComplianceOutOfCompliance, st(late, 1), ""},
		{"new unknown", nil, ComplianceUnknown, st(unknown, 0), ""},
		{"in then out", new(st(in, 0)), ComplianceOutOfCompliance, st(late, 1), ""},
		{"unknown then out", new(st(unknown, 0)), ComplianceOutOfCompliance, st(late, 1), ""},
		{"late then out", new(st(late, 1)), ComplianceOutOfCompliance, st(out, 2), store.RubrikEventComplianceLost},
		{"late then in", new(st(late, 1)), ComplianceInCompliance, st(in, 0), ""},
		{"out then out", new(st(out, 2)), ComplianceOutOfCompliance, st(out, 2), ""},
		{"out then in", new(st(out, 2)), ComplianceInCompliance, st(in, 0), store.RubrikEventComplianceRestored},
		{"out then unknown", new(st(out, 2)), ComplianceUnknown, st(out, 2), ""},
		{"late then unknown", new(st(late, 1)), ComplianceUnknown, st(late, 1), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var prev store.RubrikComplianceState
			if tc.prev != nil {
				prev = *tc.prev
			}
			got, ev := stepCompliance(prev, tc.prev != nil, tc.judgment)
			if got != tc.want || ev != tc.event {
				t.Errorf("stepCompliance = (%+v, %q), want (%+v, %q)", got, ev, tc.want, tc.event)
			}
		})
	}
}

func TestCountsAsConnectionFailure(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("x: %w", ErrUnreachable), true},
		{fmt.Errorf("x: %w", ErrAuthFailed), true},
		{fmt.Errorf("x: %w", ErrInvalidTenantURL), true},
		{&GraphQLError{}, true},
		{fmt.Errorf("x: %w", ErrUnreadable), true},
		{context.DeadlineExceeded, true},
		{&RateLimitError{}, false},
		{fmt.Errorf("x: %w", ErrNotConfigured), false},
		{fmt.Errorf("x: %w", ErrConnectionUnavailable), false},
		{fmt.Errorf("x: %w", ErrStoreUnavailable), false},
		{fmt.Errorf("x: %w", ErrTargetMissing), false},
		{context.Canceled, false},
	} {
		if got := countsAsConnectionFailure(tc.err); got != tc.want {
			t.Errorf("countsAsConnectionFailure(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// The poller never sends an RSC mutation (plan U10). Its RSC access is the
// read-only RSCReader, and its source names no write method of Operations
// and no mutation document.
func TestPoller_HasNoWritePath(t *testing.T) {
	reads := map[string]bool{}
	readerType := reflect.TypeFor[RSCReader]()
	for i := range readerType.NumMethod() {
		reads[readerType.Method(i).Name] = true
	}
	var writes []string
	opsType := reflect.TypeFor[*Operations]()
	for i := range opsType.NumMethod() {
		if name := opsType.Method(i).Name; !reads[name] && name != "Taskchain" {
			writes = append(writes, name)
		}
	}
	if len(writes) == 0 {
		t.Fatal("found no write methods on Operations; the pin would hold vacuously")
	}
	src, err := os.ReadFile("poller.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, w := range writes {
		if strings.Contains(code, w+"(") {
			t.Errorf("poller.go calls the write method %s", w)
		}
	}
	for _, banned := range []string{"mutation ", "*Operations", "*Client", "Writer", "Restorer"} {
		if strings.Contains(code, banned) {
			t.Errorf("poller.go mentions %q", banned)
		}
	}
	for i := range reflect.TypeFor[Poller]().NumField() {
		f := reflect.TypeFor[Poller]().Field(i)
		if f.Type == reflect.TypeFor[*Operations]() || f.Type == reflect.TypeFor[*Client]() {
			t.Errorf("Poller field %s holds %s", f.Name, f.Type)
		}
	}
}
