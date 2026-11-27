package rubrik

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/store"
)

// contact is one outcome a recorder was told about; kind "" is a success.
type contact struct {
	revision int64
	kind     string
}

// contactLog is an in-memory ContactRecorder.
type contactLog struct {
	mu  sync.Mutex
	got []contact
	ats []time.Time // the contact time of each entry in got
	err error       // returned by every record
}

func (l *contactLog) RecordContactSuccess(_ context.Context, revision int64, at time.Time) error {
	return l.add(contact{revision: revision}, at)
}

func (l *contactLog) RecordContactFailure(_ context.Context, revision int64, kind string, at time.Time) error {
	return l.add(contact{revision: revision, kind: kind}, at)
}

func (l *contactLog) add(c contact, at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, c)
	l.ats = append(l.ats, at)
	return l.err
}

func (l *contactLog) contacts() []contact {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

func (l *contactLog) times() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ats)
}

// newestRecorder is a ContactRecorder with the store's rule: each column
// keeps only its newest contact time, whatever order writes arrive in.
// While gate is set, a success write signals entered and waits for gate to
// close before it lands, so a test can hold it behind a later failure.
type newestRecorder struct {
	mu          sync.Mutex
	successAt   *time.Time
	failureAt   *time.Time
	failureKind string

	gate    chan struct{}
	entered chan struct{}
}

func (r *newestRecorder) RecordContactSuccess(_ context.Context, _ int64, at time.Time) error {
	if r.gate != nil {
		r.entered <- struct{}{}
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.successAt == nil || r.successAt.Before(at) {
		r.successAt = &at
	}
	return nil
}

func (r *newestRecorder) RecordContactFailure(_ context.Context, _ int64, kind string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failureAt == nil || r.failureAt.Before(at) {
		r.failureAt, r.failureKind = &at, kind
	}
	return nil
}

// health is what GET /rubrik/status would report for the recorded times.
func (r *newestRecorder) health() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return connectionHealth(store.RubrikConnection{
		HasSecret: true, LastSuccessAt: r.successAt, LastFailureAt: r.failureAt, LastFailureKind: r.failureKind,
	})
}

// recordingSource is a fakeSource that is also a ContactRecorder, the way
// StoreConnection is in production.
type recordingSource struct {
	*fakeSource
	contactLog
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestContactFailureKind(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		kind   string
		record bool
	}{
		{"success", nil, "", true},
		{"auth", fmt.Errorf("%w: status 401", ErrAuthFailed), store.RubrikFailureAuth, true},
		{"rate limit", &RateLimitError{RetryAfter: time.Second}, store.RubrikFailureRateLimited, true},
		{"graphql", &GraphQLError{Messages: []string{"tenant text 10.0.0.1"}}, store.RubrikFailureGraphQL, true},
		{"unreadable", fmt.Errorf("%w: answer over 10 bytes", ErrUnreadable), store.RubrikFailureUnreadable, true},
		{"unreachable", fmt.Errorf("%w: status 503", ErrUnreachable), store.RubrikFailureUnreachable, true},
		{"timeout", fmt.Errorf("%w: %w", ErrUnreachable, context.DeadlineExceeded), store.RubrikFailureUnreachable, true},
		{"refused address", fmt.Errorf("%w: private", ErrInvalidTenantURL), store.RubrikFailureUnreachable, true},
		{"caller gave up", fmt.Errorf("%w: %w", ErrUnreachable, context.Canceled), "", false},
		{"not configured", fmt.Errorf("%w: no usable client secret", ErrNotConfigured), "", false},
		{"store down", fmt.Errorf("%w: db", ErrConnectionUnavailable), "", false},
		{"bad request", fmt.Errorf("%w: json", ErrInvalidRequest), "", false},
		{"unclassified", errors.New("something else"), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, record := contactFailureKind(tc.err)
			if kind != tc.kind || record != tc.record {
				t.Fatalf("contactFailureKind = (%q, %v), want (%q, %v)", kind, record, tc.kind, tc.record)
			}
		})
	}
}

