package yaml

import (
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

// fuzzDiscoveryLists is the fixed API surface FuzzResolveGVR resolves
// against: a core group (including a subresource) and a CRD group.
var fuzzDiscoveryLists = []*metav1.APIResourceList{
	{GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "configmaps", Kind: "ConfigMap", Namespaced: true},
		{Name: "secrets", Kind: "Secret", Namespaced: true},
		{Name: "pods/log", Kind: "Pod", Namespaced: true},
	}},
	{GroupVersion: "example.com/v1", APIResources: []metav1.APIResource{
		{Name: "widgets", Kind: "Widget", Namespaced: true},
	}},
}

// FuzzResolveGVR drives resolveGVR with the caller-controlled {kind} URL
// segment of the export route. Oracles:
//
//   - A (crash safety): never panics.
//   - B (invariants): a success names a resource the fixture actually serves
//     and matches the input case-insensitively;
//     a failure on this complete discovery is always errKindNotServed, so the
//     handler reports 400 and never mistakes a bad kind for a cluster fault.
func FuzzResolveGVR(f *testing.F) {
	// Realistic kinds.
	f.Add("configmaps")
	f.Add("widgets")
	f.Add("secrets")
	// Teeth: case folding must still resolve; a singular or Kind name must not.
	f.Add("WIDGETS")
	f.Add("widget")
	f.Add("Widget")
	// Subresource names are served names too; they must round-trip exactly.
	f.Add("pods/log")
	// Adversarial.
	f.Add("")
	f.Add("\x00widgets")
	f.Add("widgets\n")
	f.Add("ſecrets") // U+017F folds to 's' under strings.EqualFold
	f.Add(strings.Repeat("w", 256))

	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: fuzzDiscoveryLists}}

	f.Fuzz(func(t *testing.T, kind string) {
		gvr, err := resolveGVR(disc, kind)
		if err != nil {
			if !errors.Is(err, errKindNotServed) {
				t.Fatalf("resolveGVR(%q) = %v; complete discovery must only ever miss with errKindNotServed", kind, err)
			}
			return
		}

		if !strings.EqualFold(gvr.Resource, kind) {
			t.Fatalf("resolveGVR(%q) = %v; resource does not match the requested kind", kind, gvr)
		}
		gv := gvr.GroupVersion().String()
		for _, list := range fuzzDiscoveryLists {
			if list.GroupVersion != gv {
				continue
			}
			for _, r := range list.APIResources {
				if r.Name == gvr.Resource {
					return
				}
			}
		}
		t.Fatalf("resolveGVR(%q) = %v; not a resource the fixture serves", kind, gvr)
	})
}
