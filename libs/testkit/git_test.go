package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRepoCommitsFilesOnMain(t *testing.T) {
	r := NewRepository(t, map[string]string{"a.txt": "a", "sub/b.txt": "b"})

	assert.Equal(t, "main", r.Git("rev-parse", "--abbrev-ref", "HEAD"))
	assert.Equal(t, []string{"a.txt", "sub/b.txt"}, strings.Split(r.Git("ls-files"), "\n"))
	assert.Empty(t, r.Git("status", "--porcelain"))
	assert.Equal(t, "b", r.Git("show", "HEAD:sub/b.txt"))
	toplevel, err := filepath.EvalSymlinks(r.Git("rev-parse", "--show-toplevel"))
	require.NoError(t, err)
	assert.Equal(t, r.Root(), toplevel)
}

func TestNewRepoWithNoFilesHasAHead(t *testing.T) {
	r := NewRepository(t, nil)
	assert.Len(t, r.Head(), 40)
	assert.Empty(t, r.Git("ls-files"))
}

// TestNewRepoSetsTheFixtureConfig: the config must be the repository's own, since the
// code under test runs git with the environment it builds, not the one Git uses.
func TestNewRepoSetsTheFixtureConfig(t *testing.T) {
	r := NewRepository(t, nil)
	for _, kv := range repoConfig {
		assert.Equal(t, kv[1], r.Git("config", "--local", "--get", kv[0]), kv[0])
	}
}

// TestRepoGitIgnoresTheCallersGitEnvironment: a pre-commit hook sets GIT_DIR and
// GIT_INDEX_FILE for everything it runs, and a developer's global config can install
// hooks (husky, pre-commit). None of it may reach the fixture.
func TestRepoGitIgnoresTheCallersGitEnvironment(t *testing.T) {
	home := NewWorkspace(t)
	hook := home.WriteExecutable("hooks/pre-commit", "#!/bin/sh\nexit 1\n")
	home.Write("gitconfig", fmt.Sprintf("[core]\n\thooksPath = %s\n", filepath.Dir(hook)))
	t.Setenv("GIT_CONFIG_GLOBAL", home.Path("gitconfig"))
	t.Setenv("GIT_DIR", home.Path("not-a-repo"))
	t.Setenv("GIT_INDEX_FILE", home.Path("index"))

	r := NewRepository(t, map[string]string{"a.txt": "a"})
	r.Commit("second", map[string]string{"a.txt": "b"})

	assert.Equal(t, "b", r.Git("show", "HEAD:a.txt"))
	assert.NotContains(t, r.Git("config", "--list", "--show-origin"), "hooksPath")
}

// TestNewRepoIgnoresAGlobalIgnoreFile: GIT_CONFIG_GLOBAL does not switch off the default
// $XDG_CONFIG_HOME/git/ignore, and a pattern there would otherwise drop fixture files.
func TestNewRepoIgnoresAGlobalIgnoreFile(t *testing.T) {
	home := NewWorkspace(t)
	home.Write(".config/git/ignore", "*.txt\n")
	t.Setenv("HOME", home.Root())
	t.Setenv("XDG_CONFIG_HOME", home.Path(".config"))

	r := NewRepository(t, map[string]string{"a.txt": "a"})

	assert.Equal(t, "a.txt", r.Git("ls-files"))
}

func TestRepoCommitReturnsTheNewHead(t *testing.T) {
	r := NewRepository(t, map[string]string{"a.txt": "a"})
	first := r.Head()

	second := r.Commit("change a", map[string]string{"a.txt": "a2", "c.txt": "c"})

	assert.Equal(t, r.Head(), second)
	assert.NotEqual(t, first, second)
	assert.Equal(t, first, r.Git("rev-parse", "HEAD^"))
	assert.Equal(t, "change a", r.Git("log", "-1", "--format=%s"))
	assert.Equal(t, "a2", r.Git("show", "HEAD:a.txt"))
	assert.Equal(t, "c", r.Git("show", "HEAD:c.txt"))
}

func TestAddWorktreeBranchesAtHeadInItsOwnDirectory(t *testing.T) {
	r := NewRepository(t, map[string]string{"a.txt": "a"})
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
	assert.Contains(t, r.Git("worktree", "list", "--porcelain"), fmt.Sprintf("worktree %s\n", wt.Root()))
}

func TestRepoGitFailsTheTestOnError(t *testing.T) {
	r := NewRepository(t, nil)
	fake := &fatalRecorder{TB: t}
	broken := &Repo{Workspace: &Workspace{tb: fake, root: r.Root()}}

	broken.Git("rev-parse", "no-such-ref")

	assert.True(t, fake.failed)
}

