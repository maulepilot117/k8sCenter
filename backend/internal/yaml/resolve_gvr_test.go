package yaml

import (
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// stagedDiscoverySource is a hermetic discovery.DiscoveryInterface that
// reveals an additional resource ("gadgets") only from its second scan
// onward. It stands in for a CRD that gets installed in between an initial
// discovery snapshot and a follow-up apply — the regression window this test
// guards. The embedded nil discovery.DiscoveryInterface makes any
// unimplemented method panic loudly rather than silently returning a zero
// value, mirroring countingDiscoverySource in internal/k8s/client_test.go.
type stagedDiscoverySource struct {
	discovery.DiscoveryInterface

	mu    sync.Mutex
	scans int
}

func (s *stagedDiscoverySource) ServerGroups() (*metav1.APIGroupList, error) {
	s.mu.Lock()
	s.scans++
	s.mu.Unlock()
	return &metav1.APIGroupList{
		Groups: []metav1.APIGroup{{
			Name:             "example.com",
			Versions:         []metav1.GroupVersionForDiscovery{{GroupVersion: "example.com/v1", Version: "v1"}},
			PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "example.com/v1", Version: "v1"},
		}},
	}, nil
}

func (s *stagedDiscoverySource) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	s.mu.Lock()
	scan := s.scans
	s.mu.Unlock()

	resources := []metav1.APIResource{
		{Name: "widgets", SingularName: "widget", Namespaced: true, Kind: "Widget"},
	}
	if scan >= 2 {
		// "gadgets" is the CRD that lands after the first discovery snapshot.
		resources = append(resources, metav1.APIResource{
			Name: "gadgets", SingularName: "gadget", Namespaced: true, Kind: "Gadget",
		})
	}
	return &metav1.APIResourceList{GroupVersion: groupVersion, APIResources: resources}, nil
}

func (s *stagedDiscoverySource) scanCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scans
}

// TestResolveGVR_SelfHealsOnCRDInstalledAfterCache proves the regression fix:
// a kind absent from the first (cached) discovery scan but present on a
// freshly-fetched one is still found, because resolveGVR invalidates the
// TTL-cached discovery client and retries once on a miss — closing the
// window where a CRD installed moments ago would be invisible to YAML apply
// for up to clientCacheTTL.
func TestResolveGVR_SelfHealsOnCRDInstalledAfterCache(t *testing.T) {
	source := &stagedDiscoverySource{}
	factory := k8s.NewTestClientFactoryWithDiscoverySource(source)

	gvr, err := resolveGVR(factory, "gadgets")
	if err != nil {
		t.Fatalf("resolveGVR returned error: %v", err)
	}

	want := "example.com/v1/gadgets"
	got := gvr.Group + "/" + gvr.Version + "/" + gvr.Resource
	if got != want {
		t.Errorf("resolveGVR returned %q, want %q", got, want)
	}

	if got := source.scanCount(); got != 2 {
		t.Errorf("underlying discovery source scanned %d times; want exactly 2 (one initial miss, one self-heal retry)", got)
	}
}

// TestResolveGVR_GenuinelyAbsentKindStillNotFound proves the retry is bounded:
// a kind that never appears (in the first scan or the retried one) still
// returns the original not-found error, and the source is scanned at most
// twice — not in an unbounded loop.
func TestResolveGVR_GenuinelyAbsentKindStillNotFound(t *testing.T) {
	source := &stagedDiscoverySource{}
	factory := k8s.NewTestClientFactoryWithDiscoverySource(source)

	_, err := resolveGVR(factory, "sprockets")
	if err == nil {
		t.Fatal("resolveGVR returned nil error for a kind that never exists; want a not-found error")
	}
	if !strings.Contains(err.Error(), `resource "sprockets" not found in API server`) {
		t.Errorf("resolveGVR error = %q, want it to contain the not-found message", err.Error())
	}

	if got := source.scanCount(); got != 2 {
		t.Errorf("underlying discovery source scanned %d times; want exactly 2 (bounded retry, no unbounded loop)", got)
	}
}
