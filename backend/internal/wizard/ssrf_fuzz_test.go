package wizard

import (
	"net"
	"testing"
)

// cgnatWizardNet is the CGNAT block (RFC 6598, 100.64.0.0/10) mirrored from the
// inline guard in isPublicIP so the oracle detects removal of that guard.
var cgnatWizardNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// FuzzIsPublicIP fuzzes the pure SSRF IP-classification core of validateHTTPSPublicURL.
// Oracle (inverted from FuzzCheckIPNotPrivate): for any address the Go stdlib classifies
// as loopback, private (RFC1918), link-local (unicast or multicast), multicast, unspecified,
// or CGNAT (100.64.0.0/10), isPublicIP MUST return false. A regression that lets any of
// these through is an SSRF hole. The must-reject set is a strict subset of what isPublicIP
// actually claims to block — no false positives.
func FuzzIsPublicIP(f *testing.F) {
	// 4-byte (IPv4) and 16-byte (IPv6) seeds spanning each blocked class.
	f.Add([]byte{127, 0, 0, 1})       // loopback
	f.Add([]byte{10, 0, 0, 1})        // private (RFC1918 10/8)
	f.Add([]byte{192, 168, 1, 1})     // private (RFC1918 192.168/16)
	f.Add([]byte{169, 254, 169, 254}) // link-local unicast / cloud metadata (teeth)
	f.Add([]byte{100, 64, 0, 1})      // CGNAT (teeth — no stdlib predicate covers this)
	f.Add([]byte{0, 0, 0, 0})         // unspecified
	f.Add([]byte{224, 0, 0, 1})       // multicast (224.0.0.0/4)
	f.Add([]byte{8, 8, 8, 8})         // public (should return true)
	f.Add(make([]byte, 16))           // IPv6 unspecified (::)
	f.Add([]byte{1, 2, 3})            // malformed length — must be skipped

	f.Fuzz(func(t *testing.T, raw []byte) {
		// Only 4- and 16-byte slices form a valid net.IP; others are not a
		// meaningful SSRF input and are skipped.
		if len(raw) != 4 && len(raw) != 16 {
			return
		}
		ip := net.IP(raw)

		// mustBeNonPublic mirrors the exact set of classes isPublicIP blocks.
		// Every predicate here corresponds to a guard in isPublicIP's body.
		mustBeNonPublic := ip.IsLoopback() ||
			ip.IsPrivate() ||
			ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() ||
			ip.IsMulticast() ||
			cgnatWizardNet.Contains(ip)

		if mustBeNonPublic && isPublicIP(ip) {
			t.Fatalf("SSRF hole: isPublicIP returned true (public) for blocked address %s", ip)
		}
	})
}
