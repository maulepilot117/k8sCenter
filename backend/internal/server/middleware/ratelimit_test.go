package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
)

// recordingAuditLogger captures audit entries for assertion in tests.
// Mirrors the helper in internal/server/handle_oidc_mobile_test.go; kept
// local to the middleware package to avoid an import cycle.
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

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter()

	for i := 0; i < 5; i++ {
		allowed, _ := rl.Check("192.168.1.1")
		if !allowed {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter()

	// Use up the limit
	for i := 0; i < 5; i++ {
		rl.Check("192.168.1.1")
	}

	// 6th request should be blocked
	allowed, _ := rl.Check("192.168.1.1")
	if allowed {
		t.Error("6th request should be rate limited")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := NewRateLimiter()

	// Exhaust IP 1
	for i := 0; i < 5; i++ {
		rl.Check("192.168.1.1")
	}
	allowed, _ := rl.Check("192.168.1.1")
	if allowed {
		t.Error("IP 1 should be rate limited")
	}

	// IP 2 should still be allowed
	allowed, _ = rl.Check("192.168.1.2")
	if !allowed {
		t.Error("IP 2 should not be rate limited")
	}
}

func TestRateLimiter_RetryAfter(t *testing.T) {
	rl := NewRateLimiter()

	// Exhaust the limit
	for i := 0; i < 6; i++ {
		rl.Check("192.168.1.1")
	}

	_, retryAfter := rl.Check("192.168.1.1")
	if retryAfter <= 0 || retryAfter > 61 {
		t.Errorf("expected retry after between 1 and 61 seconds, got %d", retryAfter)
	}
}

func TestRateLimiter_RetryAfter_UnknownIP(t *testing.T) {
	rl := NewRateLimiter()

	allowed, retryAfter := rl.Check("unknown-ip")
	if !allowed {
		t.Error("unknown IP should be allowed")
	}
	if retryAfter != 0 {
		t.Errorf("expected 0 retry after for unknown IP, got %d", retryAfter)
	}
}

// TestRateLimiter_CheckKey_NamespaceIsolation pins the design contract
// that backs audit finding P2-1 part 2 (2026-05-22): IP throttle keys
// and account throttle keys must NOT collide. The convention is bare
// IPs for IP throttles, "<purpose>:<account>" for account throttles —
// this test asserts the underlying bucket map keeps them separate.
func TestRateLimiter_CheckKey_NamespaceIsolation(t *testing.T) {
	rl := NewRateLimiterWithRate(2, time.Minute)

	// Exhaust the IP bucket for 192.168.1.1.
	for i := 0; i < 2; i++ {
		if allowed, _ := rl.CheckKey("192.168.1.1"); !allowed {
			t.Fatalf("setup: request %d should be allowed", i+1)
		}
	}
	if allowed, _ := rl.CheckKey("192.168.1.1"); allowed {
		t.Fatal("IP bucket should be exhausted")
	}

	// Account-throttle key for the same numeric string must be
	// independent — different namespace, different bucket.
	if allowed, _ := rl.CheckKey("login:192.168.1.1"); !allowed {
		t.Fatal("account bucket 'login:192.168.1.1' must be independent of IP bucket")
	}
	// And another namespace should be independent again.
	if allowed, _ := rl.CheckKey("setup:192.168.1.1"); !allowed {
		t.Fatal("account bucket 'setup:192.168.1.1' must be independent of 'login:' bucket")
	}
}

// TestRateLimiter_CheckKey_BackCompat — Check(ip) and CheckKey(ip)
// share the bucket so the existing IP-throttle middleware (which still
// calls Check) and the new account-throttle handler code (which calls
// CheckKey) interact correctly when wiring overlaps.
func TestRateLimiter_CheckKey_BackCompat(t *testing.T) {
	rl := NewRateLimiterWithRate(2, time.Minute)

	// CheckKey via the new name eats 1 token.
	if allowed, _ := rl.CheckKey("10.0.0.1"); !allowed {
		t.Fatal("first CheckKey call should be allowed")
	}
	// Check via the legacy alias eats the second token.
	if allowed, _ := rl.Check("10.0.0.1"); !allowed {
		t.Fatal("second Check call should be allowed")
	}
	// Third should be blocked regardless of which alias is used.
	if allowed, _ := rl.Check("10.0.0.1"); allowed {
		t.Fatal("third call should be rate limited (Check)")
	}
	if allowed, _ := rl.CheckKey("10.0.0.1"); allowed {
		t.Fatal("third call should still be rate limited via CheckKey alias")
	}
}

func TestRateLimiterWithRate_CustomLimit(t *testing.T) {
	rl := NewRateLimiterWithRate(30, time.Minute)

	// All 30 requests should be allowed
	for i := 0; i < 30; i++ {
		allowed, _ := rl.Check("192.168.1.1")
		if !allowed {
			t.Errorf("request %d should be allowed with rate 30", i+1)
		}
	}

	// 31st request should be blocked
	allowed, retryAfter := rl.Check("192.168.1.1")
	if allowed {
		t.Error("31st request should be rate limited with rate 30")
	}
	if retryAfter <= 0 || retryAfter > 61 {
		t.Errorf("expected retry after between 1 and 61 seconds, got %d", retryAfter)
	}
}

// serveDirect invokes the middleware-wrapped handler against a
// synthetic request without going through a real TCP listener. This is
// the only way to control r.RemoteAddr — once an http.Client sends to a
// real net.Listener, the server side sees the actual TCP peer
// (127.0.0.1 on loopback), and the field is read-only from the server's
// perspective. Direct invocation against an httptest.ResponseRecorder
// preserves the manually-set RemoteAddr.
func serveDirect(handler http.Handler, method, path, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// TestRateLimit_AuditLogsOn429 — issue #276 regression. When the bucket
// is exhausted, the middleware must emit one audit entry per rejected
// request so brute-force probing is visible in the audit table.
func TestRateLimit_AuditLogsOn429(t *testing.T) {
	rec := &recordingAuditLogger{}
	rl := NewRateLimiterWithRate(2, time.Minute)
	rl.SetAuditLogger(rec)

	handler := RateLimit(rl)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const probeAddr = "203.0.113.42:54321"
	// First two requests pass.
	for i := 0; i < 2; i++ {
		w := serveDirect(handler, http.MethodPost, "/auth/login", probeAddr)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}
	// Third + fourth are rate-limited; each should produce one audit entry.
	for i := 0; i < 2; i++ {
		w := serveDirect(handler, http.MethodPost, "/auth/login", probeAddr)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("probe %d: expected 429, got %d", i+1, w.Code)
		}
	}

	got := rec.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 audit entries (one per 429), got %d: %+v", len(got), got)
	}
	for i, e := range got {
		if e.Action != audit.ActionRateLimited {
			t.Errorf("entry[%d]: Action = %q, want %q", i, e.Action, audit.ActionRateLimited)
		}
		if e.Result != audit.ResultDenied {
			t.Errorf("entry[%d]: Result = %q, want %q", i, e.Result, audit.ResultDenied)
		}
		if e.SourceIP != "203.0.113.42" {
			t.Errorf("entry[%d]: SourceIP = %q, want %q", i, e.SourceIP, "203.0.113.42")
		}
		// Detail carries the route so investigators can filter by path.
		if e.Detail == "" {
			t.Errorf("entry[%d]: Detail is empty; expected route + retry info", i)
		}
	}
}

// TestRateLimit_NilAuditLoggerSilent — without SetAuditLogger called,
// the middleware preserves the pre-#276 silent behavior. Existing
// callsites that don't wire an audit logger must not crash.
func TestRateLimit_NilAuditLoggerSilent(t *testing.T) {
	rl := NewRateLimiterWithRate(1, time.Minute)
	// No SetAuditLogger — auditLogger is nil.

	handler := RateLimit(rl)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 3; i++ {
		w := serveDirect(handler, http.MethodGet, "/", "198.51.100.1:1234")
		_ = w // exit value not asserted; goal is "did not panic"
	}
}

// TestRateLimit_AuditLoggerNotInvokedOn200 — audit entries fire ONLY on
// 429. Successful requests must not pollute the audit table.
func TestRateLimit_AuditLoggerNotInvokedOn200(t *testing.T) {
	rec := &recordingAuditLogger{}
	rl := NewRateLimiterWithRate(5, time.Minute)
	rl.SetAuditLogger(rec)

	handler := RateLimit(rl)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		w := serveDirect(handler, http.MethodPost, "/auth/login", "192.0.2.1:5555")
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: unexpected status %d", i+1, w.Code)
		}
	}

	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("expected 0 audit entries for under-limit traffic, got %d: %+v", len(got), got)
	}
}
