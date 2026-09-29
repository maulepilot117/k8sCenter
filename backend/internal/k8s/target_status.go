package k8s

// Target status: the shared vocabulary for "what happened when we tried to
// use this cluster", used by the capabilities endpoint and by every feature
// handler that reads a remote cluster, so both report failures with one
// closed set of reasons.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	"github.com/kubecenter/kubecenter/internal/recoverutil"
)

// ReasonCode is the closed set of machine-readable explanations for why a
// cluster operation or capability row is not plain "ok". Any value outside this set is a bug —
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
	// ReasonForbidden — the SAR returned Allowed: false for this identity,
	// on a CLUSTER-SCOPED operation, where a cluster-wide probe asks an exact
	// question and a denial is therefore the whole answer.
	ReasonForbidden ReasonCode = "forbidden"
	// ReasonAuthzUnknown — the SAR could not be issued or errored. This code
	// means "we failed to ask", nothing more; a SAR that was asked and
	// answered "no" never lands here (see ReasonAuthzNamespaceScoped).
	ReasonAuthzUnknown ReasonCode = "authz_unknown"
	// ReasonAuthzNamespaceScoped — the SAR was issued successfully and
	// returned Allowed: false, but the operation is NAMESPACED and the probe
	// was cluster-wide, so the denial does not prove this identity lacks
	// access; it only proves the identity does not hold the permission in
	// EVERY namespace. authorized is nil here exactly as it is for
	// authz_unknown — the verdict genuinely is indeterminate — and only the
	// reason code distinguishes "the probe's shape makes this unknowable"
	// from "we never got an answer at all". Collapsing the two (as this
	// endpoint did before) hides from the operator that a definite answer
	// exists and is simply not being asked for; see Capability.Authorized for
	// the ?namespace= follow-up that would turn it into a yes/no.
	ReasonAuthzNamespaceScoped ReasonCode = "authz_namespace_scoped"
	// ReasonClusterUnknown — no such cluster id in the registry.
	ReasonClusterUnknown ReasonCode = "cluster_unknown"
	// ReasonCredentialsInvalid — decrypt / TLS-policy / impersonation-probe
	// failure while resolving the target.
	ReasonCredentialsInvalid ReasonCode = "credentials_invalid"
	// ReasonDBUnavailable — no ClusterStore wired (local-only deployment)
	// and a non-local target was asked for.
	ReasonDBUnavailable ReasonCode = "db_unavailable"
)

