package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
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
	Operation         string     `json:"operation"`
	Label             string     `json:"label"`
	PlatformSupported bool       `json:"platformSupported"`
	DiscoveryPresent  *bool      `json:"discoveryPresent"`
	Reachable         *bool      `json:"reachable"`
	Authorized        *bool      `json:"authorized"`
	ObservedAt        string     `json:"observedAt"`
	ReasonCode        ReasonCode `json:"reasonCode"`
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
		ID: "dashboard.summary", Label: "Dashboard summary",
		LocalSupported: true, RemoteSupported: false,
		Probe:    &gvrProbe{Group: "", Resource: "nodes"},
		AuthVerb: "list", AuthGroup: "", AuthResource: "nodes",
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
		// is reported forbidden here: a false negative, but one that fails
		// in the safe direction, and A1 leaves no alternative without
		// extending AccessChecker's API.
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
		// granted access only via a `resources: ["*/log"]` wildcard rule is
		// reported forbidden here rather than authorized (safe-direction
		// false negative) — see pod.exec's comment for why.
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
	// probe cycle involved) and the record's LastProbedAt for remote (the
	// weakest-freshness input contributing to the row, D3).
	observedAt time.Time
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
	rec, err := cs.Get(ctx, clusterID)
	if err != nil || rec == nil {
		// Reachability itself could not be determined (e.g. the store read
		// failed after target resolution had already succeeded, or a
		// clusterRecordGetter — real or a test fake — returns (nil, nil))
		// — treated the same as staleness: reachable stays nil, not a
		// guessed false. The real *store.ClusterStore always returns
		// (nil, err) on failure, so rec == nil here can't fire in
		// production; the check exists for a future fake that returns
		// (nil, nil) instead of dereferencing a nil rec below.
		return reachabilityResult{observedAt: now}
	}
	if rec.LastProbedAt == nil || now.Sub(*rec.LastProbedAt) > staleAfter {
		at := now
		if rec.LastProbedAt != nil {
			at = *rec.LastProbedAt
		}
		return reachabilityResult{observedAt: at}
	}
	// "connected" is part of the connected|disconnected|blocked|error status
	// vocabulary owned by internal/k8s/cluster_prober.go (~7 call sites) —
	// a rename there would silently make every remote cluster read
	// unreachable here, with no test to catch it.
	connected := rec.Status == "connected"
	return reachabilityResult{reachable: &connected, observedAt: *rec.LastProbedAt}
}

// classifyTargetSchemaErr maps a TargetSchemaFor error to one of the three
// target-resolution reason codes (step 2d / brief A3). db_unavailable is
// matched by the exact substring requireClusterStore uses
// (cluster_router.go) so the mapping tracks that message rather than
// duplicating its wording; a genuine "cluster not found" (pgx.ErrNoRows,
// propagated unwrapped through ClusterStore.Get's %w chain) is
// cluster_unknown; everything else (decrypt, SSRF block, TLS policy
// failure, or any other resolution error) is credentials_invalid as the
// catch-all "something is wrong with how we'd connect to this cluster".
func classifyTargetSchemaErr(err error) ReasonCode {
	if errors.Is(err, pgx.ErrNoRows) {
		return ReasonClusterUnknown
	}
	if strings.Contains(err.Error(), "no cluster store") {
		return ReasonDBUnavailable
	}
	return ReasonCredentialsInvalid
}

// fetchDiscoveryLists calls ServerGroupsAndResources once and tolerates the
// partial-result-with-error shape exactly as resolveGVR does
// (yaml/handler.go:354-361, cited in brief A6): only a nil list counts as
// "discovery unavailable" — a non-nil list alongside a non-nil error (some
// group/version failed to load) is still usable for the groups that did.
func fetchDiscoveryLists(disc discovery.DiscoveryInterface) (lists []*metav1.APIResourceList, unavailable bool) {
	_, apiResourceLists, err := disc.ServerGroupsAndResources()
	if err != nil && apiResourceLists == nil {
		return nil, true
	}
	return apiResourceLists, false
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
		row.ReasonCode = ReasonStaleObservation
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
		reach = resolveReachability(ctx, s.ClusterStore, isLocal, normID, now)
	}

	// Discovery lists are fetched at most once per response (both GVR-probed
	// operations share the same schema.Discovery) and ONLY when the target is
	// known-reachable — attempting a live discovery call against a target
	// already reported down or unknown would just duplicate a slow, possibly
	// hanging network round-trip the ClusterProber already owns.
	var discoveryLists []*metav1.APIResourceList
	discoveryChecked := false
	discoveryListsUnavailable := false
	fetchDiscoveryOnce := func() {
		if discoveryChecked {
			return
		}
		discoveryChecked = true
		if targetSchema == nil || targetSchema.Discovery == nil {
			discoveryListsUnavailable = true
			return
		}
		discoveryLists, discoveryListsUnavailable = fetchDiscoveryLists(targetSchema.Discovery)
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
				if discoveryListsUnavailable {
					discoveryUnavailable = true
				} else {
					present := gvrPresentIn(discoveryLists, op.Probe.Group, op.Probe.Resource)
					discoveryPresent = &present
				}
			}
			if s.ResourceHandler != nil && s.ResourceHandler.AccessChecker != nil {
				var allowed bool
				allowed, authErr = s.ResourceHandler.AccessChecker.CanAccessGroupResource(
					ctx, normID, user.KubernetesUsername, user.KubernetesGroups,
					op.AuthVerb, op.AuthGroup, op.AuthResource, "",
				)
				if authErr == nil {
					authorized = &allowed
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
