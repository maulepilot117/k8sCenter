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
	err error // returned by every record
}

func (l *contactLog) RecordContactSuccess(_ context.Context, revision int64) error {
	return l.add(contact{revision: revision})
}

func (l *contactLog) RecordContactFailure(_ context.Context, revision int64, kind string) error {
	return l.add(contact{revision: revision, kind: kind})
}

func (l *contactLog) add(c contact) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, c)
	return l.err
}

func (l *contactLog) contacts() []contact {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
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
	plain := StoreConnection{Store: &fakeStore{}, ID: testConn}
	if err := plain.RecordContactSuccess(ctx, 1); err != nil {
		t.Fatalf("success without a contact store: %v", err)
	}
	if err := plain.RecordContactFailure(ctx, 1, store.RubrikFailureAuth); err != nil {
		t.Fatalf("failure without a contact store: %v", err)
	}

	cs := &fakeContactStore{fakeStore: &fakeStore{}}
	src := StoreConnection{Store: cs, ID: testConn}
	if err := src.RecordContactSuccess(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if err := src.RecordContactFailure(ctx, 5, store.RubrikFailureGraphQL); err != nil {
		t.Fatal(err)
	}
	want := []string{testConn + "|4|", testConn + "|5|" + store.RubrikFailureGraphQL}
	if !slices.Equal(cs.calls, want) {
		t.Fatalf("store calls = %v, want %v", cs.calls, want)
	}
	cs.err = errors.New("db down")
	if err := src.RecordContactSuccess(ctx, 4); err == nil {
		t.Fatal("a store error was dropped")
	}
}

// fakeContactStore is a ConnectionStore that records contact outcomes.
type fakeContactStore struct {
	*fakeStore
	calls []string
	err   error
}

func (s *fakeContactStore) RecordConnectionSuccess(_ context.Context, id string, revision int64) (bool, error) {
	s.calls = append(s.calls, fmt.Sprintf("%s|%d|", id, revision))
	return s.err == nil, s.err
}

func (s *fakeContactStore) RecordConnectionFailure(_ context.Context, id string, revision int64, kind string) (bool, error) {
	s.calls = append(s.calls, fmt.Sprintf("%s|%d|%s", id, revision, kind))
	return s.err == nil, s.err
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
