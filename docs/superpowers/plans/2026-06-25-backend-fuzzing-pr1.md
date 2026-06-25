# Backend Fuzzing — PR #1 (Layer A security targets + nightly CI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first batch of Go native fuzz targets — the four highest-value security seams (LogQL namespace enforcement, SSRF IP gate, two composite-ID parsers) — plus a nightly scheduled GitHub Actions job that deep-fuzzes every target with a time budget.

**Architecture:** Each fuzz target is a `Fuzz<Name>(f *testing.F)` function in a `_test.go` file in the **same package** as the function under test (the three core targets are unexported, so in-package is required). Seed corpus runs as a normal unit test on every PR (existing CI); a new `fuzz.yml` matrix workflow runs `go test -fuzz` per target nightly. Targets assert crash-safety always, plus their specific security oracle on the success branch.

**Tech Stack:** Go 1.26 native fuzzing (`testing.F`), GitHub Actions.

## Global Constraints

- Go version floor: **Go 1.26** (native fuzzing; matches `backend/go.mod`).
- Repo-wide verification before push (CLAUDE.md Rule 4): `cd backend && go vet ./... && go test ./...` — must pass tree-wide, not just changed packages.
- No new third-party dependencies (native `testing` fuzzing only; honors the 7-day supply-chain cooldown by adding nothing).
- Fuzz targets must not perform network I/O, hit a real cluster, or touch Postgres. The SSRF target fuzzes the pure IP-decision core, not the DNS-resolving wrapper.
- Branch from `main`; never commit to `main` directly. Work continues on the existing `feat/backend-fuzzing-design` branch or a fresh `feat/backend-fuzzing-pr1` branch.
- Oracles assert invariants only on the **success** branch; any returned `error` on random input is acceptable (random input is expected to be rejected).

---

## File Structure

- `backend/internal/loki/security_fuzz_test.go` — Create. `FuzzEnforceNamespaces`.
- `backend/internal/k8s/ssrf_fuzz_test.go` — Create. `FuzzCheckIPNotPrivate`, `FuzzValidateRemoteURLContext`.
- `backend/internal/gitops/composite_id_fuzz_test.go` — Create. `FuzzParseCompositeID`.
- `backend/internal/servicemesh/composite_id_fuzz_test.go` — Create. `FuzzParseMeshCompositeID`.
- `.github/workflows/fuzz.yml` — Create. Nightly matrixed deep-fuzz job.

No production files are modified in PR #1.

---

### Task 1: LogQL namespace-enforcement fuzz target

Fuzz `loki.EnforceNamespaces`. Oracle: crash-safety + on non-admin success, every `namespace` matcher in the **output** selects only allowed namespaces (LogQL injection / namespace-escape defense). The target re-parses the output with the package's own `parseMatchers`, so it tracks the real implementation.

**Files:**
- Create: `backend/internal/loki/security_fuzz_test.go`

**Interfaces:**
- Consumes (in-package, already defined in `security.go`): `func EnforceNamespaces(query string, allowedNamespaces []string) (string, error)`; `type matcher struct { label, op, value string }`; `func parseMatchers(content string) ([]matcher, error)`; `func findClosingBrace(s string) int`; `const maxQueryLen = 4096`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the fuzz target**

