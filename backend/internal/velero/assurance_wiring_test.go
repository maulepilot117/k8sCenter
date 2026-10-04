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

// main wires the handler through AttachAssurance; the handler and the
// collector must end up with the same service and store.
func TestAssuranceWiring_AttachAssuranceSetsServiceAndStore(t *testing.T) {
	h := &Handler{Logger: slog.Default()}
	svc := h.AttachAssurance(&Discoverer{}, nil, nil, "local", "holder-a", nil)
	if svc == nil || h.Assurance != svc {
		t.Fatal("AttachAssurance must return the service it assigns to Handler.Assurance")
	}
	if h.AssuranceStore != nil {
		t.Fatal("nil store (no DB) must stay nil on the handler")
	}
	if svc.Snapshot().Enabled || svc.Snapshot().Holder != "holder-a" {
		t.Fatalf("unexpected snapshot %+v", svc.Snapshot())
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
