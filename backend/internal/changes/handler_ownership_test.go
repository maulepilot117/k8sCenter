package changes

// List, ownership-resolution and cross-cutting handler tests.

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/gitops"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestHandleList_OwnerScopedAndPaginated(t *testing.T) {
	hs := newHarness(t)
	var mine []*store.ChangeReceipt
	for i := 0; i < 3; i++ {
		r := hs.seed(testUser, "local", recordedDeployment(0, fmt.Sprintf("web-%d", i), "uid"))
		created := fixedNow.Add(time.Duration(i) * time.Minute)
		r.CreatedAt = created
		hs.reader.put(*r)
		mine = append(mine, r)
	}
	hs.seed(otherUser, "local", recordedDeployment(0, "bob-web", "uid-bob"))

	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?page=2&pageSize=2"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	e := decodeEnvelope(t, w)
	if e.Metadata == nil || e.Metadata.Total != 3 || e.Metadata.Page != 2 || e.Metadata.PageSize != 2 {
		t.Fatalf("metadata = %+v", e.Metadata)
	}
	var items []map[string]any
	if err := json.Unmarshal(e.Data, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0]["operationId"] != mine[0].ID.String() {
		t.Fatalf("page 2 = %+v, want the oldest receipt %s", items, mine[0].ID)
	}
	for _, k := range []string{"objects", "checks", "ownership", "summary"} {
		if _, ok := items[0][k]; ok {
			t.Fatalf("list items are envelope views only; found %q", k)
		}
	}
	if strings.Contains(w.Body.String(), "bob") {
		t.Fatal("another owner's row leaked into the list")
	}

	// Defaults and clamps come from the store's Normalize.
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?page=0&pageSize=99999"})
	e = decodeEnvelope(t, w)
	if e.Metadata.Page != 1 || e.Metadata.PageSize != store.ReceiptMaxPageSize {
		t.Fatalf("normalized metadata = %+v", e.Metadata)
	}
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?page=x"})
	expectError(t, w, http.StatusBadRequest, "invalid page")
	// A page that would overflow OFFSET is a client error, not a store outage.
	before := hs.reader.listCalls
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?page=9223372036854775807"})
	expectError(t, w, http.StatusBadRequest, "invalid page")
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: fmt.Sprintf("?page=%d", maxListPage+1)})
	expectError(t, w, http.StatusBadRequest, "invalid page")
	if hs.reader.listCalls != before {
		t.Fatal("an out-of-range page must be rejected before the store is asked")
	}
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: fmt.Sprintf("?page=%d", maxListPage)})
	if w.Code != http.StatusOK {
		t.Fatalf("the bound itself must pass: %d %s", w.Code, w.Body.String())
	}
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?pageSize=1.5"})
	expectError(t, w, http.StatusBadRequest, "invalid pageSize")
}

func TestHandleList_NeverReturnsAnotherOwnersRow(t *testing.T) {
	hs := newHarness(t)
	shared := hs.seed(otherUser, "local", recordedDeployment(0, "bob-web", "uid-bob"))
	hs.reader.grants[shared.ID] = []string{testUser.ID}

	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser})
	e := decodeEnvelope(t, w)
	if e.Metadata.Total != 0 || string(e.Data) != "[]" {
		t.Fatalf("a granted receipt is reachable by id only, not listed: %s", w.Body.String())
	}
	// And an admin lists only their own, too.
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: adminUser})
	if e := decodeEnvelope(t, w); e.Metadata.Total != 0 {
		t.Fatalf("admin list = %s", w.Body.String())
	}
}

func TestHandleList_EmptyIsAnArrayWithZeroTotal(t *testing.T) {
	hs := newHarness(t)
	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser})
	e := decodeEnvelope(t, w)
	if string(e.Data) != "[]" || e.Metadata == nil || e.Metadata.Total != 0 || e.Metadata.Page != 1 || e.Metadata.PageSize != store.ReceiptDefaultPageSize {
		t.Fatalf("empty list = %s", w.Body.String())
	}
}

func TestHandleList_RemoteGenerationResolvedOncePerCluster(t *testing.T) {
	hs := newHarness(t)
	hs.seed(adminUser, "remote-1", recordedDeployment(0, "a", "u"))
	hs.seed(adminUser, "remote-1", recordedDeployment(0, "b", "u"))
	hs.seed(adminUser, "local", recordedDeployment(0, "c", "u"))

	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: adminUser})
	var items []ReceiptView
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &items); err != nil {
		t.Fatal(err)
	}
	if calls := hs.targeter.calls(); len(calls) != 1 || calls[0] != "remote-1" {
		t.Fatalf("generation lookups = %v, want one for remote-1", calls)
	}
	for _, it := range items {
		if (it.ClusterID == "remote-1") != it.TargetGenerationChanged {
			t.Fatalf("item %+v", it)
		}
	}
}

