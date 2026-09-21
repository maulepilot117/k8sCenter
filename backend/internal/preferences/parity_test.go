package preferences

import (
	"reflect"
	"sort"
	"testing"
)

// TestContractParity pins every Go-side value that the browser re-declares to
// an expected literal. A change on either side without updating the other
// fails here (Go side) or in the TS test named in the failure message, and
// each failure names the other file so the next reader knows where the second
// edit belongs.
//
// Two contracts live here. The saved-view and pin values are mirrored in
// frontend/lib/preference-types.ts, checked by
// frontend/lib/preference-types_test.ts. The dashboard-layout values are
// mirrored in frontend/lib/dashboard/types.ts and, for the widget catalog, in
// the registration table frontend/lib/dashboard/registry.ts, checked by
// "registry ids are pinned to the server-side allowlist" in
// frontend/lib/dashboard/registry_test.ts.
//
// This is the mechanism, not the comment: the TypeScript copies exist so the
// UI can refuse an impossible save before a round trip, and a silent drift
// turns that help into a lie — the client green-lights a value the server
// rejects, or refuses one the server would have accepted. The same pairing
// guards the wizard regexes (regex_parity_test.go).
func TestContractParity(t *testing.T) {
	t.Run("allowlists", func(t *testing.T) {
		cases := []struct {
			name        string
			got         map[string]struct{}
			want        []string
			tsConstName string
			// tsFile names the file holding the other half of the pin. Most
			// live in preference-types.ts; the dashboard allowlists do not,
			// and a failure message pointing at the wrong file sends the next
			// reader to a constant that is already correct.
			tsFile string
		}{
			{
				name:        "allowedStatusFilters",
				got:         allowedStatusFilters,
				want:        []string{"all", "failed", "pending", "progressing", "running"},
				tsConstName: "SAVED_VIEW_STATUS_FILTERS",
				tsFile:      "frontend/lib/preference-types.ts",
			},
			{
				name:        "allowedSortKeys",
				got:         allowedSortKeys,
				want:        []string{"age", "name", "namespace"},
				tsConstName: "SAVED_VIEW_SORT_KEYS",
				tsFile:      "frontend/lib/preference-types.ts",
			},
			{
				name:        "allowedSortDirs",
				got:         allowedSortDirs,
				want:        []string{"asc", "desc"},
				tsConstName: "SAVED_VIEW_SORT_DIRS",
				tsFile:      "frontend/lib/preference-types.ts",
			},
			{
				name:        "allowedDashboardScopes",
				got:         allowedDashboardScopes,
				want:        []string{"overview"},
				tsConstName: "DASHBOARD_SCOPES",
				tsFile:      "frontend/lib/dashboard/types.ts",
			},
			{
				// The widget catalog is a TypeScript registration table this
				// package cannot read, so this literal is the only thing
				// standing between a widget added on one side and a layout the
				// editor lets the user build but the server refuses to store.
				// The mirror assertion is "registry ids are pinned to the
				// server-side allowlist" in the TS file named below.
				name: "allowedWidgets (ids)",
				got:  widgetIDSet(),
				want: []string{
					"active-alerts", "cluster-health", "cpu-tile",
					"diagnostics-summary", "memory-tile", "network-tile",
					"nodes", "pod-status", "pods-tile", "recent-events",
					"resource-utilization",
				},
				tsConstName: "the widget registry",
				tsFile:      "frontend/lib/dashboard/registry.ts",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := make([]string, 0, len(tc.got))
				for k := range tc.got {
					got = append(got, k)
				}
				sort.Strings(got)

				if len(got) != len(tc.want) {
					t.Fatalf("%s drift: got %v, want %v (update %s %s to match, or update this test)",
						tc.name, got, tc.want, tc.tsFile, tc.tsConstName)
				}
				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("%s drift: got %v, want %v (update %s %s to match, or update this test)",
							tc.name, got, tc.want, tc.tsFile, tc.tsConstName)
					}
				}
			})
		}
	})

	// Each widget's minimum size is a second cross-language contract, and a
	// stricter one than the id list: an id present on both sides but sized
	// differently produces an editor that lets a user drag to a size the
	// server then refuses, with a message naming a minimum the client never
	// showed. The mirror assertion is "registry minimums are pinned to the
	// server-side catalog" in frontend/lib/dashboard/registry_test.ts.
	//
	// Parameters are pinned here too, and by their VALUE rather than by their
	// absence. This subtest used to assert that no widget declared any -- a
	// tripwire for the first parameterized widget, which diagnostics-summary
	// then tripped. The pin that replaced it is stricter than the one it
	// replaced: a key declared on one side only makes every layout carrying it
	// unsaveable, and a value set narrowed on one side only makes the two
	// catalogs disagree about which values are legal, neither of which the id
	// list above would catch.
	//
	// A nil map means the widget takes no parameters at all, and the validator
	// refuses params outright for such a widget. A declared key whose value
	// slice is EMPTY means the legal values are not knowable here -- a
	// namespace name -- so only the generic bounds apply. The two are
	// deliberately different, and `reflect.DeepEqual` below tells them apart.
	//
	// The mirror assertion is "the parameterized widgets and their declared
	// keys are pinned" in frontend/lib/dashboard/registry_test.ts.
	t.Run("widget specs", func(t *testing.T) {
		want := map[string]widgetSpec{
			"active-alerts":  {MinW: 2, MinH: 3},
			"cluster-health": {MinW: 3, MinH: 4},
			"cpu-tile":       {MinW: 2, MinH: 2},
			"diagnostics-summary": {
				MinW:   3,
				MinH:   3,
				Params: map[string][]string{paramKeyNamespace: {}},
			},
			"memory-tile":          {MinW: 2, MinH: 2},
			"network-tile":         {MinW: 2, MinH: 2},
			"nodes":                {MinW: 3, MinH: 4},
			"pod-status":           {MinW: 3, MinH: 4},
			"pods-tile":            {MinW: 2, MinH: 2},
			"recent-events":        {MinW: 3, MinH: 3},
			"resource-utilization": {MinW: 4, MinH: 4},
		}

		for id, w := range want {
			t.Run(id, func(t *testing.T) {
				got, ok := allowedWidgets[id]
				if !ok {
					t.Fatalf("%s is missing from allowedWidgets", id)
				}
				if got.MinW != w.MinW || got.MinH != w.MinH {
					t.Fatalf("%s minimum drift: got minW=%d minH=%d, want minW=%d minH=%d "+
						"(update frontend/components/dashboard/widgets/ to match, or update this test)",
						id, got.MinW, got.MinH, w.MinW, w.MinH)
				}
				if !reflect.DeepEqual(got.Params, w.Params) {
					t.Fatalf("%s parameter drift: got %v, want %v (update the widget's `params` "+
						"in frontend/components/dashboard/widgets/ and its pin in "+
						"frontend/lib/dashboard/registry_test.ts to match, or update this test)",
						id, got.Params, w.Params)
				}
				// A namespace parameter is the one key the read path
				// re-authorizes (R5). A widget that meant to take one and
				// spelled it differently would store and render identically
				// and simply never be withheld, so the spelling is pinned
				// rather than left to the catalog literal above to match by
				// eye.
				for key := range got.Params {
					if key != paramKeyNamespace && len(got.Params[key]) == 0 {
						t.Fatalf("%s declares an open-valued parameter %q; only %q is "+
							"re-authorized on read, so an open value under any other key "+
							"is unvalidated caller text in a stored layout",
							id, key, paramKeyNamespace)
					}
				}
				// A minimum below 1 would be a zero-area widget. The validator
				// floors it independently, so this is the catalog's own bound.
				if got.MinW < 1 || got.MinH < 1 {
					t.Fatalf("%s declares a non-positive minimum: minW=%d minH=%d",
						id, got.MinW, got.MinH)
				}
				// A widget that cannot fit the grid could never be placed.
				if got.MinW > dashboardColumns {
					t.Fatalf("%s minW=%d exceeds the %d-column grid", id, got.MinW, dashboardColumns)
				}
			})
		}
	})

	t.Run("numbers", func(t *testing.T) {
		const (
			prefTypes = "frontend/lib/preference-types.ts"
			dashTypes = "frontend/lib/dashboard/types.ts"
		)

		cases := []struct {
			name        string
			got         int
			want        int
			tsConstName string
			tsFile      string
		}{
			{"SavedViewSchemaVersion", SavedViewSchemaVersion, 1, "SAVED_VIEW_SCHEMA_VERSION", prefTypes},
			{"PinSchemaVersion", PinSchemaVersion, 1, "PIN_SCHEMA_VERSION", prefTypes},
			{"MaxSavedViewsPerUser", MaxSavedViewsPerUser, 100, "MAX_SAVED_VIEWS", prefTypes},
			{"MaxPinsPerUser", MaxPinsPerUser, 200, "MAX_PINS", prefTypes},
			{"maxRecordNameLen", maxRecordNameLen, 128, "MAX_RECORD_NAME_LEN", prefTypes},
			{"maxSearchLen", maxSearchLen, 256, "MAX_SEARCH_LEN", prefTypes},

			// The dashboard geometry is declared on both sides because the
			// editor has to refuse an out-of-bounds drag before a round trip.
			// A client laying out 16 columns against a server that stores 12
			// would have every save rejected with a message about a number the
			// user never chose.
			{"DashboardLayoutSchemaVersion", DashboardLayoutSchemaVersion, 1, "DASHBOARD_LAYOUT_SCHEMA_VERSION", dashTypes},
			{"dashboardColumns", dashboardColumns, 12, "DASHBOARD_COLUMNS", dashTypes},
			{"maxDashboardItems", maxDashboardItems, 40, "DASHBOARD_MAX_ITEMS", dashTypes},
			// The row cap was the one enforced geometry bound with no client
			// mirror: the editor clamped width against the column count but
			// left height unbounded, so a drag past the cap produced a save
			// the client had no way to predict would fail. It is also the one
			// constant with no independent literal anywhere, which is what
			// made the fuzz oracle's reuse of it undetectable.
			{"maxDashboardRows", maxDashboardRows, 200, "DASHBOARD_MAX_ROWS", dashTypes},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.got != tc.want {
					t.Fatalf("%s drift: got %d, want %d (update %s %s to match, or update this test)",
						tc.name, tc.got, tc.want, tc.tsFile, tc.tsConstName)
				}
			})
		}
	})

	// The dedup key is derived on both sides — the server to enforce
	// uniqueness, the client to predict a collision before saving. A change to
	// either derivation that is not mirrored makes the client's prediction
	// wrong: it would show a pin as new that the server then refuses as
	// already pinned.
	t.Run("PinDedupKey derivation", func(t *testing.T) {
		got := PinDedupKey(PinConfig{
			ResourceKind: "deployments",
			Namespace:    "default",
			Name:         "api",
			UID:          "ignored-by-design",
		})
		const want = "deployments/default/api"
		if got != want {
			t.Fatalf("PinDedupKey drift: got %q, want %q (update frontend/lib/preference-types.ts pinDedupKey to match, or update this test)",
				got, want)
		}
	})
}

// widgetIDSet derives the catalog's id set for the allowlist table above,
// which compares string sets. Deriving it rather than maintaining a second
// literal keeps the id pin and the per-widget spec pin describing the same
// map: a widget removed from allowedWidgets fails both, never just one.
func widgetIDSet() map[string]struct{} {
	ids := make(map[string]struct{}, len(allowedWidgets))
	for id := range allowedWidgets {
		ids[id] = struct{}{}
	}
	return ids
}
