package resources

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const accessCacheTTL = 60 * time.Second

// accessCacheKey identifies one cached SAR decision. clusterID is part of
// the key because a user may have entirely different RBAC on the local
// cluster vs a remote one — without it, a local "allow" decision would
// silently leak into a remote check that should have denied. F#9 security
// audit 2026-05-22.
type accessCacheKey struct {
	clusterID string // "" / "local" for the local cluster, otherwise the remote ID
	username  string
	groups    string // sorted, comma-joined for deterministic keying
	resource  string
	namespace string
	verb      string
}

type accessCacheEntry struct {
	allowed   bool
	expiresAt time.Time
}

// AccessChecker provides per-request RBAC filtering for informer cache reads.
// It uses SelfSubjectAccessReview to verify individual verb+resource+namespace
// permissions, cached for 60 seconds per user.
// AccessPredicate decides whether a user should be granted a specific
// (verb, apiGroup, resource, namespace) tuple. Used only by the
// predicate test fake — production never sets it.
type AccessPredicate func(verb, apiGroup, resource, namespace string) bool

type AccessChecker struct {
	clientFactory interface {
		ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
	}
	// clusterRouter routes SAR clients to remote clusters when a non-local
	// clusterID is supplied. nil is fine — callers always passing local
	// clusterID get the pre-F#9 behavior. F#9 security audit 2026-05-22.
	clusterRouter *k8s.ClusterRouter
	cache         sync.Map // map[accessCacheKey]accessCacheEntry
	logger        *slog.Logger
	alwaysAllow   bool            // for testing only
	alwaysDeny    bool            // for testing only
	predicate     AccessPredicate // for testing only
}

// NewAccessChecker creates an AccessChecker that verifies user permissions.
// clientFactory is the local cluster's ClientFactory (used when clusterID is
// "" or "local"). Use SetClusterRouter to wire the multi-cluster router for
// non-local SARs once ClusterRouter is constructed (it depends on the cluster
// store, which is built after the AccessChecker in main.go).
func NewAccessChecker(clientFactory interface {
	ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
}, logger *slog.Logger) *AccessChecker {
	return &AccessChecker{
		clientFactory: clientFactory,
		logger:        logger,
	}
}

// SetClusterRouter wires a *k8s.ClusterRouter into the AccessChecker so SARs
// against non-local clusterIDs route through the right remote API server. Safe
// to call once at startup before any HTTP traffic; not safe to call
// concurrently with CanAccess. F#9 security audit 2026-05-22.
func (ac *AccessChecker) SetClusterRouter(cr *k8s.ClusterRouter) {
	ac.clusterRouter = cr
}

// CanAccess checks if a user has a specific verb permission on a resource in a namespace.
// Empty namespace means cluster-scoped check.
// The resource can include a subresource separated by "/" (e.g., "pods/log", "pods/exec").
// The API group is inferred from the resource name via apiGroupForResource.
// For CRD resources with custom API groups, use CanAccessGroupResource instead.
//
// clusterID selects which cluster's API server runs the SAR. Pass
// middleware.ClusterIDFromContext(r.Context()) at every call site so remote
// clusters are checked against their own RBAC rather than the local
// cluster's. F#9 security audit 2026-05-22.
func (ac *AccessChecker) CanAccess(ctx context.Context, clusterID, username string, groups []string, verb, resource, namespace string) (bool, error) {
	if ac.alwaysAllow {
		return true, nil
	}
	if ac.alwaysDeny {
		return false, nil
	}
	// In predicate-fake mode, CanAccess short-circuits to allow so tests
	// targeting CanAccessGroupResource (the CRD path) aren't forced to
	// supply rules for every core resource the base graph touches. The
	// predicate is consulted directly by CanAccessGroupResource.
	if ac.predicate != nil {
		return true, nil
	}
	key := accessCacheKey{
		clusterID: clusterID,
		username:  username,
		groups:    sortedGroups(groups),
		resource:  resource,
		namespace: namespace,
		verb:      verb,
	}

	if val, ok := ac.cache.Load(key); ok {
		entry := val.(accessCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.allowed, nil
		}
		ac.cache.Delete(key)
	}

	cs, err := ac.clientForCluster(ctx, clusterID, username, groups)
	if err != nil {
		return false, fmt.Errorf("creating client for access check: %w", err)
	}

	// Split resource/subresource (e.g., "pods/log" → resource="pods", subresource="log")
	res := resource
	subres := ""
	if idx := strings.Index(resource, "/"); idx > 0 {
		res = resource[:idx]
		subres = resource[idx+1:]
	}

	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace:   namespace,
				Verb:        verb,
				Resource:    res,
				Subresource: subres,
				Group:       apiGroupForResource(res),
			},
		},
	}

	result, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("SelfSubjectAccessReview for %s/%s in %q on cluster %q: %w", verb, resource, namespace, clusterID, err)
	}

	allowed := result.Status.Allowed
	ac.cache.Store(key, accessCacheEntry{
		allowed:   allowed,
		expiresAt: time.Now().Add(accessCacheTTL),
	})

	ac.logger.Debug("access check",
		"cluster", clusterID,
		"user", username,
		"verb", verb,
		"resource", resource,
		"namespace", namespace,
		"allowed", allowed,
	)

	return allowed, nil
}

