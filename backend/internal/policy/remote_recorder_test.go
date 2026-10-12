package policy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/store"
)

// recIdentities serves identity rows from a map.
type recIdentities struct {
	mu   sync.Mutex
	rows map[string]store.ComplianceIdentity
	err  error
}

func (f *recIdentities) List(context.Context) ([]store.ComplianceIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []store.ComplianceIdentity
	for _, id := range []string{"remote-a", "remote-b", "remote-c"} {
		if row, ok := f.rows[id]; ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *recIdentities) Get(_ context.Context, clusterID string) (store.ComplianceIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[clusterID]
	if !ok {
		return store.ComplianceIdentity{}, store.ErrComplianceIdentityNotFound
	}
	return row, nil
}

type recGap struct {
	ClusterID string
	Date      time.Time
	Reason    string
	Revision  int64
}

// recSnapshots records every write.
type recSnapshots struct {
	mu       sync.Mutex
	points   []store.ComplianceSnapshot
	gaps     []recGap
	attempts int
	cleanups []int
	written  chan struct{}
}

func (f *recSnapshots) InsertRemote(_ context.Context, snap *store.ComplianceSnapshot) (bool, error) {
	f.mu.Lock()
	f.points = append(f.points, *snap)
	f.mu.Unlock()
	f.signal()
	return true, nil
}

func (f *recSnapshots) InsertGap(ctx context.Context, clusterID string, date time.Time, reason string, revision int64) (bool, error) {
	f.mu.Lock()
	f.attempts++
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	f.mu.Lock()
	f.gaps = append(f.gaps, recGap{clusterID, date, reason, revision})
	f.mu.Unlock()
	f.signal()
	return true, nil
}

func (f *recSnapshots) Cleanup(_ context.Context, retentionDays int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanups = append(f.cleanups, retentionDays)
	return 0, nil
}

func (f *recSnapshots) signal() {
	if f.written != nil {
		f.written <- struct{}{}
	}
}

func (f *recSnapshots) snapshot() ([]store.ComplianceSnapshot, []recGap, []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.ComplianceSnapshot(nil), f.points...), append([]recGap(nil), f.gaps...), append([]int(nil), f.cleanups...)
}

// recChecker answers CanImpersonate per cluster.
type recChecker struct {
	mu    sync.Mutex
	errs  map[string]error
	calls []string
}

func (f *recChecker) CanImpersonate(_ context.Context, clusterID, _ string, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, clusterID)
	return f.errs[clusterID]
}

// recReader answers readRemoteForRecord per cluster through fn.
type recReader struct {
	mu    sync.Mutex
	calls []string
	fn    func(ctx context.Context, clusterID, username string, groups []string) (*remoteRecord, error)
}

func (f *recReader) readRemoteForRecord(ctx context.Context, clusterID, username string, groups []string) (*remoteRecord, error) {
	f.mu.Lock()
	f.calls = append(f.calls, clusterID)
	f.mu.Unlock()
	return f.fn(ctx, clusterID, username, groups)
}

func (f *recReader) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func recFullRecord(context.Context, string, string, []string) (*remoteRecord, error) {
	policies := []NormalizedPolicy{{Name: "require-labels", Engine: EngineKyverno, Blocking: true, Severity: "high"}}
	return &remoteRecord{Outcome: recordFull, Policies: policies, Violations: []NormalizedViolation{},
		Score: computeCompliance(policies, nil, ""), Installed: []Engine{EngineKyverno}}, nil
}

var recNow = time.Date(2026, 10, 12, 13, 45, 0, 0, time.UTC)

func recIdentityRow(clusterID string, revision int64) store.ComplianceIdentity {
	return store.ComplianceIdentity{ClusterID: clusterID, Username: "compliance-bot", Groups: []string{"auditors"}, Revision: revision}
}

func newTestRemoteRecorder(ids *recIdentities, snaps *recSnapshots, checker *recChecker, reader *recReader) *RemoteComplianceRecorder {
	r := &RemoteComplianceRecorder{
		identities: ids,
		snapshots:  snaps,
		checker:    checker,
		reader:     reader,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:        func() time.Time { return recNow },
		timeout:    time.Minute,
	}
	return r
}

