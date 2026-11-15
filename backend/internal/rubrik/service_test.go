package rubrik

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/store"
)

// fakeReader is an RSCReader whose answers the test sets and whose calls it
// counts.
type fakeReader struct {
	mu        sync.Mutex
	clusters  []Cluster
	objects   map[string][]Object // by RSC cluster id
	snapshots map[string][]Snapshot
	jobs      []Job
	slas      []SLADomain
	err       error // returned by every call when set
	panicNext bool

	calls map[string]int
	// gate, when set, blocks Clusters until it is closed; started is closed
	// when the first call reaches it.
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
}

func newFakeReader() *fakeReader {
	return &fakeReader{objects: map[string][]Object{}, snapshots: map[string][]Snapshot{}, calls: map[string]int{}}
}

func (f *fakeReader) enter(name string) error {
	f.mu.Lock()
	f.calls[name]++
	err, gate, started := f.err, f.gate, f.started
	doPanic := f.panicNext && name == "Clusters"
	if doPanic {
		f.panicNext = false
	}
	f.mu.Unlock()
	if doPanic {
		panic("tenant answer broke the decoder")
	}
	if gate != nil && name == "Clusters" {
		f.once.Do(func() { close(started) })
		<-gate
	}
	return err
}

func (f *fakeReader) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *fakeReader) set(fn func(f *fakeReader)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeReader) Clusters(context.Context) ([]Cluster, error) {
	if err := f.enter("Clusters"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.clusters), nil
}

func (f *fakeReader) NamespaceObjects(_ context.Context, id string) ([]Object, error) {
	if err := f.enter("NamespaceObjects"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.objects[id]), nil
}

func (f *fakeReader) ProtectionSets(_ context.Context, id string) ([]Object, error) {
	if err := f.enter("ProtectionSets"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.objects[id]), nil
}

func (f *fakeReader) Snapshots(_ context.Context, family Family, id string, limit int) ([]Snapshot, error) {
	if err := f.enter("Snapshots:" + string(family) + ":" + id + ":" + fmt.Sprint(limit)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.snapshots[id]), nil
}

func (f *fakeReader) Jobs(_ context.Context, af ActivityFilter) ([]Job, error) {
	if err := f.enter("Jobs:" + fmt.Sprint(af.ObjectIDs) + ":" + fmt.Sprint(af.Limit)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.jobs), nil
}

func (f *fakeReader) SLADomains(context.Context) ([]SLADomain, error) {
	if err := f.enter("SLADomains"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.slas), nil
}

// fakeStore is a MappingStore and ConnectionStore.
type fakeStore struct {
	mu         sync.Mutex
	revision   int64 // 0 means no connection
	revErr     error
	mappings   map[string]store.RubrikClusterMapping
	mappingErr error
	compliance []store.RubrikComplianceState
	conn       store.RubrikConnection
	secret     string
}

func (s *fakeStore) setRevision(rev int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision = rev
}

func (s *fakeStore) ConnectionRevision(_ context.Context, _ string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revErr != nil {
		return 0, s.revErr
	}
	if s.revision == 0 {
		return 0, store.ErrRubrikConnectionNotFound
	}
	return s.revision, nil
}

func (s *fakeStore) GetMapping(_ context.Context, clusterID string) (store.RubrikClusterMapping, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mappingErr != nil {
		return store.RubrikClusterMapping{}, s.mappingErr
	}
	m, ok := s.mappings[clusterID]
	if !ok {
		return store.RubrikClusterMapping{}, store.ErrRubrikMappingNotFound
	}
	return m, nil
}

func (s *fakeStore) ListComplianceStates(_ context.Context, _ string) ([]store.RubrikComplianceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.compliance), nil
}

func (s *fakeStore) GetConnection(_ context.Context, _ string) (store.RubrikConnection, store.RubrikCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision == 0 {
		return store.RubrikConnection{}, store.RubrikCredentials{}, store.ErrRubrikConnectionNotFound
	}
	c := s.conn
	c.Revision = s.revision
	return c, store.RubrikCredentials{ClientSecret: s.secret}, nil
}

