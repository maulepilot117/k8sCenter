package incidents

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// FuzzEventProjection drives projectEvents (plan §3.6) with adversarial
// events. The contract is re-derived here from wire literals, not read from
// the production constants, so a widened allowlist, a dropped sanitizer or
// a silent cut fails the oracle instead of agreeing with it.
//
// Oracles:
//
//	A  no panic on any event shape (huge or hostile strings, odd
//	   involvedObject, missing timestamps, any count or event number);
//	B  the payload is never longer than the configured bound, down to the
//	   smallest bound the Redactor accepts;
//	D  a canary planted in any field outside the allowlist (object metadata,
//	   annotations, labels, action, reporting controller, related
//	   object, source host, series) never reaches the payload; every event
//	   carries only allowlisted keys; every string in the payload is valid
//	   UTF-8 without control characters;
//	T  a cut (for count, for size, or of a string past the field bound) is
//	   always flagged, in the payload and in the redaction metadata, and a
//	   small list under a wide bound is never flagged.
const (
	fuzzEventCanary     = "CANARY-e7a1-event-plaintext"
	fuzzEventFieldBound = 4096
	fuzzEventMaxEvents  = 200
	fuzzEventMinBound   = 64
	fuzzEventWideBound  = 1 << 20
	fuzzEventMaxString  = 64 << 10 // harness bound on one input string
)

var fuzzEventAllowedKeys = map[string]bool{
	"type": true, "reason": true, "message": true, "count": true, "firstTimestamp": true,
	"lastTimestamp": true, "eventTime": true, "involvedObject": true, "sourceComponent": true,
}

var fuzzEventObjectKeys = map[string]bool{"kind": true, "name": true, "uid": true}

// fuzzEventBound maps a selector to a byte bound: 0 is the wide default,
// 1 the minimum, anything else a small bound that forces size cuts.
func fuzzEventBound(sel uint8) int {
	switch sel {
	case 0:
		return fuzzEventWideBound
	case 1:
		return fuzzEventMinBound
	default:
		return fuzzEventMinBound + int(sel)*96
	}
}

// fuzzEvent builds one event. The canary goes in every field outside the
// allowlist; the fuzzed strings go in the allowlisted ones. kind, name and
// uid are the involvedObject; i varies per-event fields.
func fuzzEvent(i int, message, reason, kind, name, uid, component string, count int32, withTimes bool) corev1.Event {
	ev := corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: fuzzEventCanary, Namespace: fuzzEventCanary, UID: types.UID("ev-" + itoa(int32(i))),
			Annotations: map[string]string{"note": fuzzEventCanary},
			Labels:      map[string]string{fuzzEventCanary: fuzzEventCanary},
		},
		InvolvedObject:      corev1.ObjectReference{Kind: kind, Name: name, UID: types.UID(uid), Namespace: fuzzEventCanary, FieldPath: fuzzEventCanary},
		Type:                reason,
		Reason:              reason,
		Message:             message,
		Count:               count,
		Source:              corev1.EventSource{Component: component, Host: fuzzEventCanary},
		Action:              fuzzEventCanary,
		Related:             &corev1.ObjectReference{Name: fuzzEventCanary},
		ReportingController: fuzzEventCanary,
		ReportingInstance:   fuzzEventCanary,
	}
	if i%2 == 1 {
		ev.Series = &corev1.EventSeries{Count: count + 1}
	}
	if withTimes {
		at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
		ev.FirstTimestamp = metav1.NewTime(at.Add(-time.Hour))
		ev.LastTimestamp = metav1.NewTime(at)
		if i%3 == 0 {
			ev.EventTime = metav1.NewMicroTime(at.Add(time.Second))
		}
	}
	return ev
}

