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
