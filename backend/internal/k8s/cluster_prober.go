package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ProbeImpersonateRights runs a SelfSubjectAccessReview against the
// impersonate verb on users. Used by ProbeOne and by the cluster-registration
// handler to fail-fast when the stored bearer cannot impersonate — every
// KubeCenter request impersonates a username, so a missing grant turns into
// a runtime 403 on every feature.
//
// F#22 — surface the gap at probe time, not at first user-facing request.
func ProbeImpersonateRights(ctx context.Context, client kubernetes.Interface) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sar := &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Verb:     "impersonate",
				Resource: "users",
				Group:    "",
			},
		},
	}
	resp, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("SelfSubjectAccessReview failed: %w", err)
	}
	if !resp.Status.Allowed {
		reason := resp.Status.Reason
		if reason == "" {
			reason = "API server denied impersonate on users"
		}
		return fmt.Errorf("%s", reason)
	}
	return nil
}

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
	recoverutil.Tick(ctx, p.logger, "k8s cluster probe", p.probeAll)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recoverutil.Tick(ctx, p.logger, "k8s cluster probe", p.probeAll)
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

	// Build rest.Config with 10s timeout enforced at transport level.
	// F#1 — TLS policy MUST match cluster_router.buildRemoteConfig: when no
	// CAData is stored, only opt-in AllowInsecureTLS clusters may probe with
	// TLS verification disabled. Without this, the prober happily disabled
	// TLS verification on every CA-less remote — even those the router would
	// refuse to build a client for at request time — and reported them as
	// "connected", masking the misconfiguration. F#5 audit 2026-05-22.
	cfg := &rest.Config{
		Host:        cluster.APIServerURL,
		BearerToken: string(token),
		Timeout:     10 * time.Second,
		QPS:         10,
		Burst:       20,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caData,
		},
	}
	if err := applyClusterTLS(cfg, clusterID, caData, cluster.AllowInsecureTLS, p.logger); err != nil {
		_ = p.clusterStore.UpdateStatus(ctx, clusterID, "error", "TLS verification required (no CA, AllowInsecureTLS=false)", "", 0)
		p.emitStatusChange(ctx, clusterID, oldStatus, "error")
		return nil, err
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

	// F#22 — Verify the stored bearer can still impersonate users. If the
	// remote RBAC was tightened post-registration (token rotated, role
	// trimmed), surface the gap as a "disconnected" probe rather than letting
	// the cluster stay "connected" while every actual request 403s.
	if err := ProbeImpersonateRights(ctx, cs); err != nil {
		_ = p.clusterStore.UpdateStatus(ctx, clusterID, "disconnected", "credentials cannot impersonate users", "", 0)
		p.emitStatusChange(ctx, clusterID, oldStatus, "disconnected")
		return nil, fmt.Errorf("impersonate probe failed: %w", err)
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
