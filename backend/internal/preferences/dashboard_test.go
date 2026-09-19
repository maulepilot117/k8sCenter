package preferences

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// layoutJSON builds a dashboard-layout body from a valid base, overriding the
// named top-level fields. A nil value deletes the field, which is how the
// "missing entirely" cases are expressed -- the same shape savedViewJSON uses.
func layoutJSON(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()

	base := map[string]any{
		"schemaVersion": DashboardLayoutSchemaVersion,
		"scope":         "overview",
		"columns":       dashboardColumns,
		"items":         []any{},
	}
	for k, v := range fields {
		if v == nil {
			delete(base, k)
			continue
		}
		base[k] = v
	}

	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal layout fixture: %v", err)
	}
	return raw
}

// item builds one placement from a valid base. Values are `any` so a case can
// supply a wrong-typed or out-of-range value the typed struct could not hold.
func item(over map[string]any) map[string]any {
	base := map[string]any{
		"instanceId": "w1",
		"id":         "cluster-health",
		"x":          0,
		"y":          0,
		"w":          4,
		"h":          4,
	}
	for k, v := range over {
		if v == nil {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	return base
}

func layoutWithItems(t *testing.T, items ...map[string]any) json.RawMessage {
	t.Helper()

	as := make([]any, 0, len(items))
	for _, it := range items {
		as = append(as, it)
	}
	return layoutJSON(t, map[string]any{"items": as})
}

func TestValidateDashboardLayout_Accepts(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"empty layout", layoutJSON(t, nil)},
		{"one widget", layoutWithItems(t, item(nil))},
		{
			"two widgets side by side",
			layoutWithItems(t,
				item(map[string]any{"instanceId": "a", "x": 0, "w": 6}),
				item(map[string]any{"instanceId": "b", "id": "nodes", "x": 6, "w": 6}),
			),
		},
		{
			// Edge-exact placement: the right edge and the bottom row cap are
			// inclusive bounds, so a widget landing exactly on them is legal.
			"widget flush against both bounds",
			layoutWithItems(t, item(map[string]any{
				"x": 8, "w": 4, "y": maxDashboardRows - 2, "h": 2,
			})),
		},
		{
			// Same widget twice is legitimate when the params differ -- prod
			// beside staging. This is the reason instanceId exists.
			"same widget with different params",
			layoutWithItems(t,
				item(map[string]any{
					"instanceId": "prod", "x": 0, "w": 6,
					"params": map[string]string{"namespace": "prod"},
				}),
				item(map[string]any{
					"instanceId": "staging", "x": 6, "w": 6,
					"params": map[string]string{"namespace": "staging"},
				}),
			),
		},
		{
			"full item count",
			func() json.RawMessage {
				items := make([]map[string]any, 0, maxDashboardItems)
				for i := 0; i < maxDashboardItems; i++ {
					items = append(items, item(map[string]any{
						"instanceId": "w" + strconv.Itoa(i),
						"params":     map[string]string{"n": strconv.Itoa(i)},
						"x":          0, "w": 12, "y": i, "h": 1,
					}))
				}
				return layoutWithItems(t, items...)
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ValidateDashboardLayout(tc.raw); err != nil {
				t.Fatalf("ValidateDashboardLayout: %v", err)
			}
		})
	}
}

