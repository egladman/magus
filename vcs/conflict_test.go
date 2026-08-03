package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conflictRepo builds a repo with a real, in-progress merge conflict: main and side both
// change shared.txt, and side deletes gone.txt that main changed. Both shapes matter -
// a VCS invokes a merge driver only for the first.
func conflictRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v\n%s", err, out)
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	write := func(name, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")

	write("shared.txt", "base\n")
	write("gone.txt", "base\n")
	write("stable.txt", "base\n")
	git("add", ".")
	git("commit", "-m", "base")

	git("checkout", "-b", "side")
	write("shared.txt", "side\n")
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
	git("add", "-A")
	git("commit", "-m", "side")

	git("checkout", "main")
	write("shared.txt", "main\n")
	write("gone.txt", "main\n")
	git("add", "-A")
	git("commit", "-m", "main")

	// Expected to exit non-zero: that IS the conflict.
	_ = exec.Command("git", "-C", dir, "merge", "side").Run()
	return dir
}

func TestGitConflicts(t *testing.T) {
	dir := conflictRepo(t)

	got, err := gitVCS{}.Conflicts(context.Background(), dir)
	require.NoError(t, err)

	byPath := map[string]types.ConflictKind{}
	for _, c := range got {
		byPath[c.Path] = c.Kind
	}
	assert.Equal(t, map[string]types.ConflictKind{
		"shared.txt": types.ConflictKindContent,
		"gone.txt":   types.ConflictKindDeleted,
	}, byPath, "both conflict shapes are reported, and told apart")
}

// TestParseConflictsRenameHazard pins the parse against the rename hazard.
//
// `git status --porcelain -z` emits a rename as TWO NUL-terminated fields, the new path
// then the original, so a parser treating every field as a status entry reads that
// trailing original as one: "Utils/x.txt" becomes XY="Ut", passes the U test, and
// surfaces as a phantom conflict at "ls/x.txt".
//
// The real command masks this with --no-renames, which is why the parser is tested
// directly - a test through the flag alone passes with the parser broken.
func TestParseConflictsRenameHazard(t *testing.T) {
	// "R  Utils/y.txt" followed by its original path, then a genuine conflict.
	out := "R  Utils/y.txt\x00Utils/x.txt\x00UU gen.txt\x00"

	got := parseConflicts(out, "")
	assert.Equal(t, []types.Conflict{{Path: "gen.txt", Kind: types.ConflictKindContent}}, got,
		"the rename contributes nothing, and its original path is not read as a status entry")
}

func TestParseConflicts(t *testing.T) {
	tests := []struct {
		name   string
		out    string
		prefix string
		want   []types.Conflict
	}{
		{
			name: "both modified is a content conflict",
			out:  "UU shared.txt\x00",
			want: []types.Conflict{{Path: "shared.txt", Kind: types.ConflictKindContent}},
		},
		{
			name: "both added is a content conflict",
			out:  "AA shared.txt\x00",
			want: []types.Conflict{{Path: "shared.txt", Kind: types.ConflictKindContent}},
		},
		{
			name: "deleted by them",
			out:  "UD gone.txt\x00",
			want: []types.Conflict{{Path: "gone.txt", Kind: types.ConflictKindDeleted}},
		},
		{
			name: "deleted by us",
			out:  "DU gone.txt\x00",
			want: []types.Conflict{{Path: "gone.txt", Kind: types.ConflictKindDeleted}},
		},
		{
			name: "both deleted has no content on either side",
			out:  "DD gone.txt\x00",
			want: []types.Conflict{{Path: "gone.txt", Kind: types.ConflictKindBothDeleted}},
		},
		{
			name: "ordinary modifications are not conflicts",
			out:  " M a.go\x00M  b.go\x00?? c.go\x00",
			want: nil,
		},
		{
			name:   "paths are rebased onto the workspace root",
			out:    "UU sub/gen.txt\x00UU other/gen.txt\x00",
			prefix: "sub/",
			want:   []types.Conflict{{Path: "gen.txt", Kind: types.ConflictKindContent}},
		},
		{
			name: "trailing empty field is ignored",
			out:  "UU a.txt\x00\x00",
			want: []types.Conflict{{Path: "a.txt", Kind: types.ConflictKindContent}},
		},
		{
			name: "empty output",
			out:  "",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseConflicts(tt.out, tt.prefix))
		})
	}
}

func TestGitConflictsNoMergeInProgress(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v\n%s", err, out)
	}
	got, err := gitVCS{}.Conflicts(context.Background(), dir)
	require.NoError(t, err, "no operation in progress is not an error")
	assert.Empty(t, got)
}

