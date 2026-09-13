package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

// clusterGetter is the subset of *store.ClusterStore that ClusterRouter's
// remote-config and target-schema paths need. Defined as an interface
// (rather than depending on *store.ClusterStore directly) purely so tests in
// this package can substitute a fake that doesn't require a live PostgreSQL
// connection — buildRemoteConfig and TargetSchemaFor both call Get on
// whatever is stored here. Production always passes a *store.ClusterStore,
// which satisfies this trivially; NewClusterRouter's exported signature is
// unchanged, and the nil-vs-non-nil semantics of the "no cluster store"
// fail-closed check are preserved explicitly in NewClusterRouter (see the
// comment there) rather than relying on interface nil-conversion.
type clusterGetter interface {
	Get(ctx context.Context, id string) (*store.ClusterRecord, error)
}

// ClusterRouter routes client requests to the correct cluster. For local
// requests it delegates to the existing ClientFactory. For remote clusters
// it builds clients from stored credentials with impersonation.
type ClusterRouter struct {
	localFactory   *ClientFactory
	clusterStore   clusterGetter // nil for local-only deployments (no database)
	encryptionKey  string
	remoteCache    sync.Map // map[string]cachedClient
	remoteDynCache sync.Map // map[string]cachedDynClient
	// schemaCache holds per-(cluster, identity) discovery + RESTMapper pairs
	// for TargetSchemaFor. Bounded, TTL'd, evicted by both EvictCluster and
	// StartCacheSweeper. See discovery_cache.go (D1). The local branch of
	// TargetSchemaFor never touches this cache.
	schemaCache *targetSchemaCache
	// configSF gates calls to remoteConfig so that RouterFor's two halves
	// (typed + dynamic) — and any concurrent first-request burst — collapse
	// onto a single DB read + decrypt + SSRF re-validate instead of doing
	// the work twice. F#18.
	configSF singleflight.Group
	// schemaSF coalesces TargetSchemaFor's whole cold-miss path (cluster
	// record read, rest.Config build, discovery client, memcache wrapper,
	// deferred mapper, cache put) on the (cluster, identity) key. configSF
	// alone only collapses the rest.Config half, so N concurrent
	// first-requests for the same key would otherwise build N distinct
	// discovery/mapper pairs and race to overwrite one another in the
	// cache. PR #436 review finding #5.
	schemaSF singleflight.Group
	logger   *slog.Logger

	// F#8 (round-3) — eviction callbacks. Subsystems that maintain their
	// own per-cluster cache (e.g. certmanager.Handler.remoteCache) register
	// a hook here so EvictCluster propagates to them on cluster deletion
	// or credential update. Without this, the cert-manager remote cache
	// would keep serving stale data for up to the next cacheTTL window
	// even after the cluster is gone from the registry, which means a
	// re-registered cluster ID could briefly leak the previous tenant's
	// data through the cert-manager endpoints. Using a callback instead of
	// a direct import keeps the k8s package free of upward dependencies on
	// certmanager / eso / future cache-holding subsystems.
	evictCBMu sync.RWMutex
	evictCBs  []func(clusterID string)
}

// NewClusterRouter creates a ClusterRouter. clusterStore may be nil for
// local-only deployments (all requests fall through to localFactory).
//
// cs is accepted as the concrete *store.ClusterStore (not the clusterGetter
// interface the field is typed as) so this signature stays byte-identical
// for every existing caller. The nil check below is deliberate: assigning a
// nil *store.ClusterStore directly to an interface-typed field would produce
// a non-nil interface wrapping a nil pointer (the classic Go nil-interface
// trap), which would silently break every "cr.clusterStore == nil" fail-
// closed check (F#18) across ClientForCluster, DynamicClientForCluster, and
// TargetSchemaFor. Converting explicitly here keeps clusterStore a true nil
// interface when no store is configured.
func NewClusterRouter(local *ClientFactory, cs *store.ClusterStore, encKey string, logger *slog.Logger) *ClusterRouter {
	cr := &ClusterRouter{
		localFactory:  local,
		encryptionKey: encKey,
		logger:        logger,
		schemaCache:   newTargetSchemaCache(),
	}
	if cs != nil {
		cr.clusterStore = cs
	}
	return cr
}

