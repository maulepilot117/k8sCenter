package server

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCapabilityContractParity is the ONLY half of this pairing that can
// actually detect cross-language drift. It reads the live TypeScript source
// at frontend/lib/capability-types.ts, extracts the REASON_CODES and
// CAPABILITY_OPERATION_IDS array literals by parsing the file text, and
// diffs both sets against the live Go values (validReasonCodes,
// capabilityOperations) in this package. A value added to, or removed from,
// either side — Go or TypeScript — without touching the other fails here,
// and the failure names the exact value and which side has it.
//
// frontend/lib/capability-types_test.ts is NOT a parity check against Go
// (it cannot see this package): it only asserts that the TS union type and
// the TS runtime array agree with each other, which this test cannot check
// from Go. The two tests are complementary, not redundant: this one is the
// cross-language guard, that one is the intra-language self-consistency
// guard.
//
// This mirrors backend/internal/preferences/parity_test.go's mechanism
// (TestContractParity) in spirit — pinning a Go-side closed set — but that
// test compares Go against a hardcoded literal in the same Go file and is
// itself circular; it is out of scope here and is not modified by this
// change.
func TestCapabilityContractParity(t *testing.T) {
	tsPath := filepath.Join("..", "..", "..", "frontend", "lib", "capability-types.ts")
	src, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("could not read %s to cross-check against Go: %v", tsPath, err)
	}

	tsReasonCodes := extractStringArrayLiteral(t, string(src), "REASON_CODES")
	tsOperationIDs := extractStringArrayLiteral(t, string(src), "CAPABILITY_OPERATION_IDS")

	t.Run("ReasonCode", func(t *testing.T) {
		goCodes := make([]string, 0, len(validReasonCodes))
		for k := range validReasonCodes {
			goCodes = append(goCodes, string(k))
		}
		compareSets(t, "ReasonCode", goCodes, tsReasonCodes,
			"backend/internal/server/handle_capabilities.go validReasonCodes",
			"frontend/lib/capability-types.ts REASON_CODES")
	})

	t.Run("CapabilityOperationId", func(t *testing.T) {
		goIDs := make([]string, 0, len(capabilityOperations))
		for _, op := range capabilityOperations {
			goIDs = append(goIDs, op.ID)
		}
		compareSets(t, "CapabilityOperationId", goIDs, tsOperationIDs,
			"backend/internal/server/handle_capabilities.go capabilityOperations",
			"frontend/lib/capability-types.ts CAPABILITY_OPERATION_IDS")
	})
}

// extractStringArrayLiteral pulls the quoted string elements out of a
// top-level `export const <arrayName> = [ ... ] as const;` declaration in
// TypeScript source, using a regex over the source text (this package has
// no TS parser available). It fails the test loudly — rather than returning
// an empty slice — whenever the declaration can't be located or contains no
// string literals, so a broken/renamed declaration cannot silently degrade
// this into a vacuous "compare against nothing" pass.
func extractStringArrayLiteral(t *testing.T, src, arrayName string) []string {
	t.Helper()

	declRe := regexp.MustCompile(`(?s)export const ` + regexp.QuoteMeta(arrayName) + `\s*=\s*\[(.*?)\]\s*as const\s*;`)
	m := declRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("could not locate `export const %s = [...] as const;` in frontend/lib/capability-types.ts — "+
			"parity check aborted rather than silently comparing against an empty set "+
			"(did the declaration get renamed or reformatted?)", arrayName)
	}

	itemRe := regexp.MustCompile(`"([^"]+)"`)
	matches := itemRe.FindAllStringSubmatch(m[1], -1)
	if len(matches) == 0 {
		t.Fatalf("found the %s declaration in frontend/lib/capability-types.ts but parsed zero string "+
			"literals out of it — parity check aborted rather than silently comparing against an empty set",
			arrayName)
	}

	values := make([]string, 0, len(matches))
	for _, mm := range matches {
		values = append(values, mm[1])
	}
	return values
}

// compareSets fails t with the exact values present on only one side,
// naming which file (goLabel/tsLabel) needs the corresponding edit.
func compareSets(t *testing.T, setName string, goValues, tsValues []string, goLabel, tsLabel string) {
	t.Helper()

	goSet := toSet(goValues)
	tsSet := toSet(tsValues)

	var onlyInGo, onlyInTS []string
	for v := range goSet {
		if !tsSet[v] {
			onlyInGo = append(onlyInGo, v)
		}
	}
	for v := range tsSet {
		if !goSet[v] {
			onlyInTS = append(onlyInTS, v)
		}
	}
	sort.Strings(onlyInGo)
	sort.Strings(onlyInTS)

	if len(onlyInGo) == 0 && len(onlyInTS) == 0 {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s drift between %s and %s:\n", setName, goLabel, tsLabel)
	if len(onlyInGo) > 0 {
		fmt.Fprintf(&b, "  present in %s but missing from %s: %v\n", goLabel, tsLabel, onlyInGo)
	}
	if len(onlyInTS) > 0 {
		fmt.Fprintf(&b, "  present in %s but missing from %s: %v\n", tsLabel, goLabel, onlyInTS)
	}
	t.Fatal(b.String())
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}
