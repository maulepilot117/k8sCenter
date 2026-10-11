package certmanager

import (
	"fmt"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
)

// These tests pin remotePeek's bookkeeping directly: the generation check
// that keeps a refresh which read pre-eviction state from being stored, the
// reservation release that lets a failed or panicked refresh be retried, and
// the size cap.

func peekTestKey(cluster string) peekKey {
	return newPeekKey(cluster, &auth.User{KubernetesUsername: "alice", KubernetesGroups: []string{"admins"}})
}

func TestRemotePeek_RefreshFinishingAfterEvictionStoresNothing(t *testing.T) {
	var p remotePeek
	k := peekTestKey(remoteCluster)
	now := time.Now()

	r, gen, started := p.begin(k)
	if !started {
		t.Fatal("first begin did not start a refresh")
	}
	p.evict(remoteCluster)
	r.snap = &remoteSnapshot{}
	r.err = nil
	p.finish(k, gen, r, now)

	select {
	case <-r.done:
	default:
		t.Fatal("finish did not wake the refresh's waiters")
	}
	if _, _, ok := p.get(k, now); ok {
		t.Fatal("a refresh that began before the eviction stored its snapshot")
	}

	// A refresh begun after the eviction stores normally.
	r2, gen2, started := p.begin(k)
	if !started {
		t.Fatal("begin after finish did not start a new refresh")
	}
	want := &remoteSnapshot{}
	r2.snap = want
	p.finish(k, gen2, r2, now)
	if got, _, ok := p.get(k, now); !ok || got != want {
		t.Fatalf("post-eviction refresh: got %v ok=%v, want the stored snapshot", got, ok)
	}
}

func TestRemotePeek_EvictionIsPerCluster(t *testing.T) {
	var p remotePeek
	kept := peekTestKey("other-cluster")
	k := peekTestKey(remoteCluster)
	now := time.Now()

	p.mu.Lock()
	p.put(kept, &remoteSnapshot{}, now)
	p.put(k, &remoteSnapshot{}, now)
	p.mu.Unlock()
	p.evict(remoteCluster)

	if _, _, ok := p.get(k, now); ok {
		t.Error("evict left the evicted cluster's snapshot")
	}
	if _, _, ok := p.get(kept, now); !ok {
		t.Error("evict dropped another cluster's snapshot")
	}
}

func TestRemotePeek_ConcurrentBeginJoinsTheRunningRefresh(t *testing.T) {
	var p remotePeek
	k := peekTestKey(remoteCluster)

	r, gen, _ := p.begin(k)
	joined, _, started := p.begin(k)
	if started || joined != r {
		t.Fatalf("second begin started=%v joined-same=%v; want it to join the running refresh", started, joined == r)
	}
	p.finish(k, gen, r, time.Now())
}

func TestRemotePeek_FailedRefreshReleasesTheReservation(t *testing.T) {
	var p remotePeek
	k := peekTestKey(remoteCluster)
	now := time.Now()

	// The state a panicked refresh leaves: the pre-seeded cause and no snapshot.
	r, gen, _ := p.begin(k)
	r.err = errRefreshPanicked
	p.finish(k, gen, r, now)

	if _, _, ok := p.get(k, now); ok {
		t.Fatal("a refresh with no snapshot stored one")
	}
	r2, _, started := p.begin(k)
	if !started || r2 == r {
		t.Fatal("a failed refresh kept its reservation, so the next call would join a finished refresh")
	}
}

func TestRemotePeek_PutDropsTheOldestBeyondTheCap(t *testing.T) {
	var p remotePeek
	base := time.Now().Add(-time.Minute)

	p.mu.Lock()
	for i := 0; i <= maxPeekEntries; i++ {
		p.put(peekTestKey(fmt.Sprintf("c-%03d", i)), &remoteSnapshot{}, base.Add(time.Duration(i)*time.Millisecond))
	}
	n := len(p.entries)
	p.mu.Unlock()

	if n != maxPeekEntries {
		t.Fatalf("len(entries) = %d, want the cap %d", n, maxPeekEntries)
	}
	now := base.Add(time.Second)
	if _, _, ok := p.get(peekTestKey("c-000"), now); ok {
		t.Error("the oldest entry survived past the cap")
	}
	if _, _, ok := p.get(peekTestKey(fmt.Sprintf("c-%03d", maxPeekEntries)), now); !ok {
		t.Error("the newest entry was dropped")
	}
	if _, _, ok := p.get(peekTestKey("c-001"), now); !ok {
		t.Error("the second-oldest entry was dropped; only the oldest should go")
	}
}
