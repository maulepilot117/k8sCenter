# Step 12: OIDC/LDAP Authentication — SSO Integration

## Overview

Add OpenID Connect (OIDC) and LDAP authentication providers to k8sCenter, enabling enterprise SSO alongside the existing local auth. This step adds multi-provider coexistence, a frontend auth settings UI, and configurable identity mapping from OIDC claims / LDAP attributes to Kubernetes groups for impersonation.

## Problem Statement

k8sCenter currently only supports local username/password authentication. Enterprise environments require SSO via OIDC (Google, Keycloak, Dex, Okta) and/or LDAP (Active Directory, OpenLDAP). Without these, k8sCenter cannot integrate into existing identity infrastructure, requiring separate credential management.

## Proposed Solution

Implement OIDC and LDAP as pluggable auth providers alongside the existing local provider, with:
- OIDC authorization code flow with PKCE for redirect-based SSO
- LDAP bind+search authentication for directory-based credentials
- A provider registry pattern to manage multiple simultaneous providers
- Frontend login page dynamically rendering configured providers
- Admin settings UI for managing OIDC/LDAP configuration

---

## Technical Approach

### Architecture

The existing `AuthProvider` interface works for credential-based providers (local, LDAP). OIDC requires a separate interface since it uses redirect-based flow, not username/password. Both ultimately produce an `auth.User` that feeds into the existing `issueTokenPair` → JWT flow.

```
┌─────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  Login Page  │────▶│  Local Provider   │────▶│                  │
│             │     └──────────────────┘     │  issueTokenPair  │
│  [form]     │     ┌──────────────────┐     │  (existing)      │
│  [SSO btns] │────▶│  LDAP Provider    │────▶│                  │
│             │     └──────────────────┘     │  ┌────────────┐  │
│             │     ┌──────────────────┐     │  │ JWT Access │  │
│             │────▶│  OIDC Provider    │────▶│  │ + Refresh  │  │
│             │     │  (redirect flow)  │     │  │ Cookie     │  │
└─────────────┘     └──────────────────┘     └──┴────────────┴──┘
```

### Key Design Decisions

**D1. OIDC does NOT use the existing `AuthProvider` interface.** The current interface is `Authenticate(ctx, Credentials) (*User, error)` — a synchronous username/password pattern. OIDC is a two-phase redirect flow. Rather than contorting the interface, OIDC gets its own handler pattern with `LoginRedirect()` and `HandleCallback()` methods.

**D2. k8sCenter issues its own JWT after OIDC/LDAP login.** The OIDC ID token is consumed server-side only during the callback. After claims mapping, k8sCenter issues its own HMAC-SHA256 JWT (same as local auth). This means the auth middleware, RBAC checking, and token refresh are completely provider-agnostic.

**D3. Token refresh does NOT re-validate against external providers.** When a k8sCenter refresh token is used, we do NOT re-check the OIDC provider or LDAP server. The session is valid until the refresh token expires (7 days). If a user is deactivated externally, their session persists until expiry. This is the same trade-off Kubernetes makes with OIDC tokens.

**D4. OIDC callback returns access token via a short-lived httpOnly cookie.** After successful OIDC callback, the backend sets an httpOnly cookie containing the access token and redirects to a frontend callback page. The frontend reads the token from the cookie (via a dedicated BFF endpoint), stores it in memory, and clears the cookie. This avoids exposing tokens in URL fragments or query parameters.

**D5. State/nonce/PKCE verifier stored server-side.** A `sync.Map`-based store with 5-minute TTL (similar to the existing `SessionStore` pattern) holds OIDC flow state. The state parameter is the lookup key.

**D6. LDAP: connect-per-request, no pooling.** LDAP is only consulted during login (not per-API-call). Creating a new TLS connection per login attempt is acceptable. Add pooling later if latency becomes an issue.

