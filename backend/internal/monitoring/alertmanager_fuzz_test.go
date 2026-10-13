package monitoring

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fuzzAlertsBodyLimit is re-derived rather than read from the production
// constant, so the oracle notices the size guard moving instead of agreeing
// with it.
const fuzzAlertsBodyLimit = 16 << 20

// fuzzAlertsRejections are the only error texts decodeAlerts may produce.
// They are fixed strings: none of them can quote a byte of the answer.
var fuzzAlertsRejections = map[string]bool{
	ErrAlertmanagerUnreadable.Error() + ": answer over 16777216 bytes":     true,
	ErrAlertmanagerUnreadable.Error() + ": answer is not a JSON array":     true,
	ErrAlertmanagerUnreadable.Error() + ": answer is not a list of alerts": true,
}

// FuzzDecodeAlerts fuzzes the Alertmanager alerts-answer decoder, which turns
// bytes from an operator-registered remote Alertmanager into typed alerts.
//
//   - Oracle A: decodeAlerts never panics.
//   - Oracle B: every rejection is ErrAlertmanagerUnreadable, comes with no
//     alerts, and carries one of the fixed texts above, so a decode error never
//     echoes the answer's bytes (the decoder's own error is dropped, not
//     wrapped).
//   - Oracle C: an accepted answer is within the size cap, is valid JSON whose
//     first non-space byte is '[', and yields exactly one alert per array
//     element, each element being a JSON object.
func FuzzDecodeAlerts(f *testing.F) {
	// Valid answers.
	f.Add([]byte(`[]`))
	f.Add([]byte(` [{"labels":{"alertname":"KubePodCrashLooping","severity":"critical"},"status":{"state":"active"}}]`))
	f.Add([]byte(`[{"labels":{"alertname":"Watchdog"},"status":{"state":"active"}},{"labels":{},"status":{"state":"suppressed"}}]`))

	// Teeth: each seed is aimed at one guard.
	f.Add([]byte(``))                                            // empty answer
	f.Add([]byte(" \t\r\n"))                                     // whitespace only
	f.Add([]byte(`null`))                                        // Unmarshal accepts null into a slice: the '[' guard
	f.Add([]byte(`{"status":"success","data":[]}`))              // an object, not an array
	f.Add([]byte(`[1,"two",null]`))                              // non-object elements
	f.Add([]byte(`[{"labels":{"alertname":1}}]`))                // a type error, whose text names the field
	f.Add([]byte(`[{"labels":{"alertname":"SECRET-BODY-BYTES"`)) // truncated, carrying a marker
	f.Add([]byte(`[{"labels":{"alertname":"\ud800"}}]`))         // lone surrogate
	f.Add([]byte("[\xff\xfe]"))                                  // invalid UTF-8
	f.Add([]byte(`[{}]  trailing`))                              // trailing garbage
	f.Add([]byte(strings.Repeat("[", 4096)))                     // deep nesting

	f.Fuzz(func(t *testing.T, body []byte) {
		alerts, err := decodeAlerts(body)
		if err != nil {
			if !errors.Is(err, ErrAlertmanagerUnreadable) {
				t.Fatalf("decodeAlerts(%q) = %v; every rejection must be ErrAlertmanagerUnreadable", body, err)
			}
			if alerts != nil {
				t.Fatalf("decodeAlerts(%q) rejected the answer but returned %d alerts", body, len(alerts))
			}
			if !fuzzAlertsRejections[err.Error()] {
				t.Fatalf("decodeAlerts(%q) error %q is not one of the fixed texts; it may quote the answer", body, err.Error())
			}
			return
		}

		if len(body) > fuzzAlertsBodyLimit {
			t.Fatalf("accepted a %d-byte answer over the %d-byte cap", len(body), fuzzAlertsBodyLimit)
		}
		if !json.Valid(body) {
			t.Fatalf("accepted invalid JSON %q", body)
		}
		trimmed := bytes.TrimLeft(body, " \t\r\n")
		if len(trimmed) == 0 || trimmed[0] != '[' {
			t.Fatalf("accepted %q, which is not a JSON array", body)
		}
		var raw []json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("accepted %q, which does not decode as an array: %v", body, err)
		}
		if len(raw) != len(alerts) {
			t.Fatalf("decodeAlerts(%q) returned %d alerts for %d elements", body, len(alerts), len(raw))
		}
		for i, el := range raw {
			if e := bytes.TrimLeft(el, " \t\r\n"); len(e) == 0 || (e[0] != '{' && !bytes.Equal(e, []byte("null"))) {
				t.Fatalf("decodeAlerts(%q) accepted element %d %q, which is not an object", body, i, el)
			}
		}
	})
}
