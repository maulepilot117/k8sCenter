package gitops

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
	if status.Detected != ToolNone {
		t.Errorf("expected detected=%q, got %q", ToolNone, status.Detected)
	}
	if status.ArgoCD != nil {
		t.Error("expected ArgoCD detail to be nil initially")
	}
	if status.FluxCD != nil {
		t.Error("expected FluxCD detail to be nil initially")
	}
	if status.LastChecked == "" {
		t.Error("expected LastChecked to be set")
	}
}