// requireClusterStore is the single definition of the F#18 fail-closed guard
// shared by ClientForCluster, DynamicClientForCluster and TargetSchemaFor:
// when a non-local clusterID is requested but no cluster registry is wired,
// hard-error instead of silently downgrading to the local cluster.
//
// The error text is load-bearing — three tests in this package and three in
// internal/certmanager assert on the "no cluster store" substring. Do not
// reword it without updating them. Callers must have already ruled out the
// local cluster (IsLocalClusterID) before calling this.
func (cr *ClusterRouter) requireClusterStore(clusterID string) error {
	if cr.clusterStore == nil {
		return fmt.Errorf("non-local clusterID %q requested but ClusterRouter has no cluster store — remote routing unavailable", clusterID)
	}
	return nil
}

// ClientForCluster returns an impersonating clientset for the given cluster.
//
// F#18 (security audit 2026-05-22, round 2) — when a non-local clusterID is
// requested but no clusterStore is wired, fail closed rather than silently
// downgrading to the local cluster. The previous fall-through let a request
// targeting `remote-99` execute against the local cluster as long as no
// cluster registry was configured, which mismatched AccessChecker's behavior
// (it already hard-errored in the same scenario). Both layers now fail
// closed in the safer direction.
func (cr *ClusterRouter) ClientForCluster(ctx context.Context, clusterID, username string, groups []string) (kubernetes.Interface, error) {
	if clusterID == "" || clusterID == "local" {
		return cr.localFactory.ClientForUser(username, groups)
	}
	if err := cr.requireClusterStore(clusterID); err != nil {
		return nil, err
	}
	return cr.remoteTypedClient(ctx, clusterID, username, groups)
}

// DynamicClientForCluster returns an impersonating dynamic client for the given cluster.
// See ClientForCluster for the F#18 fail-closed policy on nil clusterStore.
func (cr *ClusterRouter) DynamicClientForCluster(ctx context.Context, clusterID, username string, groups []string) (dynamic.Interface, error) {
	if clusterID == "" || clusterID == "local" {
		return cr.localFactory.DynamicClientForUser(username, groups)
	}
	if err := cr.requireClusterStore(clusterID); err != nil {
		return nil, err
	}
	return cr.remoteDynamicClient(ctx, clusterID, username, groups)
}

// ClientPair bundles a typed clientset and a dynamic client for a single
// cluster context. IsLocal is true when the request resolved to the local
// cluster (empty/missing/"local" X-Cluster-ID). Handlers that don't yet
// support remote cluster operations should check IsLocal and respond with
// 501 Not Implemented for non-local requests rather than silently falling
// through to the local cluster — finding P2-5 of the 2026-05-22 security
// audit.
type ClientPair struct {
	ClusterID string
	IsLocal   bool
	Typed     kubernetes.Interface
	Dynamic   dynamic.Interface
}

// RouterFor resolves the target cluster (local or remote) and returns
// impersonating clients for the user. Builds both typed and dynamic clients
// so handlers needing both don't pay two routing round-trips. The clients
// share an underlying rest.Config that is cached for clientCacheTTL (5
// minutes) keyed on (clusterID, username, groups), so unused clients are
// cheap.
//
// Handlers must call this rather than f.K8sClient.ClientForUser /
// f.K8sClient.DynamicClientForUser directly, otherwise X-Cluster-ID is
// ignored and the request silently routes to the local cluster. The CI
// guard at scripts/check-cluster-routing.sh enforces this.
func (cr *ClusterRouter) RouterFor(ctx context.Context, clusterID, username string, groups []string) (*ClientPair, error) {
	typed, err := cr.ClientForCluster(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}
	dyn, err := cr.DynamicClientForCluster(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}
	return &ClientPair{
		ClusterID: normalizedClusterID(clusterID),
		IsLocal:   isLocalClusterID(clusterID),
		Typed:     typed,
		Dynamic:   dyn,
	}, nil
}

// LocalFactory returns the underlying ClientFactory for shared-service-
// account operations (RESTMapper, DiscoveryClient, BaseDynamicClient).
// Handlers that need both user-impersonating routing AND SA-shared reads
// can carry only *ClusterRouter and reach the factory through here.
func (cr *ClusterRouter) LocalFactory() *ClientFactory {
	return cr.localFactory
}

