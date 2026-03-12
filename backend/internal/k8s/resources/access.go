package resources

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const accessCacheTTL = 60 * time.Second

type accessCacheKey struct {
	username  string
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
	key := accessCacheKey{
		username:  username,
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
	default:
		return "" // core API group
	}
}
