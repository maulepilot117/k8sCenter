package k8s

import (
	"sync"
	"testing"
	"time"
)

// fakeSchemaEntry builds a minimal, distinguishable *schemaCacheEntry for
// cache-only tests. The discovery/mapper fields are left nil — these tests
// exercise cache bookkeeping only, never touch the actual clients.
func fakeSchemaEntry(expiresAt time.Time) *schemaCacheEntry {
	return &schemaCacheEntry{expiresAt: expiresAt}
}

func TestSchemaCache_TTLExpiry(t *testing.T) {
	c := newTargetSchemaCache()
	now := time.Now()
	key := schemaCacheKey{clusterID: "remote-1", generation: "gen-1", identity: "alice"}

	c.put(key, fakeSchemaEntry(now.Add(-1*time.Minute))) // already expired relative to "now" below

	if _, ok := c.get(key, now); ok {
		t.Fatal("get() returned an entry past its TTL; want a miss")
	}
}

func TestSchemaCache_BoundedSize(t *testing.T) {
	c := newTargetSchemaCache()
	now := time.Now()
	future := now.Add(clientCacheTTL)

	firstKey := schemaCacheKey{clusterID: "remote-0", generation: "gen", identity: "id"}
	for i := 0; i < maxSchemaCacheEntries+1; i++ {
		key := schemaCacheKey{clusterID: keyForIndex(i), generation: "gen", identity: "id"}
		c.put(key, fakeSchemaEntry(future))
	}

	if got := c.len(); got != maxSchemaCacheEntries {
		t.Fatalf("len() = %d; want %d", got, maxSchemaCacheEntries)
	}
	if _, ok := c.get(firstKey, now); ok {
		t.Fatal("get() found the first-inserted key after exceeding the cap; want it evicted (FIFO)")
	}
}

// keyForIndex produces a distinct clusterID string per index without pulling
// in strconv just for a test helper.
func keyForIndex(i int) string {
	digits := "0123456789"
	if i == 0 {
		return "remote-0"
	}
	buf := make([]byte, 0, 8)
	for i > 0 {
		buf = append([]byte{digits[i%10]}, buf...)
		i /= 10
	}
	return "remote-" + string(buf)
}

func TestSchemaCache_EvictClusterDropsOnlyThatCluster(t *testing.T) {
	c := newTargetSchemaCache()
	future := time.Now().Add(clientCacheTTL)

	keyA := schemaCacheKey{clusterID: "cluster-a", generation: "gen", identity: "id"}
	keyB := schemaCacheKey{clusterID: "cluster-b", generation: "gen", identity: "id"}
	c.put(keyA, fakeSchemaEntry(future))
	c.put(keyB, fakeSchemaEntry(future))

	c.evictCluster("cluster-a")

	if _, ok := c.get(keyA, time.Now()); ok {
		t.Error("evictCluster(\"cluster-a\") left an entry for cluster-a")
	}
	if _, ok := c.get(keyB, time.Now()); !ok {
		t.Error("evictCluster(\"cluster-a\") also dropped cluster-b's entry")
	}
	if got := c.len(); got != 1 {
		t.Errorf("len() = %d after evicting one of two clusters; want 1", got)
	}
}

func TestSchemaCache_GenerationChangeIsACacheMiss(t *testing.T) {
	c := newTargetSchemaCache()
	future := time.Now().Add(clientCacheTTL)

	oldKey := schemaCacheKey{clusterID: "remote-1", generation: "gen-1", identity: "id"}
	newKey := schemaCacheKey{clusterID: "remote-1", generation: "gen-2", identity: "id"}

	c.put(oldKey, fakeSchemaEntry(future))

	if _, ok := c.get(newKey, time.Now()); ok {
		t.Fatal("get() with a different generation hit the old entry; want a miss")
	}
	if got := c.len(); got != 1 {
		t.Fatalf("len() = %d; want 1 (generation change must not have inserted anything via get)", got)
	}

	c.put(newKey, fakeSchemaEntry(future))
	if got := c.len(); got != 2 {
		t.Fatalf("len() = %d after inserting the new generation; want 2 (old + new coexist as separate entries)", got)
	}
	if _, ok := c.get(oldKey, time.Now()); !ok {
		t.Error("old generation's entry disappeared after inserting the new generation; want it to survive independently")
	}
}

func TestSchemaCache_IdentityIsolation(t *testing.T) {
	c := newTargetSchemaCache()
	future := time.Now().Add(clientCacheTTL)

	keyAlice := schemaCacheKey{clusterID: "remote-1", generation: "gen-1", identity: "alice-hash"}
	keyBob := schemaCacheKey{clusterID: "remote-1", generation: "gen-1", identity: "bob-hash"}

	entryAlice := fakeSchemaEntry(future)
	c.put(keyAlice, entryAlice)

	if _, ok := c.get(keyBob, time.Now()); ok {
		t.Fatal("get() with identity B returned an entry populated by identity A")
	}
	if got := c.len(); got != 1 {
		t.Fatalf("len() = %d; want 1 (identity B lookup must not create an entry)", got)
	}

	c.put(keyBob, fakeSchemaEntry(future))
	if got := c.len(); got != 2 {
		t.Fatalf("len() = %d after two identities on the same cluster/generation; want 2", got)
	}
	got, ok := c.get(keyAlice, time.Now())
	if !ok || got != entryAlice {
		t.Error("identity A's own entry was not returned unchanged after identity B was added")
	}
}

func TestSchemaCache_SweepExpiredIsIdempotent(t *testing.T) {
	c := newTargetSchemaCache()
	now := time.Now()

	liveKey := schemaCacheKey{clusterID: "remote-live", generation: "gen", identity: "id"}
	deadKey := schemaCacheKey{clusterID: "remote-dead", generation: "gen", identity: "id"}
	c.put(liveKey, fakeSchemaEntry(now.Add(clientCacheTTL)))
	c.put(deadKey, fakeSchemaEntry(now.Add(-time.Second)))

	sweepAt := now.Add(time.Millisecond)

	for i := 0; i < 3; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("sweepExpired panicked on call %d: %v", i, r)
				}
			}()
			c.sweepExpired(sweepAt)
		}()
	}

	if _, ok := c.get(liveKey, sweepAt); !ok {
		t.Error("sweepExpired dropped a live entry")
	}
	if _, ok := c.get(deadKey, sweepAt); ok {
		t.Error("sweepExpired left an expired entry behind")
	}
	if got := c.len(); got != 1 {
		t.Errorf("len() = %d after repeated sweeps; want 1 (only the live entry)", got)
	}
}

// TestSchemaCache_ConcurrentAccessIsRaceFree exercises get/put/evictCluster/
// sweepExpired from many goroutines at once. Not one of the six named tests,
// but cheap insurance that the cache's own locking (independent of
// ClusterRouter's -race test) holds up under `go test -race`.
func TestSchemaCache_ConcurrentAccessIsRaceFree(t *testing.T) {
	c := newTargetSchemaCache()
	future := time.Now().Add(clientCacheTTL)
	key := schemaCacheKey{clusterID: "remote-1", generation: "gen", identity: "id"}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.put(key, fakeSchemaEntry(future))
			c.get(key, time.Now())
			c.sweepExpired(time.Now())
			c.evictCluster("some-other-cluster")
			c.len()
		}()
	}
	wg.Wait()
}
