package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// oidcMobileExchangeRequest is the JSON body POSTed by the mobile client.
//
// State is generated and validated client-side per RFC 8252; the server
// does not re-validate it and silently ignores it if present in the
// request JSON (json.Decoder discards unknown fields). It is intentionally
// omitted from the struct to make the contract explicit at the type level
// rather than relying on a documented-but-unused field.
type oidcMobileExchangeRequest struct {
	Code         string `json:"code"`
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
// {code, codeVerifier, nonce} here. Response is body-mode — no cookies are
// set — because in-app browsers can't share their cookie jar with the
// embedded Dio client.
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
		// Audit unknown-provider attempts too — they're a low-cost signal
		// for misconfigured clients or scanning probes hitting the auth
		// surface. Detail string follows the same shape as the rest of the
		// mobile-exchange audit entries.
		s.auditOIDCMobileFailure(r, providerID, fmt.Errorf("unknown OIDC provider"))
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
		// Audit the post-exchange failure so operators can correlate a
		// successful IdP token exchange that nonetheless produced no
		// k8sCenter session (e.g. JWT signing failure, session store
		// pressure). Without this, the prior success-only audit hid the
		// 500.
		entry := s.newAuditEntry(r, user.Username, audit.ActionLogin, audit.ResultFailure)
		entry.Detail = "oidc/" + providerID + "/mobile: token issuance failed"
		s.AuditLogger.Log(r.Context(), entry)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to issue token"},
		})
		return
	}

	entry := s.newAuditEntry(r, user.Username, audit.ActionLogin, audit.ResultSuccess)
	entry.Detail = "oidc/" + providerID + "/mobile"
	s.AuditLogger.Log(r.Context(), entry)

	// Mobile clients should call /v1/auth/me post-exchange to hydrate the
	// canonical user model (roles, kubernetesGroups, namespace permissions).
	// This summary payload is for the initial login-screen render only and
	// intentionally omits fields a 5KB-JSON login round-trip doesn't need.
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
	cat := categorizeOIDCMobileError(err)
	entry := s.newAuditEntry(r, "", audit.ActionLogin, audit.ResultFailure)
	entry.Detail = "oidc/" + providerID + "/mobile: " + cat.auditLabel
	s.AuditLogger.Log(r.Context(), entry)
}

// writeOIDCMobileExchangeError maps an ExchangeMobile error to an HTTP
// response. Specific failure modes get specific status codes; everything
// else falls through to 401. Status + response message come from the
// shared categorizer so the audit label and HTTP envelope cannot drift.
func (s *Server) writeOIDCMobileExchangeError(w http.ResponseWriter, err error) {
	cat := categorizeOIDCMobileError(err)
	writeJSON(w, cat.httpStatus, api.Response{
		Error: &api.APIError{Code: cat.httpStatus, Message: cat.responseMessage},
	})
}

// oidcMobileErrorCategory bundles the three derived facts about an
// ExchangeMobile error: the sanitised audit label, the HTTP status the
// client should see, and the response message body. Keeping them in one
// struct (and one switch) prevents the audit-label / HTTP-status drift
// that the previous two-function arrangement allowed.
type oidcMobileErrorCategory struct {
	auditLabel      string
	httpStatus      int
	responseMessage string
}

// categorizeOIDCMobileError classifies an ExchangeMobile error into the
// shared audit + HTTP shape. Matching is by canonical substring; nonce
// mismatch matches against the shared [auth.ErrOIDCNonceMismatch]
// constant rather than the bare "nonce mismatch" fragment.
func categorizeOIDCMobileError(err error) oidcMobileErrorCategory {
	if err == nil {
		return oidcMobileErrorCategory{
			auditLabel:      "unknown",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "oidc exchange failed",
		}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "domain not allowed"):
		return oidcMobileErrorCategory{
			auditLabel:      "domain not allowed",
			httpStatus:      http.StatusForbidden,
			responseMessage: "email domain not allowed",
		}
	case strings.Contains(msg, auth.ErrOIDCNonceMismatch):
		return oidcMobileErrorCategory{
			auditLabel:      "id token nonce mismatch",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "oidc id token nonce mismatch",
		}
	case strings.Contains(msg, "not verified by identity provider"):
		return oidcMobileErrorCategory{
			auditLabel:      "email not verified",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "email address not verified by identity provider",
		}
	case strings.Contains(msg, "ID token verification failed"):
		return oidcMobileErrorCategory{
			auditLabel:      "id token verification failed",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "oidc id token verification failed",
		}
	case strings.Contains(msg, "no id_token"):
		return oidcMobileErrorCategory{
			auditLabel:      "no id_token in response",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "oidc exchange failed",
		}
	case strings.Contains(msg, auth.ErrOIDCExchangeTimeout):
		return oidcMobileErrorCategory{
			auditLabel:      "code exchange timeout",
			httpStatus:      http.StatusServiceUnavailable,
			responseMessage: "oidc exchange timeout",
		}
	case strings.Contains(msg, "token exchange failed"):
		return oidcMobileErrorCategory{
			auditLabel:      "code exchange rejected",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "oidc exchange failed",
		}
	case strings.Contains(msg, "extracting claims"):
		return oidcMobileErrorCategory{
			auditLabel:      "claims extraction failed",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "oidc exchange failed",
		}
	case strings.Contains(msg, "map OIDC claims"):
		return oidcMobileErrorCategory{
			auditLabel:      "claims mapping failed",
			httpStatus:      http.StatusUnauthorized,
			responseMessage: "oidc exchange failed",
		}
	case strings.Contains(msg, "unknown OIDC provider"):
		return oidcMobileErrorCategory{
			auditLabel:      "unknown provider",
			httpStatus:      http.StatusNotFound,
			responseMessage: "unknown OIDC provider",
		}
	}
	return oidcMobileErrorCategory{
		auditLabel:      "unspecified failure",
		httpStatus:      http.StatusUnauthorized,
		responseMessage: "oidc exchange failed",
	}
}
