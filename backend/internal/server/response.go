package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// dnsLabelRegex matches valid RFC 1123 DNS labels (used for namespace validation).
var dnsLabelRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)

// isValidDNSLabel checks whether s is a valid RFC 1123 DNS label.
func isValidDNSLabel(s string) bool {
	return dnsLabelRegex.MatchString(s)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// setRefreshCookie sets (or clears) the refresh token httpOnly cookie.
func (s *Server) setRefreshCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    value,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   !s.Config.Dev,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

// newAuditEntry creates an audit entry pre-filled with common fields.
//
// SourceIP is set from r.RemoteAddr (which chi's RealIP may have rewritten
// from X-Forwarded-For/X-Real-IP headers — useful for the logical client IP
// behind a trusted load-balancer).
//
// ConnectionIP is set from the socket-level peer captured by
// CaptureSocketPeer before RealIP ran. This is the ground-truth TCP peer
// and cannot be spoofed by header injection. It is omitted when empty
// (e.g., in tests that don't route through the full middleware stack).
// See Finding #1+#8, ce-code-review 2026-05-22.
func (s *Server) newAuditEntry(r *http.Request, username string, action audit.Action, result audit.Result) audit.Entry {
	return audit.Entry{
		Timestamp:    time.Now(),
		ClusterID:    s.Config.ClusterID,
		User:         username,
		SourceIP:     r.RemoteAddr,
		ConnectionIP: middleware.SocketPeerFromContext(r.Context()),
		Action:       action,
		Result:       result,
	}
}

// issueTokenPair creates a new access + refresh token pair, stores the
// session, and conditionally sets the refresh cookie. Returns the access
// token and the raw refresh token.
//
// cookieMode=true (web flow): sets the refresh_token httpOnly cookie.
// cookieMode=false (body-mode flow): no cookie is set; the caller MUST
// echo the refresh token in the JSON response body. This is a wire-format
// difference from the cookie-mode path — body-mode responses do NOT
// include a Set-Cookie header — and mobile clients cannot persist refresh
// tokens any other way because in-app browsers don't share their cookie
// jar with the embedded Dio client.
//
// OIDC-sourced sessions get a shorter refresh TTL ([auth.OIDCRefreshTokenLifetime]).
// The shorter window propagates IdP revocation (account disabled, group
// removed) within the hour rather than the standard 7-day window — see
// the constant's doc comment for the rationale.
func (s *Server) issueTokenPair(w http.ResponseWriter, user *auth.User, cookieMode bool) (string, string, error) {
	accessToken, err := s.TokenManager.IssueAccessToken(user)
	if err != nil {
		return "", "", err
	}

	refreshToken, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", "", err
	}

	refreshLifetime := auth.RefreshLifetimeFor(user.Provider)

	session := auth.RefreshSession{
		Token:     refreshToken,
		UserID:    user.ID,
		Provider:  user.Provider,
		ExpiresAt: time.Now().Add(refreshLifetime),
	}
	// Cache user data for non-local providers (OIDC has no local store,
	// LDAP would require reconnecting to the directory on refresh).
	// Local users are looked up by ID from the in-memory store instead.
	if user.Provider != "local" {
		session.CachedUser = user
	}
	s.Sessions.Store(session)

	if cookieMode {
		s.setRefreshCookie(w, refreshToken, int(refreshLifetime.Seconds()))
	}

	return accessToken, refreshToken, nil
}
