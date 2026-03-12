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
)
