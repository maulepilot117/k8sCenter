package loki

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
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
	recoverutil.Tick(ctx, d.logger, "loki discovery", d.Discover)

	ticker := time.NewTicker(recheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recoverutil.Tick(ctx, d.logger, "loki discovery", d.Discover)
		}
	}
}

// wellKnownLokiServices are common Loki service names and their expected
// ports, in search order. Each is looked for only in its fixed namespace.
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

	// nolint:cluster-routing local path: in-cluster discovery finds the local Loki only; the resolver reaches a remote cluster's Loki separately.
	cs := d.k8sClient.BaseClientset()

	// 2. The shared search order. Local discovery skips a step it cannot
	// read, as it always has: it runs again at the next recheck.
	found, _ := searchLokiService(ctx, cs, false)
	if found == nil {
		return "", ""
	}
	port := int32(3100)
	if found.hasPort {
		port = found.port.Port
	}
	return fmt.Sprintf("http://%s.%s:%d", found.svc.Name, found.svc.Namespace, port), found.via
}

// lokiLabelSearches is the label part of the search order (KTD6), tried
// cluster-wide after every well-known name: a gateway first, then any Loki
// component, then the older app=loki label.
var lokiLabelSearches = []string{
	"app.kubernetes.io/name=loki,app.kubernetes.io/component=gateway",
	"app.kubernetes.io/name=loki",
	"app=loki",
}

// Detection methods, reported as LokiStatus.DetectedVia.
const (
	viaServiceName  = "service-name"
	viaServiceLabel = "service-label"
)

// lokiService is the Service chosen to serve Loki and the port it is
// reached on. hasPort is false for a Service that declares no port.
type lokiService struct {
	svc     *corev1.Service
	port    corev1.ServicePort
	hasPort bool
	via     string
}

// searchLokiService finds Loki's Service with cs, in the order local
// discovery and the remote resolver share (KTD6): the well-known names in
// their fixed namespaces, in table order, then each label search in turn,
// where the lowest namespace and then name wins among the matches. A label
// match is only looked for once every well-known name is absent, so a
// Service anyone can label never outranks one at a well-known name.
//
// A nil result with a nil error means no Service was found. When failClosed
// is set, any error but NotFound ends the search with that error: a read the
// caller was refused must not let a lower-ranked Service win. Otherwise a
// step that cannot be read is skipped.
func searchLokiService(ctx context.Context, cs kubernetes.Interface, failClosed bool) (*lokiService, error) {
	for _, wk := range wellKnownLokiServices {
		svc, err := cs.CoreV1().Services(wk.namespace).Get(ctx, wk.name, metav1.GetOptions{})
		if err == nil {
			port, ok := lokiServicePort(svc, wk.port)
			return &lokiService{svc: svc, port: port, hasPort: ok, via: viaServiceName}, nil
		}
		if failClosed && !apierrors.IsNotFound(err) {
			return nil, err
		}
	}
	for _, selector := range lokiLabelSearches {
		list, err := cs.CoreV1().Services("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			if failClosed {
				return nil, err
			}
			continue
		}
		if len(list.Items) == 0 {
			continue
		}
		items := list.Items
		sort.Slice(items, func(i, j int) bool {
			if items[i].Namespace != items[j].Namespace {
				return items[i].Namespace < items[j].Namespace
			}
			return items[i].Name < items[j].Name
		})
		svc := items[0]
		port, ok := lokiServicePort(&svc, 0)
		return &lokiService{svc: &svc, port: port, hasPort: ok, via: viaServiceLabel}, nil
	}
	return nil, nil
}

// lokiServicePort picks the Service port Loki's HTTP API is served on: the
// port numbered want when it is declared, else the one named http-metrics
// (the Loki chart's name for it), else http, else the first. ok is false
// when the Service declares no port.
func lokiServicePort(svc *corev1.Service, want int32) (port corev1.ServicePort, ok bool) {
	ports := svc.Spec.Ports
	if len(ports) == 0 {
		return corev1.ServicePort{}, false
	}
	if want != 0 {
		for _, p := range ports {
			if p.Port == want {
				return p, true
			}
		}
	}
	for _, name := range []string{"http-metrics", "http"} {
		for _, p := range ports {
			if p.Name == name {
				return p, true
			}
		}
	}
	return ports[0], true
}
