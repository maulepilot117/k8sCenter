package certmanager

import (
	"context"
	"errors"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
)

// ErrCertManagerNotInstalled is returned by CertExpiryAdapter.ExpiringCounts
// when the Discoverer reports that cert-manager is not detected in the cluster.
// Callers should map this to a non-fatal "skipped" signal rather than an error.
var ErrCertManagerNotInstalled = errors.New("cert-manager not installed")

// ErrCacheNotWarm is returned by CertExpiryAdapter.ExpiringCounts when the
// local certificate cache has not yet been populated (cold start or TTL
// expired). No API fetch is triggered. Callers should map this to a non-fatal
// "skipped" signal rather than an error.
var ErrCacheNotWarm = errors.New("cert cache warming")

// CertExpiryAdapter implements resources.CertExpiryCounter using the certmanager
// Handler's local cache. It never enters the singleflight fetch path — if the
// cache is cold, it returns ErrCacheNotWarm immediately.
type CertExpiryAdapter struct {
	Handler *Handler
}

// ExpiringCounts returns the number of certificates that fall into the warning
// and critical expiry buckets for the calling user's accessible namespaces.
//
// Error cases (both non-fatal — callers should treat as skipped signals):
//   - ErrCertManagerNotInstalled — Discoverer reports cert-manager absent.
//   - ErrCacheNotWarm — local cache not yet populated; no fetch is triggered.
func (a *CertExpiryAdapter) ExpiringCounts(ctx context.Context, user *auth.User) (warning, critical int, err error) {
	if !a.Handler.Discoverer.IsAvailable(ctx) {
		return 0, 0, ErrCertManagerNotInstalled
	}

	certs, ok := a.Handler.peekCertificates()
	if !ok {
		return 0, 0, ErrCacheNotWarm
	}

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
// mutex and returns (certs, true) when a fresh entry exists, or (nil, false)
// when the cache is absent or expired. It never enters the singleflight fetch
// path — use getCached when a fetch is acceptable.
func (h *Handler) peekCertificates() ([]Certificate, bool) {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()
	if h.cache != nil && time.Since(h.cache.fetchedAt) < cacheTTL {
		return h.cache.certificates, true
	}
	return nil, false
}
