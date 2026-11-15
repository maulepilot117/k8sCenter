package rubrik

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/kubecenter/kubecenter/internal/auth"
)

// fakeChecker answers grant checks from a set of "verb resource namespace"
// grants and records every check.
type fakeChecker struct {
	mu     sync.Mutex
	grants map[string]bool
	fail   map[string]bool // namespaces whose checks error
	calls  []checkCall
}

type checkCall struct {
	cluster, user, verb, group, resource, namespace string
}

func newFakeChecker(grants ...string) *fakeChecker {
	f := &fakeChecker{grants: map[string]bool{}, fail: map[string]bool{}}
	for _, g := range grants {
		f.grants[g] = true
	}
	return f
}

func (f *fakeChecker) CanAccessGroupResource(_ context.Context, clusterID, username string, _ []string,
	verb, apiGroup, resource, namespace string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, checkCall{clusterID, username, verb, apiGroup, resource, namespace})
	if f.fail[namespace] {
		return false, errors.New("apiserver unavailable")
	}
	return apiGroup == APIGroup && f.grants[verb+" "+resource+" "+namespace], nil
}

func (f *fakeChecker) recorded() []checkCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// read grants the Rubrik read grant on each namespace.
func read(namespaces ...string) []string {
	out := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		out = append(out, "get protections "+ns)
	}
	return out
}

var created = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// lister is a local namespace informer holding each namespace, created at
// the given time.
func lister(t *testing.T, namespaces map[string]time.Time) corev1listers.NamespaceLister {
	t.Helper()
	idx := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for name, at := range namespaces {
		if err := idx.Add(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: name, CreationTimestamp: metav1.NewTime(at),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return corev1listers.NewNamespaceLister(idx)
}

func twoTeams(t *testing.T) corev1listers.NamespaceLister {
	return lister(t, map[string]time.Time{"team-a": created, "team-b": created})
}

var (
	owner = &auth.User{Username: "owner", KubernetesUsername: "owner", Roles: []string{"viewer"}}
	admin = &auth.User{Username: "root", KubernetesUsername: "root", Roles: []string{"admin"}}
)

func viewer(t *testing.T, a *Access, u *auth.User) *Viewer {
	t.Helper()
	v, err := a.Viewer(u, "local")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// validTarget is a Target Resolve vouched for, on the local cluster.
func validTarget() Target { return Target{ClusterID: "local", Revision: 1, valid: true} }

func ids(objects []Object) []string {
	out := make([]string, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.ID)
	}
	return out
}

// Covers AE1: a read grant on team-a shows team-a and nothing of team-b.
func TestNonAdminSeesOnlyGrantedNamespaces(t *testing.T) {
	ctx := context.Background()
	a := NewAccess(newFakeChecker(read("team-a")...), twoTeams(t), true)
	v := viewer(t, a, owner)
	objects := []Object{objNS("a", "team-a", idSLA, nil), objNS("b", "team-b", idSLA, nil)}

	got, err := v.VisibleObjects(ctx, objects)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"a"}) {
		t.Fatalf("visible objects = %v, want [a]", ids(got))
	}
	names, err := v.VisibleNamespaces(ctx, []string{"team-b", "team-a", "team-a", ""})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"team-a"}) {
		t.Fatalf("visible namespaces = %v, want [team-a]", names)
	}

	allowed, isAdmin, err := a.AllowedNamespaces(ctx, owner, "", []string{"team-a", "team-b"})
	if err != nil || isAdmin {
		t.Fatalf("AllowedNamespaces admin=%v err=%v", isAdmin, err)
	}
	if len(allowed) != 1 || !allowed["team-a"].Equal(created) {
		t.Fatalf("allowed = %v, want team-a created %v", allowed, created)
	}
}

