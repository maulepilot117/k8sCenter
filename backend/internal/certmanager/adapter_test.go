package certmanager

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"k8s.io/client-go/kubernetes"
)

// newTestHandler returns a minimal Handler for adapter tests. The AccessChecker
// controls RBAC; the cache field is set directly by each test.
func newTestHandler(ac *resources.AccessChecker) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := k8s.NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, nil)
	return &Handler{
		K8sClient:     factory,
		Discoverer:    newAvailableDiscoverer(),
		AccessChecker: ac,
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
	}
}

// newUnavailableDiscoverer returns a Discoverer that reports cert-manager as
// not installed (Detected=false).
func newUnavailableDiscoverer() *Discoverer {
	d := NewDiscoverer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Leave d.status at zero value — Detected=false, IsAvailable returns false.
	return d
}

// seedCache writes a warm, non-expired cachedData into h directly, bypassing
// getCached. This lets tests control the cache state without triggering a real
// API fetch.
func seedCache(h *Handler, certs []Certificate) {
	h.cacheMu.Lock()
	h.cache = &cachedData{
		certificates: certs,
		fetchedAt:    time.Now(),
	}
	h.cacheMu.Unlock()
}

// --- Test scenarios -----------------------------------------------------------

// TestCertExpiryAdapter_WarmCache_BucketCounts verifies that warning and
// critical counts are tallied correctly from a seeded warm cache.
func TestCertExpiryAdapter_WarmCache_BucketCounts(t *testing.T) {
	h := newTestHandler(resources.NewAlwaysAllowAccessChecker())
	seedCache(h, []Certificate{
		// 60 days → none
		{Name: "safe", Namespace: "default", DaysRemaining: intPtr(60)},
		// 20 days → warning (default warn=30)
		{Name: "warn1", Namespace: "default", DaysRemaining: intPtr(20)},
		// 10 days → warning
		{Name: "warn2", Namespace: "default", DaysRemaining: intPtr(10)},
		// 5 days → critical (default crit=7)
		{Name: "crit1", Namespace: "default", DaysRemaining: intPtr(5)},
		// 0 days → critical
		{Name: "crit2", Namespace: "default", DaysRemaining: intPtr(0)},
		// -1 days → expired (counted as critical)
		{Name: "expired", Namespace: "default", DaysRemaining: intPtr(-1)},
	})

	adapter := &CertExpiryAdapter{Handler: h}
	user := testUser()

	got_warn, got_crit, err := adapter.ExpiringCounts(context.Background(), user)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got_warn != 2 {
		t.Errorf("warning = %d, want 2", got_warn)
	}
	if got_crit != 3 {
		t.Errorf("critical = %d, want 3 (2 critical + 1 expired)", got_crit)
	}
}

// TestCertExpiryAdapter_EmptyWarmCache verifies that a warm but empty cache
// returns 0, 0, nil — not an error.
func TestCertExpiryAdapter_EmptyWarmCache(t *testing.T) {
	h := newTestHandler(resources.NewAlwaysAllowAccessChecker())
	seedCache(h, []Certificate{})

	adapter := &CertExpiryAdapter{Handler: h}
	warn, crit, err := adapter.ExpiringCounts(context.Background(), testUser())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != 0 || crit != 0 {
		t.Errorf("got warn=%d crit=%d, want 0, 0", warn, crit)
	}
}

// TestCertExpiryAdapter_ColdCache verifies that an absent cache returns
// ErrCacheNotWarm and does NOT trigger any API fetch (no K8s client wired).
func TestCertExpiryAdapter_ColdCache(t *testing.T) {
	h := newTestHandler(resources.NewAlwaysAllowAccessChecker())
	// Deliberately do NOT seed the cache — leave h.cache nil.

	adapter := &CertExpiryAdapter{Handler: h}
	_, _, err := adapter.ExpiringCounts(context.Background(), testUser())
	if err != ErrCacheNotWarm {
		t.Errorf("err = %v, want ErrCacheNotWarm", err)
	}
}

// TestCertExpiryAdapter_ExpiredCache_ReturnsNotWarm verifies that a cache
// entry whose fetchedAt is beyond cacheTTL is treated as cold (not warm).
func TestCertExpiryAdapter_ExpiredCache_ReturnsNotWarm(t *testing.T) {
	h := newTestHandler(resources.NewAlwaysAllowAccessChecker())
	h.cacheMu.Lock()
	h.cache = &cachedData{
		certificates: []Certificate{{Name: "old", Namespace: "default", DaysRemaining: intPtr(5)}},
		fetchedAt:    time.Now().Add(-(cacheTTL + time.Second)), // expired
	}
	h.cacheMu.Unlock()

	adapter := &CertExpiryAdapter{Handler: h}
	_, _, err := adapter.ExpiringCounts(context.Background(), testUser())
	if err != ErrCacheNotWarm {
		t.Errorf("err = %v, want ErrCacheNotWarm for stale cache", err)
	}
}

