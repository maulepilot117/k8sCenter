package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
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

// ReasonCode is the closed set of machine-readable explanations for why a
// capability row is not plain "ok". Any value outside this set is a bug —
// TestCapabilities_ReasonCodesAreClosed pins that.
type ReasonCode string

const (
	// ReasonOK — supported, discovered, reachable, authorized.
	ReasonOK ReasonCode = "ok"
	// ReasonUnsupportedPlatform — k8sCenter has not implemented this
	// operation for this target class (local vs remote).
	ReasonUnsupportedPlatform ReasonCode = "unsupported_platform"
	// ReasonDiscoveryMissing — the target's discovery does not contain the
	// required group/resource.
	ReasonDiscoveryMissing ReasonCode = "discovery_missing"
	// ReasonDiscoveryUnavailable — the discovery call failed; the answer is
	// unknown, not "absent".
	ReasonDiscoveryUnavailable ReasonCode = "discovery_unavailable"
	// ReasonUnreachable — the last probe says disconnected/blocked/error.
	ReasonUnreachable ReasonCode = "unreachable"
	// ReasonStaleObservation — the last probe is older than 3x the 60s
	// probe interval (or there has never been one); reachable is null.
	ReasonStaleObservation ReasonCode = "stale_observation"
	// ReasonForbidden — the SAR returned Allowed: false for this identity.
	ReasonForbidden ReasonCode = "forbidden"
	// ReasonAuthzUnknown — the SAR could not be issued or errored.
	ReasonAuthzUnknown ReasonCode = "authz_unknown"
	// ReasonClusterUnknown — no such cluster id in the registry.
	ReasonClusterUnknown ReasonCode = "cluster_unknown"
	// ReasonCredentialsInvalid — decrypt / TLS-policy / impersonation-probe
	// failure while resolving the target.
	ReasonCredentialsInvalid ReasonCode = "credentials_invalid"
	// ReasonDBUnavailable — no ClusterStore wired (local-only deployment)
	// and a non-local target was asked for.
	ReasonDBUnavailable ReasonCode = "db_unavailable"
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
	// namespaced operation is therefore reported as authorized: nil /
	// authz_unknown: unknown, not denied.
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
	// ClusterScoped marks AuthResource as a cluster-scoped resource (nodes),
	// as opposed to a namespaced one (configmaps, pods, pods/exec, pods/log,
	// externalsecrets). It exists solely to decide how a NEGATIVE
	// cluster-wide SAR verdict is reported — see Capability.Authorized and
	// authorizedFromClusterWideSAR.
	//
	// The zero value means namespaced deliberately: most Kubernetes
	// resources are, and a forgotten field then errs toward
	// authz_unknown ("we could not determine") rather than toward a
	// definite-but-wrong forbidden. TestCapabilityOperations_ScopePinned
	// pins the scope of every row by ID so a new row still has to make the
	// choice consciously.
	ClusterScoped bool
}