func TestHandleResolveOwnership_CapsRefCount(t *testing.T) {
	hs := newHarness(t)
	w := hs.ownership(t, testUser, "local", ownershipBody(maxOwnershipRefs+1))
	expectError(t, w, http.StatusBadRequest, "at most 50 objects")
	w = hs.ownership(t, testUser, "local", `{"objects":[]}`)
	expectError(t, w, http.StatusBadRequest, "objects is required")
	w = hs.ownership(t, testUser, "local", `{"objects":[{"kind":"Deployment"}]}`)
	expectError(t, w, http.StatusBadRequest, "objects[0]: kind and name are required")
	w = hs.ownership(t, testUser, "local", `{"objects":`)
	expectError(t, w, http.StatusBadRequest, "invalid JSON body")
	if hs.resolver.calls != 0 || len(hs.targeter.calls()) != 0 {
		t.Fatal("rejected input must not reach the cluster or the resolver")
	}

	w = hs.ownership(t, testUser, "local", ownershipBody(maxOwnershipRefs))
	if w.Code != http.StatusOK || hs.resolver.calls != 1 || len(hs.resolver.refs) != maxOwnershipRefs {
		t.Fatalf("exactly the cap must pass: %d %s (resolver calls %d)", w.Code, w.Body.String(), hs.resolver.calls)
	}
}

func TestHandleResolveOwnership_BodyTooLarge(t *testing.T) {
	hs := newHarness(t)
	padding := strings.Repeat("x", maxOwnershipBodyBytes)
	w := hs.ownership(t, testUser, "local", `{"objects":[{"kind":"Deployment","name":"`+padding+`"}]}`)
	expectError(t, w, http.StatusRequestEntityTooLarge, "request body too large")
}

func TestHandleResolveOwnership_NonAdminRemoteIs403(t *testing.T) {
	hs := newHarness(t)
	w := hs.ownership(t, testUser, "remote-1", ownershipBody(1))
	expectError(t, w, http.StatusForbidden, "admin role required for remote cluster access")
	if hs.resolver.calls != 0 || len(hs.targeter.calls()) != 0 {
		t.Fatal("a non-admin must not reach a remote cluster through ownership resolution")
	}
	if w := hs.ownership(t, adminUser, "remote-1", ownershipBody(1)); w.Code != http.StatusOK {
		t.Fatalf("admin remote = %d %s", w.Code, w.Body.String())
	}
	if w := hs.ownership(t, testUser, "local", ownershipBody(1)); w.Code != http.StatusOK {
		t.Fatalf("non-admin local = %d %s", w.Code, w.Body.String())
	}
}

