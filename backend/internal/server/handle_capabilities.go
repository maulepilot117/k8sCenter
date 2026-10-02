package server

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// U8 — per-cluster, per-identity capability disclosure.
//
// GET /api/v1/capabilities/{clusterID} answers "what can I actually do
// against cluster X right now?" with six independent dimensions per
// operation (D3), so a UI can distinguish "k8sCenter never implemented this
// for remote" from "the cluster is down" from "you personally lack RBAC"
// instead of collapsing all three into one "unsupported" boolean.
//
// See docs/plans/2026-09-10-release-c-remote-workflow-impl.md unit U8 and
// .superpowers/sdd/2026-09-10-release-c-remote-workflow-impl/u8-brief.md for
// the full spec this file implements, including the controller amendments
// (A1: CanAccessGroupResource, not CanAccess; A2: discoveryPresent is null
// for every operation except dashboard.summary/eso.write; A3: target
// resolution failure is 200-with-reason-rows, not 503; A4: nil ClusterStore
// / ClusterRouter must not panic).

// ReasonCode and its values live in package k8s, which also classifies
// target-resolution errors, so feature handlers report remote failures
// with the same closed set this endpoint discloses.
type ReasonCode = k8s.ReasonCode

const (
	ReasonOK                   = k8s.ReasonOK
	ReasonUnsupportedPlatform  = k8s.ReasonUnsupportedPlatform
	ReasonDiscoveryMissing     = k8s.ReasonDiscoveryMissing
	ReasonDiscoveryUnavailable = k8s.ReasonDiscoveryUnavailable
	ReasonUnreachable          = k8s.ReasonUnreachable
	ReasonStaleObservation     = k8s.ReasonStaleObservation
	ReasonForbidden            = k8s.ReasonForbidden
	ReasonAuthzUnknown         = k8s.ReasonAuthzUnknown
	ReasonAuthzNamespaceScoped = k8s.ReasonAuthzNamespaceScoped
	ReasonClusterUnknown       = k8s.ReasonClusterUnknown
	ReasonCredentialsInvalid   = k8s.ReasonCredentialsInvalid
	ReasonDBUnavailable        = k8s.ReasonDBUnavailable
)

// validReasonCodes is the closed set every emitted ReasonCode must belong
// to. This is a hand-maintained map literal, deliberately: a new ReasonXyz
// constant does NOT join it automatically, and that manual step is exactly
// what gives TestCapabilities_ReasonCodesAreClosed its teeth — a forgotten
// entry here is a real gap the test can catch, not busywork to eliminate.
// Do not "simplify" this into something reflection- or iota-derived from the
// const block; that would make the closed-set check vacuously pass no
// matter what buildCapability actually emits.
var validReasonCodes = map[ReasonCode]bool{
	ReasonOK:                   true,
	ReasonUnsupportedPlatform:  true,
	ReasonDiscoveryMissing:     true,
	ReasonDiscoveryUnavailable: true,
	ReasonUnreachable:          true,
	ReasonStaleObservation:     true,
	ReasonForbidden:            true,
	ReasonAuthzUnknown:         true,
	ReasonAuthzNamespaceScoped: true,
	ReasonClusterUnknown:       true,
	ReasonCredentialsInvalid:   true,
	ReasonDBUnavailable:        true,
}

// Capability is one operation's six-dimension capability row (D3).
// platformSupported/discoveryPresent/reachable/authorized are deliberately
// never collapsed into a single boolean — see the package doc comment above.
type Capability struct {
	Operation         string `json:"operation"`
	Label             string `json:"label"`
	PlatformSupported bool   `json:"platformSupported"`
	DiscoveryPresent  *bool  `json:"discoveryPresent"`
	Reachable         *bool  `json:"reachable"`
	// Authorized is this identity's SelfSubjectAccessReview verdict for the
	// operation's representative verb/group/resource, or nil when it could
	// not be determined.
	//
	// The probe is issued CLUSTER-WIDE (empty SAR namespace). For a
	// cluster-scoped operation that is the whole truth: allowed true means
	// authorized, allowed false means forbidden. For a NAMESPACED operation
	// it is only half the truth — an empty namespace on a namespaced
	// resource asks "in every namespace?", so a true answer genuinely
	// implies every namespace, while a false answer proves nothing: an
	// identity holding an ordinary namespaced Role (edit/admin in one
	// namespace) is denied cluster-wide yet can perform the operation where
	// it actually works. Reporting that as authorized: false / forbidden
	// told every namespace-scoped user they could not do things they could
	// (review findings #2, #6, #10 — the last being the
	// `resources: ["*/exec"]` wildcard grant form, which only matches a SAR
	// carrying a non-empty subresource). A negative cluster-wide probe on a
	// namespaced operation is therefore reported as authorized: nil —
	// unknown, not denied — with reasonCode authz_namespace_scoped, which is
	// deliberately NOT authz_unknown: the latter means the SAR could not be
	// evaluated at all, and a client that cannot tell the two apart cannot
	// tell "ask me again with a namespace" from "the cluster would not
	// answer".
	//
	// Accepting an optional ?namespace= parameter and probing that namespace
	// would turn the unknown into a definite per-namespace yes/no. That is
	// the deliberate follow-up option, not an oversight — it changes the
	// endpoint's request contract (and the TypeScript client's), which this
	// unit's brief fixes, so it belongs in the unit that also updates the
	// consumers rather than being smuggled in here.
	Authorized *bool `json:"authorized"`
	// ObservedAt is the RFC3339 timestamp of the REACHABILITY observation
	// this row is based on — "now" for local (no probe cycle is involved)
	// and the cluster record's LastProbedAt for remote.
	//
	// It is NOT the weakest-freshness input contributing to the row, and
	// must not be described as one (review finding #14): `authorized` can
	// come from a SelfSubjectAccessReview verdict cached for up to 60s
	// (accessCacheTTL, internal/k8s/resources/access.go), so on the local
	// path ObservedAt reads "now" while the authorization dimension beside
	// it may be a minute old. frontend/lib/capability-types.ts documents the
	// same thing on the same field; keep the two in step.
	ObservedAt string     `json:"observedAt"`
	ReasonCode ReasonCode `json:"reasonCode"`
}

