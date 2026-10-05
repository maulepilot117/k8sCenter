package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// FuzzValidateEvidenceJSON fuzzes the payload/redaction JSON gate, which runs
// over collector output derived from cluster data before it reaches jsonb.
//
//   - Oracle A: validateEvidenceJSON never panics (its escape scanner indexes
//     raw bytes on the strength of json.Valid).
//   - Oracle B: every rejection is ErrIncidentInvalid; an accepted non-empty
//     input is valid UTF-8 JSON.
//   - Oracle C (differential): acceptance agrees with an independent
//     reference that tokenizes escapes with a regexp and applies jsonb's rules
//     (no \u0000; every surrogate escape is a high one immediately followed
//     by a low one).
//   - Oracle D: an accepted input decodes to values whose strings and keys
//     contain no U+0000.
func FuzzValidateEvidenceJSON(f *testing.F) {
	for _, s := range []string{
		`{}`, `[]`, `"x"`, `null`, `{"a":1}`,
		`{"a":"\u0000"}`,               // NUL escape: rejected
		`{"a":"\\u0000"}`,              // literal backslash text: accepted
		`{"a":"\\\u0000"}`,             // escaped backslash, then a NUL escape
		`{"a":"\ud800"}`,               // lone high surrogate
		`{"a":"\udc00"}`,               // lone low surrogate
		`{"a":"\ud83d\ude00"}`,         // valid pair
		`{"a":"\uD83D\uDE00"}`,         // valid pair, upper hex
		`{"a":"\ud800\u0041"}`,         // high followed by a non-surrogate escape
		`{"a":"\ude00\ud83d"}`,         // reversed pair
		`{"a":"\ud83d\n"}`,             // high followed by a short escape
		`{"\u0000":1}`,                 // NUL escape in a key
		"{\"a\":\"\xff\"}",             // invalid UTF-8
		`{"a":`,                        // truncated
		`{"a":"\u00`,                   // truncated escape
		`["\ud83d\ude00\ud83d\ude00"]`, // two pairs
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		err := validateEvidenceJSON("payload", json.RawMessage(raw))
		if err != nil {
			if !errors.Is(err, ErrIncidentInvalid) {
				t.Fatalf("validateEvidenceJSON(%q) = %v; want ErrIncidentInvalid", raw, err)
			}
		} else if len(raw) > 0 && (!utf8.Valid(raw) || !json.Valid(raw)) {
			t.Fatalf("validateEvidenceJSON accepted %q, which is not valid UTF-8 JSON", raw)
		}

		if len(raw) == 0 || !utf8.Valid(raw) || !json.Valid(raw) {
			return
		}
		if want := referenceJSONBEscapesOK(string(raw)); (err == nil) != want {
			t.Fatalf("validateEvidenceJSON(%q) accepted=%v; reference says %v", raw, err == nil, want)
		}
		if err == nil {
			var v any
			if json.Unmarshal(raw, &v) == nil && containsNUL(v) {
				t.Fatalf("validateEvidenceJSON accepted %q, which decodes to a NUL", raw)
			}
		}
	})
}

// fuzzJSONEscape matches one JSON string escape. Leftmost, non-overlapping
// matching consumes "\\" as a unit, so it tokenizes escapes the way a JSON
// decoder does.
var fuzzJSONEscape = regexp.MustCompile(`\\(?:u[0-9A-Fa-f]{4}|.)`)

// referenceJSONBEscapesOK reports whether jsonb would accept the escapes of
// valid JSON s.
func referenceJSONBEscapesOK(s string) bool {
	locs := fuzzJSONEscape.FindAllStringIndex(s, -1)
	for i := 0; i < len(locs); i++ {
		esc := s[locs[i][0]:locs[i][1]]
		if len(esc) != 6 {
			continue
		}
		v, _ := strconv.ParseUint(esc[2:], 16, 32)
		switch {
		case v == 0:
			return false
		case v >= 0xDC00 && v <= 0xDFFF:
			return false
		case v >= 0xD800 && v <= 0xDBFF:
			if i+1 >= len(locs) || locs[i+1][0] != locs[i][1] {
				return false
			}
			next := s[locs[i+1][0]:locs[i+1][1]]
			if len(next) != 6 {
				return false
			}
			lo, _ := strconv.ParseUint(next[2:], 16, 32)
			if lo < 0xDC00 || lo > 0xDFFF {
				return false
			}
			i++
		}
	}
	return true
}