func TestHealthTrackerThrottlesSameOutcome(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: pollerStart}
	log := &contactLog{}
	h := newHealthTracker(log, quietLogger(), clock.Now)
	unreachable := fmt.Errorf("%w: status 502", ErrUnreachable)

	h.observe(ctx, 1, nil)
	clock.advance(30 * time.Second)
	h.observe(ctx, 1, nil) // same outcome inside the interval
	clock.advance(time.Second)
	h.observe(ctx, 1, unreachable) // kind changed: written at once
	h.observe(ctx, 1, unreachable)
	h.observe(ctx, 1, nil) // back to success: written at once
	clock.advance(healthWriteInterval)
	h.observe(ctx, 1, nil) // interval passed
	h.observe(ctx, 2, nil) // revision changed
	h.observe(ctx, 2, fmt.Errorf("%w: %w", ErrUnreachable, context.Canceled))
	h.observe(ctx, 2, ErrNotConfigured)

	want := []contact{{1, ""}, {1, store.RubrikFailureUnreachable}, {1, ""}, {1, ""}, {2, ""}}
	if got := log.contacts(); !slices.Equal(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
	// Each write carries the time its contact completed.
	t31, t91 := pollerStart.Add(31*time.Second), pollerStart.Add(91*time.Second)
	wantAt := []time.Time{pollerStart, t31, t31, t91, t91}
	if got := log.times(); !slices.EqualFunc(got, wantAt, time.Time.Equal) {
		t.Fatalf("contact times = %v, want %v", got, wantAt)
	}

	var nilTracker *healthTracker
	nilTracker.observe(ctx, 1, nil) // a client without a recorder
}

// A failed write is logged, never surfaced, and tried again once the
// interval has passed.
func TestHealthTrackerRetriesAfterFailedWrite(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: pollerStart}
	log := &contactLog{err: errors.New("db down")}
	h := newHealthTracker(log, quietLogger(), clock.Now)

	h.observe(ctx, 1, nil)
	h.observe(ctx, 1, nil)
	clock.advance(healthWriteInterval)
	h.observe(ctx, 1, nil)
	if got := len(log.contacts()); got != 2 {
		t.Fatalf("write attempts = %d, want 2 (one per interval)", got)
	}
}

func TestStoreConnectionRecordsThroughContactStore(t *testing.T) {
	ctx := context.Background()
	// A store that cannot record outcomes leaves the source usable.
	at := pollerStart
	plain := StoreConnection{Store: &fakeStore{}, ID: testConn}
	if err := plain.RecordContactSuccess(ctx, 1, at); err != nil {
		t.Fatalf("success without a contact store: %v", err)
	}
	if err := plain.RecordContactFailure(ctx, 1, store.RubrikFailureAuth, at); err != nil {
		t.Fatalf("failure without a contact store: %v", err)
	}

	cs := &fakeContactStore{fakeStore: &fakeStore{}}
	src := StoreConnection{Store: cs, ID: testConn}
	if err := src.RecordContactSuccess(ctx, 4, at); err != nil {
		t.Fatal(err)
	}
	if err := src.RecordContactFailure(ctx, 5, store.RubrikFailureGraphQL, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		fmt.Sprintf("%s|4||%d", testConn, at.Unix()),
		fmt.Sprintf("%s|5|%s|%d", testConn, store.RubrikFailureGraphQL, at.Unix()+1),
	}
	if !slices.Equal(cs.calls, want) {
		t.Fatalf("store calls = %v, want %v", cs.calls, want)
	}
	cs.err = errors.New("db down")
	if err := src.RecordContactSuccess(ctx, 4, at); err == nil {
		t.Fatal("a store error was dropped")
	}
}

// fakeContactStore is a ConnectionStore that records contact outcomes.
type fakeContactStore struct {
	*fakeStore
	calls []string
	err   error
}

func (s *fakeContactStore) RecordConnectionSuccess(_ context.Context, id string, revision int64, at time.Time) (bool, error) {
	s.calls = append(s.calls, fmt.Sprintf("%s|%d||%d", id, revision, at.Unix()))
	return s.err == nil, s.err
}

func (s *fakeContactStore) RecordConnectionFailure(_ context.Context, id string, revision int64, kind string, at time.Time) (bool, error) {
	s.calls = append(s.calls, fmt.Sprintf("%s|%d|%s|%d", id, revision, kind, at.Unix()))
	return s.err == nil, s.err
}

// The P1 ordering: a success completes, its write stalls, an auth failure
// completes and is written, then the success write lands. Each carries the
// time its contact completed and the recorder keeps the newest of each, so
// status reports the failure, the later outcome.
func TestHealthOutOfOrderWritesReportTheLaterFailure(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: pollerStart}
	rec := &newestRecorder{gate: make(chan struct{}), entered: make(chan struct{})}
	h := newHealthTracker(rec, quietLogger(), clock.Now)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.observe(ctx, 1, nil) // completes at pollerStart, write held
	}()
	<-rec.entered
	clock.advance(time.Second)
	h.observe(ctx, 1, fmt.Errorf("%w: status 401", ErrAuthFailed))
	close(rec.gate)
	<-done

	if got := rec.health(); got != HealthAuthFailed {
		t.Fatalf("health after a late success write = %q, want %q", got, HealthAuthFailed)
	}
	if rec.successAt == nil || !rec.successAt.Equal(pollerStart) {
		t.Fatalf("success recorded at %v, want its completion time %v", rec.successAt, pollerStart)
	}
}