```go
package loki

import (
	"strings"
	"testing"
)

// FuzzEnforceNamespaces fuzzes the non-admin namespace-rewrite path.
// Oracle: when EnforceNamespaces succeeds for a restricted (non-admin)
// caller, the rewritten query's stream selector must contain only
// namespace matchers that reference the allowed set — never a namespace
// the caller was not granted. A regression here is a tenant-isolation
// (namespace-escape) bug, not merely a crash.
func FuzzEnforceNamespaces(f *testing.F) {
	// Seeds: valid queries, an injected disallowed namespace (teeth),
	// pipeline stages, regex matchers, and boundary shapes.
	f.Add(`{app="web"}`)
	f.Add(`{namespace="team-a"}`)
	f.Add(`{namespace="team-evil"} |= "secret"`) // teeth: disallowed ns must be stripped
	f.Add(`{namespace=~"team-.*"}`)
	f.Add(`{app="web", namespace!="team-a"}`)
	f.Add(`{`)
	f.Add(``)
	f.Add(strings.Repeat("{", 5000)) // exceeds maxQueryLen

	allowed := []string{"team-a", "team-b"}
	allowedSet := map[string]bool{"team-a": true, "team-b": true}

	f.Fuzz(func(t *testing.T, query string) {
		out, err := EnforceNamespaces(query, allowed)
		if err != nil {
			return // rejection of malformed/oversized input is correct
		}

		// Success invariant 1: bounded output (no runaway expansion).
		if len(out) > maxQueryLen*4 {
			t.Fatalf("output grew unboundedly: %d bytes from %d-byte input", len(out), len(query))
		}

		// Success invariant 2: output must still be a stream selector.
		trimmed := strings.TrimSpace(out)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			t.Fatalf("rewritten query is not a stream selector: %q", out)
		}
		closeIdx := findClosingBrace(trimmed)
		if closeIdx < 0 {
			t.Fatalf("rewritten query has unclosed selector: %q", out)
		}

		// Success invariant 3 (the security oracle): every namespace
		// matcher in the output references only allowed namespaces.
		matchers, perr := parseMatchers(trimmed[1:closeIdx])
		if perr != nil {
			t.Fatalf("rewritten query failed to re-parse: %v (out=%q)", perr, out)
		}
		for _, m := range matchers {
			if m.label != "namespace" {
				continue
			}
			for _, v := range splitRegexAlternatives(m.value, m.op) {
				if v == "" {
					continue
				}
				if !allowedSet[v] {
					t.Fatalf("namespace-escape: output selects disallowed namespace %q (op=%q) from input %q -> output %q",
						v, m.op, query, out)
				}
			}
		}
	})
}

// splitRegexAlternatives expands a namespace matcher value into the
// concrete namespace names it can select. For "=" / "!=" the value is
// literal; for "=~" / "!~" the enforcement layer emits a simple
// "a|b" alternation, so split on '|'. Any other regex metacharacter
// means the value is not a plain alternation — treat the whole string
// as one token so the oracle errs toward flagging, not toward passing.
func splitRegexAlternatives(value, op string) []string {
	if op == "=" || op == "!=" {
		return []string{value}
	}
	if strings.ContainsAny(value, ".*+?()[]{}\\^$") {
		return []string{value}
	}
	return strings.Split(value, "|")
}
```

- [ ] **Step 2: Run the seed corpus as a normal test (verify it passes on current code)**

Run: `cd backend && go test ./internal/loki/ -run=FuzzEnforceNamespaces -v`
Expected: PASS (the seeds exercise the target; current `EnforceNamespaces` is correct, so the oracle holds).

- [ ] **Step 3: Run a short live fuzz to confirm no quick crash**

Run: `cd backend && go test ./internal/loki/ -run=^$ -fuzz=^FuzzEnforceNamespaces$ -fuzztime=30s`
Expected: `elapsed: 30s ... no failures`. If it finds a crash, that is a real bug — stop and report it (do not weaken the oracle to make it pass).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/loki/security_fuzz_test.go
git commit -m "test(loki): fuzz EnforceNamespaces for namespace-escape and crashes"
```

---

### Task 2: SSRF IP-gate fuzz targets

Fuzz the pure SSRF decision core `checkIPNotPrivate(ip net.IP) error` (no network) and add a crash-only target for `ValidateRemoteURLContext` driven with a pre-cancelled context so it never performs real DNS.

**Files:**
- Create: `backend/internal/k8s/ssrf_fuzz_test.go`

**Interfaces:**
- Consumes (in-package): `func checkIPNotPrivate(ip net.IP) error`; `func ValidateRemoteURLContext(ctx context.Context, apiServerURL string) error`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the fuzz target**

```go
package k8s

import (
	"context"
	"net"
	"testing"
)

// FuzzCheckIPNotPrivate fuzzes the pure SSRF IP-classification core.
// Oracle: every address Go's stdlib classifies as loopback, private
// (RFC1918), link-local (incl. 169.254.169.254 cloud metadata), or
// unspecified MUST be rejected. A regression that lets any of these
// through is an SSRF hole. (CGNAT 100.64.0.0/10 is also rejected by
// the implementation but uses a package-internal net; the stdlib
// predicates below are the load-bearing security classes.)
func FuzzCheckIPNotPrivate(f *testing.F) {
	// 4-byte (IPv4) and 16-byte (IPv6) seeds spanning each blocked class.
	f.Add([]byte{127, 0, 0, 1})              // loopback
	f.Add([]byte{10, 0, 0, 1})               // private
	f.Add([]byte{192, 168, 1, 1})            // private
	f.Add([]byte{169, 254, 169, 254})        // link-local / metadata (teeth)
	f.Add([]byte{0, 0, 0, 0})                // unspecified
	f.Add([]byte{8, 8, 8, 8})                // public (should pass)
	f.Add(make([]byte, 16))                  // IPv6 unspecified
	f.Add([]byte{1, 2, 3})                   // malformed length

	f.Fuzz(func(t *testing.T, raw []byte) {
		// Only 4- and 16-byte slices form a valid net.IP; others yield
		// an unusable IP and are not a meaningful SSRF input.
		if len(raw) != 4 && len(raw) != 16 {
			return
		}
		ip := net.IP(raw)
		err := checkIPNotPrivate(ip)

		mustReject := ip.IsLoopback() ||
			ip.IsPrivate() ||
			ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified()

		if mustReject && err == nil {
			t.Fatalf("SSRF hole: checkIPNotPrivate accepted blocked address %s", ip)
		}
	})
}

// FuzzValidateRemoteURLContext is crash-safety only. A pre-cancelled
// context guarantees the DNS path (LookupIPAddr) returns immediately
// without real network I/O, so the fuzzer exercises only URL parsing
// and the IP-literal branch. Oracle: never panics.
func FuzzValidateRemoteURLContext(f *testing.F) {
	f.Add("https://10.0.0.1:6443")
	f.Add("https://169.254.169.254:6443")
	f.Add("https://example.com")
	f.Add("not-a-url")
	f.Add("")
	f.Add("https://[::1]:6443")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: hostname lookups fail fast, no real DNS

	f.Fuzz(func(t *testing.T, rawURL string) {
		_ = ValidateRemoteURLContext(ctx, rawURL) // must not panic
	})
}
```

- [ ] **Step 2: Run the seed corpus as a normal test**

Run: `cd backend && go test ./internal/k8s/ -run='FuzzCheckIPNotPrivate|FuzzValidateRemoteURLContext' -v`
Expected: PASS.

- [ ] **Step 3: Run short live fuzz on each target**

Run: `cd backend && go test ./internal/k8s/ -run=^$ -fuzz=^FuzzCheckIPNotPrivate$ -fuzztime=30s`
Then: `cd backend && go test ./internal/k8s/ -run=^$ -fuzz=^FuzzValidateRemoteURLContext$ -fuzztime=30s`
Expected: no failures for either. A failure in the first is a real SSRF gap — report it.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/k8s/ssrf_fuzz_test.go
git commit -m "test(k8s): fuzz SSRF IP gate and remote-URL validation"
```

---

### Task 3: GitOps composite-ID parser fuzz target

Fuzz `gitops.parseCompositeID`. Oracle: crash-safety + round-trip — for any three colon-free, non-empty parts, `parseCompositeID(a:b:c)` returns exactly `(a, b, c)`.

**Files:**
- Create: `backend/internal/gitops/composite_id_fuzz_test.go`

**Interfaces:**
- Consumes (in-package): `func parseCompositeID(id string) (tool, namespace, name string, err error)`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the fuzz target**

```go
package gitops

import (
	"strings"
	"testing"
)

// FuzzParseCompositeID fuzzes the "tool:namespace:name" parser.
// Crash-safety on arbitrary input, plus a round-trip oracle: any three
// non-empty parts that contain no ':' must parse back to themselves.
func FuzzParseCompositeID(f *testing.F) {
	f.Add("argo:default:my-app")
	f.Add("flux:kube-system:podinfo")
	f.Add("a:b:c:d") // SplitN(.,3) keeps "c:d" as the third part
	f.Add("::")
	f.Add("")
	f.Add("argo%3Adefault:x") // URL-encoded colon (PathUnescape path)

	f.Fuzz(func(t *testing.T, a, b, c string) {
		// Crash-safety on the raw concatenation (covers odd unescape input).
		_, _, _, _ = parseCompositeID(a + ":" + b + ":" + c)

		// Round-trip oracle: only when parts are well-formed (non-empty,
		// colon-free, not percent-decodable into a colon). Skip parts that
		// PathUnescape would alter, since that path intentionally rewrites.
		if a == "" || b == "" || c == "" {
			return
		}
		if strings.ContainsAny(a+b+c, ":%") {
			return
		}
		tool, ns, name, err := parseCompositeID(a + ":" + b + ":" + c)
		if err != nil {
			t.Fatalf("well-formed id %q:%q:%q rejected: %v", a, b, c, err)
		}
		if tool != a || ns != b || name != c {
			t.Fatalf("round-trip mismatch: got (%q,%q,%q) want (%q,%q,%q)", tool, ns, name, a, b, c)
		}
	})
}
```

- [ ] **Step 2: Run the seed corpus as a normal test**

Run: `cd backend && go test ./internal/gitops/ -run=FuzzParseCompositeID -v`
Expected: PASS.

- [ ] **Step 3: Run short live fuzz**

Run: `cd backend && go test ./internal/gitops/ -run=^$ -fuzz=^FuzzParseCompositeID$ -fuzztime=30s`
Expected: no failures.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/gitops/composite_id_fuzz_test.go
git commit -m "test(gitops): fuzz composite-ID parser round-trip"
```

---

### Task 4: Service-mesh composite-ID parser fuzz target

Fuzz `servicemesh.parseMeshCompositeID` (four parts: `mesh:namespace:kind:name`). Same crash-safety + round-trip oracle as Task 3, adapted to four parts.

**Files:**
- Create: `backend/internal/servicemesh/composite_id_fuzz_test.go`

**Interfaces:**
- Consumes (in-package): `func parseMeshCompositeID(id string) (mesh, namespace, code, name string, err error)`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the fuzz target**

```go
package servicemesh

import (
	"strings"
	"testing"
)

// FuzzParseMeshCompositeID fuzzes the "mesh:namespace:kind:name" parser.
// Crash-safety plus a round-trip oracle for four well-formed parts.
func FuzzParseMeshCompositeID(f *testing.F) {
	f.Add("istio:default:vs:reviews")
	f.Add("linkerd:emojivoto:sp:web")
	f.Add(":::")
	f.Add("")
	f.Add("istio:default:vs:a:b") // SplitN(.,4): 5 colons -> 4 parts, last keeps "a:b"

	f.Fuzz(func(t *testing.T, a, b, c, d string) {
		_, _, _, _, _ = parseMeshCompositeID(a + ":" + b + ":" + c + ":" + d)

		if a == "" || b == "" || c == "" || d == "" {
			return
		}
		if strings.ContainsAny(a+b+c+d, ":%") {
			return
		}
		mesh, ns, code, name, err := parseMeshCompositeID(a + ":" + b + ":" + c + ":" + d)
		if err != nil {
			t.Fatalf("well-formed id %q:%q:%q:%q rejected: %v", a, b, c, d, err)
		}
		if mesh != a || ns != b || code != c || name != d {
			t.Fatalf("round-trip mismatch: got (%q,%q,%q,%q) want (%q,%q,%q,%q)",
				mesh, ns, code, name, a, b, c, d)
		}
	})
}
```

- [ ] **Step 2: Run the seed corpus as a normal test**

Run: `cd backend && go test ./internal/servicemesh/ -run=FuzzParseMeshCompositeID -v`
Expected: PASS.

- [ ] **Step 3: Run short live fuzz**

Run: `cd backend && go test ./internal/servicemesh/ -run=^$ -fuzz=^FuzzParseMeshCompositeID$ -fuzztime=30s`
Expected: no failures.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/servicemesh/composite_id_fuzz_test.go
git commit -m "test(servicemesh): fuzz mesh composite-ID parser round-trip"
```

---

### Task 5: Nightly deep-fuzz GitHub Actions workflow

Add a scheduled, matrixed workflow that runs each fuzz target with a 5-minute budget. On failure it uploads the crash reproducer under `testdata/fuzz/` as an artifact so it can be committed as a regression seed. Also `workflow_dispatch` for on-demand runs.

**Files:**
- Create: `.github/workflows/fuzz.yml`

**Interfaces:**
- Consumes: the four `Fuzz*` targets from Tasks 1–4 (referenced by package path + name in the matrix).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the workflow**

```yaml
name: Fuzz

on:
  schedule:
    - cron: "0 4 * * *" # nightly 04:00 UTC
  workflow_dispatch: {}

permissions:
  contents: read

jobs:
  fuzz:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { pkg: ./internal/loki/, target: FuzzEnforceNamespaces }
          - { pkg: ./internal/k8s/, target: FuzzCheckIPNotPrivate }
          - { pkg: ./internal/k8s/, target: FuzzValidateRemoteURLContext }
          - { pkg: ./internal/gitops/, target: FuzzParseCompositeID }
          - { pkg: ./internal/servicemesh/, target: FuzzParseMeshCompositeID }
    name: ${{ matrix.target }}
    defaults:
      run:
        working-directory: backend
    steps:
      - uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3
      - uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6.4.0
        with:
          go-version: "1.26.3"
          cache-dependency-path: backend/go.sum
      - name: Verify ${{ matrix.target }} exists
        run: go test ${{ matrix.pkg }} -list '^${{ matrix.target }}$' | grep -qx '${{ matrix.target }}'
      - name: Fuzz ${{ matrix.target }}
        run: go test ${{ matrix.pkg }} -run=^$ -fuzz=^${{ matrix.target }}$ -fuzztime=5m
      - name: Upload crash reproducers on failure
        if: failure()
        uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: fuzz-crash-${{ matrix.target }}
          path: backend/internal/**/testdata/fuzz/${{ matrix.target }}/**
          if-no-files-found: ignore
          retention-days: 90
```

> Action-pin note (CLAUDE.md 7-day cooldown): all actions are SHA-pinned with a version comment, matching the convention in `.github/workflows/ci.yml` / `e2e.yml` (reuse their already-vetted SHAs rather than resolving fresh ones). `go-version` is hard-pinned to match ci.yml rather than `go-version-file`, so the fuzz toolchain tracks the gate. The `Verify … exists` step fails fast if a target is renamed without updating the matrix (`go test -fuzz` exits 0 on a no-match regex).

- [ ] **Step 2: Validate the workflow YAML locally**

Run: `cd "C:/Users/whstu/Documents/code-projects/k8sCenter" && python -c "import yaml,sys; yaml.safe_load(open('.github/workflows/fuzz.yml')); print('yaml ok')"`
Expected: `yaml ok`. (If `actionlint` is installed, also run `actionlint .github/workflows/fuzz.yml`.)

- [ ] **Step 3: Confirm the matrix targets match real test names**

Run: `cd backend && go test ./internal/loki/ ./internal/k8s/ ./internal/gitops/ ./internal/servicemesh/ -run='Fuzz' -v`
Expected: each of the five `Fuzz*` functions runs its seed corpus and PASSes — proving the names in the matrix resolve.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/fuzz.yml
git commit -m "ci: nightly deep-fuzz workflow for backend security targets"
```

---

### Task 6: Repo-wide verification gate (CLAUDE.md Rule 4)

Before declaring PR #1 done, run the canonical repo-wide checks. This is the forced-verification step — scoped per-package runs in earlier tasks miss sibling regressions.

**Files:** none (verification only).

- [ ] **Step 1: Run go vet repo-wide**

Run: `cd backend && go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 2: Run the full test suite repo-wide**

Run: `cd backend && go test ./...`
Expected: all packages `ok` (the new fuzz seed corpora run as normal tests here). Fix any failure before proceeding — do not push red.

- [ ] **Step 3: Confirm no stray long-running fuzz state was committed**

Run: `cd "C:/Users/whstu/Documents/code-projects/k8sCenter" && git status --porcelain`
Expected: clean working tree (any `testdata/fuzz/` crash files generated during local `-fuzztime` runs should be reviewed: commit them only if they represent a genuine bug-fix regression seed; otherwise discard with `git clean -fd backend/internal/*/testdata/fuzz` after inspection).

- [ ] **Step 4: Push and open PR**

```bash
git push -u origin HEAD
gh pr create --fill --base main
```
Then watch CI per CLAUDE.md: `gh run list --limit 1` / `gh run view`. Fix any failure before merge. Run `/ce:review` before requesting merge.

---

## Self-Review

**Spec coverage (against `2026-06-25-backend-fuzzing-design.md`):**
- Layer A security-oracle targets — LogQL (Task 1), SSRF (Task 2), composite IDs (Tasks 3–4). ✅ The "PR #1" set in the spec's PR sequencing is fully covered.
- Oracle A (crash-safety) — every target's `f.Fuzz` body cannot panic without failing. ✅
- Oracle B (parser invariants) — round-trip in Tasks 3–4; output re-parse invariant in Task 1. ✅
- Oracles C and D (auth/authz, secret-leak) — Layer B only; **out of scope for PR #1**, deferred to PR #2 plan. ✅ (intentional)
- CI: gate (existing `go test ./...` runs seeds — Task 6) + scheduled deep fuzz (Task 5). ✅
- "Oracle has teeth" — Task 1 seeds an injected disallowed namespace; Task 2 seeds `169.254.169.254`; both fail if the guard is weakened. ✅

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every run step shows an exact command and expected result. ✅

**Type consistency:** `EnforceNamespaces`, `parseMatchers`, `matcher{label,op,value}`, `findClosingBrace`, `maxQueryLen` (Task 1) match `security.go`. `checkIPNotPrivate(net.IP) error` and `ValidateRemoteURLContext(context.Context, string) error` (Task 2) match `cluster_router.go`. `parseCompositeID` 3-return and `parseMeshCompositeID` 5-return (Tasks 3–4) match the handlers. Matrix target names in Task 5 match the function names defined in Tasks 1–4. ✅

## Out of scope (own plans, after PR #1 merges)
- **PR #2:** Layer B `internal/server/fuzztest` harness (fake `ClientFactory`, token minting, request builders) + auth/login, settings-SSRF, and a Secret-returning GET handler — delivering oracles C and D.
- **PR #3:** Layer A breadth — `ParseMultiDoc`, `parsePHC`, remaining URL validators, and the `unstructured` normalizers (servicemesh/gateway/gitops/policy/velero).
