package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

// systemNamespacePrefixes are namespaces filtered from RBAC summaries.
var systemNamespacePrefixes = []string{"kube-"}

// trackedResources are the resource types we check permissions for.
var trackedResources = map[string]bool{
	"pods": true, "deployments": true, "services": true,
	"configmaps": true, "secrets": true, "ingresses": true,
	"statefulsets": true, "daemonsets": true, "jobs": true,
	"networkpolicies": true, "nodes": true, "namespaces": true,
	"clusterroles": true, "clusterrolebindings": true,
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
// Uses SelfSubjectRulesReview (1 API call per namespace) instead of
// SelfSubjectAccessReview (1 call per resource per verb per namespace).
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

	// Check cluster-scoped permissions via empty-namespace rules review
	clusterRules, err := rc.getRulesForNamespace(ctx, cs, "")
	if err != nil {
		rc.logger.Warn("failed to get cluster-scoped rules", "error", err, "user", user.Username)
	} else {
		for resource, verbs := range clusterRules {
			if len(verbs) > 0 {
				summary.ClusterScoped[resource] = verbs
			}
		}
	}

	// Check namespace-scoped permissions, filtering system namespaces
	for _, ns := range namespaces {
		if isSystemNamespace(ns) {
			continue
		}

		nsRules, err := rc.getRulesForNamespace(ctx, cs, ns)
		if err != nil {
			rc.logger.Debug("failed to get rules for namespace", "namespace", ns, "error", err)
			continue
		}

		nsPerms := make(map[string][]string)
		for resource, verbs := range nsRules {
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

// getRulesForNamespace uses SelfSubjectRulesReview to get all permissions
// in a single API call per namespace.
func (rc *RBACChecker) getRulesForNamespace(ctx context.Context, cs *kubernetes.Clientset, namespace string) (map[string][]string, error) {
	review := &authorizationv1.SelfSubjectRulesReview{
		Spec: authorizationv1.SelfSubjectRulesReviewSpec{
			Namespace: namespace,
		},
	}

	result, err := cs.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("SelfSubjectRulesReview for namespace %q: %w", namespace, err)
	}

	perms := make(map[string][]string)

	for _, rule := range result.Status.ResourceRules {
		for _, resource := range rule.Resources {
			if !trackedResources[resource] {
				continue
			}
			for _, verb := range rule.Verbs {
				perms[resource] = appendUnique(perms[resource], verb)
			}
		}
	}

	return perms, nil
}

func isSystemNamespace(ns string) bool {
	for _, prefix := range systemNamespacePrefixes {
		if strings.HasPrefix(ns, prefix) {
			return true
		}
	}
	return false
}

func appendUnique(slice []string, val string) []string {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
