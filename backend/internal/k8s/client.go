package k8s

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const clientCacheTTL = 5 * time.Minute

type cachedClient struct {
	clientset *kubernetes.Clientset
	expiresAt time.Time
}

// ClientFactory creates Kubernetes clientsets, with impersonation support
// and a cache to avoid repeated TLS handshakes.
type ClientFactory struct {
	baseConfig *rest.Config
	cache      sync.Map // map[string]cachedClient
	clusterID  string
	logger     *slog.Logger
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

	// Verify connectivity with base config
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
		baseConfig: cfg,
		clusterID:  clusterID,
		logger:     logger,
	}, nil
}

// BaseClientset returns a clientset using the service account's own permissions.
// Used for informers and non-user-initiated operations.
func (f *ClientFactory) BaseClientset() (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(f.baseConfig)
}

// BaseConfig returns the base REST config (for informer factory).
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

func cacheKey(username string, groups []string) string {
	h := sha256.Sum256([]byte(username + "|" + strings.Join(groups, ",")))
	return fmt.Sprintf("%x", h[:8])
}
