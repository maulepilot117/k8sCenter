// Package recoverutil provides panic-recovery wrappers for code that runs
// OUTSIDE chi's HTTP panic-recovery middleware — background ticker loops and
// errgroup worker goroutines. A panic on such a goroutine is not caught by the
// request middleware (it runs on a separate stack) and would terminate the
// whole process. These helpers turn that crash into a logged, recoverable
// failure so adversarial cluster data degrades the affected feature instead of
// taking down the server.
//
// Reference consumers: certmanager.Poller (Tick) + certmanager.Handler fetch
// fan-outs (Go). Mirrors the convention first established by
// externalsecrets.Poller.
package recoverutil

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"golang.org/x/sync/errgroup"
)

// Go launches fn on the errgroup with panic recovery. A panic in an errgroup
// worker goroutine is NOT caught by chi's recovery middleware (it runs on a
// separate goroutine stack) and would terminate the process; errgroup itself
// only propagates *returned* errors, never panics. A recovered panic is logged
// with its stack and converted to an error so g.Wait() surfaces it as an
// ordinary failure (a graceful 500 on a request path, a skipped fill on a
// poller path) rather than a crash. A nil logger is tolerated.
func Go(g *errgroup.Group, logger *slog.Logger, label string, fn func() error) {
	g.Go(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Error("errgroup worker panic recovered",
						"worker", label, "panic", r, "stack", string(debug.Stack()))
				}
				err = fmt.Errorf("%s: recovered panic: %v", label, r)
			}
		}()
		return fn()
	})
}

// Safe runs fn with panic recovery, for plain goroutines that are neither
// errgroup workers nor ticker loops — bare `go func(){...}` fan-outs and
// fire-and-forget `go method()` calls that run outside chi's recovery
// middleware. A recovered panic is logged with its stack; fn's own cleanup
// (e.g. a deferred wg.Done() registered by the caller before invoking Safe)
// still runs. A nil logger is tolerated. Typical use:
//
//	go func(i int) { defer wg.Done(); recoverutil.Safe(logger, "label", func() { ... }) }(i)
//	go recoverutil.Safe(logger, "notify", func() { svc.Emit(ctx, n) })
func Safe(logger *slog.Logger, label string, fn func()) {
	defer func() {
		if r := recover(); r != nil && logger != nil {
			logger.Error("goroutine panic recovered",
				"task", label, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	fn()
}

// Tick runs fn(ctx) with panic recovery, for background ticker loops whose
// goroutine has no surrounding recovery. A recovered panic is logged with its
// stack so the offending input can be traced; the caller's loop continues to
// its next iteration rather than crashing the process. A nil logger is
// tolerated (the panic is still swallowed).
func Tick(ctx context.Context, logger *slog.Logger, label string, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil && logger != nil {
			logger.Error("background tick panic recovered",
				"task", label, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	fn(ctx)
}