func TestValidateDashboardLayout_Rejects(t *testing.T) {
	longID := strings.Repeat("a", maxWidgetIDLen+1)
	longInstance := strings.Repeat("a", maxInstanceIDLen+1)

	manyParams := map[string]string{}
	for i := 0; i <= maxDashboardParams; i++ {
		manyParams["k"+strconv.Itoa(i)] = "v"
	}

	cases := []struct {
		name       string
		raw        json.RawMessage
		wantReason string
	}{
		{
			"not an object",
			json.RawMessage(`["not","an","object"]`),
			"invalid_config",
		},
		{
			"truncated JSON",
			json.RawMessage(`{"schemaVersion":`),
			"invalid_config",
		},
		{
			"schema version zero (field absent)",
			layoutJSON(t, map[string]any{"schemaVersion": nil}),
			"unsupported_schema_version",
		},
		{
			"schema version from the future",
			layoutJSON(t, map[string]any{"schemaVersion": DashboardLayoutSchemaVersion + 1}),
			"unsupported_schema_version",
		},
		{
			"unknown scope",
			layoutJSON(t, map[string]any{"scope": "workloads"}),
			"invalid_config",
		},
		{
			"empty scope",
			layoutJSON(t, map[string]any{"scope": ""}),
			"invalid_config",
		},
		{
			"wrong column count",
			layoutJSON(t, map[string]any{"columns": 16}),
			"invalid_config",
		},
		{
			"columns absent",
			layoutJSON(t, map[string]any{"columns": nil}),
			"invalid_config",
		},
		{
			"too many items",
			func() json.RawMessage {
				items := make([]map[string]any, 0, maxDashboardItems+1)
				for i := 0; i <= maxDashboardItems; i++ {
					items = append(items, item(map[string]any{
						"instanceId": "w" + strconv.Itoa(i),
						"params":     map[string]string{"n": strconv.Itoa(i)},
						"x":          0, "w": 12, "y": i, "h": 1,
					}))
				}
				return layoutWithItems(t, items...)
			}(),
			"invalid_config",
		},
		{
			"empty instanceId",
			layoutWithItems(t, item(map[string]any{"instanceId": ""})),
			"invalid_config",
		},
		{
			"over-long instanceId",
			layoutWithItems(t, item(map[string]any{"instanceId": longInstance})),
			"invalid_config",
		},
		{
			"instanceId is not a DNS-1123 subdomain",
			layoutWithItems(t, item(map[string]any{"instanceId": "Not Valid/Id"})),
			"invalid_config",
		},
		{
			"duplicate instanceId",
			layoutWithItems(t,
				item(map[string]any{"instanceId": "same", "x": 0, "w": 6}),
				item(map[string]any{
					"instanceId": "same", "id": "nodes", "x": 6, "w": 6,
				}),
			),
			"invalid_config",
		},
		{
			"unknown widget id",
			layoutWithItems(t, item(map[string]any{"id": "shell-exec"})),
			"unknown_widget_id",
		},
		{
			// Empty is unknown, not a separate class: an id the server does
			// not have is an id the server does not have.
			"empty widget id",
			layoutWithItems(t, item(map[string]any{"id": ""})),
			"unknown_widget_id",
		},
		{
			// Length is checked before the allowlist so the message names the
			// real problem rather than reporting a 300-character id as merely
			// unknown.
			"over-long widget id",
			layoutWithItems(t, item(map[string]any{"id": longID})),
			"invalid_config",
		},
		{
			"zero width",
			layoutWithItems(t, item(map[string]any{"w": 0})),
			"invalid_config",
		},
		{
			"zero height",
			layoutWithItems(t, item(map[string]any{"h": 0})),
			"invalid_config",
		},
		{
			"negative width",
			layoutWithItems(t, item(map[string]any{"w": -4})),
			"invalid_config",
		},
		{
			"negative height",
			layoutWithItems(t, item(map[string]any{"h": -4})),
			"invalid_config",
		},
		{
			"negative x",
			layoutWithItems(t, item(map[string]any{"x": -1})),
			"invalid_config",
		},
		{
			"x plus w past the right edge",
			layoutWithItems(t, item(map[string]any{"x": 10, "w": 4})),
			"invalid_config",
		},
		{
			"negative y",
			layoutWithItems(t, item(map[string]any{"y": -1})),
			"invalid_config",
		},
		{
			"y plus h past the row cap",
			layoutWithItems(t, item(map[string]any{
				"y": maxDashboardRows - 1, "h": 4,
			})),
			"invalid_config",
		},
		{
			// Regression, found by FuzzValidateDashboardLayout's oracle D.
			// The bound used to be written `x+w > columns`, which wraps on an
			// int this large: MaxInt64+1 is a large negative, which is less
			// than 12, so this layout was accepted and stored a widget
			// positioned quintillions of columns off a twelve-column grid.
			// The components are bounded individually now, before any sum.
			"x at the integer ceiling overflows the right-edge sum",
			json.RawMessage(`{"schemaVersion":1,"scope":"overview","columns":12,` +
				`"items":[{"instanceId":"w1","id":"nodes","x":9223372036854775807,"y":0,"w":1,"h":1}]}`),
			"invalid_config",
		},
		{
			"y at the integer ceiling overflows the bottom-edge sum",
			json.RawMessage(`{"schemaVersion":1,"scope":"overview","columns":12,` +
				`"items":[{"instanceId":"w1","id":"nodes","x":0,"y":9223372036854775807,"w":1,"h":1}]}`),
			"invalid_config",
		},
		{
			"width at the integer ceiling",
			json.RawMessage(`{"schemaVersion":1,"scope":"overview","columns":12,` +
				`"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":9223372036854775807,"h":1}]}`),
			"invalid_config",
		},
		{
			"height at the integer ceiling",
			json.RawMessage(`{"schemaVersion":1,"scope":"overview","columns":12,` +
				`"items":[{"instanceId":"w1","id":"nodes","x":0,"y":0,"w":1,"h":9223372036854775807}]}`),
			"invalid_config",
		},
		{
			"too many params",
			layoutWithItems(t, item(map[string]any{"params": manyParams})),
			"invalid_config",
		},
		{
			"empty param name",
			layoutWithItems(t, item(map[string]any{
				"params": map[string]string{"": "v"},
			})),
			"invalid_config",
		},
		{
			"over-long param name",
			layoutWithItems(t, item(map[string]any{
				"params": map[string]string{strings.Repeat("k", maxParamKeyLen+1): "v"},
			})),
			"invalid_config",
		},
		{
			"control character in a param name",
			layoutWithItems(t, item(map[string]any{
				"params": map[string]string{"na\u0000me": "v"},
			})),
			"invalid_config",
		},
		{
			"control character in a param value",
			layoutWithItems(t, item(map[string]any{
				"params": map[string]string{"namespace": "pr\u000bod"},
			})),
			"invalid_config",
		},
		{
			"over-long param value",
			layoutWithItems(t, item(map[string]any{
				"params": map[string]string{"namespace": strings.Repeat("v", maxParamValueLen+1)},
			})),
			"invalid_config",
		},
		{
			// Two placements of the same widget with the same params render
			// identically, so the second is a duplicate no instanceId can
			// justify.
			"identical widget and params twice",
			layoutWithItems(t,
				item(map[string]any{"instanceId": "a", "x": 0, "w": 6}),
				item(map[string]any{"instanceId": "b", "x": 6, "w": 6}),
			),
			"invalid_config",
		},
		{
			"overlapping items",
			layoutWithItems(t,
				item(map[string]any{"instanceId": "a", "x": 0, "y": 0, "w": 6, "h": 4}),
				item(map[string]any{
					"instanceId": "b", "id": "nodes",
					"x": 4, "y": 2, "w": 6, "h": 4,
				}),
			),
			"invalid_config",
		},
		{
			// Containment, not just geometry: an item nested wholly inside
			// another overlaps without crossing either of its edges, which a
			// naive edge-crossing test would miss.
			"one item fully inside another",
			layoutWithItems(t,
				item(map[string]any{"instanceId": "outer", "x": 0, "y": 0, "w": 12, "h": 10}),
				item(map[string]any{
					"instanceId": "inner", "id": "nodes",
					"x": 4, "y": 4, "w": 2, "h": 2,
				}),
			),
			"invalid_config",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, normalized, err := ValidateDashboardLayout(tc.raw)
			if got := reasonOf(err); got != tc.wantReason {
				t.Fatalf("reason = %q (err %v); want %q", got, err, tc.wantReason)
			}
			if normalized != nil {
				t.Fatalf("a rejected layout returned %d bytes to store; want nil", len(normalized))
			}
		})
	}
}