// fakeClusters is the cluster registry.
type fakeClusters map[string]time.Time

func (f fakeClusters) Get(_ context.Context, id string) (*store.ClusterRecord, error) {
	at, ok := f[id]
	if !ok {
		return nil, fmt.Errorf("getting cluster %s: %w", id, pgx.ErrNoRows)
	}
	return &store.ClusterRecord{ID: id, CreatedAt: at}, nil
}

const testConn = "conn-under-test"

var registered = time.Date(2026, 3, 4, 5, 6, 7, 891011121, time.UTC)

// fixture is a service over fakes with the local cluster mapped to the
// RSC-native cluster idCluster and remote-1 mapped to the CDM cluster
// idSet2, and a clock the test moves.
type fixture struct {
	svc     *Service
	rsc     *fakeReader
	st      *fakeStore
	now     time.Time
	flushes atomic.Int32
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{rsc: newFakeReader(), now: created}
	fx.rsc.clusters = []Cluster{
		{Family: FamilyNamespace, ID: idCluster, Name: "native"},
		{Family: FamilyProtectionSet, ID: idSet2, Name: "cdm"},
	}
	fx.rsc.objects[idCluster] = []Object{objNS(idNS, "team-a", idSLA, nil)}
	fx.rsc.objects[idSet2] = []Object{objSet(idSet, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil)}
	fx.st = &fakeStore{revision: 7, mappings: map[string]store.RubrikClusterMapping{
		"local":    {ClusterID: "local", ConnectionID: testConn, RSCClusterID: idCluster, ClusterGeneration: "local"},
		"remote-1": {ClusterID: "remote-1", ConnectionID: testConn, RSCClusterID: idSet2, ClusterGeneration: registered.Format(time.RFC3339Nano)},
	}}
	clusters := fakeClusters{"remote-1": registered}
	fx.svc = NewService(fx.rsc, fx.st, clusters, testConn, nil, func() { fx.flushes.Add(1) })
	fx.svc.now = func() time.Time { return fx.now }
	return fx
}

func (fx *fixture) resolve(t *testing.T, clusterID string) Target {
	t.Helper()
	tgt, err := fx.svc.Resolve(context.Background(), clusterID)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", clusterID, err)
	}
	return tgt
}

func (fx *fixture) objects(t *testing.T, tgt Target) ClusterView {
	t.Helper()
	v, err := fx.svc.Objects(context.Background(), tgt)
	if err != nil {
		t.Fatalf("Objects: %v", err)
	}
	return v
}

