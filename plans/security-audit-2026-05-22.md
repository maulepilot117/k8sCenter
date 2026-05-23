# Security Audit Report - 2026-05-22

## Scope

This report documents a read-only security audit of the k8sCenter codebase. The audit covered the current working tree, git history, dependency advisories, backend/API authorization, Kubernetes-facing operations, frontend proxy surfaces, mobile application security, deployment manifests, CI/CD, and supply-chain defaults.

Threat model:

- LAN-accessible deployments.
- Internet-exposed deployments.
- Mobile clients connecting to a remote backend.
- Attacker goals include account takeover, Kubernetes privilege escalation, secret disclosure, SSRF, reconnaissance, supply-chain compromise, and token/session theft.

Audit methods:

- Focused read-only subagent review across backend auth/RBAC, Kubernetes operation paths, frontend/proxy surfaces, mobile app, and deployment/supply-chain surfaces.
- Local source review and high-risk pattern searches.
- Git history secret scan with Gitleaks.
- Dependency advisory checks with OSV, govulncheck, npm audit, and Flutter package metadata.

No repository files were changed during the audit itself. A temporary redacted Gitleaks report was written outside the repo at `C:\tmp\k8scenter-gitleaks.json`.

## Executive Summary

The highest-risk issues are concentrated around bootstrap/setup safety, deployable secrets, Kubernetes service-account blast radius, Grafana/Prometheus exposure, and mobile release hardening.

The most severe chain is:

1. A fresh or homelab-derived deployment is reachable from LAN/internet.
2. An attacker claims setup or forges a JWT using committed homelab values.
3. The backend accepts admin-equivalent claims and impersonates privileged Kubernetes identities.
4. Broad service-account permissions and management endpoints enable cluster takeover or secret disclosure.

Other important chains include token theft over plaintext transport followed by refresh-token race amplification, and low-privileged user reconnaissance through PromQL/CRD/diagnostics leaks followed by Grafana proxy misuse.

## P0 Findings

### P0-1: Deployable Homelab Secrets Allow Full Takeover If Used or Copied

Affected files:

- `helm/kubecenter/values-homelab.yaml:10`
- `helm/kubecenter/values-homelab.yaml:30`
- `helm/kubecenter/values-homelab.yaml:31`
- `helm/kubecenter/values-homelab.yaml:34`
- `CLAUDE.md:280`
- `backend/internal/auth/jwt.go:43`
- `backend/internal/server/middleware/auth.go:30`
- `backend/internal/k8s/client.go:143`

Evidence:

- Homelab values set `dev: true`, a fixed `jwtSecret`, a fixed `setupToken`, and expose services as `LoadBalancer`.
- Project guidance documents `admin / admin123` and the setup token.
- JWT claims drive roles and Kubernetes identity; middleware trusts signed claims; Kubernetes clients impersonate token-derived username/groups.

Preconditions:

- Operator deploys or copies `values-homelab.yaml`.
- Backend is reachable from LAN or internet.

Impact:

- Attacker can forge HS256 JWTs with `roles: ["admin"]` and privileged Kubernetes identity/groups.
- If setup is incomplete, the fixed setup token can create the first admin.
- This can lead to full app and cluster-management takeover.

Recommended fix:

- Remove committed deployable secrets and default credentials.
- Rotate any deployed homelab/default values.
- Require generated or externally supplied secrets.
- Make Helm rendering fail on known placeholder/homelab secrets.
- Do not expose `dev: true` deployments beyond localhost.

Exploit chain:

Exposed LoadBalancer -> forge JWT with committed secret -> backend accepts admin role -> backend impersonates privileged Kubernetes user/group -> cluster-admin actions through app endpoints.

## P1 Findings

### P1-1: Fresh Exposed Deployments Can Have the First Admin Claimed When Setup Token Is Unset

Affected files:

- `backend/internal/server/routes.go:72`
- `backend/internal/server/handle_setup.go:41`
- `backend/internal/server/handle_setup.go:78`
- `backend/internal/server/handle_setup.go:90`
- `helm/kubecenter/values.yaml:82`

Evidence:

- `POST /api/v1/setup/init` is public and only rate-limited.
- Setup token validation runs only when `Config.Auth.SetupToken != ""`.
- Helm defaults `auth.setupToken` to empty.
- Successful setup creates the first user with `[]string{"admin"}`.

