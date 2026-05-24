package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// testServer creates a fully wired Server for handler integration tests.
// It skips k8s-dependent features (informers, RBAC checker) by not setting them.
func testServer(t *testing.T) *Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Dev:       true,
		ClusterID: "test-cluster",
		Server: config.ServerConfig{
			Port:            8080,
			RequestTimeout:  config.DefaultRequestTimeout,
			ShutdownTimeout: config.DefaultShutdownTimeout,
		},
		Log: config.LogConfig{Level: "error", Format: "json"},
	}

	tokenManager := auth.NewTokenManager([]byte("test-signing-key-minimum-32-bytes"))
	localAuth := auth.NewLocalProvider(auth.NewMemoryUserStore(), logger)
	sessions := auth.NewSessionStore()
	auditLogger := audit.NewSlogLogger(logger)
	rateLimiter := middleware.NewRateLimiter()

	// Create auth registry with local provider
	authRegistry := auth.NewProviderRegistry()
	authRegistry.RegisterCredential("local", "Local Accounts", localAuth)

	return New(Deps{
		Config:         cfg,
		Logger:         logger,
		TokenManager:   tokenManager,
		LocalAuth:      localAuth,
		AuthRegistry:   authRegistry,
		OIDCStateStore: auth.NewOIDCStateStore(),
		Sessions:       sessions,
		AuditLogger:    auditLogger,
		RateLimiter:    rateLimiter,
		ReadyFn:        func() bool { return true },
	})
}

// loginAdmin creates an admin user and logs in, returning the access token and cookie jar.
func loginAdmin(t *testing.T, srv *Server) (token string, cookies []*http.Cookie) {
	t.Helper()

	// Create admin
	_, err := srv.LocalAuth.CreateUser(context.Background(), "admin", "password1234", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Login
	body := `{"username":"admin","password":"password1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			AccessToken string `json:"accessToken"`
			ExpiresIn   int    `json:"expiresIn"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	return resp.Data.AccessToken, w.Result().Cookies()
}

// --- Setup Tests ---

func TestHandleSetupInit(t *testing.T) {
	srv := testServer(t)
	srv.Config.Auth.SetupToken = "my-token"

	body := `{"username":"admin","password":"password1234","setupToken":"my-token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Username string `json:"username"`
			Created  bool   `json:"created"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.Username != "admin" {
		t.Errorf("expected username admin, got %s", resp.Data.Username)
	}
	if !resp.Data.Created {
		t.Errorf("expected created=true")
	}
}

func TestHandleSetupInit_AlreadyDone(t *testing.T) {
	srv := testServer(t)

	// Create a user first
	srv.LocalAuth.CreateUser(context.Background(), "existing", "password1234", []string{"admin"}, nil)

	body := `{"username":"admin","password":"password1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// P1-1: no setup token configured; Dev=true; must use loopback addr to pass the gate.
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSetupInit_WrongToken(t *testing.T) {
	srv := testServer(t)
	srv.Config.Auth.SetupToken = "correct-token"

	body := `{"username":"admin","password":"password1234","setupToken":"wrong-token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSetupInit_Validation(t *testing.T) {
	srv := testServer(t)

	tests := []struct {
		name string
		body string
		code int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"missing password", `{"username":"admin"}`, http.StatusBadRequest},
		{"missing username", `{"password":"password1234"}`, http.StatusBadRequest},
		{"short password", `{"username":"admin","password":"short"}`, http.StatusBadRequest},
		{"invalid username", `{"username":"!invalid","password":"password1234"}`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.Router.ServeHTTP(w, req)
			if w.Code != tt.code {
				t.Errorf("expected %d, got %d: %s", tt.code, w.Code, w.Body.String())
			}
		})
	}
}

// --- Login Tests ---

func TestHandleLogin(t *testing.T) {
	srv := testServer(t)
	srv.LocalAuth.CreateUser(context.Background(), "admin", "password1234", []string{"admin"}, nil)

	body := `{"username":"admin","password":"password1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			AccessToken string `json:"accessToken"`
			ExpiresIn   int    `json:"expiresIn"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Data.AccessToken == "" {
		t.Error("expected access token in response")
	}
	if resp.Data.ExpiresIn != 900 {
		t.Errorf("expected expiresIn=900, got %d", resp.Data.ExpiresIn)
	}

	// Check refresh cookie was set
	cookies := w.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			found = true
			if !c.HttpOnly {
				t.Error("refresh_token cookie should be HttpOnly")
			}
			if c.SameSite != http.SameSiteStrictMode {
				t.Error("refresh_token cookie should have SameSite=Strict")
			}
		}
	}
	if !found {
		t.Error("expected refresh_token cookie to be set")
	}
}

