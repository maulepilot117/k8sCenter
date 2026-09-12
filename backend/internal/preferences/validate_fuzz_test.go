package preferences

import (
	"encoding/json"
	"testing"
)

// FuzzPreferencesValidators asserts that the two envelope validators are
// crash-safe on arbitrary input AND that they contain it.
//
// These are the containment boundary for everything that reaches the stored
// config column, and the stored value is read back and rendered by a browser
// client and a mobile app. So crash-safety alone is not the whole contract:
// the validators are also the only thing standing between a hostile request
// body and a JSON document those consumers will trust.
//
// Two oracles:
//
//	A. No panic. Errors and zero values are fine.
//	B. Containment. When validation SUCCEEDS, the bytes it returns must be a
//	   JSON object whose keys are exactly the envelope's allowlist — no more,
//	   no fewer. The validators achieve this by re-marshalling their own typed
//	   struct rather than passing the caller's bytes through, so oracle B is
//	   what fails if that mechanism is ever replaced with a pass-through.
func FuzzPreferencesValidators(f *testing.F) {
	// Well-formed envelopes: the mutator explores around these.
	f.Add([]byte(`{"schemaVersion":1,"resourceKind":"pods","namespace":"","search":"","statusFilter":"all","sortKey":"name","sortDir":"asc"}`))
	f.Add([]byte(`{"schemaVersion":1,"resourceKind":"deployments","group":"","version":"","namespace":"prod","name":"api","uid":"8b1e","displayKind":"Deployment"}`))

	// Structural teeth — shapes that have historically broken JSON handling.
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`"a string, not an object"`))
	f.Add([]byte(`{"schemaVersion":`))                                                     // truncated
	f.Add([]byte(`{"schemaVersion":1} trailing`))                                          // trailing bytes
	f.Add([]byte(`{"schemaVersion":1,"schemaVersion":2}`))                                 // duplicate key
	f.Add([]byte(`{"schemaVersion":1.5}`))                                                 // non-integer version
	f.Add([]byte(`{"schemaVersion":99999999999999999999}`))                                // overflowing number
	f.Add([]byte(`{"resourceKind":{"nested":"object"}}`))                                  // wrong type for a string field
	f.Add([]byte(`{"namespace":["a","list"]}`))                                            // wrong type again
	f.Add([]byte("{\"schemaVersion\":1,\"resourceKind\":\"pods\",\"search\":\"\u0000\"}")) // embedded NUL
	f.Add([]byte("{\"schemaVersion\":1,\"resourceKind\":\"pods\",\"search\":\"\u202e\"}")) // bidi override
	f.Add([]byte(`{"schemaVersion":1,"resourceKind":"pods","__proto__":{"x":1}}`))         // prototype-pollution shape
	f.Add([]byte(`{"schemaVersion":1,"resourceKind":"PODS"}`))                             // case variation on a kind
	f.Add([]byte(`{"schemaVersion":1,"resourceKind":"pods","namespace":"../../etc"}`))
	f.Add([]byte(`{"schemaVersion":1,"resourceKind":"deployments","namespace":"a/b","name":"c/d"}`)) // separator in a dedup component

	savedViewKeys := map[string]struct{}{
		"schemaVersion": {}, "resourceKind": {}, "namespace": {},
		"search": {}, "statusFilter": {}, "sortKey": {}, "sortDir": {},
	}
	pinKeys := map[string]struct{}{
		"schemaVersion": {}, "resourceKind": {}, "group": {}, "version": {},
		"namespace": {}, "name": {}, "uid": {}, "displayKind": {},
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if _, normalized, err := ValidateSavedView(data); err == nil {
			assertExactKeys(t, "saved view", normalized, savedViewKeys)
		}
		if cfg, normalized, err := ValidatePin(data); err == nil {
			assertExactKeys(t, "pin", normalized, pinKeys)

			// The dedup key is built by joining three validated fields with a
			// separator. If a validated field could contain that separator,
			// two different resources could collide on one identity.
			if got := PinDedupKey(cfg); countSlashes(got) != 2 {
				t.Fatalf("pin dedup key %q has %d separators; want exactly 2 "+
					"(a validated field smuggled one through)", got, countSlashes(got))
			}
		}
	})
}

// assertExactKeys is oracle B: the stored bytes carry the envelope's keys and
// nothing else.
func assertExactKeys(t *testing.T, label string, normalized json.RawMessage, want map[string]struct{}) {
	t.Helper()

	var got map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatalf("%s: accepted config did not re-marshal to a JSON object: %v", label, err)
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Fatalf("%s: unlisted key %q reached the stored config", label, k)
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Fatalf("%s: allowlisted key %q is missing from the stored config", label, k)
		}
	}
}

func countSlashes(s string) int {
	n := 0
	for _, r := range s {
		if r == '/' {
			n++
		}
	}
	return n
}