Preconditions:

- Fresh deployment with zero users.
- Backend reachable from LAN/internet.
- No setup token configured.

Impact:

- A network attacker can claim the initial admin account and control app/cluster-management surfaces.

Recommended fix:

- Require a high-entropy setup token in every non-dev deployment.
- Generate and persist a setup token at install time when none is supplied.
- Bind setup to localhost until claimed, or fail closed on non-loopback listeners without a token.

Exploit chain:

Poll `/api/v1/setup/status` until `needsSetup:true` -> POST `/api/v1/setup/init` with attacker credentials -> log in as admin -> configure auth, monitoring, proxy, and Kubernetes-management settings.

### P1-2: Backend Service Account Has Dangerous Cluster-Wide Impersonation and Secret-Read Power

Affected files:

- `helm/kubecenter/templates/clusterrole.yaml:8`
- `helm/kubecenter/templates/clusterrole.yaml:10`
- `helm/kubecenter/templates/clusterrole.yaml:11`
- `helm/kubecenter/templates/clusterrole.yaml:182`
- `helm/kubecenter/templates/clusterrole.yaml:184`
- `helm/kubecenter/templates/clusterrole.yaml:192`
- `helm/kubecenter/templates/clusterrolebinding.yaml:1`

Evidence:

- The app service account can impersonate all users, groups, and service accounts.
- It can `get` and `list` all Kubernetes Secrets.
- It can `list` all resources across all API groups for CRD instance counts.

Preconditions:

- Backend pod, image, dependency, or service-account token is compromised.
- Or an attacker reaches an SSRF/RCE path that can call the Kubernetes API.

Impact:

- Direct cluster-wide secret enumeration.
- Broad reconnaissance.
- Likely privilege escalation by impersonating `system:masters` or other privileged identities.

Recommended fix:

- Remove wildcard user/group/service-account impersonation.
- Use `resourceNames` and explicit allowed identities where impersonation is required.
- Split service accounts by feature.
- Make secret listing opt-in and namespace-scoped.
- Replace wildcard CRD listing with explicit resource sets.

Exploit chain:

Backend pod compromise -> read service-account token -> call Kubernetes API with privileged impersonation headers -> create privileged resources or exfiltrate secrets.

### P1-3: Authenticated Non-Admins Can Use Grafana Proxy With the Backend Service Token

Affected files:

- `backend/internal/server/routes.go:328`
- `backend/internal/server/routes.go:340`
- `backend/internal/monitoring/handler.go:181`
- `backend/internal/monitoring/handler.go:216`
- `backend/internal/monitoring/discovery.go:353`
- `backend/internal/monitoring/discovery.go:366`
- `backend/internal/monitoring/grafana.go:41`

Evidence:

- Monitoring routes are mounted under the authenticated group, not admin-only.
- `/grafana/proxy/*` is registered with `HandleFunc`, so methods are not limited to GET/HEAD.
- Allowed prefixes include Grafana API paths such as dashboards, folders, and search.
- Reverse proxy injects `Authorization: Bearer <GrafanaToken>`.
- The token is also used for dashboard/folder provisioning.

Preconditions:

- Any valid k8sCenter account.
- Grafana integration configured.
- Grafana token present with dashboard/folder permissions.

Impact:

- Privilege escalation from ordinary k8sCenter user to Grafana service-account capabilities.
- Dashboard/folder modification and operational data discovery.

Recommended fix:

- Make browser-reachable Grafana proxy GET/HEAD-only.
- Require admin for Grafana API paths.
- Do not inject provisioning tokens into browser-reachable proxy traffic.
- Use a separate least-privilege viewer token for UI proxying.

Exploit chain:

Low-privileged user POSTs through `/api/v1/monitoring/grafana/proxy/api/dashboards/db` -> backend injects service token -> attacker persists malicious or misleading dashboard.

### P1-4: Mobile Release Builds Can Ship Without a Real HTTPS Backend and Fall Back to Cleartext Localhost

Affected files:

- `mobile/lib/api/dio_client.dart:25`
- `mobile/lib/api/dio_client.dart:28`
- `mobile/lib/api/websocket_client.dart:165`
- `.github/workflows/mobile-ci.yml:367`
- `.github/workflows/mobile-ci.yml:379`

