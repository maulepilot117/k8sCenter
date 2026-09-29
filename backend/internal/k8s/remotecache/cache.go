// Package remotecache is the shared read cache for data fetched from a
// registered remote cluster on behalf of one Kubernetes identity.
//
// Feature packages (gitops, velero, gateway, ...) keep their own
// service-account cache for the local cluster, where informers keep it
// fresh. A remote cluster has no informers, so its lists are fetched live
// with the requesting user's impersonated client and held here briefly.
//
// Three rules shape the type:
//   - Entries are keyed by (cluster, identity), never by cluster alone. A
//     remote cluster can grant two identities different views, and a
//     cluster-only key would serve one identity's view, or its error, to
//     another (Release C R-1).
//   - Errors are never cached, and a fetch shared through singleflight runs
//     on a context detached from the first caller's cancellation, so one
//     caller disconnecting cannot fail every coalesced waiter.
//   - EvictCluster drops every identity's entry for a cluster. Callers
//     register it as a ClusterRouter evict hook and call it after their own
//     writes, since a remote cluster sends no informer events.
package remotecache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const (
	// DefaultTTL is how long a remote read is served from cache.
	DefaultTTL = 30 * time.Second
	// DefaultMaxEntries bounds the cache so a burst of distinct
	// (cluster, identity) pairs cannot grow it without limit.
	DefaultMaxEntries = 128
	// fetchCap bounds a shared fetch whose first caller set no deadline.
	fetchCap = 30 * time.Second
)

type key struct {
	clusterID string // normalized
	identity  string // k8s.IdentityKey(username, groups)
}

type entry[T any] struct {
	value     T
	expiresAt time.Time
}

// Cache holds values of type T per (cluster, identity). The zero value is
// not usable; construct with New.
type Cache[T any] struct {
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu      sync.Mutex
	entries map[key]*entry[T]
	order   []key // insertion order for bounded-size eviction
	// gens counts evictions per cluster. A fetch records the generation
	// before it starts and stores its result only if no eviction happened
	// meanwhile, so a write or cluster removal is never masked by a fetch
	// that read the old state.
	gens map[string]uint64

	sf singleflight.Group
}

// New returns a Cache with the given TTL and entry bound. Non-positive
// values fall back to DefaultTTL and DefaultMaxEntries.
func New[T any](ttl time.Duration, maxEntries int) *Cache[T] {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &Cache[T]{
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        time.Now,
		entries:    make(map[key]*entry[T]),
		gens:       make(map[string]uint64),
	}
}

// Get returns the cached value for (clusterID, username, groups), or runs
// fetch to produce it. Concurrent misses on the same key share one fetch.
// fetch receives a context that keeps ctx's values but not its
// cancellation, bounded by ctx's deadline or, failing that, 30 seconds.
// Get itself returns early with ctx.Err() if ctx ends while waiting.
func (c *Cache[T]) Get(ctx context.Context, clusterID, username string, groups []string, fetch func(context.Context) (T, error)) (T, error) {
	k := key{clusterID: k8s.NormalizedClusterID(clusterID), identity: k8s.IdentityKey(username, groups)}
	if v, ok := c.lookup(k); ok {
		return v, nil
	}

	ch := c.sf.DoChan(k.clusterID+"\x00"+k.identity, func() (any, error) {
		if v, ok := c.lookup(k); ok {
			return v, nil
		}
		gen := c.generation(k.clusterID)

		fetchCtx := context.WithoutCancel(ctx)
		var cancel context.CancelFunc
		if deadline, ok := ctx.Deadline(); ok {
			fetchCtx, cancel = context.WithDeadline(fetchCtx, deadline)
		} else {
			fetchCtx, cancel = context.WithTimeout(fetchCtx, fetchCap)
		}
		defer cancel()

		v, err := safeFetch(fetchCtx, fetch)
		if err != nil {
			return nil, err
		}
		c.store(k, gen, v)
		return v, nil
	})

	var zero T
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return zero, res.Err
		}
		return res.Val.(T), nil
	}
}

// EvictCluster drops every entry for clusterID, for all identities, and
// invalidates fetches already in flight for it. It does not block on a
// fetch, so it is safe as a ClusterRouter evict hook.
func (c *Cache[T]) EvictCluster(clusterID string) {
	clusterID = k8s.NormalizedClusterID(clusterID)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gens[clusterID]++
	kept := c.order[:0]
	for _, k := range c.order {
		if k.clusterID == clusterID {
			delete(c.entries, k)
			continue
		}
		kept = append(kept, k)
	}
	c.order = kept
}

// safeFetch runs fetch, turning a panic into an error. singleflight.DoChan
// re-panics a panicking function on a fresh goroutine, which no recovery
// middleware covers, so a remote object that trips a normalizer would
// otherwise crash the process.
func safeFetch[T any](ctx context.Context, fetch func(context.Context) (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero T
			v, err = zero, fmt.Errorf("remotecache: fetch panicked: %v", r)
		}
	}()
	return fetch(ctx)
}

func (c *Cache[T]) lookup(k key) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || !c.now().Before(e.expiresAt) {
		var zero T
		return zero, false
	}
	return e.value, true
}

func (c *Cache[T]) generation(clusterID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gens[clusterID]
}

// store inserts v unless clusterID was evicted after gen was read.
func (c *Cache[T]) store(k key, gen uint64, v T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gens[k.clusterID] != gen {
		return
	}
	if _, exists := c.entries[k]; !exists {
		c.order = append(c.order, k)
	}
	c.entries[k] = &entry[T]{value: v, expiresAt: c.now().Add(c.ttl)}
	for len(c.order) > c.maxEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}
