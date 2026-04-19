package servicemesh

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// --- parse / dispatch unit tests -------------------------------------------

func TestParseMeshCompositeID(t *testing.T) {
	tests := []struct {
		in        string
		wantMesh  string
		wantNS    string
		wantCode  string
		wantName  string
		wantError bool
	}{
		{"istio:shop:vs:cart", "istio", "shop", "vs", "cart", false},
		{"linkerd:default:sp:books", "linkerd", "default", "sp", "books", false},
		// SplitN(4) keeps extra colons in the name — a valid resource name may contain ":".
		{"istio:ns:vs:name-with-colons:extra", "istio", "ns", "vs", "name-with-colons:extra", false},
		// URL-encoded composites decode correctly.
		{"istio%3Ashop%3Avs%3Acart", "istio", "shop", "vs", "cart", false},
		// Invalid shapes all return errors — handler emits 400.
		{"", "", "", "", "", true},
		{"istio:ns", "", "", "", "", true},
		{"istio:ns:vs", "", "", "", "", true},
		{":ns:vs:name", "", "", "", "", true},
		{"istio::vs:name", "", "", "", "", true},
		{"istio:ns::name", "", "", "", "", true},
		{"istio:ns:vs:", "", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			mesh, ns, code, name, err := parseMeshCompositeID(tt.in)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error for %q", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mesh != tt.wantMesh || ns != tt.wantNS || code != tt.wantCode || name != tt.wantName {
				t.Errorf("got (%q,%q,%q,%q), want (%q,%q,%q,%q)",
					mesh, ns, code, name, tt.wantMesh, tt.wantNS, tt.wantCode, tt.wantName)
			}
		})
	}
}

func TestResolveKind(t *testing.T) {
	tests := []struct {
		mesh, code string
		wantKind   string
		wantGroup  string
		wantOK     bool
	}{
		{"istio", "vs", "VirtualService", "networking.istio.io", true},
		{"istio", "pa", "PeerAuthentication", "security.istio.io", true},
		// The "ap" code is shared between Istio and Linkerd; resolution must
		// disambiguate on the mesh prefix.
		{"istio", "ap", "AuthorizationPolicy", "security.istio.io", true},
		{"linkerd", "ap", "AuthorizationPolicy", "policy.linkerd.io", true},
		{"linkerd", "sp", "ServiceProfile", "linkerd.io", true},
		{"linkerd", "srv", "Server", "policy.linkerd.io", true},
		{"istio", "unknown", "", "", false},
		{"bogus", "vs", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.mesh+":"+tt.code, func(t *testing.T) {
			kind, entry, ok := resolveKind(tt.mesh, tt.code)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if kind != tt.wantKind || entry.APIGroup != tt.wantGroup {
				t.Errorf("got (%q,%q), want (%q,%q)", kind, entry.APIGroup, tt.wantKind, tt.wantGroup)
			}
		})
	}
}

// --- handler integration tests --------------------------------------------

func seededDiscoverer(status MeshStatus) *Discoverer {
	return &Discoverer{
		logger: slog.Default(),
		status: MeshStatus{
			Detected:    status.Detected,
			Istio:       status.Istio,
			Linkerd:     status.Linkerd,
			LastChecked: time.Now().UTC(),
		},
	}
}

// TestHandler_NoMeshInstalled covers the plan's error-path scenario: when
// neither mesh is installed, HandleListRoutes returns {routes: []} with
// status.detected = "none", not a 500.
func TestHandler_NoMeshInstalled(t *testing.T) {
	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshNone}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}

	w := doGet(t, h.HandleListRoutes, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data routingResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Status.Detected != MeshNone {
		t.Errorf("Status.Detected = %q, want %q", env.Data.Status.Detected, MeshNone)
	}
	if env.Data.Routes == nil {
		t.Error("Routes is nil; frontend treats null as error — want empty slice")
	}
	if len(env.Data.Routes) != 0 {
		t.Errorf("Routes = %d, want 0", len(env.Data.Routes))
	}
}