func TestHandleLogin_WrongPassword(t *testing.T) {
	srv := testServer(t)
	srv.LocalAuth.CreateUser(context.Background(), "admin", "password1234", []string{"admin"}, nil)

	body := `{"username":"admin","password":"wrongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleLogin_UnknownUser(t *testing.T) {
	srv := testServer(t)

	body := `{"username":"nobody","password":"password1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// --- Refresh Tests ---

func TestHandleRefresh(t *testing.T) {
	srv := testServer(t)
	_, cookies := loginAdmin(t, srv)

	// Use the refresh cookie
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.AccessToken == "" {
		t.Error("expected new access token")
	}
}

func TestHandleRefresh_RotationInvalidatesOldToken(t *testing.T) {
	srv := testServer(t)
	_, cookies := loginAdmin(t, srv)

	// First refresh succeeds
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first refresh failed: %d", w.Code)
	}

	// Second refresh with same cookie fails (rotation)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req2.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for reused refresh token, got %d", w2.Code)
	}
}

func TestHandleRefresh_NoCookie(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestHandleRefresh_BodyMode covers the mobile flow: no cookie present, the
// raw refresh token arrives in the JSON body, and the response echoes the
// rotated refresh token back so the mobile client can persist it.
func TestHandleRefresh_BodyMode(t *testing.T) {
	srv := testServer(t)
	_, cookies := loginAdmin(t, srv)

	var refreshToken string
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			refreshToken = c.Value
		}
	}
	if refreshToken == "" {
		t.Fatalf("login did not return refresh_token cookie")
	}

	body := `{"refreshToken":"` + refreshToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.AccessToken == "" {
		t.Error("expected new access token")
	}
	if resp.Data.RefreshToken == "" {
		t.Error("expected rotated refresh token in body-mode response")
	}
	if resp.Data.RefreshToken == refreshToken {
		t.Error("rotated refresh token should differ from original")
	}
}

// TestHandleRefresh_BodyMode_NoSetCookie confirms the body-mode refresh
// path does NOT echo a `refresh_token` cookie back to mobile callers (PR-5b
// wire-format guarantee). The rotated refresh token must arrive in the JSON
// body only — web cookie semantics would be a no-op for mobile clients and
// surface as a confusing duplicate to operators reading raw HTTP traces.
func TestHandleRefresh_BodyMode_NoSetCookie(t *testing.T) {
	srv := testServer(t)
	_, cookies := loginAdmin(t, srv)

	var refreshToken string
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			refreshToken = c.Value
		}
	}
	if refreshToken == "" {
		t.Fatalf("login did not return refresh_token cookie")
	}

	body := `{"refreshToken":"` + refreshToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" && c.Value != "" {
			t.Errorf("body-mode refresh must not set a refresh_token cookie; got %+v", c)
		}
	}
}

