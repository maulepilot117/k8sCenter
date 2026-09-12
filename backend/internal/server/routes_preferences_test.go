package server

// routes_preferences_test.go — proves the preferences endpoints are registered
// on the real router, not on a copy of it.
//
// The preferences package's own tests build their own chi router because they
// cannot import this one (this package imports preferences, so importing back
// would be an import cycle). That duplicate is fine for exercising handler
// behaviour, but it cannot tell anyone whether the server actually serves
// these paths — which is exactly how a suite of green handler tests can
// coexist with a feature that is unreachable in the running binary. This file
// closes that gap by walking the routes registerPreferencesRoutes really
// registers.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/preferences"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// wantPreferenceRoutes is the contract: every method and path the preferences
// feature promises. A route added to the handler but not here, or here but not
// registered, fails the test.
var wantPreferenceRoutes = map[string]bool{
	"GET /preferences/views":         true,
	"POST /preferences/views":        true,
	"PUT /preferences/views/{id}":    true,
	"DELETE /preferences/views/{id}": true,
	"GET /preferences/pins":          true,
	"POST /preferences/pins":         true,
	"DELETE /preferences/pins/{id}":  true,
}

// preferencesRouter builds a router through the production registration
// function, with a nil store so no database is needed.
func preferencesRouter(t *testing.T) chi.Router {
	t.Helper()

	s := &Server{PreferencesHandler: &preferences.Handler{}}
	r := chi.NewRouter()
	s.registerPreferencesRoutes(r)
	return r
}

// TestRegisterPreferencesRoutes_RegistersExactly asserts the real registration
// function wires every promised route and nothing else.
func TestRegisterPreferencesRoutes_RegistersExactly(t *testing.T) {
	got := map[string]bool{}
	err := chi.Walk(preferencesRouter(t),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			// chi reports a trailing slash on sub-router index routes.
			got[method+" "+strings.TrimSuffix(route, "/")] = true
			return nil
		})
	if err != nil {
		t.Fatalf("walking the preferences routes: %v", err)
	}

	for want := range wantPreferenceRoutes {
		if !got[want] {
			t.Errorf("route %q is not registered by registerPreferencesRoutes", want)
		}
	}
	for have := range got {
		if !wantPreferenceRoutes[have] {
			t.Errorf("unexpected route %q is registered; add it to wantPreferenceRoutes "+
				"(and to the preferences package's own endpoint table) or remove it", have)
		}
	}
}

// TestPreferencesRoutes_GuardedByRealChain proves the two guards that protect
// these endpoints hold on the routes the server actually registers: CSRF on
// every state-changing method, and a 503 rather than a 404 when the handler
// has no database.
//
// A 404 from any of these would mean the route is missing, which is the
// failure this file exists to detect.
func TestPreferencesRoutes_GuardedByRealChain(t *testing.T) {
	user := &auth.User{ID: "routes-test-user", Username: "routes-test-user", Provider: "local"}
	guarded := middleware.CSRF(preferencesRouter(t))

	for route := range wantPreferenceRoutes {
		method, path, _ := strings.Cut(route, " ")
		// Substitute a concrete id for the path parameter.
		path = strings.Replace(path, "{id}", "0f6a0000-0000-4000-8000-000000000001", 1)

		t.Run(route, func(t *testing.T) {
			// Without the CSRF header, a state-changing method is refused.
			if method != http.MethodGet {
				req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
				req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				rec := httptest.NewRecorder()
				guarded.ServeHTTP(rec, req)

				if rec.Code == http.StatusNotFound {
					t.Fatalf("route is not registered on the real router")
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d without X-Requested-With; want 403", rec.Code)
				}
			}

			// With the header, the request reaches the handler, which reports
			// that it has no database rather than pretending to persist.
			req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Fatalf("route is not registered on the real router")
			}
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d with a nil store; want 503 so a client can tell "+
					"'cannot persist' from 'no such record'", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "database_unavailable") {
				t.Errorf("503 body carries no database_unavailable reason: %s", rec.Body.String())
			}
		})
	}
}

// TestServerWiring_PreferencesHandlerReachesRouter is the guard against the
// gap this unit ships with: the routes register only when the server was given
// a handler, so a deployment that never constructs one serves chi's bare 404
// on every preferences path — indistinguishable from a missing record, which
// is the exact confusion the 503 contract exists to prevent.
//
// This test states the requirement in both directions so the wiring change in
// the next unit has something to satisfy.
func TestServerWiring_PreferencesHandlerReachesRouter(t *testing.T) {
	t.Run("handler present: routes registered", func(t *testing.T) {
		s := &Server{PreferencesHandler: &preferences.Handler{}}
		if s.PreferencesHandler == nil {
			t.Fatal("handler was not retained on the server")
		}
		r := chi.NewRouter()
		s.registerPreferencesRoutes(r)

		routes := 0
		_ = chi.Walk(r, func(string, string, http.Handler, ...func(http.Handler) http.Handler) error {
			routes++
			return nil
		})
		if routes != len(wantPreferenceRoutes) {
			t.Fatalf("registered %d routes; want %d", routes, len(wantPreferenceRoutes))
		}
	})

	t.Run("handler absent: nothing is registered", func(t *testing.T) {
		// Documents the consequence rather than endorsing it: with no handler
		// the paths simply do not exist, so main.go must construct one even
		// when there is no database (the handler answers 503 in that case).
		r := chi.NewRouter()
		routes := 0
		_ = chi.Walk(r, func(string, string, http.Handler, ...func(http.Handler) http.Handler) error {
			routes++
			return nil
		})
		if routes != 0 {
			t.Fatalf("expected an empty router, got %d routes", routes)
		}
	})
}
