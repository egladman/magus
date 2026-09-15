package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// resolveGitDriver resolves dir's real git driver through the same vcs.Resolve path
// production code uses, so these tests exercise the actual types.VCSDriver
// (ParentRef/ChangedFiles/Metadata/CommitPushed), not a hand-rolled stand-in.
func resolveGitDriver(t *testing.T, dir string) types.VCSDriver {
	t.Helper()
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	require.Equal(t, "git", res.Name)
	return res.VCS
}

// fixedClassify stands in for magus.ClassifyFiles: checkDriftForCommit does not need a
// loaded workspace to make its decision, only the source/output classification a real
// one would return, so a test supplies that directly instead of building one.
func fixedClassify(files []types.FileEntry) func(context.Context, []string) ([]types.FileEntry, error) {
	return func(context.Context, []string) ([]types.FileEntry, error) {
		return files, nil
	}
}

func writeAndCommit(t *testing.T, dir, name, body, message string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", message)
}

// noFormatCheck stands in for the (formatGlobs, gofmtList) pair on a test that has
// nothing to say about formatting: nil globs already short-circuit before gofmtList
// would ever run, and the stub panics if that assumption ever breaks.
var noFormatGlobs []string

func noGofmtList(context.Context, string, []string) ([]string, error) {
	panic("gofmtList must not be called when formatGlobs is empty")
}

// TestCheckDriftForCommit_Clean pins case 1: a source change with its matching output
// change in the same commit prints nothing.
func TestCheckDriftForCommit_Clean(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.txt", "a\n", "first")
	writeAndCommit(t, dir, "b.txt", "b\n", "second")

	driver := resolveGitDriver(t, dir)
	classify := fixedClassify([]types.FileEntry{
		{Path: "b.txt", Role: "source", SourceOf: []string{"."}},
		{Path: "b.gen", Role: "output", OutputOf: []string{"."}},
	})

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, noFormatGlobs, noGofmtList)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, notice)
}

// TestCheckDriftForCommit_UnpushedHead pins case 2: a source changed with no matching
// output, on a commit that is unpushed and still HEAD, prints the regenerate command
// AND the amend command naming that commit's hash.
func TestCheckDriftForCommit_UnpushedHead(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.txt", "a\n", "first")
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-u", "origin", "HEAD")

	writeAndCommit(t, dir, "api.proto", "syntax\n", "change api source only")
	hash := gitOut(t, dir, "rev-parse", "HEAD")

	driver := resolveGitDriver(t, dir)
	classify := fixedClassify([]types.FileEntry{
		{Path: "api.proto", Role: "source", SourceOf: []string{"api"}},
	})

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, noFormatGlobs, noGofmtList)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, notice, "magus run generate:rw api")
	assert.Contains(t, notice, "git commit --amend --no-edit")
	assert.Contains(t, notice, hash)
	assert.NotContains(t, notice, "--fixup")
	assert.NotContains(t, notice, "already pushed")
}

// TestCheckDriftForCommit_UnpushedNotHead pins case 3: HEAD moves on WHILE the check is
// still running (simulated here inside the classify callback, the real seam between
// magus loading the workspace and the drift check finishing), so the drifted commit is
// no longer HEAD by the time the notice is built. The notice must switch to the
// fixup/autosquash form and must NOT offer a plain amend, which would silently rewrite
// the wrong commit.
func TestCheckDriftForCommit_UnpushedNotHead(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.txt", "a\n", "first")
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-u", "origin", "HEAD")

	writeAndCommit(t, dir, "api.proto", "syntax\n", "change api source only")
	hash := gitOut(t, dir, "rev-parse", "HEAD")

	driver := resolveGitDriver(t, dir)
	classify := func(context.Context, []string) ([]types.FileEntry, error) {
		// A second commit lands here, after checkDriftForCommit already captured HEAD
		// as the drifted commit but before it re-checks whether that commit is still
		// the tip.
		writeAndCommit(t, dir, "unrelated.txt", "x\n", "raced ahead")
		return []types.FileEntry{{Path: "api.proto", Role: "source", SourceOf: []string{"api"}}}, nil
	}

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, noFormatGlobs, noGofmtList)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, notice, "git commit --fixup="+hash)
	assert.Contains(t, notice, "rebase -i --autosquash "+hash+"^")
	assert.NotContains(t, notice, "--amend")
}

