package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// oidcMobileExchangeRequest is the JSON body POSTed by the mobile client.
type oidcMobileExchangeRequest struct {
	Code         string `json:"code"`
	State        string `json:"state"`
	CodeVerifier string `json:"codeVerifier"`
	Nonce        string `json:"nonce"`
}

// handleOIDCMobileExchange exchanges a client-side OIDC authorization code
// for a k8sCenter JWT pair.
//
// Mobile clients generate PKCE + nonce + state client-side per RFC 8252,
// open the IdP authorization URL in flutter_custom_tabs /
// SFSafariViewController, intercept the redirect via Universal Link / App
// Link, validate state client-side (it's the mobile CSRF token), and POST
// {code, state, codeVerifier, nonce} here. Response is body-mode — no
// cookies are set — because in-app browsers can't share their cookie jar
// with the embedded Dio client.
//
// State is NOT re-validated server-side; the mobile client is the only one
// that can validate it (the value was generated on-device). The nonce IS
// validated against the ID token's `nonce` claim — closing the ID-token-
// replay window that PKCE alone does not cover.
//
// CSRF: X-Requested-With: XMLHttpRequest is enforced inline since this
// route lives in the public /auth group (no CSRF middleware). The header
// is injected by the mobile interceptor on every request.
//
// Rate-limit: shares the login bucket (5/min/IP).
//
// Audit: logs audit.ActionLogin with detail "oidc/<providerID>/mobile".
func (s *Server) handleOIDCMobileExchange(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")

	// CSRF defense. Same shape as middleware.CSRF; inlined because public
	// auth routes don't run the middleware.
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		writeJSON(w, http.StatusForbidden, api.Response{
			Error: &api.APIError{Code: 403, Message: "missing CSRF header"},
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)
	var body oidcMobileExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	if body.Code == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "code required"},
		})
		return
	}
	if body.CodeVerifier == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "codeVerifier required"},
		})
		return
	}
	if body.Nonce == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "nonce required"},
		})
		return
	}

	provider, ok := s.AuthRegistry.GetOIDC(providerID)
	if !ok {
		writeJSON(w, http.StatusNotFound, api.Response{
			Error: &api.APIError{Code: 404, Message: "unknown OIDC provider"},
		})
		return
	}

	user, err := provider.ExchangeMobile(r.Context(), body.Code, body.CodeVerifier, body.Nonce)
	if err != nil {
		s.Logger.Error("OIDC mobile exchange failed",
			"error", err,
			"provider", providerID,
		)
		s.auditOIDCMobileFailure(r, providerID, err)
		s.writeOIDCMobileExchangeError(w, err)
		return
	}

	// Body-mode token issuance: no Set-Cookie header; refresh token is
	// echoed in the JSON below for the mobile client to persist into
	// flutter_secure_storage. OIDC sessions get the 1h refresh TTL cap
	// per auth.OIDCRefreshTokenLifetime.
	accessToken, refreshToken, err := s.issueTokenPair(w, user, false /* cookieMode */)
	if err != nil {
		s.Logger.Error("failed to issue tokens", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	entry := s.newAuditEntry(r, user.Username, audit.ActionLogin, audit.ResultSuccess)
	entry.Detail = "oidc/" + providerID + "/mobile"
	s.AuditLogger.Log(r.Context(), entry)

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"accessToken":      accessToken,
			"refreshToken":     refreshToken,
			"expiresIn":        int(auth.AccessTokenLifetime.Seconds()),
			"refreshExpiresIn": int(auth.OIDCRefreshTokenLifetime.Seconds()),
			"user": map[string]any{
				"username":    user.Username,
				"displayName": user.Username,
				"groups":      user.KubernetesGroups,
				"provider":    user.Provider,
			},
		},
	})
}

// auditOIDCMobileFailure logs a sanitized audit failure entry. The raw
// error is already in slog server logs; the audit detail is a stable,
// human-readable classification that does NOT include token, nonce, or
// verifier contents.
func (s *Server) auditOIDCMobileFailure(r *http.Request, providerID string, err error) {
	entry := s.newAuditEntry(r, "", audit.ActionLogin, audit.ResultFailure)
	entry.Detail = "oidc/" + providerID + "/mobile: " + classifyOIDCMobileError(err)
	s.AuditLogger.Log(r.Context(), entry)
}

// writeOIDCMobileExchangeError maps an ExchangeMobile error to an HTTP
// response. Specific failure modes get specific status codes; everything
// else falls through to 401.
func (s *Server) writeOIDCMobileExchangeError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "domain not allowed"):
		writeJSON(w, http.StatusForbidden, api.Response{
			Error: &api.APIError{Code: 403, Message: "email domain not allowed"},
		})
	case strings.Contains(msg, "nonce mismatch"):
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "oidc id token nonce mismatch"},
		})
	case strings.Contains(msg, "not verified by identity provider"):
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "email address not verified by identity provider"},
		})
	case strings.Contains(msg, "ID token verification failed"):
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "oidc id token verification failed"},
		})
	default:
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: "oidc exchange failed"},
		})
	}
}

// classifyOIDCMobileError returns a stable label suitable for an audit
// entry. The label is sanitized — no token, nonce, or verifier content.
func classifyOIDCMobileError(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "domain not allowed"):
		return "domain not allowed"
	case strings.Contains(msg, "nonce mismatch"):
		return "id token nonce mismatch"
	case strings.Contains(msg, "not verified by identity provider"):
		return "email not verified"
	case strings.Contains(msg, "ID token verification failed"):
		return "id token verification failed"
	case strings.Contains(msg, "no id_token"):
		return "no id_token in response"
	case strings.Contains(msg, "token exchange failed"):
		return "code exchange rejected"
	case strings.Contains(msg, "extracting claims"):
		return "claims extraction failed"
	case strings.Contains(msg, "map OIDC claims"):
		return "claims mapping failed"
	}
	// `errors.Is`-style typed sentinel could be added later; for now string
	// classification is good enough and avoids false-positive logging.
	if errors.Is(err, http.ErrAbortHandler) {
		return "transport aborted"
	}
	return "unspecified failure"
}
