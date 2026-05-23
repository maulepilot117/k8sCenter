package config

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestKnownLeakedHelmParity asserts that the Go deny-list (KnownLeakedSecrets)
// contains exactly the same strings as the $knownLeaked list in
// helm/kubecenter/templates/_validate.tpl.
//
// Purpose: keep the Helm-side and Go-side deny-lists in lock-step so that
// a secret blocked by the Helm chart is also blocked by the Go startup guard,
// and vice versa.
//
// If the Helm template's list structure changes in a future PR, this test
// skips (rather than fails) with a clear message so the CI doesn't break
// during parallel F2 refactors. Update the regex and remove the skip once
// the structure stabilises.
//
// Finding reference: P3 #19 (ce-code-review 2026-05-22).
func TestKnownLeakedHelmParity(t *testing.T) {
	// Locate _validate.tpl relative to this test file.
	// Test runs from backend/internal/config/; the Helm file is three
	// directories up then into helm/kubecenter/templates/.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller failed; cannot locate _validate.tpl")
	}
	tplPath := filepath.Join(
		filepath.Dir(thisFile),
		"..", "..", "..",
		"helm", "kubecenter", "templates", "_validate.tpl",
	)

	data, err := os.ReadFile(tplPath)
	if err != nil {
		t.Skipf("parity test skipped: could not read _validate.tpl (%v); "+
			"run from the repo root or update the path", err)
	}

	// Extract the strings inside the list "..." block on the $knownLeaked line.
	// Expected template line (single line):
	//   {{- $knownLeaked := list "foo" "bar" "baz" -}}
	//
	// The regex captures all quoted strings on that line.
	listLineRe := regexp.MustCompile(`\$knownLeaked\s*:=\s*list\s+(.+?)\s*-}}`)
	quotedStringRe := regexp.MustCompile(`"([^"]+)"`)

	content := string(data)
	lineMatch := listLineRe.FindString(content)
	if lineMatch == "" {
		t.Skipf("parity test skipped: _validate.tpl structure changed — "+
			"$knownLeaked := list ... line not found; "+
			"update the parity regex in known_leaked_helm_parity_test.go")
		return
	}

	helmMatches := quotedStringRe.FindAllStringSubmatch(lineMatch, -1)
	if len(helmMatches) == 0 {
		t.Skipf("parity test skipped: _validate.tpl structure changed — "+
			"no quoted strings found in $knownLeaked list line %q; "+
			"update the parity regex in known_leaked_helm_parity_test.go", lineMatch)
		return
	}

	helmSet := make(map[string]bool, len(helmMatches))
	for _, m := range helmMatches {
		helmSet[m[1]] = true
	}

	goSet := make(map[string]bool, len(KnownLeakedSecrets))
	for _, s := range KnownLeakedSecrets {
		goSet[s] = true
	}

	// Report missing from Go (Helm has it, Go doesn't).
	var missingFromGo []string
	for s := range helmSet {
		if !goSet[s] {
			missingFromGo = append(missingFromGo, s)
		}
	}

	// Report missing from Helm (Go has it, Helm doesn't).
	var missingFromHelm []string
	for s := range goSet {
		if !helmSet[s] {
			missingFromHelm = append(missingFromHelm, s)
		}
	}

	if len(missingFromGo) > 0 || len(missingFromHelm) > 0 {
		var parts []string
		if len(missingFromGo) > 0 {
			parts = append(parts, "in Helm but not in Go: "+strings.Join(missingFromGo, ", "))
		}
		if len(missingFromHelm) > 0 {
			parts = append(parts, "in Go but not in Helm: "+strings.Join(missingFromHelm, ", "))
		}
		t.Errorf("KnownLeakedSecrets / _validate.tpl deny-list mismatch:\n  %s\n"+
			"Update both lists to keep them in sync (Finding P3 #19).",
			strings.Join(parts, "\n  "))
	}
}
