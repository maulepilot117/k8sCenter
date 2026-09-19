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
// Six oracles:
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
//	E. The relationships between items hold: no repeated instanceId, no
//	   repeated (id, params) identity, and no two items sharing a cell. B, C
//	   and D all inspect one item at a time, so deleting any of the three
//	   relationship checks left every oracle green — the rules were covered by
//	   the table tests but not by this gate.
//	F. `items` is a JSON array, never null. The client type declares it
//	   non-nullable, and a length comparison cannot tell the two apart:
//	   len(nil) == len([]) == 0 is exactly why oracle B passed on a stored
//	   null.
//
// A note on what D, E and F have in common, because it is the lesson this
// target keeps re-learning: each restates a property in terms the validator
// does not itself use. An oracle written in the implementation's own idiom —
// its arithmetic, its length checks, its constants — cannot fail when that
// idiom is wrong.
func FuzzValidateDashboardLayout(f *testing.F) {
	// The param rules are unreachable while every shipped widget is
	// parameterless -- a param on one of those is refused before any key or
	// value is examined -- so the stand-in from dashboard_test.go is installed
	// for the life of this target. Without it the param seeds below would all
	// stop at the same branch and the bounds they were written for would never
	// run. Installed here rather than inside the callback because test
	// functions run sequentially, so the catalog is never mutated under
	// another test.
	defer withTestWidget(&testing.T{}, testParamWidgetID, testParamWidgetSpec)()

	// Well-formed envelopes: the mutator explores around these.
	//
	// Every geometry seed below respects the minimum size of the widget it
	// names, because a seed that trips the size check never reaches the rule
	// it was written for. cpu-tile (2x2) is used wherever a seed needs to be
	// small; nodes and pod-status are 3x4 and cluster-health 3x4.
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cluster-health","x":0,"y":0,"w":4,"h":4}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"b","id":"pod-status","x":6,"y":0,"w":6,"h":4}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"p","id":"test-param-widget","x":0,"y":0,"w":6,"h":4,"params":{"namespace":"prod"}},{"instanceId":"s","id":"test-param-widget","x":6,"y":0,"w":6,"h":4,"params":{"namespace":"staging"}}]}`))

	// Geometry teeth — mutated from the table cases in dashboard_test.go. Each
	// is one integer away from an accepted layout, which is exactly the
	// neighbourhood a bounds bug lives in.
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":8,"y":198,"w":4,"h":2}]}`))                  // flush against both bounds
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":9,"y":0,"w":4,"h":2}]}`))                    // one column past the edge
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":0,"y":199,"w":4,"h":2}]}`))                  // one row past the cap
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":0,"y":0,"w":0,"h":0}]}`))                    // zero-area
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":-1,"y":-1,"w":4,"h":4}]}`))                  // negative origin
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":2,"h":4}]}`))                       // one column under the widget's minimum
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":3,"h":3}]}`))                       // one row under the widget's minimum
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":3,"h":4}]}`))                       // exactly at the minimum, must be accepted
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":0,"y":0,"w":-9223372036854775808,"h":4}]}`)) // int64 min, to probe x+w overflow
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":9223372036854775807,"y":0,"w":2,"h":2}]}`))  // int64 max, same
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":0,"y":9223372036854775807,"w":2,"h":2}]}`))  // int64 max on the other axis
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":0,"y":0,"w":9223372036854775807,"h":2}]}`))  // int64 max size
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"cpu-tile","x":0,"y":0,"w":2,"h":9223372036854775807}]}`))

	// Relationship teeth: the checks that need two items to trip. Sizes here
	// are all at or above each widget's minimum, so the seed reaches the
	// relationship check rather than stopping at a size bound.
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"a","id":"pod-status","x":6,"y":0,"w":6,"h":4}]}`))                                                                                          // repeated instanceId
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"b","id":"nodes","x":6,"y":0,"w":6,"h":4}]}`))                                                                                               // identical identity
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":12,"h":10},{"instanceId":"b","id":"cpu-tile","x":4,"y":4,"w":2,"h":2}]}`))                                                                                          // containment, not edge crossing
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"b","id":"cpu-tile","x":4,"y":2,"w":4,"h":4}]}`))                                                                                            // partial overlap
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"nodes","x":0,"y":0,"w":6,"h":4},{"instanceId":"b","id":"pod-status","x":6,"y":4,"w":6,"h":4}]}`))                                                                                          // corners touch, must be accepted
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"a","id":"test-param-widget","x":0,"y":0,"w":6,"h":4,"params":{"namespace":"a=b"}},{"instanceId":"b","id":"test-param-widget","x":6,"y":0,"w":6,"h":4,"params":{"namespace":"a","mode":"full"}}]}`)) // separator-collision shapes

	// Param teeth: the only free-text a layout carries. These name the
	// parameterized stand-in so they reach the key and value bounds; the
	// last one checks that a shipped, parameterless widget refuses params.
	f.Add([]byte("{\"schemaVersion\":1,\"scope\":\"overview\",\"columns\":12,\"items\":[{\"instanceId\":\"w1\",\"id\":\"test-param-widget\",\"x\":0,\"y\":0,\"w\":4,\"h\":4,\"params\":{\"namespace\":\"\u0000\"}}]}")) // NUL in a value
	f.Add([]byte("{\"schemaVersion\":1,\"scope\":\"overview\",\"columns\":12,\"items\":[{\"instanceId\":\"w1\",\"id\":\"test-param-widget\",\"x\":0,\"y\":0,\"w\":4,\"h\":4,\"params\":{\"‮\":\"v\"}}]}"))              // bidi override in a key
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"test-param-widget","x":0,"y":0,"w":4,"h":4,"params":{"":"v"}}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"test-param-widget","x":0,"y":0,"w":4,"h":4,"params":{"mode":"javascript:alert(1)"}}]}`)) // outside a closed enum
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"test-param-widget","x":0,"y":0,"w":4,"h":4,"params":{"unknown":"v"}}]}`))                // key the widget never declared
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"test-param-widget","x":0,"y":0,"w":4,"h":4,"params":null}]}`))
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":4,"h":4,"params":{"namespace":"prod"}}]}`)) // params on a parameterless widget

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
		assertItemsAreDistinct(t, cfg)
		assertItemsIsAnArray(t, normalized)

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

