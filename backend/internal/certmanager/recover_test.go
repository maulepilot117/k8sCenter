package certmanager

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestPollerRunTickWithRecover_SwallowsPanicAndLogs verifies that a panic
// inside a poll cycle does not unwind the poller goroutine AND that the
// recovery actually fired. A nil Discoverer makes tick() panic on the first
// p.disc.IsAvailable() call; runTickWithRecover (via recoverutil.Tick) must
// recover and emit its error log. Asserting on the log line (not merely "did
// not crash") makes the test fail loudly if the panic source ever disappears
// (e.g. a future nil-guard on the disc chain) instead of passing vacuously.
//
// The generic recover wrappers themselves are unit-tested in
// internal/recoverutil; this test guards the certmanager wiring + that the
// poller passes a non-nil logger and a meaningful task label.
func TestPollerRunTickWithRecover_SwallowsPanicAndLogs(t *testing.T) {
	var buf bytes.Buffer
	p := &Poller{
		logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})),
		dedupe: make(map[string]threshold),
		// disc is nil → tick() panics on p.disc.IsAvailable, exercising recover.
	}
	p.runTickWithRecover(context.Background()) // must return, not crash
	got := buf.String()
	if !strings.Contains(got, "panic recovered") || !strings.Contains(got, "certmanager poller tick") {
		t.Fatalf("expected recovery to log the poller task label and 'panic recovered' (proving the recover path fired); got: %q", got)
	}
}
