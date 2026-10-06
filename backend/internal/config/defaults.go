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

	// Changes (Release E tracked changes) defaults
	DefaultChangesReceiptRetentionDays = 30

	// Incidents (Release D) defaults. The evidence limits equal the store's SQL
	// CHECK ceilings and incidents.DefaultLimits; a test in package incidents
	// keeps the three in step.
	DefaultIncidentsRetentionDays    = 30
	DefaultIncidentsMaxItemBytes     = 1 << 20
	DefaultIncidentsMaxIncidentBytes = 10 << 20
	DefaultIncidentsMaxItems         = 500
	DefaultIncidentsMaxScopes        = 20
	DefaultIncidentsCaptureTimeout   = 15 * time.Second
	DefaultIncidentsSourceTimeout    = 5 * time.Second
	DefaultIncidentsMaxConcurrency   = 4

	// Alerting defaults
	DefaultAlertingEnabled       = false
	DefaultAlertingRetentionDays = 30
	DefaultAlertingRateLimit     = 120 // max emails per hour
	DefaultAlertingSMTPPort      = 587
)
