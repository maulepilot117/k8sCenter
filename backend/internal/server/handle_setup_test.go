package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// validSetupBody is a minimal valid setup payload used across the loopback gate tests.
const validSetupBody = `{"username":"admin","password":"password1234"}`

// setupRequest builds a POST /api/v1/setup/init request with the given remote addr and body.
func setupRequest(remoteAddr, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	return req
}

// TestSetupLoopbackDevGate exercises the P1-1 loopback+dev gate for the case
// where Config.Auth.SetupToken is empty. The gate requires BOTH loopback peer
// AND Dev=true; either condition alone is insufficient.
func TestSetupLoopbackDevGate(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		dev        bool
		wantCode   int
	}{
		{
			// Non-loopback peer, Dev=false → 403 (no carve-out applies).
			name:       "non-loopback non-dev → 403",
			remoteAddr: "192.168.1.50:5432",
			dev:        false,
			wantCode:   http.StatusForbidden,
		},
		{
			// Non-loopback peer, Dev=true → 403 (Dev alone is not enough).
			name:       "non-loopback dev → 403",
			remoteAddr: "192.168.1.50:5432",
			dev:        true,
			wantCode:   http.StatusForbidden,
		},
		{
			// Loopback peer, Dev=false (production) → 403 (loopback alone is not enough).
			name:       "loopback non-dev → 403",
			remoteAddr: "127.0.0.1:54321",
			dev:        false,
			wantCode:   http.StatusForbidden,
		},
		{
			// Loopback peer, Dev=true → 201 (the carve-out for local dev setup).
			name:       "loopback dev → 201",
			remoteAddr: "127.0.0.1:54321",
			dev:        true,
			wantCode:   http.StatusCreated,
		},
		{
			// IPv6 loopback peer, Dev=true → 201.
			name:       "ipv6 loopback dev → 201",
			remoteAddr: "[::1]:54321",
			dev:        true,
			wantCode:   http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := testServer(t)
			srv.Config.Dev = tt.dev
			// Ensure no setup token is set so the loopback gate is exercised.
			srv.Config.Auth.SetupToken = ""

			req := setupRequest(tt.remoteAddr, validSetupBody)
			w := httptest.NewRecorder()
			srv.Router.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("remoteAddr=%q dev=%v: expected %d, got %d: %s",
					tt.remoteAddr, tt.dev, tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

// TestSetupConfiguredToken verifies that when a setup token IS configured, the
// constant-time comparison path applies and the loopback gate is bypassed.
func TestSetupConfiguredToken(t *testing.T) {
	tests := []struct {
		name       string
		bodyToken  string
		wantCode   int
	}{
		{
			name:      "correct token → 201",
			bodyToken: "my-secret-setup-token",
			wantCode:  http.StatusCreated,
		},
		{
			name:      "wrong token → 403",
			bodyToken: "wrong-token",
			wantCode:  http.StatusForbidden,
		},
		{
			name:      "missing token → 403",
			bodyToken: "",
			wantCode:  http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := testServer(t)
			srv.Config.Auth.SetupToken = "my-secret-setup-token"

			var bodyStr string
			if tt.bodyToken != "" {
				bodyStr = `{"username":"admin","password":"password1234","setupToken":"` + tt.bodyToken + `"}`
			} else {
				bodyStr = `{"username":"admin","password":"password1234"}`
			}

			// RemoteAddr is non-loopback to confirm the configured-token path
			// does not require a loopback peer.
			req := setupRequest("10.0.0.1:12345", bodyStr)
			w := httptest.NewRecorder()
			srv.Router.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("bodyToken=%q: expected %d, got %d: %s",
					tt.bodyToken, tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

// TestSetupLoopbackSpoofRejected is the P0 verification test for Finding #1+#8.
//
// It simulates an attacker who has a non-loopback TCP connection but injects
// "X-Forwarded-For: 127.0.0.1" to attempt to pass the loopback gate. The
// full production middleware chain (CaptureSocketPeer → chimw.RealIP → …) is
// exercised by routing through srv.Router.
//
// Expected result: 403 Forbidden, because CaptureSocketPeer stored the true
// TCP peer (203.0.113.10) before chimw.RealIP overwrote r.RemoteAddr, so the
// loopback check reads the ground-truth socket address, not the spoofed header.
func TestSetupLoopbackSpoofRejected(t *testing.T) {
	srv := testServer(t)
	srv.Config.Dev = true           // dev=true so the only gate is the loopback check
	srv.Config.Auth.SetupToken = "" // no token → loopback gate path

	// Build a request whose TCP peer is a public IP, but whose
	// X-Forwarded-For header claims loopback.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(validSetupBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "127.0.0.1") // attacker-controlled header
	req.RemoteAddr = "203.0.113.10:54321"           // real non-loopback TCP peer (TEST-NET-3)

	w := httptest.NewRecorder()
	// Route through the full chi middleware chain so CaptureSocketPeer and
	// chimw.RealIP both execute in their registered order.
	srv.Router.ServeHTTP(w, req)

	// chi's RealIP will have overwritten r.RemoteAddr to "127.0.0.1" by the
	// time handleSetupInit runs, but CaptureSocketPeer already stored
	// "203.0.113.10:54321" in the context. The loopback gate MUST reject.
	if w.Code != http.StatusForbidden {
		t.Fatalf("loopback spoof via X-Forwarded-For must be rejected with 403 Forbidden, got %d: %s",
			w.Code, w.Body.String())
	}
}
