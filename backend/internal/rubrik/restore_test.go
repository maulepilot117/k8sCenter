package rubrik

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/store"
)

const idSnapOld = "56565656-5656-4565-8565-565656565656"

// ownPerms answers Rubrik grant checks through the fake checker and the
// user's own Kubernetes permissions (R17) from a deny list: every
// permission is held unless denied as "verb resource[.group] namespace". A
// permission in fail cannot be answered: the check returns an error.
type ownPerms struct {
	rubrik *fakeChecker
	mu     sync.Mutex
	deny   map[string]bool
	fail   map[string]bool
	calls  []string
}

func (p *ownPerms) CanAccessGroupResource(ctx context.Context, clusterID, username string, groups []string,
	verb, apiGroup, resource, namespace string) (bool, error) {
	if apiGroup == APIGroup {
		return p.rubrik.CanAccessGroupResource(ctx, clusterID, username, groups, verb, apiGroup, resource, namespace)
	}
	key := permission{verb, apiGroup, resource}.String() + " " + namespace
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, key)
	if p.fail[key] {
		return false, fmt.Errorf("SubjectAccessReview to 10.0.0.7 timed out")
	}
	return !p.deny[key], nil
}

func (p *ownPerms) checked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// fakeRestorer records the restores it receives.
type fakeRestorer struct {
	mu   sync.Mutex
	sent []string // "ns snapshot cluster target" or "set snapshot"
	err  error
}

func (f *fakeRestorer) RestoreNamespace(_ context.Context, snapshotID, clusterID, target string) (JobHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "ns "+snapshotID+" "+clusterID+" "+target)
	return JobHandle{Family: FamilyNamespace, TaskchainID: "tc-r"}, f.err
}

func (f *fakeRestorer) RestoreProtectionSetSnapshot(_ context.Context, snapshotID string) (JobHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "set "+snapshotID)
	return JobHandle{Family: FamilyProtectionSet, RequestID: "req-r"}, f.err
}

func (f *fakeRestorer) restores() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// threeTeams holds team-a, team-b and team-c, created at created.
func threeTeams() map[string]time.Time {
	return map[string]time.Time{"team-a": created, "team-b": created, "team-c": created}
}

// restoreFixture is the write fixture with a restorer, the user's own
// Kubernetes permissions, and one snapshot of team-a's object taken after
// the namespace was created and one before.
func restoreFixture(t *testing.T, writes bool, grants ...string) (*handlerFixture, *fakeOps, *fakeRestorer, *ownPerms) {
	t.Helper()
	fx, ops, _, checker := writeFixture(t, threeTeams(), writes, grants...)
	perms := &ownPerms{rubrik: checker, deny: map[string]bool{}, fail: map[string]bool{}}
	fx.h.access.checker = perms
	rst := &fakeRestorer{}
	fx.h.restorer = rst
	useObjects(fx, objNS(idNS, "team-a", idSLA, nil))
	fx.rsc.set(func(f *fakeReader) {
		f.snapshots[idNS] = []Snapshot{
			{ID: idSnapshot, Time: created.Add(time.Hour)},
			{ID: idSnapOld, Time: created.Add(-time.Hour)},
		}
	})
	return fx, ops, rst, perms
}

func restoreBody(op, objectID, snapshotID, target string) map[string]any {
	return map[string]any{"operationId": op, "objectId": objectID, "snapshotId": snapshotID, "targetNamespace": target}
}

// restoreGrants is every Rubrik grant a cross-namespace restore from team-a
// into team-b needs.
var restoreGrants = []string{"get protections team-a", "get protections team-b", "create restores team-b"}

// wantRefused asserts a refused restore: the status and reason, one audit
// entry with the result, and nothing sent or recorded.
func wantRefused(t *testing.T, fx *handlerFixture, ops *fakeOps, rst *fakeRestorer, rec interface {
	Result() *http.Response
}, status int, reason string, result audit.Result) {
	t.Helper()
	res := rec.Result()
	defer res.Body.Close()
	var body struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != status || body.Error.Reason != reason {
		t.Errorf("response = %d %q, want %d %q", res.StatusCode, body.Error.Reason, status, reason)
	}
	if e := entriesFor(fx, audit.ActionRubrikRestore); len(e) != 1 || e[0].Result != result {
		t.Errorf("audit = %+v; want one %s entry", e, result)
	}
	if got := rst.restores(); len(got) != 0 {
		t.Errorf("RSC received %v", got)
	}
	if ops.count() != 0 {
		t.Error("an operation row was written for a refused restore")
	}
}

