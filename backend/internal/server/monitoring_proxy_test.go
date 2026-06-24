package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// grafanaProxyTestServer creates a Server with a MonitoringHandler wired so
// that the Grafana proxy routes exist. A stub http.Handler stands in for the
// real Grafana instance.
func grafanaProxyTestServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Stub Grafana proxy: always 200 so we can distinguish it from middleware
	// rejections (403/405).
	stubProxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	discoverer := monitoring.NewTestDiscoverer(
		&monitoring.MonitoringStatus{
			Grafana: monitoring.ComponentStatus{Available: true},
		},
		stubProxy,
	)

	monHandler := &monitoring.Handler{
		Discoverer: discoverer,
		Logger:     logger,
	}

	tokenManager := auth.NewTokenManager([]byte("test-signing-key-minimum-32-bytes"))
	localAuth := auth.NewLocalProvider(auth.NewMemoryUserStore(), logger)
	sessions := auth.NewSessionStore()
	auditLogger := audit.NewSlogLogger(logger)
	rateLimiter := middleware.NewRateLimiter()
	authRegistry := auth.NewProviderRegistry()
	authRegistry.RegisterCredential("local", "Local Accounts", localAuth)

	srv := New(Deps{
		Config: &config.Config{
			Dev:       true,
			ClusterID: "test-cluster",
			Server: config.ServerConfig{
				Port:            8080,
				RequestTimeout:  config.DefaultRequestTimeout,
				ShutdownTimeout: config.DefaultShutdownTimeout,
			},
			Log: config.LogConfig{Level: "error", Format: "json"},
		},
		Logger:            logger,
		TokenManager:      tokenManager,
		LocalAuth:         localAuth,
		AuthRegistry:      authRegistry,
		OIDCStateStore:    auth.NewOIDCStateStore(),
		Sessions:          sessions,
		AuditLogger:       auditLogger,
		RateLimiter:       rateLimiter,
		MonitoringHandler: monHandler,
		ReadyFn:           func() bool { return true },
	})
	return srv
}

// loginUser creates a user with the given username and roles, logs in, and
// returns the access token.
func loginUser(t *testing.T, srv *Server, username string, roles []string) string {
	t.Helper()
	_, err := srv.LocalAuth.CreateUser(context.Background(), username, "password1234", roles, nil)
	if err != nil {
		t.Fatalf("CreateUser %s: %v", username, err)
	}
	body := `{"username":"` + username + `","password":"password1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s failed: %d %s", username, w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login %s: %v", username, err)
	}
	return resp.Data.AccessToken
}

// TestGrafanaProxy_NonAdminGets403 verifies that a non-admin authenticated user
// receives 403 Forbidden when GETing the Grafana proxy endpoint.
func TestGrafanaProxy_NonAdminGets403(t *testing.T) {
	srv := grafanaProxyTestServer(t)
	viewerToken := loginUser(t, srv, "viewer", []string{"viewer"})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/monitoring/grafana/proxy/api/dashboards/db", nil)
	req.Header.Set("Authorization", "Bearer "+viewerToken)

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin GET grafana proxy: expected 403, got %d (body: %s)",
			w.Code, w.Body.String())
	}
}

// TestGrafanaProxy_AdminGets200 verifies that an admin user can reach the
// Grafana proxy stub on a path in the allowlist.
func TestGrafanaProxy_AdminGets200(t *testing.T) {
	srv := grafanaProxyTestServer(t)
	adminToken := loginUser(t, srv, "admin", []string{"admin"})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/monitoring/grafana/proxy/d/kubecenter-pods/overview", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	// /d/ is in the Grafana proxy allowlist, stub returns 200.
	if w.Code != http.StatusOK {
		t.Errorf("admin GET grafana proxy: expected 200, got %d (body: %s)",
			w.Code, w.Body.String())
	}
}

// loginCapture logs in and returns the grafana_proxy_token cookie set by the
// login response (or nil if absent).
func loginCapture(t *testing.T, srv *Server, username string, roles []string) *http.Cookie {
	t.Helper()
	if _, err := srv.LocalAuth.CreateUser(context.Background(), username, "password1234", roles, nil); err != nil {
		t.Fatalf("CreateUser %s: %v", username, err)
	}
	body := `{"username":"` + username + `","password":"password1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s failed: %d %s", username, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "grafana_proxy_token" {
			return c
		}
	}
	return nil
}

// TestGrafanaProxy_AdminCookieGets200 verifies the browser-navigation case: an
// admin reaches the proxy with ONLY the path-scoped cookie (no Authorization
// header), and that login sets the cookie scoped + hardened correctly.
func TestGrafanaProxy_AdminCookieGets200(t *testing.T) {
	srv := grafanaProxyTestServer(t)
	cookie := loginCapture(t, srv, "admin-cookie", []string{"admin"})
	if cookie == nil || cookie.Value == "" {
		t.Fatal("login did not set grafana_proxy_token cookie")
	}
	if cookie.Path != "/api/v1/monitoring/grafana/proxy" {
		t.Errorf("cookie path = %q, want /api/v1/monitoring/grafana/proxy", cookie.Path)
	}
	if !cookie.HttpOnly {
		t.Error("grafana_proxy_token cookie must be HttpOnly")
	}

	// No Authorization header — cookie only, as a browser navigation sends.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/monitoring/grafana/proxy/d/kubecenter-pods/overview", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("admin cookie GET grafana proxy: expected 200, got %d (body: %s)",
			w.Code, w.Body.String())
	}
}

// TestGrafanaProxy_NonAdminCookieGets403 verifies the admin gate still applies
// on the cookie auth path.
func TestGrafanaProxy_NonAdminCookieGets403(t *testing.T) {
	srv := grafanaProxyTestServer(t)
	cookie := loginCapture(t, srv, "viewer-cookie", []string{"viewer"})
	if cookie == nil || cookie.Value == "" {
		t.Fatal("login did not set grafana_proxy_token cookie")
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/monitoring/grafana/proxy/d/kubecenter-pods/overview", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin cookie GET grafana proxy: expected 403, got %d (body: %s)",
			w.Code, w.Body.String())
	}
}

// TestGrafanaProxy_PostReturns405 verifies that POST (not in Get/Head-only
// registration) returns 405 Method Not Allowed from chi, regardless of user role.
func TestGrafanaProxy_PostReturns405(t *testing.T) {
	srv := grafanaProxyTestServer(t)
	adminToken := loginUser(t, srv, "admin2", []string{"admin"})

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/monitoring/grafana/proxy/api/dashboards/db", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST grafana proxy: expected 405, got %d (body: %s)",
			w.Code, w.Body.String())
	}
}
