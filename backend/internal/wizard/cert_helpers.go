package wizard

import (
	"context"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// dnsNameRegex validates DNS names including wildcards (*.example.com).
// The leftmost label may be "*"; other labels must be RFC 1123.
var dnsNameRegex = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// dnsResolverTimeout bounds hostname lookups in validateHTTPSPublicURL.
const dnsResolverTimeout = 2 * time.Second

// validateEmailAddress returns true if addr parses as a bare RFC 5322 address
// (no display-name form) and is non-empty.
func validateEmailAddress(addr string) bool {
	if addr == "" {
		return false
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil {
		return false
	}
	return parsed.Address == strings.TrimSpace(addr)
}

// validateHTTPSPublicURL rejects non-HTTPS URLs and URLs that target non-public
// address space. When the host is a DNS name, it is resolved (with a short
// timeout); any resolved IP in non-public space rejects the URL. Resolution
// failures are ignored — this check is advisory, since cert-manager itself (not
// k8sCenter) fetches the URL.
func validateHTTPSPublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("must be a valid URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("must use https scheme")
	}

	host := u.Hostname()
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), dnsResolverTimeout)
		defer cancel()
		ips, _ = net.DefaultResolver.LookupIP(ctx, "ip", host)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("must not target a private, loopback, or non-public address")
		}
	}
	return nil
}

// isPublicIP returns true only for globally-routable unicast addresses.
// Rejects loopback, RFC1918, link-local, unspecified, multicast, and CGNAT (100.64.0.0/10).
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && (ip4[1]&0xC0) == 0x40 {
		return false // CGNAT 100.64.0.0/10
	}
	return true
}
