package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrMetricsBindingNotFound is returned when a cluster has no metrics binding.
var ErrMetricsBindingNotFound = errors.New("metrics binding not found")

// MetricsBinding is a remote cluster's Prometheus binding. The bearer token is
// never part of it: Get returns the decrypted token separately and HasToken
// only reports that one is stored.
type MetricsBinding struct {
	ClusterID       string
	PrometheusURL   string
	AlertmanagerURL string
	HasToken        bool
	UpdatedAt       time.Time
}

// ClusterMetricsStore persists per-cluster metrics bindings in the
// cluster_monitoring table. The token is encrypted at rest with the same
// master secret as cluster credentials.
type ClusterMetricsStore struct {
	pool          *pgxpool.Pool
	encryptionKey string
}

// NewClusterMetricsStore creates a metrics binding store backed by PostgreSQL.
func NewClusterMetricsStore(pool *pgxpool.Pool, encryptionKey string) *ClusterMetricsStore {
	return &ClusterMetricsStore{pool: pool, encryptionKey: encryptionKey}
}

// Get returns the binding for a cluster and its decrypted bearer token (empty
// when none is stored). A missing row, or a row without a Prometheus URL (a
// legacy grafana-only row), is ErrMetricsBindingNotFound.
func (s *ClusterMetricsStore) Get(ctx context.Context, clusterID string) (MetricsBinding, string, error) {
	var (
		b      MetricsBinding
		encTok []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT cluster_id, prometheus_url, COALESCE(alertmanager_url, ''), prometheus_token, updated_at
		FROM cluster_monitoring
		WHERE cluster_id = $1 AND COALESCE(prometheus_url, '') <> ''`, clusterID).
		Scan(&b.ClusterID, &b.PrometheusURL, &b.AlertmanagerURL, &encTok, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetricsBinding{}, "", ErrMetricsBindingNotFound
	}
	if err != nil {
		return MetricsBinding{}, "", fmt.Errorf("getting metrics binding: %w", err)
	}
	b.HasToken = len(encTok) > 0
	if !b.HasToken {
		return b, "", nil
	}
	plain, err := Decrypt(encTok, s.encryptionKey)
	if err != nil {
		return MetricsBinding{}, "", fmt.Errorf("decrypting metrics token: %w", err)
	}
	return b, string(plain), nil
}

// Upsert creates or replaces a cluster's binding. A nil token keeps the stored
// token, a pointer to "" clears it, and a non-empty value replaces it
// (encrypted). updated_at is always refreshed. It returns the resulting
// binding.
func (s *ClusterMetricsStore) Upsert(ctx context.Context, clusterID, prometheusURL, alertmanagerURL string, token *string) (MetricsBinding, error) {
	var (
		encTok   []byte
		setToken = token != nil
	)
	if setToken && *token != "" {
		var err error
		encTok, err = Encrypt([]byte(*token), s.encryptionKey)
		if err != nil {
			return MetricsBinding{}, fmt.Errorf("encrypting metrics token: %w", err)
		}
	}

	var b MetricsBinding
	err := s.pool.QueryRow(ctx, `
		INSERT INTO cluster_monitoring (cluster_id, prometheus_url, alertmanager_url, prometheus_token, updated_at)
		VALUES ($1, $2, NULLIF($3, ''), $4, now())
		ON CONFLICT (cluster_id) DO UPDATE SET
			prometheus_url   = EXCLUDED.prometheus_url,
			alertmanager_url = EXCLUDED.alertmanager_url,
			prometheus_token = CASE WHEN $5::boolean THEN EXCLUDED.prometheus_token
			                        ELSE cluster_monitoring.prometheus_token END,
			updated_at       = now()
		RETURNING cluster_id, prometheus_url, COALESCE(alertmanager_url, ''),
		          prometheus_token IS NOT NULL, updated_at`,
		clusterID, prometheusURL, alertmanagerURL, encTok, setToken).
		Scan(&b.ClusterID, &b.PrometheusURL, &b.AlertmanagerURL, &b.HasToken, &b.UpdatedAt)
	if err != nil {
		return MetricsBinding{}, fmt.Errorf("upserting metrics binding: %w", err)
	}
	return b, nil
}

// Delete removes a cluster's binding row. It returns ErrMetricsBindingNotFound
// when nothing was stored.
func (s *ClusterMetricsStore) Delete(ctx context.Context, clusterID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM cluster_monitoring WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return fmt.Errorf("deleting metrics binding: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMetricsBindingNotFound
	}
	return nil
}
