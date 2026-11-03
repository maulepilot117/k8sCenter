package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// causeRecorder is a stream's cancel func that records every cause it is
// called with.
type causeRecorder struct {
	mu     sync.Mutex
	causes []error
}

func (c *causeRecorder) cancel(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.causes = append(c.causes, cause)
}

func (c *causeRecorder) snapshot() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]error(nil), c.causes...)
}

// waitForCause waits for the first recorded cause.
func (c *causeRecorder) waitForCause(t *testing.T) error {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if causes := c.snapshot(); len(causes) > 0 {
			return causes[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("stream was never cancelled")
	return nil
}

func lifecycleTestServer() *Server {
	return &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func allStreamEnds(e *streamEnds) []*streamEnd {
	return []*streamEnd{
		e.clusterRemoved, e.accessRevoked, e.tokenExpired, e.signedOut,
		e.idleTimeout, e.serverRestarting, e.recordingUnavailable, e.recordingLimit,
	}
}

func TestStreamRegistry_CancelClusterEndsOnlyThatCluster(t *testing.T) {
	reg := newStreamRegistry(flowStreamEnds)
	var a1, a2, b, gone causeRecorder
	reg.add("a", a1.cancel)
	reg.add("a", a2.cancel)
	reg.add("b", b.cancel)
	remove := reg.add("a", gone.cancel)
	remove()

	reg.CancelCluster("a")

	for name, rec := range map[string]*causeRecorder{"a1": &a1, "a2": &a2} {
		if causes := rec.snapshot(); len(causes) != 1 || causes[0] != flowStreamEnds.clusterRemoved {
			t.Errorf("%s causes = %v, want one cluster_removed", name, causes)
		}
	}
	if causes := b.snapshot(); len(causes) != 0 {
		t.Errorf("other cluster's stream was cancelled: %v", causes)
	}
	if causes := gone.snapshot(); len(causes) != 0 {
		t.Errorf("removed stream was cancelled: %v", causes)
	}
}

func TestStreamRegistry_CancelAllEndsEveryStream(t *testing.T) {
	reg := newStreamRegistry(flowStreamEnds)
	recs := make([]causeRecorder, 3)
	reg.add("a", recs[0].cancel)
	reg.add("a", recs[1].cancel)
	reg.add("b", recs[2].cancel)

	reg.CancelAll(flowStreamEnds.serverRestarting)

	for i := range recs {
		if causes := recs[i].snapshot(); len(causes) != 1 || causes[0] != flowStreamEnds.serverRestarting {
			t.Errorf("stream %d causes = %v, want one server_restarting", i, causes)
		}
	}
}

// Every registry New builds carries its ends, so eviction and shutdown can
// end each kind's streams with that kind's wording.
func TestStreamRegistry_EveryKindIsEvicted(t *testing.T) {
	regs := testServer(t).streamRegistries()
	if len(regs) != 2 {
		t.Fatalf("got %d stream registries, want flows and logs", len(regs))
	}
	for i, reg := range regs {
		if reg == nil || reg.ends == nil {
			t.Fatalf("registry %d is not built with its ends", i)
		}
	}
}

func TestWatchStream_EndsAtTokenExpiry(t *testing.T) {
	ctx, stop := context.WithCancel(context.WithValue(context.Background(), wsTokenExpiryKey{}, time.Now().Add(50*time.Millisecond)))
	defer stop()
	var rec causeRecorder

	lifecycleTestServer().watchStream(ctx, rec.cancel, flowStreamEnds, "test", time.Hour, func(context.Context) *streamEnd { return nil })

	if cause := rec.waitForCause(t); cause != flowStreamEnds.tokenExpired {
		t.Fatalf("cause = %v, want token_expired", cause)
	}
}

func TestWatchStream_ExpiryStopsWhenStreamEndsFirst(t *testing.T) {
	ctx, stop := context.WithCancel(context.WithValue(context.Background(), wsTokenExpiryKey{}, time.Now().Add(100*time.Millisecond)))
	var rec causeRecorder

	lifecycleTestServer().watchStream(ctx, rec.cancel, flowStreamEnds, "test", time.Hour, func(context.Context) *streamEnd { return nil })
	stop()
	time.Sleep(250 * time.Millisecond)

	if causes := rec.snapshot(); len(causes) != 0 {
		t.Fatalf("a stream that already ended was cancelled with %v", causes)
	}
}

func TestWatchStream_Revalidate(t *testing.T) {
	t.Run("an end cancels the stream", func(t *testing.T) {
		ctx, stop := context.WithCancel(context.Background())
		defer stop()
		var rec causeRecorder

		lifecycleTestServer().watchStream(ctx, rec.cancel, flowStreamEnds, "test", 10*time.Millisecond, func(context.Context) *streamEnd {
			return flowStreamEnds.accessRevoked
		})

		if cause := rec.waitForCause(t); cause != flowStreamEnds.accessRevoked {
			t.Fatalf("cause = %v, want access_revoked", cause)
		}
	})

	for name, revalidate := range map[string]func(context.Context) *streamEnd{
		"nothing leaves it running": func(context.Context) *streamEnd { return nil },
		"a panic leaves it running": func(context.Context) *streamEnd { panic("revalidate panicked") },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			var rec causeRecorder
			var mu sync.Mutex
			calls := 0

			lifecycleTestServer().watchStream(ctx, rec.cancel, flowStreamEnds, "test", 10*time.Millisecond, func(ctx context.Context) *streamEnd {
				mu.Lock()
				calls++
				mu.Unlock()
				return revalidate(ctx)
			})

			deadline := time.Now().Add(2 * time.Second)
			for {
				mu.Lock()
				n := calls
				mu.Unlock()
				if n >= 3 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("revalidate ran %d times, want at least 3", n)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if causes := rec.snapshot(); len(causes) != 0 {
				t.Fatalf("stream was cancelled with %v", causes)
			}
		})
	}
}

// The mobile client treats an error frame whose message contains "auth",
// "token" or "permission" as fatal and stops retrying, so no end may say
// any of them. Every kind's ends are built the same way; these are the
// kinds the plan adds.
func TestStreamEnds_MessagesAvoidMobileFatalWords(t *testing.T) {
	snake := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	kinds := map[string]*streamEnds{
		"flows":  flowStreamEnds,
		"logs":   logStreamEnds,
		"shells": newStreamEnds("the shell", "this shell"),
	}
	retryable := map[string]bool{"token_expired": true, "server_restarting": true}
	for kind, ends := range kinds {
		seen := map[string]bool{}
		for _, end := range append(allStreamEnds(ends), endRelayRestarted) {
			if end == nil {
				t.Fatalf("%s: an end is nil", kind)
			}
			lower := strings.ToLower(end.message)
			for _, word := range []string{"auth", "token", "permission"} {
				if strings.Contains(lower, word) {
					t.Errorf("%s %s message %q contains %q", kind, end.reason, end.message, word)
				}
			}
			if !snake.MatchString(end.reason) {
				t.Errorf("%s reason %q is not snake_case", kind, end.reason)
			}
			if seen[end.reason] {
				t.Errorf("%s reason %q is used twice", kind, end.reason)
			}
			seen[end.reason] = true
			if end != endRelayRestarted && end.retryable != retryable[end.reason] {
				t.Errorf("%s %s retryable = %v, want %v", kind, end.reason, end.retryable, retryable[end.reason])
			}
			var asEnd *streamEnd
			if !errors.As(end, &asEnd) || end.Error() != end.reason {
				t.Errorf("%s %s does not work as a cancel cause", kind, end.reason)
			}
		}
	}
}

// Flows keep the exact wording they had before the shared lifecycle.
func TestStreamEnds_FlowMessagesUnchanged(t *testing.T) {
	want := map[*streamEnd]string{
		flowStreamEnds.clusterRemoved: "the cluster was removed from k8sCenter; the flow stream ended",
		flowStreamEnds.accessRevoked:  "your access to these flows was revoked; the flow stream ended",
		flowStreamEnds.tokenExpired:   "your session needs refreshing; reconnecting",
		endRelayRestarted:             "Hubble Relay restarted; reconnecting",
	}
	for end, msg := range want {
		if end.message != msg {
			t.Errorf("%s message = %q, want %q", end.reason, end.message, msg)
		}
	}
}
