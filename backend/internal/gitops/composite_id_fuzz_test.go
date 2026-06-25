package gitops

import (
	"strings"
	"testing"
)

// FuzzParseCompositeID fuzzes the "tool:namespace:name" parser.
// Crash-safety on arbitrary input, plus a round-trip oracle: any three
// non-empty parts that contain no ':' must parse back to themselves.
func FuzzParseCompositeID(f *testing.F) {
	f.Add("argo", "default", "my-app")
	f.Add("flux", "kube-system", "podinfo")
	f.Add("a", "b", "c:d")    // third part contains colon — SplitN(.,3) keeps it
	f.Add("", "", "")          // all-empty parts — expects error
	f.Add("argo%3A", "x", "y") // URL-encoded colon in first part

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