// TestValidateDashboardLayout_TouchingItemsDoNotOverlap pins the boundary the
// overlap test turns on. Adjacency is the normal case in a packed grid -- if
// shared edges counted as overlap, no two widgets could sit next to each other.
func TestValidateDashboardLayout_TouchingItemsDoNotOverlap(t *testing.T) {
	cases := []struct {
		name string
		b    map[string]any
	}{
		{"right edge touches left edge", map[string]any{
			"instanceId": "b", "id": "nodes", "x": 6, "y": 0, "w": 6, "h": 4,
		}},
		{"bottom edge touches top edge", map[string]any{
			"instanceId": "b", "id": "nodes", "x": 0, "y": 4, "w": 6, "h": 4,
		}},
		{"diagonal corners touch", map[string]any{
			"instanceId": "b", "id": "nodes", "x": 6, "y": 4, "w": 6, "h": 4,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := layoutWithItems(t,
				item(map[string]any{"instanceId": "a", "x": 0, "y": 0, "w": 6, "h": 4}),
				tc.b,
			)
			if _, _, err := ValidateDashboardLayout(raw); err != nil {
				t.Fatalf("touching items were rejected: %v", err)
			}
		})
	}
}

// TestValidateDashboardLayout_DropsUnlistedFields is the KTD4 guard, the
// dashboard half of TestValidateSavedView_DropsUnlistedFields. Unlisted keys
// must not survive into the config column -- at the envelope level and, unlike
// the flat saved-view envelope, inside each item too, which is where a
// pass-through would be easiest to miss.
func TestValidateDashboardLayout_DropsUnlistedFields(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaVersion": 1,
		"scope": "overview",
		"columns": 12,
		"ownerId": "someone-else",
		"clusterId": "other-cluster",
		"__proto__": {"polluted": true},
		"items": [{
			"instanceId": "w1",
			"id": "cluster-health",
			"x": 0, "y": 0, "w": 4, "h": 4,
			"onclick": "alert(1)",
			"href": "https://evil.example/redirect",
			"__proto__": {"polluted": true},
			"nested": {"deep": [1, 2, 3]}
		}]
	}`)

	_, normalized, err := ValidateDashboardLayout(raw)
	if err != nil {
		t.Fatalf("ValidateDashboardLayout: %v", err)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &envelope); err != nil {
		t.Fatalf("normalized config is not valid JSON: %v", err)
	}

	wantEnvelope := map[string]struct{}{
		"schemaVersion": {}, "scope": {}, "columns": {}, "items": {},
	}
	for k := range envelope {
		if _, ok := wantEnvelope[k]; !ok {
			t.Errorf("normalized config carries unexpected envelope key %q", k)
		}
	}
	for k := range wantEnvelope {
		if _, ok := envelope[k]; !ok {
			t.Errorf("normalized config is missing envelope key %q", k)
		}
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["items"], &items); err != nil {
		t.Fatalf("normalized items are not valid JSON: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items; want 1", len(items))
	}

	wantItem := map[string]struct{}{
		"instanceId": {}, "id": {}, "x": {}, "y": {}, "w": {}, "h": {},
	}
	for k := range items[0] {
		if _, ok := wantItem[k]; !ok {
			t.Errorf("normalized item carries unexpected key %q", k)
		}
	}
	for k := range wantItem {
		if _, ok := items[0][k]; !ok {
			t.Errorf("normalized item is missing key %q", k)
		}
	}
}

// TestValidateDashboardLayout_NormalizationIsAFixedPoint guards the property
// the fuzz target's oracle B checks continuously: re-validating what the
// validator produced must accept and return byte-identical output. If it did
// not, a row would decode differently than it was written -- the stored bytes
// would mean one thing on save and another on the next read.
func TestValidateDashboardLayout_NormalizationIsAFixedPoint(t *testing.T) {
	raw := layoutWithItems(t,
		item(map[string]any{
			"instanceId": "a", "x": 0, "w": 6,
			"params": map[string]string{"z": "1", "a": "2", "m": "3"},
		}),
		item(map[string]any{
			"instanceId": "b", "id": "nodes", "x": 6, "w": 6,
			// An empty param map is dropped by omitempty, so the second pass
			// sees no params at all -- exactly the asymmetry that would break
			// the fixed point if the validator treated nil and empty
			// differently.
			"params": map[string]string{},
		}),
	)

	_, first, err := ValidateDashboardLayout(raw)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	_, second, err := ValidateDashboardLayout(first)
	if err != nil {
		t.Fatalf("second pass rejected the first pass's own output: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("normalization is not a fixed point:\n first: %s\nsecond: %s", first, second)
	}
}

func TestDashboardLayoutDedupKey(t *testing.T) {
	// The scope IS the identity. Migration 000018's unique index on
	// (owner_id, kind, cluster_id, dedup_key) then enforces one layout per
	// scope without a new index, so a derivation that folded in anything else
	// would silently allow a second layout for the same dashboard.
	if got := DashboardLayoutDedupKey("overview"); got != "overview" {
		t.Fatalf("DashboardLayoutDedupKey = %q; want %q", got, "overview")
	}
	if err := ValidateDedupKey(DashboardLayoutDedupKey("overview")); err != nil {
		t.Fatalf("a scope key must pass ValidateDedupKey: %v", err)
	}
}

// TestMaxDashboardLayoutsPerUser pins the quota ceiling to the scope set it is
// derived from. The handler passes this into Create, so a ceiling below the
// number of scopes would refuse a layout for the last dashboard with
// limit_reached -- a failure indistinguishable, from the client, from the user
// actually hitting a quota.
func TestMaxDashboardLayoutsPerUser(t *testing.T) {
	if MaxDashboardLayoutsPerUser != len(allowedDashboardScopes) {
		t.Fatalf("MaxDashboardLayoutsPerUser = %d; want %d (one per allowed scope)",
			MaxDashboardLayoutsPerUser, len(allowedDashboardScopes))
	}
}

func TestCanonicalParams(t *testing.T) {
	// Map iteration order is randomized in Go, so a derivation that did not
	// sort would make duplicate detection nondeterministic: the same layout
	// would sometimes save and sometimes be refused.
	a := canonicalParams(map[string]string{"z": "1", "a": "2", "m": "3"})
	b := canonicalParams(map[string]string{"m": "3", "z": "1", "a": "2"})
	if a != b {
		t.Fatalf("canonicalParams is order-dependent: %q != %q", a, b)
	}
	if got := canonicalParams(nil); got != "" {
		t.Fatalf("canonicalParams(nil) = %q; want empty", got)
	}
	if got := canonicalParams(map[string]string{}); got != "" {
		t.Fatalf("canonicalParams(empty) = %q; want empty", got)
	}

	// Values are separated from keys and from each other, so {"ab":"c"} and
	// {"a":"bc"} cannot fold to the same string and collide as duplicates.
	if canonicalParams(map[string]string{"ab": "c"}) == canonicalParams(map[string]string{"a": "bc"}) {
		t.Fatal("canonicalParams collides on a shifted key/value boundary")
	}
}
