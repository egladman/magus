package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file asserts the invariants every backend must share, as one table run against all
// four. It exists because the per-backend files cannot: a rule stated only in git_test.go
// is a rule the next backend is free to break, and three of the defects it now pins shipped
// exactly that way - each backend was tested against its own behavior rather than against
// the contract.
//
// A backend whose binary is absent SKIPS rather than failing, so the suite still means
// something on a machine with only git. That is also its weakness: CI installs only git, so
// the other three are pinned by whoever runs this locally.
//
// Add a backend to parityBackends and every test here applies to it.

// parityBackend is one driver plus what a test needs to build a repository for it.
type parityBackend struct {
	name string
	bin  string
	drv  types.VCSDriver
	// init creates a repository in dir holding files, with everything committed.
	init func(t *testing.T, dir string, files map[string]string)
}

func parityBackends() []parityBackend {
	return []parityBackend{
		{"git", "git", gitVCS{}, gitInitRepo},
		{"hg", "hg", hgVCS{}, hgInitRepo},
		{"sl", "sl", saplingVCS{}, slInitRepo},
		{"jj", "jj", jjVCS{}, jjInitRepo},
	}
}

func hgInitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	vcsTestRun(t, dir, "hg", "init")
	for name, body := range files {
		writeRepoFile(t, dir, name, body)
		vcsTestRun(t, dir, "hg", "add", name)
	}
	vcsTestRun(t, dir, "hg", "commit", "-m", "init", "-u", "test")
}

func jjInitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	vcsTestRun(t, dir, "jj", "git", "init")
	for name, body := range files {
		writeRepoFile(t, dir, name, body)
	}
	// jj's working copy IS a commit, so the writes above land in @. `jj new` closes it and
	// starts an empty one, which is jj's equivalent of a clean tree.
	vcsTestRun(t, dir, "jj", "new")
}

// appendRepoFile adds to a file the backend already wrote, rather than replacing it.
func appendRepoFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer fh.Close()
	_, werr := fh.WriteString(body)
	require.NoError(t, werr)
}

func writeRepoFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// eachBackend runs fn against every backend whose binary is installed.
func eachBackend(t *testing.T, fn func(t *testing.T, b parityBackend)) {
	t.Helper()
	for _, b := range parityBackends() {
		t.Run(b.name, func(t *testing.T) {
			if _, err := exec.LookPath(b.bin); err != nil {
				t.Skipf("%s not available", b.bin)
			}
			fn(t, b)
		})
	}
}

// Every backend implements RevisionFileReader, and "" means the committed revision in each
// backend's own spelling - HEAD, `.`, `.`, `@`. A caller asking for the committed side has
// no way to name that portably, so the empty default is the portability, and a backend that
// resolved "" to something else would silently hand back the wrong content.
//
// `magus vcs resolve` reads the committed magusfile this way when the working copy's is
// mid-merge, so a backend answering with the CONFLICTED text would defeat the whole point:
// it would parse as badly as the file on disk.
func TestParityReadFileAtReturnsCommittedContent(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reader, ok := b.drv.(types.RevisionFileReader)
		require.Truef(t, ok, "%s does not implement RevisionFileReader", b.name)

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"magusfile.buzz": "committed\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("working\n"), 0o644))

		got, err := reader.ReadFileAt(t.Context(), dir, "", "magusfile.buzz")
		require.NoErrorf(t, err, "%s ReadFileAt", b.name)
		assert.Equalf(t, "committed\n", got,
			"%s returned the working copy, not the committed revision", b.name)
	})
}

// Content comes back EXACTLY. The shared output helpers trim, which is right for a status
// line and wrong for a file: a magusfile whose trailing newline was eaten is not the file
// the revision holds, and this is the capability whose whole promise is that it is.
func TestParityReadFileAtDoesNotTrim(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reader, ok := b.drv.(types.RevisionFileReader)
		require.Truef(t, ok, "%s does not implement RevisionFileReader", b.name)

		dir := t.TempDir()
		const body = "  leading\n\ntrailing blank line\n\n"
		b.init(t, dir, map[string]string{"a.txt": body})

		got, err := reader.ReadFileAt(t.Context(), dir, "", "a.txt")
		require.NoErrorf(t, err, "%s ReadFileAt", b.name)
		assert.Equalf(t, body, got, "%s trimmed the content", b.name)
	})
}