// CapabilitiesResponse is the body of GET /api/v1/capabilities/{clusterID}.
type CapabilitiesResponse struct {
	ClusterID    string       `json:"clusterId"`
	Capabilities []Capability `json:"capabilities"`
}

// gvrProbe names the group/resource whose presence in discovery must be
// checked for an operation. Only two operations have one — see A2.
type gvrProbe struct {
	Group    string
	Resource string
}

// capabilityOp is one row of the compile-time operation table. LocalSupported
// / RemoteSupported are static per-operation-class facts; everything else
// about a request (reachability, discovery, RBAC) is resolved per request.
type capabilityOp struct {
	ID              string
	Label           string
	LocalSupported  bool
	RemoteSupported bool
	// Probe is the GVR whose presence in discovery must be checked, or nil
	// when the operation has no fixed GVR (A2 — the yaml.* operations act on
	// arbitrary user-supplied documents and have no single resource to probe).
	Probe *gvrProbe
	// AuthVerb/AuthGroup/AuthResource are the representative
	// verb/apiGroup/resource passed to AccessChecker.CanAccessGroupResource
	// for the authorized dimension (A1 — CanAccessGroupResource, never
	// CanAccess, so a predicate-fake AccessChecker in tests can't short-
	// circuit to an unconditional allow).
	AuthVerb     string
	AuthGroup    string
	AuthResource string
	// ClusterScoped marks AuthResource as a cluster-scoped resource (nodes,
	// storageclasses), as opposed to a namespaced one (configmaps, pods, pods/exec, pods/log,
	// externalsecrets). It exists solely to decide how a NEGATIVE
	// cluster-wide SAR verdict is reported — see Capability.Authorized and
	// authorizedFromClusterWideSAR.
	//
	// The zero value means namespaced deliberately: most Kubernetes
	// resources are, and a forgotten field then errs toward
	// authz_namespace_scoped ("the probe's shape makes this unknowable")
	// rather than toward a definite-but-wrong forbidden.
	// TestCapabilityOperations_ScopePinned
	// pins the scope of every row by ID so a new row still has to make the
	// choice consciously.
	ClusterScoped bool
}

