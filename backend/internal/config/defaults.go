package config

import "time"

const (
	DefaultPort            = 8080
	DefaultLogLevel        = "info"
	DefaultLogFormat       = "json"
	DefaultShutdownTimeout = 30 * time.Second
	DefaultRequestTimeout  = 60 * time.Second
	DefaultClusterID       = "local"
	DefaultDevMode         = false

	// Audit defaults
	DefaultAuditRetentionDays = 90

	// Alerting defaults
	DefaultAlertingEnabled       = false
	DefaultAlertingRetentionDays = 30
	DefaultAlertingRateLimit     = 120 // max emails per hour
	DefaultAlertingSMTPPort      = 587
)
