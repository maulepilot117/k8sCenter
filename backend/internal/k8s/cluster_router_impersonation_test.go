package k8s

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/kubecenter/kubecenter/internal/store"
)

// These tests pin the one property every remote feature relies on: a remote
// client acts as the calling user, never as the stored cluster credential.
// The router builds an impersonating rest.Config per (cluster, user, groups);
// client-go turns Impersonate into Impersonate-User/Impersonate-Group headers.
// The tests stop at the config, because a live round trip would need a local
// API server that the router's SSRF guard (correctly) refuses to dial.

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestRemoteConfig_ImpersonatesCaller(t *testing.T) {
	rec := testClusterRecord(t, time.Now())
	router := newRemoteTestRouter(&fakeClusterGetter{record: rec})

	cfg, err := router.remoteConfig(context.Background(), "remote-1", "alice", []string{"engineering", "oncall"})
	if err != nil {
		t.Fatalf("remoteConfig: %v", err)
	}

	if cfg.Impersonate.UserName != "alice" {
		t.Errorf("Impersonate.UserName = %q, want %q", cfg.Impersonate.UserName, "alice")
	}
	if got := sortedCopy(cfg.Impersonate.Groups); !reflect.DeepEqual(got, []string{"engineering", "oncall"}) {
		t.Errorf("Impersonate.Groups = %v, want [engineering oncall]", cfg.Impersonate.Groups)
	}
	if cfg.Impersonate.UID != "" || len(cfg.Impersonate.Extra) != 0 {
		t.Errorf("Impersonate carries UID/Extra the caller never supplied: %+v", cfg.Impersonate)
	}
	// The stored token only authenticates the connection; Impersonate is what
	// makes the remote API server authorize the request as alice.
	if cfg.BearerToken != "fake-bearer-token" {
		t.Errorf("BearerToken = %q, want the stored cluster credential", cfg.BearerToken)
	}
	if cfg.Host != rec.APIServerURL {
		t.Errorf("Host = %q, want the registered API server %q", cfg.Host, rec.APIServerURL)
	}
}

func TestRemoteConfig_EmptyUsernameFailsClosed(t *testing.T) {
	// With no username, client-go sends no Impersonate headers and the
	// request runs with the stored cluster credential's own permissions.
	// The router must refuse to build that client, with or without groups.
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})
	ctx := context.Background()

	for _, groups := range [][]string{nil, {"engineering"}} {
		if cfg, err := router.remoteConfig(ctx, "remote-1", "", groups); err == nil {
			t.Errorf("remoteConfig(\"\", %v) = %+v, want an error", groups, cfg.Impersonate)
		}
	}
	if _, err := router.ClientForCluster(ctx, "remote-1", "", nil); err == nil {
		t.Error("ClientForCluster built a remote client with no user to impersonate")
	}
	if _, err := router.DynamicClientForCluster(ctx, "remote-1", "", nil); err == nil {
		t.Error("DynamicClientForCluster built a remote client with no user to impersonate")
	}

	// A user with no groups is an ordinary identity and must still work.
	cfg, err := router.remoteConfig(ctx, "remote-1", "alice", nil)
	if err != nil {
		t.Fatalf("remoteConfig(alice, no groups): %v", err)
	}
	if cfg.Impersonate.UserName != "alice" {
		t.Errorf("Impersonate.UserName = %q, want alice", cfg.Impersonate.UserName)
	}
}

func TestRemoteConfig_DistinctPerIdentity(t *testing.T) {
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})
	ctx := context.Background()

	identities := []struct {
		user   string
		groups []string
	}{
		{"alice", []string{"engineering"}},
		{"bob", []string{"engineering"}},
		{"alice", []string{"ops"}},
	}
	for _, id := range identities {
		cfg, err := router.remoteConfig(ctx, "remote-1", id.user, id.groups)
		if err != nil {
			t.Fatalf("remoteConfig(%s, %v): %v", id.user, id.groups, err)
		}
		if cfg.Impersonate.UserName != id.user || !reflect.DeepEqual(cfg.Impersonate.Groups, id.groups) {
			t.Errorf("remoteConfig(%s, %v) impersonates %q %v", id.user, id.groups, cfg.Impersonate.UserName, cfg.Impersonate.Groups)
		}
	}
}

// gatedClusterGetter holds every Get until the test closes release. Each Get
// first signals on arrived, so a test can count how many builds are in flight:
// one arrival for several callers proves they shared one singleflight build,
// and one arrival per caller proves they ran separate builds at the same time.
type gatedClusterGetter struct {
	record  *store.ClusterRecord
	arrived chan struct{}
	release chan struct{}
}

func newGatedRouter(t *testing.T) (*ClusterRouter, *gatedClusterGetter) {
	t.Helper()
	g := &gatedClusterGetter{
		record:  testClusterRecord(t, time.Now()),
		arrived: make(chan struct{}, 64),
		release: make(chan struct{}),
	}
	router := newRemoteTestRouter(nil)
	router.clusterStore = g
	return router, g
}

func (g *gatedClusterGetter) Get(ctx context.Context, _ string) (*store.ClusterRecord, error) {
	g.arrived <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	rec := *g.record
	return &rec, nil
}

// arrivals counts builds that reach the getter, stopping at want or after d.
func (g *gatedClusterGetter) arrivals(want int, d time.Duration) int {
	n := 0
	deadline := time.After(d)
	for n < want {
		select {
		case <-g.arrived:
			n++
		case <-deadline:
			return n
		}
	}
	return n
}