// TestIssueTokenPair_OIDCRefreshLifetime asserts the session row stored by
// issueTokenPair carries the provider-aware refresh TTL: OIDC sessions get
// the 1h cap, local sessions get the 7d default. Drift here silently
// extends the IdP revocation window — the change that motivated PR-5b — so
// the test reads the stored ExpiresAt directly instead of inferring it
// from cookie Max-Age (which is absent in body-mode).
func TestIssueTokenPair_OIDCRefreshLifetime(t *testing.T) {
	cases := []struct {
		name     string
		user     *auth.User
		wantTTL  time.Duration
		tolerance time.Duration
	}{
		{
			name:      "oidc user gets 1h cap",
			user:      &auth.User{ID: "oidc:authelia:sub-1", Username: "u@example.com", Provider: "oidc"},
			wantTTL:   auth.OIDCRefreshTokenLifetime,
			tolerance: 5 * time.Second,
		},
		{
			name:      "local user keeps 7d default",
			user:      &auth.User{ID: "local-1", Username: "admin", Provider: "local"},
			wantTTL:   auth.RefreshTokenLifetime,
			tolerance: 5 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer(t)

			before := time.Now()
			w := httptest.NewRecorder()
			_, refreshToken, err := srv.issueTokenPair(w, tc.user, false /* cookieMode */)
			if err != nil {
				t.Fatalf("issueTokenPair: %v", err)
			}
			if refreshToken == "" {
				t.Fatal("expected non-empty refresh token")
			}

			// Locate the stored session by its token without consuming it
			// (Rotate is single-use and we still want to assert against the
			// stored ExpiresAt). RangeSessions walks the sync.Map without
			// mutation, which is exactly the test affordance we need.
			var found bool
			var got time.Time
			srv.Sessions.RangeSessions(func(s auth.RefreshSession) bool {
				if s.Token == refreshToken {
					found = true
					got = s.ExpiresAt
					return false
				}
				return true
			})
			if !found {
				t.Fatalf("issued refresh token not in SessionStore")
			}

			wantMin := before.Add(tc.wantTTL).Add(-tc.tolerance)
			wantMax := time.Now().Add(tc.wantTTL).Add(tc.tolerance)
			if got.Before(wantMin) || got.After(wantMax) {
				t.Errorf("session ExpiresAt = %v, want within [%v, %v] (TTL %v)",
					got, wantMin, wantMax, tc.wantTTL)
			}
		})
	}
}

// TestHandleRefresh_BodyMode_BadToken verifies an unknown body-mode token
// returns 401 the same way an unknown cookie does.
func TestHandleRefresh_BodyMode_BadToken(t *testing.T) {
	srv := testServer(t)

	body := `{"refreshToken":"definitely-not-a-real-token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestHandleRefresh_CookieResponseHasNoRefreshToken confirms web behaviour
// is unchanged: cookie-mode refresh responses do not echo the refresh token
// in the JSON body.
func TestHandleRefresh_CookieResponseHasNoRefreshToken(t *testing.T) {
	srv := testServer(t)
	_, cookies := loginAdmin(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp.Data["refreshToken"]; ok {
		t.Error("cookie-mode refresh must not echo refreshToken in body")
	}
}

// --- Logout Tests ---

func TestHandleLogout(t *testing.T) {
	srv := testServer(t)
	token, cookies := loginAdmin(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Refresh cookie should be cleared (MaxAge < 0)
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" && c.MaxAge >= 0 {
			t.Error("expected refresh_token cookie to be cleared (MaxAge < 0)")
		}
	}

	// Refresh with old cookie should fail
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req2.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", w2.Code)
	}
}

// --- Auth Providers ---

func TestHandleAuthProviders(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp api.Response
	json.NewDecoder(w.Body).Decode(&resp)

	providers, ok := resp.Data.([]any)
	if !ok || len(providers) == 0 {
		t.Fatal("expected at least one provider")
	}
}

// --- Health Tests ---

func TestHandleHealthz(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleReadyz(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleReadyz_NotReady(t *testing.T) {
	// Build a server that starts not-ready
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Config{
		Dev:       true,
		ClusterID: "test",
		Server: config.ServerConfig{
			Port:            8080,
			RequestTimeout:  config.DefaultRequestTimeout,
			ShutdownTimeout: config.DefaultShutdownTimeout,
		},
		Log: config.LogConfig{Level: "error", Format: "json"},
	}

	srv := New(Deps{
		Config:       cfg,
		Logger:       logger,
		TokenManager: auth.NewTokenManager([]byte("test-signing-key-minimum-32-bytes")),
		LocalAuth:    auth.NewLocalProvider(auth.NewMemoryUserStore(), logger),
		Sessions:     auth.NewSessionStore(),
		AuditLogger:  audit.NewSlogLogger(logger),
		RateLimiter:  middleware.NewRateLimiter(),
		ReadyFn:      func() bool { return false },
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- Auth Middleware Integration ---

func TestAuthenticatedEndpoint_RequiresAuth(t *testing.T) {
	srv := testServer(t)

	// /auth/me requires auth
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthenticatedEndpoint_InvalidToken(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()

	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// --- Full Flow Integration ---

func TestFullAuthFlow(t *testing.T) {
	srv := testServer(t)
	// P1-1: supply a setup token so this integration test works from
	// any remote addr (doRequest uses the default non-loopback httptest addr).
	srv.Config.Auth.SetupToken = "flow-test-setup-token"

	// 1. Setup
	setup := doRequest(t, srv, http.MethodPost, "/api/v1/setup/init",
		`{"username":"admin","password":"password1234","setupToken":"flow-test-setup-token"}`, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup: expected 201, got %d: %s", setup.Code, setup.Body.String())
	}

	// 2. Login
	login := doRequest(t, srv, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"password1234"}`,
		map[string]string{"X-Requested-With": "XMLHttpRequest"})
	if login.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d: %s", login.Code, login.Body.String())
	}

	var loginResp struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	json.NewDecoder(login.Body).Decode(&loginResp)
	token := loginResp.Data.AccessToken

	// 3. Access protected endpoint
	providers := doRequest(t, srv, http.MethodGet, "/api/v1/auth/providers", "", nil)
	if providers.Code != http.StatusOK {
		t.Fatalf("providers: expected 200, got %d", providers.Code)
	}

	// 4. Refresh
	refreshCookies := login.Result().Cookies()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range refreshCookies {
		req.AddCookie(c)
	}
	refresh := httptest.NewRecorder()
	srv.Router.ServeHTTP(refresh, req)
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d: %s", refresh.Code, refresh.Body.String())
	}

	// 5. Logout
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+token)
	logoutReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, c := range refresh.Result().Cookies() {
		logoutReq.AddCookie(c)
	}
	logout := httptest.NewRecorder()
	srv.Router.ServeHTTP(logout, logoutReq)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout: expected 200, got %d", logout.Code)
	}

	// 6. Setup should now return 410 (user already exists)
	setup2 := doRequest(t, srv, http.MethodPost, "/api/v1/setup/init",
		`{"username":"admin2","password":"password1234","setupToken":"flow-test-setup-token"}`, nil)
	if setup2.Code != http.StatusGone {
		t.Fatalf("setup2: expected 410, got %d: %s", setup2.Code, setup2.Body.String())
	}
}