// ---------------------------------------------------------------------------

// A cross-namespace restore with every grant and permission is recorded,
// sent once with the resolved source, audited once with the job id, and a
// retry is replayed without a second restore.
func TestRestoreIsCheckedRecordedAndSent(t *testing.T) {
	fx, ops, rst, perms := restoreFixture(t, true, restoreGrants...)

	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
	wantStatus(t, rec, http.StatusCreated)
	if got := responseData[OperationView](t, rec); got.State != store.RubrikOpRunning || got.JobID != "tc-r" ||
		got.Kind != store.RubrikOpRestore || got.Namespace != "team-b" || got.ObjectID != idNS {
		t.Errorf("response = %+v; want a running restore into team-b on tc-r", got)
	}
	if got, want := rst.restores(), []string{"ns " + idSnapshot + " " + idCluster + " team-b"}; !slices.Equal(got, want) {
		t.Errorf("restores = %v, want %v", got, want)
	}

	op := ops.get(t, testOpID)
	var src restoreSource
	if err := json.Unmarshal(op.Source, &src); err != nil {
		t.Fatalf("source %s: %v", op.Source, err)
	}
	if src.ObjectID != idNS || src.Namespace != "team-a" || src.SnapshotID != idSnapshot ||
		!src.SnapshotTime.Equal(created.Add(time.Hour)) || !slices.Equal(src.Into, []string{"team-b"}) {
		t.Errorf("stored source = %+v", src)
	}
	e := entriesFor(fx, audit.ActionRubrikRestore)
	if len(e) != 1 || e[0].Result != audit.ResultSuccess || !strings.Contains(e[0].Detail, "job tc-r") ||
		!strings.Contains(e[0].Detail, "snapshot "+idSnapshot) || !strings.Contains(e[0].Detail, "from team-a; into team-b") ||
		e[0].ResourceNamespace != "team-b" || e[0].ResourceName != idNS {
		t.Errorf("audit = %+v; want one success naming the job and the source", e)
	}

	// The own-permission matrix ran in the target, and the source's
	// Secrets were checked because the restore leaves its namespace.
	checked := perms.checked()
	for _, want := range []string{
		"create deployments.apps team-b", "create secrets team-b", "bind clusterroles.rbac.authorization.k8s.io team-b",
		"escalate roles.rbac.authorization.k8s.io team-b", "get secrets team-a",
	} {
		if !slices.Contains(checked, want) {
			t.Errorf("permission %q not checked; checked %v", want, checked)
		}
	}

	again := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
	wantStatus(t, again, http.StatusOK)
	if got := responseData[OperationView](t, again); !got.Replayed {
		t.Errorf("retry = %+v; want a replay", got)
	}
	if len(rst.restores()) != 1 || len(entriesFor(fx, audit.ActionRubrikRestore)) != 1 {
		t.Error("the retry reached RSC or was audited again")
	}

	// The same id for another snapshot is a different request.
	other := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapOld, "team-b"))
	wantStatus(t, other, http.StatusConflict)
	if got := errorReason(t, other); got != "operation_id_reused" {
		t.Errorf("reused id reason = %q", got)
	}

	// The owner reads it back under the target's read grant.
	read := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
	wantStatus(t, read, http.StatusOK)
}

// Covers AE2: the restore grant on team-a only does not reach team-b.
func TestRestoreNeedsTheTargetGrant(t *testing.T) {
	fx, ops, rst, _ := restoreFixture(t, true, "get protections team-a", "get protections team-b", "create restores team-a")
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
	wantRefused(t, fx, ops, rst, rec, http.StatusForbidden, string(RefusalNoGrant), audit.ResultDenied)
}