// TestCertExpiryAdapter_NotInstalled verifies that a non-detected Discoverer
// returns ErrCertManagerNotInstalled.
func TestCertExpiryAdapter_NotInstalled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := k8s.NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, nil)
	h := &Handler{
		K8sClient:     factory,
		Discoverer:    newUnavailableDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
	}
	// Seed a warm cache — the discovery check must fire before the peek.
	seedCache(h, []Certificate{{Name: "cert", Namespace: "default", DaysRemaining: intPtr(5)}})

	adapter := &CertExpiryAdapter{Handler: h}
	_, _, err := adapter.ExpiringCounts(context.Background(), testUser())
	if err != ErrCertManagerNotInstalled {
		t.Errorf("err = %v, want ErrCertManagerNotInstalled", err)
	}
}

// TestCertExpiryAdapter_PerUserFiltering verifies that a user who is denied
// access (AlwaysDeny) sees 0, 0 even when the cache has expiring certificates.
func TestCertExpiryAdapter_PerUserFiltering(t *testing.T) {
	h := newTestHandler(resources.NewAlwaysDenyAccessChecker())
	seedCache(h, []Certificate{
		{Name: "crit1", Namespace: "default", DaysRemaining: intPtr(3)},
		{Name: "warn1", Namespace: "default", DaysRemaining: intPtr(20)},
	})

	adapter := &CertExpiryAdapter{Handler: h}
	// restrictedUser is denied access to all namespaces by AlwaysDenyAccessChecker.
	restrictedUser := &auth.User{
		ID:                 "restricted",
		Username:           "bob",
		KubernetesUsername: "bob",
		KubernetesGroups:   []string{},
		Roles:              []string{},
	}
	warn, crit, err := adapter.ExpiringCounts(context.Background(), restrictedUser)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != 0 || crit != 0 {
		t.Errorf("restricted user got warn=%d crit=%d, want 0, 0", warn, crit)
	}
}

// TestCertExpiryAdapter_PerUserFiltering_PartialAccess verifies that a user
// who can only access one namespace sees counts only from that namespace.
// Uses NewPredicateAccessChecker so we can whitelist a single namespace.
func TestCertExpiryAdapter_PerUserFiltering_PartialAccess(t *testing.T) {
	// Only allow access to the "allowed-ns" namespace.
	ac := resources.NewPredicateAccessChecker(func(_, _, _, namespace string) bool {
		return namespace == "allowed-ns"
	})
	h := newTestHandler(ac)
	seedCache(h, []Certificate{
		// In allowed namespace — should count.
		{Name: "crit-allowed", Namespace: "allowed-ns", DaysRemaining: intPtr(3)},
		// In denied namespace — should be filtered out.
		{Name: "warn-denied", Namespace: "denied-ns", DaysRemaining: intPtr(20)},
	})

	adapter := &CertExpiryAdapter{Handler: h}
	warn, crit, err := adapter.ExpiringCounts(context.Background(), testUser())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != 0 {
		t.Errorf("warning = %d, want 0", warn)
	}
	if crit != 1 {
		t.Errorf("critical = %d, want 1 (only allowed-ns cert counted)", crit)
	}
}

// TestPeekCertificates_NeverFetches verifies that peekCertificates with a cold
// cache returns (nil, false) and does NOT trigger a fetch. We confirm no fetch
// occurred by observing that no K8s calls were made (the nil dynamic client
// would panic if a list were attempted).
func TestPeekCertificates_NeverFetches(t *testing.T) {
	h := newTestHandler(resources.NewAlwaysAllowAccessChecker())
	// Cache is cold — no seedCache call.

	certs, ok := h.peekCertificates()
	if ok {
		t.Errorf("peekCertificates returned ok=true on cold cache")
	}
	if certs != nil {
		t.Errorf("peekCertificates returned non-nil certs on cold cache: %v", certs)
	}
	// If a fetch had been triggered, the nil dynamic client inside newTestHandler
	// would have panicked. Reaching this line proves no fetch happened.
}
