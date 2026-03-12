# Security Policy

## Reporting Vulnerabilities

If you discover a security vulnerability in KubeCenter, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, email security concerns to the maintainers privately. Include:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We will acknowledge receipt within 48 hours and provide an initial assessment within 7 days.

## Supported Versions

| Version | Supported |
|---|---|
| 0.x (development) | Yes |

## Security Model

### Authentication

KubeCenter supports three authentication providers:

- **Local accounts** with Argon2id password hashing
- **OIDC** (OpenID Connect) for SSO integration
- **LDAP** for directory-based authentication

Access tokens (JWT, 15-minute lifetime) are stored in memory only. Refresh tokens (7-day lifetime) are issued as `httpOnly; Secure; SameSite=Strict` cookies and rotated on each use. CSRF protection is enforced via `X-Requested-With` header validation on all state-changing endpoints.

### Authorization

All user-initiated Kubernetes API calls go through **user impersonation**. KubeCenter never uses its own service account permissions for user actions. This means Kubernetes RBAC is enforced server-side — users can only see and modify resources they have permission for in the cluster.

The KubeCenter service account requires:
- `impersonate` permissions for users, groups, and service accounts
- `get`, `list`, `watch` on resources needed for informer caches
- `create` on `selfsubjectaccessreviews` for RBAC filtering

### Secrets Handling

- Kubernetes Secrets are **never cached** in the informer. They are fetched on-demand using the impersonated client, ensuring the requesting user has permission.
- Secret values are **masked** (`****`) in all API responses. A separate reveal endpoint requires explicit user action and is audit-logged.
- Secret values are never written to audit logs.

### Container Security

KubeCenter containers follow the Kubernetes **restricted** Pod Security Standard:

- Run as non-root (UID 65534)
- Read-only root filesystem
- No privilege escalation
- All capabilities dropped
- Seccomp profile: RuntimeDefault
- Distroless base image (no shell)

### Network Security

- The Helm chart deploys NetworkPolicy resources restricting pod traffic to only what KubeCenter requires
- TLS is supported between all components (backend, frontend, Prometheus, Grafana)
- Content Security Policy headers restrict script sources

### Audit Logging

All write operations (create, update, delete) and secret reveal actions are logged with:

- Timestamp
- User identity
- Source IP
- Resource type, name, and namespace
- Action performed
- Result (success/failure)

### Rate Limiting

Authentication endpoints are rate-limited to 5 attempts per minute per IP address to prevent brute-force attacks.

## Security Checklist

The following security controls are enforced during development:

- [ ] All API endpoints require authentication (except health probes and login)
- [ ] All user-initiated k8s operations use impersonation
- [ ] Secret values are masked in API responses and audit logs
- [ ] CSRF protection on all state-changing endpoints
- [ ] Rate limiting on auth endpoints
- [ ] Input validation on all API inputs
- [ ] Containers run as non-root with read-only filesystem
- [ ] No shell in production container images
- [ ] NetworkPolicy deployed by default
- [ ] TLS between all components
- [ ] JWT secrets generated at install time
- [ ] ClusterRole uses explicit resource lists (no wildcards)
- [ ] Audit log captures all write operations and secret accesses
- [ ] CSP headers prevent XSS
- [ ] WebSocket connections authenticated with JWT

## Dependencies

We monitor dependencies for known vulnerabilities using GitHub Dependabot and address critical CVEs within 7 days of disclosure.
