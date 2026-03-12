package auth

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

const rbacCacheTTL = 60 * time.Second

// RBACSummary describes a user's permissions across the cluster.
type RBACSummary struct {
	ClusterScoped map[string][]string            `json:"clusterScoped"`
	Namespaces    map[string]map[string][]string `json:"namespaces"`
}

type rbacCacheEntry struct {
	summary   *RBACSummary
	expiresAt time.Time
}

// RBACChecker queries Kubernetes RBAC permissions for users.
type RBACChecker struct {
	clientFactory interface {
		ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
	}
	cache  sync.Map // map[string]rbacCacheEntry (keyed by username)
	logger *slog.Logger
}

// NewRBACChecker creates a new RBACChecker.
func NewRBACChecker(clientFactory interface {
	ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
}, logger *slog.Logger) *RBACChecker {
	return &RBACChecker{
		clientFactory: clientFactory,
		logger:        logger,
	}
}

// GetSummary returns a RBAC permission summary for the given user.
// Results are cached for 60 seconds per user.
func (rc *RBACChecker) GetSummary(ctx context.Context, user *User, namespaces []string) (*RBACSummary, error) {
	if val, ok := rc.cache.Load(user.Username); ok {
		entry := val.(rbacCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.summary, nil
		}
		rc.cache.Delete(user.Username)
	}

	cs, err := rc.clientFactory.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, fmt.Errorf("creating client for RBAC check: %w", err)
	}

	summary := &RBACSummary{
		ClusterScoped: make(map[string][]string),
		Namespaces:    make(map[string]map[string][]string),
	}

	// Check cluster-scoped resources
	clusterResources := []string{"nodes", "namespaces", "clusterroles", "clusterrolebindings"}
	for _, resource := range clusterResources {
		verbs := rc.checkVerbs(ctx, cs, "", resource)
		if len(verbs) > 0 {
			summary.ClusterScoped[resource] = verbs
		}
	}

	// Check namespace-scoped resources
	nsResources := []string{"pods", "deployments", "services", "configmaps", "secrets", "ingresses", "statefulsets", "daemonsets", "jobs", "networkpolicies"}
	for _, ns := range namespaces {
		nsPerms := make(map[string][]string)
		for _, resource := range nsResources {
			verbs := rc.checkVerbs(ctx, cs, ns, resource)
			if len(verbs) > 0 {
				nsPerms[resource] = verbs
			}
		}
		if len(nsPerms) > 0 {
			summary.Namespaces[ns] = nsPerms
		}
	}

	rc.cache.Store(user.Username, rbacCacheEntry{
		summary:   summary,
		expiresAt: time.Now().Add(rbacCacheTTL),
	})

	return summary, nil
}

// checkVerbs checks which verbs a user has for a resource in a namespace.
func (rc *RBACChecker) checkVerbs(ctx context.Context, cs *kubernetes.Clientset, namespace, resource string) []string {
	verbs := []string{"get", "list", "create", "update", "delete"}
	var allowed []string

	for _, verb := range verbs {
		sar := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: namespace,
					Verb:      verb,
					Resource:  resource,
				},
			},
		}

		result, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
		if err != nil {
			rc.logger.Debug("RBAC check failed", "resource", resource, "verb", verb, "namespace", namespace, "error", err)
			continue
		}
		if result.Status.Allowed {
			allowed = append(allowed, verb)
		}
	}

	return allowed
}
