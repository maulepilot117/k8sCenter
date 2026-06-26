package yaml

import "testing"

// FuzzParseMultiDoc verifies that ParseMultiDoc never panics on arbitrary
// input bytes. A returned error is acceptable; a panic is not.
func FuzzParseMultiDoc(f *testing.F) {
	// Seed corpus -------------------------------------------------------

	// Valid single-document
	f.Add([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"))

	// Valid multi-document
	f.Add([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b\n"))

	// Empty body
	f.Add([]byte{})

	// YAML anchor/alias
	f.Add([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  a: &anchor 1\n  b: *anchor\n"))

	// Deeply nested mapping (10 levels)
	f.Add([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: deep\ndata:\n  a:\n    b:\n      c:\n        d:\n          e:\n            f:\n              g:\n                h:\n                  i:\n                    j: leaf\n"))

	// Bare document separator only
	f.Add([]byte("---\n"))

	// Binary / non-UTF-8 bytes
	f.Add([]byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe, 0xfd})

	// Very long scalar value (bounded to avoid artificial OOM in seeds)
	longVal := make([]byte, 0, 256)
	longVal = append(longVal, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  key: ")...)
	for range 128 {
		longVal = append(longVal, 'A')
	}
	longVal = append(longVal, '\n')
	f.Add(longVal)

	// Fuzz target -------------------------------------------------------
	f.Fuzz(func(t *testing.T, data []byte) {
		// Oracle: must never panic. Error returns are fine.
		_, _ = ParseMultiDoc(data)
	})
}
