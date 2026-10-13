package server

// routes_incidents_test.go — Release D U23a: proves the incident endpoints are
// registered on the real router, that the authenticated group's auth and
// CSRF guards hold on them, that they draw from their own per-user rate-limit
// bucket, and that a handler without a database answers 503
// incident_persistence_unavailable on every route rather than chi's 404.
//
// It follows routes_changes_test.go, which owns the /changes group the same
// way.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/incidents"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// wantIncidentRoutes is the whole /incidents surface, relative to /api/v1.
var wantIncidentRoutes = map[string]bool{
	"GET /incidents":                                true,
	"POST /incidents":                               true,
	"GET /incidents/{incidentID}":                   true,
	"PUT /incidents/{incidentID}":                   true,
	"DELETE /incidents/{incidentID}":                true,
	"GET /incidents/{incidentID}/notes":             true,
	"POST /incidents/{incidentID}/notes":            true,
	"PUT /incidents/{incidentID}/notes/{noteID}":    true,
	"DELETE /incidents/{incidentID}/notes/{noteID}": true,
	// U23b
	"GET /incidents/{incidentID}/evidence":              true,
	"POST /incidents/{incidentID}/capture":              true,
	"GET /incidents/{incidentID}/grants":                true,
	"POST /incidents/{incidentID}/grants":               true,
	"DELETE /incidents/{incidentID}/grants/{granteeID}": true,
	"GET /incidents/{incidentID}/export":                true,
}

const (
	incidentsTestID = "0f6a0000-0000-4000-8000-000000000002"
	noteTestID      = "0f6a0000-0000-4000-8000-000000000003"
	granteeTestID   = "local:u2"
)

// incidentsHandlerWithoutDB is the handler main.go builds when no database is
// configured: every endpoint answers 503 with a reason. The collector is
// nil too; the store gate answers first on every route, capture included.
func incidentsHandlerWithoutDB() *incidents.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return incidents.NewHandler(nil, nil, nil, nil, incidents.DefaultLimits(), nil, audit.NewSlogLogger(logger), logger)
}

// incidentsFullServer builds the production server through New so requests
// pass the real /api/v1 stack.
func incidentsFullServer(t *testing.T, h *incidents.Handler, incidentsLimiter *middleware.RateLimiter) (*Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tm := auth.NewTokenManager([]byte("test-signing-key-minimum-32-bytes"))
	deps := Deps{
		Config: &config.Config{
			Dev:       true,
			ClusterID: "test-cluster",
			Server: config.ServerConfig{
				Port:            8080,
				RequestTimeout:  config.DefaultRequestTimeout,
				ShutdownTimeout: config.DefaultShutdownTimeout,
			},
			Log: config.LogConfig{Level: "error", Format: "json"},
		},
		Logger:               logger,
		TokenManager:         tm,
		AuthRegistry:         auth.NewProviderRegistry(),
		OIDCStateStore:       auth.NewOIDCStateStore(),
		Sessions:             auth.NewSessionStore(),
		AuditLogger:          audit.NewSlogLogger(logger),
		RateLimiter:          middleware.NewRateLimiter(),
		YAMLRateLimiter:      middleware.NewRateLimiterWithRate(100, time.Minute),
		ChangesRateLimiter:   middleware.NewRateLimiterWithRate(100, time.Minute),
		IncidentsRateLimiter: incidentsLimiter,
		ReadyFn:              func() bool { return true },
		IncidentsHandler:     h,
	}
	srv := New(deps)
	return srv, changesTokenFor(t, srv, "u1", false)
}

func incidentPath(route string) (method, path string) {
	method, path, _ = strings.Cut(route, " ")
	path = strings.Replace(path, "{incidentID}", incidentsTestID, 1)
	path = strings.Replace(path, "{noteID}", noteTestID, 1)
	path = strings.Replace(path, "{granteeID}", granteeTestID, 1)
	return method, "/api/v1" + path
}