func TestResolveMappingStates(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)

	if tgt := fx.resolve(t, ""); !tgt.Valid() || tgt.ClusterID != "local" || tgt.Revision != 7 {
		t.Fatalf("local target = %+v", tgt)
	}
	if tgt := fx.resolve(t, "remote-1"); !tgt.Valid() {
		t.Fatal("remote target invalid")
	}

	// The same instant in another format is the same registration.
	other := fx.st.mappings["remote-1"]
	other.ClusterGeneration = registered.In(time.FixedZone("x", 3600)).Format(time.RFC3339Nano)
	fx.st.mappings["remote-1"] = other
	if _, err := fx.svc.Resolve(ctx, "remote-1"); err != nil {
		t.Fatalf("same instant, other zone: %v", err)
	}

	cases := []struct {
		name    string
		cluster string
		edit    func(s *fakeStore)
		want    error
	}{
		{"no connection", "local", func(s *fakeStore) { s.revision = 0 }, ErrNotConfigured},
		{"revision unreadable", "local", func(s *fakeStore) { s.revErr = errors.New("db down") }, ErrStoreUnavailable},
		{"not mapped", "remote-2", func(*fakeStore) {}, ErrNotMapped},
		{"mapping unreadable", "local", func(s *fakeStore) { s.mappingErr = errors.New("db down") }, ErrStoreUnavailable},
		{"local generation changed", "local", func(s *fakeStore) {
			m := s.mappings["local"]
			m.ClusterGeneration = registered.Format(time.RFC3339Nano)
			s.mappings["local"] = m
		}, ErrMappingInvalid},
		{"remote registered again", "remote-1", func(s *fakeStore) {
			m := s.mappings["remote-1"]
			m.ClusterGeneration = registered.Add(-time.Second).Format(time.RFC3339Nano)
			s.mappings["remote-1"] = m
		}, ErrMappingInvalid},
		{"remote generation unreadable", "remote-1", func(s *fakeStore) {
			m := s.mappings["remote-1"]
			m.ClusterGeneration = "yesterday"
			s.mappings["remote-1"] = m
		}, ErrMappingInvalid},
		{"remote cluster deleted", "remote-gone", func(s *fakeStore) {
			s.mappings["remote-gone"] = store.RubrikClusterMapping{ClusterID: "remote-gone", ConnectionID: testConn,
				RSCClusterID: idCluster, ClusterGeneration: registered.Format(time.RFC3339Nano)}
		}, ErrMappingInvalid},
		{"saved for another connection", "local", func(s *fakeStore) {
			m := s.mappings["local"]
			m.ConnectionID = "default"
			s.mappings["local"] = m
		}, ErrMappingInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			tc.edit(fx.st)
			tgt, err := fx.svc.Resolve(ctx, tc.cluster)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tgt.Valid() {
				t.Fatal("a refused mapping produced a valid target")
			}
			// What a write would check with: every write refuses it.
			a := NewAccess(newFakeChecker(), twoTeams(t), true)
			r, _ := viewer(t, a, admin).CheckAction(ctx, tgt, ActionSnapshot, objNS(idNS, "team-a", idSLA, nil), "")
			if r != RefusalMappingInvalid {
				t.Fatalf("write check = %q, want mapping_invalid", r)
			}
			if _, err := fx.svc.Objects(ctx, tgt); !errors.Is(err, ErrMappingInvalid) {
				t.Fatalf("Objects on a refused target err = %v", err)
			}
		})
	}
}

func TestObjectsFollowTheMappedFamily(t *testing.T) {
	fx := newFixture(t)
	local := fx.objects(t, fx.resolve(t, "local"))
	if local.Cluster.Family != FamilyNamespace || !slices.Equal(ids(local.Objects), []string{idNS}) {
		t.Fatalf("local view = %+v", local)
	}
	remote := fx.objects(t, fx.resolve(t, "remote-1"))
	if remote.Cluster.Family != FamilyProtectionSet || !slices.Equal(ids(remote.Objects), []string{idSet}) {
		t.Fatalf("remote view = %+v", remote)
	}
	if fx.rsc.count("NamespaceObjects") != 1 || fx.rsc.count("ProtectionSets") != 1 {
		t.Fatalf("calls = %v, want one list per family", fx.rsc.calls)
	}
	if o, ok := remote.Object(idSet); !ok || o.ID != idSet {
		t.Fatal("view lookup missed its object")
	}
}

func TestTargetMissingOrAmbiguous(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	fx.rsc.set(func(f *fakeReader) { f.clusters = []Cluster{{Family: FamilyProtectionSet, ID: idSet2}} })
	if _, err := fx.svc.Objects(ctx, tgt); !errors.Is(err, ErrTargetMissing) {
		t.Fatalf("missing: err = %v, want ErrTargetMissing", err)
	}

	fx = newFixture(t)
	tgt = fx.resolve(t, "local")
	fx.rsc.set(func(f *fakeReader) {
		f.clusters = []Cluster{{Family: FamilyNamespace, ID: idCluster}, {Family: FamilyProtectionSet, ID: idCluster}}
	})
	if _, err := fx.svc.Objects(ctx, tgt); !errors.Is(err, ErrTargetMissing) {
		t.Fatalf("ambiguous: err = %v, want ErrTargetMissing", err)
	}
	if fx.rsc.count("NamespaceObjects")+fx.rsc.count("ProtectionSets") != 0 {
		t.Fatal("objects listed for a cluster bound to no family")
	}

	// Objects of the other family are not the mapped cluster's.
	fx = newFixture(t)
	fx.rsc.set(func(f *fakeReader) {
		f.objects[idCluster] = []Object{objSet(idSet, "namespace", Coverage{}, idSLA, nil)}
	})
	if _, err := fx.svc.Objects(ctx, fx.resolve(t, "local")); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("other family: err = %v, want ErrUnreadable", err)
	}
}

