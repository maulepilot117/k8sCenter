package policy

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

// PolicyChangeCallback is called when engine availability changes.
type PolicyChangeCallback func(kyvernoAvailable, gatekeeperAvailable bool)

// PolicyDiscoverer probes the cluster for Kyverno and OPA/Gatekeeper policy
// engines and maintains cached discovery state.
type PolicyDiscoverer struct {
	k8sClient    *k8s.ClientFactory
	crdDiscovery *k8s.CRDDiscovery
	logger       *slog.Logger

	mu             sync.RWMutex
	status         *EngineStatus
	gatekeeperCRDs []*k8s.CRDInfo
	onChange       PolicyChangeCallback
	hasDiscovered  bool // true after first Discover completes
}

// NewDiscoverer creates a new policy engine discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, crdDiscovery *k8s.CRDDiscovery, logger *slog.Logger) *PolicyDiscoverer {
	return &PolicyDiscoverer{
		k8sClient:    k8sClient,
		crdDiscovery: crdDiscovery,
		logger:       logger,
		status: &EngineStatus{
			LastChecked: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// SetOnChange registers a callback invoked when engine availability changes.
func (d *PolicyDiscoverer) SetOnChange(cb PolicyChangeCallback) {
	d.mu.Lock()
	d.onChange = cb
	d.mu.Unlock()
}

// Status returns a copy of the cached engine status.
func (d *PolicyDiscoverer) Status() EngineStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return *d.status
}

// GatekeeperConstraintCRDs returns the cached constraint CRDs for Gatekeeper.
func (d *PolicyDiscoverer) GatekeeperConstraintCRDs() []*k8s.CRDInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]*k8s.CRDInfo, len(d.gatekeeperCRDs))
	copy(out, d.gatekeeperCRDs)
	return out
}

// RunDiscoveryLoop runs discovery immediately, then every recheckInterval.
func (d *PolicyDiscoverer) RunDiscoveryLoop(ctx context.Context) {
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

// Discover probes the cluster for policy engines and updates cached state.
func (d *PolicyDiscoverer) Discover(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	disco := d.k8sClient.DiscoveryClient()

	var kyvernoDetail *EngineDetail
	var gatekeeperDetail *EngineDetail
	var constraintCRDs []*k8s.CRDInfo

	// Check Kyverno: look for ClusterPolicy kind in kyverno.io/v1
	kyvernoResources, err := disco.ServerResourcesForGroupVersion("kyverno.io/v1")
	if err == nil && kyvernoResources != nil {
		for _, r := range kyvernoResources.APIResources {
			if r.Kind == "ClusterPolicy" {
				kyvernoDetail = &EngineDetail{Available: true}
				break
			}
		}
	}

	// Check Gatekeeper: look for ConstraintTemplate kind in templates.gatekeeper.sh/v1
	gkResources, err := disco.ServerResourcesForGroupVersion("templates.gatekeeper.sh/v1")
	if err == nil && gkResources != nil {
		for _, r := range gkResources.APIResources {
			if r.Kind == "ConstraintTemplate" {
				gatekeeperDetail = &EngineDetail{Available: true}
				break
			}
		}
	}

	// Discover Kyverno namespace from webhook configs
	if kyvernoDetail != nil {
		ns, webhooks := d.detectWebhooks(ctx, "kyverno")
		kyvernoDetail.Namespace = ns
		kyvernoDetail.Webhooks = webhooks
	}

	// Discover Gatekeeper namespace from webhook configs
	if gatekeeperDetail != nil {
		ns, webhooks := d.detectWebhooks(ctx, "gatekeeper")
		gatekeeperDetail.Namespace = ns
		gatekeeperDetail.Webhooks = webhooks
	}

	// For Gatekeeper: filter constraint CRDs from CRDDiscovery
	if gatekeeperDetail != nil && d.crdDiscovery != nil {
		allCRDs := d.crdDiscovery.ListCRDs()
		for group, crds := range allCRDs {
			if strings.HasSuffix(group, "constraints.gatekeeper.sh") || group == "constraints.gatekeeper.sh" {
				constraintCRDs = append(constraintCRDs, crds...)
			}
		}
	}

	detected := EngineNone
	if kyvernoDetail != nil && gatekeeperDetail != nil {
		detected = EngineBoth
	} else if kyvernoDetail != nil {
		detected = EngineKyverno
	} else if gatekeeperDetail != nil {
		detected = EngineGatekeeper
	}

	status := &EngineStatus{
		Detected:    detected,
		Kyverno:     kyvernoDetail,
		Gatekeeper:  gatekeeperDetail,
		LastChecked: now,
	}

	newKyverno := kyvernoDetail != nil
	newGatekeeper := gatekeeperDetail != nil

	d.mu.Lock()
	prevKyverno := d.status.Kyverno != nil && d.status.Kyverno.Available
	prevGatekeeper := d.status.Gatekeeper != nil && d.status.Gatekeeper.Available
	firstRun := !d.hasDiscovered
	d.hasDiscovered = true
	d.status = status
	d.gatekeeperCRDs = constraintCRDs
	cb := d.onChange
	d.mu.Unlock()

	d.logger.Info("policy engine discovery complete",
		"detected", detected,
		"kyvernoAvailable", newKyverno,
		"gatekeeperAvailable", newGatekeeper,
		"constraintCRDs", len(constraintCRDs),
	)

	// Fire callback only on state transitions or the first discovery run.
	if cb != nil && (firstRun || prevKyverno != newKyverno || prevGatekeeper != newGatekeeper) {
		cb(newKyverno, newGatekeeper)
	}
}

// detectWebhooks counts validating/mutating webhooks containing the engine name
// and returns the first namespace found in webhook service references.
func (d *PolicyDiscoverer) detectWebhooks(ctx context.Context, engineName string) (string, int) {
	cs := d.k8sClient.BaseClientset()
	var namespace string
	webhookCount := 0

	vwcs, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, vwc := range vwcs.Items {
			if strings.Contains(strings.ToLower(vwc.Name), engineName) {
				webhookCount += len(vwc.Webhooks)
				for _, wh := range vwc.Webhooks {
					if wh.ClientConfig.Service != nil && namespace == "" {
						namespace = wh.ClientConfig.Service.Namespace
					}
				}
			}
		}
	}

	mwcs, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, mwc := range mwcs.Items {
			if strings.Contains(strings.ToLower(mwc.Name), engineName) {
				webhookCount += len(mwc.Webhooks)
				for _, wh := range mwc.Webhooks {
					if wh.ClientConfig.Service != nil && namespace == "" {
						namespace = wh.ClientConfig.Service.Namespace
					}
				}
			}
		}
	}

	return namespace, webhookCount
}
