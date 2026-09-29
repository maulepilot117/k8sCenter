package gitops

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const recheckInterval = 5 * time.Minute

// DiscoveryChangeCallback is called when tool availability changes.
// argoAvailable and fluxAvailable reflect the current state.
type DiscoveryChangeCallback func(argoAvailable, fluxAvailable bool)

// GitOpsDiscoverer probes the cluster for ArgoCD and FluxCD GitOps tools
// and maintains cached discovery state.
type GitOpsDiscoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu            sync.RWMutex
	status        *GitOpsStatus
	onChange      DiscoveryChangeCallback
	hasDiscovered bool // true after first Discover completes
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

// SetOnChange registers a callback invoked when tool availability changes.
func (d *GitOpsDiscoverer) SetOnChange(cb DiscoveryChangeCallback) {
	d.mu.Lock()
	d.onChange = cb
	d.mu.Unlock()
}

// Status returns a copy of the cached GitOps status.
func (d *GitOpsDiscoverer) Status() GitOpsStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return *d.status
}

// RunDiscoveryLoop runs discovery immediately, then every recheckInterval.
func (d *GitOpsDiscoverer) RunDiscoveryLoop(ctx context.Context) {
	recoverutil.Tick(ctx, d.logger, "gitops discovery", d.Discover)

	ticker := time.NewTicker(recheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recoverutil.Tick(ctx, d.logger, "gitops discovery", d.Discover)
		}
	}
}

// toolGroupVersions are the API group/versions GitOps discovery reads.
var toolGroupVersions = []string{
	"argoproj.io/v1alpha1",
	"kustomize.toolkit.fluxcd.io/v1",
	"helm.toolkit.fluxcd.io/v2",
	"notification.toolkit.fluxcd.io/v1beta3",
}

// presenceResources are the resources whose presence means a GitOps tool is
// installed: every kind statusFromLists detects a tool by.
var presenceResources = []schema.GroupResource{
	ArgoApplicationGVR.GroupResource(),
	ArgoApplicationSetGVR.GroupResource(),
	FluxKustomizationGVR.GroupResource(),
	FluxHelmReleaseGVR.GroupResource(),
	{Group: "notification.toolkit.fluxcd.io", Resource: "providers"},
}

// statusFromLists derives which GitOps tools a cluster serves from its
// discovery lists, local or remote: Argo CD from argoproj.io/v1alpha1
// Application or ApplicationSet, Flux CD from a Kustomization, HelmRelease
// or notification Provider at the versions this package reads. Namespace,
// controllers and LastChecked are left to the caller.
func statusFromLists(lists []*metav1.APIResourceList) GitOpsStatus {
	kinds := map[string]map[string]bool{} // group/version -> kind set
	for _, l := range lists {
		if kinds[l.GroupVersion] == nil {
			kinds[l.GroupVersion] = map[string]bool{}
		}
		for _, r := range l.APIResources {
			kinds[l.GroupVersion][r.Kind] = true
		}
	}

	var argoDetail, fluxDetail *ToolDetail
	argo := kinds["argoproj.io/v1alpha1"]
	if argo["Application"] || argo["ApplicationSet"] {
		argoDetail = &ToolDetail{Available: true, AppSetsAvailable: argo["ApplicationSet"]}
	}
	notification := kinds["notification.toolkit.fluxcd.io/v1beta3"]["Provider"]
	if kinds["kustomize.toolkit.fluxcd.io/v1"]["Kustomization"] || kinds["helm.toolkit.fluxcd.io/v2"]["HelmRelease"] || notification {
		fluxDetail = &ToolDetail{Available: true, NotificationAvailable: notification}
	}

	detected := ToolNone
	switch {
	case argoDetail != nil && fluxDetail != nil:
		detected = ToolBoth
	case argoDetail != nil:
		detected = ToolArgoCD
	case fluxDetail != nil:
		detected = ToolFluxCD
	}
	return GitOpsStatus{Detected: detected, ArgoCD: argoDetail, FluxCD: fluxDetail}
}

// Discover probes the local cluster for GitOps tools and updates cached
// state. Remote clusters are probed per request by the handler.
func (d *GitOpsDiscoverer) Discover(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	// nolint:cluster-routing local path: the discoverer only probes the local cluster; remote status comes from Handler.remoteDiscovery.
	disco := d.k8sClient.DiscoveryClient()

	var lists []*metav1.APIResourceList
	for _, gv := range toolGroupVersions {
		if list, err := disco.ServerResourcesForGroupVersion(gv); err == nil && list != nil {
			lists = append(lists, list)
		}
	}
	found := statusFromLists(lists)
	argoDetail, fluxDetail := found.ArgoCD, found.FluxCD

	// For ArgoCD: probe pods in the argocd namespace
	if argoDetail != nil {
		// nolint:cluster-routing local path: the discoverer only probes the local cluster.
		pods, err := d.k8sClient.BaseClientset().CoreV1().Pods("argocd").List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil && len(pods.Items) > 0 {
			argoDetail.Namespace = "argocd"
		}
	}

	// For FluxCD: probe pods in the flux-system namespace, enumerate controllers
	if fluxDetail != nil {
		// nolint:cluster-routing local path: the discoverer only probes the local cluster.
		cs := d.k8sClient.BaseClientset()
		deps, err := cs.AppsV1().Deployments("flux-system").List(ctx, metav1.ListOptions{})
		if err == nil {
			fluxDetail.Namespace = "flux-system"
			controllerNames := []string{"source", "kustomize", "helm", "notification"}
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

	detected := found.Detected
	status := &GitOpsStatus{
		Detected:    detected,
		ArgoCD:      argoDetail,
		FluxCD:      fluxDetail,
		LastChecked: now,
	}

	newArgo := argoDetail != nil
	newFlux := fluxDetail != nil

	d.mu.Lock()
	prevArgo := d.status.ArgoCD != nil && d.status.ArgoCD.Available
	prevFlux := d.status.FluxCD != nil && d.status.FluxCD.Available
	firstRun := !d.hasDiscovered
	d.hasDiscovered = true
	d.status = status
	cb := d.onChange
	d.mu.Unlock()

	d.logger.Info("gitops tool discovery complete",
		"detected", detected,
		"argoCDAvailable", newArgo,
		"fluxCDAvailable", newFlux,
	)

	// Fire callback only on state transitions or the first discovery run.
	if cb != nil && (firstRun || prevArgo != newArgo || prevFlux != newFlux) {
		cb(newArgo, newFlux)
	}
}
