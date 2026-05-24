package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// TestClassifyLDAPError pins the error classification contract that
// drives the refresh handler's fail-closed vs fail-open-with-grace
// decision. The full LDAPProvider.Revalidate path requires a live
// directory and is exercised in the homelab soak; the classifier itself
// is pure logic and worth a regression test in isolation. Audit finding
// P2-3 (2026-05-22) — see ldap.go for the security context.
func TestClassifyLDAPError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantIs  error // sentinel the result should match via errors.Is
		wantNil bool
	}{
		{
			name:    "nil input maps to nil",
			err:     nil,
			wantNil: true,
		},
		{
			name:   "NoSuchObject is a definitive rejection",
			err:    ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("entry missing")),
			wantIs: ErrInvalidCredentials,
		},
		{
			name:   "InvalidCredentials from the directory is transient (service-account problem, not per-user)",
			err:    ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("bad bind")),
			wantIs: ErrLDAPTransient,
		},
		{
			name:   "ServerDown is transient",
			err:    ldap.NewError(ldap.ErrorNetwork, errors.New("connection refused")),
			wantIs: ErrLDAPTransient,
		},
		{
			name:   "Operations error from the directory is transient",
			err:    ldap.NewError(ldap.LDAPResultOperationsError, errors.New("internal")),
			wantIs: ErrLDAPTransient,
		},
		{
			name:   "Arbitrary non-ldap error is transient",
			err:    errors.New("dial tcp: i/o timeout"),
			wantIs: ErrLDAPTransient,
		},
		{
			name:   "Wrapped LDAP NoSuchObject is still definitive",
			err:    fmt.Errorf("search failed: %w", ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("gone"))),
			wantIs: ErrInvalidCredentials,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyLDAPError(tc.err)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected error matching %v, got nil", tc.wantIs)
			}
			if !errors.Is(got, tc.wantIs) {
				t.Fatalf("expected errors.Is(%v, %v) to be true", got, tc.wantIs)
			}
		})
	}
}

// TestLDAPProvider_ID asserts the public accessor matches the config —
// the refresh handler uses this to look up the right provider instance
// from the registry when revalidating an LDAP user.
func TestLDAPProvider_ID(t *testing.T) {
	p := &LDAPProvider{config: LDAPProviderConfig{ID: "ldap-corp"}}
	if got := p.ID(); got != "ldap-corp" {
		t.Fatalf("ID() = %q, want %q", got, "ldap-corp")
	}
}

// TestLDAPProvider_Revalidate_EmptyDN exercises the only Revalidate
// branch that doesn't require a live LDAP server: an empty DN must
// short-circuit to ErrInvalidCredentials before any IO. Anything else
// would either depend on the directory or on a mock we don't have.
func TestLDAPProvider_Revalidate_EmptyDN(t *testing.T) {
	p := &LDAPProvider{config: LDAPProviderConfig{ID: "ldap-corp"}, logger: testLogger()}
	if _, err := p.Revalidate(t.Context(), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for empty DN, got %v", err)
	}
}