// A path the revision does not hold is an ERROR, not empty content. A caller building a
// magusfile overlay would otherwise load an empty magusfile and report a workspace with no
// projects rather than a file it could not read.
func TestParityReadFileAtMissingPathErrors(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reader, ok := b.drv.(types.RevisionFileReader)
		require.Truef(t, ok, "%s does not implement RevisionFileReader", b.name)

		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		_, err := reader.ReadFileAt(t.Context(), dir, "", "nosuchfile.buzz")
		require.Errorf(t, err, "%s reported no error for a path absent at that revision", b.name)
	})
}

// DirtyFiles returns PATHS, not the backend's status lines. Each backend prints a
// different prefix - git two columns, hg and sl one, jj none - and callers hand the result
// straight to glob matching and staging, so a line that keeps its "M " matches nothing and
// the file is silently treated as undeclared.
func TestParityDirtyFilesReturnsPaths(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))

		got, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyFiles")
		assert.Contains(t, got, "a.txt",
			"%s returned %q; a status prefix left on the line matches no glob", b.name, got)
	})
}

// Every backend reports paths relative to the REPOSITORY ROOT, whatever directory the
// probe runs in. Callers stamp the root as the base (std/vcs.go, std/magus.go), so a
// cwd-relative answer names a different file that frequently exists - there is nothing to
// error on, the wrong file is simply read.
//
// This is the single most valuable assertion in the file: sl and jj BOTH failed it, in
// opposite ways that neither backend's own tests could see. sl needed --root-relative; jj
// has no such flag and had to be run from the root.
func TestParityPathsAreRepositoryRelative(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"root.txt": "r\n", "sub/a.txt": "s\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("changed\n"), 0o644))

		got, err := b.drv.DirtyFiles(t.Context(), filepath.Join(dir, "sub"), nil)
		require.NoError(t, err, "DirtyFiles from a subdirectory")
		assert.Contains(t, got, "sub/a.txt",
			"%s probed from sub/ returned %q, not repo-relative paths", b.name, got)
		for _, p := range got {
			assert.NotContains(t, p, "..",
				"%s returned %q, a path escaping the directory it was asked about", b.name, p)
		}
	})
}

// A commit on linear history has exactly one parent. Reporting none makes it read as a
// root commit at the Buzz boundary, and makes len(Parents) > 1 merge detection permanently
// false. hg failed this: its `parents` template keyword filters through meaningfulparents
// and emits nothing off a merge, while sl's same-named keyword does not - so the two
// backends sharing one template disagreed, and only hg was wrong.
func TestParityLinearCommitHasOneParent(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
		commitAll(t, b, dir, "second")

		c, err := b.drv.FindCommit(t.Context(), dir, "")
		require.NoError(t, err, "FindCommit")
		require.NotEmpty(t, c.ID)
		assert.Len(t, c.Parents, 1,
			"%s reported %d parents for a linear commit; zero reads as a root commit", b.name, len(c.Parents))
	})
}

// Metadata is what stamps a revision into every output ref. An empty ID degrades every ref
// that backend touches to "unknown revision", and a dirty tree reported clean makes a ref
// claim a reproducibility it cannot deliver.
func TestParityMetadataReportsRevisionAndDirt(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		clean, err := b.drv.Metadata(t.Context(), dir)
		require.NoError(t, err, "Metadata")
		assert.NotEmpty(t, clean.ID, "%s recorded no revision", b.name)
		assert.NotEmpty(t, clean.Short, "%s recorded no short revision", b.name)
		assert.False(t, clean.IsDirty, "%s called a freshly committed tree dirty", b.name)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))
		dirty, err := b.drv.Metadata(t.Context(), dir)
		require.NoError(t, err)
		assert.True(t, dirty.IsDirty, "%s did not report an uncommitted edit", b.name)
	})
}

