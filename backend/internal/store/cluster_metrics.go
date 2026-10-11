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

// ErrMetricsBindingChanged is returned by Upsert when it was asked to keep the
// stored token but the binding's Prometheus URL is no longer the one the
// caller checked: another write moved the binding in between, so the stored
// token may belong to a different host. Nothing is written.
var ErrMetricsBindingChanged = errors.New("metrics binding changed concurrently")

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
// when none is stored). It reads from the cluster registry outward, so the two
// "nothing to read" cases stay apart: a cluster that is not registered is a
// wrapped pgx.ErrNoRows (classified as cluster_unknown, like every other
// registry lookup), and a registered cluster with no binding row, or a row
// without a Prometheus URL (a legacy grafana-only row), is
// ErrMetricsBindingNotFound.
func (s *ClusterMetricsStore) Get(ctx context.Context, clusterID string) (MetricsBinding, string, error) {
	var (
		b         MetricsBinding
		promURL   *string
		updatedAt *time.Time
		encTok    []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT c.id, m.prometheus_url, COALESCE(m.alertmanager_url, ''), m.prometheus_token, m.updated_at
		FROM clusters c
		LEFT JOIN cluster_monitoring m ON m.cluster_id = c.id
		WHERE c.id = $1`, clusterID).
		Scan(&b.ClusterID, &promURL, &b.AlertmanagerURL, &encTok, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetricsBinding{}, "", fmt.Errorf("getting metrics binding: cluster %s: %w", clusterID, err)
	}
	if err != nil {
		return MetricsBinding{}, "", fmt.Errorf("getting metrics binding: %w", err)
	}
	if promURL == nil || *promURL == "" {
		return MetricsBinding{}, "", ErrMetricsBindingNotFound
	}
	b.PrometheusURL = *promURL
	if updatedAt != nil {
		b.UpdatedAt = *updatedAt
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
//
// keptFrom guards the keep case. With a nil token the write happens only if
// the row's current Prometheus URL equals keptFrom ("" meaning no binding),
// the URL the caller read when it decided the stored token may follow the new
// one. The check and the write are one statement, so a concurrent save that
// moved the binding (and its token) elsewhere in between yields
// ErrMetricsBindingChanged instead of pairing this URL with that token.
// keptFrom is ignored when a token is supplied.
func (s *ClusterMetricsStore) Upsert(ctx context.Context, clusterID, prometheusURL, alertmanagerURL string, token *string, keptFrom string) (MetricsBinding, error) {
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
		WHERE $5::boolean OR COALESCE(cluster_monitoring.prometheus_url, '') = $6
		RETURNING cluster_id, prometheus_url, COALESCE(alertmanager_url, ''),
		          prometheus_token IS NOT NULL, updated_at`,
		clusterID, prometheusURL, alertmanagerURL, encTok, setToken, keptFrom).
		Scan(&b.ClusterID, &b.PrometheusURL, &b.AlertmanagerURL, &b.HasToken, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The conflict branch's WHERE refused the update: the stored URL is
		// no longer keptFrom.
		return MetricsBinding{}, ErrMetricsBindingChanged
	}
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