// --- Helpers ---

func doRequest(t *testing.T, srv *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------
// Audit finding P2-1 part 2 (2026-05-22) — per-account login throttle.
// ---------------------------------------------------------------------

// TestHandleLogin_AccountThrottle_BlocksBruteForce pins the per-account
// rate-limit: after the configured number of attempts against a single
// username (even from rotating IPs), the next attempt against the same
// username returns 429 regardless of IP. The previous IP-only throttle
// could be bypassed by an attacker rotating source IPs.
func TestHandleLogin_AccountThrottle_BlocksBruteForce(t *testing.T) {
	srv := testServer(t)
	srv.RateLimiter = middleware.NewRateLimiterWithRate(2, time.Minute)

	for i := 0; i < 2; i++ {
		w := postLogin(t, srv, map[string]string{
			"username": "alice",
			"password": "wrong-password",
		}, "192.0.2."+strconv.Itoa(10+i)+":54321")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 invalid-creds, got %d: %s", i+1, w.Code, w.Body.String())
		}
	}

	// Third attempt against the same username from yet another IP must
	// hit the account throttle even though no single IP exhausted its
	// per-IP bucket.
	w := postLogin(t, srv, map[string]string{
		"username": "alice",
		"password": "wrong-password",
	}, "192.0.2.99:54321")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 account-throttled, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header missing on 429")
	}
}

// TestHandleLogin_AccountThrottle_CaseFolded asserts "Alice"/"alice"/
// "ALICE" share one bucket. Without case-folding an attacker could
// multiply the per-account budget by varying letter case.
func TestHandleLogin_AccountThrottle_CaseFolded(t *testing.T) {
	srv := testServer(t)
	srv.RateLimiter = middleware.NewRateLimiterWithRate(2, time.Minute)

	for _, casing := range []string{"alice", "Alice"} {
		w := postLogin(t, srv, map[string]string{
			"username": casing,
			"password": "wrong",
		}, "192.0.2.10:54321")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", casing, w.Code)
		}
	}

	// Third attempt with another casing must still be throttled.
	w := postLogin(t, srv, map[string]string{
		"username": "ALICE",
		"password": "wrong",
	}, "192.0.2.11:54321")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("case-folded throttle missed: got %d, want 429", w.Code)
	}
}

