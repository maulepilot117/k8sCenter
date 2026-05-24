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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/kubecenter/kubecenter/internal/store"
)

// ClusterRouter routes client requests to the correct cluster. For local
// requests it delegates to the existing ClientFactory. For remote clusters
// it builds clients from stored credentials with impersonation.
type ClusterRouter struct {
	localFactory   *ClientFactory
	clusterStore   *store.ClusterStore // nil for local-only deployments (no database)
	encryptionKey  string
	remoteCache    sync.Map // map[string]cachedClient
	remoteDynCache sync.Map // map[string]cachedDynClient
	// configSF gates calls to remoteConfig so that RouterFor's two halves
	// (typed + dynamic) — and any concurrent first-request burst — collapse
	// onto a single DB read + decrypt + SSRF re-validate instead of doing
	// the work twice. F#18.
	configSF singleflight.Group
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
	evictCBMu    sync.RWMutex
	evictCBs     []func(clusterID string)
}

// NewClusterRouter creates a ClusterRouter. clusterStore may be nil for
// local-only deployments (all requests fall through to localFactory).
func NewClusterRouter(local *ClientFactory, cs *store.ClusterStore, encKey string, logger *slog.Logger) *ClusterRouter {
	return &ClusterRouter{
		localFactory:  local,
		clusterStore:  cs,
		encryptionKey: encKey,
		logger:        logger,
	}
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
func (cr *ClusterRouter) ClientForCluster(ctx context.Context, clusterID, username string, groups []string) (*kubernetes.Clientset, error) {
	if clusterID == "" || clusterID == "local" {
		return cr.localFactory.ClientForUser(username, groups)
	}
	if cr.clusterStore == nil {
		return nil, fmt.Errorf("non-local clusterID %q requested but ClusterRouter has no cluster store — remote routing unavailable", clusterID)
	}
	return cr.remoteTypedClient(ctx, clusterID, username, groups)
}

// DynamicClientForCluster returns an impersonating dynamic client for the given cluster.
// See ClientForCluster for the F#18 fail-closed policy on nil clusterStore.
func (cr *ClusterRouter) DynamicClientForCluster(ctx context.Context, clusterID, username string, groups []string) (dynamic.Interface, error) {
	if clusterID == "" || clusterID == "local" {
		return cr.localFactory.DynamicClientForUser(username, groups)
	}
	if cr.clusterStore == nil {
		return nil, fmt.Errorf("non-local clusterID %q requested but ClusterRouter has no cluster store — remote routing unavailable", clusterID)
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
	Typed     *kubernetes.Clientset
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

// StartCacheSweeper periodically evicts expired entries from the remote client caches.
func (cr *ClusterRouter) StartCacheSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cacheSwapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
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
			}
		}
	}()
}

func (cr *ClusterRouter) remoteTypedClient(ctx context.Context, clusterID, username string, groups []string) (*kubernetes.Clientset, error) {
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

// ValidateRemoteURL checks that a remote cluster URL does not resolve to
// private/loopback/CGNAT/link-local/metadata IP addresses. This prevents
// SSRF attacks. Called both at registration time and at connection time
// (DNS rebinding defense).
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
func ValidateRemoteURL(apiServerURL string) error {
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

	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("DNS resolution returned no IPs for %s", host)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
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
// as off-limits for outbound requests built from user-supplied URLs:
// loopback, private RFC1918, link-local (which includes 169.254.169.254
// cloud metadata), CGNAT, and the unspecified 0.0.0.0/:: addresses.
// Centralised so ValidateRemoteURL and the SSRF DialContext share one
// blocklist — adding a range later automatically tightens both paths.
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
