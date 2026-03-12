package middleware

import (
	"testing"
)

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter()

	for i := 0; i < 5; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter()

	// Use up the limit
	for i := 0; i < 5; i++ {
		rl.Allow("192.168.1.1")
	}

	// 6th request should be blocked
	if rl.Allow("192.168.1.1") {
		t.Error("6th request should be rate limited")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := NewRateLimiter()

	// Exhaust IP 1
	for i := 0; i < 5; i++ {
		rl.Allow("192.168.1.1")
	}
	if rl.Allow("192.168.1.1") {
		t.Error("IP 1 should be rate limited")
	}

	// IP 2 should still be allowed
	if !rl.Allow("192.168.1.2") {
		t.Error("IP 2 should not be rate limited")
	}
}

func TestRateLimiter_RetryAfter(t *testing.T) {
	rl := NewRateLimiter()

	// Exhaust the limit
	for i := 0; i < 6; i++ {
		rl.Allow("192.168.1.1")
	}

	retryAfter := rl.RetryAfter("192.168.1.1")
	if retryAfter <= 0 || retryAfter > 61 {
		t.Errorf("expected retry after between 1 and 61 seconds, got %d", retryAfter)
	}
}

func TestRateLimiter_RetryAfter_UnknownIP(t *testing.T) {
	rl := NewRateLimiter()

	retryAfter := rl.RetryAfter("unknown-ip")
	if retryAfter != 0 {
		t.Errorf("expected 0 retry after for unknown IP, got %d", retryAfter)
	}
}
