package monitoring

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// recheckInterval is how often the discoverer re-probes the cluster.
const recheckInterval = 5 * time.Minute

// ComponentStatus describes whether a monitoring component is available.
type ComponentStatus struct {
	Available       bool   `json:"available"`
	URL             string `json:"url,omitempty"`
	DetectionMethod string `json:"detectionMethod,omitempty"`
	LastChecked     string `json:"lastChecked"`
}

// DashboardStatus describes whether dashboards have been provisioned.
type DashboardStatus struct {
	Provisioned bool   `json:"provisioned"`
	Count       int    `json:"count"`
	Error       string `json:"error,omitempty"`
}

// MonitoringStatus is the cached discovery result.
type MonitoringStatus struct {
	Prometheus  ComponentStatus `json:"prometheus"`
	Grafana     ComponentStatus `json:"grafana"`
	Dashboards  DashboardStatus `json:"dashboards"`
	HasOperator bool            `json:"hasOperator"`
}

// Discoverer probes the cluster for Prometheus and Grafana and maintains
// cached clients for proxying.
type Discoverer struct {
	k8sClient *k8s.ClientFactory
	config    config.MonitoringConfig
	logger    *slog.Logger

	mu                     sync.RWMutex
	status                 *MonitoringStatus
	promClient             *PrometheusClient
	grafProxy              http.Handler
	grafClient             *GrafanaClient
	dashboardsProvisioned  bool
	dashboardsProvCount    int
}

// NewDiscoverer creates a new monitoring discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, cfg config.MonitoringConfig, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		k8sClient: k8sClient,
		config:    cfg,
		logger:    logger,
		status: &MonitoringStatus{
			Prometheus: ComponentStatus{LastChecked: time.Now().UTC().Format(time.RFC3339)},
			Grafana:    ComponentStatus{LastChecked: time.Now().UTC().Format(time.RFC3339)},
		},
	}
}

// Status returns the cached monitoring status.
func (d *Discoverer) Status() *MonitoringStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	// Return a copy to avoid data races
	s := *d.status
	return &s
}

// PrometheusClient returns the cached Prometheus client, or nil if unavailable.
func (d *Discoverer) PrometheusClient() *PrometheusClient {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.promClient
}

// GrafanaProxy returns the cached Grafana reverse proxy handler, or nil.
func (d *Discoverer) GrafanaProxy() http.Handler {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.grafProxy
}

