package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/config"
	appstore "github.com/kubecenter/kubecenter/internal/store"
)

type fakeReceiptStore struct {
	mu              sync.Mutex
	calls           []string
	graces          []time.Duration
	retention       []int
	reconcileErr    error
	cleanupErr      error
	blockUntil      bool // block reconcile until its context ends
	cleanupDeadline []bool
	panicOn         string
	reconciled      chan struct{}
	cleaned         chan struct{}
}

func newFakeReceiptStore() *fakeReceiptStore {
	return &fakeReceiptStore{reconciled: make(chan struct{}, 64), cleaned: make(chan struct{}, 64)}
}

func (f *fakeReceiptStore) ReconcileOrphans(ctx context.Context, olderThan time.Duration) (int64, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "reconcile")
	f.graces = append(f.graces, olderThan)
	panicNow := f.panicOn == "reconcile"
	err := f.reconcileErr
	block := f.blockUntil
	f.mu.Unlock()
	f.reconciled <- struct{}{}
	if panicNow {
		panic("boom")
	}
	if block {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return 3, err
}

func (f *fakeReceiptStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "cleanup")
	_, has := ctx.Deadline()
	f.cleanupDeadline = append(f.cleanupDeadline, has)
	f.retention = append(f.retention, retentionDays)
	err := f.cleanupErr
	f.mu.Unlock()
	f.cleaned <- struct{}{}
	return 2, err
}

func (f *fakeReceiptStore) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReceiptSweeper_ProductionDefaults(t *testing.T) {
	w := newReceiptSweeper(newFakeReceiptStore(), 14, quietLogger())
	if w.grace != appstore.ReceiptOrphanGrace {
		t.Errorf("grace = %v; want store.ReceiptOrphanGrace %v", w.grace, appstore.ReceiptOrphanGrace)
	}
	if w.period != appstore.ReceiptOrphanGrace {
		t.Errorf("period = %v; want the orphan grace %v", w.period, appstore.ReceiptOrphanGrace)
	}
	if w.callTimeout != receiptSweepCallTimeout || w.callTimeout > time.Minute {
		t.Errorf("callTimeout = %v; want %v (and under the ~1m startup probe budget)", w.callTimeout, receiptSweepCallTimeout)
	}
	if w.retentionDays != 14 {
		t.Errorf("retentionDays = %d; want 14", w.retentionDays)
	}
}

func TestReceiptSweeper_RetentionBelowOneUsesDefault(t *testing.T) {
	for _, bad := range []int{0, -3} {
		w := newReceiptSweeper(newFakeReceiptStore(), bad, quietLogger())
		if w.retentionDays != config.DefaultChangesReceiptRetentionDays {
			t.Errorf("retention %d -> %d; want default %d", bad, w.retentionDays, config.DefaultChangesReceiptRetentionDays)
		}
	}
}

// The boot path is reconcile only: retention never runs before the listener.
func TestReceiptSweeper_BootIsReconcileOnly(t *testing.T) {
	f := newFakeReceiptStore()
	w := newReceiptSweeper(f, 30, quietLogger())
	w.reconcileOnBoot(context.Background())

	if got := f.snapshot(); !equalStrings(got, []string{"reconcile"}) {
		t.Fatalf("boot calls = %v; want only [reconcile]", got)
	}
	if f.graces[0] != appstore.ReceiptOrphanGrace {
		t.Errorf("grace reaching the store = %v; want %v", f.graces[0], appstore.ReceiptOrphanGrace)
	}
}

// A wedged database cannot hold startup past the per-call bound.
func TestReceiptSweeper_BootIsBounded(t *testing.T) {
	f := newFakeReceiptStore()
	f.blockUntil = true
	w := newReceiptSweeper(f, 30, quietLogger())
	w.callTimeout = 50 * time.Millisecond

	done := make(chan struct{})
	go func() { w.reconcileOnBoot(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("boot reconcile did not return after the call timeout")
	}
}

func TestReceiptSweeper_BootSurvivesPanic(t *testing.T) {
	f := newFakeReceiptStore()
	f.panicOn = "reconcile"
	w := newReceiptSweeper(f, 30, quietLogger())
	w.reconcileOnBoot(context.Background()) // must not panic
}

// run: first retention pass straight away, then both calls per tick; a
// reconcile error does not skip cleanup; retention days reach the store; done
// closes after cancel.
func TestReceiptSweeper_RunLoop(t *testing.T) {
	f := newFakeReceiptStore()
	f.reconcileErr = errors.New("reconcile down")
	w := newReceiptSweeper(f, 21, quietLogger())
	w.period = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go w.run(ctx, done)

	// First pass is cleanup alone (reconcile ran at boot).
	select {
	case <-f.cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("no first retention pass")
	}
	if got := f.snapshot(); len(got) == 0 || got[0] != "cleanup" {
		t.Fatalf("first call = %v; want cleanup first", got)
	}
	// A later tick runs reconcile and then cleanup even though reconcile failed.
	select {
	case <-f.reconciled:
	case <-time.After(5 * time.Second):
		t.Fatal("no reconcile on a tick")
	}
	select {
	case <-f.cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not run after a failed reconcile")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done channel not closed after cancel")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, has := range f.cleanupDeadline {
		if has {
			t.Error("Cleanup got a caller-side deadline; it must use its own 5-minute bound so a large DELETE is not cut at 30s")
		}
	}
	for _, d := range f.retention {
		if d != 21 {
			t.Errorf("retention days reaching the store = %d; want 21", d)
		}
	}
	for _, g := range f.graces {
		if g != appstore.ReceiptOrphanGrace {
			t.Errorf("grace reaching the store = %v; want %v", g, appstore.ReceiptOrphanGrace)
		}
	}
}
