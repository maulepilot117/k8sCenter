package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClusterRecord represents a registered cluster in the database.
type ClusterRecord struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	DisplayName   string    `json:"displayName,omitempty"`
	APIServerURL  string    `json:"apiServerUrl"`
	CAData        []byte    `json:"-"`
	AuthType      string    `json:"authType"` // "token", "certificate"
	AuthData      []byte    `json:"-"`        // encrypted credentials
	Status        string    `json:"status"`
	StatusMessage string    `json:"statusMessage,omitempty"`
	K8sVersion    string    `json:"k8sVersion,omitempty"`
	NodeCount     int       `json:"nodeCount"`
	IsLocal       bool      `json:"isLocal"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	LastProbedAt  *time.Time `json:"lastProbedAt,omitempty"`
}

// ClusterStore handles CRUD for the clusters table.
// Credentials are encrypted at rest using AES-256-GCM.
type ClusterStore struct {
	pool         *pgxpool.Pool
	encryptionKey string // master secret for encrypting credentials
}

// NewClusterStore creates a cluster store backed by PostgreSQL.
// The encryptionKey is used to encrypt/decrypt credential data at rest.
func NewClusterStore(pool *pgxpool.Pool, encryptionKey string) *ClusterStore {
	return &ClusterStore{pool: pool, encryptionKey: encryptionKey}
}

// List returns all registered clusters.
func (s *ClusterStore) List(ctx context.Context) ([]ClusterRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, display_name, api_server_url, auth_type, status, status_message,
		       k8s_version, node_count, is_local, created_at, updated_at, last_probed_at
		FROM clusters ORDER BY is_local DESC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing clusters: %w", err)
	}
	defer rows.Close()

	var clusters []ClusterRecord
	for rows.Next() {
		var c ClusterRecord
		if err := rows.Scan(&c.ID, &c.Name, &c.DisplayName, &c.APIServerURL, &c.AuthType,
			&c.Status, &c.StatusMessage, &c.K8sVersion, &c.NodeCount, &c.IsLocal,
			&c.CreatedAt, &c.UpdatedAt, &c.LastProbedAt); err != nil {
			return nil, fmt.Errorf("scanning cluster: %w", err)
		}
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

// Get returns a single cluster by ID, including credentials.
func (s *ClusterStore) Get(ctx context.Context, id string) (*ClusterRecord, error) {
	var c ClusterRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, display_name, api_server_url, ca_data, auth_type, auth_data,
		       status, status_message, k8s_version, node_count, is_local, created_at, updated_at, last_probed_at
		FROM clusters WHERE id = $1`, id).Scan(
		&c.ID, &c.Name, &c.DisplayName, &c.APIServerURL, &c.CAData, &c.AuthType, &c.AuthData,
		&c.Status, &c.StatusMessage, &c.K8sVersion, &c.NodeCount, &c.IsLocal,
		&c.CreatedAt, &c.UpdatedAt, &c.LastProbedAt)
	if err != nil {
		return nil, fmt.Errorf("getting cluster %s: %w", id, err)
	}
	return &c, nil
}

// Create inserts a new cluster with encrypted credentials.
func (s *ClusterStore) Create(ctx context.Context, c ClusterRecord) error {
	encAuthData, err := Encrypt(c.AuthData, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("encrypting auth data: %w", err)
	}
	var encCAData []byte
	if len(c.CAData) > 0 {
		encCAData, err = Encrypt(c.CAData, s.encryptionKey)
		if err != nil {
			return fmt.Errorf("encrypting CA data: %w", err)
		}
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO clusters (id, name, display_name, api_server_url, ca_data, auth_type, auth_data, is_local)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.ID, c.Name, c.DisplayName, c.APIServerURL, encCAData, c.AuthType, encAuthData, c.IsLocal)
	if err != nil {
		return fmt.Errorf("creating cluster: %w", err)
	}
	return nil
}

// UpdateCredentials updates a cluster's connection credentials.
func (s *ClusterStore) UpdateCredentials(ctx context.Context, id string, apiServerURL string, caData, authData []byte) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE clusters SET api_server_url = $2, ca_data = $3, auth_data = $4, updated_at = NOW()
		WHERE id = $1`, id, apiServerURL, caData, authData)
	if err != nil {
		return fmt.Errorf("updating cluster credentials: %w", err)
	}
	return nil
}

// UpdateStatus updates a cluster's health status.
func (s *ClusterStore) UpdateStatus(ctx context.Context, id, status, message, k8sVersion string, nodeCount int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE clusters SET status = $2, status_message = $3, k8s_version = $4, node_count = $5,
		       last_probed_at = NOW(), updated_at = NOW()
		WHERE id = $1`, id, status, message, k8sVersion, nodeCount)
	if err != nil {
		return fmt.Errorf("updating cluster status: %w", err)
	}
	return nil
}

// Delete removes a cluster.
func (s *ClusterStore) Delete(ctx context.Context, id string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM clusters WHERE id = $1 AND is_local = false`, id)
	if err != nil {
		return fmt.Errorf("deleting cluster: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("cluster not found or is the local cluster")
	}
	return nil
}

// EnsureLocal creates the local cluster record if it doesn't exist.
func (s *ClusterStore) EnsureLocal(ctx context.Context, clusterID, apiServerURL string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO clusters (id, name, display_name, api_server_url, auth_type, auth_data, is_local, status)
		VALUES ($1, $1, 'Local Cluster', $2, 'in-cluster', '{}', true, 'connected')
		ON CONFLICT (id) DO UPDATE SET api_server_url = $2, status = 'connected', updated_at = NOW()`,
		clusterID, apiServerURL)
	if err != nil {
		return fmt.Errorf("ensuring local cluster: %w", err)
	}
	return nil
}