// GrafanaClient returns the cached Grafana API client, or nil.
func (d *Discoverer) GrafanaAPIClient() *GrafanaClient {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.grafClient
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

// Discover probes the cluster for Prometheus and Grafana, updating cached state.
func (d *Discoverer) Discover(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	status := &MonitoringStatus{}

	// Check for Prometheus Operator CRDs
	status.HasOperator = d.checkOperatorCRDs(ctx)

	// Discover Prometheus
	promURL := d.config.PrometheusURL
	promMethod := "config-override"
	if promURL == "" {
		promURL, promMethod = d.discoverPrometheus(ctx)
	}
	status.Prometheus = ComponentStatus{
		Available:       promURL != "",
		URL:             promURL,
		DetectionMethod: promMethod,
		LastChecked:     now,
	}

	// Discover Grafana
	grafURL := d.config.GrafanaURL
	grafMethod := "config-override"
	if grafURL == "" {
		grafURL, grafMethod = d.discoverGrafana(ctx)
	}
	status.Grafana = ComponentStatus{
		Available:       grafURL != "",
		URL:             grafURL,
		DetectionMethod: grafMethod,
		LastChecked:     now,
	}

	// Build clients based on discovery
	var promClient *PrometheusClient
	if promURL != "" {
		pc, err := NewPrometheusClient(promURL)
		if err != nil {
			d.logger.Error("failed to create prometheus client", "url", promURL, "error", err)
		} else {
			promClient = pc
		}
	}

	var grafProxy http.Handler
	var grafClient *GrafanaClient
	if grafURL != "" {
		// Proxy is always created when Grafana is discovered — token is optional
		// (it adds an Authorization header for Grafana instances that require auth)
		proxy, err := newGrafanaProxy(grafURL, d.config.GrafanaToken)
		if err != nil {
			d.logger.Error("failed to create grafana proxy", "url", grafURL, "error", err)
		} else {
			grafProxy = proxy
		}
		// API client for dashboard provisioning requires a token
		if d.config.GrafanaToken != "" {
			grafClient = NewGrafanaClient(grafURL, d.config.GrafanaToken)
		}
	}

	// Provision dashboards once when Grafana is first discovered (or rediscovered)
	d.mu.RLock()
	alreadyProvisioned := d.dashboardsProvisioned
	lastCount := d.dashboardsProvCount
	d.mu.RUnlock()

	if grafClient == nil {
		// Reset flag so we re-provision when Grafana comes back
		alreadyProvisioned = false
		lastCount = 0
	} else if !alreadyProvisioned {
		count, err := grafClient.ProvisionDashboards(ctx, d.logger)
		if err != nil {
			d.logger.Error("dashboard provisioning failed", "error", err)
			status.Dashboards = DashboardStatus{Error: err.Error()}
		} else {
			alreadyProvisioned = true
			lastCount = count
			status.Dashboards = DashboardStatus{Provisioned: true, Count: count}
			d.logger.Info("dashboards provisioned", "count", count)
		}
	} else {
		// Already provisioned — report cached status
		status.Dashboards = DashboardStatus{Provisioned: true, Count: lastCount}
	}

	d.mu.Lock()
	d.status = status
	d.promClient = promClient
	d.grafProxy = grafProxy
	d.grafClient = grafClient
	d.dashboardsProvisioned = alreadyProvisioned
	d.dashboardsProvCount = lastCount
	d.mu.Unlock()

	d.logger.Info("monitoring discovery complete",
		"prometheus", promURL != "",
		"grafana", grafURL != "",
		"operator", status.HasOperator,
	)
}

// checkOperatorCRDs checks whether Prometheus Operator CRDs are installed.
func (d *Discoverer) checkOperatorCRDs(ctx context.Context) bool {
	disc := d.k8sClient.DiscoveryClient()
	if disc == nil {
		return false
	}
	resources, err := disc.ServerResourcesForGroupVersion("monitoring.coreos.com/v1")
	if err != nil {
		return false
	}
	for _, r := range resources.APIResources {
		if r.Kind == "ServiceMonitor" {
			return true
		}
	}
	return false
}

// wellKnownPrometheusServices are common Prometheus service names.
var wellKnownPrometheusServices = []struct{ name, namespace string }{
	{"prometheus-kube-prometheus-prometheus", "monitoring"},
	{"prometheus-operated", "monitoring"},
	{"prometheus-server", "monitoring"},
	{"prometheus", "monitoring"},
}

// wellKnownGrafanaServices are common Grafana service names.
var wellKnownGrafanaServices = []struct{ name, namespace string }{
	{"prometheus-grafana", "monitoring"},
	{"kube-prometheus-stack-grafana", "monitoring"},
	{"grafana", "monitoring"},
}

// discoverPrometheus finds a Prometheus service in the cluster.
func (d *Discoverer) discoverPrometheus(ctx context.Context) (string, string) {
	cs := d.k8sClient.BaseClientset()

	// 1. Check configured namespace hint first
	if d.config.Namespace != "" {
		if url, method := d.findServiceByLabel(ctx, d.config.Namespace, "app.kubernetes.io/name", "prometheus", 9090); url != "" {
			return url, method
		}
	}

	// 2. Well-known service names
	for _, svc := range wellKnownPrometheusServices {
		ns := svc.namespace
		if d.config.Namespace != "" {
			ns = d.config.Namespace
		}
		_, err := cs.CoreV1().Services(ns).Get(ctx, svc.name, metav1.GetOptions{})
		if err == nil {
			return fmt.Sprintf("http://%s.%s:9090", svc.name, ns), "service-name"
		}
	}

	// 3. Label selector across all namespaces
	if url, method := d.findServiceByLabel(ctx, "", "app.kubernetes.io/name", "prometheus", 9090); url != "" {
		return url, method
	}

	return "", ""
}

// discoverGrafana finds a Grafana service in the cluster.
func (d *Discoverer) discoverGrafana(ctx context.Context) (string, string) {
	cs := d.k8sClient.BaseClientset()

	// 1. Check configured namespace hint first
	if d.config.Namespace != "" {
		if url, method := d.findServiceByLabel(ctx, d.config.Namespace, "app.kubernetes.io/name", "grafana", 80); url != "" {
			return url, method
		}
	}

	// 2. Well-known service names
	for _, svc := range wellKnownGrafanaServices {
		ns := svc.namespace
		if d.config.Namespace != "" {
			ns = d.config.Namespace
		}
		s, err := cs.CoreV1().Services(ns).Get(ctx, svc.name, metav1.GetOptions{})
		if err == nil {
			port := int32(80)
			if len(s.Spec.Ports) > 0 {
				port = s.Spec.Ports[0].Port
			}
			return fmt.Sprintf("http://%s.%s:%d", svc.name, ns, port), "service-name"
		}
	}

	// 3. Label selector across all namespaces
	if url, method := d.findServiceByLabel(ctx, "", "app.kubernetes.io/name", "grafana", 80); url != "" {
		return url, method
	}

	return "", ""
}

// findServiceByLabel searches for a service by label in a given namespace (or all namespaces if empty).
func (d *Discoverer) findServiceByLabel(ctx context.Context, namespace, labelKey, labelValue string, defaultPort int32) (string, string) {
	cs := d.k8sClient.BaseClientset()
	selector := fmt.Sprintf("%s=%s", labelKey, labelValue)

	svcs, err := cs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
		Limit:         1,
	})
	if err != nil || len(svcs.Items) == 0 {
		return "", ""
	}

	svc := svcs.Items[0]
	port := defaultPort
	if len(svc.Spec.Ports) > 0 {
		port = svc.Spec.Ports[0].Port
	}
	return fmt.Sprintf("http://%s.%s:%d", svc.Name, svc.Namespace, port), "service-label"
}

// newGrafanaProxy creates a reverse proxy to Grafana with auth header injection.
func newGrafanaProxy(grafanaURL, token string) (http.Handler, error) {
	target, err := url.Parse(grafanaURL)
	if err != nil {
		return nil, fmt.Errorf("parsing grafana URL: %w", err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// Strip the proxy prefix from the path
			path := r.In.URL.Path
			path = strings.TrimPrefix(path, "/api/v1/monitoring/grafana/proxy")
			if path == "" {
				path = "/"
			}

			r.SetURL(target)
			r.Out.URL.Path = path
			r.Out.URL.RawQuery = r.In.URL.RawQuery

			// Inject Grafana service account token (if configured)
			if token != "" {
				r.Out.Header.Set("Authorization", "Bearer "+token)
			}
			r.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			// Strip Grafana's own security headers — KubeCenter sets its own CSP
			resp.Header.Del("X-Frame-Options")
			resp.Header.Del("Content-Security-Policy")
			// Prevent session fixation via proxied cookies
			resp.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("grafana proxy error", "err", err, "path", r.URL.Path)
			http.Error(w, `{"error":{"code":502,"message":"monitoring service unavailable"}}`, http.StatusBadGateway)
		},
	}
	return proxy, nil
}
