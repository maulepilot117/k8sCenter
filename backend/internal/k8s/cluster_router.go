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
	logger         *slog.Logger
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

// remoteConfig builds an impersonating rest.Config from stored cluster credentials.
func (cr *ClusterRouter) remoteConfig(ctx context.Context, clusterID, username string, groups []string) (*rest.Config, error) {
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

	// If no CA data, allow insecure TLS (self-signed certs common in homelabs)
	if len(caData) == 0 {
		cfg.TLSClientConfig.Insecure = true
		cr.logger.Warn("TLS verification disabled for remote cluster — no CA data provided", "clusterID", clusterID)
	}

	return cfg, nil
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
