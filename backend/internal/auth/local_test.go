package auth

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestLocalProvider_CreateAndAuthenticate(t *testing.T) {
	p := NewLocalProvider(testLogger())

	user, err := p.CreateUser("admin", "password123", []string{"admin"})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("expected username admin, got %s", user.Username)
	}
	if user.KubernetesUsername != "admin" {
		t.Errorf("expected k8s username admin, got %s", user.KubernetesUsername)
	}

	// Authenticate with correct credentials
	authed, err := p.Authenticate(context.Background(), Credentials{
		Username: "admin",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if authed.Username != "admin" {
		t.Errorf("expected username admin, got %s", authed.Username)
	}
	if authed.ID != user.ID {
		t.Errorf("expected same user ID")
	}
}

func TestLocalProvider_WrongPassword(t *testing.T) {
	p := NewLocalProvider(testLogger())
	p.CreateUser("admin", "password123", []string{"admin"})

	_, err := p.Authenticate(context.Background(), Credentials{
		Username: "admin",
		Password: "wrongpass",
	})
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestLocalProvider_UnknownUser(t *testing.T) {
	p := NewLocalProvider(testLogger())

	_, err := p.Authenticate(context.Background(), Credentials{
		Username: "nobody",
		Password: "password123",
	})
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
}

func TestLocalProvider_DuplicateUser(t *testing.T) {
	p := NewLocalProvider(testLogger())
	p.CreateUser("admin", "password123", []string{"admin"})

	_, err := p.CreateUser("admin", "otherpass", []string{"admin"})
	if err == nil {
		t.Fatal("expected error for duplicate user")
	}
}

func TestLocalProvider_UserCount(t *testing.T) {
	p := NewLocalProvider(testLogger())

	if p.UserCount() != 0 {
		t.Errorf("expected 0 users, got %d", p.UserCount())
	}

	p.CreateUser("user1", "password123", []string{"viewer"})
	if p.UserCount() != 1 {
		t.Errorf("expected 1 user, got %d", p.UserCount())
	}

	p.CreateUser("user2", "password123", []string{"viewer"})
	if p.UserCount() != 2 {
		t.Errorf("expected 2 users, got %d", p.UserCount())
	}
}

func TestLocalProvider_GetUserByID(t *testing.T) {
	p := NewLocalProvider(testLogger())
	created, _ := p.CreateUser("admin", "password123", []string{"admin"})

	found, err := p.GetUserByID(created.ID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if found.Username != "admin" {
		t.Errorf("expected username admin, got %s", found.Username)
	}

	_, err = p.GetUserByID("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
}

func TestLocalProvider_Type(t *testing.T) {
	p := NewLocalProvider(testLogger())
	if p.Type() != "local" {
		t.Errorf("expected type 'local', got %s", p.Type())
	}
}