Evidence:

- `BACKEND_URL` defaults to `http://localhost:8080`.
- Dio uses that value for API calls.
- WebSocket URL derives `ws://` from non-HTTPS base URLs.
- Android CI release build passes `UNIVERSAL_LINK_HOST`, but not `BACKEND_URL`.
- Release docs show builds without `BACKEND_URL`.

Preconditions:

- CI/manual release build omits `BACKEND_URL`.
- Or a release is configured with an `http://` backend.

Impact:

- Credentials, refresh tokens, bearer tokens, and WebSocket auth can be sent to localhost or cleartext transport.
- A local malicious service on device loopback can impersonate the backend for a misbuilt release.

Recommended fix:

- Make `BACKEND_URL` mandatory for release builds.
- Reject localhost, loopback, and non-HTTPS values in release.
- Refuse `ws://` outside debug/test.

Exploit chain:

Misbuilt release -> attacker-controlled local service on `127.0.0.1:8080` -> capture login or refresh flow -> account/session compromise.

### P1-5: Android Release Signing Uses Debug Signing

Affected files:

- `mobile/android/app/build.gradle.kts:50`
- `mobile/android/app/build.gradle.kts:54`
- `mobile/docs/RELEASE.md:83`

Evidence:

- `release { signingConfig = signingConfigs.getByName("debug") }`.
- Release docs state CI currently signs release AABs with Gradle debug signing.

Preconditions:

- Debug-signed release is distributed through internal testing, promotion, or sideload.
- Attacker can obtain or reproduce the signing key used for that channel.

Impact:

- Malicious update signed with the same certificate can run as `io.kubecenter.kubecenter`.
- App identity and access to app-private state/token storage are at risk.

Recommended fix:

- Remove debug signing from release.
- Require upload-keystore secrets before Play deploy.
- Fail CI deploy if release signing material is absent.

Exploit chain:

Debug-signed build distributed -> signing key exposure -> malicious app update -> refresh token and local state exfiltration -> remote backend access.

## P2 Findings

### P2-1: Login and Setup Rate Limits Can Be Bypassed With Spoofed Forwarding Headers

Affected files:

- `backend/internal/server/server.go:311`
- `backend/internal/server/middleware/ratelimit.go:118`
- `backend/internal/server/middleware/ratelimit.go:119`
- `backend/internal/server/routes.go:52`
- `backend/internal/server/routes.go:73`

Evidence:

- Global `RealIP` middleware rewrites `RemoteAddr` from `X-Real-IP` / `X-Forwarded-For`.
- Rate limiting keys off `r.RemoteAddr`.
- No trusted proxy boundary is enforced.

Preconditions:

- Attacker can connect directly or through a proxy that does not strip forwarding headers.

Impact:

- Credential stuffing, password guessing, refresh abuse, OIDC initiation abuse, and setup probing are not effectively limited.

Recommended fix:

- Trust forwarded headers only from configured proxy CIDRs.
- Otherwise key on socket peer address.
- Add per-account/login-name throttles for auth endpoints.

### P2-2: Refresh-Token Rotation Race Can Mint Multiple Valid Successor Sessions

Affected files:

- `backend/internal/auth/session.go:98`
- `backend/internal/auth/session.go:130`
- `backend/internal/server/handle_auth.go:118`
- `backend/internal/server/handle_auth.go:154`
- `backend/internal/server/handle_auth.go:170`

Evidence:

- Refresh uses `Peek()` before minting a new token pair, then consumes the old token after minting.
- `session.go` documents that two callers can `Peek` the same token.
- If later `Consume` returns `ErrSessionNotFound`, the handler logs but still succeeds.

Preconditions:

- Attacker has a refresh token and can race the legitimate client.
- Or two refresh requests are sent concurrently.

Impact:

- Replay detection fails.
- One stolen refresh token can become multiple independent valid refresh sessions.

Recommended fix:

- Atomically consume or mark refresh tokens as rotating before issuing successors.
- If consumption fails, return 401 and revoke newly created successor.
- Add concurrent refresh regression tests.

### P2-3: LDAP Users Can Retain Revoked Identity or Groups Through Cached Refresh Sessions

Affected files:

- `backend/internal/server/handle_auth.go:129`
- `backend/internal/auth/jwt.go:36`
- `backend/internal/auth/ldap.go:293`

Evidence:

- Non-local users, including LDAP users, can be cached into refresh sessions.
- Refresh reuses cached user data rather than revalidating LDAP.
- LDAP refresh lifetime falls through to the default 7 days.

Preconditions:

- LDAP user logs in once.
- User is then disabled, removed, or loses group membership while refresh token remains valid.

Impact:

- User can continue receiving access tokens with stale Kubernetes groups/roles for up to 7 days.

Recommended fix:

- Revalidate LDAP identity and groups on refresh.
- Consider shorter LDAP refresh lifetime.
- Avoid caching LDAP authorization state across refresh.

### P2-4: Arbitrary PromQL Endpoints Bypass Namespace RBAC and Load Bounds

Affected files:

- `backend/internal/server/routes.go:331`
- `backend/internal/server/routes.go:334`
- `backend/internal/server/routes.go:335`
- `backend/internal/monitoring/handler.go:45`
- `backend/internal/monitoring/handler.go:65`
- `backend/internal/monitoring/handler.go:89`
- `backend/internal/monitoring/handler.go:115`

Evidence:

- Authenticated users can submit arbitrary PromQL to `/monitoring/query` and `/monitoring/query_range`.
- No namespace/resource authorization filter is applied before forwarding to Prometheus.
- Route comment says no rate limit is applied.

Preconditions:

- Valid account.
- Prometheus integration configured with cluster-wide metrics.

Impact:

- Users can enumerate namespaces, pods, nodes, workloads, labels, and operational metadata outside Kubernetes RBAC scope.
- Broad range queries can consume backend/Prometheus resources.

Recommended fix:

- Require admin for arbitrary PromQL.
- Expose allowlisted server-owned queries for non-admins.
- Enforce namespace/resource checks server-side.
- Cap range, step, samples, and request rate.

### P2-5: Multi-Cluster Target Confusion Causes Local-Cluster Operations While Remote Cluster Is Selected

Affected files:

- `backend/internal/server/routes.go:89`
- `backend/internal/yaml/handler.go:49`
- `backend/internal/yaml/handler.go:124`
- `backend/internal/yaml/handler.go:185`
- `backend/internal/yaml/handler.go:236`
- `backend/internal/k8s/resources/pods.go:182`
- `backend/internal/server/handle_ws_logs.go:136`
- `backend/internal/certmanager/handler.go:97`
- `backend/internal/certmanager/handler.go:728`
- `backend/internal/networking/handler.go:213`

Evidence:

- `ClusterContext` is applied inside `/api/v1`, but several Kubernetes-facing handlers bypass `ClusterRouter`.
- YAML, pod exec, WebSocket pod logs, cert-manager, and CNI update paths use local clients/base config directly.

Preconditions:

- Authenticated user with app admin role for non-local `X-Cluster-ID`.
- Local Kubernetes RBAC for the attempted operation.

Impact:

- Requests that appear targeted at a remote cluster can apply YAML, exec into pods, read logs, renew/reissue certificates, delete certificate backing secrets, or update Cilium config in the local cluster.

Recommended fix:

- Route every Kubernetes-facing handler through `ClusterRouter` using `middleware.ClusterIDFromContext`.
- Or explicitly reject non-local clusters before any Kubernetes read/write.

### P2-6: Remote Cluster and Monitoring URL SSRF Defenses Allow DNS Failure/Rebinding Windows

Affected files:

- `backend/internal/k8s/cluster_router.go:170`
- `backend/internal/k8s/cluster_router.go:221`
- `backend/internal/k8s/cluster_router.go:233`
- `backend/internal/k8s/cluster_router.go:236`
- `backend/internal/server/handle_settings.go:124`
- `backend/internal/server/handle_settings.go:139`
- `backend/internal/server/handle_settings.go:180`
- `backend/internal/monitoring/discovery.go:172`

Evidence:

- Remote cluster validator allows DNS lookup failure and only blocks known-private IPs when resolution returned IPs.
- Monitoring settings validation blocks private/loopback only when the host is a literal IP.
- Hostnames are not resolved for monitoring URLs.

Preconditions:

- Attacker has admin/settings access or can influence monitoring/cluster URL configuration.
- Attacker controls DNS or uses a rebinding hostname.

Impact:

- Backend can be induced to make server-side requests to internal addresses.
- Internal network probing and credential/token exposure may be possible.

Recommended fix:

- Fail closed on DNS errors.
- Reuse DNS-resolving validation for monitoring URLs.
- Resolve and dial pinned IPs through a custom `DialContext` that rechecks every connection and redirect target against private/link-local/loopback/metadata ranges.

### P2-7: Backend NetworkPolicy Egress Allows All Destinations on Sensitive Ports

Affected file:

- `helm/kubecenter/templates/networkpolicy.yaml:39`

Evidence:

- Backend egress is restricted by port only, without destination selectors/CIDRs.
- Allowed ports include DNS, HTTP, HTTPS, PostgreSQL, Kubernetes API, Prometheus, and Grafana-style ports.

Preconditions:

- NetworkPolicy is enabled and enforced.
- Attacker obtains backend code execution, SSRF, or a malicious backend image runs.

Impact:

- Data exfiltration over 443.
- Possible metadata access over 80.
- Lateral movement to internal services on allowed ports.

Recommended fix:

- Add destination restrictions for kube-dns, Kubernetes API, PostgreSQL service, and configured observability endpoints.
- Explicitly block metadata/link-local destinations where supported.

### P2-8: Chart Permits Plaintext Exposure of Authenticated App Traffic

Affected files:

- `helm/kubecenter/values.yaml:62`
- `helm/kubecenter/values.yaml:72`
- `helm/kubecenter/templates/ingress.yaml:17`
- `helm/kubecenter/values-homelab.yaml:34`
- `backend/internal/server/server.go:324`
- `backend/cmd/kubecenter/main.go:861`
- `backend/internal/server/response.go:31`
- `backend/internal/server/handle_auth.go:316`

Evidence:

- Ingress is disabled by default and `tls: []`.
- Homelab values expose services as `LoadBalancer` with ingress disabled.
- Backend serves plain HTTP.
- In dev mode, cookies are not `Secure`.

Preconditions:

- Operator exposes ingress without TLS.
- Or homelab LoadBalancer is reachable on LAN/internet.

Impact:

- Access tokens, refresh tokens, setup tokens, and API traffic can traverse plaintext.

Recommended fix:

- Require TLS for exposed deployments unless an explicit unsafe override is set.
- Keep backend service internal-only by default.
- Require `dev:false` for exposed deployments.

### P2-9: FCM Device Registrations Are Not Revoked on Logout

Affected files:

- `mobile/lib/main.dart:59`
- `mobile/lib/notifications/fcm_registration.dart:73`
- `mobile/lib/auth/auth_repository.dart:126`
- `mobile/lib/features/settings/settings_screen.dart:13`

Evidence:

- Mobile registers FCM tokens after authentication and token refresh.
- Logout only calls `/auth/logout`, clears access state, and deletes the refresh token.
- Settings text states FCM revoke is deferred.
- Backend has `DELETE /notifications/devices/{id}`, but mobile does not call it.

Preconditions:

- User enabled FCM, signed in, then signs out or loses/transfers the device.

Impact:

- Signed-out device can keep receiving notifications for the old account.
- Push content may expose cluster alert metadata.

Recommended fix:

- Before clearing auth on logout, fetch registered devices and delete the current FCM token's device record.
- Dispose token-refresh listeners.
- Then clear local credentials.

### P2-10: Sentry Request URL Scrubbing Preserves Raw URLs

Affected files:

- `mobile/lib/observability/pii_scrubber.dart:116`
- `mobile/lib/observability/pii_scrubber.dart:120`
- `mobile/lib/observability/sentry_init.dart:57`

Evidence:

- `beforeSend` uses `scrubEvent`, but request scrubbing copies `origRequest.url` unchanged while clearing body/query fields.
- Breadcrumb URL scrubbing separately strips query/fragment and scrubs resource-like path segments.

Preconditions:

- Sentry is opted in and configured.
- Event includes request context.

Impact:

- Full request URLs can leak Kubernetes namespaces, resource names, or query secrets to Sentry.

Recommended fix:

- Strip query and fragment from `SentryRequest.url`.
- Run request paths through the same scrubber used for breadcrumbs.
- Add regression tests for request URL scrubbing.

