package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseSaplingConflicts pins the `sl resolve --list` parse. Only U is a conflict: an R
// line is a path already settled - and Sapling produces those more readily than Mercurial
// does, because `resolve --tool` marks as it goes (see KeepIncoming). Re-resolving one
// would clobber a resolution the previous step just made.
func TestParseSaplingConflicts(t *testing.T) {
	got := parseSaplingConflicts("U MAGUS.md\nR docs/index.md\nU gen/graph.json\n")
	require.Len(t, got, 2)
	assert.Equal(t, types.Conflict{Path: "MAGUS.md", Kind: types.ConflictKindContent}, got[0])
	assert.Equal(t, types.Conflict{Path: "gen/graph.json", Kind: types.ConflictKindContent}, got[1])

	t.Run("no merge in progress yields none", func(t *testing.T) {
		assert.Empty(t, parseSaplingConflicts(""))
	})
	t.Run("paths with spaces survive", func(t *testing.T) {
		got := parseSaplingConflicts("U docs/my notes.md\n")
		require.Len(t, got, 1)
		assert.Equal(t, "docs/my notes.md", got[0].Path)
	})
	t.Run("CRLF and malformed lines are skipped, not mis-parsed", func(t *testing.T) {
		got := parseSaplingConflicts("U a.md\r\ngarbage\nX b.md\n\n")
		require.Len(t, got, 1)
		assert.Equal(t, "a.md", got[0].Path)
	})
}

// TestParseSaplingRemovalCandidates pins modify/delete detection against real
// `sl debugmergestate` output (Sapling 0.2.20260811-150444).
//
// Sapling's merge state is NOT Mercurial's, which is the whole reason this parser exists
// beside parseHgRemovalCandidates instead of reusing it: hg marks the deleted side with an
// `extra: merge-removal-candidate = yes` line that Sapling never writes. Sapling states it
// as a null other-side node and a "C" record type. Feeding this output to hg's parser finds
// nothing, and every modify/delete would be silently classified as a content conflict -
// which regeneration cannot settle.
func TestParseSaplingRemovalCandidates(t *testing.T) {
	const out = `local: f659ed4f11ca57164705271d7b9217cd28bb3edc
other: c1310f58284a524f8c8054ebe6fc04911d2573f5
labels:
  local: working copy
  other: merge rev
file: f.txt (record type "F", state "u", hash 7ad4af83b511907a1db3f4d18c33c63d9b6c4d9e)
  local path: f.txt (flags "")
  ancestor path: f.txt (node 45f4124ae41922b65c14c0ffb3b4c82f17590215)
  other path: f.txt (node a6ff991c9d7f358cc078596e8067ac5747dbe820)
  extras: ancestorlinknode=cd97a5ab95a8996ced574a98983bcf84e84851ab
file: gone.txt (record type "C", state "u", hash 0ce54e727589f3f743f5063e0d17c60a306f17fe)
  local path: gone.txt (flags "")
  ancestor path: gone.txt (node 1ab48f07b43344869acb18b4d6105f9c28f63210)
  other path: gone.txt (node null)
  extras: ancestorlinknode=cd97a5ab95a8996ced574a98983bcf84e84851ab
`
	got := parseSaplingRemovalCandidates(out)
	assert.True(t, got["gone.txt"], "the side with no content is a removal candidate")
	assert.False(t, got["f.txt"], "a two-sided content conflict is not a removal")

	t.Run("hg's marker alone does not satisfy it", func(t *testing.T) {
		// Guards against someone "unifying" the two parsers on the hg spelling: Sapling
		// never emits this line, so a parser keyed on it reports no deletions forever.
		const hgShaped = `file: gone.txt (state "u")
  other path: gone.txt (node 0000000000000000000000000000000000000000)
  extra: merge-removal-candidate = yes
`
		assert.Empty(t, parseSaplingRemovalCandidates(hgShaped),
			"this parser reads Sapling's shape; hg's belongs to parseHgRemovalCandidates")
	})

	t.Run("unparseable output degrades to no deletions", func(t *testing.T) {
		assert.Empty(t, parseSaplingRemovalCandidates("debugmergestate: unknown command"))
	})
}

// Sapling's status column width is pinned through the DRIVER, against a live repository,
// by TestParityDirtyFilesReturnsPaths. It used to be asserted here by calling
// trimStatusColumns with a literal 2 - which never touched saplingVCS at all, so changing
// the driver's width to 3 left it green. A test that cannot fail for the thing it names is
// worse than no test, because it reads as coverage.

