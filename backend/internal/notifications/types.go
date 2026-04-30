package notifications

import "time"

// Source identifies the subsystem that produced a notification.
type Source string

const (
	SourceAlert      Source = "alert"
	SourcePolicy     Source = "policy"
	SourceGitOps     Source = "gitops"
	SourceDiagnostic Source = "diagnostic"
	SourceScan       Source = "scan"
	SourceCluster    Source = "cluster"
	SourceAudit      Source = "audit"
	SourceLimits     Source = "limits"
	SourceVelero      Source = "velero"
	SourceCertManager Source = "certmanager"
	SourceExternalSecrets Source = "external_secrets"
)

// Severity indicates how critical a notification is.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// ChannelType identifies the external dispatch mechanism.
type ChannelType string

const (
	ChannelSlack   ChannelType = "slack"
	ChannelEmail   ChannelType = "email"
	ChannelWebhook ChannelType = "webhook"
)

// Notification is a single event from any subsystem.
//
// SuppressResourceFields, when true, instructs Slack and webhook dispatch to
// omit the resource namespace/name from outbound payloads. This closes a
// tenant-leakage path that the RBAC-generic title alone doesn't cover —
// Slack channels and webhook receivers may not honor the same RBAC scope as
// the in-app feed. Used by ESO events (R28 cross-tenant scope), opt-in for
// other sources.
type Notification struct {
	ID           string    `json:"id"`
	Source       Source    `json:"source"`
	Severity     Severity  `json:"severity"`
	Title        string    `json:"title"`
	Message      string    `json:"message"`
	ResourceKind string    `json:"resourceKind,omitempty"`
	ResourceNS   string    `json:"resourceNamespace,omitempty"`
	ResourceName string    `json:"resourceName,omitempty"`
	ClusterID    string    `json:"clusterId,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	Read         bool      `json:"read,omitempty"`

	// SuppressResourceFields strips ResourceNS/ResourceName from external
	// dispatch payloads (Slack, webhook). Not persisted to the feed —
	// in-app readers always see the resource fields, RBAC-filtered.
	SuppressResourceFields bool `json:"-"`
}

// Channel is an external dispatch target (Slack, email, webhook).
type Channel struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Type        ChannelType   `json:"type"`
	Config      ChannelConfig `json:"config"`
	CreatedBy   string        `json:"createdBy"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   *time.Time    `json:"updatedAt,omitempty"`
	UpdatedBy   string        `json:"updatedBy,omitempty"`
	LastSentAt  *time.Time    `json:"lastSentAt,omitempty"`
	LastError   string        `json:"lastError,omitempty"`
	LastErrorAt *time.Time    `json:"lastErrorAt,omitempty"`
}

// ChannelConfig holds type-specific settings, stored as encrypted BYTEA.
// Slack:   {"webhookUrl": "https://hooks.slack.com/..."}
// Email:   {"recipients": ["ops@team.com"], "schedule": "daily"}
// Webhook: {"url": "https://...", "secret": "...", "headers": {"Authorization": "Bearer ..."}}
type ChannelConfig map[string]any

// Rule maps notifications to channels based on source and severity filters.
type Rule struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	SourceFilter   []Source   `json:"sourceFilter"`
	SeverityFilter []Severity `json:"severityFilter"`
	ChannelID      string     `json:"channelId"`
	ChannelName    string     `json:"channelName,omitempty"`
	Enabled        bool       `json:"enabled"`
	CreatedBy      string     `json:"createdBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
	UpdatedBy      string     `json:"updatedBy,omitempty"`
}

// ListOpts controls notification feed pagination and filtering.
type ListOpts struct {
	UserID     string
	Namespaces []string
	Source     Source
	Severity   Severity
	ReadFilter string // "read", "unread", or "" (all)
	Since      time.Time
	Until      time.Time
	Limit      int
	Offset     int
}
