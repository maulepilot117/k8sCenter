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

// ErrLokiBindingNotFound is returned when a registered cluster has no Loki
// binding.
var ErrLokiBindingNotFound = errors.New("loki binding not found")

// ErrLokiBindingChanged is returned by Upsert when it was asked to keep the
// stored token but the stored Loki URL is no longer the one the caller read:
// another write moved the binding in between, so the stored token may belong
// to a different host. Nothing is written.
var ErrLokiBindingChanged = errors.New("loki binding changed concurrently")

// LokiBinding is a remote cluster's Loki binding (KTD8). The bearer token is
// never part of it: Get returns the decrypted token separately, and HasToken
// only reports that a usable one is stored.
type LokiBinding struct {
	ClusterID string
	URL       string
	// TenantID is sent as X-Scope-OrgID when non-empty.
	TenantID string
	// HasToken reports a usable token: one is stored and its stored origin is
	// the current URL's origin.
	HasToken bool
	// TokenStale reports a token that is stored but not usable because its
	// stored origin is not the current URL's origin. It must be entered again.
	TokenStale bool
	UpdatedAt  time.Time
}

// ClusterLogsStore persists per-cluster Loki bindings in the
// cluster_monitoring row the metrics binding also uses. The token is
// encrypted at rest with the same master secret as cluster credentials.
type ClusterLogsStore struct {
	pool          *pgxpool.Pool
	encryptionKey string
}

// NewClusterLogsStore creates a Loki binding store backed by PostgreSQL.
func NewClusterLogsStore(pool *pgxpool.Pool, encryptionKey string) *ClusterLogsStore {
	return &ClusterLogsStore{pool: pool, encryptionKey: encryptionKey}
}

// Get returns a cluster's Loki binding and its decrypted bearer token, which
// is empty unless its stored origin is the current URL's origin, so it never
// reaches a host it was not entered for.
//
// It reads from the cluster registry outward: a cluster that is not
// registered is a wrapped pgx.ErrNoRows (classified as cluster_unknown, like
// every other registry lookup), and a registered cluster with no Loki URL is
// ErrLokiBindingNotFound.
func (s *ClusterLogsStore) Get(ctx context.Context, clusterID string) (LokiBinding, string, error) {
	var (
		b         LokiBinding
		lokiURL   *string
		updatedAt *time.Time
		encTok    []byte
		origin    string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT c.id, m.loki_url, COALESCE(m.loki_tenant_id, ''), m.loki_token,
		       COALESCE(m.loki_token_origin, ''), m.loki_updated_at
		FROM clusters c
		LEFT JOIN cluster_monitoring m ON m.cluster_id = c.id
		WHERE c.id = $1`, clusterID).
		Scan(&b.ClusterID, &lokiURL, &b.TenantID, &encTok, &origin, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LokiBinding{}, "", fmt.Errorf("getting loki binding: cluster %s: %w", clusterID, err)
	}
	if err != nil {
		return LokiBinding{}, "", fmt.Errorf("getting loki binding: %w", err)
	}
	if lokiURL == nil || *lokiURL == "" {
		return LokiBinding{}, "", ErrLokiBindingNotFound
	}
	b.URL = *lokiURL
	if updatedAt != nil {
		b.UpdatedAt = *updatedAt
	}

	var token string
	b.HasToken, b.TokenStale = pinnedTokenState(b.URL, len(encTok) > 0, origin)
	if b.HasToken {
		plain, err := Decrypt(encTok, s.encryptionKey)
		if err != nil {
			return LokiBinding{}, "", fmt.Errorf("decrypting loki token: %w", err)
		}
		token = string(plain)
	}
	return b, token, nil
}

// Revision returns the binding's loki_updated_at, which every Upsert
// refreshes, without decrypting anything. A caller that caches a client
// built from Get compares it with that binding's UpdatedAt, so a write made
// by another pod is noticed on the next read. Metrics binding writes do not
// move it. Its errors match Get.
func (s *ClusterLogsStore) Revision(ctx context.Context, clusterID string) (time.Time, error) {
	var (
		id        string
		lokiURL   *string
		updatedAt *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT c.id, m.loki_url, m.loki_updated_at
		FROM clusters c
		LEFT JOIN cluster_monitoring m ON m.cluster_id = c.id
		WHERE c.id = $1`, clusterID).Scan(&id, &lokiURL, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, fmt.Errorf("getting loki binding revision: cluster %s: %w", clusterID, err)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("getting loki binding revision: %w", err)
	}
	if lokiURL == nil || *lokiURL == "" || updatedAt == nil {
		return time.Time{}, ErrLokiBindingNotFound
	}
	return *updatedAt, nil
}

// LokiBindingWrite is one Upsert. URL is required. Token is nil to keep the
// stored token, a pointer to "" to clear it, or a value to replace it
// (encrypted, and pinned to URL's origin).
type LokiBindingWrite struct {
	URL      string
	TenantID string
	Token    *string
	// KeptFrom is the Loki URL the caller read when it decided the stored
	// token may follow ("" meaning no binding). Ignored when Token is
	// supplied.
	KeptFrom string
}