// slInitRepo initializes a Sapling repository in dir with one commit holding files. It is
// the sl counterpart of gitInitRepo, and vcs_test.go's cross-backend tables call it too.
//
// ui.username is written into the REPO's config rather than the user's: sl refuses to
// commit without one, and a test must neither depend on nor touch the developer's global
// Sapling config.
func slInitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	vcsTestRun(t, dir, "sl", "init", ".")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".sl", "config"),
		[]byte("[ui]\nusername = Magus Test <magus@example.com>\n"), 0o644))
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		vcsTestRun(t, dir, "sl", "add", name)
	}
	vcsTestRun(t, dir, "sl", "commit", "-m", "init")
}

// slRepo is slInitRepo in a fresh temp dir, skipping the test when sl is absent.
func slRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("sl"); err != nil {
		t.Skip("sl not available")
	}
	dir := t.TempDir()
	slInitRepo(t, dir, files)
	return dir
}

// Sapling reports no tags, and that has to be an ANSWER rather than an accident. `sl tags`
// is a deprecated no-op that exits non-zero, so a driver that shelled out to it the way hg
// does would surface a hard error on a routine call - and Describe would fail every release
// banner. Both must report "nothing" without consulting the CLI at all.
func TestSaplingReportsNoTags(t *testing.T) {
	dir := slRepo(t, map[string]string{"a.txt": "one\n"})

	tags, err := saplingVCS{}.Tags(t.Context(), dir, "")
	require.NoError(t, err, "Tags must answer, not fail, on a backend without the concept")
	assert.Empty(t, tags)

	desc, err := saplingVCS{}.Describe(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, desc, "the contract is \"\" for a backend with no describe, not a faked one")
}

// TestSaplingDirtyFilesIgnoresTheMergeBanner is the regression test for Sapling's
// unfinished-merge commentary. During a merge `sl status` appends a block naming every
// conflicted path ("# The repository is in an unfinished *merge* state."). It goes to
// stderr, so reading stdout alone is what keeps it out - but that is a property of how this
// driver invokes sl, not a promise sl makes, and a switch to CombinedOutput would turn each
// of those lines into a phantom changed path.
func TestSaplingDirtyFilesIgnoresTheMergeBanner(t *testing.T) {
	dir := slMergeConflict(t)

	files, err := saplingVCS{}.DirtyFiles(t.Context(), dir, nil)
	require.NoError(t, err)
	for _, p := range files {
		assert.NotContains(t, p, "#", "the unfinished-merge banner leaked into the paths: %q", p)
		assert.NotContains(t, p, "unfinished", "banner text parsed as a changed path: %q", p)
	}
	assert.Equal(t, []string{"f.txt"}, files)
}

// slMergeConflict leaves dir in an unfinished merge with one content conflict (f.txt) and
// one modify/delete (gone.txt), returning the repo root.
func slMergeConflict(t *testing.T) string {
	t.Helper()
	dir := slRepo(t, map[string]string{"f.txt": "base\n", "gone.txt": "gone\n"})
	base := slRev(t, dir, ".")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("side-a\n"), 0o644))
	vcsTestRun(t, dir, "sl", "remove", "gone.txt")
	vcsTestRun(t, dir, "sl", "commit", "-m", "sideA")
	other := slRev(t, dir, ".")

	vcsTestRun(t, dir, "sl", "goto", base)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("side-b\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("changed\n"), 0o644))
	vcsTestRun(t, dir, "sl", "commit", "-m", "sideB")

	require.NoError(t, saplingVCS{}.StartMerge(t.Context(), dir, other),
		"StartMerge must treat a CONFLICTING merge as started, not as a failure")
	return dir
}

func slRev(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := vcsOutput(t.Context(), dir, "sl", "log", "-r", rev, "--template", "{node|short}")
	require.NoError(t, err)
	return out
}