// TestHandleLogin_AccountThrottle_IndependentUsernames pins isolation
// between account buckets: throttling "alice" must not affect "bob".
func TestHandleLogin_AccountThrottle_IndependentUsernames(t *testing.T) {
	srv := testServer(t)
	srv.RateLimiter = middleware.NewRateLimiterWithRate(2, time.Minute)

	// Exhaust alice's bucket.
	for i := 0; i < 3; i++ {
		postLogin(t, srv, map[string]string{
			"username": "alice",
			"password": "wrong",
		}, "192.0.2.10:54321")
	}

	// bob must still be allowed past the failure-of-creds path —
	// expecting 401 invalid-creds, NOT 429.
	w := postLogin(t, srv, map[string]string{
		"username": "bob",
		"password": "wrong",
	}, "192.0.2.10:54321")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bob should hit 401 invalid-creds, not 429; got %d: %s", w.Code, w.Body.String())
	}
}

// postLogin issues a JSON login POST against the server with the
// supplied remote address, returning the recorder. Helper for the
// account-throttle tests above.
func postLogin(t *testing.T, srv *Server, body map[string]string, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------
// Audit finding P2-3 (2026-05-22) — LDAP refresh-path revalidation
// tests. The full ldapProvider.Revalidate path requires a live directory
// (homelab soak / Phase 3 follow-up integration test); these unit tests
// pin the no-LDAP-needed failure branches: parsing the UserID, looking
// up the provider in the registry, and asserting the provider type.
// ---------------------------------------------------------------------

func TestHandleRefresh_LDAP_MalformedUserID(t *testing.T) {
	cases := []struct {
		name   string
		userID string
	}{
		{"empty", ""},
		{"missing prefix", "google:google-id:user-dn"},
		{"only prefix", "ldap:"},
		{"empty provider id", "ldap::CN=alice,OU=Users"},
		{"empty DN", "ldap:ldap-corp:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer(t)

			token := "ldap-test-token-" + tc.name
			srv.Sessions.Store(auth.RefreshSession{
				Token:     token,
				UserID:    tc.userID,
				Provider:  "ldap",
				ExpiresAt: time.Now().Add(time.Hour),
				CachedUser: &auth.User{
					ID:       tc.userID,
					Username: "alice",
					Provider: "ldap",
				},
			})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			req.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
			w := httptest.NewRecorder()
			srv.Router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 for malformed UserID %q, got %d: %s", tc.userID, w.Code, w.Body.String())
			}
			var resp api.Response
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Error == nil || !strings.Contains(strings.ToLower(resp.Error.Message), "session invalid") {
				t.Fatalf("expected 'session invalid' error, got %+v", resp.Error)
			}
		})
	}
}

