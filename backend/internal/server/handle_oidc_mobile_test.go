package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kubecenter/kubecenter/internal/audit"
)

// recordingAuditLogger captures audit entries in memory for tests that
// need to inspect Detail/Result formatting. It satisfies [audit.Logger].
type recordingAuditLogger struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recordingAuditLogger) Log(_ context.Context, e audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func (r *recordingAuditLogger) snapshot() []audit.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

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

// TestCategorizeOIDCMobileError covers the consolidated categorizer —
// every branch must produce a stable audit label AND a defensible HTTP
// status. The two were previously split across classify + writeError; the
// drift risk that split created is exactly what this test pins down.
func TestCategorizeOIDCMobileError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantLabel  string
		wantStatus int
	}{
		{"nil", nil, "unknown", http.StatusUnauthorized},
		{"domain not allowed", errors.New("email domain not allowed"), "domain not allowed", http.StatusForbidden},
		{"nonce mismatch", errors.New("oidc id token nonce mismatch"), "id token nonce mismatch", http.StatusUnauthorized},
		{"email unverified", errors.New("email address not verified by identity provider"), "email not verified", http.StatusUnauthorized},
		{"id token verify fail", errors.New("ID token verification failed: bad signature"), "id token verification failed", http.StatusUnauthorized},
		{"no id_token", errors.New("no id_token in token response"), "no id_token in response", http.StatusUnauthorized},
		{"exchange timeout", errors.New("oidc token exchange timeout: context deadline exceeded"), "code exchange timeout", http.StatusServiceUnavailable},
		{"exchange rejected", errors.New("token exchange failed: 400 Bad Request"), "code exchange rejected", http.StatusUnauthorized},
		{"claims extract", errors.New("extracting claims: invalid json"), "claims extraction failed", http.StatusUnauthorized},
		{"claims map", errors.New("failed to map OIDC claims to user"), "claims mapping failed", http.StatusUnauthorized},
		{"unknown provider", errors.New("unknown OIDC provider"), "unknown provider", http.StatusNotFound},
		{"unspecified", errors.New("something else entirely"), "unspecified failure", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := categorizeOIDCMobileError(tc.err)
			if got.auditLabel != tc.wantLabel {
				t.Errorf("auditLabel = %q, want %q", got.auditLabel, tc.wantLabel)
			}
			if got.httpStatus != tc.wantStatus {
				t.Errorf("httpStatus = %d, want %d", got.httpStatus, tc.wantStatus)
			}
			if got.responseMessage == "" {
				t.Errorf("responseMessage empty for case %q", tc.name)
			}
		})
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

// TestHandleOIDCMobileExchange_AuditDetailFormat asserts the audit entry
// written on a failed mobile exchange follows the documented contract:
// `oidc/<providerID>/mobile: <classifier-label>` with no token, nonce, or
// verifier content. We trigger it through the unknown-provider 404 path
// (the handler audits that case too, alongside actual exchange failures).
func TestHandleOIDCMobileExchange_AuditDetailFormat(t *testing.T) {
	srv := testServer(t)
	rec := &recordingAuditLogger{}
	srv.AuditLogger = rec

	body := `{"code":"abc","codeVerifier":"v","nonce":"n"}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/oidc/missing-provider/mobile-exchange",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	entries := rec.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Action != audit.ActionLogin {
		t.Errorf("Action = %q, want %q", got.Action, audit.ActionLogin)
	}
	if got.Result != audit.ResultFailure {
		t.Errorf("Result = %q, want %q", got.Result, audit.ResultFailure)
	}
	wantDetail := "oidc/missing-provider/mobile: unknown provider"
	if got.Detail != wantDetail {
		t.Errorf("Detail = %q, want %q", got.Detail, wantDetail)
	}
}

// TestHandleOIDCMobileExchange_StateFieldIgnored asserts the handler still
// accepts (and silently discards) the legacy `state` field clients may
// still emit. json.Decoder drops unknown fields by default, so the
// validation must complete on code/codeVerifier/nonce only — and the
// request must NOT 400 because state is present.
func TestHandleOIDCMobileExchange_StateFieldIgnored(t *testing.T) {
	srv := testServer(t)

	body := `{"code":"abc","codeVerifier":"v","nonce":"n","state":"client-csrf-token"}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/oidc/missing-provider/mobile-exchange",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	// We reach the unknown-provider branch — proving body decoded cleanly
	// despite the extra `state` field. A 400 here would mean state broke
	// JSON parsing or the validators rejected the request unexpectedly.
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (unknown provider), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleOIDCMobileExchange_MaxBytes asserts the maxAuthBodySize cap
// rejects oversized payloads. The MaxBytesReader wraps r.Body before
// json.Decoder runs, so the Decode call returns an error and the handler
// surfaces it as a 400. Without the cap an attacker could pin handler
// memory with a multi-MB JSON blob.
func TestHandleOIDCMobileExchange_MaxBytes(t *testing.T) {
	srv := testServer(t)

	// 65KB of valid-but-padded JSON. The cap is maxAuthBodySize (smaller
	// than 65KB), so MaxBytesReader trips during Decode.
	padding := strings.Repeat("A", 65*1024)
	body := `{"code":"` + padding + `","codeVerifier":"v","nonce":"n"}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/oidc/authelia/mobile-exchange",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d: %s", w.Code, w.Body.String())
	}
}