// Covers AE3: the restore grant on team-b without the read grant on the
// source namespace finds no source, so nothing about it is disclosed.
func TestRestoreNeedsTheSourceReadGrant(t *testing.T) {
	fx, ops, rst, _ := restoreFixture(t, true, "get protections team-b", "create restores team-b")
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
	wantRefused(t, fx, ops, rst, rec, http.StatusNotFound, "object_not_found", audit.ResultDenied)
}

// A target the user cannot read is refused before anything else, since the
// operation could not be read back.
func TestRestoreNeedsTheTargetReadGrant(t *testing.T) {
	fx, ops, rst, _ := restoreFixture(t, true, "get protections team-a", "create restores team-b")
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
	wantRefused(t, fx, ops, rst, rec, http.StatusNotFound, "no_read_grant", audit.ResultDenied)
}

// A snapshot whose object is not one of the mapped RSC cluster's is not
// found, for admins too.
func TestRestoreSourceMustBeOfTheMappedCluster(t *testing.T) {
	fx, ops, rst, _ := restoreFixture(t, true)
	fx.rsc.set(func(f *fakeReader) { f.objects[idSet2] = []Object{objNS(idB, "team-a", idSLA, nil)} })
	rec := fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idB, idSnapshot, "team-a"))
	wantRefused(t, fx, ops, rst, rec, http.StatusNotFound, "object_not_found", audit.ResultDenied)
}

// A snapshot taken before the namespace was created belongs to an earlier
// namespace of the same name (R6): refused for an owner, allowed for an
// admin.
func TestRestoreFromAnEarlierNamespace(t *testing.T) {
	fx, ops, rst, _ := restoreFixture(t, true, restoreGrants...)
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapOld, "team-b"))
	wantRefused(t, fx, ops, rst, rec, http.StatusForbidden, "earlier_snapshot", audit.ResultDenied)

	fx, _, rst, _ = restoreFixture(t, true)
	wantStatus(t, fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapOld, "team-b")), http.StatusCreated)
	if len(rst.restores()) != 1 {
		t.Error("the admin's restore was not sent")
	}
}

// An object whose namespace has been deleted is an earlier namespace's: an
// owner cannot restore from it into another namespace, an admin can.
func TestRestoreFromADeletedNamespace(t *testing.T) {
	gone := objNS(idB, "gone", idSLA, nil)
	setup := func(grants ...string) (*handlerFixture, *fakeOps, *fakeRestorer) {
		fx, ops, rst, _ := restoreFixture(t, true, grants...)
		useObjects(fx, gone)
		fx.rsc.set(func(f *fakeReader) { f.snapshots[idB] = []Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour)}} })
		return fx, ops, rst
	}
	fx, ops, rst := setup("get protections gone", "get protections team-b", "create restores team-b")
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idB, idSnapshot, "team-b"))
	wantRefused(t, fx, ops, rst, rec, http.StatusNotFound, "object_not_found", audit.ResultDenied)

	fx, _, rst = setup()
	wantStatus(t, fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idB, idSnapshot, "team-b")), http.StatusCreated)
	if got := rst.restores(); len(got) != 1 || !strings.HasSuffix(got[0], " team-b") {
		t.Errorf("admin restores = %v", got)
	}
}