func TestConcurrentReadsShareOneFetch(t *testing.T) {
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	fx.rsc.set(func(f *fakeReader) { f.gate, f.started = make(chan struct{}), make(chan struct{}) })

	var wg sync.WaitGroup
	results := make([]ClusterView, 2)
	errs := make([]error, 2)
	read := func(i int) {
		defer wg.Done()
		results[i], errs[i] = fx.svc.Objects(context.Background(), tgt)
	}
	wg.Add(1)
	go read(0)
	<-fx.rsc.started // the first fetch is in flight and blocked
	wg.Add(1)
	go read(1)
	// The second reader joins the flight: there is no entry yet and the
	// fetch cannot finish until the gate opens.
	time.Sleep(20 * time.Millisecond)
	close(fx.rsc.gate)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil || len(results[i].Objects) != 1 {
			t.Fatalf("reader %d: %v, %+v", i, errs[i], results[i])
		}
	}
	if n := fx.rsc.count("Clusters"); n != 1 {
		t.Fatalf("RSC fetches = %d, want 1", n)
	}
}

func TestCacheServesForTTL(t *testing.T) {
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	first := fx.objects(t, tgt)
	fx.now = fx.now.Add(CacheTTL - time.Second)
	if v := fx.objects(t, tgt); !v.FetchedAt.Equal(first.FetchedAt) || v.Stale {
		t.Fatalf("within TTL: %+v", v.Freshness)
	}
	if n := fx.rsc.count("Clusters"); n != 1 {
		t.Fatalf("fetches within TTL = %d, want 1", n)
	}
	fx.now = fx.now.Add(2 * time.Second)
	if v := fx.objects(t, tgt); !v.FetchedAt.Equal(fx.now) {
		t.Fatalf("after TTL fetchedAt = %v, want %v", v.FetchedAt, fx.now)
	}
	if n := fx.rsc.count("Clusters"); n != 2 {
		t.Fatalf("fetches after TTL = %d, want 2", n)
	}
}

// Covers AE6: a failing tenant leaves the last good answer in place, marked
// stale with its fetch time, never an empty "unprotected" list.
func TestFailureServesLastGoodDataStale(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	good := fx.objects(t, tgt)

	fx.rsc.set(func(f *fakeReader) { f.err = fmt.Errorf("%w: token refused", ErrAuthFailed) })
	fx.now = fx.now.Add(CacheTTL + time.Second)
	v, err := fx.svc.Objects(ctx, tgt)
	if err != nil {
		t.Fatalf("stale read returned an error: %v", err)
	}
	if !v.Stale || !errors.Is(v.Err, ErrAuthFailed) || !v.FetchedAt.Equal(good.FetchedAt) || len(v.Objects) != 1 {
		t.Fatalf("stale view = %+v", v)
	}
	// The failure is not retried on every read.
	fx.now = fx.now.Add(CacheTTL - time.Second)
	if _, err := fx.svc.Objects(ctx, tgt); err != nil {
		t.Fatal(err)
	}
	if n := fx.rsc.count("Clusters"); n != 2 {
		t.Fatalf("fetches = %d, want 2 (one good, one failed)", n)
	}
	// Recovery clears the stale mark.
	fx.rsc.set(func(f *fakeReader) { f.err = nil })
	fx.now = fx.now.Add(2 * time.Second)
	if v := fx.objects(t, tgt); v.Stale || v.Err != nil || !v.FetchedAt.Equal(fx.now) {
		t.Fatalf("recovered view = %+v", v.Freshness)
	}

	// With no earlier answer the error is returned, and no empty list.
	fx = newFixture(t)
	fx.rsc.set(func(f *fakeReader) { f.err = fmt.Errorf("%w: dial", ErrUnreachable) })
	if v, err := fx.svc.Objects(ctx, fx.resolve(t, "local")); !errors.Is(err, ErrUnreachable) || v.Objects != nil {
		t.Fatalf("first read = %+v, %v; want ErrUnreachable and no objects", v, err)
	}
}