func TestAdminSkipsAccessChecks(t *testing.T) {
	ctx := context.Background()
	checker := newFakeChecker()
	a := NewAccess(checker, lister(t, nil), true)
	v := viewer(t, a, admin)
	relic := objNS("relic", "gone", idSLA, nil)
	relic.Relic = true
	objects := []Object{
		objNS("a", "team-a", idSLA, nil), relic,
		objSet("all", "cluster", Coverage{All: true}, idSLA, nil),
		objSet("pattern", "cluster", Coverage{Unresolved: true}, idSLA, nil),
	}

	got, err := v.VisibleObjects(ctx, objects)
	if err != nil || len(got) != len(objects) {
		t.Fatalf("admin sees %v (err %v), want every object", ids(got), err)
	}
	snaps := []Snapshot{{ID: "s1", Time: created.Add(-time.Hour)}}
	if s, err := v.VisibleSnapshots(ctx, relic, snaps); err != nil || len(s) != 1 {
		t.Fatalf("admin relic snapshots = %v (err %v)", s, err)
	}
	names, err := v.VisibleNamespaces(ctx, []string{"x", "y"})
	if err != nil || len(names) != 2 {
		t.Fatalf("admin namespaces = %v (err %v)", names, err)
	}
	if _, isAdmin, err := a.AllowedNamespaces(ctx, admin, "remote-1", []string{"x"}); err != nil || !isAdmin {
		t.Fatalf("admin AllowedNamespaces admin=%v err=%v", isAdmin, err)
	}
	if calls := checker.recorded(); len(calls) != 0 {
		t.Fatalf("admin reads made %d access checks, want 0", len(calls))
	}
}

func TestAccessCheckErrorFailsWholeRequest(t *testing.T) {
	ctx := context.Background()
	checker := newFakeChecker(read("team-a")...)
	checker.fail["team-b"] = true
	a := NewAccess(checker, twoTeams(t), true)
	objects := []Object{objNS("a", "team-a", idSLA, nil), objNS("b", "team-b", idSLA, nil)}

	if got, err := viewer(t, a, owner).VisibleObjects(ctx, objects); !errors.Is(err, ErrAccessCheck) || got != nil {
		t.Fatalf("VisibleObjects = %v, %v; want nil and ErrAccessCheck", ids(got), err)
	}
	if got, err := viewer(t, a, owner).VisibleNamespaces(ctx, []string{"team-a", "team-b"}); !errors.Is(err, ErrAccessCheck) || got != nil {
		t.Fatalf("VisibleNamespaces = %v, %v; want nil and ErrAccessCheck", got, err)
	}
	if got, _, err := a.AllowedNamespaces(ctx, owner, "local", []string{"team-a", "team-b"}); !errors.Is(err, ErrAccessCheck) || got != nil {
		t.Fatalf("AllowedNamespaces = %v, %v; want nil and ErrAccessCheck", got, err)
	}
	// A failed check is no permission either.
	o := objNS("b", "team-b", idSLA, nil)
	if r, err := viewer(t, a, owner).CheckAction(ctx, validTarget(), ActionSnapshot, o, ""); !errors.Is(err, ErrAccessCheck) || r != RefusalCheckFailed {
		t.Fatalf("CheckAction = %q, %v; want check_failed and ErrAccessCheck", r, err)
	}
	// The same when the read grant passes and the action's own grant check
	// is the one that fails.
	delete(checker.fail, "team-b")
	checker.fail["team-c"] = true
	checker.grants["get protections team-b"] = true
	r, err := viewer(t, a, owner).CheckAction(ctx, validTarget(), ActionRestore, o, "team-c")
	if !errors.Is(err, ErrAccessCheck) || r != RefusalCheckFailed {
		t.Fatalf("CheckAction restore = %q, %v; want check_failed and ErrAccessCheck", r, err)
	}
}

