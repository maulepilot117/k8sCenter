package velero

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/store"
)

// The DB-less deployment path main.go takes: NewBackupAssuranceStore(nil)
// yields a nil *BackupAssuranceStore, which must not become a non-nil
// interface value inside the service.
func TestAssuranceWiring_NoDatabaseDisablesAndStartReturns(t *testing.T) {
	h := &Handler{Logger: slog.Default()}
	svc := NewAssuranceService(h, &Discoverer{}, store.NewBackupAssuranceStore(nil), nil, "local", "", nil)
	h.Assurance = svc

	if svc.Snapshot().Enabled {
		t.Fatal("Enabled = true with no store, want false")
	}
	if svc.Snapshot().Holder != DefaultAssuranceHolder() {
		t.Fatal("empty holder should default to DefaultAssuranceHolder")
	}

	done := make(chan struct{})
	go func() { svc.Start(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start on a disabled service did not return")
	}
}

func TestAssuranceWiring_HandlerFieldDefaultsToNil(t *testing.T) {
	h := &Handler{}
	if h.Assurance != nil {
		t.Fatal("a Handler built without wiring must have a nil Assurance service")
	}
	if h.AssuranceStore != nil {
		t.Fatal("a Handler built without wiring must have a nil AssuranceStore")
	}
	// DB-less main wiring: the constructor's nil must stay a nil pointer on the field.
	h.AssuranceStore = store.NewBackupAssuranceStore(nil)
	if h.AssuranceStore != nil {
		t.Fatal("NewBackupAssuranceStore(nil) must be nil (no DB)")
	}
}
