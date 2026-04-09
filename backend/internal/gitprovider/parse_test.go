package gitprovider

import (
	"testing"
)

func TestParseRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    RepoRef
		wantErr bool
	}{
		{
			name:  "https with .git",
			input: "https://github.com/org/repo.git",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "https without .git",
			input: "https://github.com/org/repo",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "https trailing slash",
			input: "https://github.com/org/repo/",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "ssh shorthand",
			input: "git@github.com:org/repo.git",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "ssh shorthand without .git",
			input: "git@github.com:org/repo",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "ssh:// scheme",
			input: "ssh://git@github.com/org/repo.git",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "ssh:// with port",
			input: "ssh://git@github.com:22/org/repo.git",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "gitlab nested groups",
			input: "https://gitlab.com/group/subgroup/repo.git",
			want:  RepoRef{Host: "gitlab.com", Owner: "group/subgroup", Repo: "repo"},
		},
		{
			name:  "github enterprise",
			input: "https://github.internal.co/team/project.git",
			want:  RepoRef{Host: "github.internal.co", Owner: "team", Repo: "project"},
		},
		{
			name:  "bitbucket cloud",
			input: "https://bitbucket.org/workspace/repo-name",
			want:  RepoRef{Host: "bitbucket.org", Owner: "workspace", Repo: "repo-name"},
		},
		{
			name:  "repo with dots in name",
			input: "https://github.com/org/repo.v2.git",
			want:  RepoRef{Host: "github.com", Owner: "org", Repo: "repo.v2"},
		},
		{
			name:  "host is lowercased",
			input: "https://GitHub.COM/Org/Repo",
			want:  RepoRef{Host: "github.com", Owner: "Org", Repo: "Repo"},
		},
		{
			name:  "http scheme",
			input: "http://git.internal.co/team/project",
			want:  RepoRef{Host: "git.internal.co", Owner: "team", Repo: "project"},
		},
		// Error cases
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "missing repo path",
			input:   "https://github.com/",
			wantErr: true,
		},
		{
			name:    "only host, no path",
			input:   "https://github.com",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			input:   "ftp://github.com/org/repo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRepoURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Host != tt.want.Host || got.Owner != tt.want.Owner || got.Repo != tt.want.Repo {
				t.Errorf("got {%s, %s, %s}, want {%s, %s, %s}",
					got.Host, got.Owner, got.Repo,
					tt.want.Host, tt.want.Owner, tt.want.Repo)
			}
		})
	}
}

func TestCanonicalURL(t *testing.T) {
	ref := &RepoRef{Host: "github.com", Owner: "org", Repo: "repo"}
	want := "https://github.com/org/repo"
	if got := ref.CanonicalURL(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCommitInfoTitle(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"fix: bug", "fix: bug"},
		{"fix: bug\n\nDetailed description", "fix: bug"},
		{"", ""},
	}
	for _, tt := range tests {
		c := &CommitInfo{Message: tt.message}
		if got := c.Title(); got != tt.want {
			t.Errorf("Title(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}
