package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/auth"
)

// Shared WebSocket constants used by log and flow stream handlers.
const (
	wsWriteWait   = 10 * time.Second
	wsPongWait    = 60 * time.Second
	wsPingPeriod  = (wsPongWait * 9) / 10 // 54s
	wsMaxReadSize = 4096                  // only expect small auth/filter messages
)

// wsAuthAndUpgrade handles WebSocket upgrade, origin validation, and JWT authentication.
// Returns the authenticated connection and user, or nil if the request was rejected
// (an error response has already been sent). The caller is responsible for closing
// the connection.
func (s *Server) wsAuthAndUpgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, *auth.User) {
	if !s.validateWSOrigin(w, r) {
		return nil, nil
	}

	up := upgrader
	up.CheckOrigin = func(r *http.Request) bool { return true }
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		s.Logger.Error("ws upgrade failed", "error", err)
		return nil, nil
	}

	conn.SetReadLimit(wsMaxReadSize)

	// Read auth message (JWT token)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var authMsg struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := conn.ReadJSON(&authMsg); err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "auth required"})
		conn.Close()
		return nil, nil
	}
	if authMsg.Type != "auth" || authMsg.Token == "" {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid auth message"})
		conn.Close()
		return nil, nil
	}

	claims, err := s.TokenManager.ValidateAccessToken(authMsg.Token)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid token"})
		conn.Close()
		return nil, nil
	}
	u := auth.UserFromClaims(claims)

	return conn, u
}

// wsStartKeepalive sets up ping/pong handlers and starts a background read pump
// that cancels the provided context when the connection closes or errors.
// Returns a ping ticker that the caller should use in their write loop and defer Stop().
func wsStartKeepalive(conn *websocket.Conn, cancel context.CancelFunc) *time.Ticker {
	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	// Read pump: detect WS close/errors (runs in background)
	go func() {
		defer cancel()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	return time.NewTicker(wsPingPeriod)
}