// The conflict round trip is what `magus vcs resolve` runs. It is checked end to end rather
// than per method because the interesting failures are in the seams: whether a conflicting
// merge counts as started, whether the delete is classified apart from the content
// conflict, and whether marking actually clears the state.
func TestSaplingConflictRoundTrip(t *testing.T) {
	dir := slMergeConflict(t)
	v := saplingVCS{}

	conflicts, err := v.Conflicts(t.Context(), dir)
	require.NoError(t, err, "Conflicts")
	kinds := map[string]types.ConflictKind{}
	for _, c := range conflicts {
		kinds[c.Path] = c.Kind
	}
	assert.Equal(t, types.ConflictKindContent, kinds["f.txt"])
	assert.Equal(t, types.ConflictKindDeleted, kinds["gone.txt"],
		"a modify/delete read as a content conflict is one regeneration cannot settle")

	require.NoError(t, v.KeepIncoming(t.Context(), dir, []string{"f.txt"}), "KeepIncoming")
	// The caller regenerates between KeepIncoming and MarkResolved, so what gets recorded is
	// the regenerated content and not whichever side seeded it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("regenerated\n"), 0o644))
	require.NoError(t, v.MarkResolved(t.Context(), dir, []string{"f.txt"}), "MarkResolved")

	left, err := v.Conflicts(t.Context(), dir)
	require.NoError(t, err)
	for _, c := range left {
		assert.NotEqual(t, "f.txt", c.Path, "f.txt was marked resolved and must leave the list")
	}

	body, err := os.ReadFile(filepath.Join(dir, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "regenerated\n", string(body), "the working-copy content is the resolution")

	require.NoError(t, v.AbortMerge(t.Context(), dir), "AbortMerge")
	after, err := v.Conflicts(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, after, "aborting leaves no operation in progress")
}

// IgnoredPaths asks about the RULES, and Sapling's two answers differ by one word:
// "<path>: ignored by rule ..." and "<path>: not ignored". Both exit 0, and the negative
// one CONTAINS "ignored" - so a substring test for the bare word (which is what hg's
// implementation uses, against hg's different phrasing) reports every path as ignored, and
// resolution would delete generated files that are still tracked.
func TestSaplingIgnoredPathsDistinguishesNotIgnored(t *testing.T) {
	dir := slRepo(t, map[string]string{"a.txt": "one\n", ".gitignore": "*.log\n"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build.log"), []byte("x\n"), 0o644))

	got, err := saplingVCS{}.IgnoredPaths(t.Context(), dir, []string{"build.log", "a.txt"})
	require.NoError(t, err)
	assert.True(t, got["build.log"], "a path an ignore rule covers")
	assert.False(t, got["a.txt"], `"not ignored" contains "ignored"; matching the bare word inverts this`)
}

// ChangesByCommit promises the NEWEST commits. `sl log -r <revset>` follows the revset's
// order rather than log's default, and ancestors() is ascending - so without reverse() the
// limit takes the OLDEST N and a churn heatmap describes the repository's first week
// forever. Nothing else in the driver depends on revset ordering, so nothing else would
// catch it.
func TestSaplingChangesByCommitIsNewestFirst(t *testing.T) {
	dir := slRepo(t, map[string]string{"a.txt": "1\n"})
	for _, body := range []string{"2\n", "3\n", "4\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644))
		vcsTestRun(t, dir, "sl", "commit", "-m", "edit "+body)
	}

	changes, err := saplingVCS{}.ChangesByCommit(t.Context(), dir, 2, "")
	require.NoError(t, err, "ChangesByCommit")
	require.Len(t, changes, 2, "the limit must bound the result")
	assert.False(t, changes[0].Date.Before(changes[1].Date),
		"newest first: got %s then %s", changes[0].Date, changes[1].Date)
	assert.Equal(t, []types.FileChange{{Path: "a.txt", Status: types.ChangeModified}}, changes[0].Files)
	assert.NotEmpty(t, changes[0].Author)

	// since arrives as RFC 3339 with a numeric offset - that is what project.parseSince
	// formats, and it goes into a date() predicate rather than a flag, so the backend has
	// to accept that exact spelling. A bound nothing satisfies is an empty answer, not a
	// failure.
	t.Run("since bounds the window", func(t *testing.T) {
		recent, err := saplingVCS{}.ChangesByCommit(t.Context(), dir, 10,
			time.Now().Add(-time.Hour).Format(time.RFC3339))
		require.NoError(t, err)
		assert.NotEmpty(t, recent, "commits made seconds ago are inside a one-hour window")

		none, err := saplingVCS{}.ChangesByCommit(t.Context(), dir, 10,
			time.Now().Add(time.Hour).Format(time.RFC3339))
		require.NoError(t, err, "a window matching nothing is an answer, not an error")
		assert.Empty(t, none)
	})
}

// ExportRevision has two Sapling-specific traps, and both are silent. `sl archive` injects
// a provenance file that belongs to no commit, and it keeps repo-relative paths where git's
// archive re-roots them - so a workspace nested below the repository root would export into
// a subdirectory of dstDir and read as an empty tree.
func TestSaplingExportRevision(t *testing.T) {
	dir := slRepo(t, map[string]string{"a.txt": "one\n", "sub/b.txt": "two\n"})
	dst := t.TempDir()

	require.NoError(t, saplingVCS{}.ExportRevision(t.Context(), dir, ".", dst), "ExportRevision")

	body, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	require.NoError(t, err, "the revision's files must land at the top of dstDir")
	assert.Equal(t, "one\n", string(body))
	assert.FileExists(t, filepath.Join(dst, "sub", "b.txt"))
	assert.NoFileExists(t, filepath.Join(dst, saplingArchivalMeta),
		"sl archive's provenance file is in no commit and must not appear in the exported tree")

	t.Run("a nested dir exports re-rooted", func(t *testing.T) {
		nested := t.TempDir()
		require.NoError(t, saplingVCS{}.ExportRevision(t.Context(), filepath.Join(dir, "sub"), ".", nested))
		assert.FileExists(t, filepath.Join(nested, "b.txt"),
			"paths must be relative to the exported dir, not to the repository root")
		assert.NoFileExists(t, filepath.Join(nested, "a.txt"), "only the subtree is exported")
	})
}

// TrackedFiles must separate a tracked path from one that merely exists, which is the
// distinction a caller uses to tell a committed artifact from a build product.
func TestSaplingTrackedFiles(t *testing.T) {
	dir := slRepo(t, map[string]string{"a.txt": "one\n"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x\n"), 0o644))

	got, err := saplingVCS{}.TrackedFiles(t.Context(), dir, []string{"a.txt", "untracked.txt"})
	require.NoError(t, err, "an unmatched pathspec is an answer, not a failure")
	assert.Equal(t, []string{"a.txt"}, got)
}

// The merge driver is registered in .sl/config, and EnsureMergeDriver has to be silent in
// the steady state - callers run it on every workspace load.
func TestSaplingMergeDriverInstall(t *testing.T) {
	if _, err := exec.LookPath("sl"); err != nil {
		t.Skip("sl not available")
	}
	dir := t.TempDir()
	vcsTestRun(t, dir, "sl", "init", ".")
	v := saplingVCS{}

	registered, err := v.CheckMergeDriver(t.Context(), dir)
	require.NoError(t, err)
	assert.False(t, registered, "a fresh repo has no driver registered")

	changed, err := v.EnsureMergeDriver(t.Context(), dir, []string{"gen/**"})
	require.NoError(t, err, "EnsureMergeDriver")
	assert.True(t, changed, "the first install changes the config")

	registered, err = v.CheckMergeDriver(t.Context(), dir)
	require.NoError(t, err)
	assert.True(t, registered)

	changed, err = v.EnsureMergeDriver(t.Context(), dir, []string{"gen/**"})
	require.NoError(t, err)
	assert.False(t, changed, "re-running with the same globs must be a no-op")

	changed, err = v.EnsureMergeDriver(t.Context(), dir, []string{"gen/**", "dist/**"})
	require.NoError(t, err)
	assert.True(t, changed, "a new declared glob has to reach the config")

	body, err := os.ReadFile(slConfigPath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(body), "glob:dist/** = magus")

	t.Run("the refresh hook coexists with the merge driver", func(t *testing.T) {
		hooks, err := v.InstallRefreshHook(t.Context(), dir, "magus graph refresh")
		require.NoError(t, err, "InstallRefreshHook")
		assert.Equal(t, []string{"update"}, hooks)

		body, err := os.ReadFile(slConfigPath(dir))
		require.NoError(t, err)
		assert.Contains(t, string(body), "[hooks]")
		assert.Contains(t, string(body), "glob:gen/** = magus",
			"the two managed sections must not overwrite each other")

		again, err := v.InstallRefreshHook(t.Context(), dir, "magus graph refresh")
		require.NoError(t, err)
		assert.Nil(t, again, "an unchanged hook reports no install")
	})
}

// TestSaplingStartMergeRefusesWhenOneIsUnderway pins the pre-flight check.
//
// types.MergeStarter names an already-in-progress operation an error, and it cannot be
// detected after the fact: the leftover merge's own conflicts satisfy any "did conflicts
// appear" test, so the caller would resolve against a merge of a ref it never asked for.
// The check therefore has to run BEFORE the merge is attempted.
func TestSaplingStartMergeRefusesWhenOneIsUnderway(t *testing.T) {
	dir := slMergeConflict(t) // already leaves a merge in progress
	other := slRev(t, dir, "p1(.)")

	err := saplingVCS{}.StartMerge(t.Context(), dir, other)
	require.Error(t, err, "a second StartMerge must not report success over an in-progress merge")
	assert.Contains(t, err.Error(), "already in progress")
}