func TestRemoteConfig_CoalescedCallersGetIndependentCopies(t *testing.T) {
	// Two concurrent callers for one identity share a single build. Each
	// must still get its own config: one caller editing its Impersonate
	// fields in place must not change the identity the other one sends.
	// Retried because the second caller has to join the build while the
	// first is still inside it, which the test can only wait for.
	for attempt := 1; attempt <= 20; attempt++ {
		router, g := newGatedRouter(t)
		results := make(chan *rest.Config, 2)
		call := func() {
			cfg, err := router.remoteConfig(context.Background(), "remote-1", "alice", []string{"engineering", "oncall"})
			if err != nil {
				t.Errorf("remoteConfig: %v", err)
			}
			results <- cfg
		}

		go call()
		if g.arrivals(1, 2*time.Second) != 1 {
			close(g.release)
			t.Fatal("first caller never reached the cluster store")
		}
		go call()
		time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
		close(g.release)
		a, b := <-results, <-results
		if a == nil || b == nil {
			t.FailNow()
		}
		if g.arrivals(1, 50*time.Millisecond) != 0 {
			continue // the second caller ran its own build; try again
		}

		if a == b {
			t.Fatal("coalesced callers received the same *rest.Config")
		}
		a.Impersonate.UserName = "mallory"
		a.Impersonate.Groups[0] = "system:masters"
		if b.Impersonate.UserName != "alice" || !reflect.DeepEqual(b.Impersonate.Groups, []string{"engineering", "oncall"}) {
			t.Errorf("one caller editing its config changed the other's identity to %q %v", b.Impersonate.UserName, b.Impersonate.Groups)
		}
		return
	}
	t.Fatal("the two callers never shared a build in 20 attempts; the test could not exercise the coalesced path")
}

func TestRemoteConfig_ConcurrentSameUserDifferentGroups(t *testing.T) {
	// The same user with two group sets is two identities. Their builds
	// must run separately even while overlapping; a singleflight key built
	// from the username alone would hand one group set to both callers.
	router, g := newGatedRouter(t)
	type result struct {
		groups []string
		cfg    *rest.Config
	}
	results := make(chan result, 2)
	for _, groups := range [][]string{{"engineering"}, {"ops"}} {
		go func() {
			cfg, err := router.remoteConfig(context.Background(), "remote-1", "alice", groups)
			if err != nil {
				t.Errorf("remoteConfig(alice, %v): %v", groups, err)
			}
			results <- result{groups, cfg}
		}()
	}

	inFlight := g.arrivals(2, 2*time.Second)
	close(g.release)
	for i := 0; i < 2; i++ {
		r := <-results
		if r.cfg == nil {
			continue
		}
		if r.cfg.Impersonate.UserName != "alice" || !reflect.DeepEqual(r.cfg.Impersonate.Groups, r.groups) {
			t.Errorf("alice %v received impersonation %q %v", r.groups, r.cfg.Impersonate.UserName, r.cfg.Impersonate.Groups)
		}
	}
	if inFlight != 2 {
		t.Errorf("%d build(s) in flight for two overlapping identities, want 2: one group set joined the other's build", inFlight)
	}
}

func TestRemoteConfig_ConcurrentIdentitiesDoNotCross(t *testing.T) {
	// A burst across many identities: usernames and group sets vary
	// independently (i%4 and i%3), so every user appears with several group
	// sets and every group set with several users.
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})

	const workers = 48
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		user := fmt.Sprintf("user-%d", i%4)
		groups := []string{fmt.Sprintf("group-%d", i%3)}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg, err := router.remoteConfig(context.Background(), "remote-1", user, groups)
			if err != nil {
				errs <- fmt.Sprintf("%s: %v", user, err)
				return
			}
			if cfg.Impersonate.UserName != user || !reflect.DeepEqual(cfg.Impersonate.Groups, groups) {
				errs <- fmt.Sprintf("%s %v got impersonation %q %v", user, groups, cfg.Impersonate.UserName, cfg.Impersonate.Groups)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestRemoteClients_CachedPerIdentity(t *testing.T) {
	// Typed and dynamic remote clients are cached for clientCacheTTL. The
	// cache key must cover username and groups: a user dropped from a group
	// must not keep being served the client built for the old group set.
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})
	ctx := context.Background()

	typed := func(t *testing.T, user string, groups ...string) any {
		t.Helper()
		cs, err := router.ClientForCluster(ctx, "remote-1", user, groups)
		if err != nil {
			t.Fatalf("ClientForCluster(%s, %v): %v", user, groups, err)
		}
		return cs
	}
	dyn := func(t *testing.T, user string, groups ...string) any {
		t.Helper()
		dc, err := router.DynamicClientForCluster(ctx, "remote-1", user, groups)
		if err != nil {
			t.Fatalf("DynamicClientForCluster(%s, %v): %v", user, groups, err)
		}
		return dc
	}

	for name, get := range map[string]func(*testing.T, string, ...string) any{"typed": typed, "dynamic": dyn} {
		t.Run(name, func(t *testing.T) {
			aliceEng := get(t, "alice", "engineering", "oncall")
			if get(t, "alice", "oncall", "engineering") != aliceEng {
				t.Error("the same identity with groups in another order missed the cache")
			}
			if get(t, "alice", "engineering") == aliceEng {
				t.Error("alice with a smaller group set was served the client built for her old groups")
			}
			if get(t, "bob", "engineering", "oncall") == aliceEng {
				t.Error("bob was served alice's client")
			}
		})
	}
}
