package loki

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// recheckInterval is how often the discoverer re-probes the cluster for Loki.
const recheckInterval = 5 * time.Minute

// Discoverer probes the cluster for a Loki instance and maintains a cached
// client for log queries.
type Discoverer struct {
	k8sClient *k8s.ClientFactory
	config    config.LokiConfig
	logger    *slog.Logger

	mu     sync.RWMutex
	status *LokiStatus
	client *Client
}

// NewDiscoverer creates a new Loki discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, cfg config.LokiConfig, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		k8sClient: k8sClient,
		config:    cfg,
		logger:    logger,
		status: &LokiStatus{
			LastChecked: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// Status returns the cached Loki discovery status.
func (d *Discoverer) Status() LokiStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return *d.status
}

// Client returns the cached Loki client, or nil if unavailable.
func (d *Discoverer) Client() *Client {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client
}

// TenantID returns the configured multi-tenant ID (X-Scope-OrgID), or empty.
func (d *Discoverer) TenantID() string {
	return d.config.TenantID
}

// RunDiscoveryLoop runs the discovery sequence immediately and then every
// recheckInterval until ctx is cancelled.
func (d *Discoverer) RunDiscoveryLoop(ctx context.Context) {
	d.Discover(ctx)

	ticker := time.NewTicker(recheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.Discover(ctx)
		}
	}
}

// wellKnownLokiServices are common Loki service names and their expected ports.
var wellKnownLokiServices = []struct {
	name      string
	port      int32
	namespace string
}{
	{"loki-gateway", 80, "monitoring"},
	{"loki-gateway", 80, "loki"},
	{"loki-gateway", 80, "observability"},
	{"loki", 3100, "monitoring"},
	{"loki", 3100, "loki"},
	{"loki", 3100, "observability"},
	{"loki-read", 3100, "monitoring"},
	{"loki-read", 3100, "loki"},
	{"loki-read", 3100, "observability"},
}

// Discover probes the cluster for a Loki instance, updating cached state.
func (d *Discoverer) Discover(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)

	lokiURL, method := d.discoverLoki(ctx)

	status := &LokiStatus{
		Detected:    lokiURL != "",
		URL:         lokiURL,
		DetectedVia: method,
		LastChecked: now,
	}

	var client *Client
	if lokiURL != "" {
		// Reuse existing client if the URL hasn't changed to avoid
		// creating a new transport on every discovery cycle.
		d.mu.RLock()
		existingClient := d.client
		d.mu.RUnlock()

		var c *Client
		if existingClient != nil && existingClient.BaseURL() == lokiURL {
			c = existingClient
		} else {
			c = NewClient(lokiURL, d.config.TenantID)
		}
		// Verify Loki is actually ready before caching the client
		if err := c.Ready(ctx); err != nil {
			d.logger.Warn("loki endpoint not ready", "url", lokiURL, "error", err)
			status.Detected = false
			status.URL = ""
			status.DetectedVia = ""
		} else {
			client = c
		}
	}

	d.mu.Lock()
	d.status = status
	d.client = client
	d.mu.Unlock()

	d.logger.Info("loki discovery complete",
		"detected", status.Detected,
		"url", status.URL,
		"method", status.DetectedVia,
	)
}

// discoverLoki finds a Loki service in the cluster.
func (d *Discoverer) discoverLoki(ctx context.Context) (string, string) {
	// 1. Config URL override
	if d.config.URL != "" {
		return d.config.URL, "config-override"
	}

	cs := d.k8sClient.BaseClientset()

	// 2. Well-known service names
	for _, svc := range wellKnownLokiServices {
		_, err := cs.CoreV1().Services(svc.namespace).Get(ctx, svc.name, metav1.GetOptions{})
		if err == nil {
			return fmt.Sprintf("http://%s.%s:%d", svc.name, svc.namespace, svc.port), "service-name"
		}
	}

	// 3. Label selector across all namespaces (prefer gateway component)
	if url, method := d.findServiceByLabel(ctx, "", "app.kubernetes.io/name", "loki", "gateway"); url != "" {
		return url, method
	}

	// No gateway found — accept any loki component
	if url, method := d.findServiceByLabel(ctx, "", "app.kubernetes.io/name", "loki", ""); url != "" {
		return url, method
	}

	// 4. Fallback label selector: app=loki
	if url, method := d.findServiceByLabel(ctx, "", "app", "loki", ""); url != "" {
		return url, method
	}

	return "", ""
}

// findServiceByLabel searches for a service by label in a given namespace
// (or all namespaces if empty). When component is non-empty, it additionally
// filters on `app.kubernetes.io/component`.
func (d *Discoverer) findServiceByLabel(ctx context.Context, namespace, labelKey, labelValue, component string) (string, string) {
	cs := d.k8sClient.BaseClientset()
	selector := fmt.Sprintf("%s=%s", labelKey, labelValue)
	if component != "" {
		selector += fmt.Sprintf(",app.kubernetes.io/component=%s", component)
	}

	svcs, err := cs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
		Limit:         1,
	})
	if err != nil || len(svcs.Items) == 0 {
		return "", ""
	}

	svc := svcs.Items[0]
	port := int32(3100)
	if len(svc.Spec.Ports) > 0 {
		port = svc.Spec.Ports[0].Port
	}
	return fmt.Sprintf("http://%s.%s:%d", svc.Name, svc.Namespace, port), "service-label"
}