// TestHandler_ListRoutes_AllVisibleWithFullAccess covers the plan's happy path:
// an authenticated user with cluster-wide RBAC sees every route.
func TestHandler_ListRoutes_AllVisibleWithFullAccess(t *testing.T) {
	vsFoo := virtualService("foo", "a", []string{"a.foo"}, nil)
	vsBar := virtualService("bar", "b", []string{"b.bar"}, nil)

	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true, Version: "1.24.0"}}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   newIstioFakeDynClient(vsFoo, vsBar),
	}

	w := doGet(t, h.HandleListRoutes, &auth.User{KubernetesUsername: "u", KubernetesGroups: []string{"g"}})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data routingResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := len(env.Data.Routes); got != 2 {
		t.Errorf("Routes = %d, want 2", got)
	}
}

// TestHandler_ListRoutes_DeniedUserSeesNothing covers the plan's RBAC scenario:
// a user whose access checker denies every namespace gets an empty list, not
// a 403. This is the canonical partial-access shape — the handler never leaks
// unchecked rows.
func TestHandler_ListRoutes_DeniedUserSeesNothing(t *testing.T) {
	vs := virtualService("foo", "a", []string{"a"}, nil)

	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true, Version: "1.24.0"}}),
		AccessChecker: resources.NewAlwaysDenyAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   newIstioFakeDynClient(vs),
	}

	w := doGet(t, h.HandleListRoutes, &auth.User{KubernetesUsername: "u", KubernetesGroups: []string{"g"}})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (denial yields empty list, not 403)", w.Code)
	}
	var env struct {
		Data routingResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Routes) != 0 {
		t.Errorf("Routes = %d, want 0 (denied user sees nothing)", len(env.Data.Routes))
	}
}

// TestHandler_GetRouteBadID covers the plan's error-path scenario for
// malformed composite IDs → 400 with a structured error message.
func TestHandler_GetRouteBadID(t *testing.T) {
	h := &Handler{
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}

	req := httptest.NewRequest(http.MethodGet, "/mesh/routing/bogus", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	req = withURLParam(req, "id", "bogus")

	w := httptest.NewRecorder()
	h.HandleGetRoute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// TestHandler_GetRouteUnknownKind covers the plan's error-path scenario: a
// well-formed composite ID with an unknown kind code must return 400 rather
// than bypass the access check.
func TestHandler_GetRouteUnknownKind(t *testing.T) {
	h := &Handler{
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}

	req := httptest.NewRequest(http.MethodGet, "/mesh/routing/", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	req = withURLParam(req, "id", "istio:ns:xx:name") // parses, kind code "xx" is unknown

	w := httptest.NewRecorder()
	h.HandleGetRoute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown kind code", w.Code)
	}
}

// TestHandler_SingleflightCoalesces covers the plan's cache scenario: two (or
// more) concurrent list requests collapse into a single upstream fetch. The
// reactor counter increments on every live List call; under singleflight, the
// count after N concurrent requests should stay well below N.
func TestHandler_SingleflightCoalesces(t *testing.T) {
	vs := virtualService("foo", "a", []string{"a"}, nil)

	var listCalls int64
	client := newIstioFakeDynClient(vs)
	// One reactor per CRD kind so the count reflects actual fan-out, not just
	// a single probe. Counting on virtualservices is enough — doFetch always
	// lists every kind as part of the same singleflight shot.
	client.PrependReactor("list", "virtualservices", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		atomic.AddInt64(&listCalls, 1)
		return false, nil, nil // let the default reactor service the request
	})

	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true, Version: "1.24.0"}}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   client,
	}
	h.InvalidateCache() // in case a previous test left something cached

	const n = 20
	start := make(chan struct{})
	done := make(chan struct{}, n)
	for range n {
		go func() {
			<-start
			w := doGet(t, h.HandleListRoutes, &auth.User{KubernetesUsername: "u"})
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
			done <- struct{}{}
		}()
	}
	close(start)
	for range n {
		<-done
	}

	// Under singleflight, the listCalls counter should be small regardless of
	// N. We assert strictly less than N to stay resilient to goroutine
	// scheduling that might let a second fetch start before the first caches.
	got := atomic.LoadInt64(&listCalls)
	if got >= int64(n) {
		t.Errorf("virtualservices List calls = %d, want < %d (singleflight not coalescing)", got, n)
	}
}

// --- helpers ---------------------------------------------------------------

func doGet(t *testing.T, handler http.HandlerFunc, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/mesh/routing", nil)
	if user == nil {
		user = &auth.User{KubernetesUsername: "u", KubernetesGroups: []string{"g"}}
	}
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func withURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
