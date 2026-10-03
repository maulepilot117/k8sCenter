package notifications

import (
	"strconv"
	"time"
)

// Source identifies the subsystem that produced a notification.
type Source string

const (
	SourceAlert           Source = "alert"
	SourcePolicy          Source = "policy"
	SourceGitOps          Source = "gitops"
	SourceDiagnostic      Source = "diagnostic"
	SourceScan            Source = "scan"
	SourceCluster         Source = "cluster"
	SourceAudit           Source = "audit"
	SourceLimits          Source = "limits"
	SourceVelero          Source = "velero"
	SourceCertManager     Source = "certmanager"
	SourceExternalSecrets Source = "external_secrets"
)

// Valid reports whether s is a known Source enum value. Used by the rule
// editor (HandleCreateRule / HandleUpdateRule) to reject bogus
// sourceFilter entries before they reach the database. nc_rules.source_filter
// is TEXT[] with no DB-level CHECK, so the application layer is the only
// validation surface.
func (s Source) Valid() bool {
	switch s {
	case SourceAlert, SourcePolicy, SourceGitOps, SourceDiagnostic,
		SourceScan, SourceCluster, SourceAudit, SourceLimits,
		SourceVelero, SourceCertManager, SourceExternalSecrets:
		return true
	}
	return false
}

// Valid reports whether sev is a known Severity enum value.
func (sev Severity) Valid() bool {
	switch sev {
	case SeverityInfo, SeverityWarning, SeverityCritical:
		return true
	}
	return false
}

// Severity indicates how critical a notification is.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// EmitResult reports what NotificationService.EmitSync did with a
// notification, so a caller holding a durable delivery intent (Release F
// backup assurance) can tell "the feed carries it" from "nothing was written".
// Emit discards it; only EmitSync returns it.
type EmitResult int

const (
	// EmitFailed is the zero value: nothing was persisted. EmitSync returns
	// it together with a non-nil error, so a caller that only inspects the
	// result still cannot read a failure as a delivery.
	EmitFailed EmitResult = iota
	// EmitPersisted: the row was written, broadcast over WebSocket and
	// offered to the external-channel queue. Persisted is not dispatched —
	// when the queue is full the external leg is dropped and the result is
	// still EmitPersisted, because the feed entry exists.
	EmitPersisted
	// EmitDeduped: an equivalent notification already exists inside the
	// dedup window; nothing was written. The feed carries the earlier entry.
	EmitDeduped
	// EmitSkipped: the audit-source short circuit ran. The row was persisted
	// and broadcast, but the dedup check and external dispatch were skipped
	// (audit → Emit → audit would otherwise loop).
	EmitSkipped
)

// Delivered reports whether the in-app feed carries the notification after
// this result: true for EmitPersisted, EmitDeduped and EmitSkipped, false
// for EmitFailed. A durable-intent caller marks its intent delivered on
// true and retries (or gives up) on false.
func (r EmitResult) Delivered() bool {
	switch r {
	case EmitPersisted, EmitDeduped, EmitSkipped:
		return true
	}
	return false
}

// String names the result for logs.
func (r EmitResult) String() string {
	switch r {
	case EmitFailed:
		return "failed"
	case EmitPersisted:
		return "persisted"
	case EmitDeduped:
		return "deduped"
	case EmitSkipped:
		return "skipped"
	}
	return "EmitResult(" + strconv.Itoa(int(r)) + ")"
}

// ChannelType identifies the external dispatch mechanism.
type ChannelType string

const (
	ChannelSlack      ChannelType = "slack"
	ChannelEmail      ChannelType = "email"
	ChannelWebhook    ChannelType = "webhook"
	ChannelMobilePush ChannelType = "mobile_push"
)

// MobilePushDevice is a registered mobile device that receives push
// notifications via the ChannelMobilePush dispatch path. One row per
// (user, device) — re-registration upserts onto last_seen_at.
type MobilePushDevice struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	DeviceToken  string    `json:"deviceToken"`
	Platform     string    `json:"platform"`
	RegisteredAt time.Time `json:"registeredAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

// Notification is a single event from any subsystem.
//
// SuppressResourceFields, when true, instructs Slack and webhook dispatch to
// omit the resource namespace/name from outbound payloads. This closes a
// tenant-leakage path that the RBAC-generic title alone doesn't cover —
// Slack channels and webhook receivers may not honor the same RBAC scope as
// the in-app feed. Used by ESO events (R28 cross-tenant scope), opt-in for
// other sources.
type Notification struct {
	ID           string   `json:"id"`
	Source       Source   `json:"source"`
	Severity     Severity `json:"severity"`
	Title        string   `json:"title"`
	Message      string   `json:"message"`
	ResourceKind string   `json:"resourceKind,omitempty"`
	ResourceNS   string   `json:"resourceNamespace,omitempty"`
	ResourceName string   `json:"resourceName,omitempty"`
	// ResourceUID is the Kubernetes UID of the resource (or the source's
	// equivalent stable identity, e.g. an Alertmanager fingerprint). Empty
	// when the source has none. Part of the dedup identity: a resource
	// deleted and recreated under the same name is a new resource, and its
	// first failure must not be suppressed as a repeat of its predecessor's.
	ResourceUID string `json:"resourceUid,omitempty"`
	// ClusterID names the cluster the event belongs to. Empty means the
	// local cluster, exactly as for X-Cluster-ID (k8s.IsLocalClusterID):
	// local-only sources may leave it unset, and rows written before the
	// field took part in dedup carry ''. Part of the dedup identity, so a
	// same-named resource failing on two clusters yields two notifications.
	ClusterID string    `json:"clusterId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read,omitempty"`

	// SuppressResourceFields strips ResourceNS/ResourceName from external
	// dispatch payloads (Slack, webhook). Not persisted to the feed —
	// in-app readers always see the resource fields, RBAC-filtered.
	SuppressResourceFields bool `json:"-"`
}

// ClusterStatusNotification builds the notification for a remote cluster's
// connectivity change, as reported by the cluster prober. ClusterID names
// the probed cluster: two clusters changing state within the dedup window
// are two events, and the feed can attribute each to its cluster.
func ClusterStatusNotification(clusterID, oldStatus, newStatus string) Notification {
	sev := SeverityInfo
	title := "Cluster " + clusterID + " is now " + newStatus
	if newStatus != "connected" {
		sev = SeverityCritical
		title = "Cluster " + clusterID + " is " + newStatus
	}
	return Notification{
		Source:    SourceCluster,
		Severity:  sev,
		Title:     title,
		Message:   "Status changed from " + oldStatus + " to " + newStatus,
		ClusterID: clusterID,
	}
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