// errorRecorder stands in for a suite's testing.TB, keeping what
// AssertUnchanged reports instead of failing the test that checks it.
type errorRecorder struct {
	testing.TB
	errors []string
}

func (e *errorRecorder) Errorf(format string, args ...any) {
	e.errors = append(e.errors, fmt.Sprintf(format, args...))
}

// sharedWithTopic is a repository with a linked worktree on branch topic,
// shared from the worktree.
func sharedWithTopic(t *testing.T) (*Repo, *Repo, *SharedRepository) {
	t.Helper()
	r := NewRepository(t, map[string]string{"a.txt": "a\n", ".gitignore": "*.log\n"})
	wt := r.AddWorktree("topic")
	return r, wt, ShareRepository(t, wt.Root())
}

func reported(t *testing.T, shared *SharedRepository) []string {
	t.Helper()
	rec := &errorRecorder{TB: t}
	shared.AssertUnchanged(rec)
	return rec.errors
}

func TestSharedRepositoryIsUnchangedByReads(t *testing.T) {
	r, wt, _ := sharedWithTopic(t)
	wt.Commit("on topic", nil)
	shared := ShareRepository(t, r.Root())

	r.Git("status")
	wt.Git("status", "--porcelain", "--untracked-files=all", "--ignored")
	r.Git("log", "--all", "--oneline")
	r.Git("diff", "main", "topic")
	r.Git("worktree", "list", "--porcelain")
	r.Git("merge-tree", "--write-tree", "main", "topic")

	assert.Empty(t, reported(t, shared))
}

func TestSharedRepositoryNamesAMovedRef(t *testing.T) {
	_, wt, shared := sharedWithTopic(t)
	wt.Commit("on topic", nil)
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "refs/heads/topic")
}

func TestSharedRepositoryNamesARefPutBack(t *testing.T) {
	r, _, shared := sharedWithTopic(t)
	head := r.Head()
	r.Git("update-ref", "refs/heads/main", r.Git("commit-tree", "-m", "moved", "HEAD^{tree}"))
	r.Git("update-ref", "refs/heads/main", head)
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "logs/refs/heads/main")
}

func TestSharedRepositoryNamesAFileWrittenBackAsItWas(t *testing.T) {
	_, wt, shared := sharedWithTopic(t)
	wt.Write("a.txt", "a\n")
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(wt.Path("a.txt"), later, later))
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], fmt.Sprintf("+ %s ", wt.Path("a.txt")))
}

func TestSharedRepositoryNamesAnIgnoredFile(t *testing.T) {
	_, wt, shared := sharedWithTopic(t)
	wt.Write("build.log", "built\n")
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], fmt.Sprintf("+ %s ", wt.Path("build.log")))
}

func TestSharedRepositoryNamesAnUnstagedPath(t *testing.T) {
	_, wt, shared := sharedWithTopic(t)
	wt.Git("rm", "-q", "--cached", "a.txt")
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], fmt.Sprintf("- staged in %s: ", wt.Root()))
}

func TestSharedRepositoryNamesAConfigChange(t *testing.T) {
	r, wt, shared := sharedWithTopic(t)
	wt.Git("config", "magus.test", "set")
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], fmt.Sprintf("+ %s ", r.Path(".git", "config")))
}

func TestSharedRepositoryNamesAWorktreeLock(t *testing.T) {
	r, wt, shared := sharedWithTopic(t)
	r.Git("worktree", "lock", "--reason", "held", wt.Root())
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "/locked ")
}

func TestSharedRepositoryNamesADeletedWorktreeDirectory(t *testing.T) {
	_, wt, shared := sharedWithTopic(t)
	require.NoError(t, os.RemoveAll(wt.Root()))
	got := reported(t, shared)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], fmt.Sprintf("+ worktree %s: ", wt.Root()))
}

func TestSharedRepositoryFailsOnlyTheTestThatChangedIt(t *testing.T) {
	_, wt, shared := sharedWithTopic(t)
	wt.Write("b.txt", "b\n")
	require.Len(t, reported(t, shared), 1)
	assert.Empty(t, reported(t, shared), "the next test reads the repository as the last one left it")
}

func TestRepoUnderReportsToItsTest(t *testing.T) {
	r := NewRepository(t, nil)
	fake := &fatalRecorder{TB: t}

	r.Under(fake).Git("rev-parse", "no-such-ref")

	assert.True(t, fake.failed)
}
