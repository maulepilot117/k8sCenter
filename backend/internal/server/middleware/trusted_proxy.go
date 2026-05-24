package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// TrustedProxy returns a middleware that rewrites r.RemoteAddr from
// forwarded headers (X-Real-IP / X-Forwarded-For) ONLY when the
// request's TCP peer is within one of the configured CIDR blocks.
// Otherwise the forwarded headers are ignored and r.RemoteAddr keeps
// the socket-peer address.
//
// This replaces chi-RealIP's blanket-trust behaviour, which honored
// the headers from every peer and let any direct LAN/internet attacker
// pin their rate-limit bucket to an arbitrary IP. Closes audit finding
// P2-1 (2026-05-22).
//
// Default fail-closed: an empty cidrs slice means no peer is trusted,
// and the middleware degenerates into a no-op rewriter (socket peer
// stays). That matches the audit's recommendation: "Trust forwarded
// headers only from configured proxy CIDRs. Otherwise key on socket
// peer address."
//
// Implementation notes:
//   - Parses the CIDR set once at construction; invalid entries are
//     logged via the supplied logger (or default slog) and dropped.
//     Construction never panics on bad input.
//   - Honors X-Real-IP first (single value), then X-Forwarded-For
//     leftmost (closest-to-client hop). Garbage values that don't
//     parse as a valid IP are dropped rather than installed —
//     downstream rate-limit buckets stay clean.
//   - Output r.RemoteAddr is the bare IP (no port), matching the
//     post-rewrite shape downstream extractIP already handles.
//   - IPv6 zone identifiers ([fe80::1%eth0]:port) are stripped from
//     the peer address before CIDR matching; net.ParseIP ignores the
//     zone and returns the unzoned address.
func TrustedProxy(cidrs []string, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	configuredNonEmpty := 0
	trusted := make([]*net.IPNet, 0, len(cidrs))
	for _, raw := range cidrs {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		configuredNonEmpty++
		_, n, err := net.ParseCIDR(entry)
		if err != nil {
			logger.Warn("trusted proxy CIDR rejected", "cidr", entry, "error", err)
			continue
		}
		// Loud warning on /0 catch-all CIDRs: they reinstate the
		// chi-RealIP-style "trust everyone" behaviour that Phase 3 set
		// out to remove. Surfaced by the adversarial review — an
		// operator who mis-copies an example template into production
		// (or expands the trust set to cover an entire pod CIDR) gets
		// a single startup line they can correlate against the actual
		// rate-limit-bucket-poisoning behaviour rather than silently
		// regressing P2-1.
		ones, bits := n.Mask.Size()
		if ones == 0 && bits > 0 {
			logger.Warn("trusted proxy CIDR is a catch-all (/0); this reinstates the pre-Phase-3 blanket-trust behaviour audit finding P2-1 was meant to remove. Narrow to your ingress controller's pod CIDR.",
				"cidr", entry)
		}
		trusted = append(trusted, n)
	}
	// If the operator configured CIDR entries but every one was
	// rejected, the middleware silently degenerates to a no-op (no
	// header rewrite, fail-closed). A single summary line surfaces the
	// fact that the deployment-intended forwarded-header behaviour is
	// not actually in effect, instead of relying on the operator to
	// correlate N individual per-entry warnings. Reliability review,
	// conf 85.
	if configuredNonEmpty > 0 && len(trusted) == 0 {
		logger.Warn("TrustedProxy: every configured CIDR entry was rejected; forwarded-header rewriting is disabled (fail-closed). Verify KUBECENTER_SERVER_TRUSTEDPROXYCIDRS values.",
			"configured", configuredNonEmpty)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			peer := socketPeerIP(r.RemoteAddr)
			if peer == nil || !ipInAny(peer, trusted) {
				next.ServeHTTP(w, r)
				return
			}
			if forwarded := pickForwardedIP(r); forwarded != "" {
				r.RemoteAddr = forwarded
			}
			next.ServeHTTP(w, r)
		})
	}
}

// socketPeerIP returns the parsed IP of a "host:port" RemoteAddr,
// stripping the IPv6 zone if present. Returns nil for unparseable
// input.
func socketPeerIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// Some test contexts pass a bare IP. Try parsing directly.
		host = remoteAddr
	}
	// Strip an IPv6 zone identifier (e.g. "fe80::1%eth0") — net.ParseIP
	// returns nil for zoned addresses but the matching semantics we want
	// are zone-agnostic.
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return net.ParseIP(host)
}

// pickForwardedIP returns the IP the trusted proxy is claiming this
// request originated from, or empty if no usable header is present.
// X-Real-IP takes precedence over X-Forwarded-For; the latter's
// leftmost entry is the closest-to-client hop.
func pickForwardedIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return ip.String()
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// Comma-separated list, leftmost is the client.
		head, _, _ := strings.Cut(v, ",")
		head = strings.TrimSpace(head)
		if ip := net.ParseIP(head); ip != nil {
			return ip.String()
		}
	}
	return ""
}

// ipInAny reports whether ip falls within any of the supplied networks.
func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
