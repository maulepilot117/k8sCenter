package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleTestLDAPPlaintextGate exercises the P3-1 mirror gate on
// the admin LDAP test endpoint. The gate at handle_settings.go:103
// must refuse `ldap://` URLs that do not opt in via startTLS or
// insecurePlaintext, matching the startup config validator in
// config.validate(). Audit finding P3-1 (2026-05-22) + Phase 7
// ce-code-review T-1.
func TestHandleTestLDAPPlaintextGate(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	cases := []struct {
		name           string
		body           string
		wantStatus     int
		wantBodyHas    string
		wantBodyHasAny []string
	}{
		{
			name:        "ldap:// without flags is rejected at the gate",
			body:        `{"url":"ldap://ldap.example.com:389","bindDN":"cn=svc","bindPassword":"x"}`,
			wantStatus:  http.StatusBadRequest,
			wantBodyHas: "plaintext",
		},
		{
			name:        "uppercase LDAP:// is rejected like lowercase",
			body:        `{"url":"LDAP://ldap.example.com:389","bindDN":"cn=svc","bindPassword":"x"}`,
			wantStatus:  http.StatusBadRequest,
			wantBodyHas: "plaintext",
		},
		{
			// startTLS=true clears the gate; the request then proceeds
			// to the SSRF + dial path which will reject the
			// unreachable test host. Either way it is no longer 400 at
			// the plaintext gate, which is the contract under test.
			name:           "ldap:// + startTLS clears the plaintext gate",
			body:           `{"url":"ldap://ldap.example.com:389","bindDN":"cn=svc","bindPassword":"x","startTLS":true}`,
			wantStatus:     http.StatusBadRequest,
			wantBodyHasAny: []string{"connection test failed", "invalid LDAP URL"},
		},
		{
			// insecurePlaintext=true clears the gate via the explicit
			// operator opt-in. Same downstream-failure caveat as above.
			name:           "ldap:// + insecurePlaintext clears the plaintext gate",
			body:           `{"url":"ldap://ldap.example.com:389","bindDN":"cn=svc","bindPassword":"x","insecurePlaintext":true}`,
			wantStatus:     http.StatusBadRequest,
			wantBodyHasAny: []string{"connection test failed", "invalid LDAP URL"},
		},
		{
			// ldaps:// is the documented happy path; the gate never
			// trips. Downstream dial still fails against a non-existent
			// host, but the response no longer mentions plaintext.
			name:           "ldaps:// is never plaintext-gated",
			body:           `{"url":"ldaps://ldap.example.com:636","bindDN":"cn=svc","bindPassword":"x"}`,
			wantStatus:     http.StatusBadRequest,
			wantBodyHasAny: []string{"connection test failed", "invalid LDAP URL"},
		},
		{
			name:        "invalid scheme returns the scheme error not the gate",
			body:        `{"url":"http://ldap.example.com:389","bindDN":"cn=svc","bindPassword":"x"}`,
			wantStatus:  http.StatusBadRequest,
			wantBodyHas: "LDAP URL must use ldap:// or ldaps://",
		},
		{
			name:        "empty URL returns the URL-required error not the gate",
			body:        `{"bindDN":"cn=svc","bindPassword":"x"}`,
			wantStatus:  http.StatusBadRequest,
			wantBodyHas: "url is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/auth/test-ldap", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			w := httptest.NewRecorder()
			srv.Router.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status: want %d, got %d; body=%s", tc.wantStatus, w.Code, w.Body.String())
			}
			body := w.Body.String()
			if tc.wantBodyHas != "" && !strings.Contains(body, tc.wantBodyHas) {
				t.Fatalf("body missing %q: %s", tc.wantBodyHas, body)
			}
			if len(tc.wantBodyHasAny) > 0 {
				found := false
				for _, s := range tc.wantBodyHasAny {
					if strings.Contains(body, s) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("body missing any of %v: %s", tc.wantBodyHasAny, body)
				}
			}
			// Defensive: a gate-rejection must not leak the bind
			// password back to the caller. Pre-existing contract;
			// pin it here so a future refactor does not regress it.
			if strings.Contains(body, `"x"`) {
				t.Fatalf("response leaked bindPassword: %s", body)
			}
		})
	}
}
