package resources

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/pkg/api"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SectionCoverage reports how completely one dashboard section was observed
// on a remote cluster. It is emitted only on the opt-in remote path
// (?coverage=1); see the Release C plan, decision D5.
type SectionCoverage struct {
	Section    string `json:"section"` // nodes|pods|services|cpu|memory|alerts|health
	Status     string `json:"status"`
	ReasonCode string `json:"reasonCode"`
	ObservedAt string `json:"observedAt,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Section statuses. "stale" is part of the wire contract but is never
// emitted in v1: remote acquisition is live per request, with no cache for a
// value to age in.
const (
	coverageOK          = "ok"
	coveragePartial     = "partial"
	coverageUnavailable = "unavailable"
	coverageForbidden   = "forbidden"
)

// Reason codes. These are the string values of server.ReasonCode (the
// capabilities enum, handle_capabilities.go), mirrored because this package
// cannot import internal/server. Emit nothing outside that closed set.
const (
	reasonOK                  = "ok"
	reasonUnsupportedPlatform = "unsupported_platform"
	reasonUnreachable         = "unreachable"
	reasonForbidden           = "forbidden"
	reasonAuthzUnknown        = "authz_unknown"
	reasonAuthzNamespaced     = "authz_namespace_scoped"
)

const (
	// remoteListPageSize matches the prober's node-list cap
	// (cluster_prober.go) so one page never asks a remote API server for more
	// than it already serves us elsewhere.
	remoteListPageSize = 500
	// remoteListMaxPages bounds one section to 5,000 items. A list still
	// carrying a continue token after this many pages is reported as partial.
	remoteListMaxPages = 10
)

// remoteDashboardBudget is the shared deadline for every section's SAR and
// list calls on one request. A variable only so the budget-expiry test can
// shorten it; nothing in production writes it.
var remoteDashboardBudget = 5 * time.Second

// Detail strings for the sections the remote path cannot serve in v1.
const (
	detailRemoteMetrics = "remote CPU/memory usage requires the remote metrics binding (deferred)"
	// detailReservationsUnknown is appended when requests/limits could not be
	// summed because the pods section did not load.
	detailReservationsUnknown = "; requests and limits need the pods section, which did not load"
	// detailReservationsPartial and detailAllocatablePartial are appended when
	// the pods or nodes read was partial, so the sums are lower bounds.
	detailReservationsPartial = "; requests and limits are summed over a truncated pod list"
	detailAllocatablePartial  = "; allocatable covers only the nodes that loaded"
	detailRemoteAlerts        = "alert counts are bound to the local Alertmanager"
	detailRemoteHealth        = "remote health scoring requires the remote metrics binding (deferred)"
)

// remoteSection is the outcome of reading one live section.
type remoteSection[T any] struct {
	items    []T
	coverage SectionCoverage
}

// handleRemoteDashboardSummary serves the dashboard summary for a remote
// cluster from direct, impersonated API lists — never informers, which exist
// only for the local cluster.
//
// Health is always nil here and the local health scorer (health.go) is
// deliberately not called: it renormalises weights across whichever signals
// resolved, so a remote cluster where only node listing succeeded would get a
// confident score built from nodes alone. The remote path has no workloads, metrics, or alert
// signal, so no score is the only truthful answer (plan decision D5).
func (h *Handler) handleRemoteDashboardSummary(w http.ResponseWriter, r *http.Request, user *auth.User, clusterID string) {
	// The budget starts before client resolution: the cluster-store read,
	// credential decrypt and dial are part of this request too.
	ctx, cancel := context.WithTimeout(r.Context(), remoteDashboardBudget)
	defer cancel()

	cs, err := h.remoteClientFor(ctx, clusterID, user)
	if err != nil {
		// No local fallback: an unresolvable remote target fails the request.
		h.Logger.Error("remote dashboard: resolve cluster client", "cluster", clusterID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	var (
		nodes    remoteSection[*corev1.Node]
		pods     remoteSection[*corev1.Pod]
		services remoteSection[*corev1.Service]
	)
	// A plain errgroup.Group (not WithContext): a failed section must not
	// cancel its siblings. Workers never return errors of their own; a
	// non-nil Wait() means a recovered panic, and that section's zero-value
	// result is turned into an "unavailable" row by sectionOrFailed below.
	var g errgroup.Group
	recoverutil.Go(&g, h.Logger, "resources remote dashboard nodes", func() error {
		nodes = readRemoteSection(h, ctx, clusterID, user, "nodes", func(ctx context.Context, opts metav1.ListOptions) ([]*corev1.Node, string, error) {
			l, err := cs.CoreV1().Nodes().List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
		return nil
	})
	recoverutil.Go(&g, h.Logger, "resources remote dashboard pods", func() error {
		pods = readRemoteSection(h, ctx, clusterID, user, "pods", func(ctx context.Context, opts metav1.ListOptions) ([]*corev1.Pod, string, error) {
			l, err := cs.CoreV1().Pods("").List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
		return nil
	})
	recoverutil.Go(&g, h.Logger, "resources remote dashboard services", func() error {
		services = readRemoteSection(h, ctx, clusterID, user, "services", func(ctx context.Context, opts metav1.ListOptions) ([]*corev1.Service, string, error) {
			l, err := cs.CoreV1().Services("").List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
		return nil
	})
	_ = g.Wait() // recovered panics are already logged by recoverutil

	var summary DashboardSummary
	summary.Nodes, summary.Pods, summary.Services = aggregateCounts(nodes.items, pods.items, len(services.items))

	// Requests, limits and allocatable are real; usage is not observable
	// remotely in v1, so the percentage stays at the N/A sentinel.
	capacity := aggregateCapacity(nodes.items, pods.items)
	summary.CPU = utilizationFrom(capacity, resourceKindCPU, nil)
	summary.Memory = utilizationFrom(capacity, resourceKindMemory, nil)

	// Requests and limits are sums over pods. When pods did not load, an
	// empty pod list would report a measured zero reservation beside a real
	// allocatable total, so report them as unknown instead.
	// A partial read keeps its numbers, which are real lower bounds, but the
	// cpu/memory rows say which input was incomplete.
	metricsDetail := detailRemoteMetrics
	switch pods.coverage.Status {
	case coverageOK:
	case coveragePartial:
		metricsDetail += detailReservationsPartial
	default:
		reservationsUnknown(summary.CPU)
		reservationsUnknown(summary.Memory)
		metricsDetail += detailReservationsUnknown
	}
	if nodes.coverage.Status == coveragePartial {
		metricsDetail += detailAllocatablePartial
	}

	summary.Coverage = []SectionCoverage{
		sectionOrFailed(nodes.coverage, "nodes"),
		sectionOrFailed(pods.coverage, "pods"),
		sectionOrFailed(services.coverage, "services"),
		unsupportedSection("cpu", metricsDetail),
		unsupportedSection("memory", metricsDetail),
		unsupportedSection("alerts", detailRemoteAlerts),
		unsupportedSection("health", detailRemoteHealth),
	}

	writeJSON(w, http.StatusOK, api.Response{Data: summary})
}

// remoteClientFor resolves the impersonating clientset for a remote cluster.
// The remoteClient override exists because a real remote resolution needs a
// cluster registry and reachable API server; production always routes through
// ClusterRouter, which has no local fallback on failure.
func (h *Handler) remoteClientFor(ctx context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error) {
	if h.remoteClient != nil {
		return h.remoteClient(ctx, clusterID, user)
	}
	return h.ClusterRouter.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
}

// readRemoteSection authorizes a cluster-wide list of resource on the remote
// cluster, then pages through it. Every outcome is reported as a coverage row.
// Items are returned whenever at least one page was read, unless the API
// server refused the list: a truncated read or a later-page failure is
// partial, while a refusal at any page is forbidden with no items, since
// what was read before the refusal may no longer be the user's to see.
func readRemoteSection[T any](
	h *Handler, ctx context.Context, clusterID string, user *auth.User, resource string,
	list func(context.Context, metav1.ListOptions) ([]T, string, error),
) remoteSection[T] {
	allowed, err := h.AccessChecker.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", resource, "")
	if err != nil {
		return remoteSection[T]{coverage: SectionCoverage{
			Section: resource, Status: coverageUnavailable, ReasonCode: reasonAuthzUnknown,
			Detail: "could not check list permission on the remote cluster",
		}}
	}
	if !allowed {
		return remoteSection[T]{coverage: forbiddenSection(resource)}
	}

	var items []T
	cont := ""
	for page := 0; page < remoteListMaxPages; page++ {
		batch, next, err := list(ctx, metav1.ListOptions{Limit: remoteListPageSize, Continue: cont})
		if err != nil {
			if apierrors.IsForbidden(err) {
				return remoteSection[T]{coverage: forbiddenSection(resource)}
			}
			if len(items) > 0 {
				return remoteSection[T]{items: items, coverage: SectionCoverage{
					Section: resource, Status: coveragePartial, ReasonCode: reasonUnreachable, ObservedAt: observedNow(),
					Detail: fmt.Sprintf("list stopped after %d items: a later page failed on the remote cluster", len(items)),
				}}
			}
			return remoteSection[T]{coverage: SectionCoverage{
				Section: resource, Status: coverageUnavailable, ReasonCode: reasonUnreachable,
				Detail: "list failed on the remote cluster",
			}}
		}
		items = append(items, batch...)
		cont = next
		if cont == "" {
			return remoteSection[T]{items: items, coverage: SectionCoverage{
				Section: resource, Status: coverageOK, ReasonCode: reasonOK, ObservedAt: observedNow(),
			}}
		}
	}
	return remoteSection[T]{items: items, coverage: SectionCoverage{
		Section: resource, Status: coveragePartial, ReasonCode: reasonOK, ObservedAt: observedNow(),
		Detail: fmt.Sprintf("list truncated after %d items", remoteListPageSize*remoteListMaxPages),
	}}
}

// forbiddenSection reports a denied cluster-wide list. Every section is
// listed across all namespaces, so the section cannot be read either way,
// but the reason differs by scope, matching /capabilities: for cluster-scoped
// nodes the deny is the whole answer (forbidden), while for namespaced pods
// and services it only proves the user lacks access in every namespace
// (authz_namespace_scoped); they may still read some namespaces.
func forbiddenSection(section string) SectionCoverage {
	if section == "nodes" {
		return SectionCoverage{
			Section: section, Status: coverageForbidden, ReasonCode: reasonForbidden,
			Detail: "you do not have permission to list nodes on this cluster",
		}
	}
	return SectionCoverage{
		Section: section, Status: coverageForbidden, ReasonCode: reasonAuthzNamespaced,
		Detail: "you do not have permission to list " + section + " across all namespaces on this cluster",
	}
}

func unsupportedSection(section, detail string) SectionCoverage {
	return SectionCoverage{Section: section, Status: coverageUnavailable, ReasonCode: reasonUnsupportedPlatform, Detail: detail}
}

// sectionOrFailed returns c, or an "unavailable" row when the section's
// worker never produced one (a recovered panic).
func sectionOrFailed(c SectionCoverage, section string) SectionCoverage {
	if c.Section != "" {
		return c
	}
	return SectionCoverage{
		Section: section, Status: coverageUnavailable, ReasonCode: reasonUnreachable,
		Detail: "section failed to load",
	}
}

// reservationsUnknown marks u's requests and limits as not observed, using
// the same "N/A" the Used field already carries for an unobserved value.
func reservationsUnknown(u *Utilization) {
	if u == nil {
		return
	}
	u.Requests = "N/A"
	u.Limits = "N/A"
}

func observedNow() string {
	return timeNow().UTC().Format(time.RFC3339)
}

func pointersTo[T any](items []T) []*T {
	out := make([]*T, len(items))
	for i := range items {
		out[i] = &items[i]
	}
	return out
}