// TargetSchema binds a cluster identity to the discovery + mapper that must
// be used for it. Callers MUST use the returned Mapper/Discovery; reaching
// back to LocalFactory().RESTMapper() on a remote request is the bug this
// type exists to prevent (see scripts/check-cluster-routing.sh, U9b).
//
// Local vs remote is deliberately asymmetric and must not be "harmonised":
//   - Local: Mapper/Discovery come from the shared ClientFactory — service-
//     account-scoped and shared across every identity, matching pre-existing
//     behavior for every local handler in the product. Changing local
//     discovery to per-identity would alter RBAC semantics repo-wide and is
//     out of scope here.
//   - Remote: every remote rest.Config carries Impersonate, and a hardened
//     remote cluster may have removed the default system:discovery binding,
//     so two identities can legitimately see different API resource sets.
//     Discovery results are therefore permission-bearing on the remote
//     branch: they are cached per-identity and never served across
//     identities.
type TargetSchema struct {
	ClusterID string // normalized
	// Generation records which cluster-record vintage this schema was built
	// from: "local" for the local cluster, ClusterRecord.CreatedAt
	// (RFC3339Nano) otherwise.
	//
	// It is INFORMATIONAL, not a freshness guarantee. Generation is not part
	// of the schema cache key (see schemaCacheKey in discovery_cache.go), so
	// a cached entry reports the vintage that was current when the pair was
	// BUILT. If a cluster row is replaced under the same id without an
	// EvictCluster call, Generation can lag reality by up to one cache TTL
	// (clientCacheTTL). Staleness is handled by EvictCluster, which
	// ClusterRouter callers invoke on cluster deletion and credential update.
	Generation string
	IsLocal    bool
	Discovery  discovery.DiscoveryInterface
	Mapper     meta.RESTMapper
	// Invalidate forces the next RESTMapping to re-discover.
	//
	// On the remote branch this is mapper.Reset — the ONLY thing that clears
	// DeferredDiscoveryRESTMapper.delegate. cachedDiscovery.Invalidate alone
	// is not enough: getDelegate short-circuits on a non-nil delegate, so a
	// CRD removed or version-dropped remotely would keep resolving from the
	// stale delegate until it happened to self-reset via the unrelated
	// !Fresh() path. Do not swap this back to discovery.Invalidate.
	//
	// On the local branch this is deliberately a no-op instead of
	// LocalFactory().RESTMapper()'s own Reset — a documented deviation from
	// D1. The local mapper is process-shared across every identity and every
	// concurrent request; letting any single caller force a full
	// re-discovery there is a real denial-of-service shape (one request
	// invalidating a cache every other request depends on) that D1 did not
	// account for. So local and remote deliberately behave differently for
	// the same call: remote Invalidate is load-bearing, local Invalidate is
	// inert.
	Invalidate func()
}

