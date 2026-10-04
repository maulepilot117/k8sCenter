package server

// routes_changes_test.go — Release E U29b: proves the tracked-change receipt
// endpoints are registered on the real router when (and only when) the changes
// handler is wired, that the authenticated group's CSRF guard holds on them,
// that they draw from their own rate-limit bucket, and that registering them
// leaves the /yaml route set untouched.
//
// It is a new file rather than a case in an existing one because no existing
// route test owns a feature group this shape; it follows routes_assurance_test.go
// and routes_preferences_test.go, which each own one group.

import (
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
	"github.com/kubecenter/kubecenter/internal/changes"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	yamlpkg "github.com/kubecenter/kubecenter/internal/yaml"
)

// wantChangesRoutes is the whole /changes surface, relative to /api/v1.
var wantChangesRoutes = map[string]bool{
	"GET /changes":                   true,
	"POST /changes/ownership":        true,
	"GET /changes/{id}":              true,
	"GET /changes/{id}/verification": true,
}

// wantYAMLRoutes pins the legacy /yaml surface, relative to the router
// registerYAMLRoutes is mounted on.
var wantYAMLRoutes = map[string]bool{
	"POST /yaml/validate":                        true,
	"POST /yaml/apply":                           true,
	"POST /yaml/diff":                            true,
	"GET /yaml/export/{kind}/{namespace}/{name}": true,
}

// changesHandlerWithoutDB is the handler U29b builds when no database is
// configured for the service but a handler is still mounted (every endpoint
// answers 503). Untyped nils for the optional collaborators, as in main.go.
func changesHandlerWithoutDB() *changes.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return changes.NewHandler(changes.NewService(nil, logger), nil, nil, nil, nil, logger)
}

func walkRoutes(t *testing.T, r chi.Router) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking routes: %v", err)
	}
	return got
}

// TestRoutes_ChangesRegisteredWhenHandlerPresent asserts the real registration
// function wires exactly the four promised routes.
func TestRoutes_ChangesRegisteredWhenHandlerPresent(t *testing.T) {
	s := &Server{ChangesHandler: changesHandlerWithoutDB(), RateLimiter: middleware.NewRateLimiter()}
	r := chi.NewRouter()
	s.registerChangesRoutes(r)

	got := walkRoutes(t, r)
	for want := range wantChangesRoutes {
		if !got[want] {
			t.Errorf("route %q is not registered by registerChangesRoutes", want)
		}
	}
	for have := range got {
		if !wantChangesRoutes[have] {
			t.Errorf("unexpected route %q registered; add it to wantChangesRoutes or remove it", have)
		}
	}
}

// changesFullServer builds the production server through New so requests pass
// the real /api/v1 stack. A nil handler leaves the changes group unregistered.
func changesFullServer(t *testing.T, h *changes.Handler, changesLimiter, yamlLimiter *middleware.RateLimiter) (*Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tm := auth.NewTokenManager([]byte("test-signing-key-minimum-32-bytes"))
	srv := New(Deps{
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
		Logger:             logger,
		TokenManager:       tm,
		AuthRegistry:       auth.NewProviderRegistry(),
		OIDCStateStore:     auth.NewOIDCStateStore(),
		Sessions:           auth.NewSessionStore(),
		AuditLogger:        audit.NewSlogLogger(logger),
		RateLimiter:        middleware.NewRateLimiter(),
		YAMLRateLimiter:    yamlLimiter,
		ChangesRateLimiter: changesLimiter,
		ReadyFn:            func() bool { return true },
		ChangesHandler:     h,
	})
	tok, err := tm.IssueAccessToken(&auth.User{ID: "u1", Username: "u1", KubernetesUsername: "u1", Roles: []string{"admin"}})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return srv, tok
}

func changesSend(srv *Server, tok, method, path string, csrf bool) int {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if csrf {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)
	return rec.Code
}

const changesTestID = "0f6a0000-0000-4000-8000-000000000001"

