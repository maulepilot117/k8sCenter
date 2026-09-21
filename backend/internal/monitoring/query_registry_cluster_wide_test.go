package monitoring

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// ─────────────────────────────────────────────────────────────────────────────
// Cluster-wide slugs (KTD6 / R15).
//
// Every other Registry entry is scoped to one resource instance and takes a
// namespace and a name. These take neither: they answer a question about the
// whole cluster. That shape is only safe if the per-slug RBAC check runs at
// cluster scope no matter what the caller puts in the query string — a
// cluster-wide template with a namespace-scoped authorization would let a
// single-namespace viewer read every namespace's data. These tests pin that.
// ─────────────────────────────────────────────────────────────────────────────

// clusterWideSlugs is the set this unit adds. Listing it explicitly (rather
// than filtering the Registry by the ClusterWide flag) makes an accidental
// flag flip on an existing per-resource slug a test failure instead of a
// silently widened authorization.
var clusterWideSlugs = []string{
	"cluster/top-consumers-cpu",
	"cluster/top-consumers-memory",
	"cluster/storage-capacity",
}

// TestRegistry_ClusterWideSlugsExist pins the slug set this unit adds. If a
// slug is renamed, the dashboard widget that calls it breaks silently at
// runtime; this fails at build time instead.
func TestRegistry_ClusterWideSlugsExist(t *testing.T) {
	for _, slug := range clusterWideSlugs {
		t.Run(slug, func(t *testing.T) {
			def, ok := Registry[slug]
			if !ok {
				t.Fatalf("slug %q not found in Registry", slug)
			}
			if !def.ClusterWide {
				t.Errorf("slug %q is in the cluster-wide set but ClusterWide is false; "+
					"without the flag the handler authorizes it against the caller-supplied namespace", slug)
			}
		})
	}
}

// TestRegistry_ClusterWideFlagIsExclusive is the inverse guard: no
// per-resource slug may carry ClusterWide, because that would drop its
// namespace from the RBAC check and turn a scoped read into an open one.
func TestRegistry_ClusterWideFlagIsExclusive(t *testing.T) {
	expected := make(map[string]bool, len(clusterWideSlugs))
	for _, s := range clusterWideSlugs {
		expected[s] = true
	}
	for key, def := range Registry {
		if def.ClusterWide && !expected[key] {
			t.Errorf("slug %q carries ClusterWide but is not in the declared cluster-wide set; "+
				"add it to clusterWideSlugs deliberately or drop the flag", key)
		}
	}
}

// TestRegistry_ClusterWideSlugsRenderWithoutParameters verifies each new slug
// produces valid, fully-substituted PromQL when no namespace and no name are
// supplied — the calling shape the dashboard widgets actually use.
func TestRegistry_ClusterWideSlugsRenderWithoutParameters(t *testing.T) {
	for _, slug := range clusterWideSlugs {
		t.Run(slug, func(t *testing.T) {
			def, ok := Registry[slug]
			if !ok {
				t.Fatalf("slug %q not found in Registry", slug)
			}

			// A cluster-wide template must not reference the per-resource
			// variables at all — a leftover {{.Namespace}} would render to
			// namespace="" and silently match nothing.
			for _, v := range []string{"{{.Namespace}}", "{{.Name}}", "{{.NamespaceRegex}}", "{{.NameRegex}}"} {
				if strings.Contains(def.Template, v) {
					t.Errorf("cluster-wide template references %s: %q", v, def.Template)
				}
			}

			rendered, err := renderSlugTemplate(def.Template, "", "")
			if err != nil {
				t.Fatalf("renderSlugTemplate with no parameters failed: %v", err)
			}
			if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
				t.Errorf("rendered output still contains template delimiters: %q", rendered)
			}
			if !strings.Contains(rendered, "topk(") {
				t.Errorf("cluster-wide query should rank its results with topk; got %q", rendered)
			}
			// Supplying parameters must not change the query — the widgets
			// have no parameters to send and a caller cannot smuggle one in.
			withParams, err := renderSlugTemplate(def.Template, "kube-system", "coredns")
			if err != nil {
				t.Fatalf("renderSlugTemplate with parameters failed: %v", err)
			}
			if withParams != rendered {
				t.Errorf("supplied parameters changed the rendered query:\n no params: %q\nwith params: %q",
					rendered, withParams)
			}
		})
	}
}

