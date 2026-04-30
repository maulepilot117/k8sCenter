package externalsecrets

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestExportedTypeShapeStability pins a sha256 hash of the exported field
// names and types of every public type whose JSON shape the frontend
// depends on. Any future addition / removal / rename / type-change of a
// public field — or any change to its `json:` tag — will fail this test
// with a clear "now=<new>, was=<old>" diagnostic, forcing the author to
// update the corresponding TS interface in `frontend/lib/eso-types.ts`
// (Phase B) at the same time.
//
// This is the Go-side half of the Go-TS hash test specified in Plan Unit 7.
// When the frontend lands, the TS side will pin the same set of fields with
// a corresponding hash; the two together prevent silent Go-TS drift.
//
// Updating a hash should be a deliberate two-line change paired with the TS
// update — not a casual edit. If you're tempted to "just fix the test,"
// stop and confirm the frontend interface was updated first.
func TestExportedTypeShapeStability(t *testing.T) {
	cases := []struct {
		typ  any
		name string
		want string
	}{
		{ESOStatus{}, "ESOStatus", "7fb5df5cb8e9ada236f3ae25bed45554ac8e83e4d8e951a230941022be4c0ed5"},
		{StoreRef{}, "StoreRef", "15c933f766a870488523aa0d28de892afe8570ddf6327b016ca5da9eb421bb35"},
		{ExternalSecret{}, "ExternalSecret", "cf0615f46c9a4c444601000a14c8fa7a516ada471ee14ee5305e2b35806ff1e4"},
		{ClusterExternalSecret{}, "ClusterExternalSecret", "40ae7c0a5334702fc888b7a6e86f2018d6d670b8ab9b07a67004ca682e51caf5"},
		{SecretStore{}, "SecretStore", "a6aa435026eda8cf349a40867c54515ae641a671a1f3da063da58485919943cb"},
		{PushSecret{}, "PushSecret", "55992ade35cc28828ec5b7a0a58dddbb8a98c52ad3c174ed31cbda2c17b57abb"},
	}

	for _, tc := range cases {
		got := shapeHash(tc.typ)
		if got != tc.want {
			t.Errorf("type %s shape hash drifted: now=%s, was=%s — update frontend/lib/eso-types.ts to match the new Go shape, then update this test's `want` constant", tc.name, got, tc.want)
		}
	}
}

// shapeHash deterministically encodes the exported-field signature of a
// struct value into a sha256 hex digest. Field order is normalized via sort
// so reordering source-level field declarations doesn't trigger spurious
// drift; the JSON tag is included because that's the wire-shape contract,
// not the Go-level field name.
func shapeHash(v any) string {
	t := reflect.TypeOf(v)
	if t.Kind() != reflect.Struct {
		return "non-struct"
	}
	parts := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
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
