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
func TestDriverArgsCurrent(t *testing.T) {
	assert.True(t, driverArgsCurrent("/usr/local/bin/magus vcs merge-driver %O %A %B %L %P"))
	assert.False(t, driverArgsCurrent("/usr/local/bin/magus merge-driver %O %A %B %L %P"),
		"the pre-move spelling must be reported stale so it gets rewritten")
	assert.False(t, driverArgsCurrent(""))
	assert.True(t, driverArgsCurrent(`"/opt/my magus/magus" vcs merge-driver %O %A %B %L %P`),
		"a quoted path is not part of the argument tail")

	// The direction the old substring probe got wrong, and the one that actually bit: a
	// binary whose own spelling is the SHORTER one reads a registration carrying the
	// longer. " merge-driver " appears inside "vcs merge-driver", so a containment test
	// called this current, nothing rewrote it, and git kept invoking a subcommand that
	// binary cannot dispatch. Simulated by asking the question the other binary would.
	const registered = "/usr/local/bin/magus vcs merge-driver %O %A %B %L %P"
	_, args := splitDriver(registered)
	assert.NotEqual(t, "merge-driver %O %A %B %L %P", args,
		"an older binary must not read the newer registration as its own spelling")
}

func TestSplitDriver(t *testing.T) {
	exe, args := splitDriver("/usr/local/bin/magus vcs merge-driver %O %A")
	assert.Equal(t, "/usr/local/bin/magus", exe)
	assert.Equal(t, "vcs merge-driver %O %A", args)

	exe, args = splitDriver(`"/opt/my magus/magus" vcs merge-driver %O`)
	assert.Equal(t, "/opt/my magus/magus", exe, "quotes are unwrapped")
	assert.Equal(t, "vcs merge-driver %O", args)

	exe, args = splitDriver("magus")
	assert.Equal(t, "magus", exe, "an executable with no arguments is still an executable")
	assert.Empty(t, args)

	exe, args = splitDriver("")
	assert.Empty(t, exe)
	assert.Empty(t, args)
}

// TestDriverExeAnswersRejectsABinaryThatDoesNotKnowTheSubcommand pins the install-time probe: PATH is only preferred when the binary
// there dispatches the spelling being registered.
func TestDriverExeAnswersRejectsABinaryThatDoesNotKnowTheSubcommand(t *testing.T) {
	exe, err := exec.LookPath("git")
	require.NoError(t, err)
	assert.False(t, driverExeAnswers(t.Context(), exe),
		"git exits non-zero on `git vcs merge-driver -h`, so it must not be registered as the driver")
	assert.False(t, driverExeAnswers(t.Context(), "/nonexistent/path/to/magus"))
}

func TestDriverExeExists(t *testing.T) {
	assert.False(t, driverExeExists(""))
	assert.False(t, driverExeExists("/nonexistent/path/to/magus vcs merge-driver %O"),
		"a registration pointing at a binary that is gone behaves exactly like no driver")

	exe, err := exec.LookPath("git")
	require.NoError(t, err)
	assert.True(t, driverExeExists(exe+" vcs merge-driver %O"))
	assert.True(t, driverExeExists(`"`+exe+`" vcs merge-driver %O`), "a quoted path is unwrapped")
}

// TestDriverUsablePreservesAWrapperAndRejectsAStaleVerb pins the pair the review found
// irreconcilable by string comparison: a wrapper-prefixed registration works and must survive
// EnsureMergeDriver, while a registration naming a verb the binary cannot dispatch must not.
func TestDriverUsablePreservesAWrapperAndRejectsAStaleVerb(t *testing.T) {
	// A stand-in for magus that accepts only the current verb, so "does it dispatch" is the
	// only thing being measured.
	dir := t.TempDir()
	fake := filepath.Join(dir, "magus")
	script := "#!/bin/sh\n[ \"$1\" = \"vcs\" ] && [ \"$2\" = \"merge-driver\" ] && exit 0\nexit 1\n"
	require.NoError(t, os.WriteFile(fake, []byte(script), 0o755))

	env, err := exec.LookPath("env")
	require.NoError(t, err)

	assert.True(t, driverUsable(t.Context(), fake+" vcs merge-driver %O %A %B %L %P"),
		"the spelling this binary writes short-circuits without a probe")

	assert.True(t, driverUsable(t.Context(), env+" FOO=1 "+fake+" vcs merge-driver %O %A %B %L %P"),
		"a wrapper that still dispatches must be left alone, not rewritten")

	assert.False(t, driverUsable(t.Context(), fake+" merge-driver %O %A %B %L %P"),
		"a verb the binary cannot dispatch must be reported unusable even though it is a suffix of the wanted one")

	assert.False(t, driverUsable(t.Context(), ""), "an empty registration is not usable")
}

// TestDriverUsableOnAnOlderBinarysSpelling is the direction a suffix comparison gets wrong, and
// the reason driverArgsMatch takes `wanted` as a parameter: posing as a magus whose verb is the
// bare `merge-driver`, a registration carrying the newer `vcs merge-driver` must read as NOT
// usable, so it gets rewritten to something that binary can dispatch. Under a suffix rule it
// reads as usable, and git falls back to conflict markers on every generated file.
func TestDriverUsableOnAnOlderBinarysSpelling(t *testing.T) {
	const olderWanted = " merge-driver %O %A %B %L %P"
	// A binary that answers NEITHER spelling, so the outcome is decided by the comparison
	// rather than by the probe.
	dir := t.TempDir()
	deaf := filepath.Join(dir, "magus")
	require.NoError(t, os.WriteFile(deaf, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	newerRegistration := deaf + " vcs merge-driver %O %A %B %L %P"
	assert.False(t, driverArgsMatch(newerRegistration, olderWanted),
		"the newer registration is not the older binary's own spelling")
	assert.False(t, driverUsableFor(t.Context(), newerRegistration, olderWanted),
		"an older binary must rewrite a verb it cannot dispatch, not keep it")

	assert.True(t, driverArgsMatch(deaf+" merge-driver %O %A %B %L %P", olderWanted),
		"its own spelling still short-circuits")
}

// TestDriverIsReachableHere: a driver registered by ANOTHER worktree points at a binary
// that exists and runs, so every rot check passes while merges resolve with a foreign
// build. Only "is this what I would have chosen" catches it.
func TestDriverIsReachableHere(t *testing.T) {
	assert.False(t, driverIsReachableHere(t.Context(), ""))
	assert.False(t, driverIsReachableHere(t.Context(),
		"/Users/x/repo/.claude/worktrees/other-8f2a/magus vcs merge-driver %O"),
		"another worktree's binary is not this one's, however runnable it is")

	assert.True(t, driverIsReachableHere(t.Context(), "magus vcs merge-driver %O"),
		"a bare name resolves through PATH wherever it runs")

	self, err := os.Executable()
	require.NoError(t, err)
	assert.True(t, driverIsReachableHere(t.Context(), self+" vcs merge-driver %O"))
	assert.True(t, driverIsReachableHere(t.Context(), `"`+self+`" vcs merge-driver %O`),
		"a quoted path is unwrapped before comparison")
}