### P2-11: Reachable Go Dependency Advisories Affect Runtime Paths

Affected files:

- `backend/go.mod:3`
- `backend/go.mod:85`
- `backend/internal/monitoring/handler.go:216`
- `backend/internal/wizard/cert_helpers.go:27`
- `backend/internal/notifications/service.go:626`
- `backend/internal/alerting/notifier.go:154`

Evidence:

- `govulncheck` found reachable advisories:
  - `GO-2026-5026` in `golang.org/x/net/idna`.
  - `GO-2026-4986`, `GO-2026-4977` in `net/mail`.
  - `GO-2026-4982`, `GO-2026-4980` in `html/template`.
  - `GO-2026-4976` in `net/http/httputil`.
  - `GO-2026-4971` in `net`.
  - `GO-2026-4918` in HTTP/2 transport / `golang.org/x/net`.
- Example traces include Grafana reverse proxy, email address parsing, and HTML email template rendering.

Preconditions:

- Vulnerable Go toolchain/modules remain in use.
- Inputs reach affected runtime paths.

Impact:

- Potential denial of service, proxy filtering bypass edge cases, and XSS/escaping issues in generated HTML email contexts.

Recommended fix:

- Upgrade build/runtime to Go `1.26.3` or later.
- Upgrade `golang.org/x/net` to at least `0.55.0`.
- Re-run `govulncheck ./...`.

## P3 Findings

### P3-1: LDAP Plaintext Bind Is Allowed in Non-Dev Configurations

Affected file:

- `backend/internal/auth/ldap.go:54`

Evidence:

- `ldap://` without StartTLS logs a warning but does not fail closed.

Impact:

- Network attackers can capture LDAP service-account or user bind credentials.

Recommended fix:

- Reject plaintext LDAP unless an explicit insecure override is set.
- Default production deployments to `ldaps://` or StartTLS with certificate validation.

### P3-2: CRD Discovery and Instance Counts Leak Cluster-Wide Operator Inventory

Affected files:

- `backend/internal/k8s/resources/crd_handler.go:32`
- `backend/internal/k8s/resources/crd_handler.go:93`
- `backend/internal/k8s/crd_discovery.go:125`
- `backend/internal/k8s/crd_discovery.go:181`

Impact:

- Authenticated users can enumerate installed CRDs/operators and approximate instance counts, including External Secrets, cert-manager, GitOps, backup, service mesh, and security scanners.

Recommended fix:

- Filter CRDs/counts by per-user SSAR/list on each GVR.
- Or make inventory/count endpoints admin-only.

### P3-3: Diagnostics Can Disclose Pod/ReplicaSet Details Without Pod/ReplicaSet RBAC

Affected files:

- `backend/internal/diagnostics/handler.go:79`
- `backend/internal/diagnostics/handler.go:98`
- `backend/internal/diagnostics/diagnostics.go:179`
- `backend/internal/diagnostics/diagnostics.go:262`

Impact:

- Users with access to a target kind, such as Deployments or Services, may learn related pod names, owner relationships, restart/failure states, and image-pull details despite lacking pod or ReplicaSet permissions.

Recommended fix:

- Enforce RBAC for every related resource type before resolving it.
- Omit checks/links that depend on resources the user cannot list/get.

### P3-4: CRD Update Route Can Audit a Different Name Than the Object Actually Updated

Affected files:

- `backend/internal/k8s/resources/crd_handler.go:241`
- `backend/internal/k8s/resources/crd_handler.go:248`
- `backend/internal/k8s/resources/crd_handler.go:261`
- `backend/internal/k8s/resources/crd_handler.go:268`

Impact:

- Request URL can name `foo` while body `metadata.name` updates `bar`; audit records the URL name, weakening forensics.

Recommended fix:

- Reject body `metadata.name`/`metadata.namespace` mismatches.
- Or overwrite them from URL params before calling `Update`.
- Audit the returned object's actual name.

### P3-5: Loki Label-Name Endpoint Returns Unscoped Global Labels

Affected files:

- `backend/internal/loki/handler.go:129`
- `backend/internal/loki/handler.go:137`
- `backend/internal/loki/handler.go:145`

Impact:

- Tenant can learn global logging schema and workload conventions across namespaces.