// assertItemsAreDistinct is oracle E: the relationships the validator promises
// between items actually hold in what it accepted.
//
// Each of the three is restated in terms the validator does not use. Identity
// is compared by JSON-encoding the (id, params) pair rather than by calling
// canonicalParams, so a bug in that derivation cannot hide behind itself --
// which is precisely what happened when its '=' separator made two different
// param maps compare equal. Overlap is recomputed cell by cell instead of
// through itemsOverlap's four-comparison form.
//
// The cell walk is bounded: the grid is 12 columns and 200 rows and a layout
// holds at most 40 items, so the worst case is well under 100k steps.
func assertItemsAreDistinct(t *testing.T, cfg DashboardLayoutConfig) {
	t.Helper()

	seenInstance := make(map[string]int, len(cfg.Items))
	seenIdentity := make(map[string]int, len(cfg.Items))
	occupied := make(map[[2]int]int)

	for i, it := range cfg.Items {
		if prev, dup := seenInstance[it.InstanceID]; dup {
			t.Fatalf("items[%d] and items[%d] share instanceId %q", prev, i, it.InstanceID)
		}
		seenInstance[it.InstanceID] = i

		identity, err := json.Marshal([]any{it.ID, it.Params})
		if err != nil {
			t.Fatalf("items[%d]: could not encode identity: %v", i, err)
		}
		if prev, dup := seenIdentity[string(identity)]; dup {
			t.Fatalf("items[%d] and items[%d] are the same widget with the same params: %s",
				prev, i, identity)
		}
		seenIdentity[string(identity)] = i

		for x := it.X; x < it.X+it.W; x++ {
			for y := it.Y; y < it.Y+it.H; y++ {
				cell := [2]int{x, y}
				if prev, taken := occupied[cell]; taken {
					t.Fatalf("items[%d] and items[%d] both occupy cell (%d,%d)", prev, i, x, y)
				}
				occupied[cell] = i
			}
		}
	}
}

// assertItemsIsAnArray is oracle F: the stored `items` is a JSON array.
//
// The client type declares it non-nullable, so a stored null is a value every
// consumer is entitled to treat as impossible. This has to inspect the bytes:
// a nil slice and an empty slice are indistinguishable by length, which is why
// the containment oracle passed on a layout that stored `"items":null`.
func assertItemsIsAnArray(t *testing.T, normalized json.RawMessage) {
	t.Helper()

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &envelope); err != nil {
		t.Fatalf("accepted layout did not re-marshal to a JSON object: %v", err)
	}
	items, ok := envelope["items"]
	if !ok {
		t.Fatal("accepted layout stored no items key")
	}
	if len(items) == 0 || items[0] != '[' {
		t.Fatalf("stored items is %s; want a JSON array (null breaks every client consumer)", items)
	}
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
