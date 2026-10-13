package store

// compliance_test.go - guarded remote compliance writes, history/gap reads,
// retention and cluster delete (#608 final slice, U2).
//
// Env-gated (testDB, skipped without KUBECENTER_TEST_DATABASE_URL). Rows are
// keyed by a unique-per-test cluster id from testOwnerID(t).

import (
	"slices"
	"testing"
	"time"
)

func complianceDay(daysAgo int) time.Time {
	n := time.Now().UTC().AddDate(0, 0, -daysAgo)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

type complianceFixture struct {
	t     *testing.T
	store *ComplianceStore
	ident *ComplianceIdentityStore
	id    string
}

// newComplianceFixture registers a cluster and gives it an identity.
func newComplianceFixture(t *testing.T) (*complianceFixture, ComplianceIdentity) {
	t.Helper()
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	insertMetricsTestCluster(ctx, t, pool, id)
	f := &complianceFixture{t: t, store: NewComplianceStore(pool), ident: NewComplianceIdentityStore(pool), id: id}
	return f, f.rename("svc-a")
}

// rename saves a new identity name and returns the stored record.
func (f *complianceFixture) rename(username string) ComplianceIdentity {
	f.t.Helper()
	got, err := f.ident.Upsert(f.t.Context(), f.id, username, []string{"auditors"}, "admin")
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *complianceFixture) point(day time.Time, score float64, rev int64) bool {
	f.t.Helper()
	ok, err := f.store.InsertRemote(f.t.Context(), &ComplianceSnapshot{
		Date: day, ClusterID: f.id, OverallScore: score, Pass: 1, Fail: 2, Warn: 3, Total: 6,
		Username: "svc-a", Groups: []string{"auditors"}, IdentityRevision: rev,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return ok
}

func (f *complianceFixture) gap(day time.Time, reason string, rev int64) bool {
	f.t.Helper()
	ok, err := f.store.InsertGap(f.t.Context(), f.id, day, reason, rev)
	if err != nil {
		f.t.Fatal(err)
	}
	return ok
}

func (f *complianceFixture) history() []ComplianceSnapshot {
	f.t.Helper()
	got, err := f.store.QueryHistory(f.t.Context(), f.id, 30)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *complianceFixture) gaps() []ComplianceGap {
	f.t.Helper()
	got, err := f.store.QueryGaps(f.t.Context(), f.id, 30)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func TestComplianceStore_GapAfterPointLeavesPoint(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(0)
	if !f.point(day, 80, ident.Revision) {
		t.Fatal("point not stored")
	}
	if f.gap(day, "forbidden", ident.Revision) {
		t.Error("same-revision gap reported stored over a point")
	}
	if h := f.history(); len(h) != 1 || h[0].OverallScore != 80 {
		t.Errorf("history = %+v; want the point kept", h)
	}
	if g := f.gaps(); len(g) != 0 {
		t.Errorf("gaps = %+v; want none", g)
	}
}

func TestComplianceStore_PointAfterGapRemovesGap(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(0)
	if !f.gap(day, "unreachable", ident.Revision) {
		t.Fatal("gap not stored")
	}
	if len(f.gaps()) != 1 {
		t.Fatalf("gaps = %+v; want one", f.gaps())
	}
	if !f.point(day, 70, ident.Revision) {
		t.Fatal("point not stored")
	}
	if g := f.gaps(); len(g) != 0 {
		t.Errorf("gaps = %+v; want the gap removed", g)
	}
	if h := f.history(); len(h) != 1 || h[0].OverallScore != 70 {
		t.Errorf("history = %+v; want the point", h)
	}
}

func TestComplianceStore_GapUpsertUpdatesReason(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(0)
	f.gap(day, "unreachable", ident.Revision)
	f.gap(day, "forbidden", ident.Revision)
	g := f.gaps()
	if len(g) != 1 || g[0].Reason != "forbidden" || g[0].IdentityRevision != ident.Revision {
		t.Errorf("gaps = %+v; want one gap with the latest reason", g)
	}
}

func TestComplianceStore_PointReplacesOnlyAtSameOrHigherRevision(t *testing.T) {
	day := complianceDay(0)
	f2, ident := newComplianceFixture(t)
	older := ident.Revision
	newer := f2.rename("svc-b").Revision
	if !f2.point(day, 50, newer) {
		t.Fatal("newer point not stored")
	}
	// The older revision is no longer current, so it cannot land at all.
	if f2.point(day, 99, older) {
		t.Error("older-revision point landed")
	}
	if h := f2.history(); len(h) != 1 || h[0].OverallScore != 50 || h[0].IdentityRevision != newer {
		t.Errorf("history = %+v; want the newer point kept", h)
	}
	// Same revision replaces.
	if !f2.point(day, 60, newer) {
		t.Error("same-revision point did not replace")
	}
	// A higher (current) revision replaces.
	newest := f2.rename("svc-c").Revision
	if !f2.point(day, 40, newest) {
		t.Error("higher-revision point did not replace")
	}
	if h := f2.history(); len(h) != 1 || h[0].OverallScore != 40 || h[0].IdentityRevision != newest {
		t.Errorf("history = %+v; want the newest point", h)
	}
}

func TestComplianceStore_LowerRevisionPointDoesNotReplaceHigher(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(0)
	// Plant a same-day point carrying a higher revision than the current
	// identity's, as a replica that raced ahead would have written. The guard
	// on the stored revision (not only on currency) must refuse the older one.
	if _, err := f.store.pool.Exec(t.Context(), `
		INSERT INTO compliance_snapshots (snapshot_date, cluster_id, overall_score, payload)
		VALUES ($1, $2, 55, jsonb_build_object('identityRevision', $3::bigint))`,
		day, f.id, ident.Revision+1000); err != nil {
		t.Fatal(err)
	}
	if f.point(day, 99, ident.Revision) {
		t.Error("lower-revision point replaced a higher-revision point")
	}
	if h := f.history(); len(h) != 1 || h[0].OverallScore != 55 {
		t.Errorf("history = %+v; want the planted point kept", h)
	}
}

func TestComplianceStore_LocalPointCountsAsRevisionZero(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(0)
	if err := f.store.Insert(t.Context(), &ComplianceSnapshot{Date: day, ClusterID: f.id, OverallScore: 10, Total: 1}); err != nil {
		t.Fatal(err)
	}
	if !f.point(day, 90, ident.Revision) {
		t.Fatal("remote point did not replace a revision-less point")
	}
	if h := f.history(); len(h) != 1 || h[0].OverallScore != 90 {
		t.Errorf("history = %+v; want the remote point", h)
	}
}

func TestComplianceStore_HigherRevisionGapReplacesPoint(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(0)
	if !f.point(day, 80, ident.Revision) {
		t.Fatal("point not stored")
	}
	newer := f.rename("svc-b")
	if !f.gap(day, "forbidden", newer.Revision) {
		t.Fatal("higher-revision gap not stored")
	}
	if h := f.history(); len(h) != 0 {
		t.Errorf("history = %+v; want the point replaced by the gap", h)
	}
	if g := f.gaps(); len(g) != 1 || g[0].Reason != "forbidden" {
		t.Errorf("gaps = %+v; want the forbidden gap", g)
	}
}

func TestComplianceStore_StaleRevisionOrNoIdentityWritesNothing(t *testing.T) {
	f, old := newComplianceFixture(t)
	day := complianceDay(0)
	f.rename("svc-b") // old revision is no longer current

	if f.point(day, 80, old.Revision) {
		t.Error("stale-revision point landed")
	}
	if f.gap(day, "unreachable", old.Revision) {
		t.Error("stale-revision gap landed")
	}
	if len(f.history()) != 0 || len(f.gaps()) != 0 {
		t.Errorf("rows written by stale revision: %+v %+v", f.history(), f.gaps())
	}

	// A cluster with no identity row.
	pool := f.store.pool
	other := testOwnerID(t) + "-noident"
	insertMetricsTestCluster(t.Context(), t, pool, other)
	ok, err := f.store.InsertRemote(t.Context(), &ComplianceSnapshot{Date: day, ClusterID: other, OverallScore: 1, IdentityRevision: 1})
	if err != nil || ok {
		t.Errorf("InsertRemote without identity = %v, %v; want false, nil", ok, err)
	}
	ok, err = f.store.InsertGap(t.Context(), other, day, "forbidden", 1)
	if err != nil || ok {
		t.Errorf("InsertGap without identity = %v, %v; want false, nil", ok, err)
	}
}

func TestComplianceStore_ClearAndRenameNewPointReplacesOldIdentityPoint(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(0)
	if !f.point(day, 80, ident.Revision) {
		t.Fatal("point not stored")
	}
	if err := f.ident.Delete(t.Context(), f.id); err != nil {
		t.Fatal(err)
	}
	renamed := f.rename("svc-a") // same name again; the sequence never reuses a revision
	if renamed.Revision <= ident.Revision {
		t.Fatalf("revision after clear = %d; want above %d", renamed.Revision, ident.Revision)
	}
	if !f.point(day, 30, renamed.Revision) {
		t.Fatal("new-identity point did not replace the old one")
	}
	if h := f.history(); len(h) != 1 || h[0].OverallScore != 30 || h[0].IdentityRevision != renamed.Revision {
		t.Errorf("history = %+v; want the new identity's point", h)
	}
}

func TestComplianceStore_RemotePointRoundTripsPayload(t *testing.T) {
	f, ident := newComplianceFixture(t)
	day := complianceDay(1)
	ok, err := f.store.InsertRemote(t.Context(), &ComplianceSnapshot{
		Date: day, ClusterID: f.id, OverallScore: 66.5, Pass: 4, Fail: 1, Warn: 2, Total: 7,
		Username: "svc-a", Groups: []string{"auditors"}, IdentityRevision: ident.Revision,
		Partial: true, Unreadable: []string{"gatekeeper"},
	})
	if err != nil || !ok {
		t.Fatalf("InsertRemote = %v, %v", ok, err)
	}
	h := f.history()
	if len(h) != 1 {
		t.Fatalf("history = %+v", h)
	}
	got := h[0]
	if got.OverallScore != 66.5 || got.Pass != 4 || got.Fail != 1 || got.Warn != 2 || got.Total != 7 ||
		got.Username != "svc-a" || !slices.Equal(got.Groups, []string{"auditors"}) ||
		got.IdentityRevision != ident.Revision || !got.Partial || !slices.Equal(got.Unreadable, []string{"gatekeeper"}) {
		t.Errorf("round trip = %+v", got)
	}
}

func TestComplianceStore_LocalInsertUnchanged(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	id := testOwnerID(t)
	s := NewComplianceStore(pool)
	day := complianceDay(0)

	if err := s.Insert(ctx, &ComplianceSnapshot{Date: day, ClusterID: id, OverallScore: 40, Pass: 2, Fail: 3, Warn: 1, Total: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, &ComplianceSnapshot{Date: day, ClusterID: id, OverallScore: 45, Pass: 3, Fail: 2, Warn: 1, Total: 6}); err != nil {
		t.Fatal(err)
	}
	h, err := s.QueryHistory(ctx, id, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 || h[0].OverallScore != 45 || h[0].Pass != 3 || h[0].Total != 6 {
		t.Fatalf("history = %+v; want the upserted local row", h)
	}
	if h[0].Username != "" || h[0].Groups != nil || h[0].IdentityRevision != 0 || h[0].Partial || h[0].Unreadable != nil {
		t.Errorf("local row carries remote fields: %+v", h[0])
	}
}

func TestComplianceStore_QueriesScopedToClusterAndWindow(t *testing.T) {
	f, ident := newComplianceFixture(t)
	other, otherIdent := newComplianceFixture(t)

	f.point(complianceDay(0), 80, ident.Revision)
	f.point(complianceDay(40), 70, ident.Revision) // outside the 30-day window
	f.gap(complianceDay(1), "forbidden", ident.Revision)
	f.gap(complianceDay(45), "forbidden", ident.Revision)
	other.point(complianceDay(0), 11, otherIdent.Revision)
	other.gap(complianceDay(2), "unreachable", otherIdent.Revision)

	if h := f.history(); len(h) != 1 || h[0].OverallScore != 80 || h[0].ClusterID != f.id {
		t.Errorf("history = %+v; want only this cluster's in-window point", h)
	}
	if g := f.gaps(); len(g) != 1 || g[0].Reason != "forbidden" {
		t.Errorf("gaps = %+v; want only this cluster's in-window gap", g)
	}
}

func TestComplianceStore_CleanupRemovesOldPointsAndGapsForEveryCluster(t *testing.T) {
	a, aIdent := newComplianceFixture(t)
	b, bIdent := newComplianceFixture(t)
	for _, c := range []struct {
		f   *complianceFixture
		rev int64
	}{{a, aIdent.Revision}, {b, bIdent.Revision}} {
		c.f.point(complianceDay(100), 50, c.rev)
		c.f.gap(complianceDay(101), "forbidden", c.rev)
		c.f.point(complianceDay(0), 60, c.rev)
		c.f.gap(complianceDay(1), "forbidden", c.rev)
	}
	if _, err := a.store.Cleanup(t.Context(), 90); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*complianceFixture{a, b} {
		hist, err := f.store.QueryHistory(t.Context(), f.id, 365)
		if err != nil {
			t.Fatal(err)
		}
		gaps, err := f.store.QueryGaps(t.Context(), f.id, 365)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 1 || hist[0].OverallScore != 60 {
			t.Errorf("history = %+v; want only the recent point", hist)
		}
		if len(gaps) != 1 || !gaps[0].Date.Equal(complianceDay(1)) {
			t.Errorf("gaps = %+v; want only the recent gap", gaps)
		}
	}
}

func TestClusterStore_DeleteRemovesComplianceHistoryOfThatClusterOnly(t *testing.T) {
	f, ident := newComplianceFixture(t)
	keep, keepIdent := newComplianceFixture(t)
	f.point(complianceDay(0), 80, ident.Revision)
	f.gap(complianceDay(1), "forbidden", ident.Revision)
	keep.point(complianceDay(0), 70, keepIdent.Revision)
	keep.gap(complianceDay(1), "forbidden", keepIdent.Revision)

	clusters := NewClusterStore(f.store.pool, "test-key")
	if err := clusters.Delete(t.Context(), f.id); err != nil {
		t.Fatal(err)
	}
	if h := f.history(); len(h) != 0 {
		t.Errorf("deleted cluster history = %+v; want none", h)
	}
	if g := f.gaps(); len(g) != 0 {
		t.Errorf("deleted cluster gaps = %+v; want none", g)
	}
	if _, err := f.ident.Get(t.Context(), f.id); err != ErrComplianceIdentityNotFound {
		t.Errorf("identity Get = %v; want not found (cascade)", err)
	}
	if len(keep.history()) != 1 || len(keep.gaps()) != 1 {
		t.Errorf("other cluster lost rows: %+v %+v", keep.history(), keep.gaps())
	}

	if err := clusters.Delete(t.Context(), f.id); err == nil || err.Error() != "cluster not found or is the local cluster" {
		t.Errorf("second Delete = %v; want the not-found error", err)
	}
}