// A path outside ASCII survives the round trip. git renders one C-quoted
// ("uni/caf\303\251.md") unless core.quotePath is off, and a quoted name matches no
// project glob - so the project owning that file is never rebuilt, with no diagnostic.
//
// This covers DirtyFiles on every backend. The sibling defect in git's ChangedFiles - the
// one probe in the package that omitted the flag - is pinned by
// git_test.go's TestChangedFilesKeepsNonASCIIPathsRaw, because ChangedFiles needs a base
// ref and the per-backend way to produce one does not belong in this table.
func TestParityNonASCIIPathsSurvive(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		writeRepoFile(t, dir, "café.md", "x\n")
		addPath(t, b, dir, "café.md")

		got, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyFiles")
		assert.Contains(t, got, "café.md",
			"%s returned %q; a C-quoted or escaped name matches no glob", b.name, got)
	})
}

// Dirty and DirtyFiles answer the same question, so they cannot disagree. Dirty is defined
// as len(DirtyFiles) > 0 in every backend, and this pins that rather than trusting four
// copies of the same one-liner to stay in step.
func TestParityDirtyAgreesWithDirtyFiles(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})

		wasDirty, err := b.drv.Dirty(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.False(t, wasDirty, "%s called a clean tree dirty", b.name)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))
		nowDirty, err := b.drv.Dirty(t.Context(), dir, nil)
		require.NoError(t, err)
		files, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.True(t, nowDirty, "%s missed an uncommitted edit", b.name)
		assert.Equal(t, len(files) > 0, nowDirty, "%s: Dirty and DirtyFiles disagree", b.name)
	})
}

// TrackedFiles must answer, not fail, when NONE of the given paths are tracked - that is
// the ordinary answer, and the question the capability exists for. `sl files` exits 1 in
// exactly that case where git's ls-files exits 0, so the driver has to absorb it; because
// the call is batched, getting it wrong fails only for some inputs.
func TestParityTrackedFilesAnswersWhenNoneAreTracked(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reporter, ok := b.drv.(types.TrackedFileReporter)
		if !ok {
			t.Skipf("%s does not implement TrackedFileReporter", b.name)
		}
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x\n"), 0o644))

		got, err := reporter.TrackedFiles(t.Context(), dir, []string{"untracked.txt"})
		require.NoError(t, err, "%s failed on a batch containing no tracked path", b.name)
		assert.Empty(t, got)

		mixed, err := reporter.TrackedFiles(t.Context(), dir, []string{"a.txt", "untracked.txt"})
		require.NoError(t, err)
		assert.Equal(t, []string{"a.txt"}, mixed, "%s", b.name)
	})
}

// An ignore reporter echoes the paths it was GIVEN. Expanding a directory argument into
// its contents breaks the set-membership test every caller does against its own input, so
// a directory it asked about is silently reported as not ignored.
func TestParityIgnoredFilesEchoesTheGivenPaths(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reporter, ok := b.drv.(types.IgnoredFileReporter)
		if !ok {
			t.Skipf("%s does not implement IgnoredFileReporter", b.name)
		}
		dir := t.TempDir()
		name, body := ignoreRule(b, "build/")
		b.init(t, dir, map[string]string{"a.txt": "one\n", name: body})
		writeRepoFile(t, dir, "build/out.o", "x\n")

		got, err := reporter.IgnoredFiles(t.Context(), dir, []string{"build", "a.txt"})
		require.NoError(t, err, "IgnoredFiles")
		assert.Equal(t, []string{"build"}, got,
			"%s must echo the given path, not expand it into its contents", b.name)
	})
}

