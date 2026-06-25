package servicemesh

import (
	"strings"
	"testing"
)

// FuzzParseMeshCompositeID fuzzes the "mesh:namespace:kind:name" parser.
// Crash-safety on arbitrary input, plus a round-trip oracle: any four
// non-empty parts that contain no ':' or '%' must parse back to themselves.
func FuzzParseMeshCompositeID(f *testing.F) {
	f.Add("istio", "default", "vs", "reviews")          // valid Istio VirtualService
	f.Add("linkerd", "emojivoto", "sp", "web")           // valid Linkerd ServiceProfile
	f.Add("", "", "", "")                                // all-empty parts — expects error
	f.Add("istio", "default", "vs", "a:b")              // last part contains colon — SplitN(.,4) keeps it
	f.Add("istio%3A", "default", "vs", "reviews")       // URL-encoded colon in first part

	f.Fuzz(func(t *testing.T, a, b, c, d string) {
		// Crash-safety on the raw concatenation (covers odd unescape input).
		_, _, _, _, _ = parseMeshCompositeID(a + ":" + b + ":" + c + ":" + d)

		// Round-trip oracle: only when parts are well-formed (non-empty,
		// colon-free, not percent-decodable into a colon). Skip parts that
		// PathUnescape would alter, since that path intentionally rewrites.
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
