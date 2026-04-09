package gitprovider

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v83/github"
)

func TestGitHubClient_GetCommit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/commits/abc123") {
			http.NotFound(w, r)
			return
		}

		resp := github.RepositoryCommit{
			SHA:     github.Ptr("abc123full"),
			HTMLURL: github.Ptr("https://github.com/org/repo/commit/abc123full"),
			Commit: &github.Commit{
				Message: github.Ptr("fix: resolve bug\n\nDetailed description"),
				Author: &github.CommitAuthor{
					Name:  github.Ptr("Jane Smith"),
					Email: github.Ptr("jane@example.com"),
					Date:  &github.Timestamp{Time: time.Date(2026, 4, 7, 15, 0, 0, 0, time.UTC)},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client, err := NewGitHubClient("test-token", ts.URL+"/", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	info, err := client.GetCommit(context.Background(), "org", "repo", "abc123")
	if err != nil {
		t.Fatal(err)
	}

	if info.SHA != "abc123full" {
		t.Errorf("SHA = %q, want %q", info.SHA, "abc123full")
	}
	if info.Message != "fix: resolve bug\n\nDetailed description" {
		t.Errorf("Message = %q, want %q", info.Message, "fix: resolve bug\n\nDetailed description")
	}
	if info.AuthorName != "Jane Smith" {
		t.Errorf("AuthorName = %q, want %q", info.AuthorName, "Jane Smith")
	}
	if info.WebURL != "https://github.com/org/repo/commit/abc123full" {
		t.Errorf("WebURL = %q", info.WebURL)
	}
	if info.Title() != "fix: resolve bug" {
		t.Errorf("Title() = %q, want %q", info.Title(), "fix: resolve bug")
	}
}

func TestGitHubClient_GetCommit_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	}))
	defer ts.Close()

	client, err := NewGitHubClient("test-token", ts.URL+"/", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.GetCommit(context.Background(), "org", "repo", "deadbeef")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestGitHubClient_GetCommit_RateLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{
			"message":           "API rate limit exceeded",
			"documentation_url": "https://docs.github.com/rest",
		})
	}))
	defer ts.Close()

	client, err := NewGitHubClient("test-token", ts.URL+"/", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.GetCommit(context.Background(), "org", "repo", "abc123")
	if err == nil {
		t.Fatal("expected error for rate limit")
	}
}
