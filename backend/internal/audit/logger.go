package audit

import (
	"context"
	"log/slog"
	"time"
)

// Action represents an auditable operation.
type Action string

const (
	ActionCreate  Action = "create"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionReveal  Action = "reveal" // secret reveal
	ActionApply   Action = "apply"  // YAML server-side apply
	ActionLogin   Action = "login"
	ActionLogout  Action = "logout"
	ActionRefresh Action = "refresh"
	ActionSetup              Action = "setup"
	ActionAlertRuleCreate    Action = "alert_rule_create"
	ActionAlertRuleUpdate    Action = "alert_rule_update"
	ActionAlertRuleDelete    Action = "alert_rule_delete"
	ActionAlertSettingsUpdate Action = "alert_settings_update"
	ActionAlertTest          Action = "alert_test"
)

// Result represents the outcome of an auditable operation.
type Result string

const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
	ResultDenied  Result = "denied"
)

// Entry is a single audit log record.
type Entry struct {
	Timestamp         time.Time `json:"timestamp"`
	ClusterID         string    `json:"clusterID"`
	User              string    `json:"user"`
	SourceIP          string    `json:"sourceIP"`
	Action            Action    `json:"action"`
	ResourceKind      string    `json:"resourceKind,omitempty"`
	ResourceNamespace string    `json:"resourceNamespace,omitempty"`
	ResourceName      string    `json:"resourceName,omitempty"`
	Result            Result    `json:"result"`
	Detail            string    `json:"detail,omitempty"`
}

// Logger is the interface for audit logging implementations.
// Step 14 swaps SlogLogger for SQLiteLogger — no middleware changes needed.
type Logger interface {
	Log(ctx context.Context, entry Entry) error
}

// SlogLogger writes audit entries as structured JSON via slog.
// This is the initial implementation; SQLite persistence comes in Step 14.
type SlogLogger struct {
	logger *slog.Logger
}

// NewSlogLogger creates an audit logger that writes to slog.
func NewSlogLogger(logger *slog.Logger) *SlogLogger {
	return &SlogLogger{
		logger: logger.With("component", "audit"),
	}
}

// Log writes an audit entry to the structured log output.
func (l *SlogLogger) Log(_ context.Context, e Entry) error {
	l.logger.Info("audit",
		"timestamp", e.Timestamp,
		"clusterID", e.ClusterID,
		"user", e.User,
		"sourceIP", e.SourceIP,
		"action", e.Action,
		"resourceKind", e.ResourceKind,
		"resourceNamespace", e.ResourceNamespace,
		"resourceName", e.ResourceName,
		"result", e.Result,
		"detail", e.Detail,
	)
	return nil
}
