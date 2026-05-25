package k8s

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSafeDialContext_LiteralBadIPRefused exercises the literal-IP
// fast path on the always-bad ranges. SafeDialContext (unlike
// StrictDialContext) deliberately allows RFC1918 so it doesn't break
// in-cluster Service ClusterIPs, so RFC1918 is NOT in this list.
func TestSafeDialContext_LiteralBadIPRefused(t *testing.T) {
	cases := []struct {
		name    string
		address string
		needle  string
	}{
		{"loopback IPv4", "127.0.0.1:1234", "loopback"},
		{"loopback IPv6", "[::1]:1234", "loopback"},
		{"link-local / metadata", "169.254.169.254:80", "link-local"},
		{"CGNAT", "100.64.1.1:443", "CGNAT"},
		{"unspecified", "0.0.0.0:443", "unspecified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := SafeDialContext(ctx, "tcp", tc.address)
			if err == nil {
				t.Fatalf("expected dial refusal containing %q, got nil", tc.needle)
			}
			if !strings.Contains(err.Error(), "SSRF") {
				t.Fatalf("expected error to mention SSRF, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.needle) {
				t.Fatalf("expected error to mention %q, got: %v", tc.needle, err)
			}
		})
	}
}

// TestStrictDialContext_LiteralPrivateIPRefused covers the strict
// variant — same always-bad ranges as Safe PLUS RFC1918.
func TestStrictDialContext_LiteralPrivateIPRefused(t *testing.T) {
	cases := []struct {
		name    string
		address string
		needle  string
	}{
		{"loopback IPv4", "127.0.0.1:1234", "loopback"},
		{"loopback IPv6", "[::1]:1234", "loopback"},
		{"RFC1918", "10.0.0.5:443", "private"},
		{"RFC1918 192.168", "192.168.5.5:443", "private"},
		{"link-local / metadata", "169.254.169.254:80", "link-local"},
		{"CGNAT", "100.64.1.1:443", "CGNAT"},
		{"unspecified", "0.0.0.0:443", "unspecified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := StrictDialContext(ctx, "tcp", tc.address)
			if err == nil {
				t.Fatalf("expected dial refusal containing %q, got nil", tc.needle)
			}
			if !strings.Contains(err.Error(), "SSRF") {
				t.Fatalf("expected error to mention SSRF, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.needle) {
				t.Fatalf("expected error to mention %q, got: %v", tc.needle, err)
			}
		})
	}
}

// TestSafeDialContext_AllowsRFC1918LiteralIP confirms the Safe variant
// permits RFC1918 destinations (in-cluster Service ClusterIPs). The
// dial will fail at the TCP layer because no service is listening,
// but it must NOT fail at the SSRF check. A regression that hoisted
// RFC1918 into checkIPAlwaysBad would surface here as an "SSRF dial
// refused: ... private" error before the TCP attempt.
func TestSafeDialContext_AllowsRFC1918LiteralIP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	// Use a port that's unlikely to be open on any RFC1918 host; the
	// expected outcome is a dial timeout / connection refusal at the
	// network layer, not an SSRF block.
	_, err := SafeDialContext(ctx, "tcp", "10.255.255.254:1")
	if err == nil {
		// Surprising — something accepted the connection. Test still
		// passes because the SSRF check didn't fire.
		return
	}
	if strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("SafeDialContext incorrectly refused RFC1918 destination as SSRF: %v", err)
	}
	// Any other error (network unreachable, timeout, refused) is fine.
}

// TestSafeDialContext_DNSFailureFailsClosed verifies the DNS-resolution
// path matches ValidateRemoteURL's posture: a lookup failure surfaces as
// an explicit dial refusal instead of silently allowing the connection.
func TestSafeDialContext_DNSFailureFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := SafeDialContext(ctx, "tcp", "this-host-cannot-exist.invalid:443")
	if err == nil {
		t.Fatal("expected DNS failure to fail closed, got nil error")
	}
	if !strings.Contains(err.Error(), "DNS") {
		t.Fatalf("expected error to mention DNS, got: %v", err)
	}
}

// TestSafeDialContext_MalformedAddress covers the SplitHostPort failure
// branch — defensive coverage for callers that hand the dialer a value
// the stdlib transport would otherwise accept and then quietly fail on.
func TestSafeDialContext_MalformedAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, err := SafeDialContext(ctx, "tcp", "not-a-valid-address")
	if err == nil {
		t.Fatal("expected malformed-address rejection, got nil")
	}
	if !strings.Contains(err.Error(), "invalid dial address") {
		t.Fatalf("expected error to mention invalid dial address, got: %v", err)
	}
}

// TestSafeHTTPTransport_NotNil is a tiny smoke test that the constructor
// returns a usable transport object with the DialContext hook set — the
// callers that use SafeHTTPTransport directly rely on this being wired.
func TestSafeHTTPTransport_NotNil(t *testing.T) {
	tr := SafeHTTPTransport()
	if tr == nil {
		t.Fatal("SafeHTTPTransport returned nil")
	}
	if tr.DialContext == nil {
		t.Fatal("SafeHTTPTransport returned transport with nil DialContext — dial-time SSRF check would be bypassed")
	}
}
