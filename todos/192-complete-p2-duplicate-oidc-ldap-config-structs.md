---
status: pending
priority: p2
issue_id: "192"
tags: [code-review, architecture, step-12]
dependencies: []
---

# 192: Duplicate config structs between config and auth packages

## Problem Statement

The OIDC and LDAP configuration is defined twice: once in the `config` package and again in the `auth` package. The structs are identical, and `main.go` contains approximately 30 lines of field-by-field copying between them. This duplication increases maintenance burden and risk of the structs drifting out of sync.

## Findings

- `config.OIDCConfig` and `auth.OIDCProviderConfig` have identical fields.
- `config.LDAPConfig` and the corresponding LDAP auth config struct have identical fields.
- `main.go` manually copies each field from the config struct to the auth struct when initializing providers.
- Any new config field must be added in three places: the config struct, the auth struct, and the copying code in main.go.

## Technical Details

**Affected files:**
- `backend/internal/config/config.go`
- `backend/internal/auth/oidc.go`
- `backend/internal/auth/ldap.go`
- `backend/cmd/kubecenter/main.go`

## Acceptance Criteria

- [ ] Use a single struct definition for OIDC config and a single struct for LDAP config
- [ ] Auth provider constructors (`NewOIDCProvider`, `NewLDAPProvider`) accept the config package types directly
- [ ] Remove the field-by-field copying code from main.go
- [ ] All existing tests continue to pass with the consolidated types
