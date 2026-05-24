package k8s

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSafeDialContext_LiteralPrivateIPRefused exercises the literal-IP
// fast path: a private/loopback/link-local destination must be refused
// before any TCP connection is attempted, so the IP block-list applies
// even when the caller bypasses DNS by passing a literal address.
func TestSafeDialContext_LiteralPrivateIPRefused(t *testing.T) {
	cases := []struct {
		name    string
		address string
		needle  string
	}{
		{"loopback IPv4", "127.0.0.1:1234", "loopback"},
		{"loopback IPv6", "[::1]:1234", "loopback"},
		{"RFC1918", "10.0.0.5:443", "private"},
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
