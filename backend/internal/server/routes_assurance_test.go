package server

// routes_assurance_test.go — Release F U35: proves the backup-assurance
// endpoints are registered on the real velero route group, that the group's
// guards hold on them, and that the route table is the release boundary the
// plan promises: reads, plus three admin policy writes, and nothing that can
// reach a Velero mutation.

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
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/velero"
)

// wantAssuranceRoutes is the whole assurance surface. Every entry other than
// the three policy writes is a GET.
var wantAssuranceRoutes = map[string]bool{
	"GET /velero/assurance/status":           true,
	"GET /velero/assurance/exceptions":       true,
	"GET /velero/assurance/policies":         true,
	"POST /velero/assurance/policies":        true,
	"PUT /velero/assurance/policies/{id}":    true,
	"DELETE /velero/assurance/policies/{id}": true,
}

// veleroRouter builds the velero group through the production registration
// with a handler that has no database, so no store is needed.
func veleroRouter(t *testing.T) chi.Router {
	t.Helper()
	s := &Server{
		VeleroHandler: &velero.Handler{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		RateLimiter:   middleware.NewRateLimiter(),
	}
	r := chi.NewRouter()
	s.registerVeleroRoutes(r)
	return r
}

// TestAssuranceRoutes_ExposeNoMutatingClusterOperation walks the routes the
// server really registers under /velero/assurance and asserts they are
// exactly the promised set: every one is a GET except the three admin policy
// writes, which address /policies only. None of them is a restore, backup or
// schedule path, so no assurance route can start a Restore or a
// DeleteBackupRequest. That the policy writes touch PostgreSQL alone is
// proven against a fake cluster by
// velero.TestAssuranceEndpoints_NeverWriteToKubernetes.
func TestAssuranceRoutes_ExposeNoMutatingClusterOperation(t *testing.T) {
	got := map[string]bool{}
	err := chi.Walk(veleroRouter(t), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if strings.HasPrefix(route, "/velero/assurance") {
			got[method+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the velero routes: %v", err)
	}

	for want := range wantAssuranceRoutes {
		if !got[want] {
			t.Errorf("route %q is not registered by registerVeleroRoutes", want)
		}
	}
	for have := range got {
		if !wantAssuranceRoutes[have] {
			t.Errorf("unexpected assurance route %q; the assurance surface is observation only", have)
		}
		method, path, _ := strings.Cut(have, " ")
		if method != http.MethodGet && !strings.HasPrefix(path, "/velero/assurance/policies") {
			t.Errorf("%s is a write outside the policy table", have)
		}
		for _, forbidden := range []string{"restore", "backups", "schedules", "trigger"} {
			if strings.Contains(path, forbidden) {
				t.Errorf("%s names %q; an assurance route must not reach a Velero object", have, forbidden)
			}
		}
	}
}

// assuranceFullServer builds the production server through New, so requests
// pass the real /api/v1 middleware stack (Auth, CSRF, ClusterContext, rate
// limiting), with a DB-less velero handler and a YAML write limiter of rate.
func assuranceFullServer(t *testing.T, rate int) (*Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tm := auth.NewTokenManager([]byte("test-signing-key-minimum-32-bytes"))
	registry := auth.NewProviderRegistry()
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
		Logger:          logger,
		TokenManager:    tm,
		AuthRegistry:    registry,
		OIDCStateStore:  auth.NewOIDCStateStore(),
		Sessions:        auth.NewSessionStore(),
		AuditLogger:     audit.NewSlogLogger(logger),
		RateLimiter:     middleware.NewRateLimiter(),
		YAMLRateLimiter: middleware.NewRateLimiterWithRate(rate, time.Minute),
		ReadyFn:         func() bool { return true },
		VeleroHandler:   &velero.Handler{Logger: logger},
	})
	tok, err := tm.IssueAccessToken(&auth.User{ID: "admin", Username: "admin", KubernetesUsername: "admin", Roles: []string{"admin"}})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return srv, tok
}

// TestAssuranceRoutes_FullChainEnforcesCSRFAndRateLimit sends policy writes
// through the production router: without X-Requested-With the enclosing
// group refuses them, and past the YAML write limiter's budget they are 429
// before the handler runs.
func TestAssuranceRoutes_FullChainEnforcesCSRFAndRateLimit(t *testing.T) {
	writes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/velero/assurance/policies"},
		{http.MethodPut, "/api/v1/velero/assurance/policies/0f6a0000-0000-4000-8000-000000000001"},
		{http.MethodDelete, "/api/v1/velero/assurance/policies/0f6a0000-0000-4000-8000-000000000001?confirm=true"},
	}
	send := func(srv *Server, tok, method, path string, csrf bool) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		if csrf {
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
		}
		rec := httptest.NewRecorder()
		srv.Router.ServeHTTP(rec, req)
		return rec.Code
	}

	for _, wr := range writes {
		t.Run(wr.method, func(t *testing.T) {
			srv, tok := assuranceFullServer(t, 1)
			if code := send(srv, tok, wr.method, wr.path, false); code != http.StatusForbidden {
				t.Fatalf("without X-Requested-With: status %d, want 403", code)
			}
			if code := send(srv, tok, wr.method, wr.path, true); code != http.StatusServiceUnavailable {
				t.Fatalf("first write: status %d, want 503 from the DB-less handler", code)
			}
			if code := send(srv, tok, wr.method, wr.path, true); code != http.StatusTooManyRequests {
				t.Fatalf("second write: status %d, want 429 from the write limiter", code)
			}
		})
	}

	// Reads are not rate-limited by the write limiter.
	srv, tok := assuranceFullServer(t, 1)
	for range 3 {
		if code := send(srv, tok, http.MethodGet, "/api/v1/velero/assurance/status", false); code != http.StatusServiceUnavailable {
			t.Fatalf("status read: %d, want 503 every time", code)
		}
	}
}

// TestAssuranceRoutes_GuardedByRealChain proves CSRF refuses every
// assurance write without X-Requested-With, and that with it a DB-less
// handler answers 503 database_unavailable, never 404, on all six routes.
func TestAssuranceRoutes_GuardedByRealChain(t *testing.T) {
	admin := &auth.User{Username: "admin", KubernetesUsername: "admin", Roles: []string{"admin"}}
	guarded := middleware.CSRF(veleroRouter(t))

	for route := range wantAssuranceRoutes {
		method, path, _ := strings.Cut(route, " ")
		path = strings.Replace(path, "{id}", "0f6a0000-0000-4000-8000-000000000001", 1)

		t.Run(route, func(t *testing.T) {
			if method != http.MethodGet {
				req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
				req = req.WithContext(auth.ContextWithUser(req.Context(), admin))
				rec := httptest.NewRecorder()
				guarded.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d without X-Requested-With; want 403", rec.Code)
				}
			}

			req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			req = req.WithContext(auth.ContextWithUser(req.Context(), admin))
			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d with no database; want 503, never 404", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "database_unavailable") {
				t.Errorf("503 body carries no database_unavailable reason: %s", rec.Body.String())
			}
		})
	}
}
