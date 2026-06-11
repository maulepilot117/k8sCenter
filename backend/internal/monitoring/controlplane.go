package monitoring

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/common/model"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Control-plane job label constants — kube-prometheus-stack defaults.
// These are the job label values emitted by the kube-prometheus-stack Helm
// chart's scrape configs. Distributions vary:
//
//   - kube-scheduler:            "kube-scheduler"
//   - kube-controller-manager:   "kube-controller-manager"
//   - etcd:                      "etcd" (kubeadm) OR "kube-etcd" (some kube-prometheus-stack versions)
//
// k3s, k0s, and managed clouds (EKS, GKE, AKS) typically don't scrape
// control-plane components at all — the absent result maps to ComponentUnscraped.
const (
	jobScheduler          = "kube-scheduler"
	jobControllerManager  = "kube-controller-manager"
	jobEtcd               = "etcd"
	jobEtcdAlt            = "kube-etcd" // alternate name used by some kube-prometheus-stack versions
)

// controlPlaneQuery is the PromQL instant query used to detect component availability.
// max by (job) collapses multiple replicas into a single 0/1 per job — a component
// is "up" when at least one replica reports up==1; "down" when all report 0.
const controlPlaneQuery = `max by (job) (up{job=~"kube-scheduler|kube-controller-manager|etcd|kube-etcd"})`

// ControlPlaneAdapter implements resources.ControlPlaneChecker using PrometheusClient.
type ControlPlaneAdapter struct {
	Discoverer *Discoverer
}

// ControlPlaneStatus queries Prometheus for scheduler, controller-manager, and etcd
// up/down states. Components absent from the result vector are treated as not-scraped
// (e.g. k3s embedded control plane, no scrape config, managed-cloud control planes).
//
// Returns an error when the Prometheus client is nil or the query fails; the caller
// maps errors to resources.ControlPlaneStates with all components set to
// ComponentUnscraped and surfaces the signal as SignalStatusUnknown.
func (a *ControlPlaneAdapter) ControlPlaneStatus(ctx context.Context) (resources.ControlPlaneStates, error) {
	pc := a.Discoverer.PrometheusClient()
	if pc == nil {
		return resources.ControlPlaneStates{}, fmt.Errorf("prometheus not available")
	}

	result, _, err := pc.Query(ctx, controlPlaneQuery, time.Now())
	if err != nil {
		return resources.ControlPlaneStates{}, fmt.Errorf("querying control-plane up metrics: %w", err)
	}

	vec, ok := result.(model.Vector)
	if !ok {
		return resources.ControlPlaneStates{}, fmt.Errorf("unexpected result type from control-plane query")
	}

	// Build a map from job label → ComponentState so each component can be looked up.
	// Absent entries remain at the zero value (ComponentUnscraped).
	states := make(map[string]resources.ComponentState, len(vec))
	for _, sample := range vec {
		job := string(sample.Metric["job"])
		if float64(sample.Value) >= 1 {
			states[job] = resources.ComponentUp
		} else {
			states[job] = resources.ComponentDown
		}
	}

	// etcd can appear under either "etcd" or "kube-etcd" — prefer the more specific
	// state when both are present (down beats unscraped; up beats down is not possible
	// since max by (job) treats them as separate jobs).
	etcdState := resolveEtcdState(states)

	return resources.ControlPlaneStates{
		SchedulerState:         stateFor(states, jobScheduler),
		ControllerManagerState: stateFor(states, jobControllerManager),
		EtcdState:              etcdState,
	}, nil
}

// stateFor returns the ComponentState for the given job, defaulting to
// ComponentUnscraped when the job was not present in the result vector.
func stateFor(states map[string]resources.ComponentState, job string) resources.ComponentState {
	if s, ok := states[job]; ok {
		return s
	}
	return resources.ComponentUnscraped
}

// resolveEtcdState returns the etcd ComponentState, checking both the canonical
// "etcd" job name and the alternate "kube-etcd" name used by some kube-prometheus-stack
// versions. When both are present, ComponentDown takes precedence over ComponentUp
// (i.e. if one scrape target is down, report the component as down).
func resolveEtcdState(states map[string]resources.ComponentState) resources.ComponentState {
	s1, ok1 := states[jobEtcd]
	s2, ok2 := states[jobEtcdAlt]

	if !ok1 && !ok2 {
		return resources.ComponentUnscraped
	}
	if ok1 && !ok2 {
		return s1
	}
	if ok2 && !ok1 {
		return s2
	}
	// Both present — if either is down, report down.
	if s1 == resources.ComponentDown || s2 == resources.ComponentDown {
		return resources.ComponentDown
	}
	return resources.ComponentUp
}
