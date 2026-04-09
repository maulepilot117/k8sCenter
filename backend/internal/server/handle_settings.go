package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/gitprovider"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/store"
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

	// Validate scheme (OIDC discovery requires HTTP(S))
	if u, err := url.Parse(req.IssuerURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "issuer URL must use https:// or http://"},
		})
		return
	}
	// Block private/loopback/CGNAT IPs to prevent SSRF
	if err := k8s.ValidateRemoteURL(req.IssuerURL); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid issuer URL: " + err.Error()},
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

	// Validate scheme (LDAP connections only)
	if u, err := url.Parse(req.URL); err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "LDAP URL must use ldap:// or ldaps://"},
		})
		return
	}
	// Block private/loopback/CGNAT IPs to prevent SSRF
	if err := k8s.ValidateRemoteURL(req.URL); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid LDAP URL: " + err.Error()},
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

// validateSettingsURL checks that a URL is http/https and not pointing at private/loopback addresses.
// Returns an error message string, or empty string if valid.
func validateSettingsURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid URL: " + raw
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "URL must use http or https scheme"
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return "URL must not point to private/loopback addresses"
		}
	}
	return ""
}

// handleGetAppSettings returns the application settings with sensitive fields masked.
// Admin-only — RequireAdmin middleware enforces auth before this handler runs.
func (s *Server) handleGetAppSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.SettingsService.Get(r.Context())
	if err != nil {
		s.Logger.Error("failed to get settings", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to load settings"},
		})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: store.MaskedSettings(settings),
	})
}

// handleUpdateAppSettings updates application settings (partial patch).
// Admin-only (RequireAdmin middleware), audit logged.
func (s *Server) handleUpdateAppSettings(w http.ResponseWriter, r *http.Request) {
	// User is guaranteed by RequireAdmin middleware
	user, _ := auth.UserFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)
	var patch store.AppSettings
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	// Validate URLs to prevent SSRF
	for _, u := range []*string{patch.MonitoringPrometheusURL, patch.MonitoringGrafanaURL} {
		if u != nil {
			if msg := validateSettingsURL(*u); msg != "" {
				writeJSON(w, http.StatusBadRequest, api.Response{
					Error: &api.APIError{Code: 400, Message: msg},
				})
				return
			}
		}
	}

	if err := s.SettingsService.Update(r.Context(), patch); err != nil {
		s.Logger.Error("failed to update settings", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to update settings"},
		})
		return
	}

	entry := s.newAuditEntry(r, user.Username, audit.ActionUpdate, audit.ResultSuccess)
	entry.ResourceKind = "Settings"
	entry.Detail = "application settings updated"
	s.AuditLogger.Log(r.Context(), entry)

	// If GitHub token was updated, reconfigure the commit enrichment client
	if patch.GitHubToken != nil && s.GitOpsHandler != nil && s.GitOpsHandler.CommitCache != nil {
		if *patch.GitHubToken == "" {
			s.GitOpsHandler.CommitCache.SetGitHubClient(nil)
			s.Logger.Info("git commit enrichment disabled (token cleared)")
		} else {
			ghClient, err := gitprovider.NewGitHubClient(*patch.GitHubToken, "", s.Logger)
			if err != nil {
				s.Logger.Warn("failed to create github client after settings update", "error", err)
			} else {
				s.GitOpsHandler.CommitCache.SetGitHubClient(ghClient)
				s.Logger.Info("git commit enrichment reconfigured")
			}
		}
	}

	// Return updated (masked) settings
	settings, err := s.SettingsService.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, api.Response{Data: map[string]any{"updated": true}})
		return
	}
	writeJSON(w, http.StatusOK, api.Response{
		Data: store.MaskedSettings(settings),
	})
}
