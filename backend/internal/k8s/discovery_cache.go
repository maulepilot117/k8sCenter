package k8s

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
)

// maxSchemaCacheEntries bounds targetSchemaCache so a burst of distinct
// (cluster, identity) pairs cannot grow the cache without limit. Insertion
// beyond the cap evicts the oldest key (FIFO, not LRU) — with a TTL of
// clientCacheTTL (5 minutes) the two strategies are practically equivalent,
// and FIFO keeps the invariant "no entry outlives its TTL" trivially
// provable. See D1 in the U7 design brief for the full sizing rationale.
const maxSchemaCacheEntries = 128

// targetSchemaCache is a bounded, TTL'd, identity-keyed cache of per-cluster
// discovery + RESTMapper pairs. Owned by ClusterRouter; never shared across
// ClusterRouter instances and never exported.
type targetSchemaCache struct {
	mu      sync.Mutex
	entries map[schemaCacheKey]*schemaCacheEntry
	order   []schemaCacheKey // insertion order for the bounded-size eviction
}

// schemaCacheKey identifies one cached discovery+mapper pair. The key is
// exactly (cluster, identity) — nothing else:
//   - clusterID scopes the entry to one registered cluster;
//   - identity isolates results by impersonated user, because a hardened
//     remote cluster may grant different identities different discoverable
//     API resources (see D1 "Identity-isolation rule"). No entry is ever
//     served to an identity other than the one that populated it.
//
// Staleness is NOT a key component. A cluster record that is deleted,
// re-registered, or has its credentials rotated is handled by
// ClusterRouter.EvictCluster, exactly as the sibling remoteCache /
// remoteDynCache client caches handle it. Folding the record's generation
// into the key would force a cluster-store read on every lookup — including
// every cache HIT — which defeats the point of the cache (PR #436 review
// finding #1). The record's vintage is carried on the ENTRY instead, purely
// as informational metadata.
type schemaCacheKey struct {
	clusterID string // normalized via NormalizedClusterID
	identity  string // cacheKey(username, groups) — sha256, already collision-resistant
}

// schemaCacheEntry is one cached discovery/mapper pair, the cluster-record
// vintage it was built from, and its expiry.
//
// generation is informational only: it records which ClusterRecord.CreatedAt
// this pair was built from and is surfaced as TargetSchema.Generation. Since
// it is not part of the key, a row replaced under the same cluster id without
// an EvictCluster call can leave generation lagging reality by up to one TTL
// (clientCacheTTL). Do not treat it as a freshness guarantee.
type schemaCacheEntry struct {
	discovery  discovery.CachedDiscoveryInterface // memory.NewMemCacheClient(...)
	mapper     meta.RESTMapper                    // restmapper.NewDeferredDiscoveryRESTMapper(discovery)
	generation string                             // ClusterRecord.CreatedAt (RFC3339Nano) at build time
	expiresAt  time.Time
}

// newTargetSchemaCache returns an empty, ready-to-use cache.
func newTargetSchemaCache() *targetSchemaCache {
	return &targetSchemaCache{
		entries: make(map[schemaCacheKey]*schemaCacheEntry),
	}
}

// get returns the entry for k, or (nil, false) when absent or expired as of
// now. An expired entry is left in place for sweepExpired to reap — get does
// not mutate the cache, so concurrent readers never contend on a write lock
// for a plain lookup.
func (c *targetSchemaCache) get(k schemaCacheKey, now time.Time) (*schemaCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[k]
	if !ok {
		return nil, false
	}
	if now.After(e.expiresAt) {
		return nil, false
	}
	return e, true
}

// put inserts or replaces the entry for k. Inserting a brand-new key beyond
// maxSchemaCacheEntries evicts the oldest key first (FIFO). Replacing an
// existing key's entry (e.g. a concurrent cold-cache race, or a refreshed
// entry with the same key) does not consume a new slot.
func (c *targetSchemaCache) put(k schemaCacheKey, e *schemaCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[k]; !exists {
		c.order = append(c.order, k)
	}
	c.entries[k] = e

	for len(c.order) > maxSchemaCacheEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// evictCluster drops every entry for clusterID, across all identities.
// Called from ClusterRouter.EvictCluster so cluster deletion or credential
// rotation drops schema alongside the client caches. This is the ONLY
// staleness mechanism for the schema cache besides the TTL — the key
// deliberately carries no generation component (see schemaCacheKey).
func (c *targetSchemaCache) evictCluster(clusterID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k := range c.entries {
		if k.clusterID == clusterID {
			delete(c.entries, k)
		}
	}
	c.order = compactOrder(c.order, c.entries)
}

// sweepExpired drops every entry whose expiresAt is at or before now. Called
// from the existing ClusterRouter cache-sweeper ticker loop — no new
// goroutine is created for this cache. Safe to call repeatedly (idempotent):
// a second sweep with no newly-expired entries is a no-op.
func (c *targetSchemaCache) sweepExpired(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}
	c.order = compactOrder(c.order, c.entries)
}

// len reports the current entry count. Test-only accessor.
func (c *targetSchemaCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// compactOrder rewrites order in place, keeping only keys still present in
// entries. Safe in-place filter: the write index never runs ahead of the
// read index, so overlapping the backing array is not a hazard. Callers must
// hold c.mu.
func compactOrder(order []schemaCacheKey, entries map[schemaCacheKey]*schemaCacheEntry) []schemaCacheKey {
	kept := order[:0]
	for _, k := range order {
		if _, ok := entries[k]; ok {
			kept = append(kept, k)
		}
	}
	return kept
}
