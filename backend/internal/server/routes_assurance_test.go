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

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
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