// Concurrent calls share one tracker. Run under -race (CI does), this
// covers the mark update and the write that follows it.
func TestHealthTrackerConcurrentObserve(t *testing.T) {
	clock := &testClock{now: pollerStart}
	log := &contactLog{}
	h := newHealthTracker(log, quietLogger(), clock.Now)
	outcomes := []error{nil, fmt.Errorf("%w: status 401", ErrAuthFailed), fmt.Errorf("%w: status 502", ErrUnreachable)}

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				if i%10 == 0 {
					clock.advance(healthWriteInterval)
				}
				h.observe(context.Background(), int64(1+i%2), outcomes[(g+i)%len(outcomes)])
			}
		})
	}
	wg.Wait()

	got := log.contacts()
	if len(got) == 0 {
		t.Fatal("no outcome was written")
	}
	for _, c := range got {
		if c.kind != "" && c.kind != store.RubrikFailureAuth && c.kind != store.RubrikFailureUnreachable {
			t.Fatalf("recorded kind %q", c.kind)
		}
	}
	for _, at := range log.times() {
		if at.IsZero() {
			t.Fatal("a write carried no contact time")
		}
	}
}

// The client records the class of each real contact's outcome, at the
// revision of the token it used, and nothing for outcomes that say nothing
// about the tenant.
func TestClientRecordsContactHealth(t *testing.T) {
	ft := newFakeTenant(t)
	src := &recordingSource{fakeSource: ft.source()}
	c := ft.client(src, nil)
	ctx := context.Background()

	if err := c.Query(ctx, testQuery, nil); err != nil {
		t.Fatal(err)
	}

	ft.setGraphQL(func(w http.ResponseWriter, _ *http.Request, _ string) {
		writeJSON(w, map[string]any{"errors": []any{map[string]any{"message": "tenant says no at 10.1.2.3"}}})
	})
	if err := c.Query(ctx, testQuery, nil); !errors.Is(err, ErrGraphQL) {
		t.Fatalf("graphql query = %v", err)
	}

	ft.setGraphQL(func(w http.ResponseWriter, _ *http.Request, _ string) { w.WriteHeader(http.StatusBadGateway) })
	if err := c.Query(ctx, testQuery, nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("502 query = %v", err)
	}

	ft.setGraphQL(nil)
	ft.setToken(func(w http.ResponseWriter, _ int32) bool {
		w.WriteHeader(http.StatusUnauthorized)
		return true
	})
	c.Flush()
	if err := c.Query(ctx, testQuery, nil); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("refused credentials = %v", err)
	}

	// A save on another replica: the next contact is recorded at the
	// token's revision.
	ft.setToken(nil)
	src.setRevision(2)
	if err := c.Query(ctx, testQuery, nil); err != nil {
		t.Fatal(err)
	}

	// Outcomes that say nothing about the tenant record nothing.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Query(canceled, testQuery, nil); err == nil {
		t.Fatal("canceled query succeeded")
	}
	src.fakeSource.mu.Lock()
	src.fakeSource.err = ErrNotConfigured
	src.fakeSource.mu.Unlock()
	if err := c.Query(ctx, testQuery, nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured query = %v", err)
	}

	want := []contact{
		{1, ""},
		{1, store.RubrikFailureGraphQL},
		{1, store.RubrikFailureUnreachable},
		{1, store.RubrikFailureAuth},
		{2, ""},
	}
	if got := src.contacts(); !slices.Equal(got, want) {
		t.Fatalf("recorded = %v, want %v", got, want)
	}
}

// skewedSource reports one revision from ConnectionRevision and the
// connection at another, as when a save lands between the two reads.
type skewedSource struct {
	*recordingSource
	readRevision int64
}

func (s *skewedSource) ConnectionRevision(context.Context) (int64, error) { return s.readRevision, nil }