var recToday = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func TestRemoteRecorder_FullPointCarriesIdentityAndRevision(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 7)}}
	snaps := &recSnapshots{}
	r := newTestRemoteRecorder(ids, snaps, &recChecker{}, &recReader{fn: recFullRecord})

	r.runDaily(context.Background())

	points, gaps, _ := snaps.snapshot()
	if len(gaps) != 0 || len(points) != 1 {
		t.Fatalf("points %d gaps %+v, want one point", len(points), gaps)
	}
	p := points[0]
	if p.ClusterID != "remote-a" || !p.Date.Equal(recToday) || p.IdentityRevision != 7 || p.Username != "compliance-bot" ||
		!reflect.DeepEqual(p.Groups, []string{"auditors"}) || p.Partial || len(p.Unreadable) != 0 {
		t.Errorf("point = %+v, want today's full point as compliance-bot at revision 7", p)
	}
	if p.Total != 1 || p.Pass != 1 || p.OverallScore != 100 {
		t.Errorf("point score = %v pass %d total %d, want the reader's score", p.OverallScore, p.Pass, p.Total)
	}
}

func TestRemoteRecorder_PartialPointNamesUnreadableEngines(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 3)}}
	snaps := &recSnapshots{}
	reader := &recReader{fn: func(ctx context.Context, c, u string, g []string) (*remoteRecord, error) {
		rec, _ := recFullRecord(ctx, c, u, g)
		rec.Outcome = recordPartial
		rec.Installed = []Engine{EngineKyverno, EngineGatekeeper}
		rec.Unreadable = []Engine{EngineGatekeeper}
		return rec, nil
	}}
	newTestRemoteRecorder(ids, snaps, &recChecker{}, reader).runDaily(context.Background())

	points, _, _ := snaps.snapshot()
	if len(points) != 1 || !points[0].Partial || !reflect.DeepEqual(points[0].Unreadable, []string{"gatekeeper"}) {
		t.Fatalf("points = %+v, want one partial point naming gatekeeper", points)
	}
}

// Covers AE5: an unreachable cluster gets a gap, and the next run records.
func TestRemoteRecorder_UnreachableIsAGapAndNextRunRecords(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 1)}}
	snaps := &recSnapshots{}
	checker := &recChecker{errs: map[string]error{"remote-a": k8s.TargetError{Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}}}
	reader := &recReader{fn: recFullRecord}
	r := newTestRemoteRecorder(ids, snaps, checker, reader)

	r.runDaily(context.Background())
	_, gaps, _ := snaps.snapshot()
	if want := []recGap{{"remote-a", recToday, "unreachable", 1}}; !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gaps = %+v, want %+v", gaps, want)
	}
	if len(reader.called()) != 0 {
		t.Error("the policy read ran after the pre-check could not reach the cluster")
	}

	checker.mu.Lock()
	checker.errs = nil
	checker.mu.Unlock()
	r.now = func() time.Time { return recNow.Add(24 * time.Hour) }
	r.runDaily(context.Background())
	points, _, _ := snaps.snapshot()
	if len(points) != 1 || !points[0].Date.Equal(recToday.Add(24*time.Hour)) {
		t.Fatalf("points = %+v, want the next day's point", points)
	}
}