// TestGitKeepIncomingAndMarkResolved walks the settle path callers use: clear the
// markers, then record the result.
func TestGitKeepIncomingAndMarkResolved(t *testing.T) {
	dir := conflictRepo(t)
	v := gitVCS{}
	ctx := context.Background()

	require.NoError(t, v.KeepIncoming(ctx, dir, []string{"shared.txt"}))

	body, err := os.ReadFile(filepath.Join(dir, "shared.txt"))
	require.NoError(t, err)
	assert.Equal(t, "side\n", string(body), "the incoming side is what gets kept")
	assert.NotContains(t, string(body), "<<<<<<<", "no conflict markers survive")

	require.NoError(t, v.MarkResolved(ctx, dir, []string{"shared.txt"}))
	out, err := exec.Command("git", "-C", dir, "diff", "--name-only", "--diff-filter=U").Output()
	require.NoError(t, err)
	assert.NotContains(t, string(out), "shared.txt", "the path is no longer unmerged")
}

func TestGitRemoveConflicts(t *testing.T) {
	dir := conflictRepo(t)
	ctx := context.Background()

	require.NoError(t, gitVCS{}.RemoveConflicts(ctx, dir, []string{"gone.txt"}))
	assert.NoFileExists(t, filepath.Join(dir, "gone.txt"))

	out, err := exec.Command("git", "-C", dir, "diff", "--name-only", "--diff-filter=U").Output()
	require.NoError(t, err)
	assert.NotContains(t, string(out), "gone.txt")
}

// TestGitRemoveConflictsToleratesAlreadyGone covers the modify/delete case where the
// merge already removed the file: one missing path must not fail the batch.
func TestGitRemoveConflictsToleratesAlreadyGone(t *testing.T) {
	dir := conflictRepo(t)
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
	require.NoError(t, gitVCS{}.RemoveConflicts(context.Background(), dir, []string{"gone.txt"}))
}

// TestGitIgnoredPaths pins the --no-index semantics resolution depends on. Every
// conflicted path is tracked, and check-ignore's default consults the index and calls
// anything tracked not-ignored - which makes a generated file one side STOPPED tracking
// look like one still under version control, reverting the deletion every merge.
func TestGitIgnoredPaths(t *testing.T) {
	dir := conflictRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("gone.txt\n"), 0o644))

	got, err := gitVCS{}.IgnoredPaths(context.Background(), dir, []string{"gone.txt", "shared.txt"})
	require.NoError(t, err)
	assert.True(t, got["gone.txt"], "the ignore RULES cover it, even though it is still tracked")
	assert.False(t, got["shared.txt"])
}

func TestGitIgnoredPathsNoneMatch(t *testing.T) {
	dir := conflictRepo(t)
	// check-ignore exits 1 when nothing matches; that is an answer, not a failure.
	got, err := gitVCS{}.IgnoredPaths(context.Background(), dir, []string{"shared.txt"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGitIgnoredPathsEmptyInput(t *testing.T) {
	got, err := gitVCS{}.IgnoredPaths(context.Background(), t.TempDir(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGitPathChunks(t *testing.T) {
	paths := make([]string, gitArgChunkSize*2+3)
	for i := range paths {
		paths[i] = "p"
	}
	chunks := gitPathChunks(paths)
	assert.Len(t, chunks, 3)
	assert.Len(t, chunks[0], gitArgChunkSize)
	assert.Len(t, chunks[1], gitArgChunkSize)
	assert.Len(t, chunks[2], 3)
	assert.Empty(t, gitPathChunks(nil))
}

// TestDriverCurrent pins the registration-staleness probe. The driver moved under `vcs`
// and the registration lives in each clone's .git/config, which no commit can update, so
// missing an old spelling means git invokes a dead subcommand and falls back to markers.
func TestDriverCurrent(t *testing.T) {
	assert.True(t, driverCurrent("/usr/local/bin/magus vcs merge-driver %O %A %B %L %P"))
	assert.False(t, driverCurrent("/usr/local/bin/magus merge-driver %O %A %B %L %P"),
		"the pre-move spelling must be reported stale so it gets rewritten")
	assert.False(t, driverCurrent(""))
}

func TestDriverExecutable(t *testing.T) {
	assert.False(t, driverExecutable(""))
	assert.False(t, driverExecutable("/nonexistent/path/to/magus vcs merge-driver %O"),
		"a registration pointing at a binary that is gone behaves exactly like no driver")

	exe, err := exec.LookPath("git")
	require.NoError(t, err)
	assert.True(t, driverExecutable(exe+" vcs merge-driver %O"))
	assert.True(t, driverExecutable(`"`+exe+`" vcs merge-driver %O`), "a quoted path is unwrapped")
}
