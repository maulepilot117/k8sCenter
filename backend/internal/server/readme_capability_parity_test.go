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

// readmeCapabilityHeading is the README section whose table documents
// capabilityOperations for readers. The table claims to mirror the Go table;
// TestReadmeCapabilityTableParity is what makes that claim true.
const readmeCapabilityHeading = "## Remote cluster support"

// readmeRemotePartialAllowed lists the operations whose README Remote cell
// may read "Partial" instead of "Yes". RemoteSupported is a bool, so the Go
// table cannot express "partial"; the README can, and must only do so where
// the remote path genuinely returns less than the local one.
//
// dashboard.summary: the remote summary serves node/pod/service counts and
// capacity, but omits CPU/memory usage, alert counts and the health score
// because there is no remote metrics (Prometheus) binding yet. Those fields
// render as unavailable, so "Yes" would overstate it.
//
// mesh.mtls: remote posture is derived from the target's pods and policies
// only. The Prometheus metric cross-check the local path applies to Istio
// workloads is skipped and reported unavailable, because the only
// Prometheus binding is the local one (R-8 R14).
var readmeRemotePartialAllowed = map[string]bool{
	"dashboard.summary": true,
	"mesh.mtls":         true,
}

// readmeGuardFileRe matches a Go source path such as
// `k8s/resources/counts.go` or `server/handle_ws_logs.go`.
var readmeGuardFileRe = regexp.MustCompile(`[A-Za-z0-9_./-]+\.go\b`)

// readmeCapabilityRow is one parsed row of the README table.
type readmeCapabilityRow struct {
	Line      int // 1-based line number in the source, for failure messages
	Operation string
	Local     string
	Remote    string
	Note      string
}

// TestReadmeCapabilityTableParity reads the live README.md and checks its
// "## Remote cluster support" table against capabilityOperations, so the next
// RemoteSupported (or LocalSupported) flip cannot leave the documentation
// stale without a CI signal. Rows are matched by capabilityOp.Label: the
// README's Operation column must be the exact Label text.
func TestReadmeCapabilityTableParity(t *testing.T) {
	readmePath := filepath.Join("..", "..", "..", "README.md")
	src, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("could not read %s to cross-check against Go: %v", readmePath, err)
	}

	rows, err := parseReadmeCapabilityTable(string(src))
	if err != nil {
		t.Fatalf("README.md: %v — parity check aborted rather than silently comparing against an empty table", err)
	}

	if problems := checkReadmeCapabilityTable(rows, capabilityOperations); len(problems) > 0 {
		t.Fatalf("README.md %q table has drifted from "+
			"backend/internal/server/handle_capabilities.go capabilityOperations:\n  %s",
			readmeCapabilityHeading, strings.Join(problems, "\n  "))
	}
}

