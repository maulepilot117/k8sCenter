package auth

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestRBACChecker_CacheHit(t *testing.T) {
	factory := &fakeClientFactory{}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	user := &User{Username: "test-user", KubernetesUsername: "test-user"}

	// First call should populate cache
	_, err := checker.GetSummary(context.Background(), user, []string{"default"})
	if err != nil {
		t.Fatalf("first GetSummary failed: %v", err)
	}
	callCount1 := factory.callCount

	// Second call should hit cache (no additional API call)
	_, err = checker.GetSummary(context.Background(), user, []string{"default"})
	if err != nil {
		t.Fatalf("second GetSummary failed: %v", err)
	}
	if factory.callCount != callCount1 {
		t.Errorf("expected cache hit (no new API call), got %d calls (was %d)", factory.callCount, callCount1)
	}
}

func TestRBACChecker_CacheExpiry(t *testing.T) {
	factory := &fakeClientFactory{}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	user := &User{Username: "test-user", KubernetesUsername: "test-user"}

	// Populate cache
	_, err := checker.GetSummary(context.Background(), user, []string{})
	if err != nil {
		t.Fatalf("first GetSummary failed: %v", err)
	}

	// Manually expire the cache entry using the hash key
	cacheKey := rbacCacheKey(user.KubernetesUsername, user.KubernetesGroups)
	checker.mu.Lock()
	if entry, ok := checker.cache[cacheKey]; ok {
		entry.expiresAt = time.Now().Add(-1 * time.Second)
		checker.cache[cacheKey] = entry
	}
	checker.mu.Unlock()

	callsBefore := factory.callCount

	// Next call should miss cache (expired)
	_, err = checker.GetSummary(context.Background(), user, []string{})
	if err != nil {
		t.Fatalf("second GetSummary failed: %v", err)
	}
	if factory.callCount == callsBefore {
		t.Error("expected cache miss after expiry, but no new API call was made")
	}
}

func TestRBACChecker_DifferentUsersSeparateCacheEntries(t *testing.T) {
	factory := &fakeClientFactory{}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	user1 := &User{Username: "user1", KubernetesUsername: "user1"}
	user2 := &User{Username: "user2", KubernetesUsername: "user2"}

	_, err := checker.GetSummary(context.Background(), user1, []string{})
	if err != nil {
		t.Fatalf("user1 GetSummary failed: %v", err)
	}
	calls1 := factory.callCount

	_, err = checker.GetSummary(context.Background(), user2, []string{})
	if err != nil {
		t.Fatalf("user2 GetSummary failed: %v", err)
	}
	if factory.callCount == calls1 {
		t.Error("expected separate cache entry for user2, but no new API call was made")
	}
}

func TestRBACChecker_ConcurrentAccess(t *testing.T) {
	factory := &fakeClientFactory{}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			user := &User{
				Username:           "concurrent-user",
				KubernetesUsername: "concurrent-user",
			}
			_, err := checker.GetSummary(context.Background(), user, []string{"default"})
			if err != nil {
				t.Errorf("concurrent GetSummary %d failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestRBACChecker_SystemNamespaceFiltered(t *testing.T) {
	if !isSystemNamespace("kube-system") {
		t.Error("expected kube-system to be a system namespace")
	}
	if !isSystemNamespace("kube-public") {
		t.Error("expected kube-public to be a system namespace")
	}
	if isSystemNamespace("default") {
		t.Error("expected default to NOT be a system namespace")
	}
	if isSystemNamespace("production") {
		t.Error("expected production to NOT be a system namespace")
	}
}

func TestAppendUnique(t *testing.T) {
	result := appendUnique([]string{"get", "list"}, "get")
	if len(result) != 2 {
		t.Errorf("expected no duplicate, got %v", result)
	}

	result = appendUnique([]string{"get", "list"}, "create")
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %v", result)
	}
}

func TestGetNamespacePermissions_UsesCacheWhenAvailable(t *testing.T) {
	factory := &fakeClientFactory{}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	user := &User{Username: "test-user", KubernetesUsername: "test-user"}

	// Populate the full-summary cache
	_, err := checker.GetSummary(context.Background(), user, []string{"default"})
	if err != nil {
		t.Fatalf("GetSummary failed: %v", err)
	}

	callsBefore := factory.callCount

	// GetNamespacePermissions should use the cached full summary, not make new API calls
	result, err := checker.GetNamespacePermissions(context.Background(), user, "default")
	if err != nil {
		t.Fatalf("GetNamespacePermissions failed: %v", err)
	}

	if factory.callCount != callsBefore {
		t.Errorf("expected cache hit (no new API calls), but callCount went from %d to %d",
			callsBefore, factory.callCount)
	}

	if result.ClusterScoped == nil {
		t.Error("expected non-nil ClusterScoped in result")
	}
}

func TestGetNamespacePermissions_SystemNamespaceSkipped(t *testing.T) {
	factory := &fakeClientFactory{}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	user := &User{Username: "test-user", KubernetesUsername: "test-user"}

	result, err := checker.GetNamespacePermissions(context.Background(), user, "kube-system")
	if err != nil {
		t.Fatalf("GetNamespacePermissions failed: %v", err)
	}

	// System namespace should be skipped — no namespace permissions returned
	if len(result.Namespaces) != 0 {
		t.Errorf("expected no namespace permissions for kube-system, got %v", result.Namespaces)
	}
}

// fakeClientFactory returns a fake clientset for testing.
// The clientset's AuthorizationV1 will fail on actual API calls,
// but that's fine — we're testing cache behavior, not the API call itself.
type fakeClientFactory struct {
	mu        sync.Mutex
	callCount int
}

func (f *fakeClientFactory) ClientForUser(username string, groups []string) (*kubernetes.Clientset, error) {
	f.mu.Lock()
	f.callCount++
	f.mu.Unlock()
	// Return a fake clientset — SelfSubjectRulesReview will fail but GetSummary
	// handles errors gracefully (logs warning, continues with empty results)
	return kubernetes.NewForConfigOrDie(&rest.Config{Host: "https://fake:6443"}), nil
}
