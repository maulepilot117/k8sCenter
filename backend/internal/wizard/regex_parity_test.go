package wizard

import (
	"regexp"
	"testing"
)

// TestRegexParity pins each Go-side regex that has a TypeScript counterpart in
// frontend/lib/wizard-constants.ts to its expected pattern string. A change on
// either side without updating the other becomes a test failure here (Go side)
// or in frontend/lib/wizard-constants_parity_test.ts (TS side).
//
// Keeping these in lockstep prevents client-side validation from green-lighting
// names the server rejects (or vice versa).
func TestRegexParity(t *testing.T) {
	cases := []struct {
		name       string
		got        *regexp.Regexp
		wantSource string
		// tsConstName names the TS constant that must mirror this pattern.
		tsConstName string
	}{
		{
			name:        "dnsLabelRegex",
			got:         dnsLabelRegex,
			wantSource:  `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`,
			tsConstName: "DNS_LABEL_REGEX",
		},
		{
			name:        "envVarNameRegex",
			got:         envVarNameRegex,
			wantSource:  `^[A-Za-z_][A-Za-z0-9_]*$`,
			tsConstName: "ENV_VAR_NAME_REGEX",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.String() != tc.wantSource {
				t.Fatalf("%s drift: got %q, want %q (update frontend/lib/wizard-constants.ts %s to match, or update this test)",
					tc.name, tc.got.String(), tc.wantSource, tc.tsConstName)
			}
		})
	}
}
