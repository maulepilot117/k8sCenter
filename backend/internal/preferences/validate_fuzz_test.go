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

// FuzzValidateDashboardLayout drives the dashboard-layout parse seam.
//
// The layout envelope is the first one in this package with nested structure —
// a variable-length array of objects, each with its own map — so containment
// has two levels and a pass-through could hide at either. It is also the first
// whose acceptance depends on relationships BETWEEN elements (no two items
// overlap, no two share an instanceId or an identity), which single-value
// bounds checking cannot express.
//
// Three oracles:
//
//	A. No panic. Errors and zero values are fine.
//	B. Containment, at both levels. An accepted layout must re-marshal to an
//	   object whose keys are exactly the envelope's allowlist, each of whose
//	   items carries exactly the item allowlist. This is what fails if the
//	   re-marshal-my-own-struct mechanism is ever replaced with a pass-through.
//	C. Normalization is a fixed point. Re-validating the bytes the validator
//	   returned must accept and return byte-identical output. Without this a
//	   stored row could decode differently than it was written — the value
//	   means one thing on save and another on the next read — and a client
//	   round-tripping a layout it just saved could be refused its own data.
//	D. Every accepted item is really on the grid, restated here in arithmetic
//	   that cannot wrap. Oracle D found a live bug on its first run: the
//	   validator bounded placements with `x+w > columns`, which overflows —
//	   x=math.MaxInt64 with w=1 folds to a large negative and passes. A and B
//	   and C were all satisfied by that layout (no panic, exact keys, stable
//	   round trip), so only an independent restatement of the bound could
//	   catch it.
func FuzzValidateDashboardLayout(f *testing.F) {
	// Well-formed envelopes: the mutator explores around these.
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cluster-health","x":0,"y":0,"w":4,"h":4}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"b","id":"pod-status","x":6,"y":0,"w":6,"h":4}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"p","id":"nodes","x":0,"y":0,"w":6,"h":4,"params":{"namespace":"prod"}},{"instanceId":"s","id":"nodes","x":6,"y":0,"w":6,"h":4,"params":{"namespace":"staging"}}]}`))

	// Geometry teeth — mutated from the table cases in dashboard_test.go. Each
	// is one integer away from an accepted layout, which is exactly the
	// neighbourhood a bounds bug lives in.
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":8,"y":198,"w":4,"h":2}]}`))                  // flush against both bounds
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":9,"y":0,"w":4,"h":2}]}`))                    // one column past the edge
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":199,"w":4,"h":2}]}`))                  // one row past the cap
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":0,"h":0}]}`))                    // zero-area
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":-1,"y":-1,"w":4,"h":4}]}`))                  // negative origin
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":-9223372036854775808,"h":4}]}`)) // int64 min, to probe x+w overflow
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":9223372036854775807,"y":0,"w":1,"h":1}]}`))  // int64 max, same
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":9223372036854775807,"w":1,"h":1}]}`))  // int64 max on the other axis
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":9223372036854775807,"h":1}]}`))  // int64 max size
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":1,"h":9223372036854775807}]}`))

	// Relationship teeth: the checks that need two items to trip.
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"a","id":"pod-status","x":6,"y":0,"w":6,"h":4}]}`))   // repeated instanceId
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"b","id":"nodes","x":6,"y":0,"w":6,"h":4}]}`))        // identical identity
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":12,"h":10},{"instanceId":"b","id":"pod-status","x":4,"y":4,"w":2,"h":2}]}`)) // containment, not edge crossing
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"b","id":"pod-status","x":6,"y":4,"w":6,"h":4}]}`))   // corners touch, must be accepted

	// Param teeth: the only free-text a layout carries.
	f.Add([]byte("{\"schemaVersion\":1,\"scope\":\"overview\",\"columns\":12,\"items\":[{\"instanceId\":\"w1\",\"id\":\"nodes\",\"x\":0,\"y\":0,\"w\":4,\"h\":4,\"params\":{\"ns\":\"\u0000\"}}]}")) // NUL in a value
	f.Add([]byte("{\"schemaVersion\":1,\"scope\":\"overview\",\"columns\":12,\"items\":[{\"instanceId\":\"w1\",\"id\":\"nodes\",\"x\":0,\"y\":0,\"w\":4,\"h\":4,\"params\":{\"‮\":\"v\"}}]}"))       // bidi override in a key
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":4,"h":4,"params":{"":"v"}}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":4,"h":4,"params":{"u":"javascript:alert(1)"}}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":4,"h":4,"params":null}]}`))

	// Structural teeth for the nesting itself.
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":null}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":{}}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[null]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[[]]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cluster-health","x":0,"y":0,"w":4,"h":4,"__proto__":{"x":1},"onclick":"alert(1)"}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"OVERVIEW","columns":12,"items":[]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12.0,"items":[]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[],"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":4,"h":4}]}`))

	envelopeKeys := map[string]struct{}{
		"schemaVersion": {}, "scope": {}, "columns": {}, "items": {},
	}
	// params is omitempty, so it is allowed but not required. Everything else
	// must be present and nothing else may appear.
	itemRequired := map[string]struct{}{
		"instanceId": {}, "id": {}, "x": {}, "y": {}, "w": {}, "h": {},
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, normalized, err := ValidateDashboardLayout(data)
		if err != nil {
			// A rejection must not hand back bytes a caller could mistake for
			// something storable.
			if normalized != nil {
				t.Fatalf("rejected layout returned %d bytes to store", len(normalized))
			}
			return
		}

		assertExactKeys(t, "dashboard layout", normalized, envelopeKeys)
		assertLayoutItems(t, normalized, len(cfg.Items), itemRequired)
		assertOnTheGrid(t, cfg)

		// Oracle C. The validator is idempotent or the stored bytes are not
		// trustworthy.
		_, again, err := ValidateDashboardLayout(normalized)
		if err != nil {
			t.Fatalf("normalized layout was rejected on re-validation: %v\nbytes: %s", err, normalized)
		}
		if string(again) != string(normalized) {
			t.Fatalf("normalization is not a fixed point:\n first: %s\nsecond: %s", normalized, again)
		}

		// The dedup key is the scope verbatim, and the scope came from a
		// closed allowlist. A key that no longer round-trips would mean the
		// validated scope and the stored identity had diverged.
		if got := DashboardLayoutDedupKey(cfg.Scope); got != cfg.Scope {
			t.Fatalf("dedup key %q does not match the validated scope %q", got, cfg.Scope)
		}
	})
}