// CanAccessGroupResource checks if a user has a specific verb permission on a
// resource in a given API group and namespace. Use this for CRD resources where
// the API group is not in the hardcoded apiGroupForResource map.
// For core API resources, pass apiGroup="".
//
// clusterID selects which cluster's API server runs the SAR — see CanAccess.
// F#9 security audit 2026-05-22.
func (ac *AccessChecker) CanAccessGroupResource(ctx context.Context, clusterID, username string, groups []string, verb, apiGroup, resource, namespace string) (bool, error) {
	if ac.alwaysAllow {
		return true, nil
	}
	if ac.alwaysDeny {
		return false, nil
	}
	if ac.predicate != nil {
		return ac.predicate(verb, apiGroup, resource, namespace), nil
	}

	key := accessCacheKey{
		clusterID: clusterID,
		username:  username,
		groups:    sortedGroups(groups),
		resource:  apiGroup + "/" + resource,
		namespace: namespace,
		verb:      verb,
	}

	if val, ok := ac.cache.Load(key); ok {
		entry := val.(accessCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.allowed, nil
		}
		ac.cache.Delete(key)
	}

	cs, err := ac.clientForCluster(ctx, clusterID, username, groups)
	if err != nil {
		return false, fmt.Errorf("creating client for access check: %w", err)
	}

	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Resource:  resource,
				Group:     apiGroup,
			},
		},
	}

	result, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("SelfSubjectAccessReview for %s %s/%s in %q on cluster %q: %w", verb, apiGroup, resource, namespace, clusterID, err)
	}

	allowed := result.Status.Allowed
	ac.cache.Store(key, accessCacheEntry{
		allowed:   allowed,
		expiresAt: time.Now().Add(accessCacheTTL),
	})

	ac.logger.Debug("access check (group resource)",
		"cluster", clusterID,
		"user", username,
		"verb", verb,
		"group", apiGroup,
		"resource", resource,
		"namespace", namespace,
		"allowed", allowed,
	)

	return allowed, nil
}

// clientForCluster picks the SAR-issuing clientset for the given cluster.
// Local cluster ("", "local") routes through the local clientFactory;
// non-local routes through clusterRouter. F#9 security audit 2026-05-22.
func (ac *AccessChecker) clientForCluster(ctx context.Context, clusterID, username string, groups []string) (*kubernetes.Clientset, error) {
	if k8s.IsLocalClusterID(clusterID) {
		return ac.clientFactory.ClientForUser(username, groups)
	}
	if ac.clusterRouter == nil {
		return nil, fmt.Errorf("non-local clusterID %q requested but AccessChecker has no ClusterRouter — RBAC for remote clusters unavailable", clusterID)
	}
	return ac.clusterRouter.ClientForCluster(ctx, clusterID, username, groups)
}

// NewAlwaysAllowAccessChecker returns an AccessChecker that permits every request.
// Intended for unit tests where RBAC is not under test.
func NewAlwaysAllowAccessChecker() *AccessChecker {
	return &AccessChecker{
		clientFactory: nil, // never used — CanAccess is short-circuited via alwaysAllow
		logger:        slog.Default(),
		alwaysAllow:   true,
	}
}

// NewAlwaysDenyAccessChecker returns an AccessChecker that denies every request.
// Intended for unit tests where RBAC denial is under test.
func NewAlwaysDenyAccessChecker() *AccessChecker {
	return &AccessChecker{
		clientFactory: nil,
		logger:        slog.Default(),
		alwaysDeny:    true,
	}
}

// NewPredicateAccessChecker returns an AccessChecker that consults the
// supplied predicate for every CanAccessGroupResource call. Intended for
// tests covering partial-RBAC scenarios (e.g., "user can list one CRD
// group but not another"). The predicate isn't consulted by the basic
// CanAccess method because no current test scenario needs it.
func NewPredicateAccessChecker(fn AccessPredicate) *AccessChecker {
	return &AccessChecker{
		clientFactory: nil,
		logger:        slog.Default(),
		predicate:     fn,
	}
}

// StartCacheSweeper runs a background goroutine that periodically removes
// expired entries from the access cache. Stops when ctx is cancelled.
func (ac *AccessChecker) StartCacheSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(accessCacheTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				ac.cache.Range(func(key, val any) bool {
					if entry, ok := val.(accessCacheEntry); ok && now.After(entry.expiresAt) {
						ac.cache.Delete(key)
					}
					return true
				})
			}
		}
	}()
}

// sortedGroups returns a deterministic string representation of a groups slice
// for use as a cache key. Groups are sorted to ensure consistent keying.
func sortedGroups(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	sorted := make([]string, len(groups))
	copy(sorted, groups)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// apiGroupForResource returns the API group for common resource types.
func apiGroupForResource(resource string) string {
	switch resource {
	case "deployments", "statefulsets", "daemonsets", "replicasets":
		return "apps"
	case "jobs", "cronjobs":
		return "batch"
	case "ingresses", "networkpolicies":
		return "networking.k8s.io"
	case "roles", "clusterroles", "rolebindings", "clusterrolebindings":
		return "rbac.authorization.k8s.io"
	case "prometheusrules", "servicemonitors", "podmonitors", "alertmanagers", "alertmanagerconfigs":
		return "monitoring.coreos.com"
	case "ciliumnetworkpolicies", "ciliumclusterwidenetworkpolicies": // clusterwide prepared for future use
		return "cilium.io"
	default:
		return "" // core API group
	}
}
