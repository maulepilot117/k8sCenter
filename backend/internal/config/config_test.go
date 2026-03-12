package config

import (
	"log/slog"
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with defaults failed: %v", err)
	}

	if cfg.Server.Port != DefaultPort {
		t.Errorf("expected port %d, got %d", DefaultPort, cfg.Server.Port)
	}
	if cfg.Log.Level != DefaultLogLevel {
		t.Errorf("expected log level %q, got %q", DefaultLogLevel, cfg.Log.Level)
	}
	if cfg.Log.Format != DefaultLogFormat {
		t.Errorf("expected log format %q, got %q", DefaultLogFormat, cfg.Log.Format)
	}
	if cfg.Dev != DefaultDevMode {
		t.Errorf("expected dev %v, got %v", DefaultDevMode, cfg.Dev)
	}
	if cfg.ClusterID != DefaultClusterID {
		t.Errorf("expected clusterID %q, got %q", DefaultClusterID, cfg.ClusterID)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	os.Setenv("KUBECENTER_SERVER_PORT", "9090")
	os.Setenv("KUBECENTER_LOG_LEVEL", "debug")
	os.Setenv("KUBECENTER_DEV", "true")
	defer func() {
		os.Unsetenv("KUBECENTER_SERVER_PORT")
		os.Unsetenv("KUBECENTER_LOG_LEVEL")
		os.Unsetenv("KUBECENTER_DEV")
	}()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with env overrides failed: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("expected log level debug, got %q", cfg.Log.Level)
	}
}

func TestValidationRejectsInvalidPort(t *testing.T) {
	os.Setenv("KUBECENTER_SERVER_PORT", "99999")
	defer os.Unsetenv("KUBECENTER_SERVER_PORT")

	_, err := Load("")
	if err == nil {
		t.Fatal("expected error for invalid port, got nil")
	}
}

func TestValidationRejectsInvalidLogLevel(t *testing.T) {
	os.Setenv("KUBECENTER_LOG_LEVEL", "verbose")
	defer os.Unsetenv("KUBECENTER_LOG_LEVEL")

	_, err := Load("")
	if err == nil {
		t.Fatal("expected error for invalid log level, got nil")
	}
}

func TestSlogLevel(t *testing.T) {
	tests := []struct {
		level    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
	}

	for _, tt := range tests {
		cfg := &Config{Log: LogConfig{Level: tt.level}}
		if got := cfg.SlogLevel(); got != tt.expected {
			t.Errorf("SlogLevel(%q) = %v, want %v", tt.level, got, tt.expected)
		}
	}
}
