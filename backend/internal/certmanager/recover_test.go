package certmanager

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

// TestPollerRunTickWithRecover_SwallowsPanicAndLogs verifies that a panic
// inside a poll cycle does not unwind the poller goroutine AND that the
// recovery actually fired. A nil Discoverer makes tick() panic on the first
// p.disc.IsAvailable() call; runTickWithRecover must recover and emit its
// error log. Asserting on the log line (not merely "did not crash") makes the
// test fail loudly if the panic source ever disappears (e.g. a future nil-guard
// on the disc chain) instead of passing vacuously.
func TestPollerRunTickWithRecover_SwallowsPanicAndLogs(t *testing.T) {
	var buf bytes.Buffer
	p := &Poller{
		logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})),
		dedupe: make(map[string]threshold),
		// disc is nil → tick() panics on p.disc.IsAvailable, exercising recover.
	}
	p.runTickWithRecover(context.Background()) // must return, not crash
	if got := buf.String(); !strings.Contains(got, "tick panic recovered") {
		t.Fatalf("expected recovery to log 'tick panic recovered' (proving the recover path fired); got: %q", got)
	}
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

// TestSafeGo_NonStringPanicBecomesError verifies a non-string panic value
// (panic(42)) is still formatted into the returned error via %v. Guards against
// a future switch to %s, which would render non-string values uselessly.
func TestSafeGo_NonStringPanicBecomesError(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	safeGo(g, slog.Default(), "int worker", func() error {
		panic(42)
	})
	err := g.Wait()
	if err == nil || !strings.Contains(err.Error(), "42") || !strings.Contains(err.Error(), "int worker") {
		t.Fatalf("expected non-string panic value and label in the error, got: %v", err)
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