// TargetSchemaFor resolves the discovery client + RESTMapper that must be
// used for clusterID, on behalf of (username, groups). See TargetSchema for
// the local/remote asymmetry this method deliberately preserves.
//
// The local branch returns the shared ClientFactory discovery/mapper
// directly and NEVER touches the schema cache — there is nothing per-
// identity to cache on the local branch, and caching it would be the first
// step toward the local/remote harmonisation this type exists to prevent.
//
// The remote branch fails closed (mirrors ClientForCluster's F#18 policy)
// when no cluster store is configured, and otherwise resolves against a
// bounded, TTL'd cache keyed on exactly (cluster, identity).
//
// The cache is consulted FIRST, before the cluster store is touched — a hit
// costs no database round trip at all, matching how remoteTypedClient /
// remoteDynamicClient already behave. Staleness is handled by EvictCluster,
// not by the key: an earlier revision folded the record's CreatedAt into the
// key, which forced a store read on every lookup (including every hit) while
// only catching the one case — a row replaced under the same id — that
// EvictCluster already covers. PR #436 review finding #1.
//
// A miss reads the cluster record (for the CreatedAt-derived generation
// stored on the entry), then builds a fresh pair via the same
// singleflight-protected remoteConfig used by RouterFor (no second remote
// rest.Config builder is introduced) and populates the cache. The entire
// miss path is coalesced on the same (cluster, identity) key via schemaSF,
// so a concurrent first-request burst builds exactly one discovery/mapper
// pair and every waiter receives it. PR #436 review finding #5.
func (cr *ClusterRouter) TargetSchemaFor(ctx context.Context, clusterID, username string, groups []string) (*TargetSchema, error) {
	if IsLocalClusterID(clusterID) {
		return &TargetSchema{
			ClusterID:  LocalClusterID,
			Generation: "local",
			IsLocal:    true,
			Discovery:  cr.localFactory.DiscoveryClient(),
			Mapper:     cr.localFactory.RESTMapper(),
			// Deliberate no-op, not LocalFactory().RESTMapper()'s own Reset —
			// see the deviation-from-D1 rationale on TargetSchema.Invalidate.
			Invalidate: func() {},
		}, nil
	}

	if err := cr.requireClusterStore(clusterID); err != nil {
		return nil, err
	}

	normID := NormalizedClusterID(clusterID)
	key := schemaCacheKey{
		clusterID: normID,
		identity:  cacheKey(username, groups),
	}

	// Cache first — no cluster-store read on the hit path (finding #1).
	if entry, ok := cr.schemaCache.get(key, time.Now()); ok {
		return remoteTargetSchema(normID, entry.generation, entry), nil
	}

	// Coalesce the whole cold-miss path, not just the rest.Config half, so
	// a burst of first-requests builds one discovery/mapper pair (finding #5).
	sfKey := key.clusterID + "\x00" + key.identity
	val, err, _ := cr.schemaSF.Do(sfKey, func() (any, error) {
		// Re-check under the singleflight slot: a waiter that queued behind
		// a just-finished build should take that build's entry rather than
		// start a second one.
		if entry, ok := cr.schemaCache.get(key, time.Now()); ok {
			return entry, nil
		}

		// Same F#17/F#6 treatment remoteConfig applies to its own shared
		// closure: keep the caller's context VALUES (request_id, trace span)
		// but drop the cancel signal, so the first caller disconnecting
		// doesn't poison every coalesced waiter. Preserve an explicit
		// deadline when the caller set one; otherwise bound the body at 30s.
		bgCtx := context.WithoutCancel(ctx)
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			bgCtx, cancel = context.WithDeadline(bgCtx, deadline)
			defer cancel()
		} else {
			var cancel context.CancelFunc
			bgCtx, cancel = context.WithTimeout(bgCtx, 30*time.Second)
			defer cancel()
		}

		// Read the record ONLY on the miss path, and only for its
		// CreatedAt-derived generation. Deliberately NOT UpdatedAt:
		// ClusterProber rewrites that every 60s, so keying or stamping off
		// it would churn the cache into uselessness.
		rec, err := cr.clusterStore.Get(bgCtx, clusterID)
		if err != nil {
			return nil, fmt.Errorf("cluster %s not found: %w", clusterID, err)
		}
		generation := rec.CreatedAt.UTC().Format(time.RFC3339Nano)

		// Reuse the existing singleflight-protected builder verbatim — do
		// not add a second remote rest.Config path.
		cfg, err := cr.remoteConfig(bgCtx, clusterID, username, groups)
		if err != nil {
			return nil, err
		}

		dc, err := discovery.NewDiscoveryClientForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("creating discovery client for cluster %s: %w", clusterID, err)
		}
		cached := memory.NewMemCacheClient(dc)
		mapper := restmapper.NewDeferredDiscoveryRESTMapper(cached)

		entry := &schemaCacheEntry{
			discovery:  cached,
			mapper:     mapper,
			generation: generation,
			expiresAt:  time.Now().Add(clientCacheTTL),
		}
		cr.schemaCache.put(key, entry)
		return entry, nil
	})
	if err != nil {
		return nil, err
	}
	entry, ok := val.(*schemaCacheEntry)
	if !ok {
		return nil, fmt.Errorf("schema singleflight returned unexpected type for cluster %s", clusterID)
	}
	return remoteTargetSchema(normID, entry.generation, entry), nil
}

// remoteTargetSchema builds the remote-branch TargetSchema from a cache
// entry. Both the hit and the miss path in TargetSchemaFor go through here
// so the five-field literal — in particular the Invalidate wiring, which a
// previous revision got wrong on one branch — exists exactly once.
func remoteTargetSchema(normID, generation string, e *schemaCacheEntry) *TargetSchema {
	return &TargetSchema{
		ClusterID:  normID,
		Generation: generation,
		IsLocal:    false,
		Discovery:  e.discovery,
		Mapper:     e.mapper,
		Invalidate: invalidateFunc(e.mapper),
	}
}

// invalidateFunc adapts m's Reset() into a func() for TargetSchema.Invalidate.
// Reset is the ONLY thing that clears DeferredDiscoveryRESTMapper.delegate —
// cachedDiscovery.Invalidate alone leaves getDelegate short-circuiting on the
// stale, still-non-nil delegate (see the doc comment on TargetSchema.Invalidate).
// Falls back to a no-op if m isn't the concrete deferred mapper TargetSchemaFor
// always constructs, so a future change to the mapper type here fails safe
// (a missed invalidation) rather than panicking.
func invalidateFunc(m meta.RESTMapper) func() {
	if r, ok := m.(*restmapper.DeferredDiscoveryRESTMapper); ok {
		return r.Reset
	}
	return func() {}
}