Recommended fix:

- Derive labels from a scoped series/query endpoint.
- Or make `/logs/labels` admin-only when Loki cannot scope label-name enumeration.

### P3-6: Mobile OIDC Error Callbacks Clear Pending PKCE Before Validating State

Affected files:

- `mobile/lib/auth/oidc_controller.dart:271`
- `mobile/lib/auth/universal_link_listener.dart:94`

Impact:

- Attacker who can cause the app to receive a callback with `error=...` can clear pending verifier/state before the legitimate IdP callback, causing targeted login denial of service.

Recommended fix:

- Require and validate `state` for error callbacks before clearing pending flow.
- Accept only `https` for OIDC universal-link callbacks.

### P3-7: Refresh Token Secure-Storage Options Are Less Explicit Than Pending OIDC Storage

Affected files:

- `mobile/lib/auth/secure_storage.dart:26`
- `mobile/lib/auth/pending_oidc_store.dart:100`

Impact:

- Refresh tokens receive less deliberate storage protection than short-lived OIDC pending state.

Recommended fix:

- Configure refresh-token storage with explicit non-migrating iOS accessibility and Android encrypted shared preferences.
- Include a migration path for existing stored tokens.

### P3-8: Dev PostgreSQL Compose Service Binds to LAN With Weak Fixed Credentials

Affected files:

- `docker-compose.yml:6`
- `docker-compose.yml:11`
- `Makefile:20`

Impact:

- LAN users can connect to a developer/operator database, read or alter app users/settings/audit data, and seed admin state.

Recommended fix:

- Bind to `127.0.0.1:5432:5432`.
- Source password from `.env`.
- Document compose as local-only.

### P3-9: Mutable Supply-Chain Inputs and Non-Gating Security Scans

Affected files:

- `helm/kubecenter/values-homelab.yaml:7`
- `helm/kubecenter/values-homelab.yaml:15`
- `helm/kubecenter/values-homelab.yaml:47`
- `helm/kubecenter/values.yaml:230`
- `scripts/build-push.sh:8`
- `.github/workflows/ci-release.yml:130`
- `.github/workflows/ci-release.yml:214`
- `.github/workflows/mobile-ci.yml:168`
- `.github/workflows/ci.yml:102`

Impact:

- Deployments and releases can pull unreviewed mutable artifacts or ship known high/critical vulnerabilities when scans are non-gating.

Recommended fix:

- Pin deployable images/actions by digest or commit SHA.
- Avoid `latest` in values.
- Deploy immutable tags/digests.
- Make release security scans fail with reviewed allowlists.

## Dependency and Secret Scan Results

### Dependency Advisories

- `govulncheck ./...` found reachable Go advisories in the backend:
  - `GO-2026-5026`
  - `GO-2026-4986`
  - `GO-2026-4982`
  - `GO-2026-4980`
  - `GO-2026-4977`
  - `GO-2026-4976`
  - `GO-2026-4971`
  - `GO-2026-4918`
- OSV scanned:
  - `backend/go.mod`
  - `mobile/pubspec.lock`
  - `e2e/package-lock.json`
- OSV reported Go findings only.
- `npm audit --package-lock-only --json` in `e2e/` returned zero vulnerabilities.
- `flutter pub outdated --json` showed no current package advisories, but did show discontinued dev/transitive packages:
  - `build_resolvers`
  - `build_runner_core`
  - `js`

### Git History Secret Scan

Gitleaks scanned 573 commits and found 8 redacted hits:

- 6 likely false positives from SHA/type-hash tests.
- 1 confirmed hardcoded homelab password in `helm/kubecenter/values-homelab.yaml`.
- 1 sample bearer token in `plans/feat-step-11-alerting.md`.

Recommended follow-up:

- Rotate any homelab/default secrets that may have been deployed.
- Replace plan-doc bearer examples with obvious non-secret placeholders such as `<webhook-token>`.
- Add repository secret scanning to CI with a baseline for known false positives.

## Exploit Chains

### Chain A: Fresh Install Takeover

1. Fresh exposed deployment has zero users.
2. `auth.setupToken` is unset.
3. Attacker polls setup status and calls setup init.
4. Attacker becomes admin.
5. Attacker uses monitoring, auth, setup, and cluster-management surfaces.

