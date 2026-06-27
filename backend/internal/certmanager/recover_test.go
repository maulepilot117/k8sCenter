package certmanager

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

// TestPollerRunTickWithRecover_SwallowsPanic verifies that a panic inside a
// poll cycle does not unwind the poller goroutine. newPollerForTest has a nil
// Discoverer, so tick() panics on the first p.disc.IsAvailable() call. If
// runTickWithRecover did not recover, this test binary would crash instead of
// passing — that is the regression guard.
func TestPollerRunTickWithRecover_SwallowsPanic(t *testing.T) {
	p := newPollerForTest()
	// Must return normally rather than crash the process.
	p.runTickWithRecover(context.Background())
}

// TestSafeGo_PanicBecomesError verifies that a panic in an errgroup worker is
// converted into a returned error (surfaced via g.Wait) rather than crashing
// the process. errgroup itself only propagates returned errors, never panics.
func TestSafeGo_PanicBecomesError(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	safeGo(g, slog.Default(), "boom worker", func() error {
		panic("kaboom")
	})
	err := g.Wait()
	if err == nil {
		t.Fatal("expected safeGo to convert the panic into an error, got nil")
	}
	if !strings.Contains(err.Error(), "boom worker") || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("error should name the worker and the panic value, got: %v", err)
	}
}

// TestSafeGo_SuccessPassesThrough verifies the happy path is untouched.
func TestSafeGo_SuccessPassesThrough(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	ran := false
	safeGo(g, slog.Default(), "ok worker", func() error {
		ran = true
		return nil
	})
	if err := g.Wait(); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !ran {
		t.Fatal("fn was not executed")
	}
}

// TestSafeGo_ErrorPassesThrough verifies a normal returned error is preserved
// (not masked or rewrapped by the recovery shim).
func TestSafeGo_ErrorPassesThrough(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	sentinel := errors.New("real failure")
	safeGo(g, slog.Default(), "err worker", func() error {
		return sentinel
	})
	if err := g.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error to pass through, got: %v", err)
	}
}

// TestSafeGo_NilLoggerNoPanic verifies the logger-nil guard: a panic must still
// convert to an error without dereferencing a nil logger.
func TestSafeGo_NilLoggerNoPanic(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	safeGo(g, nil, "nil-logger worker", func() error {
		panic("still recovered")
	})
	if err := g.Wait(); err == nil {
		t.Fatal("expected recovered panic to become an error even with a nil logger")
	}
}
