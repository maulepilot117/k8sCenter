package rubrik

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
)

// readFixture is the handler fixture with the local cluster's namespaces,
// the user's grants and the writes setting chosen by the test. The local
// cluster stays mapped to the RSC-native cluster idCluster.
func readFixture(t *testing.T, namespaces map[string]time.Time, writes bool, grants ...string) (*handlerFixture, *fakeChecker) {
	t.Helper()
	fx := newHandlerFixture(t)
	checker := newFakeChecker(grants...)
	fx.h.access = NewAccess(checker, lister(t, namespaces), writes)
	return fx, checker
}

// useSets makes the local cluster's RSC cluster a CDM cluster holding the
// given protection sets.
func useSets(fx *handlerFixture, sets ...Object) {
	fx.rsc.set(func(f *fakeReader) {
		f.clusters[0].Family = FamilyProtectionSet
		f.objects[idCluster] = sets
	})
}

func rowFor(t *testing.T, list NamespaceList, ns string) NamespaceRow {
	t.Helper()
	for _, r := range list.Namespaces {
		if r.Namespace == ns {
			return r
		}
	}
	t.Fatalf("no row for %q in %+v", ns, list.Namespaces)
	return NamespaceRow{}
}

func objectIDs(views []ObjectView) []string {
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.ID)
	}
	return out
}

func rscCalls(fx *handlerFixture) int {
	fx.rsc.mu.Lock()
	defer fx.rsc.mu.Unlock()
	n := 0
	for _, c := range fx.rsc.calls {
		n += c
	}
	return n
}

const (
	idB    = "77777777-7777-4777-8777-777777777777"
	idWide = "88888888-8888-4888-8888-888888888888"
)

// Covers AE1 at the HTTP layer: a read grant on team-a shows team-a and
// nothing of team-b, while an admin sees both.
func TestNamespaceListShowsOnlyGrantedNamespaces(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true, read("team-a")...)
	fx.rsc.objects[idCluster] = []Object{objNS(idNS, "team-a", idSLA, nil), objNS(idB, "team-b", idSLA, nil)}

	rec := fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "team-b") || strings.Contains(rec.Body.String(), idB) {
		t.Fatalf("non-admin answer mentions team-b: %s", rec.Body.String())
	}
	list := responseData[NamespaceList](t, rec)
	if list.NoAccess || len(list.Namespaces) != 1 || !rowFor(t, list, "team-a").Protected {
		t.Fatalf("list = %+v, want one protected team-a row", list)
	}
	if got := objectIDs(list.Objects); !slices.Equal(got, []string{idNS}) {
		t.Fatalf("objects = %v, want [%s]", got, idNS)
	}
	if list.Freshness == nil || list.Freshness.FetchedAt.IsZero() || list.Freshness.Stale {
		t.Fatalf("freshness = %+v, want a fresh fetch time", list.Freshness)
	}

	list = responseData[NamespaceList](t, fx.do(t, admin, "local", http.MethodGet, "/rubrik/namespaces", nil))
	if len(list.Namespaces) != 2 || len(list.Objects) != 2 {
		t.Fatalf("admin list = %+v, want both namespaces and objects", list)
	}
}

// Covers AE10: a non-admin asking about a remote cluster is refused before
// any grant check or RSC call, whatever grants they hold.
func TestReadsRefuseNonAdminOnRemoteCluster(t *testing.T) {
	fx, checker := readFixture(t, map[string]time.Time{"team-a": created}, true,
		append(read("team-a"), "create snapshots team-a")...)
	for _, path := range []string{"/rubrik/namespaces", "/rubrik/namespaces/team-a", "/rubrik/sla-domains"} {
		rec := fx.do(t, owner, "remote-1", http.MethodGet, path, nil)
		wantStatus(t, rec, http.StatusForbidden)
		if r := errorReason(t, rec); r != StateAdminOnly {
			t.Errorf("%s reason = %q, want %q", path, r, StateAdminOnly)
		}
	}
	if n := rscCalls(fx); n != 0 {
		t.Errorf("refused reads made %d RSC calls", n)
	}
	if n := len(checker.recorded()); n != 0 {
		t.Errorf("refused reads made %d grant checks", n)
	}

	rec := fx.do(t, admin, "remote-1", http.MethodGet, "/rubrik/namespaces", nil)
	wantStatus(t, rec, http.StatusOK)
	if list := responseData[NamespaceList](t, rec); len(list.Namespaces) != 1 || list.Namespaces[0].Namespace != "team-a" {
		t.Fatalf("admin remote list = %+v, want team-a from the set's coverage", list.Namespaces)
	}
}

