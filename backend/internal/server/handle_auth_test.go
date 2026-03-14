package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
	localAuth := auth.NewLocalProvider(logger)
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
	_, err := srv.LocalAuth.CreateUser("admin", "password1234", []string{"admin"})
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
			User auth.User `json:"user"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.User.Username != "admin" {
		t.Errorf("expected username admin, got %s", resp.Data.User.Username)
	}
	if resp.Data.User.Provider != "local" {
		t.Errorf("expected provider local, got %s", resp.Data.User.Provider)
	}
}

func TestHandleSetupInit_AlreadyDone(t *testing.T) {
	srv := testServer(t)

	// Create a user first
	srv.LocalAuth.CreateUser("existing", "password1234", []string{"admin"})

	body := `{"username":"admin","password":"password1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
	srv.LocalAuth.CreateUser("admin", "password1234", []string{"admin"})

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
	srv.LocalAuth.CreateUser("admin", "password1234", []string{"admin"})

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
		LocalAuth:    auth.NewLocalProvider(logger),
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

	// 1. Setup
	setup := doRequest(t, srv, http.MethodPost, "/api/v1/setup/init",
		`{"username":"admin","password":"password1234"}`, nil)
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

	// 6. Setup should now return 410
	setup2 := doRequest(t, srv, http.MethodPost, "/api/v1/setup/init",
		`{"username":"admin2","password":"password1234"}`, nil)
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