// TargetFor resolves clients AND schema for one cluster in a single call, so
// a handler cannot pair a remote client with a local mapper — both halves
// resolve the same clusterID argument. On any error, from either half, it
// returns (nil, nil, err); there is no branch that substitutes localFactory
// for a failed remote resolution.
func (cr *ClusterRouter) TargetFor(ctx context.Context, clusterID, username string, groups []string) (*ClientPair, *TargetSchema, error) {
	pair, err := cr.RouterFor(ctx, clusterID, username, groups)
	if err != nil {
		return nil, nil, err
	}
	schema, err := cr.TargetSchemaFor(ctx, clusterID, username, groups)
	if err != nil {
		return nil, nil, err
	}
	return pair, schema, nil
}

// LocalClusterID is the canonical string used everywhere the local cluster
// needs to be named: AccessChecker SAR cache keys, audit entries, debug
// logs, frontend `selectedCluster` defaults. Exported so call sites stop
// hardcoding the literal "local" — F#20 of the security audit re-review.
//
// If this value ever changes, every place that compares against it MUST go
// through IsLocalClusterID / NormalizedClusterID, never a literal.
const LocalClusterID = "local"

// IsLocalClusterID reports whether the given X-Cluster-ID value resolves
// to the local in-cluster context. Empty, "local", and missing all count
// as local. Exported so handlers can gate unsupported remote operations
// before constructing a full ClientPair.
func IsLocalClusterID(clusterID string) bool {
	return clusterID == "" || clusterID == LocalClusterID
}

// isLocalClusterID is the unexported alias kept for internal package callers.
func isLocalClusterID(clusterID string) bool {
	return IsLocalClusterID(clusterID)
}

// NormalizedClusterID returns the canonical string for the local cluster
// ("local") when the input resolves to local, otherwise the input value
// unchanged. Exported so cache keys / audit entries / log fields can use
// a single source of truth instead of separate "local" literals scattered
// across packages. F#20 of the security audit re-review.
func NormalizedClusterID(clusterID string) string {
	if IsLocalClusterID(clusterID) {
		return LocalClusterID
	}
	return clusterID
}

// normalizedClusterID is the unexported alias kept for internal callers.
func normalizedClusterID(clusterID string) string {
	return NormalizedClusterID(clusterID)
}

// EvictCluster removes all cached clients for a cluster (call on cluster
// deletion or credential update). F#8 (round-3) — also invokes every
// registered RegisterEvictHook callback so subsystems that hold their own
// per-cluster caches (cert-manager remote cache, future ESO/policy caches)
// drop their entries in the same operation.
func (cr *ClusterRouter) EvictCluster(clusterID string) {
	prefix := clusterID + "\x00"
	cr.remoteCache.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			cr.remoteCache.Delete(key)
		}
		return true
	})
	cr.remoteDynCache.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			cr.remoteDynCache.Delete(key)
		}
		return true
	})
	cr.schemaCache.evictCluster(clusterID)

	// F#8 — fan out to registered hooks. Each callback is responsible for
	// its own cache shape and locking. Snapshot under read lock so a hook
	// that re-registers callbacks (unusual but possible) doesn't deadlock.
	cr.evictCBMu.RLock()
	cbs := append([]func(string){}, cr.evictCBs...)
	cr.evictCBMu.RUnlock()
	for _, cb := range cbs {
		// Hooks must not block; if one does, only that subsystem's evict
		// is delayed (no other hook runs until it returns). Hook authors
		// own that contract.
		cb(clusterID)
	}
}

// RegisterEvictHook subscribes a callback to EvictCluster. Used by
// subsystems (cert-manager, ESO, policy) that maintain their own
// per-cluster caches so a cluster deletion or credential update wipes
// every layer in one operation. Wiring lives in main.go — keeping the
// hook list inside ClusterRouter avoids cyclic imports between k8s and
// the downstream cache holders. F#8.
func (cr *ClusterRouter) RegisterEvictHook(cb func(clusterID string)) {
	if cb == nil {
		return
	}
	cr.evictCBMu.Lock()
	cr.evictCBs = append(cr.evictCBs, cb)
	cr.evictCBMu.Unlock()
}

