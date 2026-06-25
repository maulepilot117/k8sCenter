package k8s

import (
	"context"
	"net"
	"testing"
)

// FuzzCheckIPNotPrivate fuzzes the pure SSRF IP-classification core.
// Oracle: every address Go's stdlib classifies as loopback, private
// (RFC1918), link-local (incl. 169.254.169.254 cloud metadata), or
// unspecified MUST be rejected. A regression that lets any of these
// through is an SSRF hole. (CGNAT 100.64.0.0/10 is also rejected by
// the implementation but uses a package-internal net; the stdlib
// predicates below are the load-bearing security classes.)
func FuzzCheckIPNotPrivate(f *testing.F) {
	// 4-byte (IPv4) and 16-byte (IPv6) seeds spanning each blocked class.
	f.Add([]byte{127, 0, 0, 1})       // loopback
	f.Add([]byte{10, 0, 0, 1})        // private
	f.Add([]byte{192, 168, 1, 1})     // private
	f.Add([]byte{169, 254, 169, 254}) // link-local / metadata (teeth)
	f.Add([]byte{0, 0, 0, 0})         // unspecified
	f.Add([]byte{8, 8, 8, 8})         // public (should pass)
	f.Add(make([]byte, 16))           // IPv6 unspecified
	f.Add([]byte{1, 2, 3})            // malformed length

	f.Fuzz(func(t *testing.T, raw []byte) {
		// Only 4- and 16-byte slices form a valid net.IP; others yield
		// an unusable IP and are not a meaningful SSRF input.
		if len(raw) != 4 && len(raw) != 16 {
			return
		}
		ip := net.IP(raw)
		err := checkIPNotPrivate(ip)

		mustReject := ip.IsLoopback() ||
			ip.IsPrivate() ||
			ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified()

		if mustReject && err == nil {
			t.Fatalf("SSRF hole: checkIPNotPrivate accepted blocked address %s", ip)
		}
	})
}

// FuzzValidateRemoteURLContext is crash-safety only. A pre-cancelled
// context guarantees the DNS path (LookupIPAddr) returns immediately
// without real network I/O, so the fuzzer exercises only URL parsing
// and the IP-literal branch. Oracle: never panics.
func FuzzValidateRemoteURLContext(f *testing.F) {
	f.Add("https://10.0.0.1:6443")
	f.Add("https://169.254.169.254:6443")
	f.Add("https://example.com")
	f.Add("not-a-url")
	f.Add("")
	f.Add("https://[::1]:6443")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: hostname lookups fail fast, no real DNS

	f.Fuzz(func(t *testing.T, rawURL string) {
		_ = ValidateRemoteURLContext(ctx, rawURL) // must not panic
	})
}