func TestTargetMissingIsNotMaskedByOldData(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	fx.objects(t, tgt)
	fx.rsc.set(func(f *fakeReader) { f.clusters = nil })
	fx.now = fx.now.Add(CacheTTL + time.Second)
	if v, err := fx.svc.Objects(ctx, tgt); !errors.Is(err, ErrTargetMissing) || v.Objects != nil {
		t.Fatalf("view = %+v, err = %v; want ErrTargetMissing and no objects", v, err)
	}
}

func TestRevisionChangeDropsCachedDataAndToken(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	fx.objects(t, fx.resolve(t, "local"))
	if fx.flushes.Load() != 0 {
		t.Fatal("the first revision seen flushed the token")
	}

	fx.st.setRevision(8) // another replica saved the connection
	tgt := fx.resolve(t, "local")
	if tgt.Revision != 8 || fx.flushes.Load() != 1 {
		t.Fatalf("revision %d, flushes %d; want 8 and 1", tgt.Revision, fx.flushes.Load())
	}
	fx.objects(t, tgt) // well within the TTL, but the old answer is gone
	if n := fx.rsc.count("Clusters"); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}

	// Removing the connection drops everything too; so does a connection
	// coming back, even with a revision seen before.
	fx.st.setRevision(0)
	if _, err := fx.svc.Resolve(ctx, "local"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if fx.flushes.Load() != 2 {
		t.Fatalf("flushes = %d, want 2", fx.flushes.Load())
	}
	fx.st.setRevision(8)
	fx.objects(t, fx.resolve(t, "local"))
	if n := fx.rsc.count("Clusters"); n != 3 {
		t.Fatalf("fetches = %d, want 3", n)
	}
}

// An answer that arrives after the connection changed is returned to its
// callers but not stored: the revision in the key already keeps new reads
// from it, and storing it would only leave a dead entry behind.
func TestAnswerFromAnOldRevisionIsNotStored(t *testing.T) {
	fx := newFixture(t)
	old := fx.resolve(t, "local")
	fx.st.setRevision(8)
	fresh := fx.resolve(t, "local") // observes revision 8
	if v := fx.objects(t, old); len(v.Objects) != 1 {
		t.Fatalf("old-revision reader got %+v", v)
	}
	fx.svc.mu.Lock()
	n := len(fx.svc.entries)
	fx.svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("entries after an old-revision fetch = %d, want 0", n)
	}
	fx.objects(t, fresh)
	if n := fx.rsc.count("Clusters"); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}
}

func TestComplianceComesFromPersistedRows(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	fx.st.compliance = []store.RubrikComplianceState{
		{RSCObjectID: "out", Status: store.RubrikComplianceOut},
		{RSCObjectID: "LATE", Status: store.RubrikComplianceLate},
		{RSCObjectID: "in", Status: store.RubrikComplianceIn},
		{RSCObjectID: "odd", Status: "something new"},
	}
	compliance, err := fx.svc.Compliance(ctx, fx.resolve(t, "local"))
	if err != nil {
		t.Fatal(err)
	}
	newest := ts("2026-06-01T11:00:00Z")
	cases := map[string]Compliance{
		"out":     ComplianceOutOfCompliance,
		"late":    ComplianceInCompliance,
		"in":      ComplianceInCompliance,
		"odd":     ComplianceUnknown,
		"unjudge": ComplianceUnknown,
	}
	for id, want := range cases {
		sum := SummarizeNamespace("team-a", []Object{objNS(id, "team-a", idSLA, newest)}, compliance)
		if sum.Compliance != want {
			t.Errorf("object %s: compliance %q, want %q", id, sum.Compliance, want)
		}
	}
	if _, err := fx.svc.Compliance(ctx, Target{}); !errors.Is(err, ErrMappingInvalid) {
		t.Fatalf("invalid target err = %v", err)
	}
}