// StartCacheSweeper periodically evicts expired entries from the remote
// client caches and the schema cache.
//
// The ticker loop body is wrapped in recoverutil.Tick (backend-resilience
// convention, docs/solutions/backend-resilience-conventions.md Part 1): this
// goroutine runs outside chi's request-recovery middleware, so an unrecovered
// panic here would take down the whole process. There is no wg.Done() and no
// counted channel send in this loop, so the "keep cleanup outside the
// wrapped closure" hazard does not apply.
func (cr *ClusterRouter) StartCacheSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cacheSwapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				recoverutil.Tick(ctx, cr.logger, "k8s cluster-router cache sweep", func(context.Context) {
					now := time.Now()
					cr.remoteCache.Range(func(key, val any) bool {
						cc := val.(cachedClient)
						if now.After(cc.expiresAt) {
							cr.remoteCache.Delete(key)
						}
						return true
					})
					cr.remoteDynCache.Range(func(key, val any) bool {
						cc := val.(cachedDynClient)
						if now.After(cc.expiresAt) {
							cr.remoteDynCache.Delete(key)
						}
						return true
					})
					cr.schemaCache.sweepExpired(now)
				})
			}
		}
	}()
}

func (cr *ClusterRouter) remoteTypedClient(ctx context.Context, clusterID, username string, groups []string) (kubernetes.Interface, error) {
	key := clusterID + "\x00" + cacheKey(username, groups)

	// Check cache
	if val, ok := cr.remoteCache.Load(key); ok {
		cc := val.(cachedClient)
		if time.Now().Before(cc.expiresAt) {
			return cc.clientset, nil
		}
		cr.remoteCache.Delete(key)
	}

	cfg, err := cr.remoteConfig(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating remote clientset for cluster %s: %w", clusterID, err)
	}

	cr.remoteCache.Store(key, cachedClient{
		clientset: cs,
		expiresAt: time.Now().Add(clientCacheTTL),
	})
	return cs, nil
}

func (cr *ClusterRouter) remoteDynamicClient(ctx context.Context, clusterID, username string, groups []string) (dynamic.Interface, error) {
	key := clusterID + "\x00" + cacheKey(username, groups)

	if val, ok := cr.remoteDynCache.Load(key); ok {
		cc := val.(cachedDynClient)
		if time.Now().Before(cc.expiresAt) {
			return cc.client, nil
		}
		cr.remoteDynCache.Delete(key)
	}

	cfg, err := cr.remoteConfig(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating remote dynamic client for cluster %s: %w", clusterID, err)
	}

	cr.remoteDynCache.Store(key, cachedDynClient{
		client:    dc,
		expiresAt: time.Now().Add(clientCacheTTL),
	})
	return dc, nil
}

// remoteConfig builds an impersonating rest.Config from stored cluster
// credentials. Calls collapse via singleflight keyed on
// (clusterID, username, groups) so that RouterFor's two halves (typed +
// dynamic) and concurrent first-request bursts share a single DB read +
// decrypt + SSRF re-validate. F#18.
//
// F#17 (round-2) — singleflight ctx-cancel poisoning fix. The previous
// implementation captured the FIRST caller's context inside the shared Do()
// closure. If that caller's HTTP request was cancelled mid-flight (client
// disconnect, request timeout), every coalesced waiter saw the same
// context.Canceled error even though their own requests were still alive.
//
// F#6 + F#12 + F#15 (round-3) — switched from a manual background-context
// rebuild to context.WithoutCancel (Go 1.21+). WithoutCancel preserves
// every value on the caller's context (request_id, trace span, audit
// identity) while severing the cancel signal — this fixes both the
// original F#17 poisoning AND the F#15 trace-span drop in one go. When
// the caller did NOT set a deadline, we cap the singleflight body at
// 30s to bound a runaway DB or remote API; that 30s default matches the
// existing remote-cluster connection timeout. When the caller DID set a
// deadline, we preserve it via context.WithDeadline so a slow remote
// still respects the HTTP request budget.
func (cr *ClusterRouter) remoteConfig(ctx context.Context, clusterID, username string, groups []string) (*rest.Config, error) {
	sfKey := clusterID + "\x00" + cacheKey(username, groups)
	val, err, _ := cr.configSF.Do(sfKey, func() (any, error) {
		// Preserve caller context VALUES (request_id, trace span) but
		// drop the cancel signal so one caller's disconnect doesn't
		// poison every coalesced waiter.
		bgCtx := context.WithoutCancel(ctx)
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			bgCtx, cancel = context.WithDeadline(bgCtx, deadline)
			defer cancel()
		} else {
			// F#6 — bound the no-deadline case so a hung DB / remote
			// API doesn't keep the singleflight slot pinned forever
			// while every concurrent caller hangs waiting on it. 30s
			// matches the existing remote-cluster connection timeout.
			var cancel context.CancelFunc
			bgCtx, cancel = context.WithTimeout(bgCtx, 30*time.Second)
			defer cancel()
		}
		return cr.buildRemoteConfig(bgCtx, clusterID, username, groups)
	})
	if err != nil {
		return nil, err
	}
	cfg, ok := val.(*rest.Config)
	if !ok {
		return nil, fmt.Errorf("singleflight returned unexpected type for cluster %s", clusterID)
	}
	return rest.CopyConfig(cfg), nil
}

