package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
)

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

	s.Sessions.Store(auth.RefreshSession{
		Token:     refreshToken,
		UserID:    user.ID,
		Provider:  user.Provider,
		ExpiresAt: time.Now().Add(auth.RefreshTokenLifetime),
	})

	s.setRefreshCookie(w, refreshToken, int(auth.RefreshTokenLifetime.Seconds()))

	return accessToken, nil
}
