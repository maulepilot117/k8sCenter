package auth

import (
	"context"
	"testing"
)

func TestContextWithUser_And_UserFromContext(t *testing.T) {
	user := &User{
		ID:       "user-1",
		Username: "testuser",
	}

	ctx := ContextWithUser(context.Background(), user)
	got, ok := UserFromContext(ctx)
	if !ok {
		t.Fatal("expected user in context")
	}
	if got.ID != "user-1" {
		t.Errorf("expected ID user-1, got %s", got.ID)
	}
	if got.Username != "testuser" {
		t.Errorf("expected username testuser, got %s", got.Username)
	}
}

func TestUserFromContext_Empty(t *testing.T) {
	_, ok := UserFromContext(context.Background())
	if ok {
		t.Fatal("expected no user in empty context")
	}
}
