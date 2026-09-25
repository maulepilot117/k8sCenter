package externalsecrets

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kubecenter/kubecenter/internal/store"
)

// The oracle's view of the outcome-only projection, re-derived here rather
// than read from the production drop list, reason allowlist or byte bounds,
// so removing or loosening a guard fails the oracle instead of silently
// agreeing with it.
var (
	fuzzOutcomeOnlyKeys = []string{"attemptAt", "diffKeyCounts", "id", "outcome", "reason"}

	fuzzKnownReasons = []string{
		"SecretSynced", "SecretSyncedError", "SecretDeleted", "SecretMissing",
		"ResourceSynced", "ResourceSyncedError", "ResourceDeleted", "ResourceMissing",
		"Unknown",
	}
)

const (
	fuzzMessageMaxBytes = 2048
	fuzzReasonMaxBytes  = 256
	fuzzOutcome         = "failure"
)

// FuzzESOHistoryProjection fuzzes the history redaction boundary: a stored
// row whose controller text and Secret key names come from fuzz input,
// projected at both levels.
//
//   - Oracle A: projection and sanitization never panic.
//   - Oracle D (outcome-only): the JSON entry carries exactly the permitted
//     keys, the reason is the input itself when allowlisted and "Unknown"
//     otherwise, and no message, key name or resource version appears
//     anywhere in the output.
//   - Full level: controller text comes back as valid UTF-8 without control
//     characters other than \n and \t, with no run of three newlines, within
//     its byte bound, and truncation is reported. Beyond that safety shape,
//     the message and reason must keep the input's printable content: with
//     every control character removed from both sides, the output equals
//     ToValidUTF8(input) when not truncated, or is a prefix of it cut no more
//     than one rune short of the bound, plus "…", when truncated; plain input
//     over bound must be truncated. This fidelity oracle catches a sanitizer
//     that returns "", never reports truncation, or otherwise drops printable
//     text while still passing the safety checks, for mixed input as well as
//     plain. Key names and the resource version come back verbatim.
//
// Key lists are the comma-separated fields of the three key arguments.
func FuzzESOHistoryProjection(f *testing.F) {
	f.Add("key 'prod/db/password' not found", "SecretSyncedError", "DB_USER,DB_PASS", "OLD_KEY", "API_TOKEN", "84211")
	f.Add("", "SecretSynced", "", "", "", "")

	// Teeth.
	f.Add("m", "SecretSynced", "message", "syncedResourceVersion", "diffKeysAdded", "rv") // key names that are wire keys
	f.Add("m", "\x1b]0;owned\x07", "", "", "", "")                                        // OSC title sequence as the reason
	f.Add("\xff\xfe\xfd", "\xc3\x28", "\xff", "", "", "")                                 // invalid UTF-8
	f.Add(strings.Repeat("A", 1<<20), strings.Repeat("R", 1024), "", "", "", "")          // 1 MiB message, oversize reason
	f.Add("a\n\n\n\n\x00\nb\u0085\u009bc", "Unknown", "", "", "", "")                     // newline runs, NUL, C1 NEL and CSI

	f.Fuzz(func(t *testing.T, message, reason, added, removed, changed, rv string) {
		e := store.ESOSyncHistoryEntry{
			ID:                    7,
			AttemptAt:             time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Outcome:               fuzzOutcome,
			Reason:                reason,
			Message:               message,
			DiffKeysAdded:         splitFuzzKeys(added),
			DiffKeysRemoved:       splitFuzzKeys(removed),
			DiffKeysChanged:       splitFuzzKeys(changed),
			SyncedResourceVersion: rv,
		}
		checkOutcomeOnly(t, e)
		checkFull(t, e)
	})
}

func splitFuzzKeys(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func checkOutcomeOnly(t *testing.T, e store.ESOSyncHistoryEntry) {
	t.Helper()
	out, err := json.Marshal(projectHistoryEntry(e, projectionOutcomeOnly))
	if err != nil {
		t.Fatalf("marshal outcome-only entry: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatalf("outcome-only entry is not a JSON object: %v\n%s", err, out)
	}

	keys := slices.Sorted(maps.Keys(fields))
	if !slices.Equal(keys, fuzzOutcomeOnlyKeys) {
		t.Fatalf("outcome-only entry keys = %v; want exactly %v\n%s", keys, fuzzOutcomeOnlyKeys, out)
	}

	var reason string
	if err := json.Unmarshal(fields["reason"], &reason); err != nil {
		t.Fatalf("reason: %v", err)
	}
	wantReason := "Unknown"
	if slices.Contains(fuzzKnownReasons, e.Reason) {
		wantReason = e.Reason
	}
	if reason != wantReason {
		t.Fatalf("outcome-only reason = %q for input %q; want %q", reason, e.Reason, wantReason)
	}

	var counts struct{ Added, Removed, Changed int }
	if err := json.Unmarshal(fields["diffKeyCounts"], &counts); err != nil {
		t.Fatalf("diffKeyCounts: %v", err)
	}
	if counts.Added != len(e.DiffKeysAdded) || counts.Removed != len(e.DiffKeysRemoved) || counts.Changed != len(e.DiffKeysChanged) {
		t.Fatalf("diffKeyCounts = %+v; want %d/%d/%d", counts, len(e.DiffKeysAdded), len(e.DiffKeysRemoved), len(e.DiffKeysChanged))
	}

	// Token-level leak check. Every JSON string token (key or value) the entry
	// may carry is a permitted key, the fixed outcome, the allowlisted reason
	// checked above, or the fixed attempt time. Anything else — a message, a
	// key name, a resource version, in whatever position — is a leak. Tokens
	// are compared decoded, not by byte search, so JSON punctuation in an
	// input cannot match the output's own structure.
	permitted := map[string]bool{fuzzOutcome: true, reason: true, "added": true, "removed": true, "changed": true}
	for _, k := range fuzzOutcomeOnlyKeys {
		permitted[k] = true
	}
	permitted[e.AttemptAt.Format(time.RFC3339Nano)] = true
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tokenize outcome-only entry: %v\n%s", err, out)
		}
		if s, ok := tok.(string); ok && !permitted[s] {
			t.Fatalf("outcome-only entry carries string %q\n%s", s, out)
		}
	}
}

