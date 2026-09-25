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
//     keys, the reason is an allowlisted token, and no message, key name or
//     resource version appears anywhere in the output.
//   - Full level: controller text comes back as valid UTF-8 without control
//     characters other than \n and \t, with no run of three newlines, within
//     its byte bound, and truncation is reported. For input that is already
//     plain (valid UTF-8, no control character at all — so no \n or \t
//     either), the sanitized message and reason must additionally equal the
//     input verbatim when within bound, or a truncated prefix of it plus "…"
//     when over bound, with the truncation flag set accordingly — this is a
//     fidelity oracle, not just a safety-shape check, so it catches a
//     sanitizer that returns "", never reports truncation, or otherwise
//     drops content while still passing the safety checks. Key names and the
//     resource version come back verbatim.
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
	if !slices.Contains(fuzzKnownReasons, reason) {
		t.Fatalf("outcome-only reason %q is not an allowlisted token", reason)
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
	checkPlainFidelity(t, "message", e.Message, *dto.Message, fuzzMessageMaxBytes, dto.MessageTruncated)
	checkPlainFidelity(t, "reason", e.Reason, dto.Reason, fuzzReasonMaxBytes, nil)

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

// checkPlainFidelity asserts that input with nothing to sanitize survives
// sanitization: verbatim within bound, otherwise a prefix of the input cut no
// more than one rune short of the bound, plus an ellipsis. truncated is nil
// for fields that carry no truncation flag. Input that is not plain is left
// to checkControllerText's safety-shape checks.
func checkPlainFidelity(t *testing.T, field, in, out string, maxBytes int, truncated *bool) {
	t.Helper()
	if !utf8.ValidString(in) || strings.IndexFunc(in, unicode.IsControl) >= 0 {
		return
	}
	over := len(in) > maxBytes
	if truncated != nil && *truncated != over {
		t.Fatalf("full %s truncated = %t for %d-byte plain input; bound is %d", field, *truncated, len(in), maxBytes)
	}
	if !over {
		if out != in {
			t.Fatalf("full %s = %q; want plain input %q verbatim", field, out, in)
		}
		return
	}
	const ellipsis = "…"
	prefix, ok := strings.CutSuffix(out, ellipsis)
	if !ok || !strings.HasPrefix(in, prefix) {
		t.Fatalf("full %s = %q; want a prefix of the plain input plus %q", field, out, ellipsis)
	}
	if minPrefix := maxBytes - len(ellipsis) - (utf8.UTFMax - 1); len(prefix) < minPrefix {
		t.Fatalf("full %s keeps %d bytes of %d-byte plain input; want at least %d", field, len(prefix), len(in), minPrefix)
	}
}
