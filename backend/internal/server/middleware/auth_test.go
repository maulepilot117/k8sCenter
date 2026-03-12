package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
)

func TestAuth_RejectsNoToken(t *testing.T) {
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"))

	handler := Auth(tm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/cluster/info", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuth_RejectsInvalidToken(t *testing.T) {
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"))

	handler := Auth(tm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/cluster/info", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuth_AcceptsValidToken(t *testing.T) {
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"))

	user := &auth.User{
		ID:                 "user-1",
		Username:           "admin",
		KubernetesUsername: "admin",
		KubernetesGroups:   []string{"system:masters"},
		Roles:              []string{"admin"},
	}

	token, err := tm.IssueAccessToken(user)
	if err != nil {
		t.Fatalf("IssueAccessToken failed: %v", err)
	}

	var gotUser *auth.User
	handler := Auth(tm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFromContext(r.Context())
		if ok {
			gotUser = u
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/cluster/info", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if gotUser == nil {
		t.Fatal("expected user in context")
	}
	if gotUser.Username != "admin" {
		t.Errorf("expected admin, got %s", gotUser.Username)
	}
}

func TestAuth_RejectsBadFormat(t *testing.T) {
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"))

	handler := Auth(tm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/cluster/info", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestCSRF_BlocksStateChangingWithoutHeader(t *testing.T) {
	handler := CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		req := httptest.NewRequest(method, "/api/v1/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d", method, rec.Code)
		}
	}
}

func TestCSRF_AllowsWithHeader(t *testing.T) {
	handler := CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		req := httptest.NewRequest(method, "/api/v1/test", nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", method, rec.Code)
		}
	}
}

func TestCSRF_AllowsGET(t *testing.T) {
	handler := CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}
