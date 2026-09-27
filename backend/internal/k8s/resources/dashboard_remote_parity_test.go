package resources

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRemoteDashboardReasonCodeParity pins the reason codes mirrored in
// dashboard_remote.go to their canonical server.ReasonCode values.
//
// This package cannot import internal/server (server imports resources), so
// the test reads handle_capabilities.go with the Go parser instead, the same
// source-reading approach server's TestCapabilityContractParity uses against
// the TypeScript side. A rename or value change of any mirrored code on the
// server side fails here, naming the constant that drifted.
func TestRemoteDashboardReasonCodeParity(t *testing.T) {
	canonical := serverReasonCodes(t)

	mirrored := map[string]string{
		"ReasonOK":                  reasonOK,
		"ReasonUnsupportedPlatform": reasonUnsupportedPlatform,
		"ReasonUnreachable":         reasonUnreachable,
		"ReasonForbidden":           reasonForbidden,
		"ReasonAuthzUnknown":        reasonAuthzUnknown,
	}
	for name, got := range mirrored {
		want, ok := canonical[name]
		if !ok {
			t.Errorf("server.%s no longer exists in handle_capabilities.go; update the mirror in dashboard_remote.go", name)
			continue
		}
		if got != want {
			t.Errorf("mirrored %s = %q, but server.%s = %q", name, got, name, want)
		}
	}
}

// serverReasonCodes parses internal/server/handle_capabilities.go and returns
// every constant declared with type ReasonCode, keyed by name. It fails the
// test rather than returning an empty map, so a moved or renamed declaration
// cannot turn the parity check into a vacuous pass.
func serverReasonCodes(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "server", "handle_capabilities.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	codes := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := vs.Type.(*ast.Ident); !ok || ident.Name != "ReasonCode" {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					break
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", name.Name, err)
				}
				codes[name.Name] = v
			}
		}
	}
	if len(codes) == 0 {
		t.Fatalf("found no ReasonCode constants in %s; parity check aborted rather than comparing against nothing", path)
	}
	return codes
}