// TestReadmeCapabilityTableParity_DetectsDrift proves the checker has teeth:
// it runs the same parse+compare against small mutated tables and asserts
// each kind of drift is reported. Without this, a checker that always
// returned nil would pass the real-README test forever.
func TestReadmeCapabilityTableParity_DetectsDrift(t *testing.T) {
	ops := []capabilityOp{
		{ID: "yaml.apply", Label: "Apply YAML", LocalSupported: true, RemoteSupported: true},
		{ID: "dashboard.summary", Label: "Dashboard summary", LocalSupported: true, RemoteSupported: true},
		{ID: "pod.exec", Label: "Pod exec", LocalSupported: true, RemoteSupported: false},
	}
	const good = "| Apply YAML | Yes | Yes | pinned |\n" +
		"| Dashboard summary | Yes | Partial | no metrics |\n" +
		"| Pod exec | Yes | No | `k8s/resources/pods.go` (501) |\n"

	wrap := func(body string) string {
		return "# Title\n\n" + readmeCapabilityHeading + "\n\nIntro.\n\n" +
			"| Operation | Local | Remote | Note |\n|---|---|---|---|\n" + body +
			"\nTrailing prose.\n\n## Next section\n\n| A | B |\n|---|---|\n| x | y |\n"
	}

	t.Run("baseline passes", func(t *testing.T) {
		rows, err := parseReadmeCapabilityTable(wrap(good))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("parsed %d rows, want 3 (table must stop at the first non-table line)", len(rows))
		}
		if p := checkReadmeCapabilityTable(rows, ops); len(p) > 0 {
			t.Fatalf("baseline table reported drift: %v", p)
		}
	})

	cases := []struct {
		name string
		body string
		want string // substring that must appear in one reported problem
	}{
		{
			name: "remote flipped to supported but README still says No",
			body: strings.Replace(good, "| Pod exec | Yes | No | `k8s/resources/pods.go` (501) |",
				"| Pod exec | Yes | Yes | works now |", 1),
			want: `row "Pod exec": Remote column is "Yes" but RemoteSupported is false`,
		},
		{
			name: "remote regressed but README still says Yes",
			body: strings.Replace(good, "| Apply YAML | Yes | Yes |", "| Apply YAML | Yes | No |", 1),
			want: `row "Apply YAML": Remote column is "No" but RemoteSupported is true`,
		},
		{
			name: "Partial on an operation not allowed to be partial",
			body: strings.Replace(good, "| Apply YAML | Yes | Yes |", "| Apply YAML | Yes | Partial |", 1),
			want: `row "Apply YAML": Remote column is "Partial"`,
		},
		{
			name: "Local column wrong",
			body: strings.Replace(good, "| Pod exec | Yes |", "| Pod exec | No |", 1),
			want: `row "Pod exec": Local column is "No" but LocalSupported is true`,
		},
		{
			name: "No row without a guard file",
			body: strings.Replace(good, "`k8s/resources/pods.go` (501)", "returns 501", 1),
			want: `row "Pod exec": Remote is "No" but the Note names no guard file`,
		},
		{
			name: "operation missing from README",
			body: strings.Replace(good, "| Pod exec | Yes | No | `k8s/resources/pods.go` (501) |\n", "", 1),
			want: `missing row for capabilityOperations entry "pod.exec"`,
		},
		{
			name: "README row names no real operation",
			body: good + "| Dashboard trends | Yes | No | `server/handle_dashboard.go` |\n",
			want: `row "Dashboard trends" does not match any capabilityOperations Label`,
		},
		{
			name: "label renamed in README",
			body: strings.Replace(good, "| Pod exec |", "| Pod Exec |", 1),
			want: `row "Pod Exec" does not match any capabilityOperations Label`,
		},
		{
			name: "duplicate row",
			body: good + "| Apply YAML | Yes | Yes | again |\n",
			want: `row "Apply YAML" appears 2 times`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := parseReadmeCapabilityTable(wrap(tc.body))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			problems := checkReadmeCapabilityTable(rows, ops)
			for _, p := range problems {
				if strings.Contains(p, tc.want) {
					return
				}
			}
			t.Fatalf("mutation not detected: want a problem containing %q, got %v", tc.want, problems)
		})
	}

	t.Run("missing heading is a parse error", func(t *testing.T) {
		if _, err := parseReadmeCapabilityTable("# Title\n\nno table here\n"); err == nil {
			t.Fatal("expected an error when the heading is absent")
		}
	})

	t.Run("renamed columns are a parse error", func(t *testing.T) {
		src := readmeCapabilityHeading + "\n\n| Op | Local | Remote | Note |\n|---|---|---|---|\n" + good
		if _, err := parseReadmeCapabilityTable(src); err == nil {
			t.Fatal("expected an error when the header row does not match")
		}
	})
}

// parseReadmeCapabilityTable locates readmeCapabilityHeading in src and
// returns the rows of the first markdown table beneath it (before the next
// "## " heading). It errors — rather than returning no rows — when the
// heading, the table, or the expected Operation | Local | Remote | Note
// header is missing, or when a row does not have exactly four cells.
func parseReadmeCapabilityTable(src string) ([]readmeCapabilityRow, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == readmeCapabilityHeading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("could not locate the %q heading (was it renamed?)", readmeCapabilityHeading)
	}

	header := -1
	for i := start; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "## ") {
			break
		}
		if strings.HasPrefix(l, "|") {
			header = i
			break
		}
	}
	if header < 0 {
		return nil, fmt.Errorf("no markdown table found under the %q heading", readmeCapabilityHeading)
	}

	wantHeader := []string{"Operation", "Local", "Remote", "Note"}
	gotHeader := splitMarkdownRow(lines[header])
	if strings.Join(gotHeader, "|") != strings.Join(wantHeader, "|") {
		return nil, fmt.Errorf("table under %q has header %v at line %d, want %v",
			readmeCapabilityHeading, gotHeader, header+1, wantHeader)
	}
	if header+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[header+1]), "|---") {
		return nil, fmt.Errorf("table under %q is missing its |---| separator row at line %d",
			readmeCapabilityHeading, header+2)
	}

	var rows []readmeCapabilityRow
	for i := header + 2; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "|") {
			break
		}
		cells := splitMarkdownRow(l)
		if len(cells) != len(wantHeader) {
			return nil, fmt.Errorf("line %d of the %q table has %d cells, want %d: %s",
				i+1, readmeCapabilityHeading, len(cells), len(wantHeader), l)
		}
		rows = append(rows, readmeCapabilityRow{
			Line: i + 1, Operation: cells[0], Local: cells[1], Remote: cells[2], Note: cells[3],
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("the table under %q has no data rows", readmeCapabilityHeading)
	}
	return rows, nil
}

