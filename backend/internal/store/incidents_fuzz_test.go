package store

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fuzzIncidentCursorMaxDecodedBytes is re-derived here rather than read from
// the production constant, so the oracle notices the length guard being
// removed instead of agreeing with it. fuzzCursorEpoch and fuzzCursorCeiling
// are shared with eso_history_fuzz_test.go.
const fuzzIncidentCursorMaxDecodedBytes = 80

// FuzzDecodeIncidentCursor fuzzes the incident list cursor decoder, which
// turns a client-supplied ?cursor= string into typed keyset values.
//
//   - Oracle A: DecodeIncidentCursor never panics.
//   - Oracle B: every rejection is ErrInvalidIncidentCursor, and a rejection
//     never comes with a usable cursor (the zero value is returned).
//   - Oracle C: any accepted cursor is well-formed (a non-nil UUID spelled in
//     canonical form, created time in [epoch, 2100-01-01], payload at most 80
//     bytes), carries exactly the values its "<micros>:<uuid>" payload spells,
//     and Decode∘Encode reproduces it exactly.
//
// Each input is decoded twice: as given, and base64url-encoded first, so the
// raw payload seeds below reach the field parser.
func FuzzDecodeIncidentCursor(f *testing.F) {
	id := uuid.MustParse("0f8fad5b-d9cb-469f-a165-70867728950e")

	// Valid cursors, in encoded form.
	f.Add(EncodeIncidentCursor(IncidentCursor{CreatedAt: time.Date(2026, 10, 5, 12, 0, 0, 123456000, time.UTC), ID: id}))
	f.Add(EncodeIncidentCursor(IncidentCursor{CreatedAt: fuzzCursorEpoch, ID: id}))

	// Teeth: each seed is aimed at one guard.
	f.Add("")
	f.Add("=")
	f.Add("AAAA")
	f.Add(":")
	f.Add("1:" + id.String() + ":x")                       // second separator
	f.Add("-1:" + id.String())                             // negative micros
	f.Add("4102444800000001:" + id.String())               // one microsecond past the 2100 ceiling
	f.Add("9223372036854775808:" + id.String())            // overflows int64
	f.Add("1:" + uuid.Nil.String())                        // nil id
	f.Add("1:" + strings.ToUpper(id.String()))             // non-canonical case
	f.Add("1:{" + id.String() + "}")                       // braced form uuid.Parse accepts
	f.Add("1:urn:uuid:" + id.String())                     // URN form uuid.Parse accepts
	f.Add("1:" + strings.ReplaceAll(id.String(), "-", "")) // hyphenless form uuid.Parse accepts
	f.Add(strings.Repeat("0", 50) + "1:" + id.String())    // valid digits in an 88-byte payload: the length guard
	f.Add(strings.Repeat("A", 4096))                       // 4 KiB base64 blob
	f.Add("\x00\xff")

	f.Fuzz(func(t *testing.T, s string) {
		checkIncidentCursorDecode(t, s)
		checkIncidentCursorDecode(t, base64.RawURLEncoding.EncodeToString([]byte(s)))
	})
}

func checkIncidentCursorDecode(t *testing.T, s string) {
	t.Helper()
	c, err := DecodeIncidentCursor(s)
	if err != nil {
		if !errors.Is(err, ErrInvalidIncidentCursor) {
			t.Fatalf("Decode(%q) returned %v; every rejection must be ErrInvalidIncidentCursor", s, err)
		}
		if c != (IncidentCursor{}) {
			t.Fatalf("Decode(%q) rejected the input but returned a usable cursor %+v", s, c)
		}
		return
	}

	raw, derr := base64.RawURLEncoding.DecodeString(s)
	if derr != nil || len(raw) > fuzzIncidentCursorMaxDecodedBytes {
		t.Fatalf("Decode accepted %q, whose payload is not base64url of at most %d bytes", s, fuzzIncidentCursorMaxDecodedBytes)
	}
	// The accepted values must be the ones the payload spells, parsed
	// independently, and the id text must be the canonical spelling.
	microsText, idText, ok := strings.Cut(string(raw), ":")
	micros, merr := strconv.ParseInt(microsText, 10, 64)
	if !ok || merr != nil || c.CreatedAt.UnixMicro() != micros || c.ID.String() != idText {
		t.Fatalf("Decode(%q) = %+v; payload %q does not spell those values", s, c, raw)
	}
	if c.ID == uuid.Nil {
		t.Fatalf("Decode(%q) accepted the nil UUID", s)
	}
	if c.CreatedAt.Before(fuzzCursorEpoch) || c.CreatedAt.After(fuzzCursorCeiling) {
		t.Fatalf("Decode(%q) accepted created time %s outside [epoch, 2100-01-01]", s, c.CreatedAt)
	}

	again, err := DecodeIncidentCursor(EncodeIncidentCursor(c))
	if err != nil {
		t.Fatalf("re-encoded cursor from %q does not decode: %v", s, err)
	}
	if !again.CreatedAt.Equal(c.CreatedAt) || again.ID != c.ID {
		t.Fatalf("round trip of %q: got %+v, want %+v", s, again, c)
	}
}
