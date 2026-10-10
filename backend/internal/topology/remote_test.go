package topology

import (
	"context"
	"testing"
	"time"
)

// A remote cluster's reads are bounded by RemoteReadTimeout; the local
// cluster's are left to the request's own context.
func TestWithRemoteTimeout(t *testing.T) {
	saved := RemoteReadTimeout
	RemoteReadTimeout = 50 * time.Millisecond
	t.Cleanup(func() { RemoteReadTimeout = saved })

	t.Run("remote", func(t *testing.T) {
		before := time.Now()
		ctx, cancel := WithRemoteTimeout(context.Background(), "remote-1")
		after := time.Now()
		defer cancel()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("remote ctx has no deadline")
		}
		// The deadline is set some instant between before and after, so it
		// lies in [before+timeout, after+timeout].
		if deadline.Before(before.Add(RemoteReadTimeout)) || deadline.After(after.Add(RemoteReadTimeout)) {
			t.Errorf("deadline %s, want within [%s, %s]", deadline, before.Add(RemoteReadTimeout), after.Add(RemoteReadTimeout))
		}
	})

	for _, id := range []string{"", "local"} {
		t.Run("local="+id, func(t *testing.T) {
			ctx, cancel := WithRemoteTimeout(context.Background(), id)
			defer cancel()
			if _, ok := ctx.Deadline(); ok {
				t.Error("local ctx has a deadline, want the request's ctx unchanged")
			}
		})
	}
}