// R9: a user with the read grant on no namespace gets noAccess, no
// namespace names, no RSC call, and no freshness: nothing was fetched, and a
// zero fetch time would read as a real one (R13).
func TestNoReadGrantAnywhereIsNoAccess(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true)

	rec := fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil)
	wantStatus(t, rec, http.StatusOK)
	list := responseData[NamespaceList](t, rec)
	if !list.NoAccess || len(list.Namespaces) != 0 || len(list.Objects) != 0 {
		t.Fatalf("list = %+v, want noAccess and nothing else", list)
	}
	if strings.Contains(rec.Body.String(), "team-a") {
		t.Fatalf("noAccess answer names a namespace: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"freshness"`) {
		t.Fatalf("noAccess answer carries freshness: %s", rec.Body.String())
	}

	rec = fx.do(t, owner, "local", http.MethodGet, "/rubrik/sla-domains", nil)
	sla := responseData[SLADomainList](t, rec)
	if !sla.NoAccess || len(sla.SLADomains) != 0 {
		t.Fatalf("sla domains = %+v, want noAccess", sla)
	}
	if strings.Contains(rec.Body.String(), `"freshness"`) {
		t.Fatalf("noAccess sla answer carries freshness: %s", rec.Body.String())
	}
	if n := rscCalls(fx); n != 0 {
		t.Errorf("noAccess answers made %d RSC calls", n)
	}
}

// A namespace a non-admin may not read is a 404 whether or not Rubrik
// knows it, and the check comes before any RSC call.
func TestNamespaceDetailHidesUnreadableNamespaces(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true, read("team-a")...)
	fx.rsc.objects[idCluster] = []Object{objNS(idNS, "team-a", idSLA, nil), objNS(idB, "team-b", idSLA, nil)}

	for _, ns := range []string{"team-b", "gone", "Not_A_Name"} {
		rec := fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces/"+ns, nil)
		wantStatus(t, rec, http.StatusNotFound)
	}
	if n := rscCalls(fx); n != 0 {
		t.Errorf("refused detail reads made %d RSC calls", n)
	}
	rec := fx.do(t, admin, "local", http.MethodGet, "/rubrik/namespaces/team-b", nil)
	wantStatus(t, rec, http.StatusOK)
	if d := responseData[NamespaceDetail](t, rec); len(d.Objects) != 1 || d.Objects[0].ID != idB {
		t.Fatalf("admin detail = %+v, want team-b's object", d)
	}
}

// R12: job history shows a failed job with RSC's failure reason, and the
// snapshots of the namespace's object.
func TestNamespaceDetailShowsHistory(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created}, true, read("team-a")...)
	started := created.Add(time.Hour)
	fx.rsc.jobs = []Job{
		{ID: "job-1", ObjectID: idNS, Type: "Backup", Status: JobFailed, StartTime: &started, FailureReason: "volume snapshot timed out"},
		{ID: "job-other", ObjectID: idB, Type: "Backup", Status: JobSucceeded, StartTime: &started},
	}
	fx.rsc.snapshots[idNS] = []Snapshot{{ID: "snap-1", Time: started}}

	for _, u := range []*auth.User{owner, admin} {
		rec := fx.do(t, u, "local", http.MethodGet, "/rubrik/namespaces/team-a", nil)
		wantStatus(t, rec, http.StatusOK)
		d := responseData[NamespaceDetail](t, rec)
		if len(d.Objects) != 1 {
			t.Fatalf("%s: objects = %+v", u.Username, d.Objects)
		}
		o := d.Objects[0]
		if len(o.Jobs) != 1 || o.Jobs[0].Status != JobFailed || o.Jobs[0].FailureReason != "volume snapshot timed out" {
			t.Errorf("%s: jobs = %+v, want the failed job with its reason", u.Username, o.Jobs)
		}
		if len(o.Snapshots) != 1 || o.SnapshotsFreshness == nil || o.JobsFreshness == nil {
			t.Errorf("%s: snapshots = %+v freshness %v/%v", u.Username, o.Snapshots, o.SnapshotsFreshness, o.JobsFreshness)
		}
	}
}

