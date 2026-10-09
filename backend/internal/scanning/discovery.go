package scanning

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const recheckInterval = 5 * time.Minute

// ScannerDiscoverer probes the cluster for Trivy and Kubescape security scanners
// and maintains cached discovery state.
type ScannerDiscoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu     sync.RWMutex
	status *ScannerStatus
}

// NewDiscoverer creates a new security scanner discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *ScannerDiscoverer {
	return &ScannerDiscoverer{
		k8sClient: k8sClient,
		logger:    logger,
		status: &ScannerStatus{
			LastChecked: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// Status returns a copy of the cached scanner status.
func (d *ScannerDiscoverer) Status() ScannerStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return *d.status
}

// RunDiscoveryLoop runs discovery immediately, then every recheckInterval.
func (d *ScannerDiscoverer) RunDiscoveryLoop(ctx context.Context) {
	recoverutil.Tick(ctx, d.logger, "scanning discovery", d.Discover)

	ticker := time.NewTicker(recheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recoverutil.Tick(ctx, d.logger, "scanning discovery", d.Discover)
		}
	}
}

// Discover probes the local cluster for security scanners and updates cached
// state. It reads as the service account: these are discovery-only probes
// that never reach a user, and they never answer for a remote cluster.
func (d *ScannerDiscoverer) Discover(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	// nolint:cluster-routing local path: the Discoverer answers for the local cluster only; a remote cluster is probed as the user through Presence in remote.go.
	disco := d.k8sClient.DiscoveryClient()

	var trivyDetail *ScannerDetail
	var kubescapeDetail *ScannerDetail

	// Check Trivy: look for VulnerabilityReport kind in aquasecurity.github.io/v1alpha1
	trivyResources, err := disco.ServerResourcesForGroupVersion("aquasecurity.github.io/v1alpha1")
	if err == nil && trivyResources != nil {
		for _, r := range trivyResources.APIResources {
			if r.Kind == "VulnerabilityReport" {
				trivyDetail = &ScannerDetail{Available: true}
				break
			}
		}
	}

	// Check Kubescape: look for VulnerabilityManifestSummary kind in spdx.softwarecomposition.org/v1beta1
	kubescapeResources, err := disco.ServerResourcesForGroupVersion("spdx.softwarecomposition.org/v1beta1")
	if err == nil && kubescapeResources != nil {
		for _, r := range kubescapeResources.APIResources {
			if r.Kind == "VulnerabilityManifestSummary" {
				kubescapeDetail = &ScannerDetail{Available: true}
				break
			}
		}
	}

	// For Trivy: probe pods in the trivy-system namespace
	if trivyDetail != nil {
		// nolint:cluster-routing local path: discovery-only namespace probe of the local cluster; no probe runs on a remote cluster.
		pods, err := d.k8sClient.BaseClientset().CoreV1().Pods("trivy-system").List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil && len(pods.Items) > 0 {
			trivyDetail.Namespace = "trivy-system"
		}
	}

	// For Kubescape: probe pods in the kubescape namespace
	if kubescapeDetail != nil {
		// nolint:cluster-routing local path: discovery-only namespace probe of the local cluster; no probe runs on a remote cluster.
		pods, err := d.k8sClient.BaseClientset().CoreV1().Pods("kubescape").List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil && len(pods.Items) > 0 {
			kubescapeDetail.Namespace = "kubescape"
		}
	}

	detected := ScannerNone
	if trivyDetail != nil && kubescapeDetail != nil {
		detected = ScannerBoth
	} else if trivyDetail != nil {
		detected = ScannerTrivy
	} else if kubescapeDetail != nil {
		detected = ScannerKubescape
	}

	status := &ScannerStatus{
		Detected:    detected,
		Trivy:       trivyDetail,
		Kubescape:   kubescapeDetail,
		LastChecked: now,
	}

	d.mu.Lock()
	d.status = status
	d.mu.Unlock()

	d.logger.Info("security scanner discovery complete",
		"detected", detected,
		"trivyAvailable", trivyDetail != nil,
		"kubescapeAvailable", kubescapeDetail != nil,
	)
}
