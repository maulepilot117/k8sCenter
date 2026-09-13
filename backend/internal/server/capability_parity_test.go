package server

import (
	"sort"
	"testing"
)

// TestCapabilityContractParity pins the Go-side closed sets that
// frontend/lib/capability-types.ts re-declares — REASON_CODES (mirroring
// ReasonCode) and CAPABILITY_OPERATION_IDS (mirroring CapabilityOperationId)
// — to expected literals. A change on either side without updating the other
// fails here (Go side) or in frontend/lib/capability-types_test.ts (TS
// side), and each failure names the other file so the next reader knows
// where the second edit belongs.
//
// This mirrors backend/internal/preferences/parity_test.go's mechanism
// (TestContractParity) rather than inventing a new one: three later units
// (U9a, U9b, U10) are scheduled to edit capabilityOperations, and nothing
// else — no test imports capability-types.ts, and no Go test cross-checked
// the unions — kept the two sides honest before this test existed.
func TestCapabilityContractParity(t *testing.T) {
	t.Run("ReasonCode", func(t *testing.T) {
		want := []string{
			"authz_unknown",
			"cluster_unknown",
			"credentials_invalid",
			"db_unavailable",
			"discovery_missing",
			"discovery_unavailable",
			"forbidden",
			"ok",
			"stale_observation",
			"unreachable",
			"unsupported_platform",
		}

		got := make([]string, 0, len(validReasonCodes))
		for k := range validReasonCodes {
			got = append(got, string(k))
		}
		sort.Strings(got)

		if len(got) != len(want) {
			t.Fatalf("ReasonCode drift: got %v, want %v (update frontend/lib/capability-types.ts REASON_CODES to match, or update this test)",
				got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("ReasonCode drift: got %v, want %v (update frontend/lib/capability-types.ts REASON_CODES to match, or update this test)",
					got, want)
			}
		}
	})

	t.Run("CapabilityOperationId", func(t *testing.T) {
		want := []string{
			"dashboard.summary",
			"eso.write",
			"flows.stream",
			"logs.search",
			"logs.stream",
			"pod.exec",
			"resources.counts",
			"yaml.apply",
			"yaml.diff",
			"yaml.export",
			"yaml.validate",
		}

		got := make([]string, 0, len(capabilityOperations))
		for _, op := range capabilityOperations {
			got = append(got, op.ID)
		}
		sort.Strings(got)

		if len(got) != len(want) {
			t.Fatalf("CapabilityOperationId drift: got %v, want %v (update frontend/lib/capability-types.ts CAPABILITY_OPERATION_IDS to match, or update this test)",
				got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("CapabilityOperationId drift: got %v, want %v (update frontend/lib/capability-types.ts CAPABILITY_OPERATION_IDS to match, or update this test)",
					got, want)
			}
		}
	})
}
