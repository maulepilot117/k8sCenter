package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

var validClusterName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// handleListClusters returns all registered clusters.
func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	if s.ClusterStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, api.Response{
			Error: &api.APIError{Code: 503, Message: "cluster management requires a database"},
		})
		return
	}

	clusters, err := s.ClusterStore.List(r.Context())
	if err != nil {
		s.Logger.Error("failed to list clusters", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to list clusters"},
		})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{Data: clusters})
}

// handleGetCluster returns a single cluster by ID (credentials excluded).
func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	if s.ClusterStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, api.Response{
			Error: &api.APIError{Code: 503, Message: "cluster management requires a database"},
		})
		return
	}

	id := chi.URLParam(r, "clusterID")
	cluster, err := s.ClusterStore.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Response{
			Error: &api.APIError{Code: 404, Message: "cluster not found"},
		})
		return
	}

	// Strip credentials from response
	cluster.CAData = nil
	cluster.AuthData = nil

	writeJSON(w, http.StatusOK, api.Response{Data: cluster})
}

// handleCreateCluster registers a new cluster.
func (s *Server) handleCreateCluster(w http.ResponseWriter, r *http.Request) {
	if s.ClusterStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, api.Response{
			Error: &api.APIError{Code: 503, Message: "cluster management requires a database"},
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB max

	var req struct {
		Name         string `json:"name"`
		DisplayName  string `json:"displayName"`
		APIServerURL string `json:"apiServerUrl"`
		CACert       string `json:"caCert"`
		Token        string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	// Validate required fields
	if req.Name == "" || req.APIServerURL == "" || req.Token == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "name, apiServerUrl, and token are required"},
		})
		return
	}

	// Validate cluster name format (DNS label)
	if !validClusterName.MatchString(req.Name) {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "name must be lowercase alphanumeric with hyphens (max 63 chars)"},
		})
		return
	}

	// Validate API server URL — must be HTTPS
	parsedURL, err := url.Parse(req.APIServerURL)
	if err != nil || !strings.EqualFold(parsedURL.Scheme, "https") || parsedURL.Host == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "apiServerUrl must be a valid HTTPS URL"},
		})
		return
	}

	// Validate token length
	if len(req.Token) > 65536 {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "token too large (max 64KB)"},
		})
		return
	}

	id, err := generateClusterID()
	if err != nil {
		s.Logger.Error("failed to generate cluster ID", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "internal error"},
		})
		return
	}

	record := store.ClusterRecord{
		ID:           id,
		Name:         req.Name,
		DisplayName:  req.DisplayName,
		APIServerURL: req.APIServerURL,
		CAData:       []byte(req.CACert),
		AuthType:     "token",
		AuthData:     []byte(req.Token),
	}

	if err := s.ClusterStore.Create(r.Context(), record); err != nil {
		s.Logger.Error("failed to create cluster", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to register cluster"},
		})
		return
	}

	// Audit log
	user, _ := auth.UserFromContext(r.Context())
	if user != nil {
		entry := s.newAuditEntry(r, user.Username, audit.ActionCreate, audit.ResultSuccess)
		entry.ResourceKind = "cluster"
		entry.ResourceName = req.Name
		entry.Detail = "cluster registered: " + req.Name
		s.AuditLogger.Log(r.Context(), entry)
	}

	// Return the cluster (without credentials)
	record.CAData = nil
	record.AuthData = nil
	writeJSON(w, http.StatusCreated, api.Response{Data: record})
}

// handleDeleteCluster removes a registered cluster.
func (s *Server) handleDeleteCluster(w http.ResponseWriter, r *http.Request) {
	if s.ClusterStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, api.Response{
			Error: &api.APIError{Code: 503, Message: "cluster management requires a database"},
		})
		return
	}

	id := chi.URLParam(r, "clusterID")
	if err := s.ClusterStore.Delete(r.Context(), id); err != nil {
		// Don't leak internal database errors
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "cluster not found or cannot be deleted"},
		})
		return
	}

	// Audit log
	user, _ := auth.UserFromContext(r.Context())
	if user != nil {
		entry := s.newAuditEntry(r, user.Username, audit.ActionDelete, audit.ResultSuccess)
		entry.ResourceKind = "cluster"
		entry.ResourceName = id
		s.AuditLogger.Log(r.Context(), entry)
	}

	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "deleted"}})
}

// generateClusterID creates a 128-bit cryptographically random ID.
func generateClusterID() (string, error) {
	b := make([]byte, 16) // 128 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
