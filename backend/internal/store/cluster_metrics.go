package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

// ErrMetricsBindingNotFound is returned when a cluster has no metrics binding.
var ErrMetricsBindingNotFound = errors.New("metrics binding not found")

// ErrMetricsBindingChanged is returned by Upsert when it was asked to keep a
// stored token but the binding's URL for that token is no longer the one the
// caller checked: another write moved the binding in between, so the stored
// token may belong to a different host. Nothing is written.
var ErrMetricsBindingChanged = errors.New("metrics binding changed concurrently")

// MetricsBinding is a remote cluster's Prometheus and Alertmanager binding.
// The bearer tokens are never part of it: Get returns the decrypted tokens
// separately, and the Has flags only report that a usable one is stored.
type MetricsBinding struct {
	ClusterID       string
	PrometheusURL   string
	AlertmanagerURL string
	HasToken        bool
	// HasAlertmanagerToken reports a usable Alertmanager token: one is stored
	// and its stored origin is the current Alertmanager URL's origin.
	HasAlertmanagerToken bool
	// AlertmanagerTokenStale reports an Alertmanager token that is stored but
	// not usable because its stored origin (empty or NULL included) is not
	// the current Alertmanager URL's origin, for example after a
	// previous-release pod moved the URL. It must be entered again. It is
	// false when no token is stored and when there is no Alertmanager URL.
	AlertmanagerTokenStale bool
	UpdatedAt              time.Time
}

// MetricsTokens holds a binding's decrypted bearer tokens. An empty field
// means no usable token. Never log, return or audit it.
type MetricsTokens struct {
	Prometheus   string
	Alertmanager string
}

// ClusterMetricsStore persists per-cluster metrics bindings in the
// cluster_monitoring table. The tokens are encrypted at rest with the same
// master secret as cluster credentials.
type ClusterMetricsStore struct {
	pool          *pgxpool.Pool
	encryptionKey string
}

// NewClusterMetricsStore creates a metrics binding store backed by PostgreSQL.
func NewClusterMetricsStore(pool *pgxpool.Pool, encryptionKey string) *ClusterMetricsStore {
	return &ClusterMetricsStore{pool: pool, encryptionKey: encryptionKey}
}

// alertmanagerTokenState applies the Alertmanager token origin rule to a row:
// a stored token is usable only when the Alertmanager URL is non-empty and
// its origin equals the stored origin; a stored token that fails that check
// while a URL is set is stale. It fails closed: an empty stored origin, or a
// URL without an origin, never matches.
func alertmanagerTokenState(alertmanagerURL string, tokenStored bool, storedOrigin string) (usable, stale bool) {
	if !tokenStored || alertmanagerURL == "" {
		return false, false
	}
	current := urlorigin.Of(alertmanagerURL)
	if current != "" && storedOrigin == current {
		return true, false
	}
	return false, true
}

