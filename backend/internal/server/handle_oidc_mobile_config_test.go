package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
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
	if !strings.Contains(body, `"error"`) ||
		!strings.Contains(body, `"unknown OIDC provider"`) {
		t.Fatalf("expected JSON 404 envelope from handler, got: %q", body)
	}
}

// TestHandleOIDCMobileConfig_Success asserts the 200 path: a registered
// provider returns the three load-bearing fields (authorizationEndpoint,
// clientId, scopes) and never leaks the clientSecret. The clientId JSON
// key uses lowercase-d to match the rest of the API surface convention
// (see issue #282).
func TestHandleOIDCMobileConfig_Success(t *testing.T) {
	srv := testServer(t)

	// Register a stub OIDC provider via the test-only constructor —
	// skips live `.well-known/openid-configuration` discovery so the
	// test doesn't need a mock IdP.
	srv.AuthRegistry.RegisterOIDC("authelia", auth.NewOIDCProviderForTest(
		auth.OIDCProviderConfig{
			ID:           "authelia",
			DisplayName:  "Corp Authelia",
			ClientID:     "kubecenter-mobile",
			ClientSecret: "must-not-leak",
		},
		"https://idp.example.com/oauth2/auth",
		[]string{"openid", "profile", "email"},
	))

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/oidc/authelia/mobile-config",
		nil,
	)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	// Capture body once — both the JSON decode below and the
	// clientSecret-leak assertion consume the same buffer.
	bodyStr := w.Body.String()

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, bodyStr)
	}

	var resp struct {
		Data struct {
			AuthorizationEndpoint string   `json:"authorizationEndpoint"`
			ClientID              string   `json:"clientId"`
			Scopes                []string `json:"scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(bodyStr), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got, want := resp.Data.AuthorizationEndpoint, "https://idp.example.com/oauth2/auth"; got != want {
		t.Errorf("authorizationEndpoint: got %q, want %q", got, want)
	}
	if got, want := resp.Data.ClientID, "kubecenter-mobile"; got != want {
		t.Errorf("clientId: got %q, want %q", got, want)
	}
	if got, want := strings.Join(resp.Data.Scopes, ","), "openid,profile,email"; got != want {
		t.Errorf("scopes: got %q, want %q", got, want)
	}

	// Secret must never appear in the response body.
	if strings.Contains(bodyStr, "must-not-leak") {
		t.Fatalf("clientSecret leaked into mobile-config response: %s", bodyStr)
	}

	// Wire-format guard — issue #282. The Go convention `clientID`
	// must NOT appear as a JSON key; only `clientId` is canonical.
	// Catches accidental reverts of the JSON tag.
	if strings.Contains(bodyStr, `"clientID"`) {
		t.Errorf("response carries legacy clientID JSON key (issue #282 regression): %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"clientId"`) {
		t.Errorf("response missing canonical clientId JSON key: %s", bodyStr)
	}
}
