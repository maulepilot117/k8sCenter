package rubrik

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fixed ids for the contract tests.
const (
	idCluster  = "11111111-1111-4111-8111-111111111111"
	idNS       = "22222222-2222-4222-8222-222222222222"
	idSet      = "33333333-3333-4333-8333-333333333333"
	idSet2     = "44444444-4444-4444-8444-444444444444"
	idSnapshot = "55555555-5555-4555-8555-555555555555"
	idSLA      = "66666666-6666-4666-8666-666666666666"
)

var opNameRE = regexp.MustCompile(`^\s*(?:query|mutation)\s+(\w+)`)

// gqlCall is one GraphQL request the fake RSC received.
type gqlCall struct {
	Op    string
	Query string
	Vars  map[string]any
}

// fakeRSC is a TLS GraphQL tenant that dispatches on the operation name.
// A handler returns the answer's data, or an error to answer with a
// GraphQL error.
type fakeRSC struct {
	ft *fakeTenant

	mu       sync.Mutex
	calls    []gqlCall
	handlers map[string]func(vars map[string]any) (any, error)
}

func newFakeRSC(t *testing.T) (*fakeRSC, *Operations) {
	t.Helper()
	f := &fakeRSC{ft: newFakeTenant(t), handlers: map[string]func(map[string]any) (any, error){}}
	f.ft.setGraphQL(func(w http.ResponseWriter, r *http.Request, _ string) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		m := opNameRE.FindStringSubmatch(req.Query)
		if m == nil {
			writeJSON(w, map[string]any{"errors": []any{map[string]any{"message": "anonymous operation"}}})
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, gqlCall{Op: m[1], Query: req.Query, Vars: req.Variables})
		h := f.handlers[m[1]]
		f.mu.Unlock()
		if h == nil {
			writeJSON(w, map[string]any{"errors": []any{map[string]any{"message": "no handler for " + m[1]}}})
			return
		}
		data, err := h(req.Variables)
		if err != nil {
			writeJSON(w, map[string]any{"errors": []any{map[string]any{"message": err.Error()}}})
			return
		}
		writeJSON(w, map[string]any{"data": data})
	})
	return f, &Operations{q: f.ft.client(f.ft.source(), nil)}
}

func (f *fakeRSC) handle(op string, h func(vars map[string]any) (any, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[op] = h
}

// answer registers a fixed answer for op.
func (f *fakeRSC) answer(op string, data any) {
	f.handle(op, func(map[string]any) (any, error) { return data, nil })
}

func (f *fakeRSC) recorded() []gqlCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gqlCall(nil), f.calls...)
}

func (f *fakeRSC) last(t *testing.T) gqlCall {
	t.Helper()
	calls := f.recorded()
	if len(calls) == 0 {
		t.Fatal("no request was sent")
	}
	return calls[len(calls)-1]
}

// input returns a mutation's input variable.
func (c gqlCall) input(t *testing.T) map[string]any {
	t.Helper()
	in, ok := c.Vars["input"].(map[string]any)
	if !ok {
		t.Fatalf("%s: input variable = %#v", c.Op, c.Vars["input"])
	}
	return in
}

func conn(field string, nodes []any, next bool, cursor string) map[string]any {
	return map[string]any{field: map[string]any{
		"nodes":    nodes,
		"pageInfo": map[string]any{"hasNextPage": next, "endCursor": cursor},
	}}
}

