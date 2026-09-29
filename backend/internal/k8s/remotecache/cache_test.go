package remotecache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	clusterA = "remote-a"
	clusterB = "remote-b"
)

var (
	alice = []string{"team-a"}
	bob   = []string{"team-b"}
)

// fixedClock lets a test move time without sleeping.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fixedClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fixedClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func newTestCache(ttl time.Duration, maxEntries int) (*Cache[string], *fixedClock) {
	c := New[string](ttl, maxEntries)
	clock := &fixedClock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	c.now = clock.now
	return c, clock
}

func value(v string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return v, nil }
}

func mustGet(t *testing.T, c *Cache[string], cluster, user string, groups []string, fetch func(context.Context) (string, error)) string {
	t.Helper()
	v, err := c.Get(context.Background(), cluster, user, groups, fetch)
	if err != nil {
		t.Fatalf("Get(%s, %s): %v", cluster, user, err)
	}
	return v
}

func TestGet_IdentitiesOnOneClusterAreIsolated(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)

	mustGet(t, c, clusterA, "alice", alice, value("alice's view"))
	got := mustGet(t, c, clusterA, "bob", bob, value("bob's view"))

	if got != "bob's view" {
		t.Fatalf("bob got %q, want his own fetch", got)
	}
	if again := mustGet(t, c, clusterA, "alice", alice, value("refetched")); again != "alice's view" {
		t.Errorf("alice got %q, want her cached view", again)
	}
}

func TestGet_ErrorIsNotCachedOrSharedAcrossIdentities(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	forbidden := errors.New("forbidden")

	if _, err := c.Get(context.Background(), clusterA, "alice", alice, func(context.Context) (string, error) {
		return "", forbidden
	}); !errors.Is(err, forbidden) {
		t.Fatalf("alice err = %v, want forbidden", err)
	}
	if got := mustGet(t, c, clusterA, "bob", bob, value("bob's view")); got != "bob's view" {
		t.Errorf("bob got %q after alice's error", got)
	}
	if got := mustGet(t, c, clusterA, "alice", alice, value("alice retry")); got != "alice retry" {
		t.Errorf("alice got %q, want a fresh fetch after her error", got)
	}
}

func TestGet_ConcurrentMissesShareOneFetch(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		<-release
		return "shared", nil
	}

	var wg sync.WaitGroup
	results := make(chan string, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Get(context.Background(), clusterA, "alice", alice, fetch)
			if err != nil {
				t.Errorf("Get: %v", err)
			}
			results <- v
		}()
	}
	// Give every goroutine time to join the in-flight call.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	if n := calls.Load(); n != 1 {
		t.Errorf("fetch ran %d times, want 1", n)
	}
	for v := range results {
		if v != "shared" {
			t.Errorf("waiter got %q, want shared", v)
		}
	}
}

func TestGet_BlockedFetchOnOneClusterDoesNotDelayAnother(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, _ = c.Get(context.Background(), clusterA, "alice", alice, func(context.Context) (string, error) {
			<-release
			return "a", nil
		})
	}()

	done := make(chan string, 1)
	go func() {
		done <- mustGet(t, c, clusterB, "alice", alice, value("b"))
	}()
	select {
	case v := <-done:
		if v != "b" {
			t.Errorf("cluster B got %q", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a blocked fetch on cluster A delayed cluster B")
	}
}

func TestGet_FetchSurvivesCallerCancellationButHonoursItsDeadline(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)

	ctx, cancel := context.WithCancel(context.Background())
	fetchErr := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		_, _ = c.Get(ctx, clusterA, "alice", alice, func(fctx context.Context) (string, error) {
			close(started)
			time.Sleep(20 * time.Millisecond)
			fetchErr <- fctx.Err()
			return "done", nil
		})
	}()
	<-started
	cancel()
	if err := <-fetchErr; err != nil {
		t.Errorf("fetch context ended with the caller: %v", err)
	}

	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelDeadline()
	_, err := c.Get(deadlineCtx, clusterB, "alice", alice, func(fctx context.Context) (string, error) {
		<-fctx.Done()
		return "", fctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the caller's deadline to bound the fetch", err)
	}
}