// Covers AE4: snapshots from before the namespace was created, and every
// snapshot of a relic, are for admins only.
func TestEarlierSnapshotsHiddenFromNonAdmin(t *testing.T) {
	ctx := context.Background()
	a := NewAccess(newFakeChecker(read("team-c")...), lister(t, map[string]time.Time{"team-c": created}), true)
	o := objNS("c", "team-c", idSLA, nil)
	snaps := []Snapshot{
		{ID: "before", Time: created.Add(-time.Second)},
		{ID: "at", Time: created},
		{ID: "after", Time: created.Add(time.Hour)},
	}
	snapIDs := func(s []Snapshot) []string {
		out := []string{}
		for _, x := range s {
			out = append(out, x.ID)
		}
		return out
	}

	got, err := viewer(t, a, owner).VisibleSnapshots(ctx, o, snaps)
	if err != nil || !slices.Equal(snapIDs(got), []string{"at", "after"}) {
		t.Fatalf("owner snapshots = %v (err %v), want [at after]", snapIDs(got), err)
	}
	if got, _ := viewer(t, a, admin).VisibleSnapshots(ctx, o, snaps); len(got) != 3 {
		t.Fatalf("admin snapshots = %v, want all three", snapIDs(got))
	}

	relic := o
	relic.Relic = true
	if got, err := viewer(t, a, owner).VisibleSnapshots(ctx, relic, snaps); err != nil || len(got) != 0 {
		t.Fatalf("owner relic snapshots = %v (err %v), want none", snapIDs(got), err)
	}
	if ok, _ := viewer(t, a, owner).ObjectVisible(ctx, relic); ok {
		t.Fatal("a relic is visible to a non-admin")
	}
	if got, _ := viewer(t, a, admin).VisibleSnapshots(ctx, relic, snaps); len(got) != 3 {
		t.Fatalf("admin relic snapshots = %v, want all three", snapIDs(got))
	}

	// Jobs follow the same rule; a job without a start time cannot be
	// placed, and another object's job never shows, not even to an admin.
	jobs := []Job{
		{ID: "j-before", ObjectID: "c", StartTime: ptr(created.Add(-time.Minute))},
		{ID: "j-after", ObjectID: "C", StartTime: ptr(created.Add(time.Minute))},
		{ID: "j-undated", ObjectID: "c"},
		{ID: "j-other", ObjectID: "x", StartTime: ptr(created.Add(time.Minute))},
	}
	jobIDs := func(j []Job) []string {
		out := []string{}
		for _, x := range j {
			out = append(out, x.ID)
		}
		return out
	}
	if got, err := viewer(t, a, owner).VisibleJobs(ctx, o, jobs); err != nil || !slices.Equal(jobIDs(got), []string{"j-after"}) {
		t.Fatalf("owner jobs = %v (err %v), want [j-after]", jobIDs(got), err)
	}
	if got, _ := viewer(t, a, admin).VisibleJobs(ctx, o, jobs); !slices.Equal(jobIDs(got), []string{"j-before", "j-after", "j-undated"}) {
		t.Fatalf("admin jobs = %v, want every job of the object", jobIDs(got))
	}
}

func ptr[T any](v T) *T { return &v }

// Covers AE11: a set covering every namespace needs the read grant on every
// namespace to be seen, and a non-admin never acts on it.
func TestClusterWideSetNeedsEveryNamespace(t *testing.T) {
	ctx := context.Background()
	set := objSet("all", "cluster", Coverage{All: true}, idSLA, nil)

	partial := NewAccess(newFakeChecker(read("team-a")...), twoTeams(t), true)
	if ok, err := viewer(t, partial, owner).ObjectVisible(ctx, set); err != nil || ok {
		t.Fatalf("set visible with a grant on team-a only (err %v)", err)
	}

	grants := append(read("team-a", "team-b"),
		"create snapshots team-a", "create snapshots team-b",
		"update slaassignments team-a", "update slaassignments team-b",
		"create restores team-a", "create restores team-b")
	full := NewAccess(newFakeChecker(grants...), twoTeams(t), true)
	v := viewer(t, full, owner)
	if ok, err := v.ObjectVisible(ctx, set); err != nil || !ok {
		t.Fatalf("set hidden with grants on every namespace (err %v)", err)
	}
	for _, act := range []Action{ActionSnapshot, ActionChangeSLA, ActionRestore} {
		r, err := v.CheckAction(ctx, validTarget(), act, set, "team-a")
		if err != nil || r != RefusalAdminOnly {
			t.Fatalf("%s on a cluster-wide set = %q (err %v), want admin_only", act, r, err)
		}
	}
	if f, err := v.ActionFlags(ctx, validTarget(), set); err != nil || f != (ActionFlags{}) {
		t.Fatalf("flags = %+v (err %v), want none", f, err)
	}
	// The model does not record which namespaces each snapshot covered, so
	// a multi-namespace set's history is admin-only even with every grant.
	snaps := []Snapshot{{ID: "s", Time: created.Add(time.Hour)}}
	if got, err := v.VisibleSnapshots(ctx, set, snaps); err != nil || len(got) != 0 {
		t.Fatalf("owner sees %d snapshots of a cluster-wide set (err %v)", len(got), err)
	}
	if r, _ := viewer(t, full, admin).CheckAction(ctx, validTarget(), ActionSnapshot, set, ""); r != RefusalNone {
		t.Fatalf("admin snapshot of a cluster-wide set = %q, want allowed", r)
	}
	// An empty informer cannot vouch for "every namespace".
	empty := NewAccess(newFakeChecker(), lister(t, nil), true)
	if ok, _ := viewer(t, empty, owner).ObjectVisible(ctx, set); ok {
		t.Fatal("cluster-wide set visible with no namespaces listed")
	}
}