// Covers AE4 (R6): history from before the current namespace was created
// belongs to an earlier namespace. A non-admin sees neither the earlier
// snapshot nor its time on the object or row; an admin sees both.
func TestEarlierNamespaceHistoryIsAdminOnly(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created}, true, read("team-a")...)
	earlier, later := created.Add(-time.Hour), created.Add(time.Hour)
	fx.rsc.objects[idCluster] = []Object{objNS(idNS, "team-a", idSLA, &earlier)}
	fx.rsc.snapshots[idNS] = []Snapshot{{ID: "old", Time: earlier}}

	list := responseData[NamespaceList](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil))
	if row := rowFor(t, list, "team-a"); row.LastSnapshot != nil || list.Objects[0].NewestSnapshot != nil {
		t.Fatalf("non-admin sees the earlier namespace's snapshot time: row %v object %v", row.LastSnapshot, list.Objects[0].NewestSnapshot)
	}
	d := responseData[NamespaceDetail](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces/team-a", nil))
	if len(d.Objects[0].Snapshots) != 0 {
		t.Fatalf("non-admin sees earlier snapshots: %+v", d.Objects[0].Snapshots)
	}

	list = responseData[NamespaceList](t, fx.do(t, admin, "local", http.MethodGet, "/rubrik/namespaces", nil))
	if row := rowFor(t, list, "team-a"); row.LastSnapshot == nil || !row.LastSnapshot.Equal(earlier) {
		t.Fatalf("admin row last snapshot = %v, want %v", row.LastSnapshot, earlier)
	}

	fx.rsc.set(func(f *fakeReader) {
		f.objects[idCluster] = []Object{objNS(idNS, "team-a", idSLA, &later)}
	})
	fx.h.EvictCluster("local")
	list = responseData[NamespaceList](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil))
	if row := rowFor(t, list, "team-a"); row.LastSnapshot == nil || !row.LastSnapshot.Equal(later) {
		t.Fatalf("non-admin row last snapshot = %v, want %v", row.LastSnapshot, later)
	}
}

// The action flags follow the user's grants and the writes setting: the
// read grant alone offers no snapshot or SLA change, the snapshot grant
// offers a snapshot, and with writes off nothing is offered, to admins
// either. CanRestore is eligibility only (U4): the restore grant is checked
// on the target namespace the user picks, so a readable namespace-scoped
// object is restorable from.
func TestActionFlagsFollowGrantsAndWritesSetting(t *testing.T) {
	ns := map[string]time.Time{"team-a": created}
	for _, c := range []struct {
		name   string
		user   *auth.User
		writes bool
		grants []string
		want   ActionFlags
	}{
		{"read only", owner, true, read("team-a"), ActionFlags{CanRestore: true}},
		{"snapshot grant", owner, true, append(read("team-a"), "create snapshots team-a"), ActionFlags{CanSnapshot: true, CanRestore: true}},
		{"writes off", owner, false, append(read("team-a"), "create snapshots team-a", "update slaassignments team-a"), ActionFlags{}},
		{"admin", admin, true, nil, ActionFlags{CanSnapshot: true, CanChangeSLA: true, CanRestore: true}},
		{"admin writes off", admin, false, nil, ActionFlags{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx, _ := readFixture(t, ns, c.writes, c.grants...)
			list := responseData[NamespaceList](t, fx.do(t, c.user, "local", http.MethodGet, "/rubrik/namespaces", nil))
			if got := rowFor(t, list, "team-a").ActionFlags; got != c.want {
				t.Errorf("row flags = %+v, want %+v", got, c.want)
			}
			if got := list.Objects[0].ActionFlags; got != c.want {
				t.Errorf("object flags = %+v, want %+v", got, c.want)
			}
		})
	}
}

// R7: a namespace covered only by a set spanning several namespaces offers
// no snapshot or SLA change to a non-admin, even one holding every grant.
func TestMultiNamespaceSetIsAdminOnlyToActOn(t *testing.T) {
	grants := append(read("team-a", "team-b"), "create snapshots team-a", "update slaassignments team-a")
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true, grants...)
	useSets(fx, objSet(idWide, "namespace", Coverage{Namespaces: []string{"team-a", "team-b"}}, idSLA, nil))

	list := responseData[NamespaceList](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil))
	row := rowFor(t, list, "team-a")
	if !row.Protected || row.CanSnapshot || row.CanChangeSLA {
		t.Fatalf("row = %+v, want protected with no snapshot or SLA control", row)
	}
	if len(list.Objects) != 1 || list.Objects[0].CanSnapshot || list.Objects[0].CanChangeSLA {
		t.Fatalf("objects = %+v, want the set without controls", list.Objects)
	}
}