// An outcome is recorded at the revision of the credentials the contact
// actually used (the token's), not the revision read before the exchange.
func TestClientRecordsTheTokensRevision(t *testing.T) {
	ft := newFakeTenant(t)
	inner := &recordingSource{fakeSource: ft.source()}
	inner.setRevision(2) // the connection the exchange reads
	src := &skewedSource{recordingSource: inner, readRevision: 1}
	c := ft.client(src, nil)

	if err := c.Query(context.Background(), testQuery, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := inner.contacts(), []contact{{2, ""}}; !slices.Equal(got, want) {
		t.Fatalf("recorded = %v, want %v", got, want)
	}
}

// The 401 path: a refused token is replaced once. A retry that succeeds
// records one success; a re-authentication the tenant refuses records
// auth_failed at the revision read.
func TestClientRecordsTheRetriedOutcome(t *testing.T) {
	t.Run("retry succeeds", func(t *testing.T) {
		ft := newFakeTenant(t)
		ft.setGraphQL(func(w http.ResponseWriter, _ *http.Request, auth string) {
			if auth == "tok-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeJSON(w, map[string]any{"data": map[string]any{"token": auth}})
		})
		src := &recordingSource{fakeSource: ft.source()}
		c := ft.client(src, nil)
		if err := c.Query(context.Background(), testQuery, nil); err != nil {
			t.Fatal(err)
		}
		if got, want := src.contacts(), []contact{{1, ""}}; !slices.Equal(got, want) {
			t.Fatalf("recorded = %v, want %v", got, want)
		}
	})
	t.Run("re-authentication refused", func(t *testing.T) {
		ft := newFakeTenant(t)
		ft.setGraphQL(func(w http.ResponseWriter, _ *http.Request, _ string) { w.WriteHeader(http.StatusUnauthorized) })
		ft.setToken(func(w http.ResponseWriter, n int32) bool {
			if n == 1 {
				return false // the first exchange issues tok-1
			}
			w.WriteHeader(http.StatusUnauthorized)
			return true
		})
		src := &recordingSource{fakeSource: ft.source()}
		c := ft.client(src, nil)
		if err := c.Query(context.Background(), testQuery, nil); !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("query = %v, want ErrAuthFailed", err)
		}
		if got, want := src.contacts(), []contact{{1, store.RubrikFailureAuth}}; !slices.Equal(got, want) {
			t.Fatalf("recorded = %v, want %v", got, want)
		}
	})
}

// The poller's reads go through the same client, so a tick records the
// connection's health: a success, then the class of a failure.
func TestPollerTickRecordsContactHealth(t *testing.T) {
	f, _ := newFakeRSC(t)
	f.answer("RubrikK8sClusters", conn("k8sClusters", []any{}, false, ""))
	f.answer("RubrikKubernetesClusters", conn("kubernetesClusters",
		[]any{map[string]any{"id": idCluster, "name": "cdm-prod"}}, false, ""))
	f.answer("RubrikK8sProtectionSets", conn("kubernetesProtectionSets", []any{}, false, ""))
	f.answer("RubrikSlaDomains", conn("slaDomains", nil, false, ""))
	f.answer("RubrikActivitySeries", conn("activitySeriesConnection", nil, false, ""))

	src := &recordingSource{fakeSource: f.ft.source()}
	ops := &Operations{q: f.ft.client(src, nil)}
	clock := &testClock{now: pollerStart}
	mapping := store.RubrikClusterMapping{ClusterID: "local", ConnectionID: testConn, RSCClusterID: idCluster,
		ClusterGeneration: "local", UpdatedAt: pollerStart.Add(-time.Hour)}
	st := &fakeStore{revision: 1, mappings: map[string]store.RubrikClusterMapping{"local": mapping}}
	svc := NewService(ops, st, fakeClusters{}, testConn, quietLogger(), nil)
	svc.now = clock.Now
	p := NewPoller(svc, newMemPollerStore(clock, mapping), "replica-a")
	p.now = clock.Now

	clock.advance(PollInterval)
	if err := p.tickCluster(context.Background(), "local"); err != nil {
		t.Fatalf("healthy tick: %v", err)
	}
	if got := src.contacts(); len(got) == 0 || got[0] != (contact{1, ""}) {
		t.Fatalf("after a healthy tick recorded %v, want a success first", got)
	}

	f.handle("RubrikK8sClusters", func(map[string]any) (any, error) { return nil, errors.New("tenant text") })
	clock.advance(PollInterval) // past the cache TTL, so the tick asks RSC again
	_ = p.tickCluster(context.Background(), "local")
	got := src.contacts()
	if last := got[len(got)-1]; last != (contact{1, store.RubrikFailureGraphQL}) {
		t.Fatalf("after a failing tick recorded %v, want graphql_error last", got)
	}
}
