package preferences

import (
	"sort"
	"testing"
)

// TestContractParity pins every Go-side value that the browser re-declares in
// frontend/lib/preference-types.ts to an expected literal. A change on either
// side without updating the other fails here (Go side) or in
// frontend/lib/preference-types_test.ts (TS side), and each failure names the
// other file so the next reader knows where the second edit belongs.
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
		}{
			{
				name:        "allowedStatusFilters",
				got:         allowedStatusFilters,
				want:        []string{"all", "failed", "pending", "progressing", "running"},
				tsConstName: "SAVED_VIEW_STATUS_FILTERS",
			},
			{
				name:        "allowedSortKeys",
				got:         allowedSortKeys,
				want:        []string{"age", "name", "namespace"},
				tsConstName: "SAVED_VIEW_SORT_KEYS",
			},
			{
				name:        "allowedSortDirs",
				got:         allowedSortDirs,
				want:        []string{"asc", "desc"},
				tsConstName: "SAVED_VIEW_SORT_DIRS",
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
					t.Fatalf("%s drift: got %v, want %v (update frontend/lib/preference-types.ts %s to match, or update this test)",
						tc.name, got, tc.want, tc.tsConstName)
				}
				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("%s drift: got %v, want %v (update frontend/lib/preference-types.ts %s to match, or update this test)",
							tc.name, got, tc.want, tc.tsConstName)
					}
				}
			})
		}
	})

	t.Run("numbers", func(t *testing.T) {
		cases := []struct {
			name        string
			got         int
			want        int
			tsConstName string
		}{
			{"SavedViewSchemaVersion", SavedViewSchemaVersion, 1, "SAVED_VIEW_SCHEMA_VERSION"},
			{"PinSchemaVersion", PinSchemaVersion, 1, "PIN_SCHEMA_VERSION"},
			{"MaxSavedViewsPerUser", MaxSavedViewsPerUser, 100, "MAX_SAVED_VIEWS"},
			{"MaxPinsPerUser", MaxPinsPerUser, 200, "MAX_PINS"},
			{"maxRecordNameLen", maxRecordNameLen, 128, "MAX_RECORD_NAME_LEN"},
			{"maxSearchLen", maxSearchLen, 256, "MAX_SEARCH_LEN"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.got != tc.want {
					t.Fatalf("%s drift: got %d, want %d (update frontend/lib/preference-types.ts %s to match, or update this test)",
						tc.name, tc.got, tc.want, tc.tsConstName)
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
