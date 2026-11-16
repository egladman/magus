package console

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForgeBlobBase(t *testing.T) {
	cases := []struct {
		name, remote, branch, want string
	}{
		{"github ssh, empty branch -> main", "git@github.com:egladman/magus.git", "", "https://github.com/egladman/magus/blob/main"},
		{"github https", "https://github.com/egladman/magus.git", "main", "https://github.com/egladman/magus/blob/main"},
		{"github https, no .git, branch", "https://github.com/egladman/magus", "dev", "https://github.com/egladman/magus/blob/dev"},
		{"github ssh:// scheme", "ssh://git@github.com/egladman/magus.git", "main", "https://github.com/egladman/magus/blob/main"},
		{"detached HEAD -> main", "git@github.com:egladman/magus.git", "HEAD", "https://github.com/egladman/magus/blob/main"},
		{"github enterprise host", "git@github.example.com:team/repo.git", "trunk", "https://github.example.com/team/repo/blob/trunk"},
		{"gitlab not handled", "git@gitlab.com:group/repo.git", "main", ""},
		{"bitbucket not handled", "https://bitbucket.org/team/repo.git", "main", ""},
		{"unknown host", "git@example.com:o/r.git", "main", ""},
		{"missing repo", "git@github.com:owneronly", "main", ""},
		{"empty", "", "main", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := forgeBlobBase(c.remote, c.branch); got != c.want {
				t.Errorf("forgeBlobBase(%q, %q) = %q, want %q", c.remote, c.branch, got, c.want)
			}
		})
	}
}

// TestParseGitRemote covers both remote spellings a forge hands out, plus the refusals that
// keep a wrong link from being emitted.
func TestParseGitRemote(t *testing.T) {
	tests := []struct {
		remote            string
		host, owner, repo string
	}{
		{"git@github.com:egladman/magus.git", "github.com", "egladman", "magus"},
		{"https://github.com/egladman/magus.git", "github.com", "egladman", "magus"},
		{"https://user@git.example.com:8443/team/repo", "git.example.com", "team", "repo"},
		{"ssh://git@github.com/egladman/magus", "github.com", "egladman", "magus"},
		{"  git@github.com:egladman/magus  ", "github.com", "egladman", "magus"},
		{"github.com/egladman/magus", "", "", ""},
		{"git@github.com", "", "", ""},
		{"https://github.com/egladman", "github.com", "", ""},
	}

	for _, tc := range tests {
		host, owner, repo := parseGitRemote(tc.remote)
		assert.Equal(t, tc.host, host, tc.remote)
		assert.Equal(t, tc.owner, owner, tc.remote)
		assert.Equal(t, tc.repo, repo, tc.remote)
	}
}

// TestGithubRawBase pins the deliberate narrowness: other forges serve raw content from
// different hosts, so an unmapped one yields no link rather than a wrong one.
func TestGithubRawBase(t *testing.T) {
	assert.Equal(t,
		"https://raw.githubusercontent.com/egladman/magus/main",
		githubRawBase("https://github.com/egladman/magus/blob/main"))

	assert.Equal(t, "", githubRawBase("https://gitlab.com/o/r/-/blob/main"))
	assert.Equal(t, "", githubRawBase(""))
}

// TestGraphExplorerLinkNeedsACommittedGraph covers the guard that keeps MAGUS.md from
// carrying a link that 404s. No docs/graph.json means no link, decided before any VCS
// resolution is attempted.
func TestGraphExplorerLinkNeedsACommittedGraph(t *testing.T) {
	asked := func() string {
		t.Error("the source base was asked for without a committed graph")
		return ""
	}
	assert.Equal(t, "", GraphExplorerLink(t.TempDir(), asked))
}