// capabilityOperations is the package-level operation table (implementation
// step 1). Each "no" row's comment cites the exact guard file:line it
// mirrors, verified against the tree at 5c328337 (brief A6), so a reviewer
// can diff the claim against the guard instead of trusting prose.
var capabilityOperations = []capabilityOp{
	{
		// RemoteSupported is false TODAY, not "true per the master plan's
		// 'Remote (after Release C)' column" — that column names the
		// destination, not the interim, and yaml/handler.go:62 still 501s
		// every remote request right now. Flip to true when U9a ships and
		// removes that gate (task review round 1, finding #3): until then,
		// GET /capabilities/remote-x must not claim platformSupported: true
		// for an operation that 501s the moment it's actually called — the
		// exact inversion D3 exists to prevent, pointed the other way.
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
		// reported authz_unknown rather than forbidden — see
		// Capability.Authorized and capabilityOp.ClusterScoped.
		ID: "yaml.validate", Label: "Validate YAML",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "patch", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// RemoteSupported false until U9a — see yaml.validate's comment
		// above (finding #3); yaml/handler.go:220 still 501s remote today.
		// Secrets refused on both classes (enforced inside the yaml
		// handler, not here). AuthVerb "patch", not "get" (task review round
		// 2, finding #2a): differ.go:101 does call dr.Get(...) first, but
		// differ.go:118 then does dr.Patch(..., types.ApplyPatchType, data,
		// DryRun: []string{metav1.DryRunAll}) to compute the proposed state
		// — and Kubernetes authorizes a dry-run patch exactly as it
		// authorizes a real one (dryRun skips persistence, not
		// authorization), same reasoning as yaml.validate above. Diff needs
		// BOTH get and patch; probing "patch" is the representative choice
		// because it is the verb more likely to be denied, so it is the one
		// that actually carries information — probing "get" would report
		// authorized: true for a viewer who lacks patch and then hits
		// Forbidden inside the real call. AuthResource: configmaps — see
		// yaml.validate's comment for why (finding #2).
		ID: "yaml.diff", Label: "Diff YAML against live state",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "patch", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// RemoteSupported false until U9a — see yaml.validate's comment
		// above (finding #3); yaml/handler.go:282 still 501s remote today.
		// Secrets refused on both classes. AuthVerb "get", not "list" (task
		// review round 2, finding #2b): handler.go:245 requires kind AND
		// name to be non-empty (400 otherwise) — export always fetches ONE
		// named object, never a list — and the only client calls in the
		// export path are the two dynClient...Get(...) calls at
		// handler.go:298/300. There is no .List anywhere in it.
		// AuthResource: configmaps (finding #2).
		ID: "yaml.export", Label: "Export YAML",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "get", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// RemoteSupported false until U9b — see yaml.validate's comment
		// above (finding #3); yaml/handler.go:147 still 501s remote today.
		// AuthVerb "patch": server-side apply for all YAML operations is a
		// PATCH (application/apply-patch+yaml — see CLAUDE.md's backend
		// architecture principles). AuthResource: configmaps (finding #2).
		ID: "yaml.apply", Label: "Apply YAML",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "patch", AuthGroup: "", AuthResource: "configmaps",
	},
	{
		// RemoteSupported false TODAY (task review round 1, finding #3):
		// dashboard.go:544 rejects every non-local request outright right
		// now (400, not even a partial response), so reporting
		// platformSupported: true would be the exact D3 inversion this unit
		// exists to prevent. Flip to true when U10 ships remote dashboard
		// summary — the master plan's "partial" (no health score) note
		// describes THAT future state, not this one; the plan's "Remote
		// (after Release C)" column header names the destination, never the
		// interim. Probed against core/v1 nodes (A2) since the summary's
		// node/health section is what a hardened remote cluster is most
		// likely to have hidden from discovery — this GVR probe is already
		// meaningful on the LOCAL branch today (a stripped-down local
		// install could lack node-list visibility too) and will carry over
		// unchanged once U10 flips RemoteSupported.
		//
		// The ONLY cluster-scoped row in the table: nodes are not namespaced,
		// so the cluster-wide SAR below is an exact question and a denial is
		// a real denial (forbidden), not the ambiguous namespaced negative
		// every other row has to report as authz_unknown.
		ID: "dashboard.summary", Label: "Dashboard summary",
		LocalSupported: true, RemoteSupported: false,
		Probe:    &gvrProbe{Group: "", Resource: "nodes"},
		AuthVerb: "list", AuthGroup: "", AuthResource: "nodes",
		ClusterScoped: true,
	},
	{
		// Resource counts rely on the local informer cache; remote clusters
		// use direct API calls and do not populate informers.
		// counts.go:28.
		ID: "resources.counts", Label: "Resource counts",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Pod exec requires an SPDY stream upgrade against the target
		// cluster's own API server, not yet supported for remote clusters.
		// pods.go:164.
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
		// namespaced, it is now reported as authz_unknown — the honest
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
		// servers. handle_ws_logs.go:104.
		//
		// Same "pods/log" subresource caveat as pod.exec above: an identity
		// granted access only via a `resources: ["*/log"]` wildcard rule
		// comes back Allowed: false and is reported authz_unknown (pods/log
		// is namespaced) rather than forbidden — see pod.exec's comment.
		ID: "logs.stream", Label: "Live log stream",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "get", AuthGroup: "", AuthResource: "pods/log",
	},
	{
		// Loki tail always targets the LOCAL cluster's Loki service; a
		// remote X-Cluster-ID would stream local logs under the remote
		// cluster's name (confused deputy). handle_ws_logs_search.go:61.
		ID: "logs.search", Label: "Log search",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Hubble flow streaming targets the LOCAL cluster's CNI data plane
		// for the same confused-deputy reason as logs.search.
		// handle_ws_flows.go:62.
		ID: "flows.stream", Label: "Network flow stream",
		LocalSupported: true, RemoteSupported: false,
		AuthVerb: "list", AuthGroup: "", AuthResource: "pods",
	},
	{
		// Phase E ESO writes don't route through ClusterRouter — the
		// dynamic client always points at the local ClientFactory — so
		// honoring X-Cluster-ID would desync the audit row from the actual
		// mutation. externalsecrets/actions.go:66. Probed against
		// external-secrets.io/externalsecrets (A2) since ESO is an optional
		// CRD-based operator that may not be installed even locally.
		ID: "eso.write", Label: "External Secrets write actions",
		LocalSupported: true, RemoteSupported: false,
		Probe:    &gvrProbe{Group: "external-secrets.io", Resource: "externalsecrets"},
		AuthVerb: "patch", AuthGroup: "external-secrets.io", AuthResource: "externalsecrets",
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

// classifyTargetSchemaErr maps a TargetSchemaFor error to a target-resolution
// reason code (step 2d / brief A3).
//
// Order matters, and every branch before the last one exists because it
// names a condition credentials_invalid would otherwise lie about (review
// finding #7 — three distinct error shapes reached the catch-all and told
// the operator their stored credentials were bad):
//
//  1. pgx.ErrNoRows (propagated unwrapped through ClusterStore.Get's %w
//     chain) — the row genuinely isn't there: cluster_unknown.
//  2. requireClusterStore's "no cluster store" message — no registry wired:
//     db_unavailable. Matched by substring deliberately: cluster_router.go's
//     requireClusterStore doc comment declares that wording load-bearing and
//     maintains a census of the six assertions on it (three in internal/k8s,
//     three in internal/certmanager) plus this one. Replacing it with a
//     typed sentinel would mean editing that contract and its census, which
//     is out of this change's scope, so the match stays and the census stays
//     accurate.
//  3. A PostgreSQL-level failure — a server error (pgconn.PgError) or a
//     failed connection attempt (pgconn.ConnectError) on the miss path's
//     cluster-record read: db_unavailable. This MUST precede the network
//     checks below, because a Postgres connect failure wraps a net.OpError
//     and would otherwise be reported as "the cluster is unreachable" when
//     it is the registry that is down.
//  4. DNS / timeout / transport failures — a wrapped *net.DNSError from
//     ValidateRemoteURLContext's fail-closed lookup, context.DeadlineExceeded
//     or context.Canceled from the 30s bound TargetSchemaFor puts on its
//     miss path (and remoteConfig's own), or any other net.Error: the
//     credentials are unproven, not invalid — we never got far enough to
//     use them. unreachable.
//
// Everything left is what credentials_invalid actually names: decrypt
// failure, SSRF/TLS policy refusal, impersonation probe failure — "something
// is wrong with how we'd connect to this cluster", not with whether we can
// reach it or read its registry row.
func classifyTargetSchemaErr(err error) ReasonCode {
	if errors.Is(err, pgx.ErrNoRows) {
		return ReasonClusterUnknown
	}
	if strings.Contains(err.Error(), "no cluster store") {
		return ReasonDBUnavailable
	}

	var pgErr *pgconn.PgError
	var pgConnErr *pgconn.ConnectError
	if errors.As(err, &pgErr) || errors.As(err, &pgConnErr) {
		return ReasonDBUnavailable
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ReasonUnreachable
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ReasonUnreachable
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ReasonUnreachable
	}

	return ReasonCredentialsInvalid
}

// fetchDiscoveryLists calls ServerGroupsAndResources once and tolerates the
// partial-result-with-error shape exactly as resolveGVR does
// (yaml/handler.go:354-361, cited in brief A6): only a nil list counts as
// "discovery unavailable" — a non-nil list alongside a non-nil error (some
// group/version failed to load) is still usable for the groups that did.
//
// failedGroups is the set of API groups that did NOT load, extracted from
// client-go's *discovery.ErrGroupDiscoveryFailed. Tolerating a partial
// result is right for every group that loaded, but for the group actually
// being probed it is the difference between two answers this endpoint exists
// to keep apart: gvrPresentIn finds nothing in a list the group never made
// it into, and reporting that as a definite discovery_missing claims the CRD
// is absent when all we know is that we failed to look (review finding #12).
// The caller turns membership in this set into discovery_unavailable.
func fetchDiscoveryLists(disc discovery.DiscoveryInterface) (lists []*metav1.APIResourceList, unavailable bool, failedGroups map[string]bool) {
	_, apiResourceLists, err := disc.ServerGroupsAndResources()
	if err != nil && apiResourceLists == nil {
		return nil, true, nil
	}
	return apiResourceLists, false, failedDiscoveryGroups(err)
}

// failedDiscoveryGroups reduces a discovery error to the set of API group
// names that failed to load. Returns nil for a nil error or any error shape
// that isn't client-go's per-group-version failure aggregate — in which case
// the caller has no evidence any specific group is unknown and keeps today's
// definite verdict.
func failedDiscoveryGroups(err error) map[string]bool {
	if err == nil {
		return nil
	}
	var groupErr *discovery.ErrGroupDiscoveryFailed
	if !errors.As(err, &groupErr) || len(groupErr.Groups) == 0 {
		return nil
	}
	groups := make(map[string]bool, len(groupErr.Groups))
	for gv := range groupErr.Groups {
		groups[gv.Group] = true
	}
	return groups
}

// gvrPresentIn reports whether group/resource appears in lists. Mirrors the
// matching loop in resolveGVR (yaml/handler.go).
func gvrPresentIn(lists []*metav1.APIResourceList, group, resource string) bool {
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		if gv.Group != group {
			continue
		}
		for _, r := range l.APIResources {
			if strings.EqualFold(r.Name, resource) {
				return true
			}
		}
	}
	return false
}

func boolPtr(b bool) *bool { return &b }

// truncateForEcho bounds s to n bytes before it is echoed back in an error
// response, marking that truncation happened rather than silently clipping
// — a truncated value with no marker would misrepresent what the caller
// actually sent. Used for the {clusterID} path parameter (handler.go's
// mismatch response), which has no upstream length cap the way the
// X-Cluster-ID header does (middleware.ClusterContext caps that at 64).
func truncateForEcho(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
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
// discovery_unavailable > forbidden/authz_unknown > ok.
func buildCapability(
	op capabilityOp,
	isLocal bool,
	globalReason ReasonCode,
	reach reachabilityResult,
	discoveryPresent *bool,
	discoveryUnavailable bool,
	authorized *bool,
	authErr error,
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
			reason = ReasonAuthzUnknown
		case authorized != nil && !*authorized:
			reason = ReasonForbidden
		case authorized != nil && *authorized:
			reason = ReasonOK
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
// dimension.
//
// buildCapability's contract is unchanged and deliberately dumb: a non-nil
// false means forbidden. Deciding whether a given false IS a denial is this
// function's job, because that depends on the shape of the question asked,
// which is a property of the probe, not of the reason-code priority chain.
//
//   - allowed == true → true, on any scope. A cluster-wide allow genuinely
//     implies every namespace.
//   - allowed == false on a CLUSTER-SCOPED resource → false. The question
//     had no namespace dimension; the denial is the whole answer.
//   - allowed == false on a NAMESPACED resource → nil (authz_unknown). The
//     probe asked "in ALL namespaces?"; an identity with an ordinary
//     namespaced Role answers no to that and yes where it matters. See
//     Capability.Authorized for the full rationale and the ?namespace=
//     follow-up.
//
// A SAR that ERRORS never reaches here — the caller keeps authorized nil and
// passes the error through, which buildCapability maps to authz_unknown by
// its own branch.
func authorizedFromClusterWideSAR(op capabilityOp, allowed bool) *bool {
	if allowed || op.ClusterScoped {
		return boolPtr(allowed)
	}
	return nil
}

// capabilityClusterGetter resolves the clusterRecordGetter the reachability
// dimension reads from. It is a package-level func var rather than a direct
// s.ClusterStore reference at the call site for exactly the reason
// clusterRecordGetter is an interface at all: *store.ClusterStore is a
// concrete type wrapping an unexported pgx pool, and internal/k8s exposes no
// seam for faking the router's copy of it either, so substituting a fake
// here is the only way a package-internal test can drive the handler's
// REMOTE chain (reachability → discovery → impersonated SAR) end to end
// through real HTTP instead of by calling resolveReachability and
// buildCapability directly (review finding #3 — every remote reason code was
// pinned only by direct calls against test-only synthetic rows).
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
	// when anySupported is false, every row is going to end up
	// unsupported_platform regardless of what target resolution or
	// reachability would have said — resolving them is provably wasted
	// work. Today that is true of EVERY remote request (every real row is
	// RemoteSupported: false as of task review round 1's finding #3), which
	// means the target-schema switch below was, until this fix, still
	// doing a clusterStore.Get + a live DNS re-resolution + a credential
	// decrypt (TargetSchemaFor's remote miss path, under a 30s ceiling) and
	// resolveReachability's own second clusterStore.Get, for a result that
	// was then discarded — the response to any non-local id was byte-
	// identical whether the cluster existed, had valid credentials, was
	// reachable, or was a typo (task review, whole-branch pass, Important
	// #2). Gating on anySupported removes exactly that dead work and
	// nothing else: isLocal always makes it true (every row is
	// LocalSupported: true), so the local response is unchanged, and this
	// guard removes ITSELF automatically the moment any unit (U9a/U9b/U10)
	// flips a single row's RemoteSupported to true.
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

	// Discovery lists are fetched at most once per response (both GVR-probed
	// operations share the same schema.Discovery) and ONLY when the target is
	// known-reachable — attempting a live discovery call against a target
	// already reported down or unknown would just duplicate a slow, possibly
	// hanging network round-trip the ClusterProber already owns.
	var discoveryLists []*metav1.APIResourceList
	discoveryChecked := false
	discoveryListsUnavailable := false
	var discoveryFailedGroups map[string]bool
	fetchDiscoveryOnce := func() {
		if discoveryChecked {
			return
		}
		discoveryChecked = true
		if targetSchema == nil || targetSchema.Discovery == nil {
			discoveryListsUnavailable = true
			return
		}
		discoveryLists, discoveryListsUnavailable, discoveryFailedGroups = fetchDiscoveryLists(targetSchema.Discovery)
	}

	reachableNow := globalReason == "" && reach.reachable != nil && *reach.reachable

	capabilities := make([]Capability, 0, len(capabilityOperations))
	for _, op := range capabilityOperations {
		platformSupported := op.supportedFor(isLocal)

		var discoveryPresent *bool
		discoveryUnavailable := false
		var authorized *bool
		var authErr error

		if platformSupported && globalReason == "" && reachableNow {
			if op.Probe != nil {
				fetchDiscoveryOnce()
				present := !discoveryListsUnavailable && gvrPresentIn(discoveryLists, op.Probe.Group, op.Probe.Resource)
				switch {
				case discoveryListsUnavailable:
					discoveryUnavailable = true
				case !present && discoveryFailedGroups[op.Probe.Group]:
					// The one group we needed is precisely the one that
					// failed to load, so its absence from the partial list
					// is no evidence at all. Unknown, not missing (#12).
					discoveryUnavailable = true
				default:
					discoveryPresent = &present
				}
			}
			if s.ResourceHandler != nil && s.ResourceHandler.AccessChecker != nil {
				// The trailing "" is the SAR namespace: this is a
				// CLUSTER-WIDE probe, which is an exact question only for a
				// cluster-scoped resource. authorizedFromClusterWideSAR
				// decides what a negative verdict is worth on each scope.
				var allowed bool
				allowed, authErr = s.ResourceHandler.AccessChecker.CanAccessGroupResource(
					ctx, normID, user.KubernetesUsername, user.KubernetesGroups,
					op.AuthVerb, op.AuthGroup, op.AuthResource, "",
				)
				if authErr == nil {
					authorized = authorizedFromClusterWideSAR(op, allowed)
				}
			} else {
				authErr = errNoAccessChecker
			}
		}

		capabilities = append(capabilities, buildCapability(
			op, isLocal, globalReason, reach,
			discoveryPresent, discoveryUnavailable,
			authorized, authErr, now,
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