// splitMarkdownRow splits "| a | b |" into ["a", "b"]. The capability table
// has no escaped pipes, so a plain split is sufficient; a cell containing one
// would change the cell count and fail the parse loudly.
func splitMarkdownRow(line string) []string {
	l := strings.TrimSpace(line)
	l = strings.TrimPrefix(l, "|")
	l = strings.TrimSuffix(l, "|")
	parts := strings.Split(l, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// checkReadmeCapabilityTable returns one message per discrepancy between the
// README rows and ops. Each message names the README row (and its line) to
// change and what it should say, so the fix needs no further investigation.
func checkReadmeCapabilityTable(rows []readmeCapabilityRow, ops []capabilityOp) []string {
	var problems []string

	byLabel := make(map[string]capabilityOp, len(ops))
	for _, op := range ops {
		byLabel[op.Label] = op
	}

	seen := make(map[string]int, len(rows))
	for _, r := range rows {
		seen[r.Operation]++
		if seen[r.Operation] == 2 {
			n := 0
			for _, rr := range rows {
				if rr.Operation == r.Operation {
					n++
				}
			}
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q appears %d times; keep exactly one row per operation",
				r.Line, r.Operation, n))
		}
		if seen[r.Operation] > 1 {
			continue
		}

		op, ok := byLabel[r.Operation]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q does not match any capabilityOperations Label; "+
					"rename it to the exact Label text or move it out of the table (only capability operations belong there)",
				r.Line, r.Operation))
			continue
		}

		wantLocal := yesNo(op.LocalSupported)
		if r.Local != wantLocal {
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q: Local column is %q but LocalSupported is %t for %q; change it to %q",
				r.Line, r.Operation, r.Local, op.LocalSupported, op.ID, wantLocal))
		}

		switch {
		case !op.RemoteSupported && r.Remote != "No":
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q: Remote column is %q but RemoteSupported is false for %q; "+
					"change it to \"No\" and name the refusing guard file in the Note",
				r.Line, r.Operation, r.Remote, op.ID))
		case op.RemoteSupported && r.Remote == "No":
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q: Remote column is \"No\" but RemoteSupported is true for %q; "+
					"change it to \"Yes\" and update the Note",
				r.Line, r.Operation, op.ID))
		case op.RemoteSupported && r.Remote == "Partial" && !readmeRemotePartialAllowed[op.ID]:
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q: Remote column is \"Partial\", which is only allowed for %v; "+
					"change it to \"Yes\" (or add %q to readmeRemotePartialAllowed with a reason)",
				r.Line, r.Operation, sortedKeys(readmeRemotePartialAllowed), op.ID))
		case op.RemoteSupported && r.Remote != "Yes" && r.Remote != "Partial":
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q: Remote column is %q; RemoteSupported is true for %q, so it must be \"Yes\"",
				r.Line, r.Operation, r.Remote, op.ID))
		}

		if r.Remote == "No" && !readmeGuardFileRe.MatchString(r.Note) {
			problems = append(problems, fmt.Sprintf(
				"README.md line %d: row %q: Remote is \"No\" but the Note names no guard file; "+
					"cite the .go file that refuses the operation (e.g. `server/handle_x.go`)",
				r.Line, r.Operation))
		}
	}

	for _, op := range ops {
		if seen[op.Label] == 0 {
			problems = append(problems, fmt.Sprintf(
				"README.md: missing row for capabilityOperations entry %q; add \"| %s | %s | %s | ... |\" to the %q table",
				op.ID, op.Label, yesNo(op.LocalSupported), yesNo(op.RemoteSupported), readmeCapabilityHeading))
		}
	}
	return problems
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
