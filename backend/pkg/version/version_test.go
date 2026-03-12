package version

import (
	"runtime"
	"testing"
)

func TestGet(t *testing.T) {
	info := Get()

	if info.Version != "dev" {
		t.Errorf("expected version 'dev', got %q", info.Version)
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("expected go version %q, got %q", runtime.Version(), info.GoVersion)
	}
}
