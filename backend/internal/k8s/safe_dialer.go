package k8s

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// safeDialerTimeout is the connect deadline applied to every dial that
// goes through SafeDialContext. Matches the existing 30s window used by
// the cluster prober, monitoring discovery, and rest.Config defaults.
const safeDialerTimeout = 30 * time.Second

// safeDialerKeepAlive is the TCP keepalive window applied to dials. The
// http stdlib default is 30s; mirrored so the safe dialer doesn't quietly
// shorten reuse windows for proxy connections.
const safeDialerKeepAlive = 30 * time.Second

// SafeDialContext is a net.Dialer.DialContext-compatible function that
// re-resolves the address on every connection and rejects the dial when
// any candidate IP falls inside the SSRF block-list (loopback, RFC1918,
// link-local/metadata, CGNAT, unspecified).
//
// Phase 4 of the 2026-05-22 security audit (P2-6) wired this in front
// of every long-lived HTTP transport that operators can point at a
// user-supplied URL — cluster_router remote rest.Config, the Grafana
// reverse proxy, the Grafana API client, and the Prometheus API client.
// Catches the gap between validation-time DNS resolution and connect-
// time resolution (DNS rebinding) and protects against server-side
// redirect chains that try to pivot to internal addresses, since each
// new dial re-runs the IP check.
func SafeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %q: %w", address, err)
	}

	// Literal-IP fast path — no resolver round-trip needed.
	if ip := net.ParseIP(host); ip != nil {
		if err := checkIPNotPrivate(ip); err != nil {
			return nil, fmt.Errorf("SSRF dial refused: %w", err)
		}
		return baseDialer().DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	// Resolve host and validate every candidate IP before connecting.
	// Failing closed on resolution error matches ValidateRemoteURL's
	// posture — operators with broken DNS see a clear error rather than
	// a silent fallback.
	resolver := net.DefaultResolver
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("SSRF dial DNS lookup failed for %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("SSRF dial DNS returned no IPs for %s", host)
	}
	for _, ipAddr := range ips {
		if err := checkIPNotPrivate(ipAddr.IP); err != nil {
			return nil, fmt.Errorf("SSRF dial refused for %s: %w", host, err)
		}
	}

	// Pin to the first validated IP so what we just checked is exactly
	// what gets dialled. Without pinning, a racy resolver could return
	// a different (private) IP on the underlying Dial call, defeating
	// the check we just performed.
	pinned := net.JoinHostPort(ips[0].IP.String(), port)
	return baseDialer().DialContext(ctx, network, pinned)
}

// baseDialer returns the dialer shared by every SafeDialContext call.
// Kept as a function (not a package var) so each call gets its own
// timeout context and the stdlib doesn't share connection state across
// unrelated callers.
func baseDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   safeDialerTimeout,
		KeepAlive: safeDialerKeepAlive,
	}
}

// SafeHTTPTransport returns an *http.Transport pre-wired with
// SafeDialContext. Use for HTTP clients that talk to operator-supplied
// URLs (Grafana, Prometheus, monitoring proxies). The transport's
// other settings mirror http.DefaultTransport so connection pooling
// stays comparable.
func SafeHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           SafeDialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
