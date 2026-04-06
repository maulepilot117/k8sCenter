package scanning

import (
	"log/slog"
	"testing"
)

func TestNewDiscoverer_InitialStatus(t *testing.T) {
	d := NewDiscoverer(nil, slog.Default())
	if d == nil {
		t.Fatal("expected non-nil discoverer")
	}

	status := d.Status()
	if status.Detected != ScannerNone {
		t.Errorf("expected detected=%q, got %q", ScannerNone, status.Detected)
	}
	if status.Trivy != nil {
		t.Error("expected Trivy detail to be nil initially")
	}
	if status.Kubescape != nil {
		t.Error("expected Kubescape detail to be nil initially")
	}
	if status.LastChecked == "" {
		t.Error("expected LastChecked to be set")
	}
}