func containsNUL(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.ContainsRune(x, 0)
	case []any:
		for _, e := range x {
			if containsNUL(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range x {
			if strings.ContainsRune(k, 0) || containsNUL(e) {
				return true
			}
		}
	}
	return false
}

// fuzzEvidenceCursorMaxDecodedBytes is re-derived here rather than read from
// maxEvidenceCursorBytes, so the oracle notices the length guard being removed
// instead of agreeing with it. fuzzCursorEpoch and fuzzCursorCeiling
// are shared with eso_history_fuzz_test.go.
const fuzzEvidenceCursorMaxDecodedBytes = 80

// FuzzDecodeEvidenceCursor fuzzes the evidence list cursor decoder, which turns
// a client-supplied ?cursor= string into typed keyset values.
//
//   - Oracle A: DecodeEvidenceCursor never panics.
//   - Oracle B: every rejection is ErrInvalidEvidenceCursor, and a rejection
//     never comes with a usable cursor (the zero value is returned).
//   - Oracle C: any accepted cursor is well-formed (a non-nil UUID spelled in
//     canonical form, collected time in [epoch, 2100-01-01], payload at most 80
//     bytes), carries exactly the values its "<micros>:<uuid>" payload spells,
//     and Decode∘Encode reproduces it exactly.
//
// Each input is decoded twice: as given, and base64url-encoded first, so the
// raw payload seeds below reach the field parser.
func FuzzDecodeEvidenceCursor(f *testing.F) {
	id := uuid.MustParse("0f8fad5b-d9cb-469f-a165-70867728950e")

	// Valid cursors, in encoded form.
	f.Add(EncodeEvidenceCursor(EvidenceCursor{CollectedAt: time.Date(2026, 10, 5, 12, 0, 0, 123456000, time.UTC), ID: id}))
	f.Add(EncodeEvidenceCursor(EvidenceCursor{CollectedAt: fuzzCursorEpoch, ID: id}))

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
		checkEvidenceCursorDecode(t, s)
		checkEvidenceCursorDecode(t, base64.RawURLEncoding.EncodeToString([]byte(s)))
	})
}

func checkEvidenceCursorDecode(t *testing.T, s string) {
	t.Helper()
	c, err := DecodeEvidenceCursor(s)
	if err != nil {
		if !errors.Is(err, ErrInvalidEvidenceCursor) {
			t.Fatalf("Decode(%q) returned %v; every rejection must be ErrInvalidEvidenceCursor", s, err)
		}
		if c != (EvidenceCursor{}) {
			t.Fatalf("Decode(%q) rejected the input but returned a usable cursor %+v", s, c)
		}
		return
	}

	raw, derr := base64.RawURLEncoding.DecodeString(s)
	if derr != nil || len(raw) > fuzzEvidenceCursorMaxDecodedBytes {
		t.Fatalf("Decode accepted %q, whose payload is not base64url of at most %d bytes", s, fuzzEvidenceCursorMaxDecodedBytes)
	}
	// The accepted values must be the ones the payload spells, parsed
	// independently, and the id text must be the canonical spelling.
	microsText, idText, ok := strings.Cut(string(raw), ":")
	micros, merr := strconv.ParseInt(microsText, 10, 64)
	if !ok || merr != nil || c.CollectedAt.UnixMicro() != micros || c.ID.String() != idText {
		t.Fatalf("Decode(%q) = %+v; payload %q does not spell those values", s, c, raw)
	}
	if c.ID == uuid.Nil {
		t.Fatalf("Decode(%q) accepted the nil UUID", s)
	}
	if c.CollectedAt.Before(fuzzCursorEpoch) || c.CollectedAt.After(fuzzCursorCeiling) {
		t.Fatalf("Decode(%q) accepted collected time %s outside [epoch, 2100-01-01]", s, c.CollectedAt)
	}

	again, err := DecodeEvidenceCursor(EncodeEvidenceCursor(c))
	if err != nil {
		t.Fatalf("re-encoded cursor from %q does not decode: %v", s, err)
	}
	if !again.CollectedAt.Equal(c.CollectedAt) || again.ID != c.ID {
		t.Fatalf("round trip of %q: got %+v, want %+v", s, again, c)
	}
}
