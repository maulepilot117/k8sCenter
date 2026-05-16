package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// handleOIDCMobileConfig returns the OIDC provider config a mobile client
// needs to construct its authorization URL with client-generated PKCE +
// state + nonce.
//
// Public route — same rate-limit bucket as the rest of the auth surface.
// The returned fields (authorization endpoint, client ID, scopes) are
// already discoverable via the IdP's `/.well-known/openid-configuration`
// plus the operator's registered client id, so exposing them to
// unauthenticated callers carries no new information leak. The
// `clientSecret` is NEVER returned; it stays server-side and is used only
// during the body-mode token exchange in [handleOIDCMobileExchange].
//
// The redirect URI is constructed mobile-side from the build-time
// `UNIVERSAL_LINK_HOST` dart-define; the backend does not (and should
// not) need to know which mobile host the operator has wired.
func (s *Server) handleOIDCMobileConfig(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")

	provider, ok := s.AuthRegistry.GetOIDC(providerID)
	if !ok {
		writeJSON(w, http.StatusNotFound, api.Response{
			Error: &api.APIError{Code: 404, Message: "unknown OIDC provider"},
		})
		return
	}

	writeJSON(w, http.StatusOK, api.Response{Data: provider.MobileAuthConfig()})
}
