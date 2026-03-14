package server

import (
	"encoding/json"
	"net/http"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// handleGetAuthSettings returns the current auth configuration (secrets masked).
func (s *Server) handleGetAuthSettings(w http.ResponseWriter, r *http.Request) {
	providers := s.AuthRegistry.ListProviders()
	writeJSON(w, http.StatusOK, api.Response{Data: providers})
}

// handleTestOIDC tests OIDC provider discovery against a given issuer URL.
func (s *Server) handleTestOIDC(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)

	var req struct {
		IssuerURL string `json:"issuerURL"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IssuerURL == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "issuerURL is required"},
		})
		return
	}

	// Attempt OIDC discovery
	_, err := auth.NewOIDCProvider(r.Context(), auth.OIDCProviderConfig{
		ID:        "test",
		IssuerURL: req.IssuerURL,
		ClientID:  "test-client",
		// RedirectURL not needed for discovery test
		RedirectURL: "http://localhost/callback",
	}, auth.NewOIDCStateStore(), s.Logger)

	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "OIDC discovery failed"},
		})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]string{"status": "ok"},
	})
}

// handleTestLDAP tests LDAP connectivity and service account bind.
func (s *Server) handleTestLDAP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)

	var req struct {
		URL          string `json:"url"`
		BindDN       string `json:"bindDN"`
		BindPassword string `json:"bindPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "url is required"},
		})
		return
	}

	provider := auth.NewLDAPProvider(auth.LDAPProviderConfig{
		ID:           "test",
		URL:          req.URL,
		BindDN:       req.BindDN,
		BindPassword: req.BindPassword,
	}, s.Logger)

	if err := provider.TestConnection(r.Context()); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "LDAP connection test failed"},
		})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]string{"status": "ok"},
	})
}
