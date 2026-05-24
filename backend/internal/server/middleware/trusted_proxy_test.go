package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// silentLogger returns a slog.Logger that discards output for tests.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// echoRemoteAddr is a tiny handler that records the post-middleware
// r.RemoteAddr into the response body so the test can assert what the
// downstream chain would see.
func echoRemoteAddr(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(r.RemoteAddr))
}

// runWith executes the middleware against a single request and returns
// the body the test handler observed.
func runWith(mw func(http.Handler) http.Handler, req *http.Request) string {
	h := mw(http.HandlerFunc(echoRemoteAddr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Body.String()
}

// TestTrustedProxy_EmptyCIDR_NeverRewrites verifies the audit-finding
// fail-closed default: zero configured CIDRs means NO peer is trusted,
// so X-Forwarded-For / X-Real-IP are ignored regardless of source.
func TestTrustedProxy_EmptyCIDR_NeverRewrites(t *testing.T) {
	mw := TrustedProxy(nil, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:54321" // arbitrary public peer
	req.Header.Set("X-Forwarded-For", "10.0.0.99")
	req.Header.Set("X-Real-IP", "10.0.0.99")

	if got := runWith(mw, req); got != "203.0.113.5:54321" {
		t.Fatalf("empty CIDR list must leave RemoteAddr untouched, got %q", got)
	}
}

// TestTrustedProxy_TrustedPeer_HonorsHeader is the happy path: a peer
// inside the trusted CIDR can rewrite r.RemoteAddr.
func TestTrustedProxy_TrustedPeer_HonorsHeader(t *testing.T) {
	mw := TrustedProxy([]string{"10.0.0.0/8"}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:443" // inside trusted CIDR
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := runWith(mw, req); got != "198.51.100.7" {
		t.Fatalf("trusted peer header should rewrite RemoteAddr, got %q", got)
	}
}

// TestTrustedProxy_UntrustedPeer_IgnoresHeader pins the security
// boundary: a peer OUTSIDE the trusted CIDR cannot pin the rewrite
// target, no matter what header they send.
func TestTrustedProxy_UntrustedPeer_IgnoresHeader(t *testing.T) {
	mw := TrustedProxy([]string{"10.0.0.0/8"}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:54321" // outside trusted CIDR
	req.Header.Set("X-Forwarded-For", "10.0.0.99")
	req.Header.Set("X-Real-IP", "10.0.0.99")

	if got := runWith(mw, req); got != "203.0.113.5:54321" {
		t.Fatalf("untrusted peer header must be ignored, got %q", got)
	}
}

// TestTrustedProxy_XRealIP_BeatsXFF asserts X-Real-IP wins when both
// headers are present — matches chi-RealIP precedence so downstream
// rate-limit semantics are stable.
func TestTrustedProxy_XRealIP_BeatsXFF(t *testing.T) {
	mw := TrustedProxy([]string{"10.0.0.0/8"}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:443"
	req.Header.Set("X-Real-IP", "198.51.100.7")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := runWith(mw, req); got != "198.51.100.7" {
		t.Fatalf("X-Real-IP should win over X-Forwarded-For, got %q", got)
	}
}

// TestTrustedProxy_XFF_LeftmostHop covers the multi-hop case: a load
// balancer + reverse proxy chain produces "client, proxy1, proxy2".
// The leftmost entry is the closest-to-client hop and the one we want.
func TestTrustedProxy_XFF_LeftmostHop(t *testing.T) {
	mw := TrustedProxy([]string{"10.0.0.0/8"}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.10.10.10, 10.20.20.20")

	if got := runWith(mw, req); got != "198.51.100.7" {
		t.Fatalf("leftmost X-Forwarded-For entry must win, got %q", got)
	}
}

// TestTrustedProxy_MalformedHeader_LeavesRemoteAddr verifies garbage in
// the header from a trusted peer is dropped, not installed. Without
// this guard, downstream rate-limit buckets would be keyed on arbitrary
// non-IP strings.
func TestTrustedProxy_MalformedHeader_LeavesRemoteAddr(t *testing.T) {
	mw := TrustedProxy([]string{"10.0.0.0/8"}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:443"
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	req.Header.Set("X-Real-IP", "also-not-an-ip")

	if got := runWith(mw, req); got != "10.1.2.3:443" {
		t.Fatalf("malformed forwarded header must be dropped, got %q", got)
	}
}

// TestTrustedProxy_BadCIDR_DroppedSilently confirms the constructor is
// tolerant of misconfiguration — an unparseable entry doesn't kill the
// process, it just doesn't get added to the trust set. (The dropped
// entry IS logged via the supplied slog.Logger.)
func TestTrustedProxy_BadCIDR_DroppedSilently(t *testing.T) {
	mw := TrustedProxy([]string{"definitely-not-a-cidr", "10.0.0.0/8", ""}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := runWith(mw, req); got != "198.51.100.7" {
		t.Fatalf("valid CIDR among invalid ones must still take effect, got %q", got)
	}
}

// TestTrustedProxy_IPv6 confirms IPv6 CIDRs work for both peer and
// forwarded-header. The bracketed-port form for IPv6 RemoteAddr
// ([::1]:port) is the standard net/http shape.
func TestTrustedProxy_IPv6(t *testing.T) {
	mw := TrustedProxy([]string{"2001:db8::/32"}, silentLogger())

	t.Run("trusted v6 peer rewrites", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "[2001:db8::1]:443"
		req.Header.Set("X-Forwarded-For", "2001:db8:cafe::42")

		if got := runWith(mw, req); got != "2001:db8:cafe::42" {
			t.Fatalf("trusted v6 peer should rewrite, got %q", got)
		}
	})

	t.Run("untrusted v6 peer ignored", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "[2001:db9::1]:443" // outside 2001:db8::/32
		req.Header.Set("X-Forwarded-For", "2001:db8:cafe::42")

		if got := runWith(mw, req); got != "[2001:db9::1]:443" {
			t.Fatalf("untrusted v6 peer header must be ignored, got %q", got)
		}
	})

	t.Run("zoned v6 peer parses", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "[2001:db8::1%eth0]:443"
		req.Header.Set("X-Forwarded-For", "2001:db8:cafe::42")

		if got := runWith(mw, req); got != "2001:db8:cafe::42" {
			t.Fatalf("zoned v6 peer should match trusted CIDR, got %q", got)
		}
	})
}

// TestTrustedProxy_MixedV4V6CIDRs validates that v4 and v6 CIDRs
// coexist in the same trusted set — a deployment behind both an IPv4
// and IPv6 load balancer needs both to work.
func TestTrustedProxy_MixedV4V6CIDRs(t *testing.T) {
	mw := TrustedProxy([]string{"10.0.0.0/8", "2001:db8::/32"}, silentLogger())

	t.Run("v4 hop", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.1.2.3:443"
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		if got := runWith(mw, req); got != "198.51.100.7" {
			t.Fatalf("v4 trusted peer should rewrite, got %q", got)
		}
	})
	t.Run("v6 hop", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "[2001:db8::1]:443"
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		if got := runWith(mw, req); got != "198.51.100.7" {
			t.Fatalf("v6 trusted peer rewriting to v4 forwarded should work, got %q", got)
		}
	})
}

// TestTrustedProxy_IPv4MappedV6 covers the IPv4-mapped-IPv6 corner
// case: net.ParseIP("::ffff:127.0.0.1") returns the 4-byte form. A v4
// CIDR like 127.0.0.0/8 must Contains() it; a v6-only CIDR will not.
// This pins the behaviour so a refactor doesn't silently break
// loopback handling on dual-stack stacks.
func TestTrustedProxy_IPv4MappedV6(t *testing.T) {
	mw := TrustedProxy([]string{"127.0.0.0/8"}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::ffff:127.0.0.1]:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := runWith(mw, req); got != "198.51.100.7" {
		t.Fatalf("v4-mapped-v6 loopback should match 127.0.0.0/8, got %q", got)
	}
}