func TestHistoryOnlyForObjectsOfTheMappedCluster(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	fx.rsc.set(func(f *fakeReader) {
		f.snapshots[idSet] = []Snapshot{{ID: idSnapshot, Time: created}}
		f.jobs = []Job{{ID: "mine", ObjectID: idSet}, {ID: "other", ObjectID: idNS}}
	})
	remote := fx.resolve(t, "remote-1")

	// idNS is an object of the local cluster's tenant answer, not of
	// remote-1's: it is not found and nothing is fetched for it.
	if _, _, _, err := fx.svc.Snapshots(ctx, remote, idNS, 0); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("foreign object err = %v, want ErrObjectNotFound", err)
	}
	if _, _, _, err := fx.svc.Jobs(ctx, remote, idNS, 0); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("foreign object jobs err = %v, want ErrObjectNotFound", err)
	}

	o, snaps, _, err := fx.svc.Snapshots(ctx, remote, idSet, 0)
	if err != nil || o.ID != idSet || len(snaps) != 1 {
		t.Fatalf("snapshots = %v, %v, %v", o.ID, snaps, err)
	}
	if n := fx.rsc.count(fmt.Sprintf("Snapshots:protectionSet:%s:%d", idSet, DefaultSnapshotLimit)); n != 1 {
		t.Fatalf("snapshot fetch calls = %v", fx.rsc.calls)
	}
	_, jobs, _, err := fx.svc.Jobs(ctx, remote, idSet, 0)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "mine" {
		t.Fatalf("jobs = %+v, %v; want only the object's own job", jobs, err)
	}
}

