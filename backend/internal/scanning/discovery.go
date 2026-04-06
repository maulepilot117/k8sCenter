package scanning

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
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

// Discover probes the cluster for security scanners and updates cached state.
func (d *ScannerDiscoverer) Discover(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
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
		pods, err := d.k8sClient.BaseClientset().CoreV1().Pods("trivy-system").List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil && len(pods.Items) > 0 {
			trivyDetail.Namespace = "trivy-system"
		}
	}

	// For Kubescape: probe pods in the kubescape namespace
	if kubescapeDetail != nil {
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
