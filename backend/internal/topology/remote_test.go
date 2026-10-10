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
		start := time.Now()
		ctx, cancel := WithRemoteTimeout(context.Background(), "remote-1")
		defer cancel()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("remote ctx has no deadline")
		}
		if d := deadline.Sub(start); d <= 0 || d > RemoteReadTimeout {
			t.Errorf("deadline in %s, want within (0, %s]", d, RemoteReadTimeout)
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
