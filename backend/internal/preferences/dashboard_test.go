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

// testParamWidgetID / testParamWidgetSpec stand in for the parameterized
// widgets the spec describes (a Grafana dashboard uid, a PromQL preset) and
// that a later phase will add. Every widget shipped today declares no
// parameters, so without a stand-in the entire param half of the validator --
// membership, value enums, and the identity derivation that exists only to
// tell two parameterizations of one widget apart -- would be untestable.
//
// Its minimums are 1x1 so size is never the reason a param case fails.
const testParamWidgetID = "test-param-widget"

var testParamWidgetSpec = widgetSpec{
	MinW: 1, MinH: 1,
	Params: map[string][]string{
		"namespace": {},                  // open set: any value inside the generic bounds
		"mode":      {"compact", "full"}, // closed set
		"n":         {},
	},
}

func withParamWidget(t *testing.T) func() {
	t.Helper()
	return withTestWidget(t, testParamWidgetID, testParamWidgetSpec)
}

func TestValidateDashboardLayout_Accepts(t *testing.T) {
	cases := []struct {
		name string
		// setup installs any catalog entry the case needs and returns its
		// restore func. Nil means the case runs against the real catalog.
		setup func(t *testing.T) func()
		raw   json.RawMessage
	}{
		{name: "empty layout", raw: layoutJSON(t, nil)},
		{name: "one widget", raw: layoutWithItems(t, item(nil))},
		{
			name: "items key absent normalizes rather than failing",
			raw:  layoutJSON(t, map[string]any{"items": nil}),
		},
		{
			name: "two widgets side by side",
			raw: layoutWithItems(t,
				item(map[string]any{"instanceId": "a", "x": 0, "w": 6}),
				item(map[string]any{"instanceId": "b", "id": "nodes", "x": 6, "w": 6}),
			),
		},
		{
			// Edge-exact placement: the right edge and the bottom row cap are
			// inclusive bounds, so a widget landing exactly on them is legal.
			// cpu-tile is used because its 2x2 minimum is the smallest any
			// shipped widget declares, which is what lets h=2 sit flush
			// against the row cap.
			name: "widget flush against both bounds",
			raw: layoutWithItems(t, item(map[string]any{
				"id": "cpu-tile", "x": 8, "w": 4, "y": maxDashboardRows - 2, "h": 2,
			})),
		},
		{
			name:  "widget exactly at its declared minimum size",
			setup: nil,
			raw: layoutWithItems(t, item(map[string]any{
				"id": "nodes", "x": 0, "y": 0, "w": 3, "h": 4,
			})),
		},
		{
			// Same widget twice is legitimate when the params differ -- prod
			// beside staging. This is the reason instanceId exists.
			name:  "same widget with different params",
			setup: withParamWidget,
			raw: layoutWithItems(t,
				item(map[string]any{
					"instanceId": "prod", "id": testParamWidgetID, "x": 0, "w": 6,
					"params": map[string]string{"namespace": "prod"},
				}),
				item(map[string]any{
					"instanceId": "staging", "id": testParamWidgetID, "x": 6, "w": 6,
					"params": map[string]string{"namespace": "staging"},
				}),
			),
		},
		{
			name:  "value drawn from a closed enum",
			setup: withParamWidget,
			raw: layoutWithItems(t, item(map[string]any{
				"id": testParamWidgetID, "params": map[string]string{"mode": "compact"},
			})),
		},
		{
			name:  "full item count",
			setup: withParamWidget,
			raw: func() json.RawMessage {
				items := make([]map[string]any, 0, maxDashboardItems)
				for i := 0; i < maxDashboardItems; i++ {
					items = append(items, item(map[string]any{
						"instanceId": "w" + strconv.Itoa(i),
						"id":         testParamWidgetID,
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
			if tc.setup != nil {
				defer tc.setup(t)()
			}
			if _, _, err := ValidateDashboardLayout(tc.raw); err != nil {
				t.Fatalf("ValidateDashboardLayout: %v", err)
			}
		})
	}
}

func TestValidateDashboardLayout_Rejects(t *testing.T) {
	longID := strings.Repeat("a", maxWidgetIDLen+1)
	longInstance := strings.Repeat("a", maxInstanceIDLen+1)

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
			// Every shipped widget declares no parameters, so any param on one
			// is a client bug. This is the only param rule that belongs in
			// this table; the rest need a widget that actually takes params
			// and live in TestValidateDashboardLayout_RejectsParams.
			"params on a widget that declares none",
			layoutWithItems(t, item(map[string]any{
				"params": map[string]string{"namespace": "prod"},
			})),
			"invalid_config",
		},
		{
			// Below the widget's own declared minimum. nodes registers
			// minW: 3, minH: 4, so 2x4 and 3x3 each violate exactly one bound
			// while staying comfortably inside the grid -- which is what the
			// generic 1..columns / 1..rows checks would have let through.
			"width below the widget's declared minimum",
			layoutWithItems(t, item(map[string]any{
				"id": "nodes", "x": 0, "y": 0, "w": 2, "h": 4,
			})),
			"invalid_config",
		},
		{
			"height below the widget's declared minimum",
			layoutWithItems(t, item(map[string]any{
				"id": "nodes", "x": 0, "y": 0, "w": 3, "h": 3,
			})),
			"invalid_config",
		},
		{
			// The case the spec names directly: a nodes widget at 1x1 is well
			// inside the grid and was accepted before minimums were enforced.
			"widget at 1x1 when it declares 3x4",
			layoutWithItems(t, item(map[string]any{
				"id": "nodes", "x": 0, "y": 0, "w": 1, "h": 1,
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

// TestValidateDashboardLayout_RejectsParams covers the param rules that need a
// widget which actually declares parameters. No shipped widget does, so these
// cases run against the stand-in; without it every one of them would trip the
// "takes no parameters" branch instead of the rule it names, and would pass
// while testing nothing.
func TestValidateDashboardLayout_RejectsParams(t *testing.T) {
	manyParams := map[string]string{}
	for i := 0; i <= maxDashboardParams; i++ {
		manyParams["n"+strconv.Itoa(i)] = "v"
	}

	cases := []struct {
		name       string
		params     map[string]string
		wantReason string
	}{
		{"too many params", manyParams, "invalid_config"},
		{"empty param name", map[string]string{"": "v"}, "invalid_config"},
		{
			"over-long param name",
			map[string]string{strings.Repeat("k", maxParamKeyLen+1): "v"},
			"invalid_config",
		},
		{"control character in a param name", map[string]string{"na\u0000me": "v"}, "invalid_config"},
		{"control character in a param value", map[string]string{"namespace": "pr\u000bod"}, "invalid_config"},
		{
			"over-long param value",
			map[string]string{"namespace": strings.Repeat("v", maxParamValueLen+1)},
			"invalid_config",
		},
		{
			// Declared-surface rules: a key the widget never named, and a
			// value outside the closed set it did name.
			"param the widget does not declare",
			map[string]string{"cluster": "prod"},
			"invalid_config",
		},
		{
			"value outside the declared enum",
			map[string]string{"mode": "enormous"},
			"invalid_config",
		},
		{
			// KTD4 in its concrete form: a URL and a query are refused because
			// the value is not in the widget's closed set, not because anyone
			// pattern-matched what a dangerous value looks like.
			"url in a closed-enum param",
			map[string]string{"mode": "https://evil.example/x"},
			"invalid_config",
		},
		{
			"promql fragment in a closed-enum param",
			map[string]string{"mode": `sum(rate(x[5m])) by (y)`},
			"invalid_config",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer withParamWidget(t)()

			raw := layoutWithItems(t, item(map[string]any{
				"id": testParamWidgetID, "params": tc.params,
			}))
			_, normalized, err := ValidateDashboardLayout(raw)
			if got := reasonOf(err); got != tc.wantReason {
				t.Fatalf("reason = %q (err %v); want %q", got, err, tc.wantReason)
			}
			if normalized != nil {
				t.Fatalf("a rejected layout returned %d bytes to store; want nil", len(normalized))
			}
		})
	}
}

// TestValidateDashboardLayout_ItemsIsAlwaysAnArray guards the one value the
// TypeScript mirror declares cannot occur.
//
// Items carries no omitempty, so a nil slice marshals to `"items":null` while
// an empty slice marshals to `"items":[]`. The client type is
// `items: LayoutItem[]` -- required and non-nullable -- so a stored null is a
// value every consumer is entitled to assume impossible, and iterating it
// throws on the user's own saved layout at every load.
//
// Length checks cannot see the difference (len(nil) == len([]) == 0), which is
// why this asserts on the bytes.
func TestValidateDashboardLayout_ItemsIsAlwaysAnArray(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{
			"items key absent",
			json.RawMessage(`{"schemaVersion":1,"scope":"overview","columns":12}`),
		},
		{
			"items explicitly null",
			json.RawMessage(`{"schemaVersion":1,"scope":"overview","columns":12,"items":null}`),
		},
		{
			"items an empty array",
			json.RawMessage(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[]}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, normalized, err := ValidateDashboardLayout(tc.raw)
			if err != nil {
				t.Fatalf("ValidateDashboardLayout: %v", err)
			}
			if cfg.Items == nil {
				t.Error("typed config still carries a nil Items slice")
			}

			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(normalized, &envelope); err != nil {
				t.Fatalf("normalized config is not valid JSON: %v", err)
			}
			if got := string(envelope["items"]); got != "[]" {
				t.Fatalf("stored items = %s; want [] (a JSON null breaks every client consumer)", got)
			}
		})
	}
}

// TestValidateDashboardLayout_AcceptsShippedDefaultLayout feeds the client's
// own default through the server's validator.
//
// That layout is the arrangement every user starts from and the one "Reset"
// restores, and it is authored in TypeScript on the other side of the
// contract this package pins. Nothing else checks it against these rules, so a
// tightening here -- per-widget minimums being the immediate example -- could
// make the default itself unsavable, and the first symptom would be a user
// unable to save a dashboard they never edited.
//
// The fixture is a transcription of DEFAULT_OVERVIEW_LAYOUT in
// frontend/lib/dashboard/default-layout.ts. Keep the two in step.
func TestValidateDashboardLayout_AcceptsShippedDefaultLayout(t *testing.T) {
	const defaultLayout = `{
		"schemaVersion": 1,
		"scope": "overview",
		"columns": 12,
		"items": [
			{"instanceId":"d-cluster-health","id":"cluster-health","x":0,"y":0,"w":6,"h":6},
			{"instanceId":"d-cpu-tile","id":"cpu-tile","x":6,"y":0,"w":3,"h":3},
			{"instanceId":"d-memory-tile","id":"memory-tile","x":9,"y":0,"w":3,"h":3},
			{"instanceId":"d-pods-tile","id":"pods-tile","x":6,"y":3,"w":3,"h":3},
			{"instanceId":"d-network-tile","id":"network-tile","x":9,"y":3,"w":3,"h":3},
			{"instanceId":"d-resource-utilization","id":"resource-utilization","x":0,"y":6,"w":7,"h":4},
			{"instanceId":"d-pod-status","id":"pod-status","x":7,"y":6,"w":5,"h":4},
			{"instanceId":"d-nodes","id":"nodes","x":0,"y":10,"w":4,"h":5},
			{"instanceId":"d-recent-events","id":"recent-events","x":4,"y":10,"w":5,"h":10},
			{"instanceId":"d-active-alerts","id":"active-alerts","x":9,"y":10,"w":3,"h":5}
		]
	}`

	cfg, _, err := ValidateDashboardLayout(json.RawMessage(defaultLayout))
	if err != nil {
		t.Fatalf("the shipped default layout does not pass this server's own validator: %v", err)
	}
	if len(cfg.Items) != 10 {
		t.Fatalf("default layout has %d items; the fixture is out of step with "+
			"frontend/lib/dashboard/default-layout.ts", len(cfg.Items))
	}
}

// TestValidateDashboardLayout_GeometryMessagesAreInclusive pins the numbers the
// rejection messages actually print.
//
// These messages are the only explanation a client author or a user debugging
// a refused save ever sees, and they were naming cells that are not legal: the
// start message printed the column count (so "0-12" on a grid whose last legal
// start is 11) and the span message printed the exclusive end (so a widget at
// x=10 w=3 was reported as spanning 10-13 when it occupies 10, 11 and 12).
//
// The accept/reject decisions were correct throughout, which is exactly why
// nothing caught this: every reason-code assertion passed. Only the message
// text distinguishes the fixed version from the broken one.
func TestValidateDashboardLayout_GeometryMessagesAreInclusive(t *testing.T) {
	cases := []struct {
		name    string
		over    map[string]any
		wantMsg string
	}{
		{
			name:    "start past the last legal column",
			over:    map[string]any{"id": "cpu-tile", "x": 12, "y": 0, "w": 2, "h": 2},
			wantMsg: "items[0] starts at column 12, outside 0-11",
		},
		{
			name:    "span past the right edge",
			over:    map[string]any{"id": "cpu-tile", "x": 10, "y": 0, "w": 3, "h": 2},
			wantMsg: "items[0] spans columns 10-12, outside 0-11",
		},
		{
			name:    "start past the last legal row",
			over:    map[string]any{"id": "cpu-tile", "x": 0, "y": maxDashboardRows, "w": 2, "h": 2},
			wantMsg: "items[0] starts at row 200, outside 0-199",
		},
		{
			name:    "span past the bottom",
			over:    map[string]any{"id": "cpu-tile", "x": 0, "y": 199, "w": 2, "h": 2},
			wantMsg: "items[0] spans rows 199-200, outside 0-199",
		},
		{
			// The size messages name the widget, because the minimum is the
			// widget's rather than the grid's.
			name:    "below the widget's minimum width",
			over:    map[string]any{"id": "nodes", "x": 0, "y": 0, "w": 2, "h": 4},
			wantMsg: "items[0] is 2 columns wide; nodes accepts 3-12",
		},
		{
			name:    "below the widget's minimum height",
			over:    map[string]any{"id": "nodes", "x": 0, "y": 0, "w": 3, "h": 2},
			wantMsg: "items[0] is 2 rows tall; nodes accepts 4-200",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidateDashboardLayout(layoutWithItems(t, item(tc.over)))
			if err == nil {
				t.Fatal("layout was accepted; want a rejection")
			}
			if got := err.Error(); got != tc.wantMsg {
				t.Fatalf("message =\n  %q\nwant\n  %q", got, tc.wantMsg)
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
	defer withParamWidget(t)()

	raw := layoutWithItems(t,
		item(map[string]any{
			"instanceId": "a", "id": testParamWidgetID, "x": 0, "w": 6,
			"params": map[string]string{"namespace": "z", "n": "2", "mode": "full"},
		}),
		item(map[string]any{
			"instanceId": "b", "id": "nodes", "x": 6, "w": 6,
			// An empty param map is dropped by omitempty, so the second pass
			// sees no params at all -- exactly the asymmetry that would break
			// the fixed point if the validator treated nil and empty
			// differently. nodes declares no params, and an empty map is not
			// a param, so this must still be accepted.
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

	// Injectivity: two different param maps must never fold to one string,
	// because that string IS the duplicate-detection identity and a collision
	// refuses a legitimate second placement.
	//
	// These pairs are chosen to attack the separator itself, which is the only
	// way this derivation can collide. An earlier version of this test used
	// {"ab":"c"} vs {"a":"bc"} and passed against a '=' separator that did in
	// fact collide -- the shift moved the '=' too, so the one pair it checked
	// was the one pair that survived. Every pair below puts the candidate
	// separator INSIDE a key or value, which is what a shift alone cannot do.
	collisionPairs := []struct {
		name string
		a, b map[string]string
	}{
		{"shifted key/value boundary",
			map[string]string{"ab": "c"}, map[string]string{"a": "bc"}},
		{"equals sign inside a key vs inside a value",
			map[string]string{"a=b": "c"}, map[string]string{"a": "b=c"}},
		{"equals sign at the boundary",
			map[string]string{"a": "=b"}, map[string]string{"a=": "b"}},
		{"two pairs vs one pair spelling the same text",
			map[string]string{"a": "b", "c": "d"}, map[string]string{"a": "b\x00c\x00d"}},
		{"empty value vs absent second key",
			map[string]string{"a": "", "b": ""}, map[string]string{"a": "", "b\x00": ""}},
	}
	for _, p := range collisionPairs {
		t.Run(p.name, func(t *testing.T) {
			if canonicalParams(p.a) == canonicalParams(p.b) {
				t.Fatalf("canonicalParams collides: %v and %v both fold to %q",
					p.a, p.b, canonicalParams(p.a))
			}
		})
	}
}

// TestValidateDashboardLayout_EqualsInParamKeyIsNotADuplicate is the
// end-to-end form of the collision above: before the separator was fixed, two
// placements of one widget carrying genuinely different params were refused as
// duplicates of each other.
//
// It needs a widget that accepts parameters, and no shipped widget does, so it
// installs one for the duration of the test. That is also the only way to
// exercise the param-membership path at all today.
func TestValidateDashboardLayout_EqualsInParamKeyIsNotADuplicate(t *testing.T) {
	restore := withTestWidget(t, "nodes", widgetSpec{
		MinW: 3, MinH: 4,
		Params: map[string][]string{"a=b": {}, "a": {}},
	})
	defer restore()

	raw := layoutWithItems(t,
		item(map[string]any{
			"instanceId": "one", "id": "nodes", "x": 0, "w": 6, "h": 4,
			"params": map[string]string{"a=b": "c"},
		}),
		item(map[string]any{
			"instanceId": "two", "id": "nodes", "x": 6, "w": 6, "h": 4,
			"params": map[string]string{"a": "b=c"},
		}),
	)

	if _, _, err := ValidateDashboardLayout(raw); err != nil {
		t.Fatalf("two placements with different params were refused: %v", err)
	}
}

// withTestWidget temporarily replaces one catalog entry so the parameterized
// paths can be exercised while every shipped widget is parameterless. It
// restores the original entry, so the parity pins still see the real catalog.
func withTestWidget(t *testing.T, id string, spec widgetSpec) func() {
	t.Helper()

	original, existed := allowedWidgets[id]
	allowedWidgets[id] = spec
	return func() {
		if existed {
			allowedWidgets[id] = original
			return
		}
		delete(allowedWidgets, id)
	}
}
