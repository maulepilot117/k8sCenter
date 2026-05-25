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
// any candidate IP falls inside the always-bad block-list (loopback,
// link-local/metadata, CGNAT, unspecified). RFC1918 IPs are ALLOWED —
// they're the normal home of in-cluster Kubernetes Service ClusterIPs.
//
// Use for HTTP transports that talk to URLs which may legitimately
// resolve to in-cluster RFC1918 ClusterIPs (the bundled monitoring
// subchart's Prometheus / Grafana, in-cluster Postgres, etc.). The
// audit's "block private/loopback" recommendation in P2-6 was framed
// for external-only endpoints; applying it to in-cluster auto-
// discovery would silently break the entire bundled-monitoring path
// because every ClusterIP falls inside 10.0.0.0/8 / 172.16.0.0/12 /
// 192.168.0.0/16. SafeDialContext still defends against the audit's
// real threats: DNS rebinding to localhost (loopback block) and IAM-
// credential theft via cloud metadata endpoints (link-local block).
//
// For URLs that should NEVER resolve to an internal address — remote
// cluster API servers, admin-UI-configured monitoring endpoints — use
// StrictDialContext, which adds RFC1918 to the block-list.
//
// Phase 4 of the 2026-05-22 security audit (P2-6 part 2). The split
// between Safe and Strict was added during review when reviewers
// flagged that the original blanket RFC1918 block would break in-
// cluster monitoring (correctness C1, testing TG-1, maintainability
// M-2 in the Phase 4 review).
func SafeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialContextWith(ctx, network, address, checkIPAlwaysBad)
}

// StrictDialContext mirrors SafeDialContext but additionally rejects
// RFC1918 candidates. Use for HTTP transports that target operator-
// supplied URLs which must terminate outside the cluster — primarily
// the remote cluster API server reached via cluster_router. A remote
// cluster URL that resolves to RFC1918 either was never legitimate or
// has been rebound mid-session, both of which warrant fail-closed.
func StrictDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialContextWith(ctx, network, address, checkIPNotPrivate)
}

// dialContextWith is the shared dial path, parameterised on the IP
// block-list. Same re-resolve / iterate / pin-to-validated-IP shape;
// the only difference between Safe and Strict is which IP ranges the
// check function rejects.
func dialContextWith(ctx context.Context, network, address string, check func(net.IP) error) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %q: %w", address, err)
	}

	// Literal-IP fast path — no resolver round-trip needed.
	if ip := net.ParseIP(host); ip != nil {
		if err := check(ip); err != nil {
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
		if err := check(ipAddr.IP); err != nil {
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
// SafeDialContext (allows in-cluster RFC1918, blocks loopback /
// link-local / metadata / CGNAT / unspecified). Use for HTTP clients
// that talk to URLs which may legitimately resolve to a cluster-
// internal Service ClusterIP — the bundled monitoring subchart's
// Prometheus / Grafana clients and the Grafana reverse proxy. The
// transport's other settings mirror http.DefaultTransport so
// connection pooling stays comparable.
func SafeHTTPTransport() *http.Transport {
	return transportWithDial(SafeDialContext)
}

// StrictHTTPTransport returns an *http.Transport pre-wired with
// StrictDialContext (also blocks RFC1918). Use for HTTP clients that
// talk to URLs which must resolve outside the cluster — currently
// only the remote cluster API server reached via cluster_router.
func StrictHTTPTransport() *http.Transport {
	return transportWithDial(StrictDialContext)
}

func transportWithDial(dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