// Get returns the binding for a cluster and its decrypted bearer tokens
// (empty when none is usable). The Alertmanager token is returned only while
// its stored origin is the current Alertmanager URL's origin, whichever
// release last wrote the row, so it never reaches a host it was not entered
// for. Every reader (the binding API and the client resolver) goes through
// here.
//
// It reads from the cluster registry outward, so the two "nothing to read"
// cases stay apart: a cluster that is not registered is a wrapped
// pgx.ErrNoRows (classified as cluster_unknown, like every other registry
// lookup), and a registered cluster with no binding row, or a row without a
// Prometheus URL (a legacy grafana-only row), is ErrMetricsBindingNotFound.
func (s *ClusterMetricsStore) Get(ctx context.Context, clusterID string) (MetricsBinding, MetricsTokens, error) {
	var (
		b         MetricsBinding
		promURL   *string
		updatedAt *time.Time
		encTok    []byte
		encAMTok  []byte
		amOrigin  string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT c.id, m.prometheus_url, COALESCE(m.alertmanager_url, ''), m.prometheus_token,
		       m.alertmanager_token, COALESCE(m.alertmanager_token_origin, ''), m.updated_at
		FROM clusters c
		LEFT JOIN cluster_monitoring m ON m.cluster_id = c.id
		WHERE c.id = $1`, clusterID).
		Scan(&b.ClusterID, &promURL, &b.AlertmanagerURL, &encTok, &encAMTok, &amOrigin, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetricsBinding{}, MetricsTokens{}, fmt.Errorf("getting metrics binding: cluster %s: %w", clusterID, err)
	}
	if err != nil {
		return MetricsBinding{}, MetricsTokens{}, fmt.Errorf("getting metrics binding: %w", err)
	}
	if promURL == nil || *promURL == "" {
		return MetricsBinding{}, MetricsTokens{}, ErrMetricsBindingNotFound
	}
	b.PrometheusURL = *promURL
	if updatedAt != nil {
		b.UpdatedAt = *updatedAt
	}

	var toks MetricsTokens
	b.HasToken = len(encTok) > 0
	if b.HasToken {
		plain, err := Decrypt(encTok, s.encryptionKey)
		if err != nil {
			return MetricsBinding{}, MetricsTokens{}, fmt.Errorf("decrypting metrics token: %w", err)
		}
		toks.Prometheus = string(plain)
	}
	b.HasAlertmanagerToken, b.AlertmanagerTokenStale = alertmanagerTokenState(b.AlertmanagerURL, len(encAMTok) > 0, amOrigin)
	if b.HasAlertmanagerToken {
		plain, err := Decrypt(encAMTok, s.encryptionKey)
		if err != nil {
			return MetricsBinding{}, MetricsTokens{}, fmt.Errorf("decrypting alertmanager token: %w", err)
		}
		toks.Alertmanager = string(plain)
	}
	return b, toks, nil
}

// Revision returns the binding's updated_at, which every Upsert refreshes
// (a delete and re-insert gets a new one too), without decrypting anything.
// Callers that cache clients built from Get compare it with the UpdatedAt of
// the binding they built from, so a write made by another pod, which cannot
// evict this pod's cache, is noticed on the next read. Its errors match Get:
// an unregistered cluster is a wrapped pgx.ErrNoRows, and no binding (or a
// row without a Prometheus URL) is ErrMetricsBindingNotFound.
func (s *ClusterMetricsStore) Revision(ctx context.Context, clusterID string) (time.Time, error) {
	var (
		id        string
		promURL   *string
		updatedAt *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT c.id, m.prometheus_url, m.updated_at
		FROM clusters c
		LEFT JOIN cluster_monitoring m ON m.cluster_id = c.id
		WHERE c.id = $1`, clusterID).Scan(&id, &promURL, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, fmt.Errorf("getting metrics binding revision: cluster %s: %w", clusterID, err)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("getting metrics binding revision: %w", err)
	}
	if promURL == nil || *promURL == "" || updatedAt == nil {
		return time.Time{}, ErrMetricsBindingNotFound
	}
	return *updatedAt, nil
}

// MetricsBindingWrite is one Upsert. Each token is nil to keep the stored
// token, a pointer to "" to clear it, or a value to replace it (encrypted).
type MetricsBindingWrite struct {
	PrometheusURL   string
	AlertmanagerURL string
	PrometheusToken *string
	// AlertmanagerToken is cleared, whatever is passed, when AlertmanagerURL
	// is empty.
	AlertmanagerToken *string
	// AlertmanagerTokenOrigin is urlorigin.Of(AlertmanagerURL), computed by
	// the caller. It is stored as given with a new token, and a kept token
	// survives only when its stored origin equals it.
	AlertmanagerTokenOrigin string
	// KeptFrom is the Prometheus URL the caller read when it decided the
	// stored Prometheus token may follow the new one ("" meaning no
	// binding). Ignored when PrometheusToken is supplied.
	KeptFrom string
	// KeptAMFrom is the Alertmanager URL the caller read ("" meaning none),
	// compared whether or not a token was stored then. Ignored when
	// AlertmanagerToken is supplied.
	KeptAMFrom string
}

