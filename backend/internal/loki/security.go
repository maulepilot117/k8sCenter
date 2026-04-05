package loki

import (
	"fmt"
	"strings"
)

const maxQueryLen = 4096

// matcher represents a parsed LogQL stream selector matcher.
type matcher struct {
	label string
	op    string
	value string // unquoted, with internal escapes preserved
}

// EnforceNamespaces rewrites a LogQL query to enforce namespace restrictions.
// It replaces any existing namespace matcher with the allowed set.
// Admin users (allowedNamespaces == nil) pass through unmodified.
// Queries without a stream selector (not starting with '{') are rejected.
// Cluster-scoped queries (no namespace label possible) require admin access.
func EnforceNamespaces(query string, allowedNamespaces []string) (string, error) {
	if len(query) > maxQueryLen {
		return "", fmt.Errorf("query exceeds maximum length of %d bytes", maxQueryLen)
	}

	// Admin bypass: nil means unrestricted.
	if allowedNamespaces == nil {
		return query, nil
	}

	if len(allowedNamespaces) == 0 {
		return "", fmt.Errorf("no namespaces allowed")
	}

	trimmed := strings.TrimSpace(query)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", fmt.Errorf("invalid LogQL: query must start with a stream selector '{'")
	}

	// Find the closing '}' of the stream selector, respecting quoted strings.
	closeIdx := findClosingBrace(trimmed)
	if closeIdx < 0 {
		return "", fmt.Errorf("invalid LogQL: unclosed stream selector")
	}

	selectorContent := trimmed[1:closeIdx]
	pipeline := trimmed[closeIdx+1:]

	matchers, err := parseMatchers(selectorContent)
	if err != nil {
		return "", fmt.Errorf("invalid LogQL selector: %w", err)
	}

	// Remove all existing namespace matchers.
	filtered := make([]matcher, 0, len(matchers))
	for _, m := range matchers {
		if m.label != "namespace" {
			filtered = append(filtered, m)
		}
	}

	// Build the enforced namespace matcher.
	var nsMatcher matcher
	if len(allowedNamespaces) == 1 {
		nsMatcher = matcher{
			label: "namespace",
			op:    "=",
			value: allowedNamespaces[0],
		}
	} else {
		escaped := make([]string, len(allowedNamespaces))
		for i, ns := range allowedNamespaces {
			escaped[i] = escapeRegexValue(ns)
		}
		nsMatcher = matcher{
			label: "namespace",
			op:    "=~",
			value: strings.Join(escaped, "|"),
		}
	}

	// Prepend namespace matcher.
	result := make([]matcher, 0, len(filtered)+1)
	result = append(result, nsMatcher)
	result = append(result, filtered...)

	// Reconstruct query.
	var sb strings.Builder
	sb.WriteByte('{')
	for i, m := range result {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(m.label)
		sb.WriteString(m.op)
		sb.WriteByte('"')
		sb.WriteString(m.value)
		sb.WriteByte('"')
	}
	sb.WriteByte('}')
	sb.WriteString(pipeline)

	return sb.String(), nil
}

// findClosingBrace finds the index of the '}' that closes the stream selector
// starting at position 0 (which must be '{'). Returns -1 if not found.
// Handles quoted strings with escaped quotes inside values.
func findClosingBrace(s string) int {
	inQuote := false
	escaped := false

	for i := 1; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inQuote {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && ch == '}' {
			return i
		}
	}
	return -1
}

// parseMatchers parses the content between { and } into matchers.
// Uses a character-by-character state machine. No regex.
func parseMatchers(content string) ([]matcher, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}

	var matchers []matcher
	i := 0
	n := len(content)

	for i < n {
		// Skip whitespace and commas between matchers.
		for i < n && (content[i] == ' ' || content[i] == '\t' || content[i] == ',') {
			i++
		}
		if i >= n {
			break
		}

		// Parse label name: [a-zA-Z_][a-zA-Z0-9_]*
		labelStart := i
		for i < n && isLabelChar(content[i]) {
			i++
		}
		if i == labelStart {
			return nil, fmt.Errorf("expected label name at position %d", i)
		}
		label := content[labelStart:i]

		// Skip whitespace before operator.
		for i < n && (content[i] == ' ' || content[i] == '\t') {
			i++
		}

		// Parse operator: =, !=, =~, !~
		if i >= n {
			return nil, fmt.Errorf("expected operator after label %q", label)
		}
		op, advance, err := parseOp(content[i:])
		if err != nil {
			return nil, fmt.Errorf("invalid operator after label %q: %w", label, err)
		}
		i += advance

		// Skip whitespace before value.
		for i < n && (content[i] == ' ' || content[i] == '\t') {
			i++
		}

		// Parse quoted value.
		if i >= n || content[i] != '"' {
			return nil, fmt.Errorf("expected quoted value after %s%s", label, op)
		}
		value, advance, err := parseQuotedValue(content[i:])
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: %w", label, err)
		}
		i += advance

		matchers = append(matchers, matcher{label: label, op: op, value: value})
	}

	return matchers, nil
}

// parseOp parses a LogQL matcher operator at the start of s.
// Returns the operator string, number of bytes consumed, and any error.
func parseOp(s string) (string, int, error) {
	if len(s) >= 2 {
		two := s[:2]
		if two == "!=" || two == "=~" || two == "!~" {
			return two, 2, nil
		}
	}
	if len(s) >= 1 && s[0] == '=' {
		return "=", 1, nil
	}
	return "", 0, fmt.Errorf("expected =, !=, =~, or !~")
}

// parseQuotedValue parses a double-quoted string at the start of s.
// Returns the unquoted content (with internal escapes preserved), bytes consumed, and any error.
func parseQuotedValue(s string) (string, int, error) {
	if len(s) == 0 || s[0] != '"' {
		return "", 0, fmt.Errorf("expected opening quote")
	}

	var sb strings.Builder
	escaped := false

	for i := 1; i < len(s); i++ {
		ch := s[i]
		if escaped {
			sb.WriteByte('\\')
			sb.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return sb.String(), i + 1, nil
		}
		sb.WriteByte(ch)
	}

	return "", 0, fmt.Errorf("unclosed quoted value")
}

// isLabelChar returns true if ch is valid in a LogQL label name.
func isLabelChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '_'
}

// escapeRegexValue escapes regex meta-characters in a namespace name
// for use in a =~ matcher value.
func escapeRegexValue(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if isRegexMeta(ch) {
			sb.WriteByte('\\')
		}
		sb.WriteByte(ch)
	}
	return sb.String()
}

// isRegexMeta returns true if ch is a regex metacharacter that needs escaping.
func isRegexMeta(ch byte) bool {
	switch ch {
	case '.', '+', '*', '?', '(', ')', '[', ']', '{', '}', '|', '^', '$', '\\':
		return true
	}
	return false
}
