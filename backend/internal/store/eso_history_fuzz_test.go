package store

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

// Bounds a decoded cursor must satisfy, re-derived here rather than read from
// the production constants so the oracle notices a guard being removed
// instead of agreeing with it.
var (
	fuzzCursorEpoch   = time.Unix(0, 0).UTC()
	fuzzCursorCeiling = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

const fuzzCursorMaxDecodedBytes = 64

// FuzzDecodeESOHistoryCursor fuzzes the history cursor decoder, which turns a
// client-supplied ?cursor= string into typed keyset values.
//
//   - Oracle A: DecodeESOHistoryCursor never panics.
//   - Oracle B: any accepted cursor is well-formed (id >= 1, attempt time in
//     [epoch, 2100-01-01], encoded payload at most 64 bytes), and
//     Decode∘Encode reproduces it exactly, so a page boundary survives the
//     round trip through the client.
//
// Each input is decoded twice: as given, and base64url-encoded first. The
// fuzzer rarely produces valid base64 of structured text on its own, so the
// second form is what reaches the "<micros>:<id>" parser.
func FuzzDecodeESOHistoryCursor(f *testing.F) {
	// Valid cursors, in encoded form.
	f.Add(EncodeESOHistoryCursor(ESOHistoryCursor{AttemptAt: time.Date(2026, 9, 1, 12, 0, 0, 123456000, time.UTC), ID: 42}))
	f.Add(EncodeESOHistoryCursor(ESOHistoryCursor{AttemptAt: fuzzCursorEpoch, ID: 1}))

	// Teeth. Each is also decoded base64url-encoded, so the raw payloads
	// below reach the field parser.
	f.Add("")
	f.Add("=")
	f.Add("AAAA")
	f.Add(":")
	f.Add("1:2:3")
	f.Add("-1:-1")
	f.Add("1:0")                           // id below 1: the id guard
	f.Add("9223372036854775807:1")         // max int64 micros: the 2100 ceiling
	f.Add("4102444800000001:1")            // one microsecond past the ceiling
	f.Add("9223372036854775808:1")         // overflows int64
	f.Add(strings.Repeat("0", 70) + "1:1") // parseable digits in a 73-byte payload: the length guard
	f.Add(strings.Repeat("A", 4096))       // 4 KiB base64 blob
	f.Add("\x00\xff")

	f.Fuzz(func(t *testing.T, s string) {
		checkCursorDecode(t, s)
		checkCursorDecode(t, base64.RawURLEncoding.EncodeToString([]byte(s)))
	})
}

func checkCursorDecode(t *testing.T, s string) {
	t.Helper()
	c, err := DecodeESOHistoryCursor(s)
	if err != nil {
		if !errors.Is(err, ErrInvalidESOHistoryCursor) {
			t.Fatalf("Decode(%q) returned %v; every rejection must be ErrInvalidESOHistoryCursor", s, err)
		}
		return
	}

	if raw, derr := base64.RawURLEncoding.DecodeString(s); derr != nil || len(raw) > fuzzCursorMaxDecodedBytes {
		t.Fatalf("Decode accepted %q, whose payload is not base64url of at most %d bytes", s, fuzzCursorMaxDecodedBytes)
	}
	if c.ID < 1 {
		t.Fatalf("Decode(%q) accepted id %d; ids start at 1", s, c.ID)
	}
	if c.AttemptAt.Before(fuzzCursorEpoch) || c.AttemptAt.After(fuzzCursorCeiling) {
		t.Fatalf("Decode(%q) accepted attempt time %s outside [epoch, 2100-01-01]", s, c.AttemptAt)
	}

	again, err := DecodeESOHistoryCursor(EncodeESOHistoryCursor(c))
	if err != nil {
		t.Fatalf("re-encoded cursor from %q does not decode: %v", s, err)
	}
	if !again.AttemptAt.Equal(c.AttemptAt) || again.ID != c.ID {
		t.Fatalf("round trip of %q: got %+v, want %+v", s, again, c)
	}
}