// R7: a snapshot of a set spanning several namespaces may hold a namespace
// the user cannot read, so its time is admin-only even when the user reads
// every namespace the set covers and the snapshot is newer than all of them.
func TestMultiNamespaceSetHistoryIsAdminOnly(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true, read("team-a", "team-b")...)
	later := created.Add(time.Hour)
	useSets(fx, objSet(idWide, "namespace", Coverage{Namespaces: []string{"team-a", "team-b"}}, idSLA, &later))

	list := responseData[NamespaceList](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil))
	if row := rowFor(t, list, "team-a"); row.LastSnapshot != nil {
		t.Fatalf("non-admin row last snapshot = %v, want none", row.LastSnapshot)
	}
	if len(list.Objects) != 1 || list.Objects[0].NewestSnapshot != nil {
		t.Fatalf("non-admin objects = %+v, want the set with no newest snapshot", list.Objects)
	}

	list = responseData[NamespaceList](t, fx.do(t, admin, "local", http.MethodGet, "/rubrik/namespaces", nil))
	if row := rowFor(t, list, "team-a"); row.LastSnapshot == nil || !row.LastSnapshot.Equal(later) {
		t.Fatalf("admin row last snapshot = %v, want %v", row.LastSnapshot, later)
	}
	if len(list.Objects) != 1 || list.Objects[0].NewestSnapshot == nil || !list.Objects[0].NewestSnapshot.Equal(later) {
		t.Fatalf("admin objects = %+v, want the set's newest snapshot %v", list.Objects, later)
	}
}

// KTD4, R7: an admin's list shows every object of the cluster, including the
// ones no row references: a relic, and a set whose coverage RSC does not
// resolve. On a remote cluster protected only by a set covering every
// namespace there are no rows (no informer names the namespaces), but the
// set is still listed.
func TestAdminNamespaceListShowsEveryObject(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created}, true, read("team-a")...)
	relic := objSet(idSet, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil)
	relic.Relic = true
	useSets(fx,
		relic,
		objSet(idWide, "cluster", Coverage{Unresolved: true}, idSLA, nil),
		objSet(idB, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil),
	)

	list := responseData[NamespaceList](t, fx.do(t, admin, "local", http.MethodGet, "/rubrik/namespaces", nil))
	got := objectIDs(list.Objects)
	for _, id := range []string{idSet, idWide, idB} {
		if !slices.Contains(got, id) {
			t.Errorf("admin objects = %v, missing %s", got, id)
		}
	}
	if got := objectIDs(responseData[NamespaceList](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil)).Objects); !slices.Equal(got, []string{idB}) {
		t.Errorf("non-admin objects = %v, want only [%s]", got, idB)
	}

	fx.rsc.set(func(f *fakeReader) {
		f.objects[idSet2] = []Object{objSet(idWide, "cluster", Coverage{All: true}, idSLA, nil)}
	})
	list = responseData[NamespaceList](t, fx.do(t, admin, "remote-1", http.MethodGet, "/rubrik/namespaces", nil))
	if len(list.Namespaces) != 0 || !slices.Equal(objectIDs(list.Objects), []string{idWide}) {
		t.Fatalf("admin remote list = %+v, want no rows and the cluster-wide set", list)
	}
}

// KTD3, KTD4: on the local cluster a namespace the informer no longer has is
// an earlier namespace. Objects covering it, a leftover namespace set or a
// set covering every namespace, never make it protected or offer an action
// on it; the admin still sees the objects that cover it.
func TestDeletedNamespaceRowCountsNoEarlierObjects(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created}, true, read("team-a")...)
	newest := created.Add(time.Hour)
	relic := objSet(idSet, "namespace", Coverage{Namespaces: []string{"gone"}}, idSLA, nil)
	relic.Relic = true
	useSets(fx,
		relic,
		objSet(idB, "namespace", Coverage{Namespaces: []string{"gone"}}, idSLA, &newest),
		objSet(idWide, "cluster", Coverage{All: true}, idSLA, nil),
	)

	check := func(what string, row NamespaceRow) {
		t.Helper()
		if row.Protected || row.Compliance != ComplianceUnknown || len(row.ActionTargets) != 0 || row.ActionFlags != (ActionFlags{}) {
			t.Errorf("%s row = %+v, want unprotected, unknown compliance, no targets or actions", what, row)
		}
		if !slices.Contains(row.ObjectIDs, idB) || !slices.Contains(row.ObjectIDs, idWide) {
			t.Errorf("%s row object ids = %v, want %s and %s kept", what, row.ObjectIDs, idB, idWide)
		}
		if row.LastSnapshot == nil || !row.LastSnapshot.Equal(newest) {
			t.Errorf("%s row last snapshot = %v, want %v kept", what, row.LastSnapshot, newest)
		}
	}

	list := responseData[NamespaceList](t, fx.do(t, admin, "local", http.MethodGet, "/rubrik/namespaces", nil))
	check("list", rowFor(t, list, "gone"))
	if row := rowFor(t, list, "team-a"); !row.Protected {
		t.Errorf("team-a row = %+v, want protected by the set covering every namespace", row)
	}

	rec := fx.do(t, admin, "local", http.MethodGet, "/rubrik/namespaces/gone", nil)
	wantStatus(t, rec, http.StatusOK)
	check("detail", responseData[NamespaceDetail](t, rec).Namespace)
}