// TestRoutes_ChangesAbsentWhenHandlerNil asserts that with no database (nil
// handler) no /changes route exists at all: chi answers 404, not a 503 that
// would suggest the feature is half-mounted.
func TestRoutes_ChangesAbsentWhenHandlerNil(t *testing.T) {
	srv, tok := changesFullServer(t, nil, nil, middleware.NewRateLimiterWithRate(100, time.Minute))

	for route := range wantChangesRoutes {
		method, path, _ := strings.Cut(route, " ")
		path = "/api/v1" + strings.Replace(path, "{id}", changesTestID, 1)
		if code := changesSend(srv, tok, method, path, true); code != http.StatusNotFound {
			t.Errorf("%s %s = %d with no changes handler; want 404 (route absent)", method, path, code)
		}
	}
	for have := range walkRoutes(t, srv.Router) {
		if strings.Contains(have, "/changes") {
			t.Errorf("route %q registered with a nil ChangesHandler", have)
		}
	}
}

// TestRoutes_ChangesRequiresAuthAndCSRF drives the production router: no
// token is 401, a write without X-Requested-With is 403, and with both the
// request reaches the handler (503 from a handler with no database), never 404.
func TestRoutes_ChangesRequiresAuthAndCSRF(t *testing.T) {
	srv, tok := changesFullServer(t, changesHandlerWithoutDB(), nil, middleware.NewRateLimiterWithRate(100, time.Minute))

	const ownership = "/api/v1/changes/ownership"
	if code := changesSend(srv, "", http.MethodPost, ownership, true); code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", code)
	}
	if code := changesSend(srv, tok, http.MethodPost, ownership, false); code != http.StatusForbidden {
		t.Errorf("POST without X-Requested-With: status %d, want 403", code)
	}

	for route := range wantChangesRoutes {
		method, path, _ := strings.Cut(route, " ")
		path = "/api/v1" + strings.Replace(path, "{id}", changesTestID, 1)
		if code := changesSend(srv, tok, method, path, true); code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d with a DB-less handler; want 503, never 404", method, path, code)
		}
	}
}

// TestRoutes_ChangesRateLimitedWithOwnBucket proves /changes draws from the
// dedicated limiter and leaves the YAML bucket alone: receipt polling must not
// starve /yaml/apply.
func TestRoutes_ChangesRateLimitedWithOwnBucket(t *testing.T) {
	changesLimiter := middleware.NewRateLimiterWithRate(2, time.Minute)
	yamlLimiter := middleware.NewRateLimiterWithRate(1, time.Minute)
	srv, tok := changesFullServer(t, changesHandlerWithoutDB(), changesLimiter, yamlLimiter)

	path := "/api/v1/changes/" + changesTestID + "/verification"
	for i := range 2 {
		if code := changesSend(srv, tok, http.MethodGet, path, false); code != http.StatusServiceUnavailable {
			t.Fatalf("poll %d: status %d, want 503 from the DB-less handler", i+1, code)
		}
	}
	if code := changesSend(srv, tok, http.MethodGet, path, false); code != http.StatusTooManyRequests {
		t.Fatalf("poll 3: status %d, want 429 from the changes limiter", code)
	}

	// httptest requests carry RemoteAddr 192.0.2.1:1234; the YAML bucket (budget
	// 1) must still have its whole budget, so its first check is allowed.
	if allowed, _ := yamlLimiter.Check("192.0.2.1"); !allowed {
		t.Fatal("receipt polling consumed the YAML rate-limit bucket")
	}
}

// TestRoutes_YAMLRoutesUnchanged proves registering the changes group leaves
// the /yaml surface exactly as it was.
func TestRoutes_YAMLRoutesUnchanged(t *testing.T) {
	s := &Server{
		YAMLHandler:    &yamlpkg.Handler{},
		ChangesHandler: changesHandlerWithoutDB(),
		RateLimiter:    middleware.NewRateLimiter(),
	}
	r := chi.NewRouter()
	s.registerYAMLRoutes(r)
	s.registerChangesRoutes(r)

	gotYAML := map[string]bool{}
	for have := range walkRoutes(t, r) {
		if _, path, _ := strings.Cut(have, " "); strings.HasPrefix(path, "/yaml") {
			gotYAML[have] = true
		}
	}
	for want := range wantYAMLRoutes {
		if !gotYAML[want] {
			t.Errorf("yaml route %q missing", want)
		}
	}
	for have := range gotYAML {
		if !wantYAMLRoutes[have] {
			t.Errorf("unexpected yaml route %q", have)
		}
	}
}
