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
)

const accessCacheTTL = 60 * time.Second

type accessCacheKey struct {
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
type AccessChecker struct {
	clientFactory interface {
		ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
	}
	cache       sync.Map // map[accessCacheKey]accessCacheEntry
	logger      *slog.Logger
	alwaysAllow bool // for testing only
	alwaysDeny  bool // for testing only
}

// NewAccessChecker creates an AccessChecker that verifies user permissions.
func NewAccessChecker(clientFactory interface {
	ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
}, logger *slog.Logger) *AccessChecker {
	return &AccessChecker{
		clientFactory: clientFactory,
		logger:        logger,
	}
}

// CanAccess checks if a user has a specific verb permission on a resource in a namespace.
// Empty namespace means cluster-scoped check.
func (ac *AccessChecker) CanAccess(ctx context.Context, username string, groups []string, verb, resource, namespace string) (bool, error) {
	if ac.alwaysAllow {
		return true, nil
	}
	if ac.alwaysDeny {
		return false, nil
	}
	key := accessCacheKey{
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

	cs, err := ac.clientFactory.ClientForUser(username, groups)
	if err != nil {
		return false, fmt.Errorf("creating client for access check: %w", err)
	}

	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Resource:  resource,
				Group:     apiGroupForResource(resource),
			},
		},
	}

	result, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("SelfSubjectAccessReview for %s/%s in %q: %w", verb, resource, namespace, err)
	}

	allowed := result.Status.Allowed
	ac.cache.Store(key, accessCacheEntry{
		allowed:   allowed,
		expiresAt: time.Now().Add(accessCacheTTL),
	})

	ac.logger.Debug("access check",
		"user", username,
		"verb", verb,
		"resource", resource,
		"namespace", namespace,
		"allowed", allowed,
	)

	return allowed, nil
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
	case "deployments", "statefulsets", "daemonsets":
		return "apps"
	case "jobs", "cronjobs":
		return "batch"
	case "ingresses", "networkpolicies":
		return "networking.k8s.io"
	case "roles", "clusterroles", "rolebindings", "clusterrolebindings":
		return "rbac.authorization.k8s.io"
	case "prometheusrules", "servicemonitors", "podmonitors", "alertmanagerconfigs":
		return "monitoring.coreos.com"
	default:
		return "" // core API group
	}
}
