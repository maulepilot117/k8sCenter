package k8s

import (
	"strings"
	"testing"
)

// TestValidateRemoteURL_LiteralIPs covers the IP-only fast path. These
// cases must work without DNS — checkIPNotPrivate runs directly when
// the URL host parses as a literal IP.
func TestValidateRemoteURL_LiteralIPs(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		wantErrSub string // empty → expect no error
	}{
		{"public IPv4 v4 host", "https://1.1.1.1:6443", ""},
		{"public IPv4 in v6 brackets", "https://[2606:4700:4700::1111]:6443", ""},
		{"loopback IPv4", "https://127.0.0.1:6443", "loopback"},
		{"loopback IPv6", "https://[::1]:6443", "loopback"},
		{"RFC1918 10.0.0.0/8", "https://10.1.2.3:6443", "private"},
		{"RFC1918 172.16.0.0/12", "https://172.16.5.5:6443", "private"},
		{"RFC1918 192.168.0.0/16", "https://192.168.1.1:6443", "private"},
		{"link-local 169.254.0.0/16 (metadata)", "https://169.254.169.254:6443", "link-local"},
		{"IPv6 link-local fe80::/10", "https://[fe80::1]:6443", "link-local"},
		{"CGNAT 100.64.0.0/10", "https://100.64.0.1:6443", "CGNAT"},
		{"unspecified 0.0.0.0", "https://0.0.0.0:6443", "unspecified"},
		{"unspecified ::", "https://[::]:6443", "unspecified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRemoteURL(tc.url)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

// TestValidateRemoteURL_DNSFailureFailsClosed covers the P2-6 hardening:
// previous implementation allowed connections through on lookup failure.
// Now lookup failure is a fatal validation error.
func TestValidateRemoteURL_DNSFailureFailsClosed(t *testing.T) {
	// Use a deliberately invalid TLD that no resolver can answer.
	err := ValidateRemoteURL("https://this-host-cannot-exist.invalid:6443")
	if err == nil {
		t.Fatal("expected DNS failure to fail closed, got nil error")
	}
	if !strings.Contains(err.Error(), "DNS") {
		t.Fatalf("expected error to mention DNS, got: %v", err)
	}
}

// TestValidateRemoteURL_MalformedInputs covers parse-stage rejections.
func TestValidateRemoteURL_MalformedInputs(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"unparseable", "://not a url"},
		{"empty hostname", "https://"},
		{"scheme only", "https:///path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRemoteURL(tc.url); err == nil {
				t.Fatalf("expected error for input %q, got nil", tc.url)
			}
		})
	}
}
