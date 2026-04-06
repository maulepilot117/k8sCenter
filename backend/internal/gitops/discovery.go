package gitops

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const recheckInterval = 5 * time.Minute

// GitOpsDiscoverer probes the cluster for ArgoCD and FluxCD GitOps tools
// and maintains cached discovery state.
type GitOpsDiscoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu     sync.RWMutex
	status *GitOpsStatus
}

// NewDiscoverer creates a new GitOps tool discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *GitOpsDiscoverer {
	return &GitOpsDiscoverer{
		k8sClient: k8sClient,
		logger:    logger,
		status: &GitOpsStatus{
			LastChecked: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// Status returns a copy of the cached GitOps status.
func (d *GitOpsDiscoverer) Status() GitOpsStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return *d.status
}

// RunDiscoveryLoop runs discovery immediately, then every recheckInterval.
func (d *GitOpsDiscoverer) RunDiscoveryLoop(ctx context.Context) {
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

// Discover probes the cluster for GitOps tools and updates cached state.
func (d *GitOpsDiscoverer) Discover(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	disco := d.k8sClient.DiscoveryClient()

	var argoDetail *ToolDetail
	var fluxDetail *ToolDetail

	// Check ArgoCD: look for Application kind in argoproj.io/v1alpha1
	argoResources, err := disco.ServerResourcesForGroupVersion("argoproj.io/v1alpha1")
	if err == nil && argoResources != nil {
		for _, r := range argoResources.APIResources {
			if r.Kind == "Application" {
				argoDetail = &ToolDetail{Available: true}
				break
			}
		}
	}

	// Check FluxCD: look for Kustomization kind in kustomize.toolkit.fluxcd.io/v1
	fluxKustomizeResources, err := disco.ServerResourcesForGroupVersion("kustomize.toolkit.fluxcd.io/v1")
	if err == nil && fluxKustomizeResources != nil {
		for _, r := range fluxKustomizeResources.APIResources {
			if r.Kind == "Kustomization" {
				fluxDetail = &ToolDetail{Available: true}
				break
			}
		}
	}

	// Check FluxCD Helm support: look for HelmRelease kind in helm.toolkit.fluxcd.io/v2
	fluxHelmResources, err := disco.ServerResourcesForGroupVersion("helm.toolkit.fluxcd.io/v2")
	if err == nil && fluxHelmResources != nil {
		for _, r := range fluxHelmResources.APIResources {
			if r.Kind == "HelmRelease" {
				if fluxDetail == nil {
					fluxDetail = &ToolDetail{Available: true}
				}
				break
			}
		}
	}

	// For ArgoCD: probe pods in the argocd namespace
	if argoDetail != nil {
		pods, err := d.k8sClient.BaseClientset().CoreV1().Pods("argocd").List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil && len(pods.Items) > 0 {
			argoDetail.Namespace = "argocd"
		}
	}

	// For FluxCD: probe pods in the flux-system namespace, enumerate controllers
	if fluxDetail != nil {
		cs := d.k8sClient.BaseClientset()
		deps, err := cs.AppsV1().Deployments("flux-system").List(ctx, metav1.ListOptions{})
		if err == nil {
			fluxDetail.Namespace = "flux-system"
			controllerNames := []string{"source", "kustomize", "helm"}
			for _, dep := range deps.Items {
				for _, name := range controllerNames {
					if strings.Contains(dep.Name, name) {
						fluxDetail.Controllers = append(fluxDetail.Controllers, name)
						break
					}
				}
			}
		}
	}

	detected := ToolNone
	if argoDetail != nil && fluxDetail != nil {
		detected = ToolBoth
	} else if argoDetail != nil {
		detected = ToolArgoCD
	} else if fluxDetail != nil {
		detected = ToolFluxCD
	}

	status := &GitOpsStatus{
		Detected:    detected,
		ArgoCD:      argoDetail,
		FluxCD:      fluxDetail,
		LastChecked: now,
	}

	d.mu.Lock()
	d.status = status
	d.mu.Unlock()

	d.logger.Info("gitops tool discovery complete",
		"detected", detected,
		"argoCDAvailable", argoDetail != nil,
		"fluxCDAvailable", fluxDetail != nil,
	)
}