func TestRemoteRecorder_FailureReasons(t *testing.T) {
	cases := []struct {
		name       string
		checkErr   error
		readErr    error
		readRecord *remoteRecord
		want       string // "" means nothing written
	}{
		{name: "impersonation denied", checkErr: fmt.Errorf("%w: impersonate users", k8s.ErrImpersonationDenied), want: gapImpersonationRefused},
		{name: "pre-check credentials rejected", checkErr: k8s.TargetError{Err: errors.New("Unauthorized")}, want: gapCredentialsInvalid},
		{name: "pre-check cluster gone", checkErr: k8s.TargetError{Err: fmt.Errorf("cluster x not found: %w", pgx.ErrNoRows)}},
		{name: "pre-check registry down", checkErr: k8s.TargetError{Err: errors.New("non-local clusterID requested but ClusterRouter has no cluster store")}},
		{name: "read target unreachable", readErr: k8s.TargetError{Err: &net.DNSError{Err: "no such host"}}, want: gapUnreachable},
		{name: "read credentials rejected", readErr: k8s.TargetError{Err: errors.New("decrypting auth data")}, want: gapCredentialsInvalid},
		{name: "read over budget", readErr: errRemoteBudget, want: gapDiscoveryUnavailable},
		{name: "read gap no engine", readRecord: gapRecord(gapNoPolicyEngine, nil, nil, nil), want: gapNoPolicyEngine},
		{name: "read gap forbidden", readRecord: gapRecord(gapForbidden, nil, nil, nil), want: gapForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 4)}}
			snaps := &recSnapshots{}
			reader := &recReader{fn: func(context.Context, string, string, []string) (*remoteRecord, error) {
				return tc.readRecord, tc.readErr
			}}
			newTestRemoteRecorder(ids, snaps, &recChecker{errs: map[string]error{"remote-a": tc.checkErr}}, reader).runDaily(context.Background())

			points, gaps, _ := snaps.snapshot()
			if len(points) != 0 {
				t.Fatalf("points = %+v, want none", points)
			}
			if tc.want == "" {
				if len(gaps) != 0 {
					t.Fatalf("gaps = %+v, want nothing written", gaps)
				}
				return
			}
			if want := []recGap{{"remote-a", recToday, tc.want, 4}}; !reflect.DeepEqual(gaps, want) {
				t.Fatalf("gaps = %+v, want %+v", gaps, want)
			}
		})
	}
}

func TestRemoteRecorder_PerClusterDeadlineIsUnreachable(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 2)}}
	snaps := &recSnapshots{}
	reader := &recReader{fn: func(ctx context.Context, _, _ string, _ []string) (*remoteRecord, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := newTestRemoteRecorder(ids, snaps, &recChecker{}, reader)
	r.timeout = 20 * time.Millisecond
	r.runDaily(context.Background())

	_, gaps, _ := snaps.snapshot()
	if want := []recGap{{"remote-a", recToday, gapUnreachable, 2}}; !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gaps = %+v, want %+v (written after the per-cluster deadline)", gaps, want)
	}
}

func TestRemoteRecorder_ShutdownWritesNothing(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 2), "remote-b": recIdentityRow("remote-b", 2)}}
	snaps := &recSnapshots{}
	ctx, cancel := context.WithCancel(context.Background())
	reader := &recReader{fn: func(rctx context.Context, _, _ string, _ []string) (*remoteRecord, error) {
		cancel()
		return nil, rctx.Err()
	}}
	newTestRemoteRecorder(ids, snaps, &recChecker{}, reader).runDaily(ctx)

	points, gaps, cleanups := snaps.snapshot()
	if len(points) != 0 || len(gaps) != 0 || len(cleanups) != 0 {
		t.Fatalf("points %+v gaps %+v cleanups %v, want nothing written on shutdown", points, gaps, cleanups)
	}
	if got := reader.called(); !reflect.DeepEqual(got, []string{"remote-a"}) {
		t.Errorf("read clusters %v, want the pass to stop after remote-a", got)
	}
	snaps.mu.Lock()
	defer snaps.mu.Unlock()
	if snaps.attempts != 0 {
		t.Errorf("%d gap writes attempted during shutdown, want none", snaps.attempts)
	}
}

func TestRemoteRecorder_PanicInOneClusterDoesNotStopOthers(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 1), "remote-b": recIdentityRow("remote-b", 1)}}
	snaps := &recSnapshots{}
	reader := &recReader{fn: func(ctx context.Context, c, u string, g []string) (*remoteRecord, error) {
		if c == "remote-a" {
			panic("malformed policy report")
		}
		return recFullRecord(ctx, c, u, g)
	}}
	newTestRemoteRecorder(ids, snaps, &recChecker{}, reader).runDaily(context.Background())

	points, _, cleanups := snaps.snapshot()
	if len(points) != 1 || points[0].ClusterID != "remote-b" {
		t.Fatalf("points = %+v, want remote-b's point despite remote-a's panic", points)
	}
	if len(cleanups) != 1 {
		t.Errorf("cleanups = %v, want the pass to finish with one cleanup", cleanups)
	}
}