func TestHandleResolveOwnership_BodyClusterMustMatchHeader(t *testing.T) {
	hs := newHarness(t)
	w := hs.ownership(t, adminUser, "remote-1", `{"clusterId":"remote-2","objects":[{"kind":"Deployment","name":"web"}]}`)
	expectError(t, w, http.StatusBadRequest, "clusterId does not match X-Cluster-ID")
	if hs.resolver.calls != 0 {
		t.Fatal("a mismatched body cluster must not resolve anything")
	}
	// Matching, or omitted, is fine; "" and "local" are the same cluster.
	for _, body := range []string{
		`{"clusterId":"remote-1","objects":[{"kind":"Deployment","name":"web"}]}`,
		`{"objects":[{"kind":"Deployment","name":"web"}]}`,
	} {
		if w := hs.ownership(t, adminUser, "remote-1", body); w.Code != http.StatusOK {
			t.Fatalf("body %s = %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := hs.ownership(t, testUser, "local", `{"clusterId":"","objects":[{"kind":"Deployment","name":"web"}]}`); w.Code != http.StatusOK {
		t.Fatalf("empty clusterId on local = %d", w.Code)
	}
	if hs.resolver.cluster != "local" {
		t.Fatalf("resolver cluster = %q", hs.resolver.cluster)
	}
}

func TestHandleResolveOwnership_ResolvesRefsThroughTargetMapper(t *testing.T) {
	hs := newHarness(t)
	hs.resolver.results = []gitops.OwnershipResult{
		{Object: gitops.ObjectRef{Kind: "Deployment", Name: "web"}, Controller: gitops.OwnedByNone, Confidence: gitops.ConfidenceUnknown, Reason: "no-evidence"},
		{Object: gitops.ObjectRef{Kind: "Widget", Name: "w"}, Controller: gitops.OwnedByNone, Confidence: gitops.ConfidenceUnknown, Reason: "no-evidence"},
	}
	body := `{"objects":[
		{"group":"apps","kind":"Deployment","namespace":"prod","name":"web"},
		{"group":"example.io","version":"v1","kind":"Widget","namespace":"prod","name":"w"}]}`

	w := hs.ownership(t, adminUser, "remote-1", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if calls := hs.targeter.calls(); len(calls) != 1 || calls[0] != "remote-1" {
		t.Fatalf("target = %v", calls)
	}
	if hs.resolver.user != adminUser || hs.resolver.cluster != "remote-1" || hs.resolver.dyn != hs.targeter.dyn {
		t.Fatalf("resolver got user=%v cluster=%q dyn-matches=%v", hs.resolver.user, hs.resolver.cluster, hs.resolver.dyn == hs.targeter.dyn)
	}
	refs := hs.resolver.refs
	if len(refs) != 2 {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[0].Resource != "deployments" || refs[0].Version != "v1" || refs[0].ClusterID != "remote-1" || refs[0].Namespace != "prod" {
		t.Fatalf("mapped ref = %+v", refs[0])
	}
	if refs[1].Resource != "" || refs[1].Version != "v1" || refs[1].Kind != "Widget" {
		t.Fatalf("unmappable ref must pass through: %+v", refs[1])
	}
	var resp OwnershipResponse
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ClusterID != "remote-1" || len(resp.Results) != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestHandleResolveOwnership_ForbiddenAppLeaksNoRepoURL(t *testing.T) {
	hs := newHarness(t)
	// What gitops answers when the caller may not list Applications: a
	// forbidden verdict with no apps. The handler must pass it through as-is
	// and add nothing.
	hs.resolver.results = []gitops.OwnershipResult{{
		Object:     gitops.ObjectRef{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "web"},
		Controller: gitops.OwnedByNone, Confidence: gitops.ConfidenceForbidden, Reason: "argo-list-forbidden",
		IdentityBasis: "group-kind-namespace-name",
	}}
	w := hs.ownership(t, testUser, "local", ownershipBody(1))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if hs.resolver.user != testUser {
		t.Fatal("ownership must be resolved under the caller's own identity")
	}
	body := w.Body.String()
	for _, leak := range []string{"repoURL", "apps\":", "git.example"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response carries %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, `"confidence":"forbidden"`) {
		t.Fatalf("verdict missing: %s", body)
	}
}

func TestHandleResolveOwnership_Timeout(t *testing.T) {
	hs := newHarness(t)
	hs.h.clusterTimeout = 20 * time.Millisecond
	hs.resolver.block = true
	start := time.Now()
	w := hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusGatewayTimeout, "ownership resolution timed out")
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout did not bound the call")
	}
}

func TestHandleResolveOwnership_ResolverFailure(t *testing.T) {
	hs := newHarness(t)
	hs.resolver.err = errors.New("gitops: boom")
	w := hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusInternalServerError, "ownership resolution failed")
	if strings.Contains(w.Body.String(), "boom") {
		t.Fatal("resolver error text leaked")
	}

	hs.targeter.err = errors.New("no client")
	w = hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusInternalServerError, "failed to create kubernetes client")

	hs.h.gitops = nil
	w = hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusServiceUnavailable, "ownership resolution is not configured")
}

// ---------------------------------------------------------------------------
// Cross-cutting
// ---------------------------------------------------------------------------

func TestHandlers_RequireAuth(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	for name, fn := range map[string]http.HandlerFunc{
		"list": hs.h.HandleList, "get": hs.h.HandleGet, "verification": hs.h.HandleVerification, "ownership": hs.h.HandleResolveOwnership,
	} {
		w := hs.do(t, fn, http.MethodGet, request{id: rec.ID.String(), body: ownershipBody(1)})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a user = %d %s", name, w.Code, w.Body.String())
		}
		expectError(t, w, http.StatusUnauthorized, "authentication required")
	}
	if len(hs.reader.calls) != 0 || hs.resolver.calls != 0 {
		t.Fatal("unauthenticated requests must not reach the store or the resolver")
	}
}

// TestHandlers_LegacyApplyEnvelopeUnaffected: this package serves nothing
// under /yaml and declares no type that could shadow the legacy apply
// envelope; and handler.go reaches the cluster only through the targeter.
func TestHandlers_LegacyApplyEnvelopeUnaffected(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "handler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			name := spec.(*ast.TypeSpec).Name.Name
			switch name {
			case "ApplyResult", "ApplyResponse", "ApplySummary", "ApplyRequest":
				t.Fatalf("handler.go declares %s, which shadows the legacy apply envelope", name)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING && strings.Contains(x.Value, "/yaml") {
				t.Fatalf("handler.go references a /yaml route: %s", x.Value)
			}
		case *ast.SelectorExpr:
			switch x.Sel.Name {
			case "ClientForUser", "DynamicClientForUser", "LocalFactory", "RESTMapper", "DiscoveryClient", "BaseDynamicClient":
				t.Fatalf("handler.go calls .%s at %s; cluster access must go through ClusterTargeter.TargetFor", x.Sel.Name, fset.Position(x.Pos()))
			}
		}
		return true
	})
}
