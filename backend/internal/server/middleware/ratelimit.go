package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

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
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    int
	window  time.Duration
}

// NewRateLimiter creates a rate limiter with default settings (5 req/min).
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    defaultRate,
		window:  defaultWindow,
	}
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
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