func checkFull(t *testing.T, e store.ESOSyncHistoryEntry) {
	t.Helper()
	dto := projectHistoryEntry(e, projectionFull)
	if dto.Message == nil || dto.MessageTruncated == nil || dto.SyncedResourceVersion == nil ||
		dto.DiffKeysAdded == nil || dto.DiffKeysRemoved == nil || dto.DiffKeysChanged == nil {
		t.Fatal("full entry is missing a field; at full every field must be present")
	}
	checkControllerText(t, "message", *dto.Message, fuzzMessageMaxBytes, *dto.MessageTruncated)
	checkControllerText(t, "reason", dto.Reason, fuzzReasonMaxBytes, false) // the reason carries no truncation flag
	checkTextFidelity(t, "message", e.Message, *dto.Message, fuzzMessageMaxBytes, dto.MessageTruncated)
	checkTextFidelity(t, "reason", e.Reason, dto.Reason, fuzzReasonMaxBytes, nil)
	if *dto.SyncedResourceVersion != e.SyncedResourceVersion {
		t.Fatalf("full syncedResourceVersion = %q; want %q verbatim", *dto.SyncedResourceVersion, e.SyncedResourceVersion)
	}

	for _, pair := range []struct {
		name      string
		got, want []string
	}{
		{"diffKeysAdded", *dto.DiffKeysAdded, e.DiffKeysAdded},
		{"diffKeysRemoved", *dto.DiffKeysRemoved, e.DiffKeysRemoved},
		{"diffKeysChanged", *dto.DiffKeysChanged, e.DiffKeysChanged},
	} {
		if !slices.Equal(pair.got, pair.want) { // nil and [] compare equal
			t.Fatalf("full %s = %q; want %q verbatim", pair.name, pair.got, pair.want)
		}
	}
	if _, err := json.Marshal(dto); err != nil {
		t.Fatalf("marshal full entry: %v", err)
	}
}

func checkControllerText(t *testing.T, field, s string, maxBytes int, truncated bool) {
	t.Helper()
	if !utf8.ValidString(s) {
		t.Fatalf("full %s is not valid UTF-8: %q", field, s)
	}
	if len(s) > maxBytes {
		t.Fatalf("full %s is %d bytes; bound is %d", field, len(s), maxBytes)
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			t.Fatalf("full %s carries control character %U: %q", field, r, s)
		}
	}
	if strings.Contains(s, "\n\n\n") {
		t.Fatalf("full %s carries a run of three newlines: %q", field, s)
	}
	if truncated && !strings.HasSuffix(s, "…") {
		t.Fatalf("full %s reports truncation but does not end in an ellipsis: %q", field, s)
	}
}

// checkTextFidelity asserts that sanitization keeps the input's printable
// content. Control characters are removed from both sides before comparing,
// so the oracle does not restate which controls survive or how newline runs
// collapse; checkControllerText owns that shape. What is left must equal
// ToValidUTF8(in) when the output was not truncated, or be a prefix of it cut
// no more than one rune short of the bound, plus an ellipsis, when it was.
// truncated is nil for fields that carry no truncation flag; for those, any
// output that lost printable content must have the truncated form.
func checkTextFidelity(t *testing.T, field, in, out string, maxBytes int, truncated *bool) {
	t.Helper()
	valid := strings.ToValidUTF8(in, "�")
	want, got := dropControls(valid), dropControls(out)

	// Sanitization only removes runes, so input that fits the bound before it
	// can never need truncating.
	if len(valid) <= maxBytes {
		if truncated != nil && *truncated {
			t.Fatalf("full %s reports truncation for %d-byte input; bound is %d", field, len(valid), maxBytes)
		}
		if got != want {
			t.Fatalf("full %s = %q; want the printable content of %q intact", field, out, in)
		}
		return
	}

	isTruncated := got != want
	if truncated != nil {
		isTruncated = *truncated
		if plain := valid == in && want == in; plain && !isTruncated {
			t.Fatalf("full %s is not reported truncated for %d-byte plain input; bound is %d", field, len(in), maxBytes)
		}
	}
	if !isTruncated {
		if got != want {
			t.Fatalf("full %s = %q; not truncated, so want the printable content of %q intact", field, out, in)
		}
		return
	}

	const ellipsis = "…"
	body, ok := strings.CutSuffix(out, ellipsis)
	if !ok || !strings.HasPrefix(want, dropControls(body)) {
		t.Fatalf("full %s = %q; want a prefix of the printable content of %q plus %q", field, out, in, ellipsis)
	}
	if minBody := maxBytes - len(ellipsis) - (utf8.UTFMax - 1); len(body) < minBody {
		t.Fatalf("full %s keeps %d bytes before the ellipsis; want at least %d", field, len(body), minBody)
	}
}

func dropControls(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