// Upsert creates or replaces a cluster's binding and returns the result.
// updated_at is always refreshed.
//
// Each omitted token guards its own URL: the update happens only if (a
// Prometheus token is supplied, or the stored Prometheus URL equals KeptFrom)
// and (an Alertmanager token is supplied, or the stored Alertmanager URL
// equals KeptAMFrom). The check and the write are one statement, so a
// concurrent save that moved either URL (and its token) in between yields
// ErrMetricsBindingChanged instead of pairing this save's host with that
// save's token.
//
// A kept Alertmanager token survives only when the stored Alertmanager URL is
// non-empty and the stored token origin equals both the origin of KeptAMFrom
// (the token was not already stale) and AlertmanagerTokenOrigin (it does not
// move to another origin). Otherwise the token and its origin are written as
// NULL, which drops a token a previous-release pod left behind after
// clearing or moving the URL. Clearing the Alertmanager URL clears its token
// and origin.
func (s *ClusterMetricsStore) Upsert(ctx context.Context, clusterID string, w MetricsBindingWrite) (MetricsBinding, error) {
	var (
		encTok     []byte
		encAMTok   []byte
		setToken   = w.PrometheusToken != nil
		setAMToken = w.AlertmanagerToken != nil
	)
	if setToken && *w.PrometheusToken != "" {
		var err error
		encTok, err = Encrypt([]byte(*w.PrometheusToken), s.encryptionKey)
		if err != nil {
			return MetricsBinding{}, fmt.Errorf("encrypting metrics token: %w", err)
		}
	}
	if setAMToken && *w.AlertmanagerToken != "" && w.AlertmanagerURL != "" {
		var err error
		encAMTok, err = Encrypt([]byte(*w.AlertmanagerToken), s.encryptionKey)
		if err != nil {
			return MetricsBinding{}, fmt.Errorf("encrypting alertmanager token: %w", err)
		}
	}

	var (
		b             MetricsBinding
		amTokenStored bool
		amOrigin      string
	)
	// $8 is the new URL's origin, $11 the origin of the URL the caller read.
	err := s.pool.QueryRow(ctx, `
		INSERT INTO cluster_monitoring (cluster_id, prometheus_url, alertmanager_url, prometheus_token,
			alertmanager_token, alertmanager_token_origin, updated_at)
		VALUES ($1, $2, NULLIF($3::text, ''), $4, $7::bytea,
			CASE WHEN $7::bytea IS NULL THEN NULL ELSE NULLIF($8::text, '') END, now())
		ON CONFLICT (cluster_id) DO UPDATE SET
			prometheus_url   = EXCLUDED.prometheus_url,
			alertmanager_url = EXCLUDED.alertmanager_url,
			prometheus_token = CASE WHEN $5::boolean THEN EXCLUDED.prometheus_token
			                        ELSE cluster_monitoring.prometheus_token END,
			alertmanager_token = CASE
				WHEN $9::boolean OR EXCLUDED.alertmanager_url IS NULL THEN EXCLUDED.alertmanager_token
				WHEN cluster_monitoring.alertmanager_token IS NOT NULL
				 AND COALESCE(cluster_monitoring.alertmanager_url, '') <> ''
				 AND $8::text <> ''
				 AND cluster_monitoring.alertmanager_token_origin = $8::text
				 AND cluster_monitoring.alertmanager_token_origin = $11::text
					THEN cluster_monitoring.alertmanager_token
				ELSE NULL END,
			alertmanager_token_origin = CASE
				WHEN $9::boolean OR EXCLUDED.alertmanager_url IS NULL THEN EXCLUDED.alertmanager_token_origin
				WHEN cluster_monitoring.alertmanager_token IS NOT NULL
				 AND COALESCE(cluster_monitoring.alertmanager_url, '') <> ''
				 AND $8::text <> ''
				 AND cluster_monitoring.alertmanager_token_origin = $8::text
				 AND cluster_monitoring.alertmanager_token_origin = $11::text
					THEN cluster_monitoring.alertmanager_token_origin
				ELSE NULL END,
			updated_at       = now()
		WHERE ($5::boolean OR COALESCE(cluster_monitoring.prometheus_url, '') = $6::text)
		  AND ($9::boolean OR COALESCE(cluster_monitoring.alertmanager_url, '') = $10::text)
		RETURNING cluster_id, prometheus_url, COALESCE(alertmanager_url, ''),
		          prometheus_token IS NOT NULL, alertmanager_token IS NOT NULL,
		          COALESCE(alertmanager_token_origin, ''), updated_at`,
		clusterID, w.PrometheusURL, w.AlertmanagerURL, encTok, setToken, w.KeptFrom,
		encAMTok, w.AlertmanagerTokenOrigin, setAMToken, w.KeptAMFrom, urlorigin.Of(w.KeptAMFrom)).
		Scan(&b.ClusterID, &b.PrometheusURL, &b.AlertmanagerURL, &b.HasToken, &amTokenStored, &amOrigin, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The conflict branch's WHERE refused the update: a stored URL is no
		// longer the one the caller read.
		return MetricsBinding{}, ErrMetricsBindingChanged
	}
	if err != nil {
		return MetricsBinding{}, fmt.Errorf("upserting metrics binding: %w", err)
	}
	b.HasAlertmanagerToken, b.AlertmanagerTokenStale = alertmanagerTokenState(b.AlertmanagerURL, amTokenStored, amOrigin)
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