// Upsert creates or replaces a cluster's Loki binding and returns the
// result. It never touches the metrics binding's columns or updated_at.
//
// An omitted token guards the URL: the update happens only if the stored
// Loki URL equals KeptFrom. The check and the write are one statement, so a
// concurrent save that moved the URL (and its token) in between yields
// ErrLokiBindingChanged instead of pairing this save's host with that save's
// token. A kept token survives only when its stored origin equals both the
// origin of KeptFrom (it was not already stale) and the new URL's origin (it
// does not move to another host); otherwise the token and its origin are
// written as NULL.
func (s *ClusterLogsStore) Upsert(ctx context.Context, clusterID string, w LokiBindingWrite) (LokiBinding, error) {
	if w.URL == "" {
		return LokiBinding{}, errors.New("upserting loki binding: a Loki URL is required")
	}
	var (
		encTok   []byte
		setToken = w.Token != nil
	)
	if setToken && *w.Token != "" {
		var err error
		encTok, err = Encrypt([]byte(*w.Token), s.encryptionKey)
		if err != nil {
			return LokiBinding{}, fmt.Errorf("encrypting loki token: %w", err)
		}
	}

	var (
		b           LokiBinding
		tokenStored bool
		origin      string
	)
	// $5 is the new URL's origin, $8 the origin of the URL the caller read.
	err := s.pool.QueryRow(ctx, `
		INSERT INTO cluster_monitoring (cluster_id, loki_url, loki_tenant_id, loki_token,
			loki_token_origin, loki_updated_at)
		VALUES ($1, $2, NULLIF($3::text, ''), $4::bytea,
			CASE WHEN $4::bytea IS NULL THEN NULL ELSE NULLIF($5::text, '') END, now())
		ON CONFLICT (cluster_id) DO UPDATE SET
			loki_url       = EXCLUDED.loki_url,
			loki_tenant_id = EXCLUDED.loki_tenant_id,
			loki_token = CASE
				WHEN $6::boolean THEN EXCLUDED.loki_token
				WHEN cluster_monitoring.loki_token IS NOT NULL
				 AND $5::text <> ''
				 AND cluster_monitoring.loki_token_origin = $5::text
				 AND cluster_monitoring.loki_token_origin = $8::text
					THEN cluster_monitoring.loki_token
				ELSE NULL END,
			loki_token_origin = CASE
				WHEN $6::boolean THEN EXCLUDED.loki_token_origin
				WHEN cluster_monitoring.loki_token IS NOT NULL
				 AND $5::text <> ''
				 AND cluster_monitoring.loki_token_origin = $5::text
				 AND cluster_monitoring.loki_token_origin = $8::text
					THEN cluster_monitoring.loki_token_origin
				ELSE NULL END,
			loki_updated_at = now()
		WHERE $6::boolean OR COALESCE(cluster_monitoring.loki_url, '') = $7::text
		RETURNING cluster_id, loki_url, COALESCE(loki_tenant_id, ''), loki_token IS NOT NULL,
		          COALESCE(loki_token_origin, ''), loki_updated_at`,
		clusterID, w.URL, w.TenantID, encTok, urlorigin.Of(w.URL), setToken, w.KeptFrom, urlorigin.Of(w.KeptFrom)).
		Scan(&b.ClusterID, &b.URL, &b.TenantID, &tokenStored, &origin, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The conflict branch's WHERE refused the update: the stored URL is no
		// longer the one the caller read.
		return LokiBinding{}, ErrLokiBindingChanged
	}
	if err != nil {
		return LokiBinding{}, fmt.Errorf("upserting loki binding: %w", err)
	}
	b.HasToken, b.TokenStale = pinnedTokenState(b.URL, tokenStored, origin)
	return b, nil
}

// Delete removes a cluster's Loki binding. The row also holds the metrics
// binding, so only the Loki columns are cleared, and the row goes once
// neither binding is left (clearBinding). It returns ErrLokiBindingNotFound
// when no Loki binding was stored.
//
// The SET list names every Loki binding column: a column added to the
// binding must be added here too, or it survives the delete.
func (s *ClusterLogsStore) Delete(ctx context.Context, clusterID string) error {
	cleared, err := clearBinding(ctx, s.pool, `
		UPDATE cluster_monitoring SET
			loki_url = NULL, loki_token = NULL, loki_token_origin = NULL,
			loki_tenant_id = NULL, loki_updated_at = NULL
		WHERE cluster_id = $1 AND COALESCE(loki_url, '') <> ''`, clusterID)
	if err != nil {
		return fmt.Errorf("deleting loki binding: %w", err)
	}
	if !cleared {
		return ErrLokiBindingNotFound
	}
	return nil
}
