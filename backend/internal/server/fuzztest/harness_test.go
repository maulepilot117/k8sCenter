package fuzztest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/server/fuzztest"
)

// TestHarness_AuthOnly verifies the auth-only server mode:
//   - GET /api/v1/auth/me without a token → 401 (middleware short-circuits before Informers)
//   - POST /api/v1/auth/login with valid credentials → 200 (auth routes registered)
func TestHarness_AuthOnly(t *testing.T) {
	srv := fuzztest.NewServer(t, fuzztest.Opts{})

	// Unauthenticated GET /auth/me — middleware rejects before handler body runs.
	t.Run("unauthenticated_me_401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 without token, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Login route: registers and returns an access token.
	t.Run("login_returns_token", func(t *testing.T) {
		tok := fuzztest.CreateAdminUser(t, srv, "login-user", "password1234")
		if tok == "" {
			t.Fatal("CreateAdminUser returned empty token")
		}
		body := strings.NewReader(`{"username":"login-user","password":"password1234"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 on valid login, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestHarness_WithResources verifies the with-resources server mode:
//   - Resource routes are registered (non-404 for authed requests).
//   - Authenticated requests to resource routes do not panic (even on 500 from stub k8s).
//
// Note: handleAuthMe calls s.Informers.Factory() (for namespace listing) or
// s.RBACChecker (for per-namespace check). Neither is fully wired in the stub
// harness, so /auth/me is intentionally not tested here. The smoke test uses
// resource routes that return errors gracefully without panicking.
func TestHarness_WithResources(t *testing.T) {
	srv := fuzztest.NewServer(t, fuzztest.Opts{WithResources: true})
	tok := fuzztest.CreateAdminUser(t, srv, "res-admin", "password1234")

	// These routes must be registered (non-404) in WithResources mode.
	registrationChecks := []string{
		"/api/v1/resources/secrets/default/any-name",
		"/api/v1/resources/pods",
		"/api/v1/resources/deployments",
	}
	for _, path := range registrationChecks {
		path := path
		t.Run("registered:"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			w := httptest.NewRecorder()
			srv.Router.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound {
				t.Errorf("route %q returned 404 — not registered in WithResources mode", path)
			}
		})
	}
}

// TestHarness_BuildRequest verifies that BuildRequest never panics on
// edge-case corpus inputs.
func TestHarness_BuildRequest(t *testing.T) {
	cases := []struct {
		name   string
		corpus []byte
	}{
		{"empty", []byte{}},
		{"single_byte", []byte{0x42}},
		{"four_bytes", []byte{0x00, 0x01, 0x02, 0x03}},
		{"with_body", []byte{0x01, 0x02, 0x03, 0x00, '{', '}'}},
		{"all_zeros", make([]byte, 64)},
		{"all_0xff", func() []byte {
			b := make([]byte, 64)
			for i := range b {
				b[i] = 0xff
			}
			return b
		}()},
		{"valid_json_body", []byte{0x00, 0x00, 0x00, 0x01, '{', '"', 'x', '"', ':', '1', '}'}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := fuzztest.BuildRequest(tc.corpus, "dummy-token")
			if req == nil {
				t.Fatal("BuildRequest returned nil")
			}
			if req.URL == nil {
				t.Fatal("BuildRequest returned request with nil URL")
			}
			if req.Method == "" {
				t.Fatal("BuildRequest returned request with empty Method")
			}
		})
	}
}

// TestHarness_MintExpiredToken verifies that MintExpiredToken produces a token
// that the server's Auth middleware rejects with 401.
func TestHarness_MintExpiredToken(t *testing.T) {
	srv := fuzztest.NewServer(t, fuzztest.Opts{})

	expiredTok := fuzztest.MintExpiredToken(t, fuzztest.AdminUser())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+expiredTok)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHarness_GarbageToken verifies that the garbage token constant is rejected.
func TestHarness_GarbageToken(t *testing.T) {
	srv := fuzztest.NewServer(t, fuzztest.Opts{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", fuzztest.GarbageToken)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for garbage token, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHarness_SeedSecret verifies that SeedSecret constructs a well-formed Secret.
func TestHarness_SeedSecret(t *testing.T) {
	s := fuzztest.SeedSecret("default", "my-secret", map[string][]byte{
		"key": []byte("value"),
	})
	if s == nil {
		t.Fatal("SeedSecret returned nil")
	}
	if s.Name != "my-secret" {
		t.Errorf("SeedSecret name: got %q, want %q", s.Name, "my-secret")
	}
	if s.Namespace != "default" {
		t.Errorf("SeedSecret namespace: got %q, want %q", s.Namespace, "default")
	}
	if string(s.Data["key"]) != "value" {
		t.Errorf("SeedSecret data: got %q, want %q", s.Data["key"], "value")
	}
}
