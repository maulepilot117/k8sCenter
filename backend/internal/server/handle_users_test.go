package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// authedRequest creates an authenticated request with the given method, path, and optional body.
func authedRequest(method, path, token string, body string) *http.Request {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return req
}

func TestHandleListUsers(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	// Create a second user
	_, err := srv.LocalAuth.CreateUser(context.Background(), "viewer", "password1234", []string{"viewer"}, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	req := authedRequest(http.MethodGet, "/api/v1/users", token, "")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data     []auth.UserRecord `json:"data"`
		Metadata *api.Metadata     `json:"metadata"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Metadata.Total != 2 {
		t.Errorf("expected 2 users, got %d", resp.Metadata.Total)
	}
	// Verify no password data in response
	for _, u := range resp.Data {
		if u.PasswordPHC != "" {
			t.Errorf("password should be empty in JSON, got %q for user %s", u.PasswordPHC, u.Username)
		}
	}
}

func TestHandleDeleteUser(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	// Create a user to delete
	user, err := srv.LocalAuth.CreateUser(context.Background(), "to-delete", "password1234", []string{"viewer"}, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	req := authedRequest(http.MethodDelete, "/api/v1/users/"+user.ID, token, "")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify user is gone
	_, err = srv.LocalAuth.Store().GetByID(context.Background(), user.ID)
	if err == nil {
		t.Error("user should be deleted but was found")
	}
}

func TestHandleDeleteUser_SelfDelete(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	// Get admin user ID
	users, _ := srv.LocalAuth.Store().List(context.Background())
	var adminID string
	for _, u := range users {
		if u.Username == "admin" {
			adminID = u.ID
			break
		}
	}

	req := authedRequest(http.MethodDelete, "/api/v1/users/"+adminID, token, "")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for self-delete, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteUser_LastAdmin(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	// Create a second admin so the first admin can make the request
	admin2, err := srv.LocalAuth.CreateUser(context.Background(), "admin2", "password1234", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Delete admin2 — should succeed (2 admins remain → 1)
	req := authedRequest(http.MethodDelete, "/api/v1/users/"+admin2.ID, token, "")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for deleting second admin, got %d: %s", w.Code, w.Body.String())
	}

	// Now create a non-admin and try to delete the last admin (self-delete guard fires first)
	// Instead, create another admin, login as them, and try to delete the original last admin
	admin3, err := srv.LocalAuth.CreateUser(context.Background(), "admin3", "password1234", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Get original admin ID
	users, _ := srv.LocalAuth.Store().List(context.Background())
	var origAdminID string
	for _, u := range users {
		if u.Username == "admin" {
			origAdminID = u.ID
			break
		}
	}

	// Login as admin3
	body := `{"username":"admin3","password":"password1234"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	lw := httptest.NewRecorder()
	srv.Router.ServeHTTP(lw, loginReq)
	var loginResp struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	json.NewDecoder(lw.Body).Decode(&loginResp)
	token3 := loginResp.Data.AccessToken

	// Delete original admin — should succeed (2 admins: admin + admin3)
	req = authedRequest(http.MethodDelete, "/api/v1/users/"+origAdminID, token3, "")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Now admin3 is the last admin. Try self-delete — should get self-delete error (409)
	req = authedRequest(http.MethodDelete, "/api/v1/users/"+admin3.ID, token3, "")
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for last admin self-delete, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUpdateUserPassword(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	// Create a user
	user, err := srv.LocalAuth.CreateUser(context.Background(), "testuser", "oldpassword1", []string{"viewer"}, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Change password
	body := `{"password":"newpassword1"}`
	req := authedRequest(http.MethodPut, "/api/v1/users/"+user.ID+"/password", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify login with new password works
	loginBody := `{"username":"testuser","password":"newpassword1"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	lw := httptest.NewRecorder()
	srv.Router.ServeHTTP(lw, loginReq)

	if lw.Code != http.StatusOK {
		t.Errorf("login with new password failed: %d %s", lw.Code, lw.Body.String())
	}
}

func TestHandleUpdateUserPassword_TooShort(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	user, err := srv.LocalAuth.CreateUser(context.Background(), "testuser", "password1234", []string{"viewer"}, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	body := `{"password":"short"}`
	req := authedRequest(http.MethodPut, "/api/v1/users/"+user.ID+"/password", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUsers_NonAdminAccess(t *testing.T) {
	srv := testServer(t)

	// Create admin first (needed for setup)
	_, err := srv.LocalAuth.CreateUser(context.Background(), "admin", "password1234", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateUser admin: %v", err)
	}

	// Create non-admin user
	viewer, err := srv.LocalAuth.CreateUser(context.Background(), "viewer", "password1234", []string{"viewer"}, nil)
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}

	// Login as viewer
	body := `{"username":"viewer","password":"password1234"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	lw := httptest.NewRecorder()
	srv.Router.ServeHTTP(lw, loginReq)
	var loginResp struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	json.NewDecoder(lw.Body).Decode(&loginResp)
	viewerToken := loginResp.Data.AccessToken

	// All three endpoints should return 403
	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/users", ""},
		{http.MethodDelete, "/api/v1/users/" + viewer.ID, ""},
		{http.MethodPut, "/api/v1/users/" + viewer.ID + "/password", `{"password":"newpass123"}`},
	}

	for _, ep := range endpoints {
		req := authedRequest(ep.method, ep.path, viewerToken, ep.body)
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s: expected 403, got %d: %s", ep.method, ep.path, w.Code, w.Body.String())
		}
	}
}

func TestHandleCreateUser(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	body := `{"username":"newuser","password":"password1234","roles":["viewer"]}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			ID          string   `json:"id"`
			Username    string   `json:"username"`
			K8sUsername string   `json:"k8sUsername"`
			K8sGroups   []string `json:"k8sGroups"`
			Roles       []string `json:"roles"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.ID == "" {
		t.Error("expected non-empty id in response")
	}
	if resp.Data.Username != "newuser" {
		t.Errorf("expected username=newuser, got %s", resp.Data.Username)
	}
	if resp.Data.K8sUsername != "newuser" {
		t.Errorf("expected k8sUsername=newuser (default), got %s", resp.Data.K8sUsername)
	}
	if len(resp.Data.K8sGroups) == 0 {
		t.Error("expected k8sGroups in response")
	}
}

func TestHandleCreateUser_WithK8sIdentity(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	body := `{"username":"oidcuser","password":"password1234","k8sUsername":"oidc:jane@example.com","k8sGroups":["devs","system:authenticated"],"roles":["admin"]}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			K8sUsername string   `json:"k8sUsername"`
			K8sGroups   []string `json:"k8sGroups"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.K8sUsername != "oidc:jane@example.com" {
		t.Errorf("expected custom k8sUsername, got %s", resp.Data.K8sUsername)
	}
	if len(resp.Data.K8sGroups) != 2 {
		t.Errorf("expected 2 k8sGroups, got %d", len(resp.Data.K8sGroups))
	}
}

func TestHandleCreateUser_DuplicateUsername(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	// admin already exists from loginAdmin
	body := `{"username":"admin","password":"password1234"}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUser_SystemK8sUsername(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	body := `{"username":"testuser","password":"password1234","k8sUsername":"system:admin"}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for system: k8sUsername, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUser_SystemMastersGroup(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	body := `{"username":"testuser","password":"password1234","k8sGroups":["system:masters"]}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for system:masters group, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUser_SystemPrefixGroup(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	body := `{"username":"testuser","password":"password1234","k8sGroups":["system:nodes"]}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for system: group prefix, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUser_InvalidRole(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	body := `{"username":"testuser","password":"password1234","roles":["superuser"]}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid role, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUser_TooManyGroups(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	// Build JSON with 21 groups
	groups := make([]string, 21)
	for i := range groups {
		groups[i] = "group" + strings.Repeat("x", i)
	}
	groupsJSON, _ := json.Marshal(groups)
	body := `{"username":"testuser","password":"password1234","k8sGroups":` + string(groupsJSON) + `}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too many groups, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUser_PasswordTooShort(t *testing.T) {
	srv := testServer(t)
	token, _ := loginAdmin(t, srv)

	body := `{"username":"testuser","password":"short"}`
	req := authedRequest(http.MethodPost, "/api/v1/users", token, body)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d: %s", w.Code, w.Body.String())
	}
}
