package auth

import "golang.org/x/oauth2"

// NewOIDCProviderForTest builds a minimal OIDCProvider with the fields
// the read-only [MobileAuthConfig] surface depends on populated. Skips
// the live `.well-known/openid-configuration` discovery that
// [NewOIDCProvider] performs, so server-package tests can register a
// stub provider against [ProviderRegistry] without standing up a mock
// IdP.
//
// **Internal — test-only.** Production wiring uses [NewOIDCProvider];
// this helper exists so cross-package tests (e.g.
// `internal/server/handle_oidc_mobile_config_test.go`) can exercise the
// 200 path on routes that depend on `OIDCProvider` internals. Lives
// outside an `_test.go` file because Go does not export test symbols
// across package boundaries; the `ForTest` suffix is the visible
// marker that callers in non-test code must not use it.
func NewOIDCProviderForTest(cfg OIDCProviderConfig, authEndpoint string, scopes []string) *OIDCProvider {
	return &OIDCProvider{
		Config: cfg,
		oauth2Config: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL: authEndpoint,
			},
		},
	}
}
