package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/kubecenter/kubecenter/internal/store"
)

// StatusChangeFunc is called when a cluster's probe status transitions.
// Parameters: ctx, clusterID, oldStatus, newStatus.
type StatusChangeFunc func(ctx context.Context, clusterID, oldStatus, newStatus string)

// ClusterProber periodically checks the health of registered remote clusters.
// It also exposes ProbeOne for on-demand testing from the API.
type ClusterProber struct {
	clusterStore   *store.ClusterStore
	encKey         string
	onStatusChange StatusChangeFunc
	logger         *slog.Logger
}

// SetStatusChangeFunc sets the callback for cluster status transitions.
// This allows late-binding when the notification service isn't available at construction time.
func (p *ClusterProber) SetStatusChangeFunc(fn StatusChangeFunc) {
	p.onStatusChange = fn
}

// NewClusterProber creates a cluster health prober.
func NewClusterProber(cs *store.ClusterStore, encKey string, onStatusChange StatusChangeFunc, logger *slog.Logger) *ClusterProber {
	return &ClusterProber{
		clusterStore:   cs,
		encKey:         encKey,
		onStatusChange: onStatusChange,
		logger:         logger,
	}
}

// Run starts the probing loop. Probes immediately on startup, then every 60s.
// Blocks until ctx is cancelled.
func (p *ClusterProber) Run(ctx context.Context) {
	p.probeAll(ctx)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.probeAll(ctx)
		}
	}
}

func (p *ClusterProber) probeAll(ctx context.Context) {
	clusters, err := p.clusterStore.List(ctx)
	if err != nil {
		p.logger.Error("failed to list clusters for probing", "error", err)
		return
	}

	for _, c := range clusters {
		if c.IsLocal {
			continue
		}
		if _, err := p.ProbeOne(ctx, c.ID); err != nil {
			p.logger.Debug("probe failed", "clusterID", c.ID, "error", err)
		}
	}
}

// ProbeOne probes a single cluster and updates its status in the database.
// Returns the updated ClusterRecord. Exported for use by the test endpoint.
func (p *ClusterProber) ProbeOne(ctx context.Context, clusterID string) (*store.ClusterRecord, error) {
	// Fetch full record with credentials (List() omits them)
	cluster, err := p.clusterStore.Get(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("cluster %s not found: %w", clusterID, err)
	}

	oldStatus := cluster.Status

	// SSRF check — re-resolve DNS at probe time (DNS rebinding defense)
	if err := ValidateRemoteURL(cluster.APIServerURL); err != nil {
		_ = p.clusterStore.UpdateStatus(ctx, clusterID, "blocked", "URL resolves to private address", "", 0)
		p.emitStatusChange(ctx, clusterID, oldStatus, "blocked")
		return nil, fmt.Errorf("SSRF blocked: %w", err)
	}

	// Decrypt credentials
	token, err := store.Decrypt(cluster.AuthData, p.encKey)
	if err != nil {
		_ = p.clusterStore.UpdateStatus(ctx, clusterID, "error", "credential error", "", 0)
		p.emitStatusChange(ctx, clusterID, oldStatus, "error")
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	var caData []byte
	if len(cluster.CAData) > 0 {
		caData, err = store.Decrypt(cluster.CAData, p.encKey)
		if err != nil {
			// CA was stored but can't be decrypted — don't auto-downgrade to insecure
			_ = p.clusterStore.UpdateStatus(ctx, clusterID, "error", "credential error", "", 0)
			p.emitStatusChange(ctx, clusterID, oldStatus, "error")
			return nil, fmt.Errorf("CA decryption failed: %w", err)
		}
	}

	// Build rest.Config with 10s timeout enforced at transport level
	cfg := &rest.Config{
		Host:        cluster.APIServerURL,
		BearerToken: string(token),
		Timeout:     10 * time.Second,
		QPS:         10,
		Burst:       20,
	}
	if len(caData) > 0 {
		cfg.TLSClientConfig = rest.TLSClientConfig{CAData: caData}
	} else {
		cfg.TLSClientConfig = rest.TLSClientConfig{Insecure: true}
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		_ = p.clusterStore.UpdateStatus(ctx, clusterID, "error", sanitizeProbeError(err), "", 0)
		p.emitStatusChange(ctx, clusterID, oldStatus, "error")
		return nil, err
	}

	version, err := cs.Discovery().ServerVersion()
	if err != nil {
		_ = p.clusterStore.UpdateStatus(ctx, clusterID, "disconnected", sanitizeProbeError(err), "", 0)
		p.emitStatusChange(ctx, clusterID, oldStatus, "disconnected")
		return nil, err
	}

	// Node count (limit=500 — sufficient for any real cluster)
	nodeCount := 0
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 500})
	if err == nil {
		nodeCount = len(nodes.Items)
	}

	// Update status
	_ = p.clusterStore.UpdateStatus(ctx, clusterID, "connected", "", version.GitVersion, nodeCount)
	p.emitStatusChange(ctx, clusterID, oldStatus, "connected")
	p.logger.Debug("cluster probe succeeded", "clusterID", clusterID, "version", version.GitVersion, "nodes", nodeCount)

	updated, _ := p.clusterStore.Get(ctx, clusterID)
	return updated, nil
}

// emitStatusChange invokes the status change callback when a cluster transitions state.
func (p *ClusterProber) emitStatusChange(ctx context.Context, clusterID, oldStatus, newStatus string) {
	if p.onStatusChange == nil || oldStatus == newStatus {
		return
	}
	p.onStatusChange(ctx, clusterID, oldStatus, newStatus)
}

// sanitizeProbeError strips raw Go error details. Returns only safe categories.
func sanitizeProbeError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "i/o timeout") || strings.Contains(s, "deadline exceeded"):
		return "connection timeout"
	case strings.Contains(s, "certificate") || strings.Contains(s, "tls") || strings.Contains(s, "x509"):
		return "TLS certificate error"
	case strings.Contains(s, "401") || strings.Contains(s, "403") || strings.Contains(s, "unauthorized"):
		return "authentication failed"
	case strings.Contains(s, "no such host"):
		return "DNS resolution failed"
	default:
		return "connection error"
	}
}