**D7. Auth settings stored in k8s ConfigMap + Secret.** OIDC/LDAP configuration (non-sensitive) goes in a ConfigMap. Client secrets and bind passwords go in a k8s Secret. Settings changes take effect without restart via a file watcher or re-read-on-request pattern.

**D8. `handleRefresh` needs multi-provider user lookup.** Currently hardcoded to `s.LocalAuth.GetUserByID(userID)`. Must evolve to look up users from the correct provider. The `Provider` field in the refresh session identifies which provider to query. For OIDC users (no local user store), user data is reconstructed from the JWT claims stored at session creation time.

---

## Implementation Phases

### Phase 1: Backend Provider Infrastructure (Foundation)

Create the provider registry, refactor existing handlers for multi-provider support, and add OIDC flow state management.

#### Files to create:

**`backend/internal/auth/registry.go`** — Provider registry
- `ProviderRegistry` struct holding `map[string]AuthProvider` for credential-based providers and `map[string]*OIDCProvider` for redirect-based providers
- `Register(id string, provider AuthProvider)` — add a credential-based provider
- `RegisterOIDC(id string, provider *OIDCProvider)` — add an OIDC provider
- `Get(id string) (AuthProvider, bool)` — lookup credential-based provider
- `GetOIDC(id string) (*OIDCProvider, bool)` — lookup OIDC provider
- `ListProviders() []ProviderInfo` — return all providers for the login page
- `GetUserByID(providerType, userID string) (*User, error)` — multi-provider user lookup for refresh

**`backend/internal/auth/oidcstate.go`** — OIDC flow state store
- `OIDCFlowState` struct: `State`, `Nonce`, `PKCEVerifier`, `ProviderID`, `RedirectURI`, `CreatedAt`
- `OIDCStateStore` using `sync.Map` with 5-minute TTL
- `Store(state OIDCFlowState)` — save flow state
- `Consume(stateParam string) (*OIDCFlowState, error)` — retrieve and delete (single-use)
- `StartCleanup(ctx, interval)` — background goroutine to evict expired entries

#### Files to modify:

**`backend/internal/auth/provider.go`** — Add `ProviderInfo` type
- Add `ProviderInfo` struct: `ID`, `Type` ("local"/"oidc"/"ldap"), `DisplayName`, `LoginURL` (for OIDC)
- Keep existing `AuthProvider` interface unchanged
- Add `UserLookup` interface: `GetUserByID(id string) (*User, error)` — implemented by LocalProvider and LDAPProvider

**`backend/internal/server/server.go`** — Replace `LocalAuth` with registry
- Add `AuthRegistry *auth.ProviderRegistry` to `Server` and `Deps`
- Keep `LocalAuth` temporarily for backward compat during migration (removed at end of step)

**`backend/internal/server/handle_auth.go`** — Multi-provider login + refresh
- `handleLogin`: Accept optional `provider` field in request body (default: `"local"`). Route to correct provider via registry.
- `handleRefresh`: Use `s.AuthRegistry.GetUserByID(session.Provider, userID)` instead of `s.LocalAuth.GetUserByID(userID)`
- `handleAuthProviders`: Return `s.AuthRegistry.ListProviders()` instead of hardcoded list

**`backend/internal/auth/session.go`** — Store provider in refresh session
- `RefreshSession` already has `Provider` field — verify it's correctly set for all providers
- `Validate()` should return `(userID, provider, error)` instead of just `(userID, error)`

**Acceptance criteria (Phase 1):**
- [ ] Provider registry can hold local + LDAP + OIDC providers simultaneously
- [ ] Login endpoint routes to correct provider based on `provider` field
- [ ] Refresh endpoint works across all provider types
- [ ] `/auth/providers` dynamically lists configured providers
- [ ] OIDC state store has TTL-based cleanup
- [ ] All existing auth tests continue to pass
- [ ] No behavioral changes for local auth users

---

### Phase 2: OIDC Provider Implementation

#### Files to create:

**`backend/internal/auth/oidc.go`** — OIDC provider
- `OIDCProviderConfig` struct:
  - `ID` string — unique provider identifier (e.g., "google", "keycloak")
  - `DisplayName` string — shown on login page
  - `IssuerURL` string — OIDC discovery URL
  - `ClientID` string
  - `ClientSecret` string — from env var / k8s Secret
  - `RedirectURL` string — callback URL
  - `Scopes` []string — default: `["openid", "email", "profile"]`
  - `UsernameClaim` string — default: `"email"` — maps to `KubernetesUsername`
  - `GroupsClaim` string — default: `"groups"` — maps to `KubernetesGroups`
  - `GroupsPrefix` string — default: `""` — prepended to each group name
  - `AllowedDomains` []string — optional email domain restriction
  - `TLSInsecure` bool — skip TLS verification (internal providers)
  - `CACertPath` string — custom CA for internal OIDC providers
- `OIDCProvider` struct holding `*oidc.Provider`, `oauth2.Config`, `*oidc.IDTokenVerifier`
- `NewOIDCProvider(ctx, config) (*OIDCProvider, error)` — performs OIDC discovery
- `LoginRedirect(w, r)` — generates state + nonce + PKCE, stores in OIDCStateStore, returns redirect URL
- `HandleCallback(w, r, flowState) (*User, error)` — exchanges code, validates ID token + nonce, maps claims to `User`
- Custom claims struct for extracting `email`, `preferred_username`, `groups`, `name`
- `mapClaimsToUser(idToken, claims, config) *User` — configurable claim mapping
- Handle groups claim as either `string` or `[]string` (provider inconsistency per k8s issue #33290)

**`backend/internal/auth/oidc_test.go`** — OIDC tests
- Use `httptest.NewServer` as mock OIDC provider with JWKS endpoint
- Test: successful auth flow end-to-end (mock provider → callback → user mapping)
- Test: invalid state parameter → rejection
- Test: nonce mismatch → rejection
- Test: expired ID token → rejection
- Test: wrong audience → rejection
- Test: groups claim as string vs array
- Test: email domain filtering
- Test: custom username/groups claim mapping

#### Files to modify:

**`backend/internal/server/routes.go`** — Add OIDC routes
- `GET /api/v1/auth/oidc/{providerID}/login` — public, rate limited
- `GET /api/v1/auth/oidc/{providerID}/callback` — public, rate limited
- Both in the public route group (no JWT required), outside CSRF middleware (OIDC callback is a browser redirect, not a fetch)

**`backend/internal/server/handle_auth.go`** — Add OIDC handlers
- `handleOIDCLogin(w, r)` — extract `providerID` from URL, get OIDC provider from registry, call `LoginRedirect`, issue HTTP redirect
- `handleOIDCCallback(w, r)` — extract `state` + `code` from query params, consume flow state, call `HandleCallback`, issue JWT + refresh cookie, redirect to frontend

**`backend/internal/config/config.go`** — Add OIDC config
- Add `OIDCConfig` struct and `[]OIDCConfig` to `AuthConfig`
- Env var mapping: `KUBECENTER_AUTH_OIDC_0_CLIENTID`, etc.

**`backend/cmd/kubecenter/main.go`** — Wire OIDC providers
- Create `ProviderRegistry`, register local provider
- For each configured OIDC provider: call `NewOIDCProvider`, register in registry
- Pass registry to `server.Deps`

**Acceptance criteria (Phase 2):**
- [ ] OIDC login redirects to provider with correct state, nonce, PKCE challenge
- [ ] PKCE code verifier is stored server-side, never sent to browser
- [ ] Callback validates state, exchanges code for tokens, verifies ID token
- [ ] Nonce is validated against stored value
- [ ] OIDC groups are correctly mapped to `KubernetesGroups`
- [ ] Username claim is configurable (email, preferred_username, sub)
- [ ] Groups claim name is configurable
- [ ] Email domain filtering works when configured
- [ ] After callback, k8sCenter JWT + refresh cookie are issued
- [ ] User is redirected to frontend dashboard after successful OIDC login
- [ ] Multiple OIDC providers can coexist
- [ ] Audit log captures OIDC login success/failure with provider name
- [ ] Rate limiting on OIDC login and callback endpoints
- [ ] Custom CA certificate support for internal OIDC providers

---

### Phase 3: LDAP Provider Implementation

#### Files to create:

**`backend/internal/auth/ldap.go`** — LDAP provider
- `LDAPConfig` struct:
  - `ID` string — unique provider identifier (e.g., "corp-ad")
  - `DisplayName` string — shown on login page
  - `URL` string — `ldaps://host:636` or `ldap://host:389`
  - `BindDN` string — service account DN
  - `BindPassword` string — from env var / k8s Secret
  - `StartTLS` bool — upgrade plaintext connection to TLS
  - `TLSInsecure` bool — skip TLS cert verification
  - `CACertPath` string — custom CA cert for internal LDAP
  - `UserBaseDN` string — e.g., `DC=corp,DC=com`
  - `UserFilter` string — e.g., `(sAMAccountName={0})` — `{0}` replaced with escaped username
  - `UserAttributes` []string — attributes to fetch (e.g., `["sAMAccountName", "mail", "memberOf"]`)
  - `GroupBaseDN` string — where to search for groups
  - `GroupFilter` string — e.g., `(member={0})` — `{0}` replaced with user DN
  - `GroupNameAttr` string — default: `"cn"` — which group attribute = group name
  - `UsernameMapAttr` string — maps to `KubernetesUsername`
  - `GroupsPrefix` string — prepended to k8s group names
- `LDAPProvider` struct implementing `AuthProvider` interface
- `NewLDAPProvider(config, logger) *LDAPProvider`
- `Authenticate(ctx, creds) (*User, error)`:
  1. Validate username against allowlist regex `^[a-zA-Z0-9._@-]+$` (pre-LDAP-escape defense)
  2. Dial LDAP server (with timeout)
  3. If StartTLS, upgrade connection
  4. Bind as service account
  5. Search for user DN using `ldap.EscapeFilter(username)` in filter template
  6. Verify exactly one result
  7. Bind as user DN with supplied password
  8. Rebind as service account
  9. Fetch group membership (via group search or memberOf attribute)
  10. Map attributes to `auth.User`
  11. Close connection
- `TestConnection(ctx) error` — verify LDAP connectivity and service account bind (for settings UI)

**`backend/internal/auth/ldap_test.go`** — LDAP tests
- Unit tests with a mock LDAP interface (wrap ldap.Conn operations behind an interface)
- Test: successful bind+search authentication
- Test: invalid password → `ldap.LDAPResultInvalidCredentials`
- Test: user not found → error
- Test: multiple users found → error (ambiguous)
- Test: LDAP injection attempt in username → escaped, no injection
- Test: group membership extraction
- Test: connection timeout handling
- Test: TLS certificate verification
- Test: `TestConnection` for settings UI validation
- Integration test (optional, requires Docker): use testcontainers OpenLDAP module

#### Files to modify:

**`backend/internal/server/handle_auth.go`** — Update login for LDAP
- `handleLogin`: When `provider` field is an LDAP provider ID, route to LDAP provider's `Authenticate` method
- Same `issueTokenPair` flow after successful LDAP auth

**`backend/internal/config/config.go`** — Add LDAP config
- Add `LDAPConfig` struct and `[]LDAPConfig` to `AuthConfig`
- Env var mapping: `KUBECENTER_AUTH_LDAP_0_BINDPASSWORD`, etc.

**`backend/cmd/kubecenter/main.go`** — Wire LDAP providers
- For each configured LDAP provider: create `LDAPProvider`, register in `ProviderRegistry`

**Acceptance criteria (Phase 3):**
- [ ] LDAP bind+search authentication works with Active Directory
- [ ] LDAP bind+search authentication works with OpenLDAP
- [ ] LDAP injection is prevented via `ldap.EscapeFilter()` + username allowlist
- [ ] TLS (LDAPS) and STARTTLS both work
- [ ] Custom CA certificate support for internal LDAP servers
- [ ] Group membership correctly maps to `KubernetesGroups`
- [ ] Groups prefix prevents collision with local/OIDC groups
- [ ] Connection timeouts prevent hung goroutines (dial: 5s, operation: 10s)
- [ ] `TestConnection` endpoint validates LDAP config before saving
- [ ] Audit log captures LDAP login success/failure
- [ ] Error messages do not leak LDAP internals (DN structure, server info)

---

### Phase 4: Frontend — Login Page + Auth Settings UI

#### Files to create:

**`frontend/islands/AuthProviderButtons.tsx`** — SSO buttons island
- Fetches provider list from `/api/v1/auth/providers` on mount
- Renders "Sign in with X" buttons for each OIDC provider
- Renders provider selector (dropdown/tabs) for multiple credential-based providers (local + LDAP)
- OIDC buttons navigate to `/api/v1/auth/oidc/{id}/login` (full page navigation, not fetch)
- Passes selected credential provider ID to LoginForm for LDAP support

**`frontend/routes/auth/callback.tsx`** — OIDC callback landing page
- Server-rendered page that reads the access token from the httpOnly cookie set by the OIDC callback
- Renders a `OIDCCallbackHandler` island that:
  1. Calls a BFF endpoint to retrieve and clear the token cookie
  2. Stores the access token in memory via `setAccessToken()`
  3. Fetches user info via `fetchCurrentUser()`
  4. Redirects to dashboard

**`frontend/islands/OIDCCallbackHandler.tsx`** — Handles post-OIDC-callback token retrieval
- Calls `POST /api/auth/oidc-token-exchange` (BFF endpoint)
- On success: stores token, redirects to `/`
- On failure: shows error, links back to login

**`frontend/routes/api/auth/oidc-token-exchange.ts`** — BFF endpoint for OIDC token
- Reads the `oidc_access_token` httpOnly cookie
- Returns it in the response body
- Clears the cookie (single-use)
- This prevents the access token from being exposed in URL fragments

**`frontend/routes/settings/auth.tsx`** — Auth settings page route
- Server-rendered page for the auth configuration UI
- Renders `AuthSettings` island

**`frontend/islands/AuthSettings.tsx`** — Auth settings island
- Tabs: "Local", "OIDC Providers", "LDAP Providers"
- **OIDC tab:**
  - List configured OIDC providers with edit/delete
  - "Add OIDC Provider" form: issuer URL, client ID, client secret, scopes, claim mappings, allowed domains
  - "Test Connection" button that validates the issuer URL discovery
- **LDAP tab:**
  - List configured LDAP providers with edit/delete
  - "Add LDAP Provider" form: URL, bind DN, bind password, user/group search config, TLS settings
  - "Test Connection" button that validates LDAP connectivity and service bind
- **Local tab:**
  - Toggle local auth enabled/disabled (cannot disable if it's the only provider)
  - List local users (future: user management)
- Save triggers `PUT /api/v1/settings/auth`
- Requires admin role

#### Files to modify:

**`frontend/routes/login.tsx`** — Update login page
- Replace hardcoded "KubeCenter" with "k8sCenter"
- Add `AuthProviderButtons` island below the login form
- Show a divider ("or") between username/password form and SSO buttons when OIDC providers exist

**`frontend/islands/LoginForm.tsx`** — Accept provider prop
- Accept optional `provider` prop (default: `"local"`)
- Include `provider` field in the login POST body
- When provider is LDAP, show appropriate placeholder text

**`frontend/lib/auth.ts`** — Update login function
- `login(username, password, provider?)` — include provider in request body
- Add `handleOIDCCallback()` function for the callback page

**`frontend/islands/Sidebar.tsx`** — Add settings nav
- Add "Settings" section with "Authentication" link (visible to admin users only)

**`frontend/lib/constants.ts`** — Add settings nav section
- Add `NAV_SECTIONS` entry for Settings > Authentication

#### Backend endpoints for settings UI:

**`backend/internal/server/handle_settings.go`** — Settings handlers
- `GET /api/v1/settings/auth` — returns current auth config (secrets masked)
- `PUT /api/v1/settings/auth` — updates auth config (writes to ConfigMap + Secret in cluster)
- `POST /api/v1/settings/auth/test-oidc` — test OIDC provider discovery
- `POST /api/v1/settings/auth/test-ldap` — test LDAP connection and bind

**Acceptance criteria (Phase 4):**
- [ ] Login page dynamically shows configured SSO buttons
- [ ] OIDC "Sign in with X" performs full redirect flow and returns to dashboard
- [ ] LDAP login works via the same form with provider selector
- [ ] OIDC callback page handles token exchange securely
- [ ] Auth settings UI allows adding/editing/deleting OIDC providers
- [ ] Auth settings UI allows adding/editing/deleting LDAP providers
- [ ] "Test Connection" validates OIDC discovery and LDAP bind
- [ ] Client secrets and bind passwords are masked in the settings UI
- [ ] Settings changes persist across backend restarts (ConfigMap/Secret)
- [ ] Only admin users can access the settings page
- [ ] Login page shows "k8sCenter" (not "KubeCenter")

---

### Phase 5: Integration Testing & Polish

#### Files to create:

**`backend/internal/auth/oidc_integration_test.go`** — OIDC integration tests
- Full end-to-end flow with mock OIDC server (`httptest.NewServer`)
- Test: login redirect → callback → JWT issued → API call works
- Test: refresh token works for OIDC-authenticated users
- Test: multiple OIDC providers, correct routing

**`backend/internal/auth/ldap_integration_test.go`** — LDAP integration tests (optional)
- Use testcontainers OpenLDAP module if Docker available in CI
- Test: full bind+search flow against real LDAP server
- Test: group membership extraction

**`backend/internal/server/handle_auth_test.go`** — Update existing tests
- Add tests for multi-provider login routing
- Add tests for provider-aware refresh
- Verify backward compat: existing local auth tests unchanged

#### Files to modify:

**`backend/internal/server/handle_auth_test.go`** — Extend
- Test: login with `provider: "ldap"` routes to LDAP
- Test: login with unknown provider returns 400
- Test: refresh for OIDC-originated session works
- Test: `/auth/providers` returns all configured providers

**Acceptance criteria (Phase 5):**
- [ ] All existing auth tests pass (no regressions)
- [ ] OIDC end-to-end integration test passes
- [ ] Multi-provider refresh test passes
- [ ] Error messages are user-friendly (no internal details leaked)
- [ ] Audit log entries include provider type for all auth events
- [ ] `make lint` and `make test` pass

---

## Alternative Approaches Considered

### 1. Use HashiCorp CAP library instead of go-oidc + oauth2
**Rejected.** CAP provides higher-level abstractions but internally depends on go-oidc. Using go-oidc directly gives us more control over how OIDC integrates with the existing JWT session model. CAP's opinionated callback handler would conflict with our custom token issuance flow.

### 2. OIDC access token in URL fragment (`#access_token=...`)
**Rejected.** URL fragments can leak via browser history, referer headers, and debugging tools. Using an httpOnly cookie for the token handoff is more secure and aligns with the existing cookie-based refresh token pattern.

### 3. Re-validate against external provider on every token refresh
**Rejected (for MVP).** This adds latency and complexity. If the OIDC provider or LDAP server is temporarily down, all refreshes would fail. The 7-day refresh window is an acceptable trade-off. Can be added as P2 enhancement.

### 4. Store OIDC/LDAP config in SQLite (Step 14)
**Deferred.** Step 14 adds SQLite for audit logs. Auth config could move there, but for Step 12, ConfigMap + Secret is simpler and works with the existing k8s-native pattern.

### 5. Connection pooling for LDAP
**Deferred.** LDAP is only consulted during login. At typical login rates (< 10/min), connect-per-request is fine. Pool if latency is measured as a problem.

---

## Dependencies & Prerequisites

| Dependency | Version | Purpose |
|---|---|---|
| `github.com/coreos/go-oidc/v3` | latest v3.x | OIDC discovery, ID token verification |
| `golang.org/x/oauth2` | >= v0.13.0 (currently v0.30.0 indirect) | Authorization code flow with PKCE |
| `github.com/go-ldap/ldap/v3` | latest v3.x | LDAP bind, search, group queries |

**No breaking changes to existing dependencies.** `golang.org/x/oauth2` is already an indirect dependency via client-go and just needs promotion to direct.

**Prerequisite:** Steps 1-11 must be complete (all done per progress tracker).

---

## Risk Analysis & Mitigation

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| OIDC provider unreachable at startup | Medium | Provider unusable until restart | Lazy initialization with retry: discover on first login attempt, cache result |
| LDAP injection via crafted username | Low | Data exfiltration from directory | `ldap.EscapeFilter()` + username allowlist regex + service account least privilege |
| OIDC groups claim varies by provider | High | Groups not mapped correctly | Configurable `GroupsClaim` field; handle both string and []string types |
| PKCE verifier leak | Low | Auth flow compromise | Verifier never leaves server; stored in server-side state store with 5-min TTL |
| Nonce replay attack | Low | Session hijacking | Nonce consumed on first use (deleted from state store) |
| `handleRefresh` regression for local users | Medium | Existing users can't refresh | Phase 1 includes comprehensive backward-compat tests |
| LDAP connection timeout blocks goroutine | Medium | Goroutine leak, memory pressure | Explicit dial timeout (5s) and operation timeout (10s) on all LDAP connections |
| Settings UI exposes secrets | Medium | Credential theft | Mask all secrets in API responses; require re-entry for updates |

---

## Security Considerations

- **PKCE (S256) required** for all OIDC flows, even with confidential clients (per OAuth 2.1 draft)
- **State parameter** validated on every callback to prevent CSRF
- **Nonce** validated against ID token to prevent replay
- **LDAP filter escaping** via `ldap.EscapeFilter()` on all user input
- **Username allowlist** `^[a-zA-Z0-9._@-]+$` validated before LDAP operations
- **OIDC redirect URI exact match** — no wildcards, no substring matching
- **TLS 1.2+ enforced** for LDAP connections (`tls.Config.MinVersion = tls.VersionTLS12`)
- **Client secrets and bind passwords** stored in k8s Secrets, loaded from env vars, never in config files
- **Audit logging** for all auth events (OIDC/LDAP success + failure) with provider identification
- **Rate limiting** on OIDC login/callback and LDAP login endpoints
- **Error messages** do not leak LDAP DN structure, server info, or OIDC provider internals

---

## Files Summary

### New files (13):
```
backend/internal/auth/
├── registry.go              # Provider registry (multi-provider management)
├── oidcstate.go             # OIDC flow state store (state/nonce/PKCE)
├── oidc.go                  # OIDC provider (discovery, login redirect, callback)
├── oidc_test.go             # OIDC unit tests
├── oidc_integration_test.go # OIDC integration tests (mock server)
├── ldap.go                  # LDAP provider (bind+search, group mapping)
├── ldap_test.go             # LDAP unit tests
└── ldap_integration_test.go # LDAP integration tests (testcontainers, optional)

backend/internal/server/
└── handle_settings.go       # Auth settings API handlers

frontend/islands/
├── AuthProviderButtons.tsx  # SSO buttons + provider selector
├── OIDCCallbackHandler.tsx  # Post-OIDC callback token exchange
└── AuthSettings.tsx         # Auth settings admin UI

frontend/routes/
├── auth/callback.tsx        # OIDC callback landing page
├── settings/auth.tsx        # Auth settings page route
└── api/auth/oidc-token-exchange.ts  # BFF endpoint for OIDC token handoff
```

### Modified files (12):
```
backend/internal/auth/provider.go    # Add ProviderInfo, UserLookup interface
backend/internal/auth/session.go     # Validate returns (userID, provider, error)
backend/internal/config/config.go    # Add OIDCConfig, LDAPConfig to AuthConfig
backend/internal/config/defaults.go  # Add defaults for new config fields
backend/internal/server/server.go    # Add AuthRegistry to Server + Deps
backend/internal/server/routes.go    # Add OIDC + settings routes
backend/internal/server/handle_auth.go        # Multi-provider login/refresh/providers
backend/internal/server/handle_auth_test.go   # Extended tests
backend/cmd/kubecenter/main.go       # Wire OIDC + LDAP providers
frontend/routes/login.tsx            # SSO buttons, "k8sCenter" branding
frontend/islands/LoginForm.tsx       # Accept provider prop
frontend/lib/auth.ts                 # Multi-provider login, OIDC callback handler
frontend/islands/Sidebar.tsx         # Settings nav section
```

---

## Specification Gaps & Open Questions

1. **OIDC user deactivation:** When an OIDC user is removed from the identity provider, their k8sCenter session persists until refresh token expiry (7 days). Is this acceptable, or do we need a shorter refresh TTL for external providers?

2. **Multiple LDAP providers:** The plan supports multiple LDAP configs. Is this needed (e.g., corp AD + partner LDAP), or is a single LDAP config sufficient?

3. **Local auth disable:** Should admins be able to disable local auth entirely once OIDC/LDAP is configured? The plan prevents disabling the last provider but allows disabling local if others exist.

4. **OIDC provider discovery failure at startup:** If an OIDC provider's issuer URL is unreachable at startup, should k8sCenter: (a) fail to start, (b) start without that provider and retry, or (c) lazy-discover on first login? Plan proposes (c).

5. **Group prefix collisions:** If two OIDC providers both map to the same k8s groups without prefixes, users could gain unintended permissions. Should group prefixes be required (not optional)?

6. **Settings hot-reload:** Should auth settings changes require a backend restart, or take effect immediately? Plan proposes immediate effect via ConfigMap/Secret watch, but this adds complexity.

---

## References

### Internal
- `backend/internal/auth/provider.go` — Existing AuthProvider interface
- `backend/internal/auth/local.go` — Local provider implementation (pattern to follow)
- `backend/internal/auth/jwt.go` — TokenManager, TokenClaims
- `backend/internal/auth/session.go` — SessionStore pattern
- `backend/internal/server/handle_auth.go` — Current login/refresh handlers
- `backend/internal/server/routes.go` — Route registration pattern
- `backend/cmd/kubecenter/main.go` — Dependency wiring

### External
- [coreos/go-oidc v3 docs](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc)
- [golang.org/x/oauth2 PKCE support](https://pkg.go.dev/golang.org/x/oauth2)
- [go-ldap/ldap/v3 docs](https://pkg.go.dev/github.com/go-ldap/ldap/v3)
- [OWASP LDAP Injection Prevention](https://cheatsheetseries.owasp.org/cheatsheets/LDAP_Injection_Prevention_Cheat_Sheet.html)
- [Kubernetes OIDC Authentication](https://kubernetes.io/docs/reference/access-authn-authz/authentication/)
- [OAuth 2.1 Draft — PKCE requirement](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1)
- [mockoidc — test OIDC server](https://github.com/oauth2-proxy/mockoidc)
- [testcontainers OpenLDAP module](https://golang.testcontainers.org/modules/openldap/)