### Chain B: Homelab Secret to Cluster Takeover

1. Operator deploys/copies homelab values.
2. Attacker forges HS256 JWT with committed secret.
3. Backend accepts admin role and Kubernetes identity claims.
4. Backend impersonates privileged identities.
5. Cluster actions and secret exfiltration follow.

### Chain C: Low-Privilege Reconnaissance to Grafana Abuse

1. Low-privileged user authenticates.
2. User enumerates cluster metadata via PromQL, CRD counts, diagnostics, and Loki label names.
3. User calls Grafana proxy write-capable API path.
4. Backend injects service token.
5. User modifies dashboards/folders or persists misleading operational artifacts.

### Chain D: Plaintext Token Theft to Session Forking

1. Mobile or web deployment uses plaintext HTTP/WS.
2. Attacker captures access/refresh token.
3. Attacker races refresh.
4. Non-atomic rotation creates multiple valid successor sessions.
5. If LDAP identity was revoked, stale cached sessions can extend access.

### Chain E: Backend Pod Compromise to Internal Pivot

1. Attacker compromises backend pod or image.
2. Broad service-account permissions allow secret reads and impersonation.
3. Port-only egress allows connections to internal services.
4. Attacker pivots to Kubernetes API, databases, observability, or metadata endpoints.

## Remediation Plan

### Immediate

- Remove or gate homelab secrets and rotate any deployed values.
- Require setup token for all non-dev setup flows.
- Restrict backend service-account impersonation and secret permissions.
- Admin-gate or harden Grafana proxy and arbitrary PromQL endpoints.
- Block release mobile builds without HTTPS `BACKEND_URL` and real Android release signing.
- Upgrade Go/toolchain dependencies and rerun `govulncheck`.

### Near Term

- Fix refresh-token rotation atomicity.
- Revalidate LDAP users/groups on refresh.
- Implement trusted-proxy handling for `X-Forwarded-For` and `X-Real-IP`.
- Route all Kubernetes-facing handlers through `ClusterRouter` or reject non-local cluster IDs.
- Harden SSRF validation for cluster and monitoring URLs.
- Add destination restrictions to NetworkPolicy egress.
- Revoke FCM registration on logout.
- Fix Sentry request URL scrubbing.

### Follow-Up Hardening

- Scope CRD inventory/counts by RBAC or admin-gate them.
- Enforce RBAC on diagnostics related resources.
- Fix CRD update URL/body name mismatch.
- Scope or admin-gate Loki label-name enumeration.
- Reject plaintext LDAP unless explicitly allowed.
- Harden mobile OIDC error callback state handling.
- Use explicit secure-storage options for refresh tokens.
- Bind dev PostgreSQL to localhost and remove fixed compose password.
- Pin mutable supply-chain inputs and make release scans gating.

## Open Items (Process)

- **F#8 — Phase 2 commit `1b76f2c` violated the CLAUDE.md "5-file cap per
  phase" rule** (Agent Directive 2). The commit touched 17 files in a
  single change; the cap exists to keep per-phase context tight and to
  bound the blast radius of a bad merge. The offending commit is already
  in `feat/security-phase-2-rbac-cluster-routing` history and rebasing it
  out would invalidate downstream review work — leaving as-is, noting
  here for the next audit. **Action for future security phases:** break
  rule-changing sweeps into a sequence of ≤5-file commits per phase, even
  when the changes are mechanically isomorphic across many files. The
  follow-up review path stays the same per commit; just the chunking
  changes.

## Verification Commands Run During Audit

- `git status --short`
- `rg --files`
- High-risk `rg` pattern scans for secrets, auth, tokens, SSRF, proxying, TLS, exec, and Kubernetes-sensitive paths.
- `go run github.com/zricethezav/gitleaks/v8@latest detect --source . --redact --report-format json --report-path C:\tmp\k8scenter-gitleaks.json --no-banner`
- `go run github.com/google/osv-scanner/v2/cmd/osv-scanner@latest scan source --format markdown --verbosity error --lockfile e2e/package-lock.json --lockfile mobile/pubspec.lock --lockfile backend/go.mod`
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
- `npm audit --package-lock-only --json` from `e2e/`
- `flutter pub outdated --json` from `mobile/`
- `helm dependency list helm/kubecenter`