// A backend's two ignore reporters must agree. IgnoredFileReporter.IgnoredFiles and
// ConflictResolver.IgnoredPaths are names one letter apart on the same type answering
// nearly the same question in different shapes - and git's gave OPPOSITE answers for a
// path that is tracked AND matches an ignore rule, because only one passed --no-index.
// Reaching for the wrong one of two near-identical names is not a compile error, so this is
// the only thing that catches it.
func TestParityIgnoreReportersAgree(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reporter, ok := b.drv.(types.IgnoredFileReporter)
		if !ok {
			t.Skipf("%s does not implement IgnoredFileReporter", b.name)
		}
		resolver, ok := b.drv.(types.ConflictResolver)
		if !ok {
			t.Skipf("%s does not implement ConflictResolver", b.name)
		}
		// keep.log is TRACKED and matches an ignore rule - the case the two disagreed on.
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"keep.log": "x\n"})
		name, body := ignoreRule(b, "*.log")
		writeRepoFile(t, dir, name, body)
		addPath(t, b, dir, name)
		commitAll(t, b, dir, "ignore logs")

		files, err := reporter.IgnoredFiles(t.Context(), dir, []string{"keep.log"})
		require.NoError(t, err, "IgnoredFiles")
		paths, err := resolver.IgnoredPaths(t.Context(), dir, []string{"keep.log"})
		require.NoError(t, err, "IgnoredPaths")

		// Both must say TRUE, not merely agree. Agreement alone is satisfied by
		// false == false, which is what two reporters BOTH returning nothing looks like -
		// and hg's and sl's IgnoredPaths swallow a failed probe into an empty map, so
		// deleting their debugignore call entirely would leave an agreement-only assertion
		// green.
		assert.True(t, paths["keep.log"],
			"%s: IgnoredPaths did not report a tracked path covered by an ignore rule", b.name)
		assert.Equal(t, []string{"keep.log"}, files,
			"%s: IgnoredFiles disagrees with IgnoredPaths on the same tracked-and-ignored path", b.name)
	})
}

// ignoreRule returns the ignore file a backend reads and the content expressing pattern in
// its syntax. Mercurial is the odd one: .hgignore patterns are REGULAR EXPRESSIONS unless
// the file opens with a "syntax: glob" line, so a bare "*.log" there is not merely
// ineffective - hg rejects it as an invalid pattern and every subsequent command aborts.
// git, sl and jj all read a .gitignore of plain globs.
func ignoreRule(b parityBackend, pattern string) (name, body string) {
	if b.name == "hg" {
		return ".hgignore", "syntax: glob\n" + pattern + "\n"
	}
	return ".gitignore", pattern + "\n"
}

// AbortMerge refuses when there is no merge to abort. It is reached on failure paths,
// which is exactly when there may be nothing in progress - and sl's implementation is a
// whole-tree revert that exits 0 either way, so without the guard the error path silently
// discards the developer's uncommitted work.
func TestParityAbortMergeRefusesWithNoMergeInProgress(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		starter, ok := b.drv.(types.MergeStarter)
		if !ok {
			t.Skipf("%s does not implement MergeStarter", b.name)
		}
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "one\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("UNCOMMITTED\n"), 0o644))

		assert.Error(t, starter.AbortMerge(t.Context(), dir),
			"%s reported success aborting a merge that was never started", b.name)

		body, err := os.ReadFile(filepath.Join(dir, "a.txt"))
		require.NoError(t, err)
		assert.Equal(t, "UNCOMMITTED\n", string(body),
			"%s destroyed uncommitted work while failing to abort a merge", b.name)
	})
}

// commitAll records every pending change in dir.
func commitAll(t *testing.T, b parityBackend, dir, msg string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "commit", "-am", msg)
	case "hg":
		vcsTestRun(t, dir, "hg", "commit", "-m", msg, "-u", "test")
	case "sl":
		vcsTestRun(t, dir, "sl", "commit", "-m", msg)
	case "jj":
		// jj snapshots the working copy automatically, so `describe` is the commit. It
		// deliberately does NOT run `jj new` afterwards: that would leave @ pointing at a
		// fresh EMPTY change, and a test asking about "the commit I just made" via
		// FindCommit(dir, "") would resolve that empty one instead - passing without ever
		// touching the commit it built.
		vcsTestRun(t, dir, "jj", "describe", "-m", msg)
	}
}

