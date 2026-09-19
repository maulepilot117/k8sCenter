package preferences

import (
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
				name: "allowedWidgetIDs",
				got:  allowedWidgetIDs,
				want: []string{
					"active-alerts", "cluster-health", "cpu-tile", "memory-tile",
					"network-tile", "nodes", "pod-status", "pods-tile",
					"recent-events", "resource-utilization",
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
