package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Server    ServerConfig `koanf:"server"`
	Log       LogConfig    `koanf:"log"`
	Dev       bool         `koanf:"dev"`
	ClusterID string       `koanf:"clusterid"`
	CORS      CORSConfig   `koanf:"cors"`
}

type ServerConfig struct {
	Port            int           `koanf:"port"`
	TLSCert         string        `koanf:"tlscert"`
	TLSKey          string        `koanf:"tlskey"`
	ShutdownTimeout time.Duration `koanf:"shutdowntimeout"`
	RequestTimeout  time.Duration `koanf:"requesttimeout"`
}

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

type CORSConfig struct {
	AllowedOrigins []string `koanf:"allowedorigins"`
}

func Load(configPath string) (*Config, error) {
	k := koanf.New(".")

	// Set defaults
	defaults := map[string]any{
		"server.port":            DefaultPort,
		"server.shutdowntimeout": DefaultShutdownTimeout,
		"server.requesttimeout":  DefaultRequestTimeout,
		"log.level":              DefaultLogLevel,
		"log.format":             DefaultLogFormat,
		"dev":                    DefaultDevMode,
		"clusterid":              DefaultClusterID,
	}
	for key, val := range defaults {
		k.Set(key, val)
	}

	// Load optional YAML config file
	if configPath != "" {
		if err := k.Load(file.Provider(configPath), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading config file %s: %w", configPath, err)
		}
	}

	// Load env vars (KUBECENTER_ prefix, e.g. KUBECENTER_SERVER_PORT)
	if err := k.Load(env.Provider("KUBECENTER_", ".", func(s string) string {
		return strings.ToLower(strings.ReplaceAll(
			strings.TrimPrefix(s, "KUBECENTER_"), "_", "."))
	}), nil); err != nil {
		return nil, fmt.Errorf("loading env vars: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.Server.Port)
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level must be debug|info|warn|error, got %q", c.Log.Level)
	}

	switch c.Log.Format {
	case "json", "text":
	default:
		return fmt.Errorf("log.format must be json|text, got %q", c.Log.Format)
	}

	return nil
}

func (c *Config) SlogLevel() slog.Level {
	switch c.Log.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