func FuzzEventProjection(f *testing.F) {
	// Realistic, then teeth: control characters and invalid UTF-8 in the
	// message (sanitizer), a message past the field bound and a list past
	// the event cap (truncation honesty), a Secret involvedObject (secret
	// derivation), a bound that cannot hold even one event (size cut to
	// empty), and an event with no timestamps (nil observedAt).
	f.Add("Back-off restarting failed container", "BackOff", "Pod", "web-0", "uid-1", "kubelet", int32(12), uint8(3), uint8(0), true)
	f.Add("line\x00one\x1b[31m\xff\xfe"+fuzzEventCanary, "Bad\x00Reason", "Pod", "web-0", "uid-1", "kubelet", int32(1), uint8(1), uint8(0), true)
	f.Add(strings.Repeat("m", fuzzEventFieldBound+1), "Spam", "Deployment", "web", "uid-2", "controller", int32(3), uint8(2), uint8(0), true)
	f.Add("repeat", "Spam", "Deployment", "web", "uid-2", "controller", int32(1), uint8(255), uint8(0), true)
	f.Add("synced", "Synced", "Secret", "db", "uid-3", "eso", int32(1), uint8(2), uint8(0), true)
	f.Add("too big for the bound", "Big", "Pod", "web-0", "uid-1", "kubelet", int32(1), uint8(4), uint8(1), true)
	f.Add("no clock", "Timeless", "Pod", "web-0", "uid-1", "kubelet", int32(0), uint8(1), uint8(0), false)
	f.Add("", "", "", "", "", "", int32(-1), uint8(0), uint8(7), false)
	// Sanitizes into the canary: carried by the input, not a leak.
	f.Add("CANAR\x0eY-e7a1-event-plaintext", "0", "0", "0", "0", "0", int32(1), uint8(1), uint8(0), true)
	// Over the field bound only before control characters are removed:
	// not a cut.
	f.Add(strings.Repeat("m", fuzzEventFieldBound-3)+"\x00\x00\x04\x00", "Spam", "Deployment", "web", "uid-2", "controller", int32(3), uint8(2), uint8(0), true)

	f.Fuzz(func(t *testing.T, message, reason, kind, name, uid, component string, count int32, n uint8, boundSel uint8, withTimes bool) {
		for _, s := range []*string{&message, &reason, &kind, &name, &uid, &component} {
			if len(*s) > fuzzEventMaxString {
				*s = (*s)[:fuzzEventMaxString]
			}
		}
		bound := fuzzEventBound(boundSel)
		r, err := NewRedactor(bound)
		if err != nil {
			t.Fatalf("bound %d rejected: %v", bound, err)
		}
		events := make([]corev1.Event, 0, int(n)+1)
		for i := range int(n) + 1 {
			events = append(events, fuzzEvent(i, message, reason, kind, name, uid, component, count, withTimes))
		}
		observed := len(events)

		out := projectEvents(r, events, nil, false, false) // oracle A: returns

		// Oracle B.
		if len(out.payload) > bound {
			t.Fatalf("payload %d bytes exceeds bound %d", len(out.payload), bound)
		}

		// Oracle D: shape, allowlist, canary, text.
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(out.payload, &payload); err != nil {
			t.Fatalf("payload is not a JSON object: %v: %s", err, out.payload)
		}
		for k := range payload {
			if k != "events" && k != "observed" && k != "truncated" {
				t.Fatalf("payload carries key %q", k)
			}
		}
		// The fuzzed strings land in allowlisted fields, so the canary is
		// only a leak when no input carried it there. An input that
		// sanitizes INTO the canary (a control character inside it) also
		// carried it, so the comparison is on the scrubbed inputs.
		inputs := fuzzScrub(message + reason + kind + name + uid + component)
		if !strings.Contains(inputs, fuzzEventCanary) && strings.Contains(string(out.payload), fuzzEventCanary) {
			t.Fatalf("canary reached the payload: %s", out.payload)
		}
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(payload["events"], &list); err != nil {
			t.Fatalf("events: %v", err)
		}
		for _, ev := range list {
			for k, v := range ev {
				if !fuzzEventAllowedKeys[k] {
					t.Fatalf("event carries key %q", k)
				}
				if k == "involvedObject" {
					var ref map[string]string
					if err := json.Unmarshal(v, &ref); err != nil {
						t.Fatalf("involvedObject: %v", err)
					}
					for rk, rv := range ref {
						if !fuzzEventObjectKeys[rk] {
							t.Fatalf("involvedObject carries key %q", rk)
						}
						fuzzCheckEventText(t, rk, rv)
					}
					continue
				}
				var s string
				if json.Unmarshal(v, &s) == nil {
					fuzzCheckEventText(t, k, s)
				}
			}
		}

		// Oracle T.
		var truncated bool
		if err := json.Unmarshal(payload["truncated"], &truncated); err != nil {
			t.Fatalf("truncated: %v", err)
		}
		if truncated != out.meta.Truncated {
			t.Fatalf("payload truncated=%v but metadata truncated=%v", truncated, out.meta.Truncated)
		}
		var kept int
		if err := json.Unmarshal(payload["observed"], &kept); err != nil {
			t.Fatalf("observed: %v", err)
		}
		cutForCount := observed > fuzzEventMaxEvents
		cutForSize := len(list) < min(observed, fuzzEventMaxEvents)
		cutString := false
		// Sanitization (invalid UTF-8 replaced, control characters
		// removed) happens before the bound, so the cut is judged on the
		// scrubbed string.
		for _, s := range []string{message, reason, kind, name, uid, component} {
			if len(fuzzScrub(s)) > fuzzEventFieldBound {
				cutString = true
			}
		}
		if (cutForCount || cutForSize || cutString) && !truncated {
			t.Fatalf("a cut was not flagged (count=%v size=%v string=%v)", cutForCount, cutForSize, cutString)
		}
		if kept != min(observed, fuzzEventMaxEvents) {
			t.Fatalf("observed=%d, want %d", kept, min(observed, fuzzEventMaxEvents))
		}
		small := observed <= 8 && bound == fuzzEventWideBound
		for _, s := range []string{message, reason, kind, name, uid, component} {
			if len(s) > 1000 {
				small = false
			}
		}
		if small && truncated {
			t.Fatalf("a small list under the wide bound was flagged truncated")
		}
		if !truncated && len(list) != observed {
			t.Fatalf("unflagged payload kept %d of %d events", len(list), observed)
		}

		// Secret derivation follows the involvedObject kind; observedAt
		// follows the timestamps.
		if (kind == "Secret") != out.meta.SecretDerived {
			t.Fatalf("secretDerived=%v for kind %q", out.meta.SecretDerived, kind)
		}
		if withTimes != (out.observedAt != nil) {
			t.Fatalf("observedAt=%v with timestamps=%v", out.observedAt, withTimes)
		}
	})
}

// fuzzCheckEventText is the text contract: valid UTF-8, no C0/C1 control
// characters or DEL other than newline and tab, and at most the field bound.
// fuzzScrub is the oracle's own re-derivation of the text contract's
// removals (invalid UTF-8 to U+FFFD, control characters other than newline
// and tab dropped), used only to tell an input that carried the canary from
// a leak.
func fuzzScrub(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

func fuzzCheckEventText(t *testing.T, field, s string) {
	t.Helper()
	if !utf8.ValidString(s) {
		t.Fatalf("%s is not valid UTF-8: %q", field, s)
	}
	if len(s) > fuzzEventFieldBound {
		t.Fatalf("%s is %d bytes, past the field bound", field, len(s))
	}
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("%s keeps control character %U: %q", field, r, s)
		}
	}
}
