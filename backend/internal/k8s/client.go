package k8s

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	clientCacheTTL    = 5 * time.Minute
	cacheSwapInterval = 60 * time.Second
)

type cachedClient struct {
	clientset *kubernetes.Clientset
	expiresAt time.Time
}

// ClientFactory creates Kubernetes clientsets, with impersonation support
// and a cache to avoid repeated TLS handshakes.
type ClientFactory struct {
	baseConfig    *rest.Config
	baseClientset *kubernetes.Clientset
	cache         sync.Map // map[string]cachedClient
	clusterID     string
	logger        *slog.Logger
}

// NewClientFactory creates a ClientFactory using in-cluster config with
// a kubeconfig fallback for local development.
func NewClientFactory(clusterID string, devMode bool, logger *slog.Logger) (*ClientFactory, error) {
	var cfg *rest.Config
	var err error

	cfg, err = rest.InClusterConfig()
	if err != nil {
		if !devMode {
			return nil, fmt.Errorf("in-cluster config not available and dev mode is off: %w", err)
		}
		logger.Info("in-cluster config not available, falling back to kubeconfig")
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules, configOverrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig: %w", err)
		}
	}

	// Tune QPS/Burst for platform workloads (defaults are 5/10, too low)
	cfg.QPS = 50
	cfg.Burst = 100

	// Create and verify base clientset
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating base clientset: %w", err)
	}
	_, err = cs.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("connecting to kubernetes API: %w", err)
	}

	logger.Info("kubernetes client initialized",
		"cluster", clusterID,
		"host", cfg.Host,
	)

	return &ClientFactory{
		baseConfig:    cfg,
		baseClientset: cs,
		clusterID:     clusterID,
		logger:        logger,
	}, nil
}

// BaseClientset returns the shared clientset using the service account's own
// permissions. Used for informers and non-user-initiated operations.
func (f *ClientFactory) BaseClientset() *kubernetes.Clientset {
	return f.baseClientset
}

// BaseConfig returns a copy of the base REST config.
func (f *ClientFactory) BaseConfig() *rest.Config {
	return rest.CopyConfig(f.baseConfig)
}

// ClientForUser returns an impersonating clientset for the given user.
// Results are cached for 5 minutes keyed by hash(username+groups).
func (f *ClientFactory) ClientForUser(username string, groups []string) (*kubernetes.Clientset, error) {
	key := cacheKey(username, groups)

	if val, ok := f.cache.Load(key); ok {
		cc := val.(cachedClient)
		if time.Now().Before(cc.expiresAt) {
			return cc.clientset, nil
		}
		f.cache.Delete(key)
	}

	cfg := rest.CopyConfig(f.baseConfig)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: username,
		Groups:   groups,
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating impersonating clientset for %s: %w", username, err)
	}

	f.cache.Store(key, cachedClient{
		clientset: cs,
		expiresAt: time.Now().Add(clientCacheTTL),
	})

	return cs, nil
}

// StartCacheSweeper runs a background goroutine that evicts expired clients
// from the impersonation cache. Stops when the context is cancelled.
func (f *ClientFactory) StartCacheSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cacheSwapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				f.cache.Range(func(key, val any) bool {
					cc := val.(cachedClient)
					if now.After(cc.expiresAt) {
						f.cache.Delete(key)
					}
					return true
				})
			}
		}
	}()
}

// cacheKey produces a collision-resistant key from username and groups.
// Groups are sorted to ensure consistent keys regardless of input order.
// Null byte delimiters prevent ambiguity (k8s names cannot contain \x00).
func cacheKey(username string, groups []string) string {
	sorted := make([]string, len(groups))
	copy(sorted, groups)
	sort.Strings(sorted)

	input := username + "\x00" + strings.Join(sorted, "\x00")
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h)
}