// addPath starts tracking one new file. jj tracks on snapshot, so it needs nothing.
func addPath(t *testing.T, b parityBackend, dir, path string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "add", "--", path)
	case "hg":
		vcsTestRun(t, dir, "hg", "add", path)
	case "sl":
		vcsTestRun(t, dir, "sl", "add", path)
	}
}

// DirtyFiles and DirtyDiff must answer about the SAME change. A gate that names an output
// as drifted and then shows an empty diff sends its reader to reproduce the run to learn
// what the two calls already knew - and it fires in CI, where nobody can look at the tree.
//
// git was the one backend that could disagree: a bare `git diff` is working tree against
// the INDEX, so a STAGED change was reported by DirtyFiles and invisible to DirtyDiff. hg,
// sl and jj have no index and so could not have the bug, which is exactly why only a
// cross-backend test states the rule.
func TestParityDirtyDiffCoversWhatDirtyFilesNames(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"gen.txt": "v1\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gen.txt"), []byte("REGENERATED\n"), 0o644))
		stagePath(t, b, dir, "gen.txt")

		files, err := b.drv.DirtyFiles(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyFiles")
		require.Contains(t, files, "gen.txt", "%s did not report the change at all", b.name)

		diff, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err, "DirtyDiff")
		assert.Contains(t, diff, "gen.txt",
			"%s named gen.txt as dirty but its diff does not mention it", b.name)
	})
}

// stagePath stages a path where the backend has an index to stage into. Only git does; for
// the other three this is a no-op, which is the point - they cannot reach the state that
// made git's two probes disagree.
func stagePath(t *testing.T, b parityBackend, dir, path string) {
	t.Helper()
	if b.name == "git" {
		vcsTestRun(t, dir, "git", "add", "--", path)
	}
}

// A repository with no commits still answers. `git diff HEAD` exits 128 there, so the fix
// for the index skew above had to keep the no-HEAD case working rather than trade one
// failure for another.
func TestParityDirtyDiffOnRepoWithNoCommits(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		if b.name != "git" {
			t.Skip("only git distinguishes an unborn HEAD this way")
		}
		dir := t.TempDir()
		vcsTestRun(t, dir, "git", "init", "-q")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o644))

		_, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		assert.NoError(t, err, "a repository with no commits has nothing to diff, not an error")
	})
}

// A GLOB pathspec matches, on every backend. This is the assertion the suite was missing,
// and its absence hid the worst defect the VCS work produced: magus.diagnoseDrift hands
// DirtyFiles a project's declared output globs verbatim, and an hg pathspec defaults to a
// LITERAL path - so "gen/**" matched nothing, hg wrote "No such file or directory" to
// stderr, exited 0 with empty stdout, and the generate drift gate reported every project
// clean having checked nothing. In CI, with no diagnostic. sl inherited it; git and jj
// handle the glob natively, which is exactly why a per-backend test would not have found it.
func TestParityGlobPathspecMatches(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"gen/x.json": "v1\n"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gen", "x.json"), []byte("DRIFTED\n"), 0o644))

		files, err := b.drv.DirtyFiles(t.Context(), dir, []string{"gen/**"})
		require.NoError(t, err, "DirtyFiles with a glob pathspec")
		assert.Contains(t, files, "gen/x.json",
			"%s returned %q for glob 'gen/**'; an empty answer here is a drift gate that passes blind", b.name, files)

		diff, err := b.drv.DirtyDiff(t.Context(), dir, []string{"gen/**"})
		require.NoError(t, err, "DirtyDiff with a glob pathspec")
		assert.Contains(t, diff, "x.json",
			"%s produced no diff for glob 'gen/**' while naming the file as dirty", b.name)
	})
}