// buildRemoteConfig is the singleflight-protected body of remoteConfig.
// rest.Config carries client-go's own internal state and is not safe to
// mutate concurrently — callers receive a CopyConfig from the cache hit
// above so each clientset/dynamic-client gets its own instance.
func (cr *ClusterRouter) buildRemoteConfig(ctx context.Context, clusterID, username string, groups []string) (*rest.Config, error) {
	cluster, err := cr.clusterStore.Get(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("cluster %s not found: %w", clusterID, err)
	}

	// SSRF protection: re-resolve hostname and check for private IPs at connection time
	if err := ValidateRemoteURL(cluster.APIServerURL); err != nil {
		return nil, fmt.Errorf("cluster %s URL blocked: %w", clusterID, err)
	}

	// Decrypt credentials
	token, err := store.Decrypt(cluster.AuthData, cr.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting auth data for cluster %s: %w", clusterID, err)
	}

	var caData []byte
	if len(cluster.CAData) > 0 {
		caData, err = store.Decrypt(cluster.CAData, cr.encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("decrypting CA data for cluster %s: %w", clusterID, err)
		}
	}

	cfg := &rest.Config{
		Host:        cluster.APIServerURL,
		BearerToken: string(token),
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caData,
		},
		Impersonate: rest.ImpersonationConfig{
			UserName: username,
			Groups:   groups,
		},
		QPS:   50,
		Burst: 100,
		// P2-6 part 2: defend against DNS rebinding and unintended
		// server-controlled redirects by re-resolving the cluster host
		// on every dial and rejecting any candidate IP in the strict
		// block-list (including RFC1918 — a remote cluster API server
		// that resolves to a private address either was never legitimate
		// or has been rebound mid-session, and either case warrants
		// fail-closed). rest.Config.Dial is invoked by client-go's
		// underlying http transport for each new TCP connection, so
		// this protects every API call routed through the returned
		// config — not just the first dial after validation.
		Dial: StrictDialContext,
	}

	if err := applyClusterTLS(cfg, clusterID, caData, cluster.AllowInsecureTLS, cr.logger); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ApplyClusterTLS is the exported alias for applyClusterTLS, so external
// callers (the registration connection-test in
// server/handle_clusters.go) can route through the same F#5 fail-closed
// policy as the runtime router and the probe. F#13 round-3.
func ApplyClusterTLS(cfg *rest.Config, clusterID string, caData []byte, allowInsecure bool, logger *slog.Logger) error {
	return applyClusterTLS(cfg, clusterID, caData, allowInsecure, logger)
}

// applyClusterTLS enforces the F#5 fail-closed TLS policy on a remote
// cluster's rest.Config. When no CA data is stored the previous behaviour
// was to silently set TLSClientConfig.Insecure = true, which removed the
// MITM defense for every kubeconfig that happened to omit CA data. Now
// admins must explicitly opt in by setting AllowInsecureTLS on the cluster
// record; otherwise we return an error and refuse to build the client.
//
// Extracted so the policy is testable without needing a live PostgreSQL
// ClusterStore. F#5 security audit 2026-05-22.
func applyClusterTLS(cfg *rest.Config, clusterID string, caData []byte, allowInsecure bool, logger *slog.Logger) error {
	if len(caData) > 0 {
		return nil
	}
	if !allowInsecure {
		return fmt.Errorf("cluster %s has no CAData and AllowInsecureTLS is false; reject to prevent silent MITM exposure", clusterID)
	}
	cfg.TLSClientConfig.Insecure = true
	if logger != nil {
		logger.Warn("TLS verification disabled for remote cluster — operator opted in via AllowInsecureTLS", "clusterID", clusterID)
	}
	return nil
}

// SSRF blocklist: private, loopback, link-local, and CGNAT ranges.
var cgnatNet = &net.IPNet{
	IP:   net.ParseIP("100.64.0.0"),
	Mask: net.CIDRMask(10, 32),
}

