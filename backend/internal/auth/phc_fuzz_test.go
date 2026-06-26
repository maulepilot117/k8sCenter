package auth

import (
	"strings"
	"testing"
)

// FuzzParsePHC fuzzes the PHC-string parser used by Argon2id password
// verification. parsePHC is called on persisted (attacker-influenced)
// credential strings, so crash-safety on malformed input is the security
// value. Oracle: parsePHC must never panic for any input string. A
// returned error on malformed input is expected and correct — the test
// asserts nothing about the error value. On the success branch (err==nil)
// we sanity-check that salt and hash are non-nil to guard against a
// silent empty-slice return that would let an attacker bypass verification.
func FuzzParsePHC(f *testing.F) {
	// Valid PHC string — real base64url (no padding) segments.
	// Salt: 16 random bytes → base64.RawStdEncoding → "c2FsdHNhbHRzYWx0c2Fs"
	// Hash: 32 random bytes → base64.RawStdEncoding → "aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA"
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2Fs$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA")

	// Truncated — missing everything after v=19.
	f.Add("$argon2id$v=19$")

	// Missing hash segment — valid params + salt but no trailing $<hash>.
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2Fs")

	// Bad base64 in both salt and hash positions.
	f.Add("$argon2id$v=19$m=1,t=1,p=1$!!!$@@@")

	// Empty string.
	f.Add("")

	// Huge string (64 KB of '$' separators).
	f.Add(strings.Repeat("$", 1<<16))

	// Many '$' with otherwise plausible-looking content.
	f.Add("$argon2id$v=19$m=1,t=1,p=1$" + strings.Repeat("aGk$", 50))

	// Correct prefix but wrong algorithm name.
	f.Add("$argon2i$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2Fs$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA")

	// Negative / zero parameters.
	f.Add("$argon2id$v=19$m=0,t=0,p=0$c2FsdHNhbHRzYWx0c2Fs$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA")

	// Version field overflow bait.
	f.Add("$argon2id$v=99999999999999999999$m=65536,t=3,p=4$c2FsdA$aGFzaA")

	// Null bytes embedded.
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$\x00\x00$\x00\x00")

	f.Fuzz(func(t *testing.T, phc string) {
		salt, hash, err := parsePHC(phc)
		if err != nil {
			// Malformed input → error is the expected, correct path.
			return
		}
		// Success branch: sanity — neither slice should be nil.
		if salt == nil {
			t.Fatal("parsePHC returned nil salt with nil error")
		}
		if hash == nil {
			t.Fatal("parsePHC returned nil hash with nil error")
		}
	})
}
