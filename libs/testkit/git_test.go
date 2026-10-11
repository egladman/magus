package testkit

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRepoCommitsFilesOnMain(t *testing.T) {
	r := NewRepo(t, map[string]string{"a.txt": "a", "sub/b.txt": "b"})

	assert.Equal(t, "main", r.Git("rev-parse", "--abbrev-ref", "HEAD"))
	assert.Equal(t, []string{"a.txt", "sub/b.txt"}, strings.Split(r.Git("ls-files"), "\n"))
	assert.Empty(t, r.Git("status", "--porcelain"))
	assert.Equal(t, "b", r.Git("show", "HEAD:sub/b.txt"))
	toplevel, err := filepath.EvalSymlinks(r.Git("rev-parse", "--show-toplevel"))
	require.NoError(t, err)
	assert.Equal(t, r.Root(), toplevel)
}

func TestNewRepoWithNoFilesHasAHead(t *testing.T) {
	r := NewRepo(t, nil)
	assert.Len(t, r.Head(), 40)
	assert.Empty(t, r.Git("ls-files"))
}

// TestNewRepoSetsTheFixtureConfig: the config must be the repository's own, since the
// code under test runs git with the environment it builds, not the one Git uses.
func TestNewRepoSetsTheFixtureConfig(t *testing.T) {
	r := NewRepo(t, nil)
	for _, kv := range repoConfig {
		assert.Equal(t, kv[1], r.Git("config", "--local", "--get", kv[0]), kv[0])
	}
}

// TestRepoGitIgnoresTheCallersGitEnvironment: a pre-commit hook sets GIT_DIR and
// GIT_INDEX_FILE for everything it runs, and a developer's global config can install
// hooks (husky, pre-commit). None of it may reach the fixture.
func TestRepoGitIgnoresTheCallersGitEnvironment(t *testing.T) {
	home := NewWorkspace(t)
	hook := home.Executable("hooks/pre-commit", "#!/bin/sh\nexit 1\n")
	home.Write("gitconfig", "[core]\n\thooksPath = "+filepath.Dir(hook)+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", home.Path("gitconfig"))
	t.Setenv("GIT_DIR", home.Path("not-a-repo"))
	t.Setenv("GIT_INDEX_FILE", home.Path("index"))

	r := NewRepo(t, map[string]string{"a.txt": "a"})
	r.Commit("second", map[string]string{"a.txt": "b"})

	assert.Equal(t, "b", r.Git("show", "HEAD:a.txt"))
	assert.NotContains(t, r.Git("config", "--list", "--show-origin"), "hooksPath")
}

func TestRepoCommitReturnsTheNewHead(t *testing.T) {
	r := NewRepo(t, map[string]string{"a.txt": "a"})
	first := r.Head()

	second := r.Commit("change a", map[string]string{"a.txt": "a2", "c.txt": "c"})

	assert.Equal(t, r.Head(), second)
	assert.NotEqual(t, first, second)
	assert.Equal(t, first, r.Git("rev-parse", "HEAD^"))
	assert.Equal(t, "change a", r.Git("log", "-1", "--format=%s"))
	assert.Equal(t, "a2", r.Git("show", "HEAD:a.txt"))
	assert.Equal(t, "c", r.Git("show", "HEAD:c.txt"))
}

func TestRepoAddWorktree(t *testing.T) {
	r := NewRepo(t, map[string]string{"a.txt": "a"})
	base := r.Head()

	wt := r.AddWorktree("topic")

	assert.NotEqual(t, r.Root(), wt.Root())
	assert.Equal(t, base, wt.Head())
	assert.Equal(t, "topic", wt.Git("rev-parse", "--abbrev-ref", "HEAD"))
	assert.FileExists(t, wt.Path("a.txt"))
	resolved, err := filepath.EvalSymlinks(wt.Root())
	require.NoError(t, err)
	assert.Equal(t, resolved, wt.Root())

	moved := wt.Commit("on topic", map[string]string{"b.txt": "b"})
	assert.Equal(t, base, r.Head(), "a commit in the worktree moved main")
	assert.Equal(t, moved, r.Git("rev-parse", "topic"))
	assert.Contains(t, r.Git("worktree", "list", "--porcelain"), "worktree "+wt.Root()+"\n")
}

func TestRepoGitFailsTheTestOnError(t *testing.T) {
	r := NewRepo(t, nil)
	fake := &fatalRecorder{TB: t}
	broken := &Repo{Workspace: &Workspace{tb: fake, root: r.Root()}}

	broken.Git("rev-parse", "no-such-ref")

	assert.True(t, fake.failed)
}