// assertOnTheGrid is oracle D: every accepted placement genuinely fits inside
// the grid the config declares.
//
// The bound is restated as subtraction against an already-bounded operand
// rather than as the addition the validator uses, so the oracle cannot fail
// the same way the code under test does. Each step narrows the range the next
// step relies on: w is pinned to 1..columns first, which is what makes
// `columns - w` safe, and only then is x compared against it.
func assertOnTheGrid(t *testing.T, cfg DashboardLayoutConfig) {
	t.Helper()

	if cfg.Columns != dashboardColumns {
		t.Fatalf("accepted a layout declaring %d columns; the server lays out %d",
			cfg.Columns, dashboardColumns)
	}

	for i, it := range cfg.Items {
		if it.W < 1 || it.W > cfg.Columns {
			t.Fatalf("items[%d] accepted with width %d, outside 1-%d", i, it.W, cfg.Columns)
		}
		if it.H < 1 || it.H > maxDashboardRows {
			t.Fatalf("items[%d] accepted with height %d, outside 1-%d", i, it.H, maxDashboardRows)
		}
		if it.X < 0 || it.X > cfg.Columns-it.W {
			t.Fatalf("items[%d] accepted at x=%d with width %d; the last legal x is %d",
				i, it.X, it.W, cfg.Columns-it.W)
		}
		if it.Y < 0 || it.Y > maxDashboardRows-it.H {
			t.Fatalf("items[%d] accepted at y=%d with height %d; the last legal y is %d",
				i, it.Y, it.H, maxDashboardRows-it.H)
		}
	}
}

// assertLayoutItems is the second level of oracle B: every stored item carries
// the item allowlist and nothing else, and the array the caller gets back has
// the same length as the typed config it was built from.
func assertLayoutItems(t *testing.T, normalized json.RawMessage, wantLen int, required map[string]struct{}) {
	t.Helper()

	var envelope struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(normalized, &envelope); err != nil {
		t.Fatalf("accepted layout items did not re-marshal to JSON objects: %v", err)
	}
	if len(envelope.Items) != wantLen {
		t.Fatalf("stored layout has %d items; the validated config had %d",
			len(envelope.Items), wantLen)
	}

	for i, it := range envelope.Items {
		for k := range it {
			if _, ok := required[k]; ok {
				continue
			}
			if k == "params" {
				continue
			}
			t.Fatalf("items[%d]: unlisted key %q reached the stored config", i, k)
		}
		for k := range required {
			if _, ok := it[k]; !ok {
				t.Fatalf("items[%d]: allowlisted key %q is missing from the stored config", i, k)
			}
		}
	}
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
