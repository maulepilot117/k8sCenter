package auth

import (
	"context"
	"crypto/sha256"
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

const (
	rbacCacheTTL     = 60 * time.Second
	rbacCacheMaxSize = 1000
)

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
// Must include every kind the frontend renders in RESOURCE_COLUMNS and ACTIONS_BY_KIND.
var trackedResources = map[string]bool{
	// Core
	"pods": true, "services": true, "configmaps": true, "secrets": true,
	"namespaces": true, "nodes": true, "persistentvolumeclaims": true,
	"persistentvolumes": true, "endpoints": true, "events": true,
	"resourcequotas": true, "limitranges": true, "serviceaccounts": true,
	// Apps
	"deployments": true, "statefulsets": true, "daemonsets": true, "replicasets": true,
	// Batch
	"jobs": true, "cronjobs": true,
	// Networking
	"ingresses": true, "networkpolicies": true,
	// Policy
	"poddisruptionbudgets": true,
	// Autoscaling
	"horizontalpodautoscalers": true,
	// Storage
	"storageclasses": true,
	// RBAC
	"roles": true, "clusterroles": true, "rolebindings": true, "clusterrolebindings": true,
	// Discovery
	"endpointslices": true,
}

// RBACChecker queries Kubernetes RBAC permissions for users.
type RBACChecker struct {
	clientFactory interface {
		ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
	}
	mu     sync.Mutex
	cache  map[string]rbacCacheEntry // keyed by username
	logger *slog.Logger
}

// NewRBACChecker creates a new RBACChecker.
func NewRBACChecker(clientFactory interface {
	ClientForUser(username string, groups []string) (*kubernetes.Clientset, error)
}, logger *slog.Logger) *RBACChecker {
	return &RBACChecker{
		clientFactory: clientFactory,
		cache:         make(map[string]rbacCacheEntry),
		logger:        logger,
	}
}

// GetSummary returns a RBAC permission summary for the given user.
// Uses SelfSubjectRulesReview (1 API call per namespace) instead of
// SelfSubjectAccessReview (1 call per resource per verb per namespace).
// Results are cached for 60 seconds per user.
func (rc *RBACChecker) GetSummary(ctx context.Context, user *User, namespaces []string) (*RBACSummary, error) {
	cacheKey := rbacCacheKey(user.KubernetesUsername, user.KubernetesGroups)
	rc.mu.Lock()
	if entry, ok := rc.cache[cacheKey]; ok {
		if time.Now().Before(entry.expiresAt) {
			rc.mu.Unlock()
			return entry.summary, nil
		}
		delete(rc.cache, cacheKey)
	}
	rc.mu.Unlock()

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

	rc.mu.Lock()
	// Evict expired entries if cache is at capacity
	if len(rc.cache) >= rbacCacheMaxSize {
		now := time.Now()
		for k, v := range rc.cache {
			if now.After(v.expiresAt) {
				delete(rc.cache, k)
			}
		}
		// If still at capacity after evicting expired entries, clear the oldest half
		if len(rc.cache) >= rbacCacheMaxSize {
			count := 0
			for k := range rc.cache {
				delete(rc.cache, k)
				count++
				if count >= rbacCacheMaxSize/2 {
					break
				}
			}
		}
	}
	rc.cache[cacheKey] = rbacCacheEntry{
		summary:   summary,
		expiresAt: time.Now().Add(rbacCacheTTL),
	}
	rc.mu.Unlock()

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

// rbacCacheKey produces a cache key from username + groups to avoid serving
// stale permissions when groups change (e.g., OIDC token refresh with new groups).
func rbacCacheKey(username string, groups []string) string {
	sorted := make([]string, len(groups))
	copy(sorted, groups)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(username + "\x00" + strings.Join(sorted, "\x00")))
	return fmt.Sprintf("%x", h[:16])
}

// GetNamespacePermissions returns permissions for a single namespace plus cluster-scoped.
// This is more efficient than GetSummary for the common case (frontend requesting permissions
// for the currently selected namespace). Checks the full-summary cache first to avoid
// redundant API calls; falls back to fresh SelfSubjectRulesReview if not cached.
func (rc *RBACChecker) GetNamespacePermissions(ctx context.Context, user *User, namespace string) (*RBACSummary, error) {
	// Check existing full-summary cache — if we have a warm entry, extract the subset
	cacheKey := rbacCacheKey(user.KubernetesUsername, user.KubernetesGroups)
	rc.mu.Lock()
	if entry, ok := rc.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		rc.mu.Unlock()
		// Extract requested namespace + cluster-scoped from cached full summary
		result := &RBACSummary{
			ClusterScoped: entry.summary.ClusterScoped,
			Namespaces:    make(map[string]map[string][]string),
		}
		if nsPerms, ok := entry.summary.Namespaces[namespace]; ok {
			result.Namespaces[namespace] = nsPerms
		}
		return result, nil
	}
	rc.mu.Unlock()

	cs, err := rc.clientFactory.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, fmt.Errorf("creating client for RBAC check: %w", err)
	}

	summary := &RBACSummary{
		ClusterScoped: make(map[string][]string),
		Namespaces:    make(map[string]map[string][]string),
	}

	// Cluster-scoped permissions
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

	// Single namespace permissions
	if namespace != "" && !isSystemNamespace(namespace) {
		nsRules, err := rc.getRulesForNamespace(ctx, cs, namespace)
		if err != nil {
			rc.logger.Debug("failed to get rules for namespace", "namespace", namespace, "error", err)
		} else {
			nsPerms := make(map[string][]string)
			for resource, verbs := range nsRules {
				if len(verbs) > 0 {
					nsPerms[resource] = verbs
				}
			}
			if len(nsPerms) > 0 {
				summary.Namespaces[namespace] = nsPerms
			}
		}
	}

	return summary, nil
}