// KTD4: a namespace protected by an object the user may not see still shows
// protected, without naming the object.
func TestHiddenProtectionStillShowsProtected(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true, read("team-a")...)
	useSets(fx,
		objSet(idSet, "namespace", Coverage{Namespaces: []string{"team-a"}}, "UNPROTECTED", nil),
		objSet(idWide, "cluster", Coverage{All: true}, idSLA, nil),
	)
	rec := fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), idWide) {
		t.Fatalf("answer names the hidden set: %s", rec.Body.String())
	}
	row := rowFor(t, responseData[NamespaceList](t, rec), "team-a")
	if !row.Protected || !row.HiddenObjects || !slices.Equal(row.ObjectIDs, []string{idSet}) {
		t.Fatalf("row = %+v, want protected with hidden objects and only the visible set", row)
	}
}

// R13, AE6: a failed fetch serves the last good data marked stale with the
// failure's class; with nothing cached the read fails with that class and
// never lists a namespace as unprotected.
func TestFailedFetchServesStaleOrFails(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created}, true, read("team-a")...)
	now := time.Now()
	fx.svc.now = func() time.Time { return now }

	wantStatus(t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil), http.StatusOK)
	now = now.Add(CacheTTL + time.Second)
	fx.rsc.set(func(f *fakeReader) { f.err = ErrAuthFailed })

	list := responseData[NamespaceList](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil))
	if list.Freshness == nil || !list.Freshness.Stale || list.Freshness.Error != HealthAuthFailed || !rowFor(t, list, "team-a").Protected {
		t.Fatalf("list = %+v, want the last good data, stale with auth_failed", list)
	}

	fx.h.EvictCluster("local")
	rec := fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil)
	wantStatus(t, rec, http.StatusBadRequest)
	if r := errorReason(t, rec); r != HealthAuthFailed {
		t.Fatalf("reason = %q, want %q", r, HealthAuthFailed)
	}
}

// R22: every setup problem reads as not_set_up to a non-admin; an admin
// sees which one it is.
func TestReadsReportSetupState(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created}, true, read("team-a")...)
	if err := fx.st.DeleteMapping(t.Context(), "local"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		user *auth.User
		want string
	}{{owner, StateNotSetUp}, {admin, StateNotMapped}} {
		for _, path := range []string{"/rubrik/namespaces", "/rubrik/namespaces/team-a", "/rubrik/sla-domains"} {
			rec := fx.do(t, c.user, "local", http.MethodGet, path, nil)
			wantStatus(t, rec, http.StatusConflict)
			if r := errorReason(t, rec); r != c.want {
				t.Errorf("%s %s reason = %q, want %q", c.user.Username, path, r, c.want)
			}
		}
	}
}

// A failed grant check fails the read rather than shortening the list
// (KTD3).
func TestFailedGrantCheckFailsTheRead(t *testing.T) {
	fx, checker := readFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true, read("team-a")...)
	checker.fail["team-b"] = true
	rec := fx.do(t, owner, "local", http.MethodGet, "/rubrik/namespaces", nil)
	wantStatus(t, rec, http.StatusServiceUnavailable)
	if r := errorReason(t, rec); r != "access_check_failed" {
		t.Fatalf("reason = %q, want access_check_failed", r)
	}
}

// SLA domains come back sorted by name without reordering the cached slice.
func TestSLADomainsSortedCopy(t *testing.T) {
	fx, _ := readFixture(t, map[string]time.Time{"team-a": created}, true, read("team-a")...)
	fx.rsc.slas = []SLADomain{{ID: "2", Name: "Silver"}, {ID: "1", Name: "gold"}}

	for range 2 {
		got := responseData[SLADomainList](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/sla-domains", nil))
		if len(got.SLADomains) != 2 || got.SLADomains[0].Name != "gold" || got.NoAccess {
			t.Fatalf("sla domains = %+v, want gold then Silver", got)
		}
	}
	tgt, err := fx.svc.Resolve(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	cached, _, err := fx.svc.SLADomains(t.Context(), tgt)
	if err != nil || cached[0].Name != "Silver" {
		t.Fatalf("cached slice = %+v (%v), want RSC's order untouched", cached, err)
	}
	if n := fx.rsc.count("SLADomains"); n != 1 {
		t.Errorf("SLADomains fetched %d times, want once", n)
	}
}
