package resources

import (
	"context"
	"errors"
	"strings"

	"github.com/kubecenter/kubecenter/internal/auth"
	"golang.org/x/sync/errgroup"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// The remote health mapper (KTD4, KTD5, KTD11). It turns the remote summary's
// reads into one outcome per health input, and assembleHealthInputs
// (health_shaping.go) applies the local scoring rules to them. It differs from
// the local mapper (gatherHealthInputs) only where KTD4 says it must: an
// allowed read that did not complete is a failed outcome, which withholds the
// score, rather than a degrade-but-score state.

// Cause classes for an allowed remote read that did not complete. They are
// written into the health row's detail, so each is a fixed phrase, never
// error text (which can name an address).
const (
	// healthCauseRefused: the API server refused a list the permission check
	// allowed.
	healthCauseRefused healthCause = "refused by the API"
	// healthCauseAuthzUnknown: the permission check itself could not be made.
	healthCauseAuthzUnknown healthCause = "permission check failed"
	healthCauseListFailed   healthCause = "list failed"
	// healthCausePartial: a later page failed after earlier pages were read.
	healthCausePartial healthCause = "stopped after a page failed"
	// healthCauseTruncated: the list still had more pages at the remote list
	// cap. Clusters that large get no remote score (plan scope boundary).
	healthCauseTruncated healthCause = "truncated at the remote list cap"
	// healthCauseTimedOut: the shared summary budget ended first (KTD11).
	healthCauseTimedOut healthCause = "timed out"
	// healthCauseQueryFailed: the cluster's Prometheus did not answer the
	// control-plane query.
	healthCauseQueryFailed healthCause = "the cluster's Prometheus could not be read"
	// The Alertmanager classes, one per error class of the alert read.
	healthCauseAlertmanagerRefused     healthCause = "the cluster's Alertmanager refused the request"
	healthCauseAlertmanagerRedirected  healthCause = "the cluster's Alertmanager answered with a redirect"
	healthCauseAlertmanagerUnreachable healthCause = "the cluster's Alertmanager could not be reached"
	healthCauseAlertmanagerUnreadable  healthCause = "the cluster's Alertmanager answer could not be read"
	healthCauseAlertmanagerReentry     healthCause = "the Alertmanager token must be entered again"
	healthCauseAlertmanagerFailed      healthCause = "the cluster's Alertmanager could not be read"
)

// detailHealthMissing opens the health row's detail when no score is drawn.
const detailHealthMissing = "no score is drawn because these inputs did not load: "

// healthInputLabels names each health input in the health row's detail.
var healthInputLabels = map[string]string{
	healthInputNodes:          "nodes",
	healthInputDeployments:    "deployments",
	healthInputStatefulSets:   "statefulsets",
	healthInputDaemonSets:     "daemonsets",
	healthInputPods:           "pods",
	healthInputPDBs:           "pod disruption budgets",
	signalAlerts:              "alerts",
	signalCerts:               "certificates",
	healthInputPVCs:           "persistent volume claims",
	healthInputStorageClasses: "storage classes",
	signalControlPlane:        "control plane",
}

// remoteHealthReads holds the reads that only the health score uses. The
// nodes, pods and alerts reads are the tile sections' own (R8).
type remoteHealthReads struct {
	deployments    remoteSection[*appsv1.Deployment]
	statefulSets   remoteSection[*appsv1.StatefulSet]
	daemonSets     remoteSection[*appsv1.DaemonSet]
	pdbs           remoteSection[*policyv1.PodDisruptionBudget]
	pvcs           remoteSection[*corev1.PersistentVolumeClaim]
	storageClasses remoteSection[*storagev1.StorageClass]
	// certificates and controlPlane are written once their read returns, so
	// a worker that panics leaves them unset, which the assembly counts as
	// not read.
	certificates healthOutcome[int]
	controlPlane healthOutcome[ControlPlaneStates]
}

// startRemoteHealthReads adds the health-only reads to the summary's
// fan-out. Every read runs as the user under the request's budget (KTD11).
// The workload and control-plane reads are not held back on the nodes
// permission as the local path holds them: they run concurrently with the
// nodes check, and the assembly ignores them when nodes are denied.
func (h *Handler) startRemoteHealthReads(g *errgroup.Group, ctx context.Context, cs kubernetes.Interface, clusterID string, user *auth.User) *remoteHealthReads {
	r := &remoteHealthReads{}
	h.goSection(g, clusterID, "deployments", func() {
		r.deployments = readRemoteSection(h, ctx, clusterID, user, "deployments", func(ctx context.Context, opts metav1.ListOptions) ([]*appsv1.Deployment, string, error) {
			l, err := cs.AppsV1().Deployments("").List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
	})
	h.goSection(g, clusterID, "statefulsets", func() {
		r.statefulSets = readRemoteSection(h, ctx, clusterID, user, "statefulsets", func(ctx context.Context, opts metav1.ListOptions) ([]*appsv1.StatefulSet, string, error) {
			l, err := cs.AppsV1().StatefulSets("").List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
	})
	h.goSection(g, clusterID, "daemonsets", func() {
		r.daemonSets = readRemoteSection(h, ctx, clusterID, user, "daemonsets", func(ctx context.Context, opts metav1.ListOptions) ([]*appsv1.DaemonSet, string, error) {
			l, err := cs.AppsV1().DaemonSets("").List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
	})
	h.goSection(g, clusterID, "poddisruptionbudgets", func() {
		r.pdbs = readRemoteSection(h, ctx, clusterID, user, "poddisruptionbudgets", func(ctx context.Context, opts metav1.ListOptions) ([]*policyv1.PodDisruptionBudget, string, error) {
			l, err := cs.PolicyV1().PodDisruptionBudgets("").List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
	})
	h.goSection(g, clusterID, "persistentvolumeclaims", func() {
		r.pvcs = readRemoteSection(h, ctx, clusterID, user, "persistentvolumeclaims", func(ctx context.Context, opts metav1.ListOptions) ([]*corev1.PersistentVolumeClaim, string, error) {
			l, err := cs.CoreV1().PersistentVolumeClaims("").List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
	})
	// Read as the user, unlike the local path's service-account informer, so
	// a denied StorageClass check skips storage (KTD4 difference 3).
	h.goSection(g, clusterID, "storageclasses", func() {
		r.storageClasses = readRemoteSection(h, ctx, clusterID, user, "storageclasses", func(ctx context.Context, opts metav1.ListOptions) ([]*storagev1.StorageClass, string, error) {
			l, err := cs.StorageV1().StorageClasses().List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointersTo(l.Items), l.Continue, nil
		})
	})
	h.goSection(g, clusterID, "certificates", func() {
		r.certificates = remoteCertOutcome(h, ctx, clusterID, user)
	})
	h.goSection(g, clusterID, "controlPlane", func() {
		r.controlPlane = remoteControlPlaneOutcome(h, ctx, clusterID)
	})
	return r
}

// outcomes maps every remote read to its health outcome. nodes, pods and
// alerts are the reads the tiles were drawn from (R8).
func (r *remoteHealthReads) outcomes(nodes remoteSection[*corev1.Node], pods remoteSection[*corev1.Pod], alerts remoteAlerts) healthOutcomes {
	return healthOutcomes{
		nodes:          listOutcome(nodes),
		deployments:    listOutcome(r.deployments),
		statefulSets:   listOutcome(r.statefulSets),
		daemonSets:     listOutcome(r.daemonSets),
		pods:           listOutcome(pods),
		pdbs:           listOutcome(r.pdbs),
		pvcs:           listOutcome(r.pvcs),
		storageClasses: listOutcome(r.storageClasses),
		alerts:         remoteAlertsOutcome(alerts),
		certificates:   r.certificates,
		controlPlane:   r.controlPlane,
	}
}

// listOutcome maps one list section to its outcome: denied when the
// permission check refused it, failed with its cause when an allowed read
// did not complete (a refusal by the API server included), and ok only for
// a complete read. A section whose worker never reported (a recovered
// panic) is failed as not read.
func listOutcome[T any](s remoteSection[T]) healthOutcome[[]T] {
	var o healthOutcome[[]T]
	switch {
	case s.coverage.Section == "":
		o.setFailed(healthCauseNotRead)
	case s.denied:
		o.setDenied()
	case s.cause != "":
		o.setFailed(s.cause)
	default:
		o.setOK(s.items)
	}
	return o
}

// remoteAlertsOutcome maps the alerts section's one read to its outcome. A
// missing Alertmanager is skipped with the local reason; every failure of an
// allowed read fails the score.
func remoteAlertsOutcome(a remoteAlerts) healthOutcome[alertCounts] {
	var o healthOutcome[alertCounts]
	switch {
	case a.err == nil:
		o.setOK(alertCounts{active: a.counts.Active, critical: a.counts.Critical})
	case errors.Is(a.err, errRemoteAlertsDenied):
		o.setDenied()
	case alertmanagerNotConfigured(a.err):
		o.setNoSource("alerting not configured")
	case errors.Is(a.err, errRemoteAlertsNotRead):
		o.setFailed(healthCauseNotRead)
	case errors.Is(a.err, errRemoteAlertsAuthzUnknown):
		o.setFailed(healthCauseAuthzUnknown)
	case a.timedOut:
		o.setFailed(healthCauseTimedOut)
	default:
		o.setFailed(classifyAlertmanagerError(a.err).cause)
	}
	return o
}

// remoteCertOutcome reads the cluster's expiring certificates as the user.
// Not installed is skipped and a denial (the permission check, or any
// cert-manager list refused) is skipped, as locally. Any other incomplete
// read fails the score, where local skips it (KTD4 difference 1), including
// a cold snapshot whose fetch outlasted the budget (KTD11).
func remoteCertOutcome(h *Handler, ctx context.Context, clusterID string, user *auth.User) healthOutcome[int] {
	var o healthOutcome[int]
	if h.CertExpiry == nil {
		o.setNoSource("cert-manager not configured")
		return o
	}
	expiry, err := h.CertExpiry.ExpiringCountsForCluster(ctx, clusterID, user)
	if err != nil {
		expiry = CertExpiry{State: CertExpiryIncomplete, Cause: err}
	}
	switch expiry.State {
	case CertExpiryComplete:
		o.setOK(expiry.Critical)
	case CertExpiryNotInstalled:
		o.setNoSource("cert-manager not installed")
	case CertExpiryDenied:
		o.setDenied()
	default:
		cause := healthCauseListFailed
		if readTimedOut(ctx, expiry.Cause) || errors.Is(expiry.Cause, ErrCertCacheNotWarm) {
			cause = healthCauseTimedOut
		}
		if h.Logger != nil {
			h.Logger.Warn("remote dashboard: read certificate expiry", "cluster", clusterID, "error", expiry.Cause)
		}
		o.setFailed(cause)
	}
	return o
}

// remoteControlPlaneOutcome queries the control plane through the cluster's
// metrics binding under the request's budget (KTD11). Without a binding the
// signal is skipped, as locally without Prometheus; a query error or timeout
// with a binding fails the score, where local skips it (KTD4 difference 2).
func remoteControlPlaneOutcome(h *Handler, ctx context.Context, clusterID string) healthOutcome[ControlPlaneStates] {
	var o healthOutcome[ControlPlaneStates]
	if h.ControlPlane == nil {
		o.setNoSource("")
		return o
	}
	states, err := h.ControlPlane.ControlPlaneStatus(ctx, clusterID)
	switch {
	case err == nil:
		o.setOK(states)
	case errors.Is(err, ErrNoMetricsBinding):
		o.setNoSource("")
	case readTimedOut(ctx, err):
		o.setFailed(healthCauseTimedOut)
	default:
		if h.Logger != nil {
			h.Logger.Warn("remote dashboard: query control-plane status from the cluster's Prometheus", "cluster", clusterID, "error", err)
		}
		o.setFailed(healthCauseQueryFailed)
	}
	return o
}

// readTimedOut reports a read that ended because the request's budget did.
// It is called right after the read returns, before a later expiry could
// misclassify an unrelated failure.
func readTimedOut(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
}

// remoteHealth assembles the outcomes under the local scoring rules. With
// every input complete it returns the score and an ok row: a cluster with
// zero visible nodes is Health{unknown}, as locally (KTD5). Otherwise Health
// is nil and the row is unavailable, its detail naming every missing input
// and its cause.
func remoteHealth(o healthOutcomes) (*ClusterHealth, SectionCoverage) {
	in, missing := assembleHealthInputs(o)
	if len(missing) == 0 {
		health := computeClusterHealth(in)
		return &health, SectionCoverage{Section: "health", Status: coverageOK, ReasonCode: reasonOK, ObservedAt: observedNow()}
	}
	parts := make([]string, len(missing))
	for i, m := range missing {
		parts[i] = healthInputLabels[m.input] + " (" + string(m.cause) + ")"
	}
	return nil, SectionCoverage{
		Section: "health", Status: coverageUnavailable,
		ReasonCode: missingHealthReason(missing),
		Detail:     detailHealthMissing + strings.Join(parts, ", "),
	}
}

// missingHealthReason picks the health row's reason code (KTD5): forbidden
// only when every missing input was refused by the API server, otherwise the
// code of the first other missing input, in signal order.
func missingHealthReason(missing []missingHealthInput) string {
	for _, m := range missing {
		if m.cause != healthCauseRefused {
			return healthCauseReason(m.cause)
		}
	}
	return reasonForbidden
}

// healthCauseReason is the reason code for one cause class. A truncated read
// is unsupported_platform: past the remote list cap there is no remote score
// at all, which is not an outage.
func healthCauseReason(c healthCause) string {
	switch c {
	case healthCauseRefused:
		return reasonForbidden
	case healthCauseAuthzUnknown:
		return reasonAuthzUnknown
	case healthCauseTruncated:
		return reasonUnsupportedPlatform
	default:
		return reasonUnreachable
	}
}