// Covers AE11 and KTD9: a source that may hold cluster-scoped resources, or
// a set covering several namespaces, is admin-only even for an owner with
// every grant; an admin's set restore runs in place and the matrix runs in
// every namespace the set covers.
func TestRestoreAdminOnlySources(t *testing.T) {
	scoped := objNS(idNS, "team-a", idSLA, nil)
	scoped.MayHoldClusterScoped = true
	wide := objSet(idWide, "cluster", Coverage{Namespaces: []string{"team-a", "team-b"}}, idSLA, nil)
	all := []string{
		"get protections team-a", "get protections team-b", "create restores team-a", "create restores team-b",
	}
	use := func(fx *handlerFixture, o Object) {
		if o.Family == FamilyProtectionSet {
			useSets(fx, o)
		} else {
			useObjects(fx, o)
		}
	}
	for name, o := range map[string]Object{"cluster-scoped object": scoped, "multi-namespace set": wide} {
		t.Run(name, func(t *testing.T) {
			fx, ops, rst, _ := restoreFixture(t, true, all...)
			use(fx, o)
			fx.rsc.set(func(f *fakeReader) { f.snapshots[o.ID] = []Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour)}} })
			rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, o.ID, idSnapshot, "team-a"))
			wantRefused(t, fx, ops, rst, rec, http.StatusForbidden, string(RefusalAdminOnly), audit.ResultDenied)

			fx, _, rst, perms := restoreFixture(t, true)
			use(fx, o)
			fx.rsc.set(func(f *fakeReader) { f.snapshots[o.ID] = []Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour)}} })
			wantStatus(t, fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, o.ID, idSnapshot, "team-a")), http.StatusCreated)
			if len(rst.restores()) != 1 {
				t.Fatalf("admin restores = %v", rst.restores())
			}
			if o.Family == FamilyProtectionSet {
				if got := rst.restores()[0]; got != "set "+idSnapshot {
					t.Errorf("set restore sent as %q", got)
				}
				if !slices.Contains(perms.checked(), "create deployments.apps team-b") {
					t.Error("the matrix did not run in every namespace the set restores into")
				}
			}
		})
	}
}

// R17: a restore never exceeds what the user could create themselves. A
// missing permission refuses it with the permission named, for every Rubrik
// grant held and for admins too, and a cross-namespace restore needs the
// source namespace's Secrets.
func TestRestoreOwnPermissionMatrix(t *testing.T) {
	for _, c := range []struct {
		deny, target, missing string
	}{
		{"bind clusterroles.rbac.authorization.k8s.io team-b", "team-b", "bind clusterroles.rbac.authorization.k8s.io in team-b"},
		{"escalate roles.rbac.authorization.k8s.io team-b", "team-b", "escalate roles.rbac.authorization.k8s.io in team-b"},
		{"create deployments.apps team-b", "team-b", "create deployments.apps in team-b"},
		{"create persistentvolumeclaims team-b", "team-b", "create persistentvolumeclaims in team-b"},
		{"get secrets team-a", "team-b", "get secrets in team-a"},
	} {
		for _, u := range []string{"owner", "admin"} {
			t.Run(c.deny+" as "+u, func(t *testing.T) {
				fx, ops, rst, perms := restoreFixture(t, true, restoreGrants...)
				perms.deny[c.deny] = true
				user := alice
				if u == "admin" {
					user = root
				}
				rec := fx.do(t, user, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, c.target))
				wantRefused(t, fx, ops, rst, rec, http.StatusForbidden, "missing_permission", audit.ResultDenied)
				if !strings.Contains(rec.Body.String(), c.missing) {
					t.Errorf("response does not name %q: %s", c.missing, rec.Body.String())
				}
				if e := entriesFor(fx, audit.ActionRubrikRestore); len(e) == 1 && !strings.Contains(e[0].Detail, c.missing) {
					t.Errorf("audit detail %q does not name %q", e[0].Detail, c.missing)
				}
			})
		}
	}

	// Without the source's Secrets, restoring in place is still allowed.
	fx, _, rst, perms := restoreFixture(t, true, "get protections team-a", "create restores team-a")
	perms.deny["get secrets team-a"] = true
	wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-a")), http.StatusCreated)
	if len(rst.restores()) != 1 {
		t.Error("the in-place restore was not sent")
	}
}

