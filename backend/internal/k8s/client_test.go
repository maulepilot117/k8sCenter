package k8s

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/fake"
)

// TestNewFakeClientFactory verifies the test-injection seam: ClientForUser must
// return the exact injected kubernetes.Interface (bypassing impersonation), and
// baseClientset is intentionally nil. The nil-baseClientset contract is asserted
// here so a future caller that reaches for BaseClientset() / RESTMapper() on a
// fake-injected factory finds the constraint documented by a failing test rather
// than a runtime nil dereference.
func TestNewFakeClientFactory(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	f := NewFakeClientFactory(fakeCS)

	got, err := f.ClientForUser("alice", []string{"team"})
	if err != nil {
		t.Fatalf("ClientForUser returned error: %v", err)
	}
	if got != fakeCS {
		t.Errorf("ClientForUser returned %v, want the injected fake %v", got, fakeCS)
	}

	if f.baseClientset != nil {
		t.Error("NewFakeClientFactory must leave baseClientset nil (Secret handlers use only the impersonated path)")
	}
}

// countingDiscoverySource is a hermetic discovery.DiscoveryInterface that
// counts ServerGroups calls. It stands in for the delegate that
// DiscoveryClient wraps with memory.NewMemCacheClient on each rebuild — the
// layer memory.NewMemCacheClient itself calls into on a cache miss — so
// tests can tell whether DiscoveryClient reused its cached client or
// rebuilt (and therefore re-fetched) one.
//
// ServerGroups, not ServerGroupsAndResources, is the actual seam:
// memCacheClient.ServerGroupsAndResources is implemented generically (see
// discovery.ServerGroupsAndResourcesWithContext) by calling the delegate's
// ServerGroups and then ServerResourcesForGroupVersion per group — it never
// calls a delegate's ServerGroupsAndResources directly. The embedded nil
// discovery.DiscoveryInterface makes any other unimplemented method panic
// loudly rather than silently returning a zero value.
type countingDiscoverySource struct {
	discovery.DiscoveryInterface

	mu    sync.Mutex
	calls int
}

func (c *countingDiscoverySource) ServerGroups() (*metav1.APIGroupList, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &metav1.APIGroupList{
		Groups: []metav1.APIGroup{{
			Name:             "",
			Versions:         []metav1.GroupVersionForDiscovery{{GroupVersion: "v1", Version: "v1"}},
			PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "v1", Version: "v1"},
		}},
	}, nil
}

func (c *countingDiscoverySource) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	return &metav1.APIResourceList{
		GroupVersion: groupVersion,
		APIResources: []metav1.APIResource{{
			Name: "pods", SingularName: "pod", Namespaced: true, Kind: "Pod",
		}},
	}, nil
}

func (c *countingDiscoverySource) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestClientFactory_DiscoveryClient_CachesWithinTTL is finding #8 (P2): the
// remote branch (cluster_router.go TargetSchemaFor) caches its discovery
// client for clientCacheTTL, but DiscoveryClient() used to return
// f.baseClientset.Discovery() raw on every call, so the polled
// /api/v1/capabilities/{clusterID} endpoint and yaml.resolveGVR walked the
// full API surface on every single call. This proves repeated
// DiscoveryClient() calls within the TTL reuse the same cached client and
// do not re-fetch.
func TestClientFactory_DiscoveryClient_CachesWithinTTL(t *testing.T) {
	source := &countingDiscoverySource{}
	f := &ClientFactory{
		clusterID:       "test",
		logger:          slog.Default(),
		discoverySource: source,
	}

	first := f.DiscoveryClient()
	if _, _, err := first.ServerGroupsAndResources(); err != nil {
		t.Fatalf("ServerGroupsAndResources returned error: %v", err)
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("underlying discovery source called %d times after the first fetch; want 1", got)
	}

	second := f.DiscoveryClient()
	if second != first {
		t.Error("DiscoveryClient() returned a different instance within the TTL; want the cached client reused")
	}
	if _, _, err := second.ServerGroupsAndResources(); err != nil {
		t.Fatalf("ServerGroupsAndResources returned error: %v", err)
	}
	if got := source.callCount(); got != 1 {
		t.Errorf("underlying discovery source called %d times after a warm hit; want 1 (DiscoveryClient must not rebuild within the TTL, and the reused memcache client must serve its own warm cache)", got)
	}
}

// TestClientFactory_DiscoveryClient_RefreshesAfterTTL proves the other half
// of finding #8: staleness must be bounded, not eliminated by copying
// RESTMapper's mapperOnce (an indefinite memoization RESTMapper only gets
// away with because DeferredDiscoveryRESTMapper resets itself on a lookup
// miss — a bare discovery client has no such escape hatch, and permanently
// caching it would hide newly-installed CRDs from yaml.resolveGVR forever).
// The cache entry's expiresAt is rewritten into the past instead of
// sleeping clientCacheTTL (5m), mirroring the technique already used for
// the remote branch's TTL'd schema cache in discovery_cache_test.go.
func TestClientFactory_DiscoveryClient_RefreshesAfterTTL(t *testing.T) {
	source := &countingDiscoverySource{}
	f := &ClientFactory{
		clusterID:       "test",
		logger:          slog.Default(),
		discoverySource: source,
	}

	first := f.DiscoveryClient()
	if _, _, err := first.ServerGroupsAndResources(); err != nil {
		t.Fatalf("ServerGroupsAndResources returned error: %v", err)
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("underlying discovery source called %d times after the first fetch; want 1", got)
	}

	entry := f.discoveryCache.Load()
	if entry == nil {
		t.Fatal("discoveryCache is nil after DiscoveryClient(); want a populated entry")
	}
	f.discoveryCache.Store(&cachedDiscovery{
		client:    entry.client,
		expiresAt: time.Now().Add(-time.Second),
	})

	second := f.DiscoveryClient()
	if second == first {
		t.Error("DiscoveryClient() returned the same instance after TTL expiry; want a rebuilt client")
	}
	if _, _, err := second.ServerGroupsAndResources(); err != nil {
		t.Fatalf("ServerGroupsAndResources returned error: %v", err)
	}
	if got := source.callCount(); got != 2 {
		t.Errorf("underlying discovery source called %d times after the TTL-expired rebuild; want 2 (the rebuilt memcache client starts cold and must re-fetch)", got)
	}
}

// TestClientFactory_DiscoveryClient_ConcurrentAccess exercises the
// concurrency-safety requirement from finding #8: the cache slot is an
// atomic.Pointer, so concurrent callers around the TTL boundary must never
// race. Run with -race (as the repo's canonical `go test ./...` does) to
// get the actual teeth from this test.
func TestClientFactory_DiscoveryClient_ConcurrentAccess(t *testing.T) {
	source := &countingDiscoverySource{}
	f := &ClientFactory{
		clusterID:       "test",
		logger:          slog.Default(),
		discoverySource: source,
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c := f.DiscoveryClient(); c == nil {
				t.Error("DiscoveryClient() returned nil")
			}
		}()
	}
	wg.Wait()
}
