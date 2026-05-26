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

// TestValidateLDAPPlaintextGate locks the P3-1 fail-closed contract:
// `ldap://` URLs must declare StartTLS or explicitly opt in to a
// plaintext bind. ldaps:// always passes regardless of the flag set.
// Audit finding P3-1 (2026-05-22).
func TestValidateLDAPPlaintextGate(t *testing.T) {
	baseConfig := func() Config {
		return Config{
			Server: ServerConfig{Port: DefaultPort},
			Log:    LogConfig{Level: DefaultLogLevel, Format: DefaultLogFormat},
		}
	}

	cases := []struct {
		name    string
		ldap    LDAPConfig
		wantErr bool
	}{
		{
			name:    "ldaps:// passes by default",
			ldap:    LDAPConfig{ID: "corp", URL: "ldaps://ldap.example.com:636"},
			wantErr: false,
		},
		{
			name:    "ldap:// without StartTLS or opt-in is rejected",
			ldap:    LDAPConfig{ID: "corp", URL: "ldap://ldap.example.com:389"},
			wantErr: true,
		},
		{
			name:    "ldap:// with StartTLS passes",
			ldap:    LDAPConfig{ID: "corp", URL: "ldap://ldap.example.com:389", StartTLS: true},
			wantErr: false,
		},
		{
			name:    "ldap:// with explicit InsecurePlaintext opt-in passes",
			ldap:    LDAPConfig{ID: "corp", URL: "ldap://ldap.example.com:389", InsecurePlaintext: true},
			wantErr: false,
		},
		{
			name:    "empty URL passes (LDAP provider effectively disabled)",
			ldap:    LDAPConfig{ID: "corp", URL: ""},
			wantErr: false,
		},
		{
			name:    "ldap:// with both flags passes (StartTLS wins, opt-in irrelevant)",
			ldap:    LDAPConfig{ID: "corp", URL: "ldap://ldap.example.com:389", StartTLS: true, InsecurePlaintext: true},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig()
			cfg.Auth.LDAP = []LDAPConfig{tc.ldap}
			err := cfg.validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %+v, got nil", tc.ldap)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %+v: %v", tc.ldap, err)
			}
		})
	}
}

// TestValidateLDAPPlaintextGateMultiProvider confirms a single bad
// provider in a list of otherwise valid ones still fails closed, and
// the error message names the offending provider so operators can
// fix it without guessing.
func TestValidateLDAPPlaintextGateMultiProvider(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Port: DefaultPort},
		Log:    LogConfig{Level: DefaultLogLevel, Format: DefaultLogFormat},
		Auth: AuthConfig{
			LDAP: []LDAPConfig{
				{ID: "ldaps-corp", URL: "ldaps://corp.example.com:636"},
				{ID: "ldap-legacy", URL: "ldap://legacy.example.com:389"},
			},
		},
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for plaintext provider in mixed list, got nil")
	}
	if got := err.Error(); !contains(got, "ldap-legacy") {
		t.Fatalf("expected error to name ldap-legacy provider, got %q", got)
	}
}

// TestValidateLDAPPlaintextGateUnnamedProvider exercises the fallback
// label when an operator omits the provider ID — the error should use
// the index so the operator can still locate the misconfigured entry.
func TestValidateLDAPPlaintextGateUnnamedProvider(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Port: DefaultPort},
		Log:    LogConfig{Level: DefaultLogLevel, Format: DefaultLogFormat},
		Auth: AuthConfig{
			LDAP: []LDAPConfig{{URL: "ldap://anon.example.com:389"}},
		},
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for plaintext provider with empty ID, got nil")
	}
	if got := err.Error(); !contains(got, "auth.ldap[0]") {
		t.Fatalf("expected error to fall back to indexed label, got %q", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
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