// A protection set restores in place only (R16): another target is 400, its
// own namespace runs the set restore, and a set whose namespaces do not
// resolve cannot be restored here at all.
func TestProtectionSetRestoresInPlace(t *testing.T) {
	set := objSet(idSet, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil)
	setup := func(o Object, grants ...string) (*handlerFixture, *fakeOps, *fakeRestorer) {
		fx, ops, rst, _ := restoreFixture(t, true, grants...)
		useSets(fx, o)
		fx.rsc.set(func(f *fakeReader) { f.snapshots[o.ID] = []Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour)}} })
		return fx, ops, rst
	}
	grants := []string{"get protections team-a", "get protections team-b", "create restores team-a", "create restores team-b"}

	fx, ops, rst := setup(set, grants...)
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idSet, idSnapshot, "team-b"))
	wantRefused(t, fx, ops, rst, rec, http.StatusBadRequest, "target_not_allowed", audit.ResultDenied)

	fx, ops, rst = setup(set, grants...)
	wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idSet, idSnapshot, "team-a")), http.StatusCreated)
	if got := rst.restores(); !slices.Equal(got, []string{"set " + idSnapshot}) {
		t.Errorf("restores = %v", got)
	}
	if op := ops.get(t, testOpID); op.JobFamily != store.RubrikJobAsyncRequest || op.JobHandle != "req-r" {
		t.Errorf("job = %s %s; want the async request", op.JobFamily, op.JobHandle)
	}

	unresolved := objSet(idSet, "cluster", Coverage{Unresolved: true}, idSLA, nil)
	fx, ops, rst = setup(unresolved)
	rec = fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idSet, idSnapshot, "team-a"))
	wantRefused(t, fx, ops, rst, rec, http.StatusBadRequest, "set_scope_unknown", audit.ResultDenied)
}

// The snapshot is looked up live: one that expired or vanished since the
// list was loaded is 404 and audited as a failure; a corrupted or
// quarantined one is refused.
func TestRestoreSnapshotLookup(t *testing.T) {
	for name, c := range map[string]struct {
		snaps  []Snapshot
		status int
		reason string
		result audit.Result
	}{
		"gone":        {nil, http.StatusNotFound, "snapshot_not_found", audit.ResultFailure},
		"expired":     {[]Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour), Expired: true}}, http.StatusNotFound, "snapshot_not_found", audit.ResultFailure},
		"past expiry": {[]Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour), Expires: ptr(time.Now().Add(-time.Minute))}}, http.StatusNotFound, "snapshot_not_found", audit.ResultFailure},
		"corrupted":   {[]Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour), Corrupted: true}}, http.StatusConflict, "snapshot_unusable", audit.ResultDenied},
		"quarantined": {[]Snapshot{{ID: idSnapshot, Time: created.Add(time.Hour), Quarantined: true}}, http.StatusConflict, "snapshot_unusable", audit.ResultDenied},
	} {
		t.Run(name, func(t *testing.T) {
			fx, ops, rst, _ := restoreFixture(t, true, restoreGrants...)
			// Load the cached list first, as the page would.
			wantStatus(t, fx.do(t, alice, "local", http.MethodGet, "/rubrik/namespaces/team-a", nil), http.StatusOK)
			fx.rsc.set(func(f *fakeReader) { f.snapshots[idNS] = c.snaps })
			rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
			wantRefused(t, fx, ops, rst, rec, c.status, c.reason, c.result)
		})
	}
}

