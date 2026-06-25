package resources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const sentinelValue = "super-sentinel-secret"

// FuzzMaskedSecret tests the maskedSecret security invariants against arbitrary
// key/value inputs:
//  1. Every Data value in the returned secret equals []byte("****").
//  2. Every StringData value equals "****".
//  3. The sentinel value never survives masking (regression guard).
//  4. No panic on nil/empty maps, binary data, huge values, many keys.
//  5. Key sets are preserved (no keys dropped or added).
//  6. The last-applied-configuration annotation is stripped from ObjectMeta.
//  7. The original Secret is not mutated (maskedSecret must operate on a
//     DeepCopy; the caller's Data/StringData maps are unchanged after the call).
func FuzzMaskedSecret(f *testing.F) {
	// Seed corpus: cover normal, empty, sentinel, binary, and long values.
	// Seeds: normal key/value, empty, binary, long, sentinel attempt (via fuzz key).
	f.Add("mykey", []byte("myvalue"), "strKey", "strVal")
	f.Add("", []byte(""), "", "")
	f.Add("key", []byte{0x00, 0xFF, 0x1B, 0x0A}, "k2", "plaintext")
	f.Add("longkey", []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "lk2", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	f.Add("k", []byte("v"), "sk", "sv")
	f.Add("user", []byte("admin"), "password", "s3cret!")
	// Attempt to slip in the sentinel value via the fuzz key — masking must still erase it.
	f.Add("extraKey", []byte(sentinelValue), "tok2", sentinelValue)

	f.Fuzz(func(t *testing.T, key string, value []byte, key2 string, strVal string) {
		// Build the input secret.  Vary empty/nil map exercise via empty keys.
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
				Annotations: map[string]string{
					lastAppliedConfigAnnotation: `{"apiVersion":"v1","kind":"Secret","stringData":{"password":"` + sentinelValue + `"}}`,
					"other-annotation":          "value",
				},
			},
		}

		// Build Data map.  Use a reserved sentinel key that cannot be reached by
		// the fuzz key (the fuzz key is the variable "key"; we use a different
		// constant so the fuzzer cannot collide with the sentinel entry).
		// Only add the fuzz key when non-empty (exercises single-entry and
		// nil-map paths).
		const sentinelKey = "__sentinel__"
		s.Data = map[string][]byte{
			sentinelKey: []byte(sentinelValue),
		}
		if key != "" && key != sentinelKey {
			s.Data[key] = value
		}

		// Build StringData map — only add when non-empty.
		if key2 != "" {
			s.StringData = map[string]string{
				key2: strVal,
			}
		}

		// Snapshot input key sets before calling maskedSecret (DeepCopy mutates).
		dataKeysBefore := make(map[string]struct{}, len(s.Data))
		for k := range s.Data {
			dataKeysBefore[k] = struct{}{}
		}
		strDataKeysBefore := make(map[string]struct{}, len(s.StringData))
		for k := range s.StringData {
			strDataKeysBefore[k] = struct{}{}
		}

		// Call the function under test — must not panic.
		masked := maskedSecret(s)

		// Oracle 1 + 3: every Data value must be exactly []byte("****") and
		// the sentinel must never survive.
		if masked.Data != nil {
			for k, v := range masked.Data {
				if string(v) != "****" {
					t.Errorf("Data[%q] = %q; want %q", k, v, "****")
				}
				// Explicit teeth: the sentinel key's original value must be gone.
				if k == sentinelKey && string(v) == sentinelValue {
					t.Errorf("sentinel survived masking in Data[%q]", k)
				}
			}
		}

		// Oracle 2 + 3: every StringData value must be exactly "****" and
		// the sentinel must never survive.
		if masked.StringData != nil {
			for k, v := range masked.StringData {
				if v != "****" {
					t.Errorf("StringData[%q] = %q; want %q", k, v, "****")
				}
				if v == sentinelValue {
					t.Errorf("sentinel survived masking in StringData[%q]", k)
				}
			}
		}

		// Oracle 4 (key-set preservation): Data keys must not be dropped or added.
		if len(masked.Data) != len(dataKeysBefore) {
			t.Errorf("Data key count changed: got %d, want %d", len(masked.Data), len(dataKeysBefore))
		}
		for k := range dataKeysBefore {
			if _, ok := masked.Data[k]; !ok {
				t.Errorf("Data key %q was dropped by masking", k)
			}
		}
		for k := range masked.Data {
			if _, ok := dataKeysBefore[k]; !ok {
				t.Errorf("Data key %q was added by masking", k)
			}
		}

		// Oracle 4 (key-set preservation): StringData keys must not be dropped or added.
		if len(masked.StringData) != len(strDataKeysBefore) {
			t.Errorf("StringData key count changed: got %d, want %d", len(masked.StringData), len(strDataKeysBefore))
		}
		for k := range strDataKeysBefore {
			if _, ok := masked.StringData[k]; !ok {
				t.Errorf("StringData key %q was dropped by masking", k)
			}
		}
		for k := range masked.StringData {
			if _, ok := strDataKeysBefore[k]; !ok {
				t.Errorf("StringData key %q was added by masking", k)
			}
		}

		// Oracle 5: last-applied-configuration annotation must be stripped.
		if masked.Annotations != nil {
			if _, present := masked.Annotations[lastAppliedConfigAnnotation]; present {
				t.Error("last-applied-configuration annotation was not stripped by masking")
			}
		}

		// Oracle 6: original secret must not be mutated (maskedSecret should
		// operate on a deep copy).  Check the sentinel key specifically since we
		// control its exact original value.
		if s.Data != nil {
			if v, ok := s.Data[sentinelKey]; ok && string(v) != sentinelValue {
				t.Errorf("maskedSecret mutated the original Data[%q]: got %q", sentinelKey, v)
			}
		}

		// Nil-map nil-guard: exercise the branch in maskedSecret where both
		// Data and StringData are nil (no map allocation).  Verifies no panic
		// and that a non-nil *corev1.Secret is returned.
		nilSecret := &corev1.Secret{}
		maskedNil := maskedSecret(nilSecret)
		if maskedNil == nil {
			t.Error("maskedSecret returned nil for a Secret with nil Data and nil StringData")
		}
	})
}