// validateURLLookupTimeout caps the DNS resolver wait when callers
// don't supply a deadline. 5s is well below the 30s safeDialerTimeout
// downstream and well above the typical kube-dns response. Phase 4
// review (reliability R-1) — net.LookupHost without a deadline can
// stall the calling goroutine for up to the OS resolver timeout
// (~90s on default glibc), stacking goroutines in the cluster prober.
const validateURLLookupTimeout = 5 * time.Second

// ValidateRemoteURL is the no-context shim retained for callers that
// can't easily plumb a context. Prefer ValidateRemoteURLContext.
// Internally creates a 5-second deadline so a slow / broken resolver
// can't stall the caller for 90s.
func ValidateRemoteURL(apiServerURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), validateURLLookupTimeout)
	defer cancel()
	return ValidateRemoteURLContext(ctx, apiServerURL)
}

// ValidateRemoteURLContext checks that a remote cluster URL does not
// resolve to private/loopback/CGNAT/link-local/metadata IP addresses.
// This prevents SSRF attacks. Called both at registration time and at
// connection time (DNS rebinding defense).
//
// Phase 4 of the 2026-05-22 security audit (P2-6) — fails closed on
// DNS resolution errors. The previous implementation allowed the
// connection through on lookup failure, on the theory that the
// underlying client would produce a more specific error. That left a
// window where a transient DNS server response (NXDOMAIN intermittently
// returned for a poisoned response, or a rebinding flip between
// validation and dial) could let a request reach a private endpoint
// the IP-based block was supposed to refuse. Fail-closed is the safer
// posture: operators with broken DNS see an unambiguous error instead
// of an SSRF-shaped silent success.
//
// Phase 4 review (reliability R-1) — the ctx parameter caps the
// resolver wait. Callers without a context should use ValidateRemoteURL
// (which applies a 5s deadline internally).
func ValidateRemoteURLContext(ctx context.Context, apiServerURL string) error {
	u, err := url.Parse(apiServerURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname")
	}

	// If the host is already a literal IP, validate it directly without
	// going through DNS. Skipping resolution avoids spurious DNS-error
	// failures in environments without a resolver (in-cluster sidecar
	// configurations using IP-only endpoints).
	if ip := net.ParseIP(host); ip != nil {
		return checkIPNotPrivate(ip)
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("DNS resolution returned no IPs for %s", host)
	}

	for _, ipAddr := range ips {
		ip := ipAddr.IP
		if ip == nil {
			continue
		}
		if err := checkIPNotPrivate(ip); err != nil {
			return err
		}
	}

	return nil
}

// checkIPNotPrivate returns an error when ip is in any range we treat
// as off-limits for outbound requests built from user-supplied URLs
// targeting external endpoints: loopback, RFC1918 private, link-local
// (which includes 169.254.169.254 cloud metadata), CGNAT, and the
// unspecified 0.0.0.0/:: addresses. Used by ValidateRemoteURL +
// StrictDialContext for remote cluster URLs and other admin-supplied
// inputs that must not resolve to anything cluster-internal.
func checkIPNotPrivate(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("URL resolves to loopback address %s", ip)
	}
	if ip.IsPrivate() {
		return fmt.Errorf("URL resolves to private address %s", ip)
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("URL resolves to link-local/metadata address %s", ip)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("URL resolves to unspecified address %s", ip)
	}
	if cgnatNet.Contains(ip) {
		return fmt.Errorf("URL resolves to CGNAT address %s", ip)
	}
	return nil
}

// checkIPAlwaysBad returns an error for IP ranges that are off-limits
// even from inside the cluster: loopback (DNS-rebinding to localhost),
// link-local (cloud metadata IAM theft via 169.254.169.254), CGNAT
// (carrier infrastructure), and unspecified. RFC1918 is INTENTIONALLY
// allowed — every in-cluster Service ClusterIP falls inside RFC1918,
// and SafeDialContext is used by the in-cluster monitoring clients
// where blocking RFC1918 would silently break the bundled monitoring
// subchart.
//
// The audit's "block private/loopback" recommendation in P2-6 was
// framed for admin-supplied URLs targeting external endpoints. For
// those, use checkIPNotPrivate via StrictDialContext. For URLs that
// may legitimately resolve to a cluster-internal ClusterIP, use this
// looser check via SafeDialContext.
func checkIPAlwaysBad(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("URL resolves to loopback address %s", ip)
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("URL resolves to link-local/metadata address %s", ip)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("URL resolves to unspecified address %s", ip)
	}
	if cgnatNet.Contains(ip) {
		return fmt.Errorf("URL resolves to CGNAT address %s", ip)
	}
	return nil
}