// Refusals that come before any RSC call: writes off (AE8), a malformed
// body, a target that does not exist, and an object in flight.
func TestRestoreRefusals(t *testing.T) {
	t.Run("writes off", func(t *testing.T) {
		fx, ops, rst, _ := restoreFixture(t, false, restoreGrants...)
		rec := fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
		wantRefused(t, fx, ops, rst, rec, http.StatusForbidden, string(RefusalWritesDisabled), audit.ResultDenied)
		if rscCalls(fx) != 0 {
			t.Error("RSC was read with writes off")
		}
	})
	t.Run("missing target", func(t *testing.T) {
		fx, ops, rst, _ := restoreFixture(t, true)
		rec := fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-z"))
		wantRefused(t, fx, ops, rst, rec, http.StatusNotFound, "target_not_found", audit.ResultDenied)
	})
	t.Run("in flight", func(t *testing.T) {
		fx, ops, rst, _ := restoreFixture(t, true, restoreGrants...)
		ops.seed(store.RubrikOperation{ID: otherOp, Owner: "u-bob", ClusterID: "local", Kind: store.RubrikOpRestore,
			Namespace: "team-a", RSCObjectID: idNS, State: store.RubrikOpUnknown, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
		wantStatus(t, rec, http.StatusConflict)
		if got := errorReason(t, rec); got != "operation_in_flight" || len(rst.restores()) != 0 {
			t.Errorf("reason = %q, restores %v; want operation_in_flight and nothing sent", got, rst.restores())
		}
	})
	for name, body := range map[string]map[string]any{
		"no snapshot":   restoreBody(testOpID, idNS, "", "team-b"),
		"no object":     restoreBody(testOpID, "", idSnapshot, "team-b"),
		"bad target":    restoreBody(testOpID, idNS, idSnapshot, "Team_B"),
		"bad operation": restoreBody("op-1", idNS, idSnapshot, "team-b"),
	} {
		t.Run(name, func(t *testing.T) {
			fx, _, rst, _ := restoreFixture(t, true, restoreGrants...)
			rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", body)
			wantStatus(t, rec, http.StatusBadRequest)
			if len(rst.restores()) != 0 {
				t.Error("a malformed restore reached RSC")
			}
		})
	}
	t.Run("not wired", func(t *testing.T) {
		fx, _, _, _ := restoreFixture(t, true, restoreGrants...)
		fx.h.restorer = nil
		rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
		wantStatus(t, rec, http.StatusServiceUnavailable)
	})
}

// A restore's mutation outcome follows the write rules: a refusal proves
// nothing was applied, anything else leaves the outcome unknown, and
// neither is resent.
func TestRestoreMutationOutcomes(t *testing.T) {
	for name, c := range map[string]struct {
		err   error
		state string
	}{
		"rejected": {fmt.Errorf("%w: namespace exists at 10.0.0.7", ErrRejected), store.RubrikOpFailed},
		"timeout":  {fmt.Errorf("%w: context deadline exceeded at 10.0.0.7", ErrUnreachable), store.RubrikOpUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			fx, ops, rst, _ := restoreFixture(t, true, restoreGrants...)
			rst.err = c.err
			rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
			wantStatus(t, rec, http.StatusCreated)
			if got := responseData[OperationView](t, rec); got.State != c.state || strings.Contains(rec.Body.String(), "10.0.0.7") {
				t.Errorf("response = %s; want %s without RSC text", rec.Body.String(), c.state)
			}
			if op := ops.get(t, testOpID); op.State != c.state {
				t.Errorf("stored state = %s, want %s", op.State, c.state)
			}
			if e := entriesFor(fx, audit.ActionRubrikRestore); len(e) != 1 || e[0].Result != audit.ResultFailure ||
				strings.Contains(e[0].Detail, "10.0.0.7") {
				t.Errorf("audit = %+v; want one failure without RSC text", e)
			}
			wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b")), http.StatusOK)
			if len(rst.restores()) != 1 {
				t.Errorf("restore sent %d times", len(rst.restores()))
			}
		})
	}
}

// R18: the target picker lists existing namespaces where the user holds the
// read and restore grants; an admin sees every namespace; a protection set
// offers its own namespace only; and with writes off nothing is offered.
func TestRestoreTargets(t *testing.T) {
	path := "/rubrik/namespaces/team-a/restore-targets?objectId=" + idNS
	grants := []string{
		"get protections team-a", "create restores team-a", "get protections team-b", "create restores team-b",
		"get protections team-c", "get protections ghost", "create restores ghost",
	}

	fx, _, _, _ := restoreFixture(t, true, grants...)
	rec := fx.do(t, alice, "local", http.MethodGet, path, nil)
	wantStatus(t, rec, http.StatusOK)
	got := responseData[RestoreTargets](t, rec)
	if !slices.Equal(got.Targets, []string{"team-a", "team-b"}) || got.OverwriteNotice != msgOverwrite ||
		got.InPlaceOnly || got.SourceNamespace != "team-a" || got.Refusal != "" {
		t.Errorf("owner targets = %+v; want team-a and team-b with the notice", got)
	}

	rec = fx.do(t, root, "local", http.MethodGet, path, nil)
	if got := responseData[RestoreTargets](t, rec); !slices.Equal(got.Targets, []string{"team-a", "team-b", "team-c"}) {
		t.Errorf("admin targets = %v", got.Targets)
	}

	fx, _, _, _ = restoreFixture(t, true, grants...)
	useSets(fx, objSet(idSet, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil))
	rec = fx.do(t, alice, "local", http.MethodGet, "/rubrik/namespaces/team-a/restore-targets?objectId="+idSet, nil)
	if got := responseData[RestoreTargets](t, rec); !slices.Equal(got.Targets, []string{"team-a"}) || !got.InPlaceOnly {
		t.Errorf("set targets = %+v; want team-a in place", got)
	}

	fx, _, _, _ = restoreFixture(t, false, grants...)
	rec = fx.do(t, alice, "local", http.MethodGet, path, nil)
	if got := responseData[RestoreTargets](t, rec); len(got.Targets) != 0 || got.Refusal != string(RefusalWritesDisabled) {
		t.Errorf("targets with writes off = %+v", got)
	}

	// An object the user cannot see, or one not covering the namespace,
	// is not found.
	fx, _, _, _ = restoreFixture(t, true, "get protections team-b")
	wantStatus(t, fx.do(t, alice, "local", http.MethodGet, "/rubrik/namespaces/team-b/restore-targets?objectId="+idNS, nil), http.StatusNotFound)
	wantStatus(t, fx.do(t, root, "local", http.MethodGet, "/rubrik/namespaces/team-b/restore-targets?objectId="+idNS, nil), http.StatusNotFound)
	wantStatus(t, fx.do(t, root, "local", http.MethodGet, "/rubrik/namespaces/team-a/restore-targets?objectId=x", nil), http.StatusBadRequest)
}

// A per-namespace object that names no namespace cannot be restored, admins
// included: the restore might be cross-namespace and the source's Secrets
// cannot be checked in a namespace that cannot be named (R17, fail closed).
func TestRestoreFromAnUnresolvedSource(t *testing.T) {
	o := objNS(idNS, "team-a", idSLA, nil)
	o.Coverage = Coverage{Unresolved: true}
	fx, ops, rst, perms := restoreFixture(t, true)
	useObjects(fx, o)
	rec := fx.do(t, root, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
	wantRefused(t, fx, ops, rst, rec, http.StatusBadRequest, string(refusalSourceUnknown), audit.ResultDenied)
	if got := perms.checked(); len(got) != 0 {
		t.Errorf("own permissions checked for a refused restore: %v", got)
	}
}

// A check that cannot be answered stops the restore before RSC: the live
// snapshot list failing, or one of the user's own permission checks failing.
// Each is audited once as a failure, nothing is sent or recorded, and no raw
// error text reaches the response.
func TestRestoreFailsClosed(t *testing.T) {
	t.Run("snapshot list", func(t *testing.T) {
		fx, ops, rst, _ := restoreFixture(t, true, restoreGrants...)
		wantStatus(t, fx.do(t, alice, "local", http.MethodGet, "/rubrik/namespaces/team-a", nil), http.StatusOK)
		fx.rsc.set(func(f *fakeReader) { f.err = fmt.Errorf("%w: dial 10.0.0.7", ErrUnreachable) })
		rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
		if rec.Code < 500 {
			t.Errorf("status = %d, want a 5xx", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "10.0.0.7") {
			t.Errorf("raw RSC error text in the response: %s", rec.Body.String())
		}
		if e := entriesFor(fx, audit.ActionRubrikRestore); len(e) != 1 || e[0].Result != audit.ResultFailure {
			t.Errorf("audit = %+v; want one failure", e)
		}
		if len(rst.restores()) != 0 || ops.count() != 0 {
			t.Error("the restore was sent or recorded although its snapshot could not be looked up")
		}
	})
	t.Run("own permission", func(t *testing.T) {
		fx, ops, rst, perms := restoreFixture(t, true, restoreGrants...)
		perms.fail["create deployments.apps team-b"] = true
		rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/restores", restoreBody(testOpID, idNS, idSnapshot, "team-b"))
		wantRefused(t, fx, ops, rst, rec, http.StatusServiceUnavailable, "access_check_failed", audit.ResultFailure)
		if strings.Contains(rec.Body.String(), "10.0.0.7") {
			t.Errorf("raw error text in the response: %s", rec.Body.String())
		}
	})
}

// A restore followed through the activity series (a protection-set restore
// has no taskchain) is settled only by a recovery job of its object: a
// scheduled backup in the same window neither settles it nor keeps it open.
// With no single recovery job it does not hold its object's in-flight lock
// for good: running becomes unknown after noJobGrace, and unknown becomes
// failed after another, with a message that sends the operator to RSC.
func TestRestoreOperationSettles(t *testing.T) {
	now := time.Now()
	old := now.Add(-noJobGrace - time.Minute)
	backup := func(at time.Time, st JobStatus) Job {
		return Job{ID: "backup", ObjectID: idSet, Type: "Backup", Status: st, StartTime: &at, FailureReason: "a backup's"}
	}
	recovery := func(id string, at time.Time, st JobStatus) Job {
		return Job{ID: id, ObjectID: idSet, Type: "Recovery", Status: st, StartTime: &at, FailureReason: "restore failed in RSC"}
	}
	for name, c := range map[string]struct {
		state   string
		updated time.Time
		jobs    []Job
		want    string
		msg     string
	}{
		"recovery job beside a backup": {store.RubrikOpRunning, now,
			[]Job{backup(now, JobPartial), recovery("r1", now.Add(time.Second), JobFailed)}, store.RubrikOpFailed, "restore failed in RSC"},
		"recovery job succeeded": {store.RubrikOpRunning, now,
			[]Job{recovery("r1", now, JobSucceeded)}, store.RubrikOpSucceeded, ""},
		"a backup alone settles nothing": {store.RubrikOpRunning, now,
			[]Job{backup(now, JobSucceeded)}, store.RubrikOpRunning, ""},
		"no job yet": {store.RubrikOpRunning, now, nil, store.RubrikOpRunning, ""},
		"running, no job after the grace": {store.RubrikOpRunning, old,
			[]Job{backup(old, JobSucceeded)}, store.RubrikOpUnknown, ""},
		"running, two recovery jobs after the grace": {store.RubrikOpRunning, old,
			[]Job{recovery("r1", old, JobSucceeded), recovery("r2", old.Add(time.Second), JobSucceeded)}, store.RubrikOpUnknown, ""},
		"unknown, no job after the grace": {store.RubrikOpUnknown, old, nil, store.RubrikOpFailed, msgNoRestoreJob},
	} {
		t.Run(name, func(t *testing.T) {
			fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
			ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpRestore,
				Namespace: "team-a", RSCObjectID: idSet, State: c.state, JobFamily: store.RubrikJobAsyncRequest,
				JobHandle: "req-r", CreatedAt: c.updated, UpdatedAt: c.updated})
			w.jobs = c.jobs
			rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
			wantStatus(t, rec, http.StatusOK)
			if got := responseData[OperationView](t, rec); got.State != c.want || got.Message != c.msg {
				t.Errorf("state = %+v; want %s with message %q", got, c.want, c.msg)
			}
		})
	}

	// A namespace restore follows its taskchain; a partial backup running
	// alongside does not make the restore partial.
	t.Run("taskchain beside a partial backup", func(t *testing.T) {
		fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
		ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpRestore,
			Namespace: "team-a", RSCObjectID: idNS, State: store.RubrikOpRunning, JobFamily: store.RubrikJobTaskchain,
			JobHandle: "tc-r", CreatedAt: now, UpdatedAt: now})
		w.taskchains["tc-r"] = TaskchainState{Status: JobSucceeded}
		w.jobs = []Job{{ID: "b", ObjectID: idNS, Type: "Backup", Status: JobPartial, StartTime: &now, FailureReason: "a backup's"}}
		rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
		if got := responseData[OperationView](t, rec); got.State != store.RubrikOpSucceeded {
			t.Errorf("state = %+v; want the taskchain's succeeded", got)
		}
	})

	// A snapshot is never settled by a restore's recovery job.
	t.Run("snapshot ignores a recovery job", func(t *testing.T) {
		fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
		ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpSnapshot,
			Namespace: "team-a", RSCObjectID: idSet, State: store.RubrikOpRunning, JobFamily: store.RubrikJobAsyncRequest,
			JobHandle: "req-1", CreatedAt: now, UpdatedAt: now})
		w.jobs = []Job{recovery("r1", now, JobFailed)}
		rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
		if got := responseData[OperationView](t, rec); got.State != store.RubrikOpRunning {
			t.Errorf("state = %+v; want running", got)
		}
	})
}
