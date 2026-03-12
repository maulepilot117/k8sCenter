package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/pkg/api"
)

const (
	defaultRate    = 5           // requests per window
	defaultWindow  = time.Minute // sliding window
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

// Allow checks if the given IP is within rate limits.
// Returns true if the request is allowed, false if rate-limited.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.Sub(b.lastReset) >= rl.window {
		rl.buckets[ip] = &bucket{tokens: 1, lastReset: now}
		return true
	}

	b.tokens++
	return b.tokens <= rl.rate
}

// RetryAfter returns the number of seconds until the rate limit resets for an IP.
func (rl *RateLimiter) RetryAfter(ip string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok {
		return 0
	}

	remaining := rl.window - time.Since(b.lastReset)
	if remaining <= 0 {
		return 0
	}
	return int(remaining.Seconds()) + 1
}

// StartCleanup removes stale entries periodically.
func (rl *RateLimiter) StartCleanup(done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
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

// RateLimit returns middleware that applies rate limiting.
// It extracts the client IP from X-Real-IP (set by chi RealIP middleware).
func RateLimit(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr

			if !limiter.Allow(ip) {
				retryAfter := limiter.RetryAfter(ip)
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
