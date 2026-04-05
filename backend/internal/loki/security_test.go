package loki

import (
	"strings"
	"testing"
)

func TestEnforceNamespaces_BasicInjection(t *testing.T) {
	result, err := EnforceNamespaces(`{app="nginx"}`, []string{"default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="default",app="nginx"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_MultiNamespace(t *testing.T) {
	result, err := EnforceNamespaces(`{app="nginx"}`, []string{"default", "kube-system"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace=~"default|kube-system",app="nginx"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_ReplaceExisting(t *testing.T) {
	result, err := EnforceNamespaces(`{namespace="evil",app="x"}`, []string{"allowed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="allowed",app="x"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_AdminBypass(t *testing.T) {
	original := `{namespace="anything"}`
	result, err := EnforceNamespaces(original, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != original {
		t.Errorf("admin bypass should return original, got %q, want %q", result, original)
	}
}

func TestEnforceNamespaces_EmptySelector(t *testing.T) {
	result, err := EnforceNamespaces(`{}`, []string{"ns1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="ns1"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_PipelinePreserved(t *testing.T) {
	result, err := EnforceNamespaces(`{app="x"} |= "error" | json`, []string{"ns1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="ns1",app="x"} |= "error" | json`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_RejectNoBraces(t *testing.T) {
	_, err := EnforceNamespaces(`app="x"`, []string{"ns1"})
	if err == nil {
		t.Fatal("expected error for query without stream selector")
	}
	if !strings.Contains(err.Error(), "stream selector") {
		t.Errorf("error should mention stream selector, got: %v", err)
	}
}

func TestEnforceNamespaces_RejectTooLong(t *testing.T) {
	longQuery := "{" + strings.Repeat("a", 5000) + "}"
	_, err := EnforceNamespaces(longQuery, []string{"ns1"})
	if err == nil {
		t.Fatal("expected error for oversized query")
	}
	if !strings.Contains(err.Error(), "maximum length") {
		t.Errorf("error should mention maximum length, got: %v", err)
	}
}

func TestEnforceNamespaces_RejectEmptyNamespaces(t *testing.T) {
	_, err := EnforceNamespaces(`{app="x"}`, []string{})
	if err == nil {
		t.Fatal("expected error for empty namespaces list")
	}
	if !strings.Contains(err.Error(), "no namespaces") {
		t.Errorf("error should mention no namespaces, got: %v", err)
	}
}

func TestEnforceNamespaces_EscapedQuotes(t *testing.T) {
	result, err := EnforceNamespaces(`{app="my\"app"}`, []string{"ns1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="ns1",app="my\"app"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_MultipleNamespaceMatchersStripped(t *testing.T) {
	result, err := EnforceNamespaces(`{namespace="a",namespace=~"b|c",app="x"}`, []string{"allowed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="allowed",app="x"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_WhitespaceBetweenMatchers(t *testing.T) {
	result, err := EnforceNamespaces(`{ app = "nginx" , container = "web" }`, []string{"ns1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="ns1",app="nginx",container="web"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_LeadingWhitespace(t *testing.T) {
	result, err := EnforceNamespaces(`  {app="x"}`, []string{"ns1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="ns1",app="x"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_RegexMetaInNamespace(t *testing.T) {
	// Namespace names with regex metacharacters should be escaped.
	result, err := EnforceNamespaces(`{app="x"}`, []string{"ns.prod", "ns+staging"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace=~"ns\.prod|ns\+staging",app="x"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_UnclosedSelector(t *testing.T) {
	_, err := EnforceNamespaces(`{app="x"`, []string{"ns1"})
	if err == nil {
		t.Fatal("expected error for unclosed selector")
	}
	if !strings.Contains(err.Error(), "unclosed") {
		t.Errorf("error should mention unclosed, got: %v", err)
	}
}

func TestEnforceNamespaces_EmptyQuery(t *testing.T) {
	_, err := EnforceNamespaces(``, []string{"ns1"})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestEnforceNamespaces_NotEqualOperator(t *testing.T) {
	result, err := EnforceNamespaces(`{app!="nginx"}`, []string{"ns1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="ns1",app!="nginx"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_RegexMatchOperator(t *testing.T) {
	result, err := EnforceNamespaces(`{app=~"nginx|apache"}`, []string{"ns1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := `{namespace="ns1",app=~"nginx|apache"}`
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEnforceNamespaces_AdminBypassLongQuery(t *testing.T) {
	// Admin bypass should still enforce the query length limit.
	longQuery := "{" + strings.Repeat("a", 5000) + "}"
	_, err := EnforceNamespaces(longQuery, nil)
	if err == nil {
		t.Fatal("expected error for oversized query even with admin bypass")
	}
}
