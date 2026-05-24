package k8s

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

// TestApplyClusterTLS_RejectsCAlessWithoutAllowInsecure verifies F#1:
// a probe / config build for a cluster with empty CAData and
// AllowInsecureTLS=false must error out instead of silently disabling TLS.
// This test does not need a live cluster — it exercises only the policy
// helper that both ProbeOne and buildRemoteConfig call.
func TestApplyClusterTLS_RejectsCAlessWithoutAllowInsecure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &rest.Config{}
	err := applyClusterTLS(cfg, "remote-99", nil, false, logger)
	if err == nil {
		t.Fatal("applyClusterTLS with empty CAData and AllowInsecureTLS=false must return error; got nil (silent MITM regression)")
	}
	if !strings.Contains(err.Error(), "no CAData") {
		t.Errorf("error message should mention 'no CAData'; got %q", err.Error())
	}
	if cfg.TLSClientConfig.Insecure {
		t.Error("rejected config must NOT have Insecure=true set")
	}
}

// TestApplyClusterTLS_AllowsCAlessWhenOptedIn verifies that operators can
// still opt in to insecure TLS for self-signed homelab control planes.
func TestApplyClusterTLS_AllowsCAlessWhenOptedIn(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &rest.Config{}
	if err := applyClusterTLS(cfg, "homelab", nil, true, logger); err != nil {
		t.Fatalf("applyClusterTLS with AllowInsecureTLS=true must succeed; got %v", err)
	}
	if !cfg.TLSClientConfig.Insecure {
		t.Error("AllowInsecureTLS=true should set TLSClientConfig.Insecure=true")
	}
}

// TestApplyClusterTLS_PrefersCAWhenPresent verifies that when CAData is
// supplied, the policy leaves the config alone (CA-pinned TLS) regardless
// of the AllowInsecureTLS flag.
func TestApplyClusterTLS_PrefersCAWhenPresent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &rest.Config{TLSClientConfig: rest.TLSClientConfig{CAData: []byte("pem")}}
	if err := applyClusterTLS(cfg, "remote-99", []byte("pem"), true, logger); err != nil {
		t.Fatalf("applyClusterTLS with CAData must succeed; got %v", err)
	}
	if cfg.TLSClientConfig.Insecure {
		t.Error("CA-pinned config must not flip Insecure=true even when AllowInsecureTLS=true")
	}
}

