package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleOIDCMobileConfig_UnknownProvider asserts the 404 path when
// the providerID URL param doesn't resolve to a registered OIDC
// provider. testServer wires only a local credential provider.
func TestHandleOIDCMobileConfig_UnknownProvider(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/oidc/nonexistent/mobile-config",
		nil,
	)
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

// TestHandleOIDCMobileConfig_RouteRegistration smoke-checks the route is
// reachable through the router. Returns 404 (provider not found) instead
// of 404 (route not registered) — distinguished by the response shape.
func TestHandleOIDCMobileConfig_RouteRegistration(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/oidc/any-provider/mobile-config",
		nil,
	)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 from provider-not-found, got %d", w.Code)
	}

	// chi returns "404 page not found\n" for unregistered routes; our
	// handler returns a JSON envelope. Body shape distinguishes them.
	body := w.Body.String()
	if !contains(body, `"error"`) || !contains(body, `"unknown OIDC provider"`) {
		t.Fatalf("expected JSON 404 envelope from handler, got: %q", body)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
