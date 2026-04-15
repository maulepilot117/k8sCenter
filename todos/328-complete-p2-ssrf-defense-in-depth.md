---
name: Harden ACME/Vault URL validation (CGNAT, multicast, hostname resolution)
status: complete
priority: p2
issue_id: 328
tags: [code-review, security, backend, ssrf, pr-180]
dependencies: []
---

## Problem Statement

`backend/internal/wizard/issuer.go:202-217` (`validateHTTPSPublicURL`) only parses the URL and checks the IP literal. Gaps:

1. **Hostnames not resolved.** `internal.attacker.tld` → A 10.0.0.1 passes the check.
2. **CGNAT range 100.64.0.0/10** not covered by Go's `net.IP.IsPrivate()` (EKS secondary ranges, etc.).
3. **Multicast (224.0.0.0/4)** not checked.

This is defense-in-depth: k8sCenter does not fetch the URL — cert-manager does. So direct SSRF against our backend is not the threat. The threat is an attacker-configured Issuer pointing at an internal service, exfiltrating ACME account key material.

## Findings

- `backend/internal/wizard/issuer.go:202-217`
- Security review F1 (security-sentinel, P2)
- Verified: IPv4-mapped (`::ffff:10.0.0.1`), IPv6 ULA (`fc00::/7`), link-local (`fe80::/10`) ARE correctly rejected by existing code. IPv6 coverage is fine.

## Proposed Solutions

### Option A — layered hardening (recommended)
```go
func validateHTTPSPublicURL(raw string) error {
    u, err := url.Parse(raw)
    if err != nil || u.Host == "" { return fmt.Errorf("must be a valid URL") }
    if u.Scheme != "https" { return fmt.Errorf("must use https scheme") }

    host := u.Hostname()
    var ips []net.IP
    if ip := net.ParseIP(host); ip != nil {
        ips = []net.IP{ip}
    } else {
        // short-timeout resolution; fall back to allowing the hostname if DNS fails
        // (we want this to be advisory, not hard-dependent on the resolver)
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        ips, _ = net.DefaultResolver.LookupIP(ctx, "ip", host)
    }
    for _, ip := range ips {
        if !isPublicIP(ip) {
            return fmt.Errorf("must not target a private or loopback address")
        }
    }
    return nil
}

func isPublicIP(ip net.IP) bool {
    if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
        ip.IsUnspecified() || ip.IsMulticast() { return false }
    // CGNAT 100.64.0.0/10
    if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xC0 == 64 { return false }
    return true
}
```

Add a code comment explaining this is advisory (cert-manager, not k8sCenter, fetches the URL).

### Option B — accept the gap
Document the limitation in release notes and move on. cert-manager is the actual SSRF vector; k8sCenter can't be the last line of defense.

## Recommended Action
<!-- Option A, with the comment. DNS resolution should be non-failing advisory. -->

## Acceptance Criteria
- [ ] CGNAT `100.64.0.0/10` rejected.
- [ ] Multicast `224.0.0.0/4` rejected.
- [ ] Hostname that resolves only to private IPs rejected.
- [ ] DNS timeout does not cause validation to fail open aggressively (advisory behavior documented).
- [ ] Unit tests for each class.

## Work Log
- 2026-04-14: Filed from PR #180 review.

## Resources
- PR #180
- security-sentinel review F1
