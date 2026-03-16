package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AppSettings represents the mutable application settings stored in PostgreSQL.
// Fields are pointers so nil means "not set in DB, use config default".
type AppSettings struct {
	MonitoringPrometheusURL *string  `json:"monitoringPrometheusUrl,omitempty"`
	MonitoringGrafanaURL    *string  `json:"monitoringGrafanaUrl,omitempty"`
	MonitoringGrafanaToken  *string  `json:"monitoringGrafanaToken,omitempty"`
	MonitoringNamespace     *string  `json:"monitoringNamespace,omitempty"`
	AlertingEnabled         *bool    `json:"alertingEnabled,omitempty"`
	AlertingSMTPHost        *string  `json:"alertingSmtpHost,omitempty"`
	AlertingSMTPPort        *int     `json:"alertingSmtpPort,omitempty"`
	AlertingSMTPUsername    *string  `json:"alertingSmtpUsername,omitempty"`
	AlertingSMTPPassword    *string  `json:"alertingSmtpPassword,omitempty"`
	AlertingSMTPFrom        *string  `json:"alertingSmtpFrom,omitempty"`
	AlertingRateLimit       *int     `json:"alertingRateLimit,omitempty"`
	AlertingRecipients      []string `json:"alertingRecipients,omitempty"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

// SettingsService manages persistent application settings with an in-memory cache.
type SettingsService struct {
	pool  *pgxpool.Pool
	mu    sync.RWMutex
	cache *AppSettings
}

// NewSettingsService creates a settings service backed by PostgreSQL.
func NewSettingsService(pool *pgxpool.Pool) *SettingsService {
	return &SettingsService{pool: pool}
}

// Get returns the current settings from cache (refreshes if stale).
func (s *SettingsService) Get(ctx context.Context) (*AppSettings, error) {
	s.mu.RLock()
	if s.cache != nil {
		cached := *s.cache
		s.mu.RUnlock()
		return &cached, nil
	}
	s.mu.RUnlock()

	return s.refresh(ctx)
}

// Update patches settings in the database and refreshes the cache.
func (s *SettingsService) Update(ctx context.Context, patch AppSettings) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE app_settings SET
			monitoring_prometheus_url = COALESCE($1, monitoring_prometheus_url),
			monitoring_grafana_url = COALESCE($2, monitoring_grafana_url),
			monitoring_grafana_token = COALESCE($3, monitoring_grafana_token),
			monitoring_namespace = COALESCE($4, monitoring_namespace),
			alerting_enabled = COALESCE($5, alerting_enabled),
			alerting_smtp_host = COALESCE($6, alerting_smtp_host),
			alerting_smtp_port = COALESCE($7, alerting_smtp_port),
			alerting_smtp_username = COALESCE($8, alerting_smtp_username),
			alerting_smtp_password = COALESCE($9, alerting_smtp_password),
			alerting_smtp_from = COALESCE($10, alerting_smtp_from),
			alerting_rate_limit = COALESCE($11, alerting_rate_limit),
			alerting_recipients = COALESCE($12, alerting_recipients),
			updated_at = NOW()
		WHERE id = 1`,
		patch.MonitoringPrometheusURL, patch.MonitoringGrafanaURL,
		patch.MonitoringGrafanaToken, patch.MonitoringNamespace,
		patch.AlertingEnabled, patch.AlertingSMTPHost,
		patch.AlertingSMTPPort, patch.AlertingSMTPUsername,
		patch.AlertingSMTPPassword, patch.AlertingSMTPFrom,
		patch.AlertingRateLimit, patch.AlertingRecipients,
	)
	if err != nil {
		return fmt.Errorf("updating settings: %w", err)
	}

	// Refresh cache
	_, err = s.refresh(ctx)
	return err
}

func (s *SettingsService) refresh(ctx context.Context) (*AppSettings, error) {
	var settings AppSettings
	err := s.pool.QueryRow(ctx, `
		SELECT monitoring_prometheus_url, monitoring_grafana_url, monitoring_grafana_token,
		       monitoring_namespace, alerting_enabled, alerting_smtp_host, alerting_smtp_port,
		       alerting_smtp_username, alerting_smtp_password, alerting_smtp_from,
		       alerting_rate_limit, alerting_recipients, updated_at
		FROM app_settings WHERE id = 1`).Scan(
		&settings.MonitoringPrometheusURL, &settings.MonitoringGrafanaURL,
		&settings.MonitoringGrafanaToken, &settings.MonitoringNamespace,
		&settings.AlertingEnabled, &settings.AlertingSMTPHost,
		&settings.AlertingSMTPPort, &settings.AlertingSMTPUsername,
		&settings.AlertingSMTPPassword, &settings.AlertingSMTPFrom,
		&settings.AlertingRateLimit, &settings.AlertingRecipients,
		&settings.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}

	s.mu.Lock()
	s.cache = &settings
	s.mu.Unlock()

	return &settings, nil
}

// MaskedSettings returns settings with sensitive fields masked.
func MaskedSettings(s *AppSettings) *AppSettings {
	masked := *s
	if masked.MonitoringGrafanaToken != nil && *masked.MonitoringGrafanaToken != "" {
		m := "****"
		masked.MonitoringGrafanaToken = &m
	}
	if masked.AlertingSMTPPassword != nil && *masked.AlertingSMTPPassword != "" {
		m := "****"
		masked.AlertingSMTPPassword = &m
	}
	if masked.AlertingSMTPUsername != nil && *masked.AlertingSMTPUsername != "" {
		m := "****"
		masked.AlertingSMTPUsername = &m
	}
	return &masked
}
