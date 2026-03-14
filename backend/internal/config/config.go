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
	Server     ServerConfig     `koanf:"server"`
	Log        LogConfig        `koanf:"log"`
	Auth       AuthConfig       `koanf:"auth"`
	Monitoring MonitoringConfig `koanf:"monitoring"`
	Alerting   AlertingConfig   `koanf:"alerting"`
	Dev        bool             `koanf:"dev"`
	ClusterID  string           `koanf:"clusterid"`
	CORS       CORSConfig       `koanf:"cors"`
}

type MonitoringConfig struct {
	Namespace     string `koanf:"namespace"`     // Namespace hint for discovery (empty = search all)
	PrometheusURL string `koanf:"prometheusurl"` // Override auto-discovery
	GrafanaURL    string `koanf:"grafanaurl"`     // Override auto-discovery
	GrafanaToken  string `koanf:"grafanatoken"`   // Grafana service account token
}

type AuthConfig struct {
	JWTSecret  string       `koanf:"jwtsecret"`
	SetupToken string       `koanf:"setuptoken"`
	OIDC       []OIDCConfig `koanf:"oidc"`
	LDAP       []LDAPConfig `koanf:"ldap"`
}

// OIDCConfig holds configuration for a single OIDC provider.
type OIDCConfig struct {
	ID             string   `koanf:"id"`
	DisplayName    string   `koanf:"displayname"`
	IssuerURL      string   `koanf:"issuerurl"`
	ClientID       string   `koanf:"clientid"`
	ClientSecret   string   `koanf:"clientsecret"`
	RedirectURL    string   `koanf:"redirecturl"`
	Scopes         []string `koanf:"scopes"`
	UsernameClaim  string   `koanf:"usernameclaim"`
	GroupsClaim    string   `koanf:"groupsclaim"`
	GroupsPrefix   string   `koanf:"groupsprefix"`
	AllowedDomains []string `koanf:"alloweddomains"`
	TLSInsecure    bool     `koanf:"tlsinsecure"`
	CACertPath     string   `koanf:"cacertpath"`
}

// LDAPConfig holds configuration for a single LDAP provider.
type LDAPConfig struct {
	ID              string   `koanf:"id"`
	DisplayName     string   `koanf:"displayname"`
	URL             string   `koanf:"url"`
	BindDN          string   `koanf:"binddn"`
	BindPassword    string   `koanf:"bindpassword"`
	StartTLS        bool     `koanf:"starttls"`
	TLSInsecure     bool     `koanf:"tlsinsecure"`
	CACertPath      string   `koanf:"cacertpath"`
	UserBaseDN      string   `koanf:"userbasedn"`
	UserFilter      string   `koanf:"userfilter"`
	UserAttributes  []string `koanf:"userattributes"`
	GroupBaseDN     string   `koanf:"groupbasedn"`
	GroupFilter     string   `koanf:"groupfilter"`
	GroupNameAttr   string   `koanf:"groupnameattr"`
	UsernameMapAttr string   `koanf:"usernamemapattr"`
	GroupsPrefix    string   `koanf:"groupsprefix"`
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

type AlertingConfig struct {
	Enabled       bool       `koanf:"enabled"`
	WebhookToken  string     `koanf:"webhooktoken"`
	RetentionDays int        `koanf:"retentiondays"`
	RateLimit     int        `koanf:"ratelimit"` // max emails per hour
	Recipients    []string   `koanf:"recipients"`
	SMTP          SMTPConfig `koanf:"smtp"`
}

type SMTPConfig struct {
	Host        string `koanf:"host"`
	Port        int    `koanf:"port"`
	Username    string `koanf:"username"`
	Password    string `koanf:"password"`
	From        string `koanf:"from"`
	TLSInsecure bool   `koanf:"tlsinsecure"`
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
		"dev":                         DefaultDevMode,
		"clusterid":                   DefaultClusterID,
		"alerting.enabled":            DefaultAlertingEnabled,
		"alerting.retentiondays":      DefaultAlertingRetentionDays,
		"alerting.ratelimit":          DefaultAlertingRateLimit,
		"alerting.smtp.port":          DefaultAlertingSMTPPort,
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
