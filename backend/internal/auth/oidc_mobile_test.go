package auth

import (
	"context"
	"strings"
	"testing"
)

// TestOIDCProvider_ExchangeMobile_InputValidation covers the pre-network
// validation in ExchangeMobile. Empty code / codeVerifier / nonce must
// short-circuit before any IdP round-trip — otherwise an unauthenticated
// caller can probe IdP error envelopes.
//
// The happy-path branches (real oauth2 Exchange, ID token verify, claim
// mapping) require a full mock IdP and are exercised by manual smoke
// against the homelab Authelia in U2's Verification step, matching the
// existing HandleCallback test posture.
func TestOIDCProvider_ExchangeMobile_InputValidation(t *testing.T) {
	p := &OIDCProvider{
		Config: OIDCProviderConfig{ID: "test"},
	}
	ctx := context.Background()

	cases := []struct {
		name         string
		code         string
		codeVerifier string
		nonce        string
		wantErr      string
	}{
		{"empty code", "", "v", "n", "code required"},
		{"empty verifier", "abc", "", "n", "codeVerifier required"},
		{"empty nonce", "abc", "v", "", "nonce required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.ExchangeMobile(ctx, tc.code, tc.codeVerifier, tc.nonce)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