// Every churn reporter names the files a commit touched AND what it did to each. The status
// half is what lets attribution tell a rename from a delete plus an add, and each backend
// reaches it through a different log format - git tags every path, hg and sl group paths by
// what happened to them, jj spells its statuses as words. Only a shared parser reads all
// three, so a backend whose log stops matching that parser reports a commit with NO files:
// no error, no diagnostic, just a churn heatmap that goes quiet. hg and sl shipped exactly
// that when git's format gained the status column.
func TestParityChangesByCommitReportsStatus(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		reporter, ok := b.drv.(types.ChurnReporter)
		if !ok {
			t.Skipf("%s does not implement ChurnReporter", b.name)
		}
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"keep.txt": "one\n", "gone.txt": "two\n"})
		writeRepoFile(t, dir, "keep.txt", "EDITED\n")
		writeRepoFile(t, dir, "new.txt", "three\n")
		addPath(t, b, dir, "new.txt")
		removePath(t, b, dir, "gone.txt")
		commitAll(t, b, dir, "edit one, add one, delete one")

		changes, err := reporter.ChangesByCommit(t.Context(), dir, 1, "")
		require.NoError(t, err, "ChangesByCommit")
		require.Len(t, changes, 1, "%s: the limit must bound the result", b.name)

		got := make(map[string]types.ChangeStatus, len(changes[0].Files))
		for _, f := range changes[0].Files {
			got[f.Path] = f.Status
		}
		assert.Equal(t, map[string]types.ChangeStatus{
			"keep.txt": types.ChangeModified,
			"new.txt":  types.ChangeAdded,
			"gone.txt": types.ChangeDeleted,
		}, got, "%s reported %v", b.name, changes[0].Files)
	})
}

// removePath stops tracking one file. jj snapshots the working copy, so deleting it is the
// whole operation there.
func removePath(t *testing.T, b parityBackend, dir, path string) {
	t.Helper()
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "rm", "-q", "--", path)
	case "hg":
		vcsTestRun(t, dir, "hg", "rm", path)
	case "sl":
		vcsTestRun(t, dir, "sl", "rm", path)
	case "jj":
		require.NoError(t, os.Remove(filepath.Join(dir, filepath.FromSlash(path))))
	}
}

// forceColor writes each backend's "colorize even when not a terminal" setting into the
// repository's own config, which is where a real user's would live.
func forceColor(t *testing.T, b parityBackend, dir string) {
	t.Helper()
	switch b.bin {
	case "git":
		vcsTestRun(t, dir, "git", "config", "color.ui", "always")
	case "hg":
		writeRepoFile(t, dir, ".hg/hgrc", "[ui]\ncolor = always\n")
	case "sl":
		// Sapling's repo config is .sl/config, NOT .sl/hgrc, and slInitRepo has already
		// written a username into it - so this appends. Pointing at the hg path instead
		// makes this helper do nothing, and the subtest then passes without ever forcing
		// color, which is exactly how it first went green against a colorizing Sapling.
		appendRepoFile(t, dir, ".sl/config", "\n[ui]\ncolor = always\n")
	case "jj":
		vcsTestRun(t, dir, "jj", "config", "set", "--repo", "ui.color", "always")
	}
}

// A colorized diff is not a cosmetic problem: the escape sequence lands in FRONT of the
// `diff --git` header, so the header no longer begins a line and every reader of the patch
// misses the file entirely. Measured before the fix, with `color.ui = always` in an ordinary
// gitconfig: `magus diff` listed the untracked files - which magus synthesizes itself, and
// so never colorizes - and silently dropped every tracked modification, at exit 0.
//
// Each backend needs a DIFFERENT switch and they are not interchangeable: NO_COLOR loses to
// git's explicit config, and Sapling ignores HGPLAIN even though Mercurial honors it. That is
// what this test is really pinning - one switch per backend, verified against the real binary
// rather than assumed from the family.
func TestParityDirtyDiffIsNeverColorized(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"f.txt": "a\nb\n"})
		forceColor(t, b, dir)
		writeRepoFile(t, dir, "f.txt", "a\nB\n")

		patch, err := b.drv.DirtyDiff(t.Context(), dir, nil)
		require.NoError(t, err)
		require.NotEmpty(t, patch, "the tracked edit must show up at all")
		assert.NotContains(t, patch, "\x1b[", "an escape sequence hides the header that follows it")
	})
}