func TestUnresolvedSetIsAdminOnly(t *testing.T) {
	ctx := context.Background()
	a := NewAccess(newFakeChecker(read("team-a", "team-b")...), twoTeams(t), true)
	set := objSet("pattern", "cluster", Coverage{Unresolved: true}, idSLA, nil)
	if ok, err := viewer(t, a, owner).ObjectVisible(ctx, set); err != nil || ok {
		t.Fatalf("unresolved set visible to a non-admin (err %v)", err)
	}
	if ok, _ := viewer(t, a, admin).ObjectVisible(ctx, set); !ok {
		t.Fatal("unresolved set hidden from an admin")
	}
}

func TestSourceNamespaceGoneIsAdminOnly(t *testing.T) {
	ctx := context.Background()
	// team-d no longer exists locally; the owner still holds a grant on the
	// name (a ClusterRole, say).
	a := NewAccess(newFakeChecker(append(read("team-d"), "create snapshots team-d")...), twoTeams(t), true)
	o := objNS("d", "team-d", idSLA, nil)
	snaps := []Snapshot{{ID: "s", Time: created.Add(time.Hour)}}

	if ok, err := viewer(t, a, owner).ObjectVisible(ctx, o); err != nil || ok {
		t.Fatalf("object of a deleted namespace visible to a non-admin (err %v)", err)
	}
	if got, err := viewer(t, a, owner).VisibleSnapshots(ctx, o, snaps); err != nil || len(got) != 0 {
		t.Fatalf("owner sees %d snapshots of a deleted namespace (err %v)", len(got), err)
	}
	if got, _ := viewer(t, a, admin).VisibleSnapshots(ctx, o, snaps); len(got) != 1 {
		t.Fatal("admin cannot see snapshots of a deleted namespace")
	}
	if r, _ := viewer(t, a, owner).CheckAction(ctx, validTarget(), ActionSnapshot, o, ""); r != RefusalAdminOnly {
		t.Fatalf("snapshot of a deleted namespace = %q, want admin_only", r)
	}
	if names, _ := viewer(t, a, owner).VisibleNamespaces(ctx, []string{"team-d"}); len(names) != 0 {
		t.Fatalf("deleted namespace listed: %v", names)
	}
}