func ts(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

// TestOperationsSendNameAndVariables pins each operation's name and the
// variables it sends.
func TestOperationsSendNameAndVariables(t *testing.T) {
	f, ops := newFakeRSC(t)
	ctx := context.Background()
	f.answer("RubrikK8sClusters", conn("k8sClusters", []any{map[string]any{"id": idCluster, "name": "prod", "status": "STATUS_CONNECTED"}}, false, ""))
	f.answer("RubrikKubernetesClusters", conn("kubernetesClusters", []any{map[string]any{"id": idSet2, "name": "cdm-prod", "status": "Connected"}}, false, ""))
	f.answer("RubrikK8sNamespaces", conn("k8sNamespaces", nil, false, ""))
	f.answer("RubrikK8sProtectionSets", conn("kubernetesProtectionSets", nil, false, ""))
	f.answer("RubrikK8sNamespaceSnapshots", map[string]any{"k8sNamespace": map[string]any{"snapshotConnection": map[string]any{"nodes": []any{}, "pageInfo": map[string]any{}}}})
	f.answer("RubrikK8sProtectionSetSnapshots", map[string]any{"kubernetesProtectionSet": map[string]any{"snapshotConnection": nil}})
	f.answer("RubrikActivitySeries", conn("activitySeriesConnection", nil, false, ""))
	f.answer("RubrikTaskchain", map[string]any{"taskchain": map[string]any{"state": "RUNNING", "error": ""}})
	f.answer("RubrikSlaDomains", conn("slaDomains", nil, false, ""))
	f.answer("RubrikCreateK8sNamespaceSnapshots", map[string]any{"createK8sNamespaceSnapshots": []any{map[string]any{"jobId": 7, "taskchainId": "tc-1"}}})
	f.answer("RubrikCreateK8sProtectionSetSnapshot", map[string]any{"createK8sProtectionSetSnapshot": map[string]any{"id": "req-1", "status": "QUEUED"}})
	f.answer("RubrikAssignSla", map[string]any{"assignSla": map[string]any{"success": true}})
	f.answer("RubrikRestoreK8sNamespace", map[string]any{"restoreK8sNamespace": map[string]any{"jobId": 9, "taskchainId": "tc-2"}})
	f.answer("RubrikRestoreK8sProtectionSetSnapshot", map[string]any{"restoreK8sProtectionSetSnapshot": map[string]any{"id": "req-2", "status": "QUEUED"}})

	since := ts("2026-11-01T00:00:00Z")
	cases := []struct {
		name string
		op   string
		run  func() error
		vars map[string]any // expected subset of the variables
	}{
		{"namespaces", "RubrikK8sNamespaces", func() error { _, err := ops.NamespaceObjects(ctx, idCluster); return err },
			map[string]any{"clusterId": idCluster, "first": float64(pageSize)}},
		{"protection sets", "RubrikK8sProtectionSets", func() error { _, err := ops.ProtectionSets(ctx, idCluster); return err },
			map[string]any{"clusterId": idCluster, "first": float64(pageSize)}},
		{"namespace snapshots", "RubrikK8sNamespaceSnapshots", func() error { _, err := ops.Snapshots(ctx, FamilyNamespace, idNS, 0); return err },
			map[string]any{"id": idNS}},
		{"set snapshots", "RubrikK8sProtectionSetSnapshots", func() error { _, err := ops.Snapshots(ctx, FamilyProtectionSet, idSet, 0); return err },
			map[string]any{"id": idSet}},
		{"jobs", "RubrikActivitySeries", func() error {
			_, err := ops.Jobs(ctx, ActivityFilter{ObjectIDs: []string{idNS}, Statuses: []string{"FAILURE"}, StartedAfter: since})
			return err
		}, map[string]any{"filters": map[string]any{"objectFid": []any{idNS}, "lastActivityStatus": []any{"FAILURE"}, "startTimeGt": "2026-11-01T00:00:00Z"}}},
		{"taskchain", "RubrikTaskchain", func() error { _, err := ops.Taskchain(ctx, "tc-1"); return err },
			map[string]any{"id": "tc-1"}},
		{"sla domains", "RubrikSlaDomains", func() error { _, err := ops.SLADomains(ctx); return err },
			map[string]any{"first": float64(pageSize)}},
		{"namespace snapshot", "RubrikCreateK8sNamespaceSnapshots", func() error { _, err := ops.CreateSnapshot(ctx, FamilyNamespace, idNS, idSLA); return err },
			map[string]any{"input": map[string]any{"snapshotInput": []any{map[string]any{"namespaceId": idNS, "onDemandSnapshotSlaId": idSLA}}}}},
		{"set snapshot", "RubrikCreateK8sProtectionSetSnapshot", func() error { _, err := ops.CreateSnapshot(ctx, FamilyProtectionSet, idSet, idSLA); return err },
			map[string]any{"input": map[string]any{"id": idSet, "config": map[string]any{"slaId": idSLA}}}},
		{"assign sla", "RubrikAssignSla", func() error { return ops.AssignSLA(ctx, []string{idNS}, idSLA) },
			map[string]any{"input": map[string]any{"objectIds": []any{idNS}, "slaDomainAssignType": "protectWithSlaId", "slaOptionalId": idSLA, "existingSnapshotRetention": "RETAIN_SNAPSHOTS"}}},
		{"restore namespace", "RubrikRestoreK8sNamespace", func() error {
			_, err := ops.RestoreNamespace(ctx, idSnapshot, idCluster, "team-a")
			return err
		}, map[string]any{"input": map[string]any{"snapshotUuid": idSnapshot, "targetClusterUuid": idCluster, "targetNamespaceName": "team-a"}}},
		{"restore set", "RubrikRestoreK8sProtectionSetSnapshot", func() error { _, err := ops.RestoreProtectionSetSnapshot(ctx, idSnapshot); return err },
			map[string]any{"input": map[string]any{"id": idSnapshot, "jobConfig": map[string]any{}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got := f.last(t)
			if got.Op != tc.op {
				t.Fatalf("operation = %q, want %q", got.Op, tc.op)
			}
			for k, want := range tc.vars {
				if !reflect.DeepEqual(got.Vars[k], want) {
					t.Fatalf("variable %s = %#v, want %#v", k, got.Vars[k], want)
				}
			}
		})
	}

	t.Run("clusters", func(t *testing.T) {
		clusters, err := ops.Clusters(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := []Cluster{
			{Family: FamilyNamespace, ID: idCluster, Name: "prod", Status: "STATUS_CONNECTED"},
			{Family: FamilyProtectionSet, ID: idSet2, Name: "cdm-prod", Status: "Connected"},
		}
		if !reflect.DeepEqual(clusters, want) {
			t.Fatalf("clusters = %+v, want %+v", clusters, want)
		}
	})
}

func TestNamespaceListCollectsEveryPage(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.handle("RubrikK8sNamespaces", func(vars map[string]any) (any, error) {
		switch vars["after"] {
		case nil:
			return conn("k8sNamespaces", []any{map[string]any{"id": "a", "namespaceName": "team-a", "clusterScoped": false}}, true, "c1"), nil
		case "c1":
			return conn("k8sNamespaces", []any{map[string]any{"id": "b", "namespaceName": "team-b", "clusterScoped": false}}, true, "c2"), nil
		case "c2":
			return conn("k8sNamespaces", []any{map[string]any{"id": "c", "namespaceName": "team-c", "clusterScoped": false}}, false, ""), nil
		}
		return nil, fmt.Errorf("unexpected cursor %v", vars["after"])
	})
	objs, err := ops.NamespaceObjects(context.Background(), idCluster)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range objs {
		names = append(names, o.Name)
	}
	if strings.Join(names, ",") != "team-a,team-b,team-c" {
		t.Fatalf("namespaces = %v", names)
	}
	if n := len(f.recorded()); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
}

func TestPagingStopsOnBadCursors(t *testing.T) {
	t.Run("repeated cursor", func(t *testing.T) {
		f, ops := newFakeRSC(t)
		f.answer("RubrikK8sNamespaces", conn("k8sNamespaces", nil, true, "same"))
		_, err := ops.NamespaceObjects(context.Background(), idCluster)
		if !errors.Is(err, ErrUnreadable) {
			t.Fatalf("err = %v, want ErrUnreadable", err)
		}
		if n := len(f.recorded()); n != 2 {
			t.Fatalf("requests = %d, want 2", n)
		}
	})
	t.Run("endless fresh cursors stop at the page cap", func(t *testing.T) {
		f, ops := newFakeRSC(t)
		var mu sync.Mutex
		n := 0
		f.handle("RubrikK8sNamespaces", func(map[string]any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			n++
			return conn("k8sNamespaces", nil, true, fmt.Sprintf("c%d", n)), nil
		})
		_, err := ops.NamespaceObjects(context.Background(), idCluster)
		if !errors.Is(err, ErrUnreadable) {
			t.Fatalf("err = %v, want ErrUnreadable", err)
		}
		if got := len(f.recorded()); got != 100 { // re-derived page cap
			t.Fatalf("requests = %d, want 100", got)
		}
	})
	t.Run("next page without a cursor", func(t *testing.T) {
		f, ops := newFakeRSC(t)
		f.answer("RubrikK8sNamespaces", conn("k8sNamespaces", nil, true, ""))
		if _, err := ops.NamespaceObjects(context.Background(), idCluster); !errors.Is(err, ErrUnreadable) {
			t.Fatalf("err = %v, want ErrUnreadable", err)
		}
	})
}

func TestProtectionSetCoverageAndClusterScope(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.answer("RubrikK8sProtectionSets", conn("kubernetesProtectionSets", []any{
		map[string]any{"id": "cluster-all", "name": "everything", "rsType": "cluster", "definition": "{}"},
		map[string]any{"id": "cluster-pattern", "name": "teams", "rsType": "cluster", "namespaceIncludePatterns": "team-*"},
		map[string]any{"id": "app", "name": "shop", "rsType": "application", "namespaceIncludePatterns": "shop-*"},
		map[string]any{"id": "ns", "name": "team-a-set", "rsType": "namespace", "namespace": "team-a", "definition": `{"includeResources":["Deployment","ConfigMap"]}`},
		map[string]any{"id": "ns-crd", "name": "team-b-set", "rsType": "Namespace", "namespace": "team-b", "definition": `{"includeResources":["ClusterRoleBinding"]}`},
		map[string]any{"id": "ns-deps", "name": "team-c-set", "rsType": "namespace", "namespace": "team-c", "customResourceDependencies": "certificates.cert-manager.io"},
		map[string]any{"id": "ns-empty", "name": "orphan", "rsType": "namespace"},
		// Shapes a kind-name match would miss: lowercase plurals and a wildcard.
		map[string]any{"id": "ns-plural", "name": "team-d-set", "rsType": "namespace", "namespace": "team-d", "definition": `{"includes":[{"resource":"clusterrolebindings"}]}`},
		map[string]any{"id": "ns-wildcard", "name": "team-e-set", "rsType": "namespace", "namespace": "team-e", "definition": `{"resources":["*"]}`},
	}, false, ""))
	objs, err := ops.ProtectionSets(context.Background(), idCluster)
	if err != nil {
		t.Fatal(err)
	}
	// KTD9 fails closed: the schema gives the definition no format, so no
	// protection set, of any type, can be shown free of cluster-scoped kinds.
	want := map[string]struct {
		cov      Coverage
		mayHold  bool
		nsScoped bool
	}{
		"cluster-all":     {Coverage{All: true}, true, false},
		"cluster-pattern": {Coverage{Unresolved: true}, true, false},
		"app":             {Coverage{Unresolved: true}, true, false},
		"ns":              {Coverage{Namespaces: []string{"team-a"}}, true, true},
		"ns-crd":          {Coverage{Namespaces: []string{"team-b"}}, true, true},
		"ns-deps":         {Coverage{Namespaces: []string{"team-c"}}, true, true},
		"ns-empty":        {Coverage{Unresolved: true}, true, false},
		"ns-plural":       {Coverage{Namespaces: []string{"team-d"}}, true, true},
		"ns-wildcard":     {Coverage{Namespaces: []string{"team-e"}}, true, true},
	}
	if len(objs) != len(want) {
		t.Fatalf("decoded %d sets, want %d", len(objs), len(want))
	}
	for _, o := range objs {
		w := want[o.ID]
		if o.Family != FamilyProtectionSet {
			t.Errorf("%s family = %q", o.ID, o.Family)
		}
		if !reflect.DeepEqual(o.Coverage, w.cov) {
			t.Errorf("%s coverage = %+v, want %+v", o.ID, o.Coverage, w.cov)
		}
		if o.MayHoldClusterScoped != w.mayHold {
			t.Errorf("%s mayHoldClusterScoped = %v, want %v", o.ID, o.MayHoldClusterScoped, w.mayHold)
		}
		if o.NamespaceScoped() != w.nsScoped {
			t.Errorf("%s namespaceScoped = %v, want %v", o.ID, o.NamespaceScoped(), w.nsScoped)
		}
	}
	if !objs[0].Coverage.Covers("anything") || objs[1].Coverage.Covers("team-a") {
		t.Fatal("a cluster set without patterns must cover every namespace; a pattern set none")
	}
}

func TestNamespaceObjectClusterScopedFailsClosed(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.answer("RubrikK8sNamespaces", conn("k8sNamespaces", []any{
		map[string]any{"id": "plain", "namespaceName": "team-a", "clusterScoped": false},
		map[string]any{"id": "scoped", "namespaceName": "team-b", "clusterScoped": true},
		map[string]any{"id": "missing", "namespaceName": "team-c"},
	}, false, ""))
	objs, err := ops.NamespaceObjects(context.Background(), idCluster)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, o := range objs {
		got[o.ID] = o.MayHoldClusterScoped
	}
	if got["plain"] || !got["scoped"] || !got["missing"] {
		t.Fatalf("mayHoldClusterScoped = %v, want plain false, scoped and missing true", got)
	}
}

func objNS(id, ns string, sla string, newest *time.Time) Object {
	return Object{Family: FamilyNamespace, ID: id, Name: ns, Coverage: Coverage{Namespaces: []string{ns}},
		EffectiveSLA: SLARef{ID: sla, Name: sla}, NewestSnapshot: newest}
}

func objSet(id, setType string, cov Coverage, sla string, newest *time.Time) Object {
	return Object{Family: FamilyProtectionSet, ID: id, SetType: setType, Coverage: cov,
		EffectiveSLA: SLARef{ID: sla, Name: sla}, NewestSnapshot: newest}
}

func TestSummarizeNamespaceAcrossFamilies(t *testing.T) {
	older, newer := ts("2026-11-10T00:00:00Z"), ts("2026-11-12T00:00:00Z")
	objects := []Object{
		objNS("ns-obj", "team-a", idSLA, older),
		objSet("ns-set", "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, newer),
		objSet("other", "namespace", Coverage{Namespaces: []string{"team-b"}}, idSLA, newer),
	}
	judge := map[string]Compliance{"ns-obj": ComplianceInCompliance, "ns-set": ComplianceOutOfCompliance}
	sum := SummarizeNamespace("team-a", objects, func(o Object) Compliance { return judge[o.ID] })
	if !sum.Protected {
		t.Fatal("namespace not protected")
	}
	if sum.Compliance != ComplianceOutOfCompliance {
		t.Fatalf("compliance = %q, want the worst", sum.Compliance)
	}
	if !reflect.DeepEqual(sum.ObjectIDs, []string{"ns-obj", "ns-set"}) {
		t.Fatalf("objects = %v", sum.ObjectIDs)
	}
	if !reflect.DeepEqual(sum.ActionTargets, []string{"ns-obj"}) {
		t.Fatalf("action targets = %v, want only the per-namespace object", sum.ActionTargets)
	}
	if sum.LastSnapshot == nil || !sum.LastSnapshot.Equal(*newer) {
		t.Fatalf("last snapshot = %v, want %v", sum.LastSnapshot, newer)
	}
}

func TestSummarizeNamespaceTargets(t *testing.T) {
	objects := []Object{
		objSet("set-1", "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil),
		objSet("set-2", "namespace", Coverage{Namespaces: []string{"team-a"}}, "UNPROTECTED", nil),
		objSet("wide", "cluster", Coverage{All: true}, idSLA, nil),
		objSet("unresolved", "application", Coverage{Unresolved: true}, idSLA, nil),
	}
	sum := SummarizeNamespace("team-a", objects, func(Object) Compliance { return ComplianceInCompliance })
	if !reflect.DeepEqual(sum.ActionTargets, []string{"set-1", "set-2"}) {
		t.Fatalf("action targets = %v, want both namespace sets for the user to pick", sum.ActionTargets)
	}
	if !reflect.DeepEqual(sum.ObjectIDs, []string{"set-1", "set-2", "wide"}) {
		t.Fatalf("objects = %v; an unresolved set covers no namespace", sum.ObjectIDs)
	}
	if !sum.Protected || sum.Compliance != ComplianceInCompliance {
		t.Fatalf("protected = %v compliance = %q", sum.Protected, sum.Compliance)
	}

	// Only an unprotected object: not protected, compliance unknown.
	sum = SummarizeNamespace("team-a", objects[1:2], func(Object) Compliance { return ComplianceOutOfCompliance })
	if sum.Protected || sum.Compliance != ComplianceUnknown {
		t.Fatalf("unprotected object: protected = %v compliance = %q", sum.Protected, sum.Compliance)
	}
}

func TestRelicNeverCountsAsProtection(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.answer("RubrikK8sNamespaces", conn("k8sNamespaces", []any{
		map[string]any{"id": "relic", "namespaceName": "team-c", "isRelic": true, "clusterScoped": false,
			"effectiveSlaDomain": map[string]any{"id": idSLA, "name": "Gold"}, "newestSnapshot": map[string]any{"date": "2026-11-01T00:00:00Z"}},
	}, false, ""))
	objs, err := ops.NamespaceObjects(context.Background(), idCluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || !objs[0].Relic {
		t.Fatalf("relic must stay in the decoded model, got %+v", objs)
	}
	sum := SummarizeNamespace("team-c", objs, func(Object) Compliance { return ComplianceInCompliance })
	if sum.Protected || len(sum.ObjectIDs) != 0 || len(sum.ActionTargets) != 0 || sum.LastSnapshot != nil {
		t.Fatalf("relic counted: %+v", sum)
	}

	// The protection-set family decodes and drops a relic the same way.
	f.answer("RubrikK8sProtectionSets", conn("kubernetesProtectionSets", []any{
		map[string]any{"id": "relic-set", "name": "team-a-set", "rsType": "namespace", "namespace": "team-a", "isRelic": true,
			"effectiveSlaDomain": map[string]any{"id": idSLA, "name": "Gold"}, "newestSnapshot": map[string]any{"date": "2026-11-01T00:00:00Z"}},
	}, false, ""))
	sets, err := ops.ProtectionSets(context.Background(), idCluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 1 || !sets[0].Relic {
		t.Fatalf("relic set must stay in the decoded model, got %+v", sets)
	}
	sum = SummarizeNamespace("team-a", sets, func(Object) Compliance { return ComplianceInCompliance })
	if sum.Protected || len(sum.ObjectIDs) != 0 || len(sum.ActionTargets) != 0 || sum.LastSnapshot != nil {
		t.Fatalf("relic set counted: %+v", sum)
	}
}

func TestAssignSLAPinsSafetyArguments(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.answer("RubrikAssignSla", map[string]any{"assignSla": map[string]any{"success": true}})
	ctx := context.Background()

	// Removal: doNotProtect, existing snapshots retained (AE5).
	if err := ops.AssignSLA(ctx, []string{idNS}, ""); err != nil {
		t.Fatal(err)
	}
	in := f.last(t).input(t)
	want := map[string]any{"objectIds": []any{idNS}, "slaDomainAssignType": "doNotProtect", "existingSnapshotRetention": "RETAIN_SNAPSHOTS"}
	if !reflect.DeepEqual(in, want) {
		t.Fatalf("removal input = %#v, want %#v", in, want)
	}

	// Change: protectWithSlaId, still retained, and never applied to
	// existing snapshots.
	if err := ops.AssignSLA(ctx, []string{idNS, idSet}, idSLA); err != nil {
		t.Fatal(err)
	}
	in = f.last(t).input(t)
	if in["existingSnapshotRetention"] != "RETAIN_SNAPSHOTS" || in["slaDomainAssignType"] != "protectWithSlaId" || in["slaOptionalId"] != idSLA {
		t.Fatalf("assignment input = %#v", in)
	}
	for _, c := range f.recorded() {
		if _, ok := c.input(t)["shouldApplyToExistingSnapshots"]; ok || strings.Contains(c.Query, "shouldApplyToExistingSnapshots") {
			t.Fatal("assignSla must never send shouldApplyToExistingSnapshots")
		}
	}

	f.answer("RubrikAssignSla", map[string]any{"assignSla": map[string]any{"success": false}})
	if err := ops.AssignSLA(ctx, []string{idNS}, idSLA); !errors.Is(err, ErrRejected) {
		t.Fatalf("success false: err = %v, want ErrRejected", err)
	}
}

func TestOnDemandSnapshotUsesEffectiveSLA(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.answer("RubrikCreateK8sNamespaceSnapshots", map[string]any{"createK8sNamespaceSnapshots": []any{map[string]any{"jobId": 7, "taskchainId": "tc-1"}}})
	obj := objNS(idNS, "team-a", idSLA, nil)
	sla, ok := obj.SnapshotSLA()
	if !ok || sla != idSLA {
		t.Fatalf("SnapshotSLA = %q, %v", sla, ok)
	}
	h, err := ops.CreateSnapshot(context.Background(), obj.Family, obj.ID, sla)
	if err != nil {
		t.Fatal(err)
	}
	if h != (JobHandle{Family: FamilyNamespace, TaskchainID: "tc-1", JobID: 7}) {
		t.Fatalf("handle = %+v", h)
	}
	snap := f.last(t).input(t)["snapshotInput"].([]any)[0].(map[string]any)
	if snap["onDemandSnapshotSlaId"] != idSLA {
		t.Fatalf("snapshot sla = %v", snap["onDemandSnapshotSlaId"])
	}

	unprotected := objNS(idNS, "team-a", "DO_NOT_PROTECT", nil)
	if _, ok := unprotected.SnapshotSLA(); ok {
		t.Fatal("an unprotected object has no effective SLA to snapshot under")
	}
	before := len(f.recorded())
	for _, id := range []string{"", "UNPROTECTED", "do_not_protect"} {
		if _, err := ops.CreateSnapshot(context.Background(), FamilyNamespace, idNS, id); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("sla %q: err = %v, want ErrInvalidRequest", id, err)
		}
	}
	if len(f.recorded()) != before {
		t.Fatal("a snapshot without an SLA must not reach RSC")
	}
}

func TestEvaluateCompliance(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	freq := 4 * time.Hour
	at := func(mult float64) *time.Time {
		t := now.Add(-time.Duration(mult * float64(freq)))
		return &t
	}
	cases := []struct {
		name   string
		newest *time.Time
		freq   time.Duration
		want   Compliance
	}{
		{"1.4x is in compliance", at(1.4), freq, ComplianceInCompliance},
		{"1.6x is late", at(1.6), freq, ComplianceOutOfCompliance},
		{"exactly 1.5x is late", at(1.5), freq, ComplianceOutOfCompliance},
		{"future snapshot is in compliance", at(-1), freq, ComplianceInCompliance},
		{"no snapshot is late", nil, freq, ComplianceOutOfCompliance},
		{"missing frequency is unknown", at(0.1), 0, ComplianceUnknown},
		{"missing frequency without snapshot is unknown", nil, 0, ComplianceUnknown},
		{"huge frequency does not overflow", at(0), time.Duration(math.MaxInt64), ComplianceInCompliance},
	}
	for _, tc := range cases {
		if got := EvaluateCompliance(tc.newest, tc.freq, now); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if WorstCompliance(ComplianceInCompliance, ComplianceUnknown) != ComplianceUnknown ||
		WorstCompliance(ComplianceOutOfCompliance, ComplianceUnknown) != ComplianceOutOfCompliance ||
		WorstCompliance(ComplianceInCompliance, ComplianceInCompliance) != ComplianceInCompliance {
		t.Fatal("WorstCompliance order is out of compliance > unknown > in compliance")
	}
}

func TestSLADomainsDecodeShortestFrequency(t *testing.T) {
	f, ops := newFakeRSC(t)
	basic := func(n int64) map[string]any { return map[string]any{"basicSchedule": map[string]any{"frequency": n}} }
	f.answer("RubrikSlaDomains", conn("slaDomains", []any{
		map[string]any{"id": "gold", "name": "Gold", "snapshotSchedule": map[string]any{"hourly": basic(4), "daily": basic(1), "weekly": basic(1)}},
		map[string]any{"id": "monthly", "name": "Monthly", "snapshotSchedule": map[string]any{"monthly": basic(1), "yearly": basic(1)}},
		map[string]any{"id": "none", "name": "Archive only"},
		map[string]any{"id": "zero", "name": "Zero", "snapshotSchedule": map[string]any{"daily": basic(0)}},
		map[string]any{"id": "huge", "name": "Huge", "snapshotSchedule": map[string]any{"yearly": basic(math.MaxInt64)}},
	}, false, ""))
	domains, err := ops.SLADomains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []SLADomain{
		{ID: "gold", Name: "Gold", Frequency: 4 * time.Hour},
		{ID: "monthly", Name: "Monthly", Frequency: 31 * 24 * time.Hour},
		{ID: "none", Name: "Archive only"},
		{ID: "zero", Name: "Zero"},
		{ID: "huge", Name: "Huge", Frequency: time.Duration(math.MaxInt64)},
	}
	if !reflect.DeepEqual(domains, want) {
		t.Fatalf("domains = %+v\nwant %+v", domains, want)
	}
	q := f.last(t).Query
	if !strings.Contains(q, "objectTypeList: [K8S_OBJECT_TYPE, KUPR_OBJECT_TYPE]") || !strings.Contains(q, "... on GlobalSlaReply") {
		t.Fatalf("sla domain query does not filter to both Kubernetes families or read schedules: %s", q)
	}
}

func TestJobsDecodeStatusesAndReasons(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.answer("RubrikActivitySeries", conn("activitySeriesConnection", []any{
		map[string]any{"activitySeriesId": "j1", "objectId": idNS, "lastActivityType": "Backup", "lastActivityStatus": "Failure",
			"failureReason": "PVC snapshot timed out", "causeErrorMessage": "ignored", "startTime": "2026-11-13T01:00:00Z"},
		map[string]any{"activitySeriesId": "j2", "objectId": idNS, "lastActivityType": "Backup", "lastActivityStatus": "TaskFailure", "causeErrorMessage": "quota exceeded"},
		map[string]any{"activitySeriesId": "j3", "objectId": idNS, "lastActivityType": "Recovery", "lastActivityStatus": "PARTIAL_SUCCESS"},
		map[string]any{"activitySeriesId": "j4", "objectId": idNS, "lastActivityType": "Backup", "lastActivityStatus": "Success", "failureReason": "stale"},
		map[string]any{"activitySeriesId": "j5", "objectId": idNS, "lastActivityType": "Backup", "lastActivityStatus": "Queued"},
		map[string]any{"activitySeriesId": "j6", "objectId": idNS, "lastActivityType": "Backup", "lastActivityStatus": "Canceled"},
		map[string]any{"activitySeriesId": "j7", "objectId": idNS, "lastActivityType": "Backup", "lastActivityStatus": "Warning"},
	}, false, ""))
	jobs, err := ops.Jobs(context.Background(), ActivityFilter{ObjectIDs: []string{idNS}})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		status JobStatus
		reason string
	}
	want := []row{
		{JobFailed, "PVC snapshot timed out"}, {JobFailed, "quota exceeded"}, {JobPartial, ""},
		{JobSucceeded, ""}, {JobRunning, ""}, {JobCanceled, ""}, {JobUnknown, ""},
	}
	for i, j := range jobs {
		if j.Status != want[i].status || j.FailureReason != want[i].reason {
			t.Errorf("job %s = %q/%q, want %q/%q", j.ID, j.Status, j.FailureReason, want[i].status, want[i].reason)
		}
	}
	if jobs[0].StartTime == nil || !jobs[0].StartTime.Equal(*ts("2026-11-13T01:00:00Z")) {
		t.Fatalf("start time = %v", jobs[0].StartTime)
	}
	if !JobFailed.Terminal() || JobRunning.Terminal() || JobUnknown.Terminal() {
		t.Fatal("Terminal is wrong")
	}

	// Every schema EventStatus is a valid filter, task-level and partial
	// outcomes included.
	statuses := []string{"FAILURE", "TASK_FAILURE", "PARTIAL_SUCCESS", "TASK_SUCCESS"}
	if _, err := ops.Jobs(context.Background(), ActivityFilter{ObjectIDs: []string{idNS}, Statuses: statuses}); err != nil {
		t.Fatalf("schema statuses: %v", err)
	}
	if got := f.last(t).Vars["filters"].(map[string]any)["lastActivityStatus"]; !reflect.DeepEqual(got, []any{"FAILURE", "TASK_FAILURE", "PARTIAL_SUCCESS", "TASK_SUCCESS"}) {
		t.Fatalf("status filter = %#v", got)
	}

	before := len(f.recorded())
	if _, err := ops.Jobs(context.Background(), ActivityFilter{ObjectIDs: []string{idNS}, Statuses: []string{"Failure"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown status: err = %v", err)
	}
	if _, err := ops.Jobs(context.Background(), ActivityFilter{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("no object: err = %v", err)
	}
	if len(f.recorded()) != before {
		t.Fatal("an invalid filter must not reach RSC")
	}
}

func TestTaskchainStates(t *testing.T) {
	f, ops := newFakeRSC(t)
	cases := map[string]TaskchainState{
		"SUCCEEDED": {Status: JobSucceeded},
		"FAILED":    {Status: JobFailed, Error: "restore failed"},
		"CANCELED":  {Status: JobCanceled},
		"READY":     {Status: JobRunning},
		"RUNNING":   {Status: JobRunning},
		"UNDOING":   {Status: JobRunning},
		"UNKNOWN":   {Status: JobUnknown},
		"NEW_STATE": {Status: JobUnknown},
	}
	for state, want := range cases {
		f.answer("RubrikTaskchain", map[string]any{"taskchain": map[string]any{"state": state, "error": "restore failed"}})
		got, err := ops.Taskchain(context.Background(), "tc-1")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("state %s = %+v, want %+v", state, got, want)
		}
	}
	if _, err := ops.Taskchain(context.Background(), " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blank taskchain id: err = %v", err)
	}
}

func TestSnapshotsDecodeAndLimit(t *testing.T) {
	f, ops := newFakeRSC(t)
	nodes := []any{}
	for i := range 3 {
		nodes = append(nodes, map[string]any{"id": fmt.Sprintf("s%d", i), "date": fmt.Sprintf("2026-11-1%dT00:00:00Z", 3-i),
			"expirationDate": "2026-12-31T00:00:00Z", "isOnDemandSnapshot": i == 0, "isCorrupted": i == 2})
	}
	f.answer("RubrikK8sNamespaceSnapshots", map[string]any{"k8sNamespace": map[string]any{"snapshotConnection": map[string]any{
		"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": true, "endCursor": "next"}}}})
	snaps, err := ops.Snapshots(context.Background(), FamilyNamespace, idNS, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 || snaps[0].ID != "s0" || !snaps[0].OnDemand || snaps[0].Expires == nil {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("requests = %d; a limit met on the first page needs no second", n)
	}

	f.answer("RubrikK8sProtectionSetSnapshots", map[string]any{"kubernetesProtectionSet": map[string]any{"snapshotConnection": nil}})
	snaps, err = ops.Snapshots(context.Background(), FamilyProtectionSet, idSet, 0)
	if err != nil || len(snaps) != 0 {
		t.Fatalf("null connection: %v, %+v", err, snaps)
	}
	if snapshotLimit(0) != DefaultSnapshotLimit || snapshotLimit(1<<20) != MaxSnapshotLimit {
		t.Fatal("snapshot limit defaults and cap")
	}
}

// TestMutationRepliesWithoutOneJobAreIndeterminate pins that a reply which
// cannot name exactly one job is ErrUnreadable, never ErrRejected: RSC may
// have started a job, so a caller must not resend it.
func TestMutationRepliesWithoutOneJobAreIndeterminate(t *testing.T) {
	f, ops := newFakeRSC(t)
	ctx := context.Background()
	indeterminate := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrUnreadable) || errors.Is(err, ErrRejected) {
			t.Fatalf("%s: err = %v, want ErrUnreadable and not ErrRejected", what, err)
		}
	}
	f.answer("RubrikCreateK8sNamespaceSnapshots", map[string]any{"createK8sNamespaceSnapshots": []any{}})
	_, err := ops.CreateSnapshot(ctx, FamilyNamespace, idNS, idSLA)
	indeterminate("no job", err)
	f.answer("RubrikCreateK8sNamespaceSnapshots", map[string]any{"createK8sNamespaceSnapshots": []any{
		map[string]any{"jobId": 1, "taskchainId": "a"}, map[string]any{"jobId": 2, "taskchainId": "b"}}})
	_, err = ops.CreateSnapshot(ctx, FamilyNamespace, idNS, idSLA)
	indeterminate("two jobs", err)
	f.answer("RubrikCreateK8sNamespaceSnapshots", map[string]any{"createK8sNamespaceSnapshots": []any{map[string]any{"jobId": 3, "taskchainId": ""}}})
	_, err = ops.CreateSnapshot(ctx, FamilyNamespace, idNS, idSLA)
	indeterminate("job without a taskchain", err)
	f.answer("RubrikRestoreK8sNamespace", map[string]any{"restoreK8sNamespace": map[string]any{"jobId": 0, "taskchainId": ""}})
	_, err = ops.RestoreNamespace(ctx, idSnapshot, idCluster, "team-a")
	indeterminate("no taskchain", err)
	f.answer("RubrikRestoreK8sProtectionSetSnapshot", map[string]any{"restoreK8sProtectionSetSnapshot": map[string]any{"status": "QUEUED"}})
	_, err = ops.RestoreProtectionSetSnapshot(ctx, idSnapshot)
	indeterminate("no request id", err)
	f.answer("RubrikCreateK8sProtectionSetSnapshot", map[string]any{"createK8sProtectionSetSnapshot": map[string]any{"id": "req-9", "status": "QUEUED"}})
	h, err := ops.CreateSnapshot(ctx, FamilyProtectionSet, idSet, idSLA)
	if err != nil || h != (JobHandle{Family: FamilyProtectionSet, RequestID: "req-9", RequestStatus: "QUEUED"}) {
		t.Fatalf("set snapshot handle = %+v, %v", h, err)
	}
}

func TestInvalidArgumentsSendNothing(t *testing.T) {
	f, ops := newFakeRSC(t)
	ctx := context.Background()
	calls := []func() error{
		func() error { _, err := ops.NamespaceObjects(ctx, "not-a-uuid"); return err },
		func() error { _, err := ops.ProtectionSets(ctx, ""); return err },
		func() error { _, err := ops.Snapshots(ctx, FamilyNamespace, "x", 0); return err },
		func() error { _, err := ops.Snapshots(ctx, Family("other"), idNS, 0); return err },
		func() error { _, err := ops.CreateSnapshot(ctx, FamilyNamespace, "x", idSLA); return err },
		func() error { _, err := ops.CreateSnapshot(ctx, Family("other"), idNS, idSLA); return err },
		func() error { return ops.AssignSLA(ctx, nil, idSLA) },
		func() error { return ops.AssignSLA(ctx, []string{"x"}, idSLA) },
		func() error { return ops.AssignSLA(ctx, []string{idNS}, "gold") },
		func() error { _, err := ops.RestoreNamespace(ctx, "x", idCluster, "team-a"); return err },
		func() error { _, err := ops.RestoreNamespace(ctx, idSnapshot, "x", "team-a"); return err },
		func() error { _, err := ops.RestoreNamespace(ctx, idSnapshot, idCluster, "Team_A"); return err },
		func() error { _, err := ops.RestoreProtectionSetSnapshot(ctx, "x"); return err },
		func() error { _, err := ops.Taskchain(ctx, strings.Repeat("t", 257)); return err },
	}
	for i, call := range calls {
		if err := call(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("call %d: err = %v, want ErrInvalidRequest", i, err)
		}
	}
	if n := len(f.recorded()); n != 0 {
		t.Fatalf("%d invalid requests reached RSC", n)
	}
}

func TestMalformedAnswersAreUnreadable(t *testing.T) {
	f, ops := newFakeRSC(t)
	ctx := context.Background()
	f.answer("RubrikK8sNamespaces", map[string]any{"k8sNamespaces": nil})
	f.answer("RubrikK8sProtectionSets", map[string]any{"kubernetesProtectionSets": map[string]any{"nodes": []any{map[string]any{"name": "no id"}}}})
	f.answer("RubrikK8sNamespaceSnapshots", map[string]any{"k8sNamespace": map[string]any{"snapshotConnection": map[string]any{"nodes": []any{map[string]any{"id": "s"}}}}})
	f.answer("RubrikActivitySeries", map[string]any{"activitySeriesConnection": "oops"})
	f.answer("RubrikTaskchain", map[string]any{"taskchain": nil})
	f.answer("RubrikSlaDomains", map[string]any{"slaDomains": map[string]any{"nodes": []any{map[string]any{"id": "x", "snapshotSchedule": map[string]any{"daily": map[string]any{"basicSchedule": map[string]any{"frequency": "daily"}}}}}}})
	f.answer("RubrikAssignSla", map[string]any{"assignSla": map[string]any{}})
	f.answer("RubrikK8sClusters", map[string]any{"k8sClusters": map[string]any{"nodes": []any{map[string]any{"name": "no id"}}}})
	checks := map[string]func() error{
		"namespaces":  func() error { _, err := ops.NamespaceObjects(ctx, idCluster); return err },
		"sets":        func() error { _, err := ops.ProtectionSets(ctx, idCluster); return err },
		"snapshots":   func() error { _, err := ops.Snapshots(ctx, FamilyNamespace, idNS, 0); return err },
		"jobs":        func() error { _, err := ops.Jobs(ctx, ActivityFilter{ObjectIDs: []string{idNS}}); return err },
		"taskchain":   func() error { _, err := ops.Taskchain(ctx, "tc"); return err },
		"sla domains": func() error { _, err := ops.SLADomains(ctx); return err },
		"assign sla":  func() error { return ops.AssignSLA(ctx, []string{idNS}, idSLA) },
		"clusters":    func() error { _, err := ops.Clusters(ctx); return err },
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: err = %v, want ErrUnreadable", name, err)
		}
	}
}

func TestGraphQLErrorsPassThrough(t *testing.T) {
	f, ops := newFakeRSC(t)
	f.handle("RubrikAssignSla", func(map[string]any) (any, error) { return nil, errors.New("object is not Kubernetes") })
	err := ops.AssignSLA(context.Background(), []string{idNS}, idSLA)
	var gqlErr *GraphQLError
	if !errors.As(err, &gqlErr) || !strings.Contains(err.Error(), "object is not Kubernetes") {
		t.Fatalf("err = %v, want the GraphQL error", err)
	}
}

func TestSLARefProtects(t *testing.T) {
	for id, want := range map[string]bool{
		idSLA: true, "gold": true, "": false, "UNPROTECTED": false, " unprotected ": false,
		"DO_NOT_PROTECT": false, "DoNotProtect": false, "NO_SLA": false,
	} {
		if got := (SLARef{ID: id}).Protects(); got != want {
			t.Errorf("Protects(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestAsyncReplyIgnoresOtherFields(t *testing.T) {
	data := []byte(`{"createK8sNamespaceSnapshots":[{"jobId":1}],"restoreK8sProtectionSetSnapshot":{"id":"r2","status":"QUEUED"}}`)
	h, err := decodeAsyncReply(data, "restoreK8sProtectionSetSnapshot")
	if err != nil || h.RequestID != "r2" {
		t.Fatalf("handle = %+v, %v", h, err)
	}
	if _, err := decodeAsyncReply(data, "createK8sProtectionSetSnapshot"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("missing field: err = %v, want ErrUnreadable", err)
	}
}
