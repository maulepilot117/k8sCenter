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
	Server      ServerConfig      `koanf:"server"`
	Log         LogConfig         `koanf:"log"`
	Auth        AuthConfig        `koanf:"auth"`
	Monitoring  MonitoringConfig  `koanf:"monitoring"`
	Loki        LokiConfig        `koanf:"loki"`
	Alerting    AlertingConfig    `koanf:"alerting"`
	Audit       AuditConfig       `koanf:"audit"`
	Database    DatabaseConfig    `koanf:"database"`
	Dev         bool              `koanf:"dev"`
	ClusterID   string            `koanf:"clusterid"`
	CORS        CORSConfig        `koanf:"cors"`
	CiliumAgent CiliumAgentConfig `koanf:"ciliumagent"`
}

// AuditConfig holds configuration for audit logging.
type AuditConfig struct {
	RetentionDays int `koanf:"retentiondays"` // Days to retain audit entries (default: 90)
}

// DatabaseConfig holds PostgreSQL connection configuration.
type DatabaseConfig struct {
	URL           string `koanf:"url"`           // PostgreSQL connection URL (empty = audit via slog only)
	MaxConns      int    `koanf:"maxconns"`      // Connection pool max (default: 10)
	MinConns      int    `koanf:"minconns"`      // Connection pool min (default: 2)
	EncryptionKey string `koanf:"encryptionkey"` // AES-256 key for encrypting credentials at rest
}

type MonitoringConfig struct {
	Namespace              string `koanf:"namespace"`              // Namespace hint for discovery (empty = search all)
	PrometheusURL          string `koanf:"prometheusurl"`          // Override auto-discovery
	GrafanaURL             string `koanf:"grafanaurl"`             // Override auto-discovery
	GrafanaToken           string `koanf:"grafanatoken"`           // Deprecated: use GrafanaViewerToken. Backward-compat: maps to viewer-only.
	GrafanaViewerToken     string `koanf:"grafanaviewertoken"`     // Injected into proxy requests (Authorization: Bearer). KUBECENTER_MONITORING_GRAFANAVIEWERTOKEN
	GrafanaProvisioningToken string `koanf:"grafanaprovisioningtoken"` // Used only for dashboard/folder writes. KUBECENTER_MONITORING_GRAFANAPROVISIONINGTOKEN
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
	// InsecurePlaintext lets an operator opt in to a plaintext
	// ldap:// bind without StartTLS. Defaults to false — the config
	// validator refuses to start with `ldap://` URLs unless either
	// StartTLS is true or this flag is explicitly set. The flag exists
	// so test / homelab fixtures that talk to a directory inside a
	// trusted network can still boot, but it must be a conscious
	// operator decision per LDAP provider rather than a silent
	// behaviour. Audit finding P3-1 (2026-05-22).
	InsecurePlaintext bool `koanf:"insecureplaintext"`
}

type ServerConfig struct {
	Port            int           `koanf:"port"`
	TLSCert         string        `koanf:"tlscert"`
	TLSKey          string        `koanf:"tlskey"`
	ShutdownTimeout time.Duration `koanf:"shutdowntimeout"`
	RequestTimeout  time.Duration `koanf:"requesttimeout"`
	// TrustedProxyCIDRs lists IPv4 / IPv6 CIDR blocks whose socket peers
	// are trusted to set X-Forwarded-For / X-Real-IP headers. The
	// TrustedProxy middleware rewrites r.RemoteAddr from those headers
	// ONLY when the request's TCP peer falls inside one of these blocks;
	// otherwise the forwarded headers are ignored and r.RemoteAddr stays
	// at the socket-peer address. Default empty = fail-closed (no proxies
	// trusted) so rate-limit buckets cannot be poisoned by spoofed
	// headers from direct LAN/internet attackers (audit finding P2-1,
	// 2026-05-22).
	//
	// Configure via KUBECENTER_SERVER_TRUSTEDPROXYCIDRS as a
	// comma-separated list. Narrow the trust set to the ingress
	// controller's exact pod or service CIDR — e.g. a single
	// nginx-ingress pod /32 like "10.42.5.17/32", or the load-
	// balancer's source range. AVOID broad ranges that include the
	// full Kubernetes pod CIDR (typically 10.0.0.0/8 or 10.42.0.0/16);
	// in those configurations any pod in the cluster can spoof
	// X-Forwarded-For to poison rate-limit buckets and pollute audit
	// SourceIP fields. The middleware emits a startup warning on /0
	// catch-all CIDRs that reinstate the pre-Phase-3 blanket-trust
	// behaviour, but narrower mis-scoping (e.g. /8) is silent — operator
	// must scope this correctly per deployment.
	TrustedProxyCIDRs []string `koanf:"trustedproxycidrs"`
}

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

type CORSConfig struct {
	AllowedOrigins []string `koanf:"allowedorigins"`
}

// CiliumAgentConfig controls opt-in exec-based Cilium agent diagnostics.
type CiliumAgentConfig struct {
	ExecEnabled   bool `koanf:"execenabled"`   // KUBECENTER_CILIUMAGENT_EXECENABLED
	MaxConcurrent int  `koanf:"maxconcurrent"` // KUBECENTER_CILIUMAGENT_MAXCONCURRENT (default: 10)
}

// LokiConfig holds configuration for Loki log aggregation integration.
type LokiConfig struct {
	URL      string `koanf:"url"`      // Override auto-discovery: KUBECENTER_LOKI_URL
	TenantID string `koanf:"tenantid"` // Multi-tenant X-Scope-OrgID: KUBECENTER_LOKI_TENANTID
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
		"dev":                    DefaultDevMode,
		"clusterid":              DefaultClusterID,
		"audit.retentiondays":    DefaultAuditRetentionDays,
		"alerting.enabled":       DefaultAlertingEnabled,
		"alerting.retentiondays": DefaultAlertingRetentionDays,
		"alerting.ratelimit":     DefaultAlertingRateLimit,
		"alerting.smtp.port":     DefaultAlertingSMTPPort,
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

	// P3-1 (2026-05-22 audit): reject plaintext LDAP binds unless the
	// operator explicitly opted in via `auth.ldap[].insecureplaintext`.
	// `ldap://` without StartTLS sends the service-account credentials
	// and every user bind in cleartext, so an on-path attacker captures
	// every login. Operators with a legitimate plaintext-on-trusted-LAN
	// use case must set the flag per provider; everyone else fails
	// closed with a directly actionable startup error.
	for i, l := range c.Auth.LDAP {
		if !strings.HasPrefix(l.URL, "ldap://") {
			continue
		}
		if l.StartTLS || l.InsecurePlaintext {
			continue
		}
		id := l.ID
		if id == "" {
			id = fmt.Sprintf("auth.ldap[%d]", i)
		}
		return fmt.Errorf("auth.ldap %q uses ldap:// without StartTLS: switch the URL to ldaps://, set starttls=true, or explicitly set insecureplaintext=true to acknowledge the risk", id)
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
