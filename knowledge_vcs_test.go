package magus

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitRun runs one git command in dir, fataling on error. Skips the test if git is
// unavailable so CI without git does not fail spuriously.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

func writeCommit(t *testing.T, dir, file, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644))
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c")
}

func gitHeadFull(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// TestLoadKnowledgeVCSHistory drives the whole opt-in path against a real repo (routed
// through the VCS abstraction): per-file commit counts, most-recent-commit, and - locking
// in the core.quotePath fix - a non-ASCII filename that must come through raw to match.
func TestLoadKnowledgeVCSHistory(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "one\n")  // a: commit 1
	writeCommit(t, root, "b.buzz", "one\n")  // b: commit 1
	writeCommit(t, root, "a.buzz", "two\n")  // a: commit 2 (most recent for a)
	writeCommit(t, root, "café.buzz", "u\n") // non-ASCII path

	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.VCSConfig{Enabled: true}}}
	entries := loadKnowledgeVCS(context.Background(), cfg, root, t.TempDir(), slog.Default())
	require.NotEmpty(t, entries)

	byPath := map[string]int{}
	last := map[string]string{}
	for _, e := range entries {
		byPath[e.Path] = e.Commits
		last[e.Path] = e.LastCommit
		assert.NotEmpty(t, e.LastCommit, "every entry has a last commit")
		assert.Positive(t, e.LastUnix, "every entry has an author time")
		assert.Equal(t, "t", e.LastAuthor, "the last commit's author is captured (GIT_AUTHOR_NAME)")
	}
	assert.Equal(t, 2, byPath["a.buzz"], "a.buzz was touched by two commits")
	assert.Equal(t, 1, byPath["b.buzz"], "b.buzz was touched by one commit")
	assert.Contains(t, byPath, "café.buzz", "non-ASCII path comes through raw (quotePath=false), not git-quoted")

	// a.buzz's recorded last commit is the most recent one (HEAD), abbreviated.
	head := gitHeadFull(t, root)
	assert.True(t, strings.HasPrefix(head, last["café.buzz"]), "last commit is an abbreviation of HEAD")
}

func TestLoadKnowledgeVCSDisabledAndNonGit(t *testing.T) {
	// Disabled: no scan, nil result, even in a git repo.
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	assert.Nil(t, loadKnowledgeVCS(context.Background(), config.Config{}, root, t.TempDir(), slog.Default()))

	// Enabled but not a git repo: best-effort nil, no error.
	enabled := config.Config{Knowledge: config.Knowledge{VCS: config.VCSConfig{Enabled: true}}}
	assert.Nil(t, loadKnowledgeVCS(context.Background(), enabled, t.TempDir(), t.TempDir(), slog.Default()))
}

// TestLoadKnowledgeVCSHeadCache proves the scan is gated on the revision: the first call
// writes a sidecar keyed by the current commit, and a second call with an unchanged
// revision reuses it (identical results, sidecar head matches HEAD).
func TestLoadKnowledgeVCSHeadCache(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	cacheDir := t.TempDir()
	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.VCSConfig{Enabled: true}}}

	first := loadKnowledgeVCS(context.Background(), cfg, root, cacheDir, slog.Default())
	require.NotEmpty(t, first)

	cached, ok := readVCSCache(filepath.Join(cacheDir, "knowledge", "vcs-inputs.json"))
	require.True(t, ok, "first call writes the sidecar")
	assert.Equal(t, gitHeadFull(t, root), cached.Head, "sidecar is keyed by the current revision")

	// Second call with the same cache and unchanged revision returns the same entries.
	second := loadKnowledgeVCS(context.Background(), cfg, root, cacheDir, slog.Default())
	assert.Equal(t, first, second)
}

// TestLoadKnowledgeVCSMaxInvalidatesCache confirms a changed max_commits (not just a new
// commit) invalidates the cache, since the resolved bound is part of the key.
func TestLoadKnowledgeVCSMaxInvalidatesCache(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	cacheDir := t.TempDir()
	sidecar := filepath.Join(cacheDir, "knowledge", "vcs-inputs.json")

	loadKnowledgeVCS(context.Background(), config.Config{Knowledge: config.Knowledge{VCS: config.VCSConfig{Enabled: true}}}, root, cacheDir, slog.Default())
	c1, _ := readVCSCache(sidecar)
	assert.Equal(t, vcsDefaultMaxCommits, c1.Max, "unset max resolves to the default in the key")

	loadKnowledgeVCS(context.Background(), config.Config{Knowledge: config.Knowledge{VCS: config.VCSConfig{Enabled: true, MaxCommits: 5}}}, root, cacheDir, slog.Default())
	c2, _ := readVCSCache(sidecar)
	assert.Equal(t, 5, c2.Max, "a changed max_commits rewrites the sidecar with the new bound")
}

// TestLoadKnowledgeVCSNestedWorkspace confirms the prefix strip: when the workspace root
// is a subdir of the git root, ChangesByCommit's VCS-root-relative paths are re-rooted to
// workspace-relative so they line up with file-node Sources.
func TestLoadKnowledgeVCSNestedWorkspace(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	writeCommit(t, repo, "other.buzz", "root\n") // outside the sub-workspace
	sub := filepath.Join(repo, "sub", "proj")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	writeCommit(t, repo, "sub/proj/app.buzz", "nested\n")

	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.VCSConfig{Enabled: true}}}
	entries := loadKnowledgeVCS(context.Background(), cfg, sub, t.TempDir(), slog.Default())
	require.NotEmpty(t, entries)

	paths := map[string]bool{}
	for _, e := range entries {
		paths[e.Path] = true
	}
	assert.True(t, paths["app.buzz"], "sub/proj/app.buzz is re-rooted to app.buzz")
	assert.False(t, paths["sub/proj/app.buzz"], "the vcs-root prefix is stripped")
	assert.False(t, paths["other.buzz"], "files outside the workspace subtree are excluded")
}
