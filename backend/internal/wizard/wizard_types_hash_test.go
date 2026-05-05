package wizard

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestWizardInputTypeShapeStability pins the exported-field signature of
// SecretStoreInput (and other Phase H wizard inputs as they land) so that any
// future addition, removal, rename, type change, or json-tag edit fails fast
// and forces the author to also update the corresponding TypeScript interface
// in frontend/islands/SecretStoreWizard.tsx and lib/eso-types.ts at the same
// time.
//
// When the hash fails: confirm the TS interface is updated first, then update
// the `want` constant here. A deliberate two-line change, not a casual fix.
//
// To obtain the new hash after a field change:
//
//	go test ./internal/wizard/ -run TestWizardInputTypeShapeStability -v
//
// and read "now=..." from the failure message.
func TestWizardInputTypeShapeStability(t *testing.T) {
	cases := []struct {
		typ  any
		name string
		want string
	}{
		{SecretStoreInput{}, "SecretStoreInput", "0916e14671ebe0b7ef3cea636d997fd206846d22ebc1d01fb64959bc2de5e276"},
	}

	for _, tc := range cases {
		got := shapeHashWizard(tc.typ)
		if got != tc.want {
			t.Errorf(
				"type %s shape hash drifted: now=%s, was=%s\n"+
					"— update frontend/islands/SecretStoreWizard.tsx (SecretStoreWizardForm interface) "+
					"and frontend/lib/eso-types.ts to match the new Go shape, "+
					"then update this test's `want` constant",
				tc.name, got, tc.want,
			)
		}
	}
}

// shapeHashWizard is the wizard-package copy of the shape hasher from
// internal/externalsecrets/types_hash_test.go. Kept local to avoid a test
// dependency between packages.
func shapeHashWizard(v any) string {
	t := reflect.TypeOf(v)
	if t.Kind() != reflect.Struct {
		return "non-struct"
	}
	parts := make([]string, 0, t.NumField())
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		// FieldName + Go type + json tag. Three independent dimensions a
		// frontend consumer cares about.
		parts = append(parts, f.Name+":"+f.Type.String()+":"+f.Tag.Get("json"))
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, ";")))
	return hex.EncodeToString(h[:])
}