func TestHandleRefresh_LDAP_ProviderUnregistered(t *testing.T) {
	srv := testServer(t)
	// Note: testServer only registers the "local" provider. A session
	// claiming "ldap-corp" will fail the registry lookup and produce
	// the "auth provider unavailable" error path.

	token := "ldap-orphan-token"
	srv.Sessions.Store(auth.RefreshSession{
		Token:     token,
		UserID:    "ldap:ldap-corp:CN=alice,OU=Users,DC=example,DC=com",
		Provider:  "ldap",
		ExpiresAt: time.Now().Add(time.Hour),
		CachedUser: &auth.User{
			ID:       "ldap:ldap-corp:CN=alice,OU=Users,DC=example,DC=com",
			Username: "alice",
			Provider: "ldap",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unregistered LDAP provider, got %d: %s", w.Code, w.Body.String())
	}
	var resp api.Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || !strings.Contains(strings.ToLower(resp.Error.Message), "auth provider unavailable") {
		t.Fatalf("expected 'auth provider unavailable' error, got %+v", resp.Error)
	}

	// The original session was Rotate-removed and NOT Restored
	// (definitive failure path), so retry with the same token should
	// 401 with the "invalid or expired refresh token" path, not the
	// "auth provider unavailable" path.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req2.Header.Set("X-Requested-With", "XMLHttpRequest")
	req2.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
	w2 := httptest.NewRecorder()
	srv.Router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("retry expected 401, got %d", w2.Code)
	}
}

// ---------------------------------------------------------------------
// Phase 3 review additions — regression tests added in response to the
// ce-code-review findings on audit emission and LastRevalidated
// stamping. See the Phase 3 session log + PR description.
// ---------------------------------------------------------------------

// TestHandleLogin_AccountThrottle_AuditEntry pins the audit emit on
// 429 from the per-account throttle. The earlier brute-force /
// case-folded / independent-usernames tests asserted HTTP status and
// Retry-After only, so a regression that reordered statements before
// the writeJSON early-return (or dropped the AuditLogger.Log call
// entirely) would pass all three but leave throttle hits invisible
// in the audit table. Testing review T-01, conf 95.
func TestHandleLogin_AccountThrottle_AuditEntry(t *testing.T) {
	srv := testServer(t)
	srv.RateLimiter = middleware.NewRateLimiterWithRate(1, time.Minute)
	rec := &recordingAuditLogger{}
	srv.AuditLogger = rec

	// First attempt consumes the bucket — should fail with 401 (bad
	// creds, no user exists), emitting a login-failure audit entry.
	w1 := postLogin(t, srv, map[string]string{
		"username": "alice",
		"password": "wrong",
	}, "192.0.2.10:54321")
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt: expected 401, got %d", w1.Code)
	}

	// Second attempt hits the per-account throttle.
	w2 := postLogin(t, srv, map[string]string{
		"username": "alice",
		"password": "wrong",
	}, "192.0.2.99:54321") // rotated IP — IP throttle doesn't fire
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second attempt: expected 429 from account throttle, got %d: %s", w2.Code, w2.Body.String())
	}

	entries := rec.snapshot()
	var throttleEntry *audit.Entry
	for i := range entries {
		if entries[i].Action == audit.ActionRateLimited {
			throttleEntry = &entries[i]
			break
		}
	}
	if throttleEntry == nil {
		t.Fatalf("expected at least one ActionRateLimited audit entry; got: %+v", entries)
	}
	if throttleEntry.Result != audit.ResultDenied {
		t.Errorf("Result = %q, want %q", throttleEntry.Result, audit.ResultDenied)
	}
	if !strings.Contains(throttleEntry.Detail, "login") {
		t.Errorf("Detail = %q, want substring %q (purpose namespace)", throttleEntry.Detail, "login")
	}
	if !strings.Contains(throttleEntry.Detail, "retry") {
		t.Errorf("Detail = %q, want retry-seconds reference", throttleEntry.Detail)
	}
}

// TestIssueTokenPairAt_LastRevalidatedStamped pins the LastRevalidated
// field on the stored session. The LDAP grace-window predicate in
// revalidateLDAPSession (handle_auth.go) pivots entirely on this field
// — zero/stale -> fail-closed, recent -> grace-open. If the
// assignment were dropped from issueTokenPairAt's struct literal
// during a future refactor, the grace window would silently always
// fail-closed (or always open if the zero-time guard were also wrong)
// and nothing else would catch it. Testing review T-02, conf 90.
func TestIssueTokenPairAt_LastRevalidatedStamped(t *testing.T) {
	srv := testServer(t)

	fixedTime := time.Now().Add(-2 * time.Minute) // distinct from time.Now() inside the call
	user := &auth.User{ID: "ldap:corp:CN=alice,OU=Users", Username: "alice", Provider: "ldap"}

	w := httptest.NewRecorder()
	_, refreshToken, err := srv.issueTokenPairAt(w, user, false /* cookieMode */, fixedTime)
	if err != nil {
		t.Fatalf("issueTokenPairAt: %v", err)
	}

	var got time.Time
	var found bool
	srv.Sessions.RangeSessions(func(s auth.RefreshSession) bool {
		if s.Token == refreshToken {
			found = true
			got = s.LastRevalidated
			return false
		}
		return true
	})
	if !found {
		t.Fatalf("issued refresh token not in SessionStore")
	}
	if !got.Equal(fixedTime) {
		t.Errorf("session.LastRevalidated = %v, want %v", got, fixedTime)
	}
}
