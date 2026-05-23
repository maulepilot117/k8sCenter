package audit

import (
	"context"
	"log/slog"
	"time"
)

// Action represents an auditable operation.
type Action string

const (
	ActionCreate              Action = "create"
	ActionUpdate              Action = "update"
	ActionDelete              Action = "delete"
	ActionReveal              Action = "reveal" // secret reveal
	ActionApply               Action = "apply"  // YAML server-side apply
	ActionLogin               Action = "login"
	ActionLogout              Action = "logout"
	ActionRefresh             Action = "refresh"
	ActionSetup               Action = "setup"
	ActionAlertRuleCreate     Action = "alert_rule_create"
	ActionAlertRuleUpdate     Action = "alert_rule_update"
	ActionAlertRuleDelete     Action = "alert_rule_delete"
	ActionAlertSettingsUpdate Action = "alert_settings_update"
	ActionAlertTest           Action = "alert_test"
	ActionReadLogs            Action = "read_logs"
	ActionAgentExec           Action = "agent_exec"
	ActionGitOpsSync          Action = "gitops_sync"
	ActionGitOpsSuspend       Action = "gitops_suspend"
	ActionGitOpsResume        Action = "gitops_resume"
	ActionGitOpsRollback      Action = "gitops_rollback"

	// Velero actions
	ActionVeleroBackupCreate    Action = "velero_backup_create"
	ActionVeleroBackupDelete    Action = "velero_backup_delete"
	ActionVeleroRestoreCreate   Action = "velero_restore_create"
	ActionVeleroScheduleCreate  Action = "velero_schedule_create"
	ActionVeleroScheduleUpdate  Action = "velero_schedule_update"
	ActionVeleroScheduleDelete  Action = "velero_schedule_delete"
	ActionVeleroScheduleTrigger Action = "velero_schedule_trigger"

	// Cert-manager actions
	ActionCertRenew   Action = "cert_renew"
	ActionCertReissue Action = "cert_reissue"

	// External Secrets Operator actions (Phase E)
	ActionESOForceSync             Action = "eso_force_sync"
	ActionESOBulkRefresh           Action = "eso_bulk_refresh"
	ActionESOBulkRefreshNamespace  Action = "eso_bulk_refresh_namespace"

	// Infrastructure / cross-cutting actions
	//
	// ActionRateLimited fires when the rate-limit middleware rejects a
	// request before the handler runs. Surfaces brute-force probing in
	// the audit table — previously rate-limited requests left no trace,
	// which is what issue #276 fixes. Recorded with SourceIP populated;
	// User is empty (the 429 fires before any auth happens). The Detail
	// field carries "<METHOD> <path>: rate limited" so investigators can
	// filter by route.
	ActionRateLimited Action = "rate_limited"
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
	// SourceIP is the IP as seen after chi's RealIP middleware has applied
	// X-Forwarded-For / X-Real-IP rewriting. Behind a trusted load-balancer
	// this is the client's logical IP; without a load-balancer this equals
	// the TCP peer address. Do not use this field for access-control
	// decisions — it can be spoofed by an attacker who controls headers.
	SourceIP          string    `json:"sourceIP"`
	// ConnectionIP is the raw TCP socket peer address captured by
	// CaptureSocketPeer BEFORE chi's RealIP rewrote r.RemoteAddr. This is
	// the ground-truth network peer and is safe to use for access-control
	// decisions (e.g., the loopback-setup gate). Populated only in-memory
	// and in the slog output; not persisted to the audit_logs PostgreSQL
	// table in this version (follow-up: add connection_ip column to
	// migrations). See Finding #1+#8, ce-code-review 2026-05-22.
	ConnectionIP      string    `json:"connectionIP,omitempty"`
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
	args := []any{
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
	}
	if e.ConnectionIP != "" {
		args = append(args, "connectionIP", e.ConnectionIP)
	}
	l.logger.Info("audit", args...)
	return nil
}
