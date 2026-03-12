package middleware

import (
	"testing"
)

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter()

	for i := 0; i < 5; i++ {
		allowed, _ := rl.Check("192.168.1.1")
		if !allowed {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter()

	// Use up the limit
	for i := 0; i < 5; i++ {
		rl.Check("192.168.1.1")
	}

	// 6th request should be blocked
	allowed, _ := rl.Check("192.168.1.1")
	if allowed {
		t.Error("6th request should be rate limited")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := NewRateLimiter()

	// Exhaust IP 1
	for i := 0; i < 5; i++ {
		rl.Check("192.168.1.1")
	}
	allowed, _ := rl.Check("192.168.1.1")
	if allowed {
		t.Error("IP 1 should be rate limited")
	}

	// IP 2 should still be allowed
	allowed, _ = rl.Check("192.168.1.2")
	if !allowed {
		t.Error("IP 2 should not be rate limited")
	}
}

func TestRateLimiter_RetryAfter(t *testing.T) {
	rl := NewRateLimiter()

	// Exhaust the limit
	for i := 0; i < 6; i++ {
		rl.Check("192.168.1.1")
	}

	_, retryAfter := rl.Check("192.168.1.1")
	if retryAfter <= 0 || retryAfter > 61 {
		t.Errorf("expected retry after between 1 and 61 seconds, got %d", retryAfter)
	}
}

func TestRateLimiter_RetryAfter_UnknownIP(t *testing.T) {
	rl := NewRateLimiter()

	allowed, retryAfter := rl.Check("unknown-ip")
	if !allowed {
		t.Error("unknown IP should be allowed")
	}
	if retryAfter != 0 {
		t.Errorf("expected 0 retry after for unknown IP, got %d", retryAfter)
	}
}
