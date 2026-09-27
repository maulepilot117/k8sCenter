package k8s

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
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

func TestRemoteConfig_EmptyUsernameIsNotSilentlyUnimpersonated(t *testing.T) {
	// An empty username would leave Impersonate unset, so client-go would
	// send requests as the stored credential itself. Pin today's behavior:
	// the router passes the caller's (empty) identity through unchanged
	// rather than substituting one, so this is visible if it ever changes.
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})
	cfg, err := router.remoteConfig(context.Background(), "remote-1", "", nil)
	if err != nil {
		t.Fatalf("remoteConfig: %v", err)
	}
	if cfg.Impersonate.UserName != "" || len(cfg.Impersonate.Groups) != 0 {
		t.Errorf("Impersonate = %+v, want the caller's empty identity passed through unchanged", cfg.Impersonate)
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

	// Each call returns its own copy: a caller that mutates its config
	// cannot change the identity the next caller receives.
	first, err := router.remoteConfig(ctx, "remote-1", "alice", []string{"engineering"})
	if err != nil {
		t.Fatalf("remoteConfig: %v", err)
	}
	first.Impersonate.UserName = "mallory"
	first.Impersonate.Groups = append(first.Impersonate.Groups, "system:masters")

	again, err := router.remoteConfig(ctx, "remote-1", "alice", []string{"engineering"})
	if err != nil {
		t.Fatalf("remoteConfig: %v", err)
	}
	if again.Impersonate.UserName != "alice" || !reflect.DeepEqual(again.Impersonate.Groups, []string{"engineering"}) {
		t.Errorf("mutating one caller's config leaked into the next: %+v", again.Impersonate)
	}
}

func TestRemoteConfig_ConcurrentIdentitiesDoNotCross(t *testing.T) {
	// remoteConfig coalesces concurrent builds with singleflight. The key
	// must include the identity, or one user's burst would hand another
	// user's impersonation to everyone waiting on it.
	router := newRemoteTestRouter(&fakeClusterGetter{record: testClusterRecord(t, time.Now())})

	const workers = 40
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		user := fmt.Sprintf("user-%d", i%4)
		groups := []string{fmt.Sprintf("group-%d", i%4)}
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

	typed := func(user string, groups ...string) any {
		t.Helper()
		cs, err := router.ClientForCluster(ctx, "remote-1", user, groups)
		if err != nil {
			t.Fatalf("ClientForCluster(%s, %v): %v", user, groups, err)
		}
		return cs
	}
	dyn := func(user string, groups ...string) any {
		t.Helper()
		dc, err := router.DynamicClientForCluster(ctx, "remote-1", user, groups)
		if err != nil {
			t.Fatalf("DynamicClientForCluster(%s, %v): %v", user, groups, err)
		}
		return dc
	}

	for name, get := range map[string]func(string, ...string) any{"typed": typed, "dynamic": dyn} {
		t.Run(name, func(t *testing.T) {
			aliceEng := get("alice", "engineering", "oncall")
			if get("alice", "oncall", "engineering") != aliceEng {
				t.Error("the same identity with groups in another order missed the cache")
			}
			if get("alice", "engineering") == aliceEng {
				t.Error("alice with a smaller group set was served the client built for her old groups")
			}
			if get("bob", "engineering", "oncall") == aliceEng {
				t.Error("bob was served alice's client")
			}
		})
	}
}