// TestRoutes_IncidentsRegisteredWhenHandlerPresent asserts the registration
// function wires exactly the promised routes.
func TestRoutes_IncidentsRegisteredWhenHandlerPresent(t *testing.T) {
	s := &Server{IncidentsHandler: incidentsHandlerWithoutDB(), RateLimiter: middleware.NewRateLimiter()}
	r := chi.NewRouter()
	s.registerIncidentRoutes(r)

	got := walkRoutes(t, r)
	for want := range wantIncidentRoutes {
		if !got[want] {
			t.Errorf("route %q is not registered by registerIncidentRoutes", want)
		}
	}
	for have := range got {
		if !wantIncidentRoutes[have] {
			t.Errorf("unexpected route %q registered; add it to wantIncidentRoutes or remove it", have)
		}
	}
}

// TestRoutes_IncidentsRequireAuthAndCSRFAndAnswer503WithoutDB drives the
// production router: no token is 401, a write without X-Requested-With is
// 403, and with both every route reaches the handler, which without a
// database answers 503 incident_persistence_unavailable and never 404.
func TestRoutes_IncidentsRequireAuthAndCSRFAndAnswer503WithoutDB(t *testing.T) {
	srv, tok := incidentsFullServer(t, incidentsHandlerWithoutDB(), middleware.NewRateLimiterWithRate(100, time.Minute))

	for route := range wantIncidentRoutes {
		method, path := incidentPath(route)
		if code := changesSend(srv, "", method, path, true); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: status %d, want 401", method, path, code)
		}
		if method != http.MethodGet {
			if code := changesSend(srv, tok, method, path, false); code != http.StatusForbidden {
				t.Errorf("%s %s without X-Requested-With: status %d, want 403", method, path, code)
			}
		}
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		rec := httptest.NewRecorder()
		srv.Router.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s answered 404 with a DB-less handler; the route is missing or the gate is wrong", method, path)
			continue
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d with a DB-less handler; want 503", method, path, rec.Code)
			continue
		}
		var body api.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == nil {
			t.Errorf("%s %s: body is not an error envelope: %s", method, path, rec.Body.String())
			continue
		}
		if body.Error.Reason != incidents.ReasonPersistenceUnavailable || body.Error.Extra["requires"] != "postgresql" {
			t.Errorf("%s %s: reason %q extra %v", method, path, body.Error.Reason, body.Error.Extra)
		}
	}
}

// TestRoutes_IncidentsRateLimitedPerUserWithOwnBucket proves /incidents
// draws from the dedicated per-user limiter: a second user keeps a full
// budget when the first is exhausted, and the YAML and changes buckets are
// untouched.
func TestRoutes_IncidentsRateLimitedPerUserWithOwnBucket(t *testing.T) {
	limiter := middleware.NewRateLimiterWithRate(2, time.Minute)
	srv, tok := incidentsFullServer(t, incidentsHandlerWithoutDB(), limiter)
	other := changesTokenFor(t, srv, "u2", false)

	path := "/api/v1/incidents"
	for i := range 2 {
		if code := changesSend(srv, tok, http.MethodGet, path, false); code != http.StatusServiceUnavailable {
			t.Fatalf("read %d: status %d, want 503 from the DB-less handler", i+1, code)
		}
	}
	if code := changesSend(srv, tok, http.MethodGet, path, false); code != http.StatusTooManyRequests {
		t.Fatalf("read 3: status %d, want 429 from the incidents limiter", code)
	}
	// Keyed per user: u2 is not starved by u1 even though httptest gives both
	// the same RemoteAddr.
	if code := changesSend(srv, other, http.MethodGet, path, false); code != http.StatusServiceUnavailable {
		t.Fatalf("other user: status %d, want 503 (own budget)", code)
	}
	if allowed, _ := srv.YAMLRateLimiter.Check("192.0.2.1"); !allowed {
		t.Fatal("incident reads consumed the YAML rate-limit bucket")
	}
	if allowed, _ := srv.ChangesRateLimiter.Check("changes:user:local:u1"); !allowed {
		t.Fatal("incident reads consumed the changes rate-limit bucket")
	}
}