func TestGet_CallerCancellationReturnsWithoutWaiting(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	release := make(chan struct{})
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.Get(ctx, clusterA, "alice", alice, func(context.Context) (string, error) {
			<-release
			return "late", nil
		})
		errc <- err
	}()
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get kept waiting on the fetch after its caller cancelled")
	}
}

func TestEvictCluster_DropsEveryIdentityForThatClusterOnly(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	mustGet(t, c, clusterA, "alice", alice, value("a-alice"))
	mustGet(t, c, clusterA, "bob", bob, value("a-bob"))
	mustGet(t, c, clusterB, "alice", alice, value("b-alice"))

	c.EvictCluster(clusterA)

	if got := mustGet(t, c, clusterA, "alice", alice, value("a-alice-2")); got != "a-alice-2" {
		t.Errorf("cluster A alice got %q, want a refetch", got)
	}
	if got := mustGet(t, c, clusterA, "bob", bob, value("a-bob-2")); got != "a-bob-2" {
		t.Errorf("cluster A bob got %q, want a refetch", got)
	}
	if got := mustGet(t, c, clusterB, "alice", alice, value("b-alice-2")); got != "b-alice" {
		t.Errorf("cluster B alice got %q, want the cached entry kept", got)
	}
}

func TestEvictCluster_DuringFetchDiscardsItsResult(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	inFetch := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Get(context.Background(), clusterA, "alice", alice, func(context.Context) (string, error) {
			close(inFetch)
			<-release
			return "read before the write", nil
		})
	}()
	<-inFetch
	c.EvictCluster(clusterA) // a write landed while the fetch was reading
	close(release)
	<-done

	if got := mustGet(t, c, clusterA, "alice", alice, value("after the write")); got != "after the write" {
		t.Errorf("got %q, want a fetch after the eviction, not the stale in-flight result", got)
	}
}

func TestGet_RefetchesAfterTTL(t *testing.T) {
	c, clock := newTestCache(30*time.Second, 0)
	mustGet(t, c, clusterA, "alice", alice, value("first"))

	clock.advance(29 * time.Second)
	if got := mustGet(t, c, clusterA, "alice", alice, value("second")); got != "first" {
		t.Errorf("before TTL got %q, want cached", got)
	}
	clock.advance(2 * time.Second)
	if got := mustGet(t, c, clusterA, "alice", alice, value("second")); got != "second" {
		t.Errorf("after TTL got %q, want a refetch", got)
	}
}

func TestGet_EntryCountStaysBounded(t *testing.T) {
	c, _ := newTestCache(time.Minute, 4)
	for i := range 20 {
		mustGet(t, c, clusterA, fmt.Sprintf("user-%d", i), nil, value("v"))
	}
	c.mu.Lock()
	n, orderLen := len(c.entries), len(c.order)
	c.mu.Unlock()
	if n != 4 || orderLen != 4 {
		t.Errorf("entries=%d order=%d, want both at the bound 4", n, orderLen)
	}
	// The oldest identity was evicted first.
	if got := mustGet(t, c, clusterA, "user-0", nil, value("refetched")); got != "refetched" {
		t.Errorf("oldest identity got %q, want a refetch", got)
	}
}

func TestGet_PanickingFetchReturnsAnError(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	_, err := c.Get(context.Background(), clusterA, "alice", alice, func(context.Context) (string, error) {
		panic("malformed remote object")
	})
	if err == nil {
		t.Fatal("a panicking fetch must surface as an error")
	}
}

func TestGet_LocalAliasesShareOneKey(t *testing.T) {
	c, _ := newTestCache(time.Minute, 0)
	mustGet(t, c, "", "alice", alice, value("local"))
	if got := mustGet(t, c, "local", "alice", alice, value("other")); got != "local" {
		t.Errorf("\"local\" got %q, want the entry cached under the empty id", got)
	}
}
