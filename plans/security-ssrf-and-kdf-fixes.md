# fix: SSRF Protection for Auth Test Endpoints

## Overview

Security review confirmed a MEDIUM SSRF vulnerability: `handleTestOIDC` and `handleTestLDAP` accept user-controlled URLs with zero validation. The fix is to call the existing `ValidateRemoteURL` function (which already protects cluster registration) before making outbound connections.

## Problem Statement

`handleTestOIDC` (`handle_settings.go:23-55`) passes a user-supplied `issuerURL` directly to `auth.NewOIDCProvider()`, which fetches `{issuerURL}/.well-known/openid-configuration` — a blind SSRF. `handleTestLDAP` (`handle_settings.go:58-90`) passes a user-supplied `url` to `auth.NewLDAPProvider()` → `ldap.DialURL()` — TCP connection to arbitrary host:port.

Both endpoints are admin-only (`middleware.RequireAdmin`), but defense-in-depth requires URL validation regardless.

The existing `ValidateRemoteURL` in `cluster_router.go:221-254` already handles DNS resolution, private IP blocking (RFC 1918, loopback, link-local, CGNAT), and is battle-tested across 3 call sites. It does not enforce schemes, so it works for `https://`, `ldap://`, and `ldaps://` URLs out of the box.

## Proposed Solution

Add `ValidateRemoteURL` calls to both test handlers. Add scheme validation inline (OIDC requires `http`/`https`, LDAP requires `ldap`/`ldaps`). One file changed, ~15 lines added.

## Implementation

### Modify `backend/internal/server/handle_settings.go`

**In `handleTestOIDC` (after line ~33, after `issuerURL != ""` check):**

```go
// Validate scheme
u, err := url.Parse(req.IssuerURL)
if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
    writeError(w, http.StatusBadRequest, "Issuer URL must use https:// or http://")
    return
}
// Block private/internal IPs (DNS resolution + CGNAT/loopback/link-local check)
if err := k8s.ValidateRemoteURL(req.IssuerURL); err != nil {
    writeError(w, http.StatusBadRequest, "Invalid issuer URL: "+err.Error())
    return
}
```

**In `handleTestLDAP` (after line ~66, after `url != ""` check):**

```go
// Validate scheme
u, err := url.Parse(req.URL)
if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") {
    writeError(w, http.StatusBadRequest, "LDAP URL must use ldap:// or ldaps://")
    return
}
// Block private/internal IPs
if err := k8s.ValidateRemoteURL(req.URL); err != nil {
    writeError(w, http.StatusBadRequest, "Invalid LDAP URL: "+err.Error())
    return
}
```

**Add import for `k8s` package** (if not already imported) and `net/url`.

### What We Are NOT Doing (and why)

| Deferred Item | Reason |
|---|---|
| New `internal/ssrf` package | `ValidateRemoteURL` already exists and works. One function used in 5 places doesn't need a package. |
| `net.Dialer.Control` callback (DNS rebinding defense) | Pre-flight DNS resolution is the same pattern used for the more sensitive cluster credential path. Admin-only endpoint. |
| Injecting safe dialer into `ldap.go`/`oidc.go` | Would break production LDAP/OIDC providers on private networks (the common case). The threat is test URLs, not stored configs. |
| Consolidating `validateSettingsURL` into shared function | `validateSettingsURL` intentionally skips DNS resolution because monitoring URLs are often internal. Different threat model. |
| HKDF key derivation migration | Security review marked as false positive. SHA-256 on high-entropy env var is equivalent. Version-tagged ciphertext migration has real risks (nonce collision, permanent dual code path). |

## Acceptance Criteria

- [ ] `handleTestOIDC` rejects `issuerURL` pointing to private/loopback/CGNAT IPs with a clear error
- [ ] `handleTestLDAP` rejects `url` pointing to private/loopback/CGNAT IPs with a clear error
- [ ] OIDC test enforces `https://` or `http://` scheme
- [ ] LDAP test enforces `ldap://` or `ldaps://` scheme
- [ ] Existing functionality unaffected (production OIDC/LDAP connections, cluster registration, monitoring URL settings)
- [ ] `go vet ./...` passes
- [ ] `go test ./...` passes

## Files Changed

| File | Action |
|---|---|
| `backend/internal/server/handle_settings.go` | MODIFY — add URL validation to both test handlers |

## References

- Security review finding: SSRF via OIDC test endpoint (`handle_settings.go:22-55`), confidence 8/10
- Existing protection pattern: `ValidateRemoteURL` (`cluster_router.go:221-254`)
- Plan review feedback: DHH, Pattern Recognition, Simplicity reviewers — unanimous on minimal fix