// TestRegistry_ClusterWideSlugsDeclareAuthorization verifies each new slug
// declares the verbs and group-resource its data actually comes from, per G7.
func TestRegistry_ClusterWideSlugsDeclareAuthorization(t *testing.T) {
	want := map[string]struct {
		verbs []string
		gvr   string
	}{
		"cluster/top-consumers-cpu":    {verbs: []string{"list"}, gvr: "pods"},
		"cluster/top-consumers-memory": {verbs: []string{"list"}, gvr: "pods"},
		"cluster/storage-capacity":     {verbs: []string{"list"}, gvr: "persistentvolumeclaims"},
	}

	for slug, exp := range want {
		t.Run(slug, func(t *testing.T) {
			def, ok := Registry[slug]
			if !ok {
				t.Fatalf("slug %q not found in Registry", slug)
			}
			if def.RequiredGVR != exp.gvr {
				t.Errorf("RequiredGVR = %q, want %q", def.RequiredGVR, exp.gvr)
			}
			if len(def.RequiredVerbs) != len(exp.verbs) {
				t.Fatalf("RequiredVerbs = %v, want %v", def.RequiredVerbs, exp.verbs)
			}
			for i, v := range exp.verbs {
				if def.RequiredVerbs[i] != v {
					t.Errorf("RequiredVerbs[%d] = %q, want %q", i, def.RequiredVerbs[i], v)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Handler-level authorization
// ─────────────────────────────────────────────────────────────────────────────

// recordingPredicate captures every CanAccessGroupResource argument tuple so a
// test can assert the *scope* of the check, not just its outcome.
type recordingPredicate struct {
	mu    sync.Mutex
	calls []predicateCall
	allow func(predicateCall) bool
}

type predicateCall struct {
	verb      string
	apiGroup  string
	resource  string
	namespace string
}

func (rp *recordingPredicate) fn(verb, apiGroup, resource, namespace string) bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	c := predicateCall{verb: verb, apiGroup: apiGroup, resource: resource, namespace: namespace}
	rp.calls = append(rp.calls, c)
	return rp.allow(c)
}

func (rp *recordingPredicate) recorded() []predicateCall {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	out := make([]predicateCall, len(rp.calls))
	copy(out, rp.calls)
	return out
}

// newSlugTestHandler wires a Handler against a stub Prometheus and the given
// access predicate, and returns a chi router so the `*` wildcard the handler
// reads is actually populated.
func newSlugTestHandler(t *testing.T, pred *recordingPredicate) http.Handler {
	t.Helper()

	mockProm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(mockProm.Close)

	pc, err := NewPrometheusClientWithTransport(mockProm.URL, http.DefaultTransport)
	if err != nil {
		t.Fatalf("NewPrometheusClientWithTransport: %v", err)
	}

	h := &Handler{
		Discoverer: &Discoverer{
			status:     &MonitoringStatus{Prometheus: ComponentStatus{Available: true}},
			promClient: pc,
		},
		AccessChecker: resources.NewPredicateAccessChecker(pred.fn),
		Logger:        testLogger(),
	}

	r := chi.NewRouter()
	r.Get("/api/v1/monitoring/queries/*", h.HandleSlugQuery)
	return r
}

// slugRequest issues an authenticated GET against the slug route as a
// non-admin user.
func slugRequest(t *testing.T, router http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{
		ID:                 "u1",
		Username:           "viewer",
		KubernetesUsername: "viewer@example.com",
		KubernetesGroups:   []string{"viewers"},
		Roles:              []string{"viewer"},
	}))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestHandleSlugQuery_ClusterWideAuthorization is the load-bearing test for
// this unit. A cluster-wide slug resolves to an empty namespace; the RBAC
// check must run at cluster scope and must not be satisfiable by a
// namespace-scoped grant, however the caller decorates the query string.
func TestHandleSlugQuery_ClusterWideAuthorization(t *testing.T) {
	cases := []struct {
		name string
		// allow decides the fake RBAC answer.
		allow    func(predicateCall) bool
		target   string
		wantCode int
		// wantNamespace is the namespace the RBAC check must have been made
		// against ("" = cluster scope).
		wantNamespace string
		wantResource  string
	}{
		{
			name:          "cluster-wide grant on pods succeeds for top consumers cpu",
			allow:         func(c predicateCall) bool { return c.namespace == "" },
			target:        "/api/v1/monitoring/queries/cluster/top-consumers-cpu",
			wantCode:      http.StatusOK,
			wantNamespace: "",
			wantResource:  "pods",
		},
		{
			name:          "cluster-wide grant on pods succeeds for top consumers memory",
			allow:         func(c predicateCall) bool { return c.namespace == "" },
			target:        "/api/v1/monitoring/queries/cluster/top-consumers-memory",
			wantCode:      http.StatusOK,
			wantNamespace: "",
			wantResource:  "pods",
		},
		{
			name:          "cluster-wide grant on pvcs succeeds for storage capacity",
			allow:         func(c predicateCall) bool { return c.namespace == "" },
			target:        "/api/v1/monitoring/queries/cluster/storage-capacity",
			wantCode:      http.StatusOK,
			wantNamespace: "",
			wantResource:  "persistentvolumeclaims",
		},
		{
			name:          "caller without the verb is refused, not errored",
			allow:         func(predicateCall) bool { return false },
			target:        "/api/v1/monitoring/queries/cluster/top-consumers-cpu",
			wantCode:      http.StatusNotFound,
			wantNamespace: "",
			wantResource:  "pods",
		},
		{
			name:          "pvc reader cannot reach the pod-backed slug",
			allow:         func(c predicateCall) bool { return c.resource == "persistentvolumeclaims" },
			target:        "/api/v1/monitoring/queries/cluster/top-consumers-cpu",
			wantCode:      http.StatusNotFound,
			wantNamespace: "",
			wantResource:  "pods",
		},
		{
			name:          "pod reader cannot reach the pvc-backed slug",
			allow:         func(c predicateCall) bool { return c.resource == "pods" },
			target:        "/api/v1/monitoring/queries/cluster/storage-capacity",
			wantCode:      http.StatusNotFound,
			wantNamespace: "",
			wantResource:  "persistentvolumeclaims",
		},
		{
			// The scope-inversion case. A viewer holding pods/list in exactly
			// one namespace must not be able to satisfy a cluster-wide slug's
			// authorization by naming that namespace in the query string —
			// the rendered query has no namespace filter and would return
			// every namespace's pods.
			name:          "namespace-scoped grant does not satisfy a cluster-wide slug",
			allow:         func(c predicateCall) bool { return c.namespace == "tenant-a" },
			target:        "/api/v1/monitoring/queries/cluster/top-consumers-cpu?namespace=tenant-a",
			wantCode:      http.StatusNotFound,
			wantNamespace: "",
			wantResource:  "pods",
		},
		{
			// Same for the name parameter, which the namespaces/* special case
			// otherwise promotes into the RBAC namespace.
			name:          "name parameter does not narrow a cluster-wide slug's check",
			allow:         func(c predicateCall) bool { return c.namespace == "tenant-a" },
			target:        "/api/v1/monitoring/queries/cluster/storage-capacity?name=tenant-a&namespace=tenant-a",
			wantCode:      http.StatusNotFound,
			wantNamespace: "",
			wantResource:  "persistentvolumeclaims",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred := &recordingPredicate{allow: tc.allow}
			router := newSlugTestHandler(t, pred)

			w := slugRequest(t, router, tc.target)

			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tc.wantCode, w.Body.String())
			}

			calls := pred.recorded()
			if len(calls) == 0 {
				t.Fatal("no RBAC check was made — the slug is unauthorized")
			}
			for _, c := range calls {
				if c.namespace != tc.wantNamespace {
					t.Errorf("RBAC check ran against namespace %q, want %q — a cluster-wide "+
						"query authorized against a single namespace is an open read", c.namespace, tc.wantNamespace)
				}
				if c.resource != tc.wantResource {
					t.Errorf("RBAC check ran against resource %q, want %q", c.resource, tc.wantResource)
				}
				if c.apiGroup != "" {
					t.Errorf("RBAC check ran against apiGroup %q, want core group", c.apiGroup)
				}
				if c.verb != "list" {
					t.Errorf("RBAC check used verb %q, want \"list\"", c.verb)
				}
			}
		})
	}
}

// TestHandleSlugQuery_RefusalIsNotAServerError pins that a denied caller gets a
// permission outcome with the catalog-opaque body, never a 5xx.
func TestHandleSlugQuery_RefusalIsNotAServerError(t *testing.T) {
	pred := &recordingPredicate{allow: func(predicateCall) bool { return false }}
	router := newSlugTestHandler(t, pred)

	for _, slug := range clusterWideSlugs {
		t.Run(slug, func(t *testing.T) {
			w := slugRequest(t, router, "/api/v1/monitoring/queries/"+slug)

			if w.Code >= 500 {
				t.Fatalf("refusal surfaced as a server error: status %d, body %s", w.Code, w.Body.String())
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}

			var resp struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if resp.Error.Message != "not found or forbidden" {
				t.Errorf("message = %q, want the catalog-opaque refusal", resp.Error.Message)
			}
			if strings.Contains(resp.Error.Message, slug) {
				t.Errorf("refusal echoed the slug name: %q", resp.Error.Message)
			}
		})
	}
}

// TestHandleSlugQuery_PerResourceSlugsUnaffected guards the existing
// authorization scoping against the parameter-resolution change the
// cluster-wide shape required.
func TestHandleSlugQuery_PerResourceSlugsUnaffected(t *testing.T) {
	cases := []struct {
		name          string
		target        string
		wantNamespace string
		wantResource  string
		wantGroup     string
	}{
		{
			name:          "namespaced slug still checks the requested namespace",
			target:        "/api/v1/monitoring/queries/pods/cpu?namespace=tenant-a&name=nginx",
			wantNamespace: "tenant-a",
			wantResource:  "pods",
		},
		{
			name:          "grouped namespaced slug still checks the requested namespace",
			target:        "/api/v1/monitoring/queries/deployments/cpu?namespace=tenant-a&name=api",
			wantNamespace: "tenant-a",
			wantResource:  "deployments",
			wantGroup:     "apps",
		},
		{
			// F#10: for namespaces/* the target namespace lives in `name`.
			name:          "namespaces slug still promotes name into the RBAC namespace",
			target:        "/api/v1/monitoring/queries/namespaces/cpu?name=tenant-b",
			wantNamespace: "tenant-b",
			wantResource:  "pods",
		},
		{
			// Cluster-scoped GVR: rbacNS is forced to "" by clusterScopedGVRs.
			name:          "cluster-scoped resource slug still checks at cluster scope",
			target:        "/api/v1/monitoring/queries/nodes/cpu?name=node-1",
			wantNamespace: "",
			wantResource:  "nodes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred := &recordingPredicate{allow: func(predicateCall) bool { return true }}
			router := newSlugTestHandler(t, pred)

			w := slugRequest(t, router, tc.target)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}

			calls := pred.recorded()
			if len(calls) == 0 {
				t.Fatal("no RBAC check was made")
			}
			for _, c := range calls {
				if c.namespace != tc.wantNamespace {
					t.Errorf("RBAC namespace = %q, want %q", c.namespace, tc.wantNamespace)
				}
				if c.resource != tc.wantResource {
					t.Errorf("RBAC resource = %q, want %q", c.resource, tc.wantResource)
				}
				if c.apiGroup != tc.wantGroup {
					t.Errorf("RBAC apiGroup = %q, want %q", c.apiGroup, tc.wantGroup)
				}
			}
		})
	}
}
