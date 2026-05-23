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
func (cr *ClusterRouter) ClientForCluster(ctx context.Context, clusterID, username string, groups []string) (*kubernetes.Clientset, error) {
	if clusterID == "" || clusterID == "local" || cr.clusterStore == nil {
		return cr.localFactory.ClientForUser(username, groups)
	}
	return cr.remoteTypedClient(ctx, clusterID, username, groups)
}

// DynamicClientForCluster returns an impersonating dynamic client for the given cluster.
func (cr *ClusterRouter) DynamicClientForCluster(ctx context.Context, clusterID, username string, groups []string) (dynamic.Interface, error) {
	if clusterID == "" || clusterID == "local" || cr.clusterStore == nil {
		return cr.localFactory.DynamicClientForUser(username, groups)
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

// IsLocalClusterID reports whether the given X-Cluster-ID value resolves
// to the local in-cluster context. Empty, "local", and missing all count
// as local. Exported so handlers can gate unsupported remote operations
// before constructing a full ClientPair.
func IsLocalClusterID(clusterID string) bool {
	return clusterID == "" || clusterID == "local"
}

// isLocalClusterID is the unexported alias kept for internal package callers.
func isLocalClusterID(clusterID string) bool {
	return IsLocalClusterID(clusterID)
}

// normalizedClusterID returns "local" for empty/missing/"local", otherwise
// the input value unchanged. Used so handlers logging the resolved cluster
// see a stable string in audit entries.
func normalizedClusterID(clusterID string) string {
	if isLocalClusterID(clusterID) {
		return "local"
	}
	return clusterID
}

// EvictCluster removes all cached clients for a cluster (call on cluster deletion or credential update).
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
func (cr *ClusterRouter) remoteConfig(ctx context.Context, clusterID, username string, groups []string) (*rest.Config, error) {
	sfKey := clusterID + "\x00" + cacheKey(username, groups)
	val, err, _ := cr.configSF.Do(sfKey, func() (any, error) {
		return cr.buildRemoteConfig(ctx, clusterID, username, groups)
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
// private/loopback/CGNAT IP addresses. This prevents SSRF attacks.
// Called both at registration time and at connection time (DNS rebinding defense).
func ValidateRemoteURL(apiServerURL string) error {
	u, err := url.Parse(apiServerURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname")
	}

	// Resolve hostname to IPs
	ips, err := net.LookupHost(host)
	if err != nil {
		// If DNS resolution fails, allow the connection — the k8s client will
		// produce a more specific error. We only block known-private IPs.
		return nil
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL resolves to private/loopback address %s", ipStr)
		}
		if cgnatNet.Contains(ip) {
			return fmt.Errorf("URL resolves to CGNAT address %s", ipStr)
		}
	}

	return nil
}