// capabilityOperations is the package-level operation table (implementation
// step 1). Each "yes" row's comment names what makes remote support real, and
// each "no" row's comment cites the exact guard file:line it mirrors,
// re-verified against the tree at 887174c6, so a reviewer can diff the claim
// against the code instead of trusting prose. TestCapabilityOperations_
// RemoteSupportPinned pins the exact remote-supported set, so flipping a row
// without updating that test (and this comment) fails loudly.
var capabilityOperations = []capabilityOp{
	{
		// Remote since U9a (#493): every yaml.* handler resolves its client
		// AND its RESTMapper from the request's X-Cluster-ID in one
		// ClusterRouter.TargetFor call (yaml/handler.go resolveTarget), so a
		// dry-run executes against, and resolves kinds from, the cluster it
		// names. There is no remote rejection left in yaml/handler.go and no
		// local fallback: a remote target that cannot be resolved is an error.
		//
		// AuthResource: dry-run apply's SAR check is identical to a real
		// apply — Kubernetes' dryRun flag only skips persistence, it does
		// NOT relax RBAC — so the representative verb matches yaml.apply's
		// below rather than a weaker read-only probe. configmaps (core) is
		// the representative resource for all four yaml.* rows: it is a
		// near-universal namespaced resource present in ordinary
		// edit/admin-shaped roles without requiring the elevated access
		// Secrets carry (which this operation refuses outright — see the
		// diff/export rows below) or the all-resources wildcard's
		// pathologically strict match semantics (a wildcard REQUESTED
		// group/resource only matches an RBAC rule that itself literally
		// contains "*", so "*"/"*" made ordinary namespace-scoped editors
		// come back forbidden — task review round 1, finding #2).
		//
		// Choosing a resource ordinary namespaced roles carry only pays off
		// if the probe also asks a namespaced question. It does not: the SAR
		// namespace is empty (cluster-wide), which is why a negative here is
		// reported authz_namespace_scoped rather than forbidden — see
		// Capability.Authorized and capabilityOp.ClusterScoped.
		ID: "yaml.validate", Label: "Validate YAML",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "patch", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// Remote since U9a (#493) — same target-scoped resolution as
		// yaml.validate above. Secrets refused on both classes (enforced
		// inside the yaml handler, not here). AuthVerb "patch", not "get"
		// (task review round 2, finding #2a): differ.go:101 does call
		// dr.Get(...) first, but differ.go:118 then does dr.Patch(...,
		// types.ApplyPatchType, data, DryRun: []string{metav1.DryRunAll}) to
		// compute the proposed state — and Kubernetes authorizes a dry-run
		// patch exactly as it authorizes a real one (dryRun skips
		// persistence, not authorization), same reasoning as yaml.validate
		// above. Diff needs BOTH get and patch; probing "patch" is the
		// representative choice because it is the verb more likely to be
		// denied, so it is the one that actually carries information —
		// probing "get" would report authorized: true for a viewer who lacks
		// patch and then hits Forbidden inside the real call. AuthResource:
		// configmaps — see yaml.validate's comment for why (finding #2).
		ID: "yaml.diff", Label: "Diff YAML against live state",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "patch", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// Remote since U9a (#493) — same target-scoped resolution as
		// yaml.validate above, so a CRD that exists only remotely is
		// exportable. Secrets refused on both classes. AuthVerb "get", not
		// "list" (task review round 2, finding #2b): handler.go:263 requires
		// kind AND name to be non-empty (400 otherwise) — export always
		// fetches ONE named object, never a list — and the only client calls
		// in the export path are the two dynClient...Get(...) calls at
		// handler.go:317/319. There is no .List anywhere in it.
		// AuthResource: configmaps (finding #2).
		ID: "yaml.export", Label: "Export YAML",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "get", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// Remote since U9b (#494): server-side apply runs against the
		// resolved target, and an apply carrying the preview's
		// targetCluster/targetGeneration pin is refused with 409 before any
		// document is applied if the request's cluster or that cluster's
		// registration generation no longer matches (yaml/handler.go
		// HandleApply, refusePin). AuthVerb "patch": server-side apply for
		// all YAML operations is a PATCH (application/apply-patch+yaml — see
		// CLAUDE.md's backend architecture principles). AuthResource:
		// configmaps (finding #2).
		ID: "yaml.apply", Label: "Apply YAML",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "patch", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// Remote since U10 (#495), but ONLY on the opt-in
		// GET /cluster/dashboard-summary?coverage=1 path
		// (k8s/resources/dashboard.go HandleDashboardSummary →
		// dashboard_remote.go handleRemoteDashboardSummary). Without the
		// parameter a remote request still gets the legacy 400 that mobile's
		// DashboardLocalOnlyError matches, so "supported" here describes the
		// contract the web client uses (it always sends ?coverage=1 for a
		// remote cluster). The remote response is partial by design: it
		// carries per-section coverage and its health is always null — the
		// health score is never computed remotely. Probed against core/v1
		// nodes (A2) since the summary's node section is what a hardened
		// cluster is most likely to have hidden from discovery, on either
		// target class.
		//
		// The ONLY cluster-scoped row in the table: nodes are not namespaced,
		// so the cluster-wide SAR below is an exact question and a denial is
		// a real denial (forbidden), not the ambiguous namespaced negative
		// every other row has to report as authz_namespace_scoped.
		ID: "dashboard.summary", Label: "Dashboard summary",
		LocalSupported: true, RemoteSupported: true,
		Probe:    &gvrProbe{Group: "", Resource: "nodes"},
		AuthVerb: "list", AuthGroup: "", AuthResource: "nodes",
		ClusterScoped: true,
	},
	{
		// Resource counts rely on the local informer cache; remote clusters
		// use direct API calls and do not populate informers.
		// k8s/resources/counts.go:28 (400).
		ID: "resources.counts", Label: "Resource counts",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Pod exec requires an SPDY stream upgrade against the target
		// cluster's own API server, not yet supported for remote clusters.
		// k8s/resources/pods.go:164 (501).
		//
		// Subresource shape: CanAccess splits "pods/exec" into
		// Resource:"pods", Subresource:"exec" (access.go:137-143);
		// CanAccessGroupResource (A1) sends "pods/exec" verbatim with an
		// empty Subresource. These agree under the RBAC authorizer's
		// RuleAllows, which reconstructs the combined resource by
		// concatenation — EXCEPT for the `resources: ["*/exec"]` wildcard
		// grant form, which ResourceMatches only honors when the requested
		// subresource is non-empty. An identity granted exec only that way
		// comes back Allowed: false from the SAR. That used to be reported
		// as a definite forbidden (review finding #10); because pods/exec is
		// namespaced, it is now reported as authz_namespace_scoped — the honest
		// answer for a negative that the probe shape itself may have
		// manufactured. A1 leaves no alternative without extending
		// AccessChecker's API.
		ID: "pod.exec", Label: "Pod exec",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "create", AuthGroup: "", AuthResource: "pods/exec",
	},
	{
		// WebSocket log streams against remote clusters are not yet
		// supported — the watch connection lifecycle differs for remote API
		// servers. handle_ws_logs.go:103.
		//
		// Same "pods/log" subresource caveat as pod.exec above: an identity
		// granted access only via a `resources: ["*/log"]` wildcard rule
		// comes back Allowed: false and is reported authz_namespace_scoped (pods/log
		// is namespaced) rather than forbidden — see pod.exec's comment.
		ID: "logs.stream", Label: "Live log stream",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "get", AuthGroup: "", AuthResource: "pods/log",
	},
	{
		// Loki tail always targets the LOCAL cluster's Loki service; a
		// remote X-Cluster-ID would stream local logs under the remote
		// cluster's name (confused deputy). handle_ws_logs_search.go:60.
		ID: "logs.search", Label: "Log search",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Hubble flow streaming targets the LOCAL cluster's CNI data plane
		// for the same confused-deputy reason as logs.search.
		// handle_ws_flows.go:61.
		ID: "flows.stream", Label: "Network flow stream",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Phase E ESO writes don't route through ClusterRouter — the
		// dynamic client always points at the local ClientFactory — so
		// honoring X-Cluster-ID would desync the audit row from the actual
		// mutation. R-8 keeps this carve-out (R12) while ESO reads went
		// remote (eso.read below). externalsecrets/actions.go:144
		// (rejectNonLocalClusterWrite, 501; called from actions.go:182 and
		// bulk.go:81/109/137/308, which also refuse the bulk-refresh scope
		// previews). Probed against
		// external-secrets.io/externalsecrets (A2) since ESO is an optional
		// CRD-based operator that may not be installed even locally.
		ID: "eso.write", Label: "External Secrets write actions",
		LocalSupported: true, RemoteSupported: false,
		Probe:    &gvrProbe{Group: "external-secrets.io", Resource: "externalsecrets"},
		AuthVerb: "patch", AuthGroup: "external-secrets.io", AuthResource: "externalsecrets",
	},

	// R-8 rows (docs/plans/2026-09-29-0908-fix-r8-remote-cluster-routing-plan.md
	// U13). Each package below resolves every per-user call through
	// ClusterRouter for the request's X-Cluster-ID, reads remote lists
	// through a per-identity remotecache snapshot, and is listed in
	// scripts/check-cluster-routing.sh REMOTE_ROUTED_DIRS, so the lint flags
	// any new local-schema or service-account read in it. None carries a
	// Probe: each feature's own status route reports whether its CRDs are
	// installed on the target (KTD5), and A2 keeps discoveryPresent to the
	// two rows above. The representative AuthResource is the feature's
	// primary list; the handlers still check each resource they touch.
	{
		// Remote since U1 (#510): the drain resolves its clientset for the
		// request's cluster before the 202 and runs detached from the request
		// on that client (k8s/resources/nodes.go HandleDrainNode ->
		// clientForCluster). AuthVerb "update" nodes mirrors the handler's
		// own checkAccess. Cluster-scoped: nodes are not namespaced, so a
		// cluster-wide denial is a real one.
		ID: "node.drain", Label: "Node drain",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "update", AuthGroup: "", AuthResource: "nodes",
		ClusterScoped: true,
	},
	{
		// Remote since U5 (#515): Argo CD and Flux lists, detail, sync,
		// suspend and rollback run on the selected cluster (gitops/remote.go).
		// A remote list whose Argo or Flux read failed names that source in
		// its coverage field. Argo CD Applications stand in for both tools.
		ID: "gitops.applications", Label: "GitOps applications and sync",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "argoproj.io", AuthResource: "applications",
	},
	{
		// Remote since U6 (#516): backups, restores, schedules, locations and
		// their actions run on the selected cluster (velero/remote.go),
		// including the delete-backup in-progress-restore check, which fails
		// closed when the remote restore list cannot be read.
		ID: "velero.backups", Label: "Velero backups and restores",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "velero.io", AuthResource: "backups",
	},
	{
		// Remote since U7 (#517): VolumeSnapshot list, detail, create and
		// delete run on the selected cluster (storage/handler.go).
		ID: "storage.snapshots", Label: "Volume snapshots",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "snapshot.storage.k8s.io", AuthResource: "volumesnapshots",
	},
	{
		// Remote since #533: the CSI driver and StorageClass lists are read
		// from the selected cluster as the user, behind the per-identity
		// remote cache (storage/remote.go loadRemoteDrivers,
		// loadRemoteClasses), instead of the local informers. StorageClasses
		// stand in for both lists. Cluster-scoped: StorageClasses are not
		// namespaced, so a cluster-wide denial is a real one.
		ID: "storage.classes", Label: "CSI drivers and StorageClasses",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "storage.k8s.io", AuthResource: "storageclasses",
		ClusterScoped: true,
	},
	{
		// Remote since U8 (#518): Flux notification Providers, Alerts and
		// Receivers are read and written on the selected cluster
		// (notification/remote.go). Providers and Alerts are read and
		// written at v1beta3, or at v1beta2 on a remote that serves only
		// that (Flux 2.0, #534).
		ID: "flux.notifications", Label: "Flux notifications",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "notification.toolkit.fluxcd.io", AuthResource: "providers",
	},
	{
		// Remote since U9 (#519): PrometheusRule object CRUD on the selected
		// cluster (alerting/rules.go). This is the rule objects only (KTD13):
		// whether they fire depends on the target running
		// prometheus-operator, and the active and history alert feeds stay on
		// the local Alertmanager.
		ID: "alert.rules", Label: "Alert rules",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "monitoring.coreos.com", AuthResource: "prometheusrules",
	},
	{
		// Remote since U4 (#514): GatewayClasses, Gateways and every route
		// kind are read from the selected cluster (gateway/handler.go).
		// Gateway API has no write actions in k8sCenter.
		ID: "gateway.read", Label: "Gateway API views",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "gateway.networking.k8s.io", AuthResource: "gateways",
	},
	{
		// Remote since U10 (#520): Istio and Linkerd routing lists and route
		// detail come from the selected cluster (servicemesh/remote.go), with
		// mesh presence detected per cluster. The topology mesh overlay stays
		// local. Istio VirtualServices stand in for both meshes.
		ID: "mesh.routing", Label: "Service mesh routing",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "networking.istio.io", AuthResource: "virtualservices",
	},
	{
		// Remote since U10 (#520), in part: posture is derived from the
		// selected cluster's pods and policies, but the Prometheus metric
		// cross-check is skipped and reported unavailable
		// (servicemesh/handler.go HandleMTLSPosture,
		// crossCheckUnavailableRemote). The README marks it Partial.
		// AuthResource pods: posture is computed per pod.
		ID: "mesh.mtls", Label: "Service mesh mTLS posture",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Remote since U17 (#523): ExternalSecret, ClusterExternalSecret,
		// store and PushSecret lists and detail, and path discovery, read the
		// selected cluster (externalsecrets/remote.go). The drift column is
		// "unknown" on remote: the drift hint comes from the local poller.
		ID: "eso.read", Label: "External Secrets views",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "external-secrets.io", AuthResource: "externalsecrets",
	},
	{
		// Remote since #531: Certificate, Issuer and ClusterIssuer lists, the
		// expiring list, detail, renew and re-issue run on the selected
		// cluster (certmanager/remote.go), with cert-manager presence read
		// from that cluster's own discovery. The expiry poller and its
		// notifications stay on the local cluster.
		ID: "certmanager.certificates", Label: "cert-manager certificates and issuers",
		LocalSupported: true, RemoteSupported: true,
		AuthVerb: "list", AuthGroup: "cert-manager.io", AuthResource: "certificates",
	},
	{
		// Cilium config is tied to the local installation, so reads and
		// updates are refused on remote (R13, the P2-5 decision).
		// networking/handler.go:171 and :234 (rejectNonLocal, 501). The
		// config lives in the cilium-config ConfigMap.
		ID: "cni.config", Label: "Cilium CNI configuration",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "get", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// Golden signals come from the local Prometheus, which knows nothing
		// of a remote cluster's traffic (R14). servicemesh/handler.go:834
		// (HandleGoldenSignals answers with reason unsupported_platform and
		// queries nothing). AuthVerb "list" pods mirrors the handler's own
		// namespace check.
		ID: "mesh.golden_signals", Label: "Service mesh golden signals",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Sync history and evidence events are recorded by the local poller
		// for the local cluster only (R14).
		// externalsecrets/history_handler.go:157 (501
		// remote_history_unsupported) and detail_evidence.go:173 (501
		// remote_events_unsupported).
		ID: "eso.history", Label: "External Secrets sync history",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "get", AuthGroup: "external-secrets.io", AuthResource: "externalsecrets",
	},
	{
		// Store metrics join the local ESO cache with the local Prometheus
		// (R14). externalsecrets/metrics.go:75 (answers "rate metrics
		// unavailable on remote clusters" before any client is resolved).
		ID: "eso.metrics", Label: "External Secrets store metrics",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "get", AuthGroup: "external-secrets.io", AuthResource: "secretstores",
	},
	{
		// The graph is built from the local cluster's informers, which
		// remote clusters do not have (#532). topology/handler.go
		// HandleNamespaceGraph answers 501 unsupported_platform before the
		// builder reads anything. AuthResource pods stands in for the
		// per-kind list checks the builder makes on each node it adds.
		ID: "topology.graph", Label: "Resource topology graph",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Diagnostics resolve the target, its related pods and the
		// blast-radius graph from the local cluster's informers (#532).
		// diagnostics/handler.go refuseRemote answers 501
		// unsupported_platform for both diagnostics routes before any read,
		// SAR or notification. AuthResource pods mirrors the namespace
		// summary's own check.
		ID: "diagnostics.read", Label: "Resource diagnostics and blast radius",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
}

// supportedFor returns the static platformSupported value for this
// operation on the given target class.
func (op capabilityOp) supportedFor(isLocal bool) bool {
	if isLocal {
		return op.LocalSupported
	}
	return op.RemoteSupported
}

// staleAfter is the D3 staleness threshold: 3x the 60s ClusterProber
// interval. A probe older than this makes reachable null rather than a
// (possibly very stale) true/false.
const staleAfter = 3 * 60 * time.Second

// clusterRecordGetter is the subset of *store.ClusterStore the reachability
// dimension needs — mirrors internal/k8s's clusterGetter (cluster_router.go)
// so the computation is unit-testable with a fake instead of requiring a
// live PostgreSQL-backed *store.ClusterStore, which is a concrete type and
// cannot otherwise be faked from outside the store package.
type clusterRecordGetter interface {
	Get(ctx context.Context, id string) (*store.ClusterRecord, error)
}

// reachabilityResult is the outcome of resolving the `reachable` dimension.
// It is the same for every operation in one response (reachability does not
// vary per-operation), so it is computed once per request and reused.
type reachabilityResult struct {
	// reachable is nil when unknown/stale, non-nil true/false otherwise.
	reachable *bool
	// observedAt is when this observation was made — "now" for local (no
	// probe cycle involved) and the record's LastProbedAt for remote. It
	// describes the reachability observation only; see Capability.ObservedAt
	// for why that is NOT the row's weakest-freshness input.
	observedAt time.Time
	// reason, when non-empty, says WHY reachable is nil. Empty means the
	// ordinary staleness case (buildCapability then reports
	// stale_observation). A registry read that failed sets db_unavailable
	// here instead: "the database is down" and "the prober is lagging" are
	// different operator actions — investigate vs wait — and collapsing the
	// first into the second (review finding #1) sends the operator to wait
	// out an outage that will never clear on its own.
	reason ReasonCode
}

// resolveReachability computes the `reachable` dimension (step 2e). Local is
// trivially true/now and needs no store. Remote reads Status + LastProbedAt
// from the cluster record and applies the 3x60s staleness rule (D3
// stale_observation). A never-probed cluster (LastProbedAt nil) is treated
// the same as a stale one: unknown, not false.
func resolveReachability(ctx context.Context, cs clusterRecordGetter, isLocal bool, clusterID string, now time.Time) reachabilityResult {
	if isLocal {
		return reachabilityResult{reachable: boolPtr(true), observedAt: now}
	}
	if cs == nil {
		// No cluster registry to read the observation from at all. Distinct
		// from a stale probe for the same reason the failed read below is.
		return reachabilityResult{observedAt: now, reason: ReasonDBUnavailable}
	}
	rec, err := cs.Get(ctx, clusterID)
	if err != nil || rec == nil {
		// Reachability itself could not be determined: the registry read
		// failed (a Postgres outage after target resolution had already
		// succeeded from cache), or a clusterRecordGetter — real or a test
		// fake — returned (nil, nil). reachable stays nil, never a guessed
		// false, and the reason is db_unavailable rather than
		// stale_observation (review finding #1): a lagging prober resolves
		// itself, a registry outage does not, and the operator must be able
		// to tell which one they are looking at.
		//
		// The real *store.ClusterStore always returns (nil, err) on failure,
		// so rec == nil with a nil error can't fire in production; the check
		// exists so a fake returning (nil, nil) can't dereference a nil rec
		// below.
		return reachabilityResult{observedAt: now, reason: ReasonDBUnavailable}
	}
	if rec.LastProbedAt == nil || now.Sub(*rec.LastProbedAt) > staleAfter {
		at := now
		if rec.LastProbedAt != nil {
			at = *rec.LastProbedAt
		}
		return reachabilityResult{observedAt: at}
	}
	// k8s.StatusConnected, not a bare "connected" literal: the
	// connected|disconnected|blocked|error vocabulary is owned by
	// internal/k8s/cluster_prober.go, which now exports it as typed
	// constants (review finding #11). store.ClusterRecord.Status is a plain
	// string column, so .String() is the comparison form — the same one
	// handle_clusters.go uses. Comparing against a literal here meant a
	// rename in the prober would silently mark every remote cluster
	// unreachable with no compile error.
	connected := rec.Status == k8s.StatusConnected.String()
	return reachabilityResult{reachable: &connected, observedAt: *rec.LastProbedAt}
}

// The target-error classifier and discovery helpers moved to package k8s
// (target_status.go); these names keep this file and its tests unchanged.
var (
	classifyTargetSchemaErr = k8s.ClassifyTargetErr
	fetchDiscoveryLists     = k8s.DiscoveryLists
	failedDiscoveryGroups   = k8s.FailedDiscoveryGroups
	gvrPresentIn            = k8s.GVRPresentIn
)

func boolPtr(b bool) *bool { return &b }

// truncateForEcho bounds s to n bytes before it is echoed back in an error
// response, marking that truncation happened rather than silently clipping
// — a truncated value with no marker would misrepresent what the caller
// actually sent. Used for the {clusterID} path parameter (handler.go's
// mismatch response), which has no upstream length cap the way the
// X-Cluster-ID header does (middleware.ClusterContext caps that at 64).
//
// n bounds BYTES, not runes — it exists to cap the response body — but the
// cut is pulled back to a rune boundary first. A plain s[:n] can land in the
// middle of a multi-byte UTF-8 sequence; encoding/json then rewrites those
// orphaned bytes as U+FFFD, so the echoed id silently differs from what the
// caller actually sent in a way "...(truncated)" does not explain, which is
// the one thing this helper exists to prevent. Trimming instead drops the
// partial rune, so every byte that IS echoed is a byte the caller sent.
func truncateForEcho(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	// DecodeLastRuneInString returns (RuneError, 1) for a byte that cannot
	// start or complete a rune — i.e. exactly the partial sequence the cut
	// created. A genuine U+FFFD in the input decodes with size 3 and is left
	// alone, so this trims the split only, never the caller's own content.
	for len(cut) > 0 {
		r, size := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut + "...(truncated)"
}

// buildCapability composes one operation's full six-dimension row from
// already-resolved inputs. It is the single place reasonCode priority is
// decided and holds no I/O of its own, so the priority rules — the exact
// thing AE2/AE3 depend on — are unit-tested directly with synthetic inputs
// rather than only reachable through a live cluster.
//
// Priority (highest first): unsupported_platform (static, always wins —
// whether k8sCenter implemented this for this target class doesn't depend on
// whether the target happens to be resolvable right now) > global target-
// resolution failure (cluster_unknown/credentials_invalid/db_unavailable —
// nothing else is knowable about ANY operation when the target itself
// can't be resolved) > unreachable/stale_observation > discovery_missing/
// discovery_unavailable > forbidden/authz_namespace_scoped/authz_unknown > ok.
//
// authReason is the caller's explanation for a nil `authorized` that is NOT
// an error: today the single value authorizedFromClusterWideSAR can produce,
// authz_namespace_scoped. It is a separate parameter rather than something
// inferred from (authorized == nil && authErr == nil) because that condition
// is also what "the caller never computed authz at all" looks like, and
// those two are exactly the pair this endpoint must not conflate.
func buildCapability(
	op capabilityOp,
	isLocal bool,
	globalReason ReasonCode,
	reach reachabilityResult,
	discoveryPresent *bool,
	discoveryUnavailable bool,
	authorized *bool,
	authErr error,
	authReason ReasonCode,
	now time.Time,
) Capability {
	row := Capability{
		Operation:         op.ID,
		Label:             op.Label,
		PlatformSupported: op.supportedFor(isLocal),
	}

	if !row.PlatformSupported {
		row.ReasonCode = ReasonUnsupportedPlatform
		row.ObservedAt = now.UTC().Format(time.RFC3339)
		return row
	}

	if globalReason != "" {
		row.ReasonCode = globalReason
		// A global reason means the TARGET could not be resolved, so nothing
		// downstream of it is knowable and reachable stays null — with one
		// exception. classifyTargetSchemaErr returns unreachable only for
		// errors that ARE a reachability verdict (DNS failure, dial refusal,
		// the 30s ceiling expiring), so leaving reachable null there shipped
		// a row that said reasonCode: unreachable, reachable: null: two
		// statements contradicting each other, and a different code path
		// below emits that same reason with reachable: false. cluster_unknown,
		// credentials_invalid and db_unavailable are NOT reachability
		// verdicts — we never got far enough to learn whether the target
		// answers — so they keep the honest null.
		if globalReason == ReasonUnreachable {
			row.Reachable = boolPtr(false)
		}
		row.ObservedAt = now.UTC().Format(time.RFC3339)
		return row
	}

	row.Reachable = reach.reachable
	if reach.reachable == nil {
		// stale_observation is the DEFAULT explanation for a null reachable,
		// not the only one: resolveReachability sets reach.reason when it
		// knows something more specific (db_unavailable for a registry it
		// could not read). Preferring it keeps "the prober is behind" and
		// "the database is down" distinct — finding #1.
		row.ReasonCode = ReasonStaleObservation
		if reach.reason != "" {
			row.ReasonCode = reach.reason
		}
		row.ObservedAt = reach.observedAt.UTC().Format(time.RFC3339)
		return row
	}
	if !*reach.reachable {
		row.ReasonCode = ReasonUnreachable
		row.ObservedAt = reach.observedAt.UTC().Format(time.RFC3339)
		return row
	}

	// Reachable — safe to report discovery + authz (both were only computed
	// by the caller when reach.reachable was true, so no live call was made
	// against a target already known to be down or unknown).
	row.ObservedAt = reach.observedAt.UTC().Format(time.RFC3339)

	var reason ReasonCode
	if op.Probe != nil {
		row.DiscoveryPresent = discoveryPresent
		switch {
		case discoveryUnavailable:
			reason = ReasonDiscoveryUnavailable
		case discoveryPresent != nil && !*discoveryPresent:
			reason = ReasonDiscoveryMissing
		}
	}

	row.Authorized = authorized
	if reason == "" {
		switch {
		case authErr != nil:
			// The SAR could not be issued or errored — we failed to ask.
			reason = ReasonAuthzUnknown
		case authorized != nil && !*authorized:
			reason = ReasonForbidden
		case authorized != nil && *authorized:
			reason = ReasonOK
		case authReason != "":
			// The SAR was asked and answered, but the answer does not settle
			// the question — today only authz_namespace_scoped.
			reason = authReason
		default:
			// authorized was never computed (caller skipped it) — treat as
			// unknown rather than guessing ok.
			reason = ReasonAuthzUnknown
		}
	}
	row.ReasonCode = reason
	return row
}

// authorizedFromClusterWideSAR turns a successful cluster-wide (empty
// namespace) SelfSubjectAccessReview verdict into the reported `authorized`
// dimension, plus the reason code that explains a nil one.
//
// buildCapability's contract is unchanged and deliberately dumb: a non-nil
// false means forbidden. Deciding whether a given false IS a denial is this
// function's job, because that depends on the shape of the question asked,
// which is a property of the probe, not of the reason-code priority chain.
//
// The three outcomes:
//
//   - allowed == true → (true, ""), on any scope. A cluster-wide allow
//     genuinely implies every namespace; buildCapability reports ok.
//   - allowed == false on a CLUSTER-SCOPED resource → (false, ""). The
//     question had no namespace dimension; the denial is the whole answer,
//     and buildCapability reports forbidden.
//   - allowed == false on a NAMESPACED resource → (nil,
//     authz_namespace_scoped). The probe asked "in ALL namespaces?"; an
//     identity with an ordinary namespaced Role answers no to that and yes
//     where it matters. authorized is nil because the verdict really is
//     indeterminate, and the reason code — NOT authz_unknown — says why. See
//     Capability.Authorized for the full rationale and the ?namespace=
//     follow-up.
//
// A SAR that ERRORS never reaches here — the caller keeps authorized nil and
// passes the error through, which buildCapability maps to authz_unknown by
// its own branch. That is the only thing authz_unknown means.
func authorizedFromClusterWideSAR(op capabilityOp, allowed bool) (*bool, ReasonCode) {
	if allowed || op.ClusterScoped {
		return boolPtr(allowed), ""
	}
	return nil, ReasonAuthzNamespaceScoped
}

// capabilityClusterGetter resolves the clusterRecordGetter the reachability
// dimension reads from. It is a package-level func var rather than a direct
// s.ClusterStore reference at the call site for exactly the reason
// clusterRecordGetter is an interface at all: *store.ClusterStore is a
// concrete type wrapping an unexported pgx pool, and internal/k8s exposes no
// seam for faking the router's copy of it either, so substituting a fake
// here is the only way a package-internal test can drive the handler's
// REMOTE chain (reachability → discovery → impersonated SAR) end to end
// through real HTTP, rather than only through direct resolveReachability
// and buildCapability calls.
//
// Production never reassigns it. It also normalizes the nil case: returning
// a nil *store.ClusterStore as a non-nil interface (the classic Go
// nil-interface trap) would hand resolveReachability something that panics
// on Get rather than a nil it can report db_unavailable for.
var capabilityClusterGetter = func(s *Server) clusterRecordGetter {
	if s.ClusterStore == nil {
		return nil
	}
	return s.ClusterStore
}

// handleClusterCapabilities answers GET /api/v1/capabilities/{clusterID}.
// See the package doc comment above for the full behavioral contract.
func (s *Server) handleClusterCapabilities(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	pathID := chi.URLParam(r, "clusterID")
	hdrID := middleware.ClusterIDFromContext(r.Context())
	// No length branch is needed here: middleware.ClusterContext already
	// caps the HEADER at 64 bytes (middleware/cluster.go) before this
	// handler ever runs, and k8s.NormalizedClusterID is an identity function
	// for non-local ids, so an over-64-byte pathID can never equal the
	// always-<=64-byte hdrID — the equality check below already catches it.
	// What IS unbounded is what gets echoed back: pathID has no such cap, so
	// it is truncated before going into extra.pathClusterId purely so this
	// 409 response body can't itself carry an arbitrarily long attacker-
	// supplied string.
	if k8s.NormalizedClusterID(pathID) != k8s.NormalizedClusterID(hdrID) {
		httputil.WriteErrorWithReason(w, http.StatusConflict, "cluster target mismatch",
			"cluster_target_mismatch", map[string]any{"pathClusterId": truncateForEcho(pathID, 64), "headerClusterId": hdrID})
		return
	}

	ctx := r.Context()
	normID := k8s.NormalizedClusterID(pathID)
	isLocal := k8s.IsLocalClusterID(normID)
	now := time.Now()

	// anySupported is whether ANY operation in the table is
	// supportedFor(isLocal). buildCapability checks !row.PlatformSupported
	// first and returns before globalReason/reach are ever consulted, so
	// when anySupported is false every row ends up unsupported_platform
	// regardless of what target resolution or reachability would have said,
	// and resolving them — a clusterStore.Get, a live DNS re-resolution and a
	// credential decrypt on TargetSchemaFor's remote miss path (30s ceiling),
	// plus resolveReachability's own clusterStore.Get — would be work whose
	// result is discarded.
	//
	// With the current table the gate is always open on both classes: every
	// row is LocalSupported, and yaml.validate/diff/export/apply and
	// dashboard.summary are RemoteSupported, so every remote request now
	// resolves the target, reads reachability and issues the impersonated
	// SARs for those rows (the still-unsupported rows skip that per-row work
	// in the loop below). The gate only bites for a table with no row
	// supported on the requested class; it is kept because it costs one
	// short loop and keeps such a table from paying for a live remote round
	// trip nobody reads. TestCapabilities_NoSupportedRowSkipsTargetResolution
	// pins it with a substituted table.
	anySupported := false
	for _, op := range capabilityOperations {
		if op.supportedFor(isLocal) {
			anySupported = true
			break
		}
	}

	var globalReason ReasonCode
	var targetSchema *k8s.TargetSchema

	if anySupported {
		switch {
		case !isLocal && s.ClusterStore == nil:
			// A4: no cluster registry wired at all — every row db_unavailable.
			globalReason = ReasonDBUnavailable
		case s.ClusterRouter == nil:
			// A4: ClusterRouter itself is nil (distinct from "no store" above).
			// Calling TargetSchemaFor on a nil receiver would panic on the local
			// branch (cr.localFactory) just as much as the remote one, so it is
			// never called here. This is deliberately narrow, NOT a global
			// failure: reachable still resolves from Server.ClusterStore and
			// authorized still resolves from ResourceHandler.AccessChecker,
			// which are independently wired. Only the discovery dimension is
			// affected, for the two operations that probe a GVR.
		default:
			var err error
			targetSchema, err = s.ClusterRouter.TargetSchemaFor(ctx, normID, user.KubernetesUsername, user.KubernetesGroups)
			if err != nil {
				globalReason = classifyTargetSchemaErr(err)
			}
		}
	}

	var reach reachabilityResult
	if anySupported && globalReason == "" {
		// s.ClusterStore may be nil here, but only when isLocal is true — the
		// switch above already routed the "non-local + nil ClusterStore" case
		// to globalReason=db_unavailable, and resolveReachability's local
		// branch returns before ever touching cs.
		reach = resolveReachability(ctx, capabilityClusterGetter(s), isLocal, normID, now)
	}

	// Live probes run ONLY when the target is known-reachable — attempting a
	// live discovery call against a target already reported down or unknown
	// would just duplicate a slow, possibly hanging network round-trip the
	// ClusterProber already owns.
	reachableNow := globalReason == "" && reach.reachable != nil && *reach.reachable
	probes := s.runCapabilityProbes(ctx, reachableNow, isLocal, targetSchema, normID, user.KubernetesUsername, user.KubernetesGroups)

	capabilities := make([]Capability, 0, len(capabilityOperations))
	for _, op := range capabilityOperations {
		platformSupported := op.supportedFor(isLocal)

		var discoveryPresent *bool
		discoveryUnavailable := false
		var authorized *bool
		var authErr error
		var authReason ReasonCode

		if platformSupported && globalReason == "" && reachableNow {
			if op.Probe != nil {
				present := !probes.discoveryUnavailable && gvrPresentIn(probes.discoveryLists, op.Probe.Group, op.Probe.Resource)
				switch {
				case probes.discoveryUnavailable:
					discoveryUnavailable = true
				case !present && probes.discoveryFailedGroups[op.Probe.Group]:
					// The one group we needed is precisely the one that
					// failed to load, so its absence from the partial list
					// is no evidence at all. Unknown, not missing (#12).
					discoveryUnavailable = true
				default:
					discoveryPresent = &present
				}
			}
			verdict := probes.sar[sarQuestionFor(op)]
			authErr = verdict.err
			if authErr == nil {
				authorized, authReason = authorizedFromClusterWideSAR(op, verdict.allowed)
			}
		}

		capabilities = append(capabilities, buildCapability(
			op, isLocal, globalReason, reach,
			discoveryPresent, discoveryUnavailable,
			authorized, authErr, authReason, now,
		))
	}

	w.Header().Set("Cache-Control", "no-store")
	httputil.WriteData(w, CapabilitiesResponse{
		ClusterID:    normID,
		Capabilities: capabilities,
	})
}

// errNoAccessChecker stands in for "no ResourceHandler/AccessChecker wired"
// (a real local-only or partially-configured deployment shape, and the
// default shape of testServer(t) in this package's tests) so that case maps
// to authz_unknown via the same authErr != nil branch as a real SAR error,
// rather than needing a second code path.
var errNoAccessChecker = errors.New("no AccessChecker wired")

// errProbeNotRun marks a probe whose worker never recorded a result, which
// only happens when it panicked: recoverutil.Go logged the panic and turned
// it into an errgroup error, and the slot keeps this value so the rows that
// depended on it report authz_unknown instead of a guessed answer.
var errProbeNotRun = errors.New("capability probe did not complete")

// capabilityProbeConcurrency caps the live calls one capabilities request has
// in flight at once. Each is a round trip to the target's API server, so the
// cap keeps a single page load from bursting the remote cluster, while still
// collapsing the old serial sum (discovery plus every SAR, one after another)
// into roughly the slowest few.
const capabilityProbeConcurrency = 4

// sarQuestion is one distinct access question. Several rows ask the same
// verb/group/resource (patch configmaps, list pods), so each distinct
// question is asked once per request and its verdict shared by those rows.
type sarQuestion struct{ verb, group, resource string }

func sarQuestionFor(op capabilityOp) sarQuestion {
	return sarQuestion{verb: op.AuthVerb, group: op.AuthGroup, resource: op.AuthResource}
}

// sarVerdict is the raw answer to one sarQuestion. What a denial is worth on
// a given row is still decided per row by authorizedFromClusterWideSAR.
type sarVerdict struct {
	allowed bool
	err     error
}

// capabilityProbes holds the results of the live calls one request makes.
// It is written by runCapabilityProbes' workers and read only after they
// have all finished.
type capabilityProbes struct {
	discoveryLists        []*metav1.APIResourceList
	discoveryUnavailable  bool
	discoveryFailedGroups map[string]bool
	sar                   map[sarQuestion]sarVerdict
}

// runCapabilityProbes makes the live calls behind the discovery and
// authorized dimensions concurrently: at most one discovery fetch (both
// GVR-probed rows share schema.Discovery) and one cluster-wide SAR per
// distinct question, for the rows supported on this target class. On a
// remote cluster each is a network round trip, and asking them serially made
// the endpoint's latency their sum.
//
// Nothing runs unless the target is reachable. Workers run off the request
// goroutine, where chi's Recoverer cannot catch a panic, so each goes through
// recoverutil.Go; a panicking worker leaves its result at the unavailable /
// errProbeNotRun default rather than taking the process down.
func (s *Server) runCapabilityProbes(ctx context.Context, reachable, isLocal bool, targetSchema *k8s.TargetSchema, clusterID, username string, groups []string) capabilityProbes {
	probes := capabilityProbes{sar: map[sarQuestion]sarVerdict{}}
	if !reachable {
		return probes
	}

	needDiscovery := false
	var questions []sarQuestion
	seen := map[sarQuestion]bool{}
	for _, op := range capabilityOperations {
		if !op.supportedFor(isLocal) {
			continue
		}
		if op.Probe != nil {
			needDiscovery = true
		}
		if q := sarQuestionFor(op); !seen[q] {
			seen[q] = true
			questions = append(questions, q)
		}
	}

	var ac *resources.AccessChecker
	if s.ResourceHandler != nil {
		ac = s.ResourceHandler.AccessChecker
	}

	var g errgroup.Group
	g.SetLimit(capabilityProbeConcurrency)

	if needDiscovery {
		// Unavailable until the fetch actually completes, so a missing
		// schema or a panicking fetch both read as discovery_unavailable.
		probes.discoveryUnavailable = true
		if targetSchema != nil && targetSchema.Discovery != nil {
			recoverutil.Go(&g, s.Logger, "capabilities discovery", func() error {
				lists, unavailable, failed := fetchDiscoveryLists(targetSchema.Discovery)
				probes.discoveryLists, probes.discoveryUnavailable, probes.discoveryFailedGroups = lists, unavailable, failed
				return nil
			})
		}
	}

	verdicts := make([]sarVerdict, len(questions))
	for i, q := range questions {
		if ac == nil {
			verdicts[i] = sarVerdict{err: errNoAccessChecker}
			continue
		}
		verdicts[i] = sarVerdict{err: errProbeNotRun}
		recoverutil.Go(&g, s.Logger, "capabilities access check", func() error {
			// The trailing "" is the SAR namespace: this is a CLUSTER-WIDE
			// probe, which is an exact question only for a cluster-scoped
			// resource. authorizedFromClusterWideSAR decides what a negative
			// verdict is worth on each scope.
			allowed, err := ac.CanAccessGroupResource(ctx, clusterID, username, groups, q.verb, q.group, q.resource, "")
			verdicts[i] = sarVerdict{allowed: allowed, err: err}
			return nil
		})
	}

	// Workers never return an error of their own; a non-nil Wait means a
	// recovered panic, already logged by recoverutil, whose slot kept its
	// default above.
	_ = g.Wait()

	for i, q := range questions {
		probes.sar[q] = verdicts[i]
	}
	return probes
}
