package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const (
	maxAuthBodySize = 1 << 16 // 64 KB — more than enough for auth payloads
	maxPasswordLen  = 128
	maxUsernameLen  = 253 // k8s username limit
)

var validUsername = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]*$`)

// handleSetupStatus returns whether the instance needs initial setup.
// Public endpoint — only returns a boolean, never leaks user count or settings state.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.LocalAuth.Store().Count(r.Context())
	if err != nil {
		s.Logger.Error("failed to count users for setup status", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "internal error"},
		})
		return
	}
	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"needsSetup": count == 0,
		},
	})
}

// handleSetupInit creates the first admin user. Returns 410 Gone if any user exists.
func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)

	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		SetupToken string `json:"setupToken,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid request body"},
		})
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "username and password are required"},
		})
		return
	}

	if len(req.Username) > maxUsernameLen || !validUsername.MatchString(req.Username) {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "invalid username format"},
		})
		return
	}

	if len(req.Password) < 8 || len(req.Password) > maxPasswordLen {
		writeJSON(w, http.StatusBadRequest, api.Response{
			Error: &api.APIError{Code: 400, Message: "password must be 8-128 characters"},
		})
		return
	}

	// Per-account throttle (P2-1 part 2). Setup is one-shot — only the
	// first call succeeds, every subsequent call returns 410 — but
	// audit-listed setup-probing benefits from a per-username bucket
	// so an attacker iterating likely admin names ("admin", "root",
	// "administrator") is throttled independently of the global IP
	// bucket.
	if s.accountThrottle(w, r, "setup", req.Username, audit.ActionSetup) {
		return
	}

	// P1-1: Setup token gate.
	//
	// If SetupToken is configured, a constant-time comparison is required
	// (existing behaviour, unchanged).
	//
	// If SetupToken is NOT configured, the request is only allowed when
	// BOTH of the following are true:
	//   1. The request originates from a loopback address (127.x.x.x or ::1).
	//   2. Config.Dev is true.
	//
	// All other cases — including non-loopback peers in dev mode, or loopback
	// peers in production — are rejected with 503 so that a freshly deployed
	// instance without a setup token cannot be taken over from the network.
	if s.Config.Auth.SetupToken == "" {
		// P1-1: loopback + dev gate.
		//
		// Use the socket-level peer address captured by CaptureSocketPeer
		// BEFORE chi's RealIP overwrote r.RemoteAddr. This prevents an
		// attacker from spoofing loopback via "X-Forwarded-For: 127.0.0.1"
		// from a non-loopback connection (Finding #1+#8, ce-code-review
		// 2026-05-22).
		//
		// Fall back to r.RemoteAddr when the middleware was not in the
		// chain (e.g., in tests that construct bare requests without routing
		// through the full stack), but log a warning so misconfigured order
		// is visible in production logs.
		rawPeer := middleware.SocketPeerFromContext(r.Context())
		if rawPeer == "" {
			s.Logger.Warn("SocketPeerFromContext returned empty — CaptureSocketPeer may be missing from the middleware chain; falling back to r.RemoteAddr (finding P1-1 defense degraded)",
				slog.String("remoteAddr", r.RemoteAddr),
			)
			rawPeer = r.RemoteAddr
		}

		host, _, err := net.SplitHostPort(rawPeer)
		if err != nil {
			// Fallback: treat the raw string as the IP (covers unit-test
			// scenarios where RemoteAddr may not include a port).
			host = rawPeer
		}
		peerIP := net.ParseIP(host)
		isLoopback := peerIP != nil && peerIP.IsLoopback()

		if !isLoopback || !s.Config.Dev {
			s.Logger.Warn("setup init rejected: setup token required for non-loopback setup (finding P1-1)",
				"socketPeer", rawPeer,
				"dev", s.Config.Dev,
				"isLoopback", isLoopback,
			)
			s.AuditLogger.Log(r.Context(), s.newAuditEntry(r, "", audit.ActionSetup, audit.ResultFailure))
			writeJSON(w, http.StatusForbidden, api.Response{
				Error: &api.APIError{
					Code:    403,
					Message: "setup token required; configure KUBECENTER_AUTH_SETUPTOKEN before remote setup",
				},
			})
			return
		}
	} else {
		// Verify setup token if configured — constant-time comparison.
		if subtle.ConstantTimeCompare([]byte(req.SetupToken), []byte(s.Config.Auth.SetupToken)) != 1 {
			s.Logger.Warn("setup init rejected: invalid setup token", "remoteAddr", r.RemoteAddr)
			// Audit the rejected setup attempt: an attacker probing the
			// setup-token endpoint otherwise leaves no trace until the
			// IP rate-limit middleware kicks in. The sibling no-token
			// branch already audits — keeping parity here closes the
			// gap (Phase 3 review, audit P2-1 cluster).
			entry := s.newAuditEntry(r, req.Username, audit.ActionSetup, audit.ResultFailure)
			entry.Detail = "invalid setup token"
			s.AuditLogger.Log(r.Context(), entry)
			writeJSON(w, http.StatusForbidden, api.Response{
				Error: &api.APIError{Code: 403, Message: "invalid setup token"},
			})
			return
		}
	}

	// Atomic: checks no users exist AND creates under the same lock.
	user, err := s.LocalAuth.CreateFirstUser(r.Context(), req.Username, req.Password, []string{"admin"})
	if err != nil {
		if errors.Is(err, auth.ErrSetupCompleted) {
			writeJSON(w, http.StatusGone, api.Response{
				Error: &api.APIError{Code: 410, Message: "setup already completed"},
			})
			return
		}
		s.Logger.Error("failed to create admin user", "error", err)
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to create admin user"},
		})
		return
	}

	entry := s.newAuditEntry(r, user.Username, audit.ActionSetup, audit.ResultSuccess)
	entry.Detail = "initial admin account created"
	s.AuditLogger.Log(r.Context(), entry)

	writeJSON(w, http.StatusCreated, api.Response{
		Data: map[string]any{
			"username": user.Username,
			"created":  true,
		},
	})
}