// ClassifyTargetErr maps a TargetSchemaFor (or ClientForCluster) error to a target-resolution
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
func ClassifyTargetErr(err error) ReasonCode {
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

// DiscoveryLists calls ServerGroupsAndResources once and tolerates the
// partial-result-with-error shape exactly as resolveGVR does
// (yaml/handler.go:548-553): only a nil list counts as "discovery
// unavailable" — a non-nil list alongside a non-nil error (some group/version
// failed to load) is still usable for the groups that did.
//
// failedGroups is the set of API groups that did NOT load, extracted from
// client-go's *discovery.ErrGroupDiscoveryFailed. Tolerating a partial
// result is right for every group that loaded, but for the group actually
// being probed it is the difference between two answers this endpoint exists
// to keep apart: GVRPresentIn finds nothing in a list the group never made
// it into, and reporting that as a definite discovery_missing claims the CRD
// is absent when all we know is that we failed to look (review finding #12).
// The caller turns membership in this set into discovery_unavailable.
func DiscoveryLists(disc discovery.DiscoveryInterface) (lists []*metav1.APIResourceList, unavailable bool, failedGroups map[string]bool) {
	_, apiResourceLists, err := disc.ServerGroupsAndResources()
	if err != nil && apiResourceLists == nil {
		return nil, true, nil
	}
	return apiResourceLists, false, FailedDiscoveryGroups(err)
}

// FailedDiscoveryGroups reduces a discovery error to the set of API group
// names that failed to load. Returns nil for a nil error or any error shape
// that isn't client-go's per-group-version failure aggregate — in which case
// the caller has no evidence any specific group is unknown and keeps today's
// definite verdict.
func FailedDiscoveryGroups(err error) map[string]bool {
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

// GVRPresentIn reports whether group/resource appears in lists. Mirrors the
// matching loop in resolveGVR (yaml/handler.go).
func GVRPresentIn(lists []*metav1.APIResourceList, group, resource string) bool {
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

// presenceAbsentTTL is how long a definite "not installed" verdict is reused
// before discovery is read again. The remote schema cache itself holds
// discovery for clientCacheTTL (5 minutes); without this shorter window and
// the invalidation on expiry, a CRD installed on a remote cluster would stay
// invisible for that long.
const presenceAbsentTTL = 30 * time.Second

// PresenceVerdict answers "is this API installed on the cluster, as seen by
// this identity?". Installed is nil when the answer is unknown; Reason then
// says why (unreachable, discovery_unavailable, ...). A definite absence is
// Installed false with ReasonDiscoveryMissing, and presence is Installed true
// with ReasonOK.
type PresenceVerdict struct {
	Installed *bool
	Reason    ReasonCode
}

type presenceKey struct {
	clusterID string
	identity  string
	resource  schema.GroupResource
}

// Presence checks whether a group/resource is served by a cluster, through
// the cluster's own discovery as the requesting identity sees it. Feature
// packages use it to answer their status routes and to tell "not installed"
// apart from "could not tell" on a remote cluster.
type Presence struct {
	clients ClusterClients
	now     func() time.Time

	mu     sync.Mutex
	absent map[presenceKey]time.Time // definite-absence verdicts and their expiry

	// sf coalesces concurrent probes of one key, so an absence verdict
	// expiring under load triggers one schema invalidation and one
	// discovery read, not one per waiting request.
	sf singleflight.Group
}

func newPresenceKey(clusterID, username string, groups []string, resource schema.GroupResource) presenceKey {
	return presenceKey{clusterID: NormalizedClusterID(clusterID), identity: IdentityKey(username, groups), resource: resource}
}

// flightKey identifies a probe for singleflight. A probe that invalidates
// the schema never joins one that does not, so Recheck always re-reads.
func (k presenceKey) flightKey(invalidate bool) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%t", k.clusterID, k.identity, k.resource, invalidate)
}

// NewPresence returns a Presence that reads discovery through clients.
func NewPresence(clients ClusterClients) *Presence {
	return &Presence{
		clients: clients,
		now:     time.Now,
		absent:  make(map[presenceKey]time.Time),
	}
}

// Check reports whether resource is installed on clusterID for the identity.
// Only a definite absence is remembered, for presenceAbsentTTL; once that
// passes the cluster's cached schema is invalidated and discovery read
// again. Unknown verdicts are never remembered.
func (p *Presence) Check(ctx context.Context, clusterID, username string, groups []string, resource schema.GroupResource) PresenceVerdict {
	key := newPresenceKey(clusterID, username, groups, resource)

	p.mu.Lock()
	expiry, remembered := p.absent[key]
	p.mu.Unlock()
	if remembered && p.now().Before(expiry) {
		return absentVerdict()
	}
	return p.probe(ctx, key, username, groups, remembered)
}

// Recheck discards anything known about resource on clusterID for the
// identity and reads discovery afresh. Call it when a list of a resource
// the last check called installed fails with IsResourceGone: the CRD may
// have been removed since the schema was cached.
func (p *Presence) Recheck(ctx context.Context, clusterID, username string, groups []string, resource schema.GroupResource) PresenceVerdict {
	return p.probe(ctx, newPresenceKey(clusterID, username, groups, resource), username, groups, true)
}

// EvictCluster forgets every verdict for clusterID. Register it as a
// ClusterRouter evict hook so a re-registered cluster is probed afresh.
func (p *Presence) EvictCluster(clusterID string) {
	clusterID = NormalizedClusterID(clusterID)
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.absent {
		if k.clusterID == clusterID {
			delete(p.absent, k)
		}
	}
}

// probe reads the cluster's discovery for key, first invalidating the cached
// schema when invalidate is set (an expired absence, or a Recheck).
// Concurrent probes of the same key share one read; a caller whose ctx ends
// while waiting gets an unknown verdict.
func (p *Presence) probe(ctx context.Context, key presenceKey, username string, groups []string, invalidate bool) PresenceVerdict {
	ch := p.sf.DoChan(key.flightKey(invalidate), func() (any, error) {
		verdict := PresenceVerdict{Reason: ReasonDiscoveryUnavailable}
		// singleflight re-panics on a fresh goroutine; recover here so
		// malformed discovery from a remote cluster degrades to "unknown".
		recoverutil.Safe(nil, "k8s presence probe", func() {
			verdict = p.read(ctx, key, username, groups, invalidate)
		})
		return verdict, nil
	})
	select {
	case <-ctx.Done():
		return PresenceVerdict{Reason: ReasonUnreachable}
	case res := <-ch:
		return res.Val.(PresenceVerdict)
	}
}

func (p *Presence) read(ctx context.Context, key presenceKey, username string, groups []string, invalidate bool) PresenceVerdict {
	target, err := p.clients.TargetSchemaFor(ctx, key.clusterID, username, groups)
	if err != nil {
		return PresenceVerdict{Reason: ClassifyTargetErr(err)}
	}
	if invalidate {
		target.Invalidate()
	}

	lists, unavailable, failedGroups := DiscoveryLists(target.Discovery)
	var verdict PresenceVerdict
	switch {
	case unavailable || failedGroups[key.resource.Group]:
		verdict = PresenceVerdict{Reason: ReasonDiscoveryUnavailable}
	case GVRPresentIn(lists, key.resource.Group, key.resource.Resource):
		installed := true
		verdict = PresenceVerdict{Installed: &installed, Reason: ReasonOK}
	default:
		verdict = absentVerdict()
	}

	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	// Drop absences for keys that stopped asking, so the map stays bounded
	// by recent activity. An expired entry is kept until the schema cache it
	// guards has rebuilt itself (clientCacheTTL): until then it is the only
	// signal that the key's next Check must invalidate the schema.
	for k, expiry := range p.absent {
		if now.After(expiry.Add(clientCacheTTL)) {
			delete(p.absent, k)
		}
	}
	if verdict.Reason == ReasonDiscoveryMissing {
		p.absent[key] = now.Add(presenceAbsentTTL)
	} else {
		delete(p.absent, key)
	}
	return verdict
}

func absentVerdict() PresenceVerdict {
	installed := false
	return PresenceVerdict{Installed: &installed, Reason: ReasonDiscoveryMissing}
}

// IsResourceGone reports whether err from a list or get means the resource
// type itself is no longer served: a RESTMapper no-match, or a NotFound on
// the collection rather than on one named object. A caller that sees it
// should Recheck presence instead of reporting an empty list.
func IsResourceGone(err error) bool {
	if meta.IsNoMatchError(err) {
		return true
	}
	var status apierrors.APIStatus
	if !apierrors.IsNotFound(err) || !errors.As(err, &status) {
		return false
	}
	details := status.Status().Details
	return details == nil || details.Name == ""
}
