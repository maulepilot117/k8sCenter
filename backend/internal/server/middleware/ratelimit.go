package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const (
	defaultRate     = 5           // requests per window
	defaultWindow   = time.Minute // sliding window
	cleanupInterval = 5 * time.Minute
)

type bucket struct {
	tokens    int
	lastReset time.Time
}

// RateLimiter tracks request counts per IP using a fixed-window algorithm.
//
// Optional [audit.Logger] hook surfaces 429 rejections in the audit table.
// Set via [RateLimiter.SetAuditLogger] at server construction; nil logger
// means rate-limit hits are silent (the legacy behavior).
type RateLimiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	rate        int
	window      time.Duration
	auditLogger audit.Logger
}

// NewRateLimiter creates a rate limiter with default settings (5 req/min).
func NewRateLimiter() *RateLimiter {
	return NewRateLimiterWithRate(defaultRate, defaultWindow)
}

// NewRateLimiterWithRate creates a rate limiter with the specified rate and window.
func NewRateLimiterWithRate(rate int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		window:  window,
	}
}

// SetAuditLogger attaches an audit logger so 429 rejections emit
// [audit.ActionRateLimited] entries. Issue #276 — without this, brute-force
// probing of rate-limited endpoints leaves no trace in the audit table.
// Safe to call once at startup; not safe to call concurrently with
// requests in flight (no lock — kept lock-free because typical wiring is
// "construct + SetAuditLogger + start serving").
func (rl *RateLimiter) SetAuditLogger(logger audit.Logger) {
	rl.auditLogger = logger
}

// Check tests if the given IP is within rate limits and returns the retry-after
// duration in seconds if rate-limited. Both values are computed under a single
// lock acquisition to avoid race conditions.
func (rl *RateLimiter) Check(ip string) (allowed bool, retryAfterSec int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.Sub(b.lastReset) >= rl.window {
		rl.buckets[ip] = &bucket{tokens: 1, lastReset: now}
		return true, 0
	}

	b.tokens++
	if b.tokens <= rl.rate {
		return true, 0
	}

	remaining := rl.window - now.Sub(b.lastReset)
	if remaining <= 0 {
		return true, 0
	}
	return false, int(remaining.Seconds()) + 1
}

// StartCleanup removes stale entries periodically until the context is cancelled.
func (rl *RateLimiter) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rl.cleanup()
			}
		}
	}()
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, b := range rl.buckets {
		if now.Sub(b.lastReset) >= rl.window*2 {
			delete(rl.buckets, ip)
		}
	}
}

// extractIP parses the IP address from r.RemoteAddr, stripping the port.
// chi's RealIP middleware overwrites RemoteAddr with the client IP from
// X-Real-IP or X-Forwarded-For headers.
func extractIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may already be just an IP (no port)
		return r.RemoteAddr
	}
	return host
}

// RateLimit returns middleware that applies rate limiting per client IP.
// When the bucket is exhausted, the middleware emits an audit entry
// (if the limiter has an audit logger attached via [RateLimiter.SetAuditLogger])
// before returning 429. Audit-log writes do NOT block the response — they
// fail silently in the same request goroutine; a slow audit backend would
// only slow down already-rejected requests, never legitimate ones.
func RateLimit(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractIP(r)

			allowed, retryAfter := limiter.Check(ip)
			if !allowed {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(api.Response{
					Error: &api.APIError{
						Code:    429,
						Message: "rate limit exceeded",
						Detail:  fmt.Sprintf("try again in %d seconds", retryAfter),
					},
				})
				if logger := limiter.auditLogger; logger != nil {
					// Best-effort audit write. We deliberately ignore the
					// return value — if the audit backend is down, the
					// 429 response has already been sent and there's
					// nothing useful to do about a logging failure.
					_ = logger.Log(r.Context(), audit.Entry{
						Timestamp: time.Now(),
						SourceIP:  ip,
						Action:    audit.ActionRateLimited,
						Result:    audit.ResultDenied,
						Detail:    fmt.Sprintf("%s %s: rate limited (retry %ds)", r.Method, r.URL.Path, retryAfter),
					})
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
