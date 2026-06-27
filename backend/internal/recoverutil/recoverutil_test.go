package recoverutil

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

// TestGo_PanicBecomesError verifies a panic in an errgroup worker is converted
// into a returned error (surfaced via g.Wait) rather than crashing the process.
func TestGo_PanicBecomesError(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	Go(g, slog.Default(), "boom worker", func() error {
		panic("kaboom")
	})
	err := g.Wait()
	if err == nil {
		t.Fatal("expected Go to convert the panic into an error, got nil")
	}
	if !strings.Contains(err.Error(), "boom worker") || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("error should name the worker and the panic value, got: %v", err)
	}
}

// TestGo_NonStringPanicBecomesError verifies a non-string panic value
// (panic(42)) is still formatted into the returned error via %v. Guards against
// a future switch to %s, which would render non-string values uselessly.
func TestGo_NonStringPanicBecomesError(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	Go(g, slog.Default(), "int worker", func() error {
		panic(42)
	})
	err := g.Wait()
	if err == nil || !strings.Contains(err.Error(), "42") || !strings.Contains(err.Error(), "int worker") {
		t.Fatalf("expected non-string panic value and label in the error, got: %v", err)
	}
}

// TestGo_SuccessPassesThrough verifies the happy path is untouched.
func TestGo_SuccessPassesThrough(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	ran := false
	Go(g, slog.Default(), "ok worker", func() error {
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

// TestGo_ErrorPassesThrough verifies a normal returned error is preserved
// (not masked or rewrapped by the recovery shim).
func TestGo_ErrorPassesThrough(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	sentinel := errors.New("real failure")
	Go(g, slog.Default(), "err worker", func() error {
		return sentinel
	})
	if err := g.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error to pass through, got: %v", err)
	}
}

// TestGo_NilLoggerNoPanic verifies the logger-nil guard: a panic must still
// convert to an error without dereferencing a nil logger.
func TestGo_NilLoggerNoPanic(t *testing.T) {
	g, _ := errgroup.WithContext(context.Background())
	Go(g, nil, "nil-logger worker", func() error {
		panic("still recovered")
	})
	if err := g.Wait(); err == nil {
		t.Fatal("expected recovered panic to become an error even with a nil logger")
	}
}

// TestSafe_SwallowsPanicAndLogs verifies a panic inside a plain-goroutine fn
// is recovered and logged (and does not crash the process).
func TestSafe_SwallowsPanicAndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	Safe(logger, "unit goroutine", func() {
		panic("safe boom")
	})
	got := buf.String()
	if !strings.Contains(got, "panic recovered") || !strings.Contains(got, "unit goroutine") {
		t.Fatalf("expected recovery to log the task label and 'panic recovered'; got: %q", got)
	}
}

// TestSafe_NilLoggerNoPanic verifies a nil logger is tolerated.
func TestSafe_NilLoggerNoPanic(t *testing.T) {
	Safe(nil, "nil-logger goroutine", func() { panic("boom") })
}

// TestSafe_SuccessRunsFn verifies the happy path executes fn.
func TestSafe_SuccessRunsFn(t *testing.T) {
	ran := false
	Safe(slog.Default(), "ok goroutine", func() { ran = true })
	if !ran {
		t.Fatal("fn was not executed")
	}
}

// TestTick_SwallowsPanicAndLogs verifies a panic inside the tick fn is
// recovered and logged (the log line proves the recover path actually fired,
// not merely that the process did not crash).
func TestTick_SwallowsPanicAndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	Tick(context.Background(), logger, "unit task", func(context.Context) {
		panic("tick boom")
	})
	got := buf.String()
	if !strings.Contains(got, "panic recovered") || !strings.Contains(got, "unit task") {
		t.Fatalf("expected recovery to log the task label and 'panic recovered'; got: %q", got)
	}
}

// TestTick_NilLoggerNoPanic verifies a nil logger is tolerated.
func TestTick_NilLoggerNoPanic(t *testing.T) {
	// Must not crash despite the nil logger.
	Tick(context.Background(), nil, "nil-logger task", func(context.Context) {
		panic("boom")
	})
}

// TestTick_SuccessPassesThrough verifies the happy path runs fn normally.
func TestTick_SuccessPassesThrough(t *testing.T) {
	ran := false
	Tick(context.Background(), slog.Default(), "ok task", func(context.Context) {
		ran = true
	})
	if !ran {
		t.Fatal("fn was not executed")
	}
}
