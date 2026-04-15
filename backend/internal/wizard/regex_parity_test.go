package wizard

import "testing"

// TestDnsLabelRegexParity pins the dnsLabelRegex pattern string so any change
// to the Go side is an explicit signal to update the matching TS regex at
// frontend/lib/wizard-constants.ts (DNS_LABEL_REGEX). Keeping these in lockstep
// prevents client-side validation from green-lighting names the server rejects.
func TestDnsLabelRegexParity(t *testing.T) {
	const expected = `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`
	if got := dnsLabelRegex.String(); got != expected {
		t.Fatalf("dnsLabelRegex drift: got %q, want %q (update frontend/lib/wizard-constants.ts DNS_LABEL_REGEX to match)", got, expected)
	}
}
