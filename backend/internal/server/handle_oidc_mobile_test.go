package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleOIDCMobileExchange_MissingCSRF verifies the public-route inline
// CSRF check rejects requests without X-Requested-With.
func TestHandleOIDCMobileExchange_MissingCSRF(t *testing.T) {
	srv := testServer(t)

	body := `{"code":"abc","codeVerifier":"v","nonce":"n","state":"s"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oidc/authelia/mobile-exchange", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Intentionally no X-Requested-With header.
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleOIDCMobileExchange_MalformedBody covers the JSON decode error path.
func TestHandleOIDCMobileExchange_MalformedBody(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/oidc/authelia/mobile-exchange",
		strings.NewReader("not-json"),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleOIDCMobileExchange_RequiredFields exercises each per-field
// validation path with a single table-driven test.
func TestHandleOIDCMobileExchange_RequiredFields(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		expectField string
	}{
		{
			name:        "missing code",
			body:        `{"code":"","codeVerifier":"v","nonce":"n","state":"s"}`,
			expectField: "code required",
		},
		{
			name:        "missing codeVerifier",
			body:        `{"code":"abc","codeVerifier":"","nonce":"n","state":"s"}`,
			expectField: "codeVerifier required",
		},
		{
			name:        "missing nonce",
			body:        `{"code":"abc","codeVerifier":"v","nonce":"","state":"s"}`,
			expectField: "nonce required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer(t)

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/oidc/authelia/mobile-exchange",
				strings.NewReader(tc.body),
			)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			w := httptest.NewRecorder()

			srv.Router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", w.Code)
			}

			var resp struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Message != tc.expectField {
				t.Errorf("expected error message %q, got %q", tc.expectField, resp.Error.Message)
			}
		})
	}
}

// TestHandleOIDCMobileExchange_UnknownProvider verifies the handler returns
// 404 when the providerID URL param doesn't resolve to a registered OIDC
// provider. testServer wires only a local credential provider, so any
// providerID matches the "unknown" branch.
func TestHandleOIDCMobileExchange_UnknownProvider(t *testing.T) {
	srv := testServer(t)

	body := `{"code":"abc","codeVerifier":"v","nonce":"n","state":"s"}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/oidc/nonexistent/mobile-exchange",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error.Message != "unknown OIDC provider" {
		t.Errorf("unexpected error message: %q", resp.Error.Message)
	}
}

// TestClassifyOIDCMobileError covers the pure classifier — every branch
// must map to a stable, sanitized audit label.
func TestClassifyOIDCMobileError(t *testing.T) {
	cases := []struct {
		err    error
		expect string
	}{
		{nil, "unknown"},
		{errors.New("email domain not allowed"), "domain not allowed"},
		{errors.New("oidc id token nonce mismatch"), "id token nonce mismatch"},
		{errors.New("email address not verified by identity provider"), "email not verified"},
		{errors.New("ID token verification failed: bad signature"), "id token verification failed"},
		{errors.New("no id_token in token response"), "no id_token in response"},
		{errors.New("token exchange failed: 400 Bad Request"), "code exchange rejected"},
		{errors.New("extracting claims: invalid json"), "claims extraction failed"},
		{errors.New("failed to map OIDC claims to user"), "claims mapping failed"},
		{errors.New("something else entirely"), "unspecified failure"},
	}

	for _, tc := range cases {
		got := classifyOIDCMobileError(tc.err)
		if got != tc.expect {
			label := "<nil>"
			if tc.err != nil {
				label = tc.err.Error()
			}
			t.Errorf("classifyOIDCMobileError(%q) = %q, want %q", label, got, tc.expect)
		}
	}
}

// TestHandleOIDCMobileExchange_RouteRegistration is a smoke check that the
// route is reachable through the router. We confirm by sending a request
// without CSRF and asserting we get 403 (route matched, CSRF rejected) not
// 404 (route not registered).
func TestHandleOIDCMobileExchange_RouteRegistration(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/oidc/any-provider/mobile-exchange",
		strings.NewReader(`{}`),
	)
	// No X-Requested-With — expect 403 from CSRF, not 404 from missing route.
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatal("route not registered — got 404")
	}
}