// The limit is part of the history cache keys: a 10-row answer is never
// served for a 50-row request, and each limit reaches RSC as asked, with the
// jobs request scoped to the object.
func TestHistoryLimitsAreCachedSeparately(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	for _, limit := range []int{10, 50, 10, 50} {
		if _, _, _, err := fx.svc.Snapshots(ctx, tgt, idNS, limit); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := fx.svc.Jobs(ctx, tgt, idNS, limit); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{10, 50} {
		if n := fx.rsc.count(fmt.Sprintf("Snapshots:%s:%s:%d", FamilyNamespace, idNS, limit)); n != 1 {
			t.Errorf("snapshot fetches with limit %d = %d, want 1 (calls %v)", limit, n, fx.rsc.calls)
		}
		if n := fx.rsc.count(fmt.Sprintf("Jobs:[%s]:%d", idNS, limit)); n != 1 {
			t.Errorf("job fetches with limit %d = %d, want 1 (calls %v)", limit, n, fx.rsc.calls)
		}
	}
}

func TestSLADomainsAreCached(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	fx.rsc.set(func(f *fakeReader) { f.slas = []SLADomain{{ID: idSLA, Name: "Gold", Frequency: time.Hour}} })
	tgt := fx.resolve(t, "local")
	for range 3 {
		got, _, err := fx.svc.SLADomains(ctx, tgt)
		if err != nil || len(got) != 1 {
			t.Fatalf("SLADomains = %v, %v", got, err)
		}
	}
	if n := fx.rsc.count("SLADomains"); n != 1 {
		t.Fatalf("SLA fetches = %d, want 1", n)
	}
	if _, _, err := fx.svc.SLADomains(ctx, Target{}); !errors.Is(err, ErrMappingInvalid) {
		t.Fatalf("invalid target err = %v", err)
	}
}

// A panic in a fetch runs on singleflight's goroutine, where nothing else
// would recover it; it must come back as an error, not crash the process.
func TestFetchPanicIsRecovered(t *testing.T) {
	fx := newFixture(t)
	fx.rsc.set(func(f *fakeReader) { f.panicNext = true })
	if _, err := fx.svc.Objects(context.Background(), fx.resolve(t, "local")); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestCancelledReaderDoesNotFailOthers(t *testing.T) {
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	fx.rsc.set(func(f *fakeReader) { f.gate, f.started = make(chan struct{}), make(chan struct{}) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fx.svc.Objects(ctx, tgt)
		done <- err
	}()
	<-fx.rsc.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnreachable) {
		t.Fatalf("cancelled reader err = %v", err)
	}
	other := make(chan error, 1)
	go func() {
		_, err := fx.svc.Objects(context.Background(), tgt)
		other <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(fx.rsc.gate)
	if err := <-other; err != nil {
		t.Fatalf("waiting reader failed with the cancelled one: %v", err)
	}
	if n := fx.rsc.count("Clusters"); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
}

func TestHistoryEntriesAgeOut(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t)
	tgt := fx.resolve(t, "local")
	if _, _, _, err := fx.svc.Snapshots(ctx, tgt, idNS, 10); err != nil {
		t.Fatal(err)
	}
	fx.now = fx.now.Add(historyEntryAge + time.Minute)
	fx.objects(t, tgt) // a refresh prunes
	fx.svc.mu.Lock()
	kinds := []string{}
	for k := range fx.svc.entries {
		kinds = append(kinds, cacheKeyKind(k))
	}
	fx.svc.mu.Unlock()
	if !slices.Equal(kinds, []string{"objects"}) {
		t.Fatalf("entries after pruning = %v, want only the object list", kinds)
	}
}

func TestStoreConnection(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{}
	src := StoreConnection{Store: st, ID: testConn}
	if _, err := src.Connection(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no connection: %v", err)
	}
	if _, err := src.ConnectionRevision(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no connection revision: %v", err)
	}

	st.revision = 3
	st.conn = store.RubrikConnection{TenantURL: "https://example.my.rubrik.com", ClientID: "client|id", HasSecret: false}
	st.secret = "s3cret"
	if _, err := src.Connection(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("secret for another origin: %v", err)
	}
	st.conn.HasSecret = true
	c, err := src.Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.TenantURL != st.conn.TenantURL || c.ClientID != "client|id" || c.ClientSecret != "s3cret" || c.Revision != 3 {
		t.Fatalf("connection = %+v", Connection{TenantURL: c.TenantURL, ClientID: c.ClientID, Revision: c.Revision})
	}
	if rev, err := src.ConnectionRevision(ctx); err != nil || rev != 3 {
		t.Fatalf("revision = %d, %v", rev, err)
	}
	st.revErr = errors.New("db down")
	if _, err := src.ConnectionRevision(ctx); err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("db error = %v, want a non-ErrNotConfigured error", err)
	}
}

// The service runs over the real operations against the fake tenant: the
// family comes from RSC's two cluster lists, and the matching list query
// carries the mapped id.
func TestServiceOverTheFakeTenant(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.answer("RubrikK8sClusters", conn("k8sClusters", []any{}, false, ""))
	f.answer("RubrikKubernetesClusters", conn("kubernetesClusters",
		[]any{map[string]any{"id": idCluster, "name": "cdm-prod"}}, false, ""))
	f.handle("RubrikK8sProtectionSets", func(vars map[string]any) (any, error) {
		if vars["clusterId"] != idCluster {
			return nil, fmt.Errorf("asked for cluster %v", vars["clusterId"])
		}
		return conn("kubernetesProtectionSets", []any{map[string]any{
			"id": idSet, "name": "team-a", "rsType": "namespace", "namespace": "team-a",
			"k8sClusterUuid":     idCluster,
			"effectiveSlaDomain": map[string]any{"id": idSLA, "name": "Gold"},
		}}, false, ""), nil
	})
	st := &fakeStore{revision: 1, mappings: map[string]store.RubrikClusterMapping{
		"local": {ClusterID: "local", ConnectionID: testConn, RSCClusterID: idCluster, ClusterGeneration: "local"},
	}}
	svc := NewService(ops, st, fakeClusters{}, testConn, nil, nil)
	tgt, err := svc.Resolve(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	v, err := svc.Objects(context.Background(), tgt)
	if err != nil {
		t.Fatal(err)
	}
	if v.Cluster.Family != FamilyProtectionSet || v.Cluster.Name != "cdm-prod" || !slices.Equal(ids(v.Objects), []string{idSet}) {
		t.Fatalf("view = %+v", v)
	}
	for _, c := range f.recorded() {
		if c.Op == "RubrikK8sNamespaces" {
			t.Fatal("listed per-namespace objects for a protection-set cluster")
		}
	}
}