// TestCheckDriftForCommit_Pushed pins case 4: the drifted commit already reached the
// remote. Neither rewrite form appears; the notice says to follow up instead.
func TestCheckDriftForCommit_Pushed(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.txt", "a\n", "first")
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-u", "origin", "HEAD")

	writeAndCommit(t, dir, "api.proto", "syntax\n", "change api source only")
	hash := gitOut(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "push", "origin", "HEAD")

	driver := resolveGitDriver(t, dir)
	classify := fixedClassify([]types.FileEntry{
		{Path: "api.proto", Role: "source", SourceOf: []string{"api"}},
	})

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, noFormatGlobs, noGofmtList)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, notice, hash+" is already pushed")
	assert.NotContains(t, notice, "--amend")
	assert.NotContains(t, notice, "--fixup")
}

// TestCheckDriftForCommit_FormattingOnly pins the formatting class end to end against a
// stub gofmt, with no generated-output finding in play: formatGlobs narrows to Go files
// the format target governs, gofmtList reports which are unformatted, and the notice
// carries MGS4009 (not MGS4006, which is the other class) for it.
func TestCheckDriftForCommit_FormattingOnly(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.go", "package a\n", "first")
	writeAndCommit(t, dir, "b.go", "package a\n\nfunc B() {}\n", "second")

	driver := resolveGitDriver(t, dir)
	classify := fixedClassify(nil)
	gofmtList := func(_ context.Context, gotRoot string, files []string) ([]string, error) {
		assert.Equal(t, dir, gotRoot)
		assert.Equal(t, []string{"b.go"}, files, "only the changed, format-governed Go file is checked")
		return []string{"b.go"}, nil
	}

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, []string{"**/*.go"}, gofmtList)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, notice, "gofmt would reformat")
	assert.Contains(t, notice, "b.go")
	assert.Contains(t, notice, "magus run format:rw .")
	assert.Contains(t, notice, "MGS4009")
	assert.NotContains(t, notice, "MGS4006")
	assert.NotContains(t, notice, "generate:rw")
}

// TestCheckDriftForCommit_FormatGlobsScopeTheCheck pins that a Go file outside the
// format target's declared globs never reaches gofmtList at all - the narrowing happens
// before the (only) live check runs.
func TestCheckDriftForCommit_FormatGlobsScopeTheCheck(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.go", "package a\n", "first")
	writeAndCommit(t, dir, "vendor/b.go", "package a\n", "second")

	driver := resolveGitDriver(t, dir)
	classify := fixedClassify(nil)

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, []string{"internal/**/*.go"}, noGofmtList)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, notice)
}

// TestCheckDriftForCommit_RealGofmt runs the actual gofmt binary (not a stub) against a
// real misformatted file, end to end: this is the one live check the whole feature
// performs, and it must stay read-only (no -w: b.go is byte-identical on disk
// afterwards) and must stay scoped to the one file that changed.
func TestCheckDriftForCommit_RealGofmt(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not available")
	}
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.go", "package a\n", "first")
	const misformatted = "package a\nfunc   B(){}\n" // gofmt -l flags this
	writeAndCommit(t, dir, "b.go", misformatted, "second")

	driver := resolveGitDriver(t, dir)
	classify := fixedClassify(nil)

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, []string{"**/*.go"}, realGofmtList)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, notice, "b.go")

	body, err := os.ReadFile(filepath.Join(dir, "b.go"))
	require.NoError(t, err)
	assert.Equal(t, misformatted, string(body), "gofmt -l must never rewrite the file")
}

// gitOut runs a git command in dir and returns trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	require.NoError(t, err, "git %v", args)
	return strings.TrimSpace(string(out))
}