func TestRemoteRecorder_OnlyClustersWithAnIdentityAreRead(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-b": recIdentityRow("remote-b", 1)}}
	snaps := &recSnapshots{}
	checker := &recChecker{}
	reader := &recReader{fn: recFullRecord}
	r := newTestRemoteRecorder(ids, snaps, checker, reader)
	r.runDaily(context.Background())

	if got := reader.called(); !reflect.DeepEqual(got, []string{"remote-b"}) {
		t.Errorf("daily run read %v, want only remote-b", got)
	}

	// SnapshotNow for a cluster with no identity reads nothing either.
	r.SnapshotNow(context.Background(), "remote-a")
	r.wait()
	if got := reader.called(); !reflect.DeepEqual(got, []string{"remote-b"}) {
		t.Errorf("SnapshotNow without an identity read %v", got)
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if !reflect.DeepEqual(checker.calls, []string{"remote-b"}) {
		t.Errorf("pre-check ran for %v, want only remote-b", checker.calls)
	}
}

func TestRemoteRecorder_SnapshotNowWritesTodaysPointWithCurrentRevision(t *testing.T) {
	ids := &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 12)}}
	snaps := &recSnapshots{written: make(chan struct{}, 1)}
	release := make(chan struct{})
	reader := &recReader{fn: func(ctx context.Context, c, u string, g []string) (*remoteRecord, error) {
		<-release
		return recFullRecord(ctx, c, u, g)
	}}
	r := newTestRemoteRecorder(ids, snaps, &recChecker{}, reader)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.SnapshotNow(reqCtx, "remote-a")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SnapshotNow blocked on the snapshot instead of returning")
	}
	// The request that triggered it ends; the snapshot carries on.
	cancelReq()
	close(release)
	select {
	case <-snaps.written:
	case <-time.After(5 * time.Second):
		t.Fatal("SnapshotNow wrote nothing")
	}
	r.wait()

	points, gaps, _ := snaps.snapshot()
	if len(gaps) != 0 || len(points) != 1 || points[0].IdentityRevision != 12 || !points[0].Date.Equal(recToday) {
		t.Fatalf("points %+v gaps %+v, want today's point at revision 12", points, gaps)
	}
}

func TestRemoteRecorder_DailyRunAgesOutRetention(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  *recIdentities
	}{
		{"with clusters", &recIdentities{rows: map[string]store.ComplianceIdentity{"remote-a": recIdentityRow("remote-a", 1)}}},
		{"no clusters", &recIdentities{}},
		{"identity list fails", &recIdentities{err: errors.New("db down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snaps := &recSnapshots{}
			newTestRemoteRecorder(tc.ids, snaps, &recChecker{}, &recReader{fn: recFullRecord}).runDaily(context.Background())
			_, _, cleanups := snaps.snapshot()
			if !reflect.DeepEqual(cleanups, []int{snapshotRetentionDays}) {
				t.Errorf("cleanups = %v, want one Cleanup(%d) per daily run", cleanups, snapshotRetentionDays)
			}
		})
	}
}

func TestNewRemoteComplianceRecorder_NilDependencies(t *testing.T) {
	var ids *store.ComplianceIdentityStore
	if r := NewRemoteComplianceRecorder(ids, &store.ComplianceStore{}, &k8s.ClusterRouter{}, &Handler{}, nil); r != nil {
		t.Error("constructor built a recorder over a nil identity store")
	}
	var h *Handler
	if r := NewRemoteComplianceRecorder(&store.ComplianceIdentityStore{}, &store.ComplianceStore{}, &k8s.ClusterRouter{}, h, nil); r != nil {
		t.Error("constructor built a recorder over a nil handler")
	}
	if r := NewRemoteComplianceRecorder(&store.ComplianceIdentityStore{}, &store.ComplianceStore{}, &k8s.ClusterRouter{}, &Handler{}, nil); r == nil {
		t.Error("constructor refused complete dependencies")
	}
}