func TestCheckActionRules(t *testing.T) {
	ctx := context.Background()
	perNS := objNS("a", "team-a", idSLA, nil) // MayHoldClusterScoped false
	scoped := perNS
	scoped.MayHoldClusterScoped = true
	nsSet := objSet("set", "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil)
	nsSet.MayHoldClusterScoped = true
	relic := perNS
	relic.Relic = true
	remoteTarget := Target{ClusterID: "remote-1", Revision: 1, valid: true}

	full := append(read("team-a"), "create snapshots team-a", "update slaassignments team-a", "create restores team-b")
	cases := []struct {
		name    string
		writes  bool
		grants  []string
		user    *auth.User
		target  Target
		action  Action
		object  Object
		restore string
		want    Refusal
	}{
		{"writes off refuses admins too", false, nil, admin, validTarget(), ActionSnapshot, perNS, "", RefusalWritesDisabled},
		{"zero target is an invalid mapping", true, full, owner, Target{}, ActionSnapshot, perNS, "", RefusalMappingInvalid},
		{"target of another cluster", true, full, admin, remoteTarget, ActionSnapshot, perNS, "", RefusalMappingInvalid},
		{"unknown action", true, full, admin, validTarget(), Action("delete"), perNS, "", RefusalNotActionable},
		{"relic cannot be snapshotted", true, full, admin, validTarget(), ActionSnapshot, relic, "", RefusalNotActionable},
		{"admin restores from a relic", true, nil, admin, validTarget(), ActionRestore, relic, "team-a", RefusalNone},
		{"owner cannot restore from a relic", true, full, owner, validTarget(), ActionRestore, relic, "team-b", RefusalAdminOnly},
		{"owner snapshot with grants", true, full, owner, validTarget(), ActionSnapshot, perNS, "", RefusalNone},
		{"owner SLA change with grants", true, full, owner, validTarget(), ActionChangeSLA, perNS, "", RefusalNone},
		{"owner snapshot without the snapshot grant", true, read("team-a"), owner, validTarget(), ActionSnapshot, perNS, "", RefusalNoGrant},
		{"owner snapshot without the read grant", true, []string{"create snapshots team-a"}, owner, validTarget(), ActionSnapshot, perNS, "", RefusalNoGrant},
		{"owner restore into a namespace with the restore grant", true, full, owner, validTarget(), ActionRestore, perNS, "team-b", RefusalNone},
		{"owner restore into a namespace without it", true, full, owner, validTarget(), ActionRestore, perNS, "team-a", RefusalNoGrant},
		{"restore may hold cluster-scoped resources", true, full, owner, validTarget(), ActionRestore, scoped, "team-b", RefusalAdminOnly},
		{"protection set restore is admin-only", true, full, owner, validTarget(), ActionRestore, nsSet, "team-a", RefusalAdminOnly},
		{"namespace set snapshot with grants", true, full, owner, validTarget(), ActionSnapshot, nsSet, "", RefusalNone},
		{"restore target must be a namespace name", true, full, admin, validTarget(), ActionRestore, perNS, "Team_B", RefusalInvalidTarget},
		{"restore needs a target", true, full, owner, validTarget(), ActionRestore, perNS, "", RefusalInvalidTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAccess(newFakeChecker(tc.grants...), twoTeams(t), tc.writes)
			got, err := viewer(t, a, tc.user).CheckAction(ctx, tc.target, tc.action, tc.object, tc.restore)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("refusal = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestActionFlagsMatchCheckAction(t *testing.T) {
	ctx := context.Background()
	o := objNS("a", "team-a", idSLA, nil)
	a := NewAccess(newFakeChecker(append(read("team-a"), "create snapshots team-a")...), twoTeams(t), true)
	f, err := viewer(t, a, owner).ActionFlags(ctx, validTarget(), o)
	if err != nil {
		t.Fatal(err)
	}
	// Restore eligibility needs only the read grant on the source: the
	// target picker checks the restore grant per namespace.
	if want := (ActionFlags{CanSnapshot: true, CanRestore: true}); f != want {
		t.Fatalf("flags = %+v, want %+v", f, want)
	}
	off := NewAccess(newFakeChecker(), twoTeams(t), false)
	if f, _ := viewer(t, off, admin).ActionFlags(ctx, validTarget(), o); f != (ActionFlags{}) {
		t.Fatalf("flags with writes off = %+v, want none", f)
	}
	if f, _ := viewer(t, a, admin).ActionFlags(ctx, Target{}, o); f != (ActionFlags{}) {
		t.Fatalf("flags with an invalid mapping = %+v, want none", f)
	}
}

// Each flag is pinned on its own, and each agrees with CheckAction: snapshot
// and SLA with the same action, restore with a restore into team-b, where
// every case below holds the restore grant (KTD3: the UI never offers a
// control the server refuses, nor hides one it allows).
func TestActionFlagsAgreeWithCheckAction(t *testing.T) {
	ctx := context.Background()
	perNS := objNS("a", "team-a", idSLA, nil)
	scoped := perNS
	scoped.MayHoldClusterScoped = true
	relic := perNS
	relic.Relic = true
	multi := objSet("multi", "cluster", Coverage{Namespaces: []string{"team-a", "team-b"}}, idSLA, nil)
	gone := objNS("d", "team-d", idSLA, nil)
	remoteTarget := Target{ClusterID: "remote-1", Revision: 1, valid: true}

	full := append(read("team-a", "team-b", "team-d"),
		"create snapshots team-a", "create snapshots team-b", "create snapshots team-d",
		"update slaassignments team-a", "update slaassignments team-b", "update slaassignments team-d",
		"create restores team-b")
	cases := []struct {
		name   string
		grants []string
		user   *auth.User
		target Target
		object Object
		want   ActionFlags
	}{
		{"owner with every grant", full, owner, validTarget(), perNS, ActionFlags{CanSnapshot: true, CanChangeSLA: true, CanRestore: true}},
		{"owner with the SLA grant only", append(read("team-a"), "update slaassignments team-a", "create restores team-b"), owner, validTarget(), perNS, ActionFlags{CanChangeSLA: true, CanRestore: true}},
		{"owner without the read grant", []string{"create snapshots team-a", "update slaassignments team-a", "create restores team-b"}, owner, validTarget(), perNS, ActionFlags{}},
		{"snapshots may hold cluster-scoped resources", full, owner, validTarget(), scoped, ActionFlags{CanSnapshot: true, CanChangeSLA: true}},
		{"owner on a relic", full, owner, validTarget(), relic, ActionFlags{}},
		{"owner on a multi-namespace set", full, owner, validTarget(), multi, ActionFlags{}},
		{"owner on a deleted namespace", full, owner, validTarget(), gone, ActionFlags{}},
		{"target of another cluster", full, owner, remoteTarget, perNS, ActionFlags{}},
		{"admin on a relic restores only", nil, admin, validTarget(), relic, ActionFlags{CanRestore: true}},
		{"admin on cluster-scoped snapshots", nil, admin, validTarget(), scoped, ActionFlags{CanSnapshot: true, CanChangeSLA: true, CanRestore: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAccess(newFakeChecker(tc.grants...), twoTeams(t), true)
			f, err := viewer(t, a, tc.user).ActionFlags(ctx, tc.target, tc.object)
			if err != nil {
				t.Fatal(err)
			}
			if f != tc.want {
				t.Fatalf("flags = %+v, want %+v", f, tc.want)
			}
			allowed := func(action Action, targetNamespace string) bool {
				r, err := viewer(t, a, tc.user).CheckAction(ctx, tc.target, action, tc.object, targetNamespace)
				if err != nil {
					t.Fatal(err)
				}
				return r == RefusalNone
			}
			server := ActionFlags{
				CanSnapshot:  allowed(ActionSnapshot, ""),
				CanChangeSLA: allowed(ActionChangeSLA, ""),
				CanRestore:   allowed(ActionRestore, "team-b"),
			}
			if f != server {
				t.Fatalf("flags = %+v, but CheckAction allows %+v", f, server)
			}
		})
	}
}

// Covers AE10's second line: a non-admin never gets a view of a remote
// cluster, whatever grants they hold there.
func TestRemoteClusterIsAdminOnly(t *testing.T) {
	ctx := context.Background()
	checker := newFakeChecker(read("team-a")...)
	a := NewAccess(checker, twoTeams(t), true)
	if _, err := a.Viewer(owner, "remote-1"); !errors.Is(err, ErrRemoteAdminOnly) {
		t.Fatalf("remote viewer err = %v, want ErrRemoteAdminOnly", err)
	}
	allowed, isAdmin, err := a.AllowedNamespaces(ctx, owner, "remote-1", []string{"team-a"})
	if err != nil || isAdmin || len(allowed) != 0 || allowed == nil {
		t.Fatalf("remote AllowedNamespaces = %v admin=%v err=%v; want an empty map", allowed, isAdmin, err)
	}
	if v, err := a.Viewer(admin, "remote-1"); err != nil || !v.Admin() {
		t.Fatalf("admin remote viewer err = %v", err)
	}
	if _, err := a.Viewer(nil, "local"); !errors.Is(err, ErrAccessCheck) {
		t.Fatalf("nil user err = %v, want ErrAccessCheck", err)
	}
	if calls := checker.recorded(); len(calls) != 0 {
		t.Fatalf("remote refusals made %d access checks", len(calls))
	}
}

// The grants are SARs on the virtual group, on the viewer's cluster, with
// each action's own verb and resource (KTD2), and each is asked once per
// request.
func TestGrantChecksUseTheVirtualGroup(t *testing.T) {
	ctx := context.Background()
	checker := newFakeChecker()
	a := NewAccess(checker, twoTeams(t), true)
	v, err := a.Viewer(owner, "") // "" is the local cluster
	if err != nil {
		t.Fatal(err)
	}
	checker.grants["get protections team-a"] = true
	o := objNS("a", "team-a", idSLA, nil)
	for _, act := range []Action{ActionSnapshot, ActionChangeSLA, ActionRestore} {
		if _, err := v.CheckAction(ctx, validTarget(), act, o, "team-b"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := v.CanRead(ctx, "team-a"); err != nil {
		t.Fatal(err)
	}
	want := []checkCall{
		{"local", "owner", "get", APIGroup, "protections", "team-a"},
		{"local", "owner", "create", APIGroup, "snapshots", "team-a"},
		{"local", "owner", "update", APIGroup, "slaassignments", "team-a"},
		{"local", "owner", "create", APIGroup, "restores", "team-b"},
	}
	if got := checker.recorded(); !slices.Equal(got, want) {
		t.Fatalf("checks =\n%v\nwant\n%v", got, want)
	}
}
