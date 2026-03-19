package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
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
func (s *Server) newAuditEntry(r *http.Request, username string, action audit.Action, result audit.Result) audit.Entry {
	return audit.Entry{
		Timestamp: time.Now(),
		ClusterID: s.Config.ClusterID,
		User:      username,
		SourceIP:  r.RemoteAddr,
		Action:    action,
		Result:    result,
	}
}

// issueTokenPair creates a new access + refresh token pair, stores the session,
// and sets the refresh cookie. Returns the access token or an error.
func (s *Server) issueTokenPair(w http.ResponseWriter, user *auth.User) (string, error) {
	accessToken, err := s.TokenManager.IssueAccessToken(user)
	if err != nil {
		return "", err
	}

	refreshToken, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", err
	}

	session := auth.RefreshSession{
		Token:     refreshToken,
		UserID:    user.ID,
		Provider:  user.Provider,
		ExpiresAt: time.Now().Add(auth.RefreshTokenLifetime),
	}
	// Cache user data for non-local providers (OIDC has no local store,
	// LDAP would require reconnecting to the directory on refresh).
	// Local users are looked up by ID from the in-memory store instead.
	if user.Provider != "local" {
		session.CachedUser = user
	}
	s.Sessions.Store(session)

	s.setRefreshCookie(w, refreshToken, int(auth.RefreshTokenLifetime.Seconds()))

	return accessToken, nil
}
