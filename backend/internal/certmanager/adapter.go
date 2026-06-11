package certmanager

import (
	"context"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// peekStaleness is the maximum age of a cached entry that peekCertificates
// will still treat as warm. It must exceed the poller's 60-second tick so that
// a steady-state peek always finds a warm entry; the 30-second cacheTTL still
// governs the singleflight fetch path.
const peekStaleness = 90 * time.Second

// ErrCertManagerNotInstalled is returned by CertExpiryAdapter.ExpiringCounts
// when the Discoverer reports that cert-manager is not detected in the cluster.
// Callers should map this to a non-fatal "skipped" signal rather than an error.
// Aliases the sentinel defined in resources (the consumer package) so callers
// there can errors.Is-match without importing certmanager.
var ErrCertManagerNotInstalled = resources.ErrCertManagerNotInstalled

// ErrCertCacheNotWarm is returned by CertExpiryAdapter.ExpiringCounts when the
// local certificate cache has not yet been populated (cold start or TTL
// expired). No API fetch is triggered. Callers should map this to a non-fatal
// "skipped" signal rather than an error.
var ErrCertCacheNotWarm = resources.ErrCertCacheNotWarm

// CertExpiryAdapter implements resources.CertExpiryCounter using the certmanager
// Handler's local cache. It never enters the singleflight fetch path — if the
// cache is cold, it returns ErrCertCacheNotWarm immediately.
type CertExpiryAdapter struct {
	Handler *Handler
}

// ExpiringCounts returns the number of certificates that fall into the warning
// and critical expiry buckets for the calling user's accessible namespaces.
//
// Error cases (both non-fatal — callers should treat as skipped signals):
//   - ErrCertManagerNotInstalled — Discoverer reports cert-manager absent.
//   - ErrCertCacheNotWarm — local cache not yet populated; no fetch is triggered.
func (a *CertExpiryAdapter) ExpiringCounts(ctx context.Context, user *auth.User) (warning, critical int, err error) {
	if !a.Handler.Discoverer.IsAvailable(ctx) {
		return 0, 0, ErrCertManagerNotInstalled
	}

	certs, ok := a.Handler.peekCertificates()
	if !ok {
		return 0, 0, ErrCertCacheNotWarm
	}

	// Cluster-wide fast path: if the user can list certificates across all
	// namespaces, skip per-namespace SubjectAccessReviews (which issue one SAR
	// per distinct namespace — 100–400 ms on a cold SAR cache with many
	// namespaces) and bucket the full cached list directly.
	if a.Handler.canAccess(ctx, user, "list", "certificates", "") {
		for _, cert := range certs {
			switch thresholdBucket(cert) {
			case thresholdWarning:
				warning++
			case thresholdCritical, thresholdExpired:
				critical++
			}
		}
		return warning, critical, nil
	}

	// Per-namespace fallback: filter by RBAC and bucket.
	filtered := filterByRBAC(ctx, a.Handler, user, "certificates", certs)

	for _, cert := range filtered {
		switch thresholdBucket(cert) {
		case thresholdWarning:
			warning++
		case thresholdCritical, thresholdExpired:
			critical++
		}
	}
	return warning, critical, nil
}

// peekCertificates reads the current local certificate cache under the read
// mutex and returns (certs, true) when the entry exists and is within the
// peekStaleness window, or (nil, false) when the cache is absent or too stale.
// It never enters the singleflight fetch path — use getCached when a fetch is
// acceptable. peekStaleness (90s) is intentionally larger than cacheTTL (30s)
// so a steady-state peek (poller fires every 60s) is always warm.
func (h *Handler) peekCertificates() ([]Certificate, bool) {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()
	if h.cache != nil && time.Since(h.cache.fetchedAt) < peekStaleness {
		return h.cache.certificates, true
	}
	return nil, false
}
