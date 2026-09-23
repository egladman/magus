package vcs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarball builds an in-memory tar stream from name->content entries (a directory entry
// has empty content and a trailing slash in name).
func tarball(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		if content == "" && strings.HasSuffix(name, "/") {
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}))
			continue
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return bytes.NewReader(buf.Bytes())
}

func TestExtractTar(t *testing.T) {
	dst := t.TempDir()
	err := extractTar(tarball(t, map[string]string{
		"magus.yaml":       "version: 1\n",
		"pkg/":             "",
		"pkg/service.buzz": "target build {}\n",
		"docs/readme.md":   "# hi\n",
	}), dst)
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dst, "pkg", "service.buzz"))
	require.NoError(t, err)
	assert.Equal(t, "target build {}\n", string(got))
}

// TestExtractTarRejectsEscape locks in the defense-in-depth guard: a crafted entry whose
// path escapes the destination is refused rather than written outside dst.
func TestExtractTarRejectsEscape(t *testing.T) {
	dst := t.TempDir()
	err := extractTar(tarball(t, map[string]string{"../escape.txt": "pwned"}), dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the destination")
	_, statErr := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt"))
	assert.True(t, os.IsNotExist(statErr), "escaping entry must not be written")
}

// gitEnv is the environment every git subprocess in this file runs under. Beyond fixed
// identities for reproducible commits, it neuters the developer's own git config: a global
// core.hooksPath (husky and pre-commit both install one) would otherwise run their hooks
// inside these fixtures, and a hardened protocol.file.allow would break the file:// clones
// outright. GIT_TERMINAL_PROMPT=0 keeps a misconfigured fixture from hanging on a prompt.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
}

// gitRun runs one git command in dir and fails the test if it does not succeed.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

// gitInitRepo makes a throwaway repo at dir with files committed. Skips if git is absent.
func gitInitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) { gitRun(t, dir, args...) }
	run("init", "-q")
	// Nearly every mutating git command spawns `git maintenance run --auto --quiet
	// --detach`, which outlives its parent by design and races t.TempDir cleanup into
	// "unlinkat .git: directory not empty" under load. Repo config is the only lever that
	// also reaches the git subprocesses production code spawns with its own environment.
	// gitCloneShallow sets the same pair, because a clone does not inherit these.
	run("config", "maintenance.auto", "false")
	run("config", "gc.auto", "0")
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
}

// TestExportRevision exercises the real git-archive -> tar -> temp-tree path, including
// the subdir re-rooting (a workspace root nested below the git root).
func TestExportRevision(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{
		"magus.yaml":       "version: 1\n",
		"pkg/service.buzz": "target build {}\n",
		"sub/proj/app.txt": "nested\n",
	})
	ctx := context.Background()

	// From the git root: the whole tree is exported, re-rooted at repo.
	dst := t.TempDir()
	require.NoError(t, gitVCS{}.ExportRevision(ctx, repo, "HEAD", dst))
	got, err := os.ReadFile(filepath.Join(dst, "pkg", "service.buzz"))
	require.NoError(t, err)
	assert.Equal(t, "target build {}\n", string(got))

	// From a subdir: only that subtree is exported, re-rooted so the subdir's own files
	// sit at the destination top level (app.txt, not sub/proj/app.txt).
	sub := filepath.Join(repo, "sub", "proj")
	dstSub := t.TempDir()
	require.NoError(t, gitVCS{}.ExportRevision(ctx, sub, "HEAD", dstSub))
	got, err = os.ReadFile(filepath.Join(dstSub, "app.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nested\n", string(got))
	_, statErr := os.Stat(filepath.Join(dstSub, "magus.yaml"))
	assert.True(t, os.IsNotExist(statErr), "subdir export must not include repo-root files")
}

// TestExportRevisionBadRev reports a clear error (not a panic or hang) for an unknown rev.
func TestExportRevisionBadRev(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	err := gitVCS{}.ExportRevision(context.Background(), repo, "no-such-rev", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-rev")
}

func TestWriteManagedHookNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-checkout")
	body := gitHookBody("post-checkout", "magus server sync")
	changed, err := writeManagedSection(path, refreshMarkers, body, hookFile)
	require.NoError(t, err)
	assert.True(t, changed)

	assert.Equal(t, "[ \"$3\" = \"1\" ] || exit 0\nmagus server sync >/dev/null 2>&1 || true\n", body,
		"post-checkout guards on the branch-checkout flag")
	assertFile(t, path, "#!/bin/sh\n\n"+refreshMarkers.section(body), 0o755)
}

func TestWriteManagedHookIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-merge")
	_, err := writeManagedSection(path, refreshMarkers, gitHookBody("post-merge", "magus server sync"), hookFile)
	require.NoError(t, err)

	changed, err := writeManagedSection(path, refreshMarkers, gitHookBody("post-merge", "magus server sync"), hookFile)
	require.NoError(t, err)
	assert.False(t, changed, "re-installing an unchanged section is a no-op")
}

func TestWriteManagedHookPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-rewrite")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho 'my own hook'\n"), 0o755))

	body := gitHookBody("post-rewrite", "magus server sync")
	changed, err := writeManagedSection(path, refreshMarkers, body, hookFile)
	require.NoError(t, err)
	assert.True(t, changed)
	assertFile(t, path, "#!/bin/sh\necho 'my own hook'\n\n"+refreshMarkers.section(body), 0o755)
}

// TestInstallDriftHookInstallsBoth pins that both post-commit and pre-push get the
// managed section, that it is idempotent, and that a fail-open one-liner is what got
// written: no shell logic beyond the command and its `|| true`.
func TestInstallDriftHookInstallsBoth(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})

	installed, err := gitVCS{}.InstallDriftHook(t.Context(), dir, "magus job run check-drift")
	require.NoError(t, err)
	assert.Equal(t, []string{"post-commit", "pre-push"}, installed)

	for _, name := range gitDriftHooks {
		assertFile(t, filepath.Join(dir, ".git", "hooks", name),
			"#!/bin/sh\n\n"+driftMarkers.section("magus job run check-drift >/dev/null 2>&1 || true\n"), 0o755)
	}

	again, err := gitVCS{}.InstallDriftHook(t.Context(), dir, "magus job run check-drift")
	require.NoError(t, err)
	assert.Empty(t, again, "re-installing an unchanged drift hook reports no install")
}

// TestInstallDriftHookCoexistsWithRefreshHook pins that the two managed sections, and a
// hand-written body, share one hook file without overwriting each other. Today's hook
// sets do not overlap, so the shared file is staged by hand.
func TestInstallDriftHookCoexistsWithRefreshHook(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})

	_, err := gitVCS{}.InstallRefreshHook(t.Context(), dir, "magus job run sync-graph")
	require.NoError(t, err)
	_, err = gitVCS{}.InstallDriftHook(t.Context(), dir, "magus job run check-drift")
	require.NoError(t, err)

	postCommit := filepath.Join(dir, ".git", "hooks", "post-commit")
	drift := driftMarkers.section("magus job run check-drift >/dev/null 2>&1 || true\n")
	assertFile(t, postCommit, "#!/bin/sh\n\n"+drift, 0o755)

	refresh := refreshMarkers.section("magus job run sync-graph >/dev/null 2>&1 || true\n")
	_, err = writeManagedSection(postCommit, refreshMarkers, "magus job run sync-graph >/dev/null 2>&1 || true\n", hookFile)
	require.NoError(t, err)
	_, err = gitVCS{}.InstallDriftHook(t.Context(), dir, "magus job run check-drift")
	require.NoError(t, err)
	assertFile(t, postCommit, "#!/bin/sh\n\n"+drift+"\n"+refresh, 0o755)
}

// Outside any repository there is nothing to hook, which the installer contract calls a
// quiet no-op rather than a failure.
func TestInstallGitHooksOutsideARepositoryInstallNothing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	installed, err := gitVCS{}.InstallRefreshHook(t.Context(), dir, "magus job run sync-graph")
	require.NoError(t, err)
	assert.Nil(t, installed)
	assert.NoDirExists(t, filepath.Join(dir, ".git"))

	err = gitVCS{}.InstallMergeDriver(t.Context(), dir, []string{"gen/**"})
	require.EqualError(t, err, "vcs: install merge driver: "+dir+" is not in a git repository")
	assert.NoFileExists(t, filepath.Join(dir, ".gitattributes"))
}

// A cancelled install is an error, never the "not a repository, nothing to do" answer.
func TestInstallGitHooksReportsCancellation(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	installed, err := gitVCS{}.InstallDriftHook(ctx, dir, "magus job run check-drift")
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, installed)
}

// Every worktree of a repository shares one lock, in the common dir, so no lock file
// appears in a worktree where it would show as untracked.
func TestGitRepoPathsOfLinkedWorktree(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, dir, "worktree", "add", "-q", linked)

	paths, ok, err := gitRepoPathsOf(t.Context(), linked)
	require.NoError(t, err)
	require.True(t, ok)
	common, err := filepath.EvalSymlinks(paths.commonDir)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	require.NoError(t, err)
	assert.Equal(t, want, common)

	_, err = gitVCS{}.InstallDriftHook(t.Context(), linked, "magus job run check-drift")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(want, managedLockName))
	assert.NoFileExists(t, filepath.Join(linked, managedLockName))
}

// TestGitVCSChecksoutsFindsTheRootBehindABareRepoRedirect covers a bare-repo-backed
// primary: root/.git is a FILE redirecting to root/.bare rather than a directory named
// ".git", so matching the common dir's basename against ".git" never recognized it, and
// it carries no worktrees/ admin entry of its own either since `worktree add` was never
// run against it. From a sibling worktree, Checkouts must still find it.
func TestGitVCSChecksoutsFindsTheRootBehindABareRepoRedirect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	bare := filepath.Join(root, ".bare")
	cmd := exec.Command("git", "init", "-q", "--bare", bare)
	cmd.Env = gitEnv()
	require.NoError(t, cmd.Run())
	// core.bare stays true from init; flip it so git accepts root, reached through the
	// plain .git redirect below, as this repo's work tree instead of refusing every
	// command with "this operation must be run in a work tree".
	gitRun(t, bare, "config", "core.bare", "false")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ./.bare\n"), 0o644))

	run := func(args ...string) { gitRun(t, root, args...) }
	run("config", "maintenance.auto", "false")
	run("commit", "-q", "--allow-empty", "-m", "init")
	run("branch", "-q", "-M", "main")

	linked := filepath.Join(t.TempDir(), "linked")
	run("worktree", "add", "-q", "-b", "feature", linked)

	_, others, err := Checkouts(linked)
	require.NoError(t, err)
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.Equal(t, []string{realRoot}, others)
}

// TestCommitPushed covers the three answers CommitPushed can give: not pushed (ahead of
// upstream), pushed (upstream contains it), and unknown (no upstream configured at all).
func TestCommitPushed(t *testing.T) {
	remote := t.TempDir()
	gitRun(t, remote, "init", "-q", "--bare")

	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	gitRun(t, dir, "remote", "add", "origin", remote)
	gitRun(t, dir, "push", "-q", "-u", "origin", "HEAD")

	pushedSHA := gitRun2(t, dir, "rev-parse", "HEAD")

	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "second")
	unpushedSHA := gitRun2(t, dir, "rev-parse", "HEAD")

	pushed, ok, err := gitVCS{}.CommitPushed(t.Context(), dir, pushedSHA)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, pushed, "the pushed commit must report pushed")

	pushed, ok, err = gitVCS{}.CommitPushed(t.Context(), dir, unpushedSHA)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.False(t, pushed, "the local-only commit must report not pushed")

	noUpstream := t.TempDir()
	gitInitRepo(t, noUpstream, map[string]string{"a.txt": "a\n"})
	sha := gitRun2(t, noUpstream, "rev-parse", "HEAD")
	_, ok, err = gitVCS{}.CommitPushed(t.Context(), noUpstream, sha)
	require.NoError(t, err)
	assert.False(t, ok, "no upstream configured means unknown, not a guess")
}

// TestCommitPushedRefusesToAnswerFromAnAbsentHistory pins the shallow-clone case, where
// merge-base exits 128 rather than 1: the history that would decide is not in the object
// store. Reading that as merge-base's "not an ancestor" answer is what makes the drift
// notice offer --amend on a commit the remote already carries.
func TestCommitPushedRefusesToAnswerFromAnAbsentHistory(t *testing.T) {
	remote := t.TempDir()
	gitRun(t, remote, "init", "-q", "--bare")

	origin := t.TempDir()
	gitInitRepo(t, origin, map[string]string{"a.txt": "a\n"})
	firstSHA := gitRun2(t, origin, "rev-parse", "HEAD")
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "second")
	gitRun(t, origin, "remote", "add", "origin", remote)
	gitRun(t, origin, "push", "-q", "-u", "origin", "HEAD")

	shallow := t.TempDir()
	gitRun(t, shallow, "clone", "-q", "--depth", "1", "file://"+remote, ".")

	pushed, ok, err := gitVCS{}.CommitPushed(t.Context(), shallow, firstSHA)
	require.Error(t, err, "a history that cannot decide must surface as an error, never as an answer")
	assert.False(t, ok)
	assert.False(t, pushed)
}

// gitRun2 is gitRun for the one case that needs the command's stdout back.
func gitRun2(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	require.NoError(t, err, "git %s", strings.Join(args, " "))
	return strings.TrimSpace(string(out))
}

// TestDriftHookBodyNeverBlocksAndNeedsNoDaemon runs the installed hook scripts directly,
// with a command that cannot possibly succeed and no daemon anywhere nearby, and pins
// that both still exit 0. This is the whole safety contract types.DriftHookInstaller
// promises: whatever the command does, the hook itself never fails a commit or a push.
func TestDriftHookBodyNeverBlocksAndNeedsNoDaemon(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})

	_, err := gitVCS{}.InstallDriftHook(t.Context(), dir, "definitely-not-a-real-command-xyz")
	require.NoError(t, err)

	for _, name := range gitDriftHooks {
		hookPath := filepath.Join(dir, ".git", "hooks", name)
		cmd := exec.Command("sh", hookPath)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		assert.NoError(t, err, "%s must exit 0 even when its command fails: %s", name, out)
	}
}

// TestParseChangesByCommit verifies the NUL-delimited `git log -M --name-status` parse:
// a NUL line opens a commit (hash, author, date); following non-empty lines are one
// status-prefixed entry each.
func TestParseChangesByCommit(t *testing.T) {
	out := "\x00abc123\x00Ada\x002026-06-20T10:00:00Z\n\nM\tapi/main.go\nA\tapi/util.go\n" +
		"\x00def456\x00Babbage\x002026-06-19T09:00:00Z\n\nD\tweb/app.ts\n"

	got := parseChangesByCommit(out)
	require.Len(t, got, 2)

	assert.Equal(t, "abc123", got[0].ID)
	assert.Equal(t, "Ada", got[0].Author)
	assert.Equal(t, time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), got[0].Date.UTC())
	assert.Equal(t, []types.FileChange{
		{Path: "api/main.go", Status: types.ChangeModified},
		{Path: "api/util.go", Status: types.ChangeAdded},
	}, got[0].Files)

	assert.Equal(t, "def456", got[1].ID)
	assert.Equal(t, "Babbage", got[1].Author)
	assert.Equal(t, []types.FileChange{{Path: "web/app.ts", Status: types.ChangeDeleted}}, got[1].Files)
}

// TestParseChangesByCommitRename is the case -M exists for: a rename arrives as ONE entry
// carrying both names, so churn can follow the file instead of splitting across the two paths.
// A copy is deliberately NOT lineage (both files survive it, so crediting the new path with the
// old one's history would attribute edits it never received); it is recorded as a plain add.
func TestParseChangesByCommitRename(t *testing.T) {
	out := "\x00abc123\x00Ada\x002026-06-20T10:00:00Z\n\n" +
		"R096\tinternal/old.go\tinternal/new.go\n" +
		"C075\tinternal/new.go\tinternal/copy.go\n"

	got := parseChangesByCommit(out)
	require.Len(t, got, 1)
	require.Len(t, got[0].Files, 2)

	assert.Equal(t, types.FileChange{
		Path: "internal/new.go", PrevPath: "internal/old.go", Status: types.ChangeRenamed,
	}, got[0].Files[0], "a rename carries both names")
	assert.Equal(t, types.FileChange{
		Path: "internal/copy.go", Status: types.ChangeAdded,
	}, got[0].Files[1], "a copy is an add, with no lineage back to its source")
}

// TestParseChangesByCommitMalformed pins that an unreadable entry is SKIPPED rather
// than guessed at: a rename line missing its second path has no usable destination,
// and inventing one would attribute churn to a file that does not exist.
func TestParseChangesByCommitMalformed(t *testing.T) {
	out := "\x00abc123\x00Ada\x002026-06-20T10:00:00Z\n\nR100\tonly/one/path.go\nM\tgood.go\n"

	got := parseChangesByCommit(out)
	require.Len(t, got, 1)
	assert.Equal(t, []types.FileChange{{Path: "good.go", Status: types.ChangeModified}}, got[0].Files)
}

// A "?" is the letter a non-git driver emits for a status IT could not translate (see
// jjChurnTemplate). Skipping it is the whole point: recorded as an edit (which the default
// branch would have done), the driver's uncertainty reads back as a fact about the file, and
// churn attributes work to a path that may not have changed at all.
func TestParseChangesByCommitSkipsAnUntranslatedStatus(t *testing.T) {
	out := "\x00abc123\x00Ada\x002026-06-20T10:00:00Z\n\n?\tmystery.go\t mystery.go\nM\tgood.go\n"

	got := parseChangesByCommit(out)
	require.Len(t, got, 1)
	assert.Equal(t, []types.FileChange{{Path: "good.go", Status: types.ChangeModified}}, got[0].Files)
}

// TestParseChangesByCommitEmpty covers a commit that touched no files and a bad date.
func TestParseChangesByCommitEmpty(t *testing.T) {
	got := parseChangesByCommit("\x00abc123\x00Ada\x00not-a-date\n\n")
	require.Len(t, got, 1)
	assert.Equal(t, "abc123", got[0].ID)
	assert.True(t, got[0].Date.IsZero(), "unparsable date is zero, not an error")
	assert.Empty(t, got[0].Files)
}

// gitDivergedOrigin builds an origin repository with `trunk` commits of shared history, a
// branch "feat" cut from its tip carrying one commit of its own, and three further commits
// on "main" past the branch point. It returns the repository path and the number of
// commits reachable from main, which the fixture knows exactly because it made them.
//
// Returning the count matters more than it looks. A caller that wants "the full history"
// would otherwise shell out to `git rev-list --count HEAD`, walking every object in the
// repository to re-measure a number this function already determined, and that walk is
// the most object-hungry thing in these tests. It failed once in CI with
//
//	error: Could not read <sha>
//	fatal: Failed to traverse parents of commit <sha>
//
// which is the signature of a loose object file that was written and then could not be
// read back (reproduced exactly by deleting one file from .git/objects). Nothing in the
// test touches the origin between building it and counting it, the shallow clone provably
// leaves the origin's .git unchanged, and a redirected GIT_OBJECT_DIRECTORY reports
// different errors. The surviving explanation is the runner losing a write under load, on the
// shard this repo has already measured at 7.5GB+6.1GB on a 16GB box. No assertion can make
// that correct; not depending on those objects is the available fix.
//
// The trunk is what makes a bounded clone measurable. With a short shared history, any
// fetch deep enough to reach the branch point also reaches the root, so the repository
// stops being shallow and a test cannot tell a bounded recovery from `git fetch
// --unshallow`. Keep `trunk` well above the ladder's first rung.
func gitDivergedOrigin(t *testing.T, trunk int) (string, int) {
	t.Helper()
	origin := t.TempDir()
	gitInitRepo(t, origin, map[string]string{"magus.yaml": "version: 1\n"})
	gitRun(t, origin, "branch", "-M", "main")
	commit := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(origin, name), []byte(name), 0o644))
		gitRun(t, origin, "add", "-A")
		gitRun(t, origin, "commit", "-q", "-m", name)
	}
	for i := range trunk {
		commit(fmt.Sprintf("trunk-%d.txt", i))
	}

	gitRun(t, origin, "checkout", "-q", "-b", "feat")
	commit("app.txt")

	// main moves on past the branch point, so the merge base is neither branch's tip and
	// the diff has post-branch-point commits it must exclude.
	gitRun(t, origin, "checkout", "-q", "main")
	const pastBranchPoint = 3
	for i := range pastBranchPoint {
		commit(fmt.Sprintf("main-%d.txt", i))
	}
	// gitInitRepo's own commit, the trunk, then the three above. "feat" is not on main.
	return origin, 1 + trunk + pastBranchPoint
}

// gitCloneShallow clones branch "feat" from origin at depth, single-branch: the shape a CI
// checkout with a bounded fetch-depth produces. The result holds neither origin/main nor
// the commit the two branches share, so `git merge-base origin/main HEAD` fails outright.
// depth 0 clones the full history instead, still single-branch.
func gitCloneShallow(t *testing.T, origin string, depth int) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "checkout")
	// The detached-maintenance kill switch from gitInitRepo, set at birth: the deepen
	// fetches the tests trigger would otherwise each leave one behind in the checkout.
	args := []string{"clone", "--quiet", "--single-branch", "--branch", "feat",
		"--config", "maintenance.auto=false", "--config", "gc.auto=0"}
	if depth > 0 {
		args = append(args, fmt.Sprintf("--depth=%d", depth))
	}
	cmd := exec.Command("git", append(args, "file://"+origin, clone)...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "clone: %s", out)

	wantShallow := strconv.FormatBool(depth > 0)
	require.Equal(t, wantShallow, gitOutput(t, clone, "rev-parse", "--is-shallow-repository"),
		"the clone must start in the state the test is about, or it proves nothing")
	return clone
}

// gitOutput returns the trimmed stdout of one git command in dir.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), exit.Stderr)
	}
	require.NoError(t, err, "git %s", strings.Join(args, " "))
	return strings.TrimSpace(string(out))
}

// gitCommitCount is how much history the checkout actually holds, the quantity every
// depth assertion below is about.
func gitCommitCount(t *testing.T, dir string) int {
	t.Helper()
	n, err := strconv.Atoi(gitOutput(t, dir, "rev-list", "--count", "HEAD"))
	require.NoError(t, err)
	return n
}

// TestDiffRecoversMergeBaseInShallowClone is the regression for the silent full build:
// before Diff recovered, a shallow CI checkout made `git merge-base` fail, which affected
// reports as MGS1010 and answers by selecting every project. The changed files must come
// back from a clone that never had the merge base to begin with; the recovery must stay
// bounded rather than quietly turning into `git fetch --unshallow`.
func TestDiffRecoversMergeBaseInShallowClone(t *testing.T) {
	origin, full := gitDivergedOrigin(t, 40)
	clone := gitCloneShallow(t, origin, 1)

	// An uncommitted edit too, so the recovered merge base is exercised against the work
	// tree the same way a real `magus affected` run sees it.
	require.NoError(t, os.WriteFile(filepath.Join(clone, "dirty.txt"), []byte("uncommitted\n"), 0o644))

	files, err := gitVCS{}.ChangedFiles(t.Context(), clone, "origin/main")
	require.NoError(t, err)
	assert.Contains(t, files, "app.txt", "the branch's committed change")
	assert.Contains(t, files, "dirty.txt", "an untracked working-tree file")
	assert.NotContains(t, files, "main-0.txt", "commits that landed on main after the branch point stay out")

	// Bounded, not unshallowed: the ladder stops as soon as the ancestor is reachable. If
	// this ever holds the whole history, the recovery has degenerated into `git fetch
	// --unshallow` and is charging exactly the cost it exists to avoid.
	assert.Equal(t, "true", gitOutput(t, clone, "rev-parse", "--is-shallow-repository"),
		"recovery must leave the clone shallow")
	assert.Less(t, gitCommitCount(t, clone), full,
		"recovery must fetch less than the full history")
}

// TestRecoverMergeBaseNeverShortens is the regression for a data-destructive bug: `git
// fetch --depth=N` is absolute in BOTH directions, so a ladder built on --depth truncated
// any checkout deeper than its first rung, and a later rung failing left the repository
// holding less history than it was cloned with.
func TestRecoverMergeBaseNeverShortens(t *testing.T) {
	origin, _ := gitDivergedOrigin(t, 40)
	const depth = 36 // deeper than the ladder's first rung (32), shallower than the divergence
	clone := gitCloneShallow(t, origin, depth)
	before := gitCommitCount(t, clone)
	require.Greater(t, before, 32, "the clone must start deeper than the first rung")

	gitVCS{}.recoverMergeBase(t.Context(), clone, "origin/main")

	assert.GreaterOrEqual(t, gitCommitCount(t, clone), before,
		"recovery must only ever add history, never truncate what the checkout arrived with")
}

// TestRecoverMergeBaseSkipsFullClone locks in the guard. The clone here has a working
// origin remote and a reachable base branch, so the fetch WOULD succeed and WOULD return a
// merge base: the empty result is the guard refusing, and deleting the guard fails this
// test. A full clone that cannot find a merge base has a bad ref, not missing history, and
// no read-only query should fetch into it.
func TestRecoverMergeBaseSkipsFullClone(t *testing.T) {
	origin, _ := gitDivergedOrigin(t, 3)
	clone := gitCloneShallow(t, origin, 0)
	require.Empty(t, gitOutput(t, clone, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/main"),
		"single-branch clone must lack the base ref, so merge-base fails for a reason recovery could fix")

	assert.Empty(t, gitVCS{}.recoverMergeBase(t.Context(), clone, "origin/main"))
	assert.Empty(t, gitOutput(t, clone, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/main"),
		"the guard must return before any ref is fetched")
}

// TestRecoverMergeBaseUnusableBase covers the two bases with nothing to fetch from: one with
// no remote segment at all, and one naming a remote this repository does not have. Neither may
// be guessed at, because the segment reaches `git fetch` as a repository argument: a URL sink.
func TestRecoverMergeBaseUnusableBase(t *testing.T) {
	shortOrigin, _ := gitDivergedOrigin(t, 3)
	clone := gitCloneShallow(t, shortOrigin, 1)
	assert.Empty(t, gitVCS{}.recoverMergeBase(t.Context(), clone, "deadbeef"))
	assert.Empty(t, gitVCS{}.recoverMergeBase(t.Context(), clone, "refs/remotes/origin/main"))
}

// TestTrackedFiles covers the primitive MGS1019 rests on: telling a committed file from a
// build product. Neither Dirty nor DirtyFiles can answer it, because an ignored file and a
// clean tracked file both report nothing dirty.
func TestTrackedFiles(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{
		"tracked.html": "<small>built</small>\n",
		".gitignore":   "gen/\n",
	})
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "gen"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "gen", "page.html"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.html"), []byte("x"), 0o644))

	got, err := gitVCS{}.TrackedFiles(context.Background(), repo,
		[]string{"tracked.html", "gen/page.html", "untracked.html", "absent.html"})
	require.NoError(t, err)
	assert.Equal(t, []string{"tracked.html"}, got,
		"only the committed path; ignored, untracked, and missing are all absent")

	t.Run("no paths asks nothing", func(t *testing.T) {
		got, err := gitVCS{}.TrackedFiles(context.Background(), repo, nil)
		require.NoError(t, err)
		assert.Empty(t, got, "an empty request must not list the whole repository")
	})

	t.Run("batches beyond the argv cap", func(t *testing.T) {
		paths := make([]string, 0, gitTrackedBatch*2+5)
		for i := range cap(paths) {
			paths = append(paths, fmt.Sprintf("filler-%d.txt", i))
		}
		paths = append(paths, "tracked.html")
		got, err := gitVCS{}.TrackedFiles(context.Background(), repo, paths)
		require.NoError(t, err)
		assert.Equal(t, []string{"tracked.html"}, got, "a path in the last batch is still found")
	})
}

// TestGitEnvironStripsRedirectsAndKeepsTransport pins the split gitEnviron is built on: the
// GIT_* prefix covers two unrelated categories, and only the repository-selecting one is
// removed. The "keeps" half is the load-bearing one: a blanket prefix strip would pass a
// test that only checked the "strips" half, then break fetch authentication in the field.
func TestGitEnvironStripsRedirectsAndKeepsTransport(t *testing.T) {
	strip := map[string]string{
		"GIT_DIR":        "/elsewhere/.git",
		"GIT_WORK_TREE":  "/elsewhere",
		"GIT_INDEX_FILE": "/elsewhere/.git/index",
		"GIT_NAMESPACE":  "refs/namespaces/x",
	}
	keep := map[string]string{
		"GIT_SSH_COMMAND":     "ssh -i /key",
		"GIT_ASKPASS":         "/usr/bin/askpass",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_AUTHOR_NAME":     "t",
		"GIT_TRACE":           "1",
	}
	for name, value := range strip {
		t.Setenv(name, value)
	}
	for name, value := range keep {
		t.Setenv(name, value)
	}

	got := map[string]string{}
	for _, kv := range gitEnviron() {
		name, value, _ := strings.Cut(kv, "=")
		got[name] = value
	}

	for name := range strip {
		assert.NotContains(t, got, name, "%s selects a repository and must be removed", name)
	}
	for name, value := range keep {
		assert.Equal(t, value, got[name], "%s governs how git works, not where, and must survive", name)
	}
}

// mergeRepo builds a repo whose branch `other` and HEAD both changed the same file, so a
// merge of `other` conflicts. Returns the repo dir.
func mergeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v\n%s", err, out)
	}
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	write := func(body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gen.txt"), []byte(body), 0o644))
	}
	write("base\n")
	git("add", "gen.txt")
	git("commit", "-m", "base")
	git("checkout", "-b", "other")
	write("theirs\n")
	git("commit", "-am", "theirs")
	git("checkout", "main")
	write("ours\n")
	git("commit", "-am", "ours")
	return dir
}

// TestStartMergeReportsConflictsRatherThanFailing pins the contract vcs resolve --against
// depends on: a merge that CONFLICTS has still started, and the conflicts are the payload.
// Treating git's non-zero exit as failure would refuse exactly the case this exists for.
func TestStartMergeReportsConflictsRatherThanFailing(t *testing.T) {
	dir := mergeRepo(t)
	ctx := context.Background()

	require.NoError(t, gitVCS{}.StartMerge(ctx, dir, "other"),
		"a conflicting merge has begun; the conflicts are the result, not an error")

	conflicts, err := gitVCS{}.Conflicts(ctx, dir)
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "gen.txt", conflicts[0].Path)

	// AbortMerge restores the pre-merge tree, which is what makes --dry-run honest.
	require.NoError(t, gitVCS{}.AbortMerge(ctx, dir))
	after, err := gitVCS{}.Conflicts(ctx, dir)
	require.NoError(t, err)
	assert.Empty(t, after, "aborting leaves nothing in progress")
	body, err := os.ReadFile(filepath.Join(dir, "gen.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ours\n", string(body), "the pre-merge content is back")
}

// TestStartMergeRejectsFlagLikeRef guards argument injection: `git merge` has no `--`
// separator for its ref, so a ref beginning with "-" would be read as a flag.
func TestStartMergeRejectsFlagLikeRef(t *testing.T) {
	err := gitVCS{}.StartMerge(context.Background(), t.TempDir(), "--exec=touch pwned")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "looks like a flag")
}

// TestStartMergeFailsOnUnknownRef proves a merge that never began IS an error, so
// --against cannot silently proceed to "no conflicts" on a typo'd ref.
func TestStartMergeFailsOnUnknownRef(t *testing.T) {
	err := gitVCS{}.StartMerge(context.Background(), mergeRepo(t), "no-such-branch")

	require.Error(t, err)
}

// TestGitStatusPaths pins the porcelain parse, which lives beside the driver that produces
// those lines. It is a unit table rather than a live-git test because the shapes it covers
// (a rename, a C-quoted name, both status columns) are awkward to provoke on demand and easy
// to state exactly.
func TestGitStatusPaths(t *testing.T) {
	for name, tc := range map[string]struct {
		lines []string
		want  []string
	}{
		"modified":     {[]string{" M cmd/magus/agent.go"}, []string{"cmd/magus/agent.go"}},
		"staged add":   {[]string{"A  docs/new.md"}, []string{"docs/new.md"}},
		"untracked":    {[]string{"?? scratch.txt"}, []string{"scratch.txt"}},
		"both columns": {[]string{"MM internal/agent/catalog.go"}, []string{"internal/agent/catalog.go"}},
		"several":      {[]string{" M a.go", "?? b.go"}, []string{"a.go", "b.go"}},
		"clean tree":   {nil, []string{}},

		// A rename must name the NEW path; the old one no longer exists on disk.
		"rename keeps the new name": {[]string{"R  old/path.go -> new/path.go"}, []string{"new/path.go"}},

		// core.quotePath=false stops the escaping of non-ASCII bytes and nothing else: a
		// name carrying a double quote still arrives quoted and escaped.
		"quoted name is unquoted":  {[]string{` M "we\"ird.txt"`}, []string{`we"ird.txt`}},
		"quoted name with a space": {[]string{` M "docs/a file.md"`}, []string{"docs/a file.md"}},

		// strconv.Unquote also accepts Go raw-string and rune literals, so the unquoting is
		// gated on git's own form: a file literally named `x` must keep its backquotes.
		"backquoted name is left alone": {[]string{" M `x`"}, []string{"`x`"}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, gitStatusPaths(tc.lines))
		})
	}
}

// TestTagsResolvesAnnotatedTagsToTheirCommit pins the %(*objectname) deref.
//
// An ANNOTATED tag's %(objectname) is the tag OBJECT's id, not the commit it points at,
// while a lightweight tag's is the commit. types.VCSTag.ID promises "the revision
// identifier the tag resolves to", so recording objectname made every annotated tag (the kind
// `git tag -a` and most release tooling creates) report an id matching no commit. A caller
// asking "is this release tagged at HEAD?" got no match for exactly the tags a release
// process creates.
func TestTagsResolvesAnnotatedTagsToTheirCommit(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.txt": "one\n"})
	gitRun(t, repo, "tag", "-a", "v1.0.0", "-m", "annotated")
	gitRun(t, repo, "tag", "v1.0.1")

	head, err := vcsOutput(t.Context(), repo, "git", "rev-parse", "HEAD")
	require.NoError(t, err)

	tags, err := gitVCS{}.Tags(t.Context(), repo, "")
	require.NoError(t, err, "Tags")
	require.Len(t, tags, 2)
	for _, tag := range tags {
		assert.Equal(t, head, tag.ID,
			"%s resolves to %s, not the commit it marks", tag.Name, tag.ID)
	}
}

// TestTagsNamesATagSharedWithABranch pins %(refname:lstrip=2) over %(refname:short), which
// abbreviates refs/tags/v1.0.0 to "tags/v1.0.0" when a branch shares the name and so hides
// the tag from every caller asking whether HEAD carries a release.
func TestTagsNamesATagSharedWithABranch(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.txt": "one\n"})
	gitRun(t, repo, "tag", "v1.0.0")
	gitRun(t, repo, "branch", "v1.0.0")

	tags, err := gitVCS{}.Tags(t.Context(), repo, "v*")
	require.NoError(t, err, "Tags")
	require.Len(t, tags, 1)
	assert.Equal(t, "v1.0.0", tags[0].Name)
	assert.Empty(t, tags[0].Prefix, "a root tag carries no module prefix")
}

// TestTagsSeesLooseAndPackedRefs covers both ref storages in one repository, since a release
// cut today is a loose file while the repository's history is packed, and pins that a
// wildcard still stops at "/".
func TestTagsSeesLooseAndPackedRefs(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.txt": "one\n"})
	gitRun(t, repo, "tag", "v1.0.0")
	gitRun(t, repo, "tag", "libs/diagnostics/v0.1.0")
	gitRun(t, repo, "pack-refs", "--all")
	gitRun(t, repo, "tag", "v1.1.0")

	all, err := gitVCS{}.Tags(t.Context(), repo, "")
	require.NoError(t, err, "Tags")
	names := make([]string, 0, len(all))
	for _, tag := range all {
		names = append(names, tag.Name)
	}
	assert.ElementsMatch(t, []string{"v1.0.0", "libs/diagnostics/v0.1.0", "v1.1.0"}, names)

	roots, err := gitVCS{}.Tags(t.Context(), repo, "v*")
	require.NoError(t, err, "Tags")
	rootNames := make([]string, 0, len(roots))
	for _, tag := range roots {
		rootNames = append(rootNames, tag.Name)
	}
	assert.ElementsMatch(t, []string{"v1.0.0", "v1.1.0"}, rootNames,
		"a wildcard stops at / so the namespaced tag stays out")
}

// TestChangedFilesKeepsNonASCIIPathsRaw pins core.quotePath=false on BOTH of ChangedFiles'
// probes. git otherwise renders a path outside ASCII as a C-quoted, backslash-escaped
// literal ("uni/caf\303\251.md"), and project.normalizeFiles only trims and slash-converts,
// so the quoted string matches no source glob and the project owning that file is silently
// never rebuilt. No diagnostic, no error; `magus affected` just under-builds forever.
//
// Both probes are covered: the tracked path goes through `git diff`, the untracked one
// through `git ls-files --others`, and the flag has to be on each of them.
func TestChangedFilesKeepsNonASCIIPathsRaw(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.txt": "one\n", "uni/café.md": "x\n"})
	base, err := vcsOutput(t.Context(), repo, "git", "rev-parse", "HEAD")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(repo, "uni", "café.md"), []byte("changed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "uni", "naïve.md"), []byte("new\n"), 0o644))
	gitRun(t, repo, "commit", "-q", "-am", "edit the tracked one")

	got, err := gitVCS{}.ChangedFiles(t.Context(), repo, base)
	require.NoError(t, err, "ChangedFiles")
	assert.Contains(t, got, "uni/café.md", "tracked non-ASCII path came back quoted: %q", got)
	assert.Contains(t, got, "uni/naïve.md", "untracked non-ASCII path came back quoted: %q", got)
}

// The switch is per-backend and the wrong one is silently useless, so each is pinned here
// rather than left to the parity suite alone: that suite skips a backend whose binary is
// absent, which is most CI machines for three of these four.
//
// git is the odd one out on purpose: it has no global --color flag (only a per-subcommand
// one, which not every subcommand takes), so it gets the config override, which covers diff,
// log and status alike. The other three accept --color=never before the subcommand.
func TestUncoloredUsesEachBackendsOwnSwitch(t *testing.T) {
	assert.Equal(t, []string{"-c", "color.ui=false", "diff", "-U1", "HEAD"},
		uncolored("git", []string{"diff", "-U1", "HEAD"}))
	for _, name := range []string{"hg", "sl", "jj"} {
		assert.Equal(t, []string{"--color=never", "diff"}, uncolored(name, []string{"diff"}), name)
	}
	// The switch must PRECEDE the subcommand: all four treat it as a global option, and one
	// placed after the subcommand is either rejected or silently scoped to it.
	got := uncolored("hg", []string{"-R", "/repo", "log"})
	assert.Equal(t, "--color=never", got[0])

	// An unknown backend is passed through untouched rather than guessed at: inventing a
	// flag for it would break every invocation instead of merely leaving color on.
	assert.Equal(t, []string{"diff"}, uncolored("fossil", []string{"diff"}))
}

// BranchChanges answers the question the console asks to warn a reader that a file in front of
// them is also being edited elsewhere. The remote-tracking refs are built by hand rather than by
// cloning: what matters is that the ref exists under refs/remotes, not how it got there.
func TestBranchChangesReportsOtherRemoteBranches(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	gitRun(t, repo, "branch", "-M", "main")

	// Two colleagues' branches, each touching one file, recorded where a fetch would put them.
	gitRun(t, repo, "checkout", "-q", "-b", "theirs")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a // theirs\n"), 0o644))
	gitRun(t, repo, "commit", "-qam", "theirs")
	gitRun(t, repo, "update-ref", "refs/remotes/origin/theirs", "theirs")

	gitRun(t, repo, "checkout", "-q", "-b", "other", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.go"), []byte("package b // other\n"), 0o644))
	gitRun(t, repo, "commit", "-qam", "other")
	gitRun(t, repo, "update-ref", "refs/remotes/origin/other", "other")

	// The reader's own branch, which must NOT come back as competition with itself.
	gitRun(t, repo, "checkout", "-q", "-b", "mine", "main")
	gitRun(t, repo, "update-ref", "refs/remotes/origin/mine", "mine")

	got, err := gitVCS{}.BranchChanges(t.Context(), repo, "main", 10)
	require.NoError(t, err)

	byRef := map[string][]string{}
	for _, b := range got {
		byRef[b.Ref] = b.Paths
	}
	assert.Equal(t, []string{"a.go"}, byRef["theirs"])
	assert.Equal(t, []string{"b.go"}, byRef["other"])
	assert.NotContains(t, byRef, "mine", "the reader's own branch is not competition")
	// The remote prefix is stripped: a reader names the branch, not the ref.
	assert.NotContains(t, byRef, "origin/theirs")
}

// The cap belongs to the backend so git can apply it to the ref listing and no diff is ever run
// for a branch that was going to be discarded.
func TestBranchChangesHonorsTheLimit(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.go": "package a\n"})
	gitRun(t, repo, "branch", "-M", "main")
	for _, name := range []string{"one", "two", "three"} {
		gitRun(t, repo, "checkout", "-q", "-b", name, "main")
		require.NoError(t, os.WriteFile(filepath.Join(repo, name+".go"), []byte("package "+name+"\n"), 0o644))
		gitRun(t, repo, "add", "-A")
		gitRun(t, repo, "commit", "-qm", name)
		gitRun(t, repo, "update-ref", "refs/remotes/origin/"+name, name)
	}
	gitRun(t, repo, "checkout", "-q", "main")

	got, err := gitVCS{}.BranchChanges(t.Context(), repo, "main", 2)
	require.NoError(t, err)
	assert.Len(t, got, 2)

	none, err := gitVCS{}.BranchChanges(t.Context(), repo, "main", 0)
	require.NoError(t, err)
	assert.Empty(t, none, "a limit of zero asks for nothing and must fork nothing")
}

// The remote is not always called "origin". Trimming that literal prefix left an `upstream/feat/x`
// with its prefix intact, so it never matched the reader's own branch name and was reported as
// somebody else editing the exact files the reader had open: the worst possible false alarm from
// a feature whose whole job is warning about collisions.
func TestBranchChangesExcludesTheReadersBranchOnAnyRemote(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.go": "package a\n"})
	gitRun(t, repo, "branch", "-M", "main")

	gitRun(t, repo, "checkout", "-q", "-b", "mine", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a // mine\n"), 0o644))
	gitRun(t, repo, "commit", "-qam", "mine")
	// The same branch on two remotes, which is what a fork checkout looks like.
	gitRun(t, repo, "update-ref", "refs/remotes/origin/mine", "mine")
	gitRun(t, repo, "update-ref", "refs/remotes/upstream/mine", "mine")

	got, err := gitVCS{}.BranchChanges(t.Context(), repo, "main", 10)
	require.NoError(t, err)
	for _, b := range got {
		assert.NotEqual(t, "mine", b.Ref, "the reader's own branch is not competition, on any remote")
	}
	assert.Empty(t, got)
}

// TestBranchChangesSeesLocalBranchesNobodyHasPushed is the case the remote-only scan went blind
// on, and it is the normal shape of agent fan-out: worktrees of one repository, on local branches
// with no remote-tracking copy. An empty answer here is indistinguishable from "nothing competes".
func TestBranchChangesSeesLocalBranchesNobodyHasPushed(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	gitRun(t, repo, "branch", "-M", "main")

	// Never pushed, so no refs/remotes/ entry exists for either.
	gitRun(t, repo, "checkout", "-q", "-b", "agent-one")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a // one\n"), 0o644))
	gitRun(t, repo, "commit", "-qam", "one")

	gitRun(t, repo, "checkout", "-q", "-b", "agent-two", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.go"), []byte("package b // two\n"), 0o644))
	gitRun(t, repo, "commit", "-qam", "two")

	gitRun(t, repo, "checkout", "-q", "-b", "mine", "main")

	got, err := gitVCS{}.BranchChanges(t.Context(), repo, "main", 10)
	require.NoError(t, err)

	byRef := map[string]types.BranchChange{}
	for _, b := range got {
		byRef[b.Ref] = b
	}
	assert.Equal(t, []string{"a.go"}, byRef["agent-one"].Paths)
	assert.Equal(t, []string{"b.go"}, byRef["agent-two"].Paths)
	assert.True(t, byRef["agent-one"].Local, "a local branch is current, not as-of-last-fetch")
	assert.NotContains(t, byRef, "mine", "the reader's own branch is not competition")
}

// A branch and its remote-tracking copy are ONE line of work under two names. Reporting both would
// tell the reader two people are editing a file when one is, and the local side wins because it is
// the current answer where the tracking copy is only as new as the last fetch.
func TestBranchChangesReportsABranchAndItsTrackingCopyOnce(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"a.go": "package a\n"})
	gitRun(t, repo, "branch", "-M", "main")

	gitRun(t, repo, "checkout", "-q", "-b", "theirs")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a // theirs\n"), 0o644))
	gitRun(t, repo, "commit", "-qam", "theirs")
	gitRun(t, repo, "update-ref", "refs/remotes/origin/theirs", "theirs")

	gitRun(t, repo, "checkout", "-q", "-b", "mine", "main")

	got, err := gitVCS{}.BranchChanges(t.Context(), repo, "main", 10)
	require.NoError(t, err)

	var theirs []types.BranchChange
	for _, b := range got {
		if b.Ref == "theirs" {
			theirs = append(theirs, b)
		}
	}
	require.Len(t, theirs, 1, "one line of work, reported once")
	assert.True(t, theirs[0].Local, "the local side wins: it is current, the tracking copy is not")
}

// gitCapture runs one git command in dir with extra environment and returns its trimmed
// stdout. It covers the two things gitRun cannot: reading a result back, and setting
// GIT_COMMITTER_DATE, which is the only way to mint a ref that is genuinely old.
func gitCapture(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitEnv(), env...)
	out, err := cmd.Output()
	require.NoErrorf(t, err, "git %s", strings.Join(args, " "))
	return strings.TrimSpace(string(out))
}

// What makes an object magus's is the MESSAGE Preserve wrote on it, not where it sits.
// refs/magus/preserved is a namespace, not a signature: anyone can update-ref into it, and
// this pruner runs unasked inside every Preserve.
func TestGitPrunePreservedSparesARefMagusDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"})

	tree := gitCapture(t, dir, nil, "rev-parse", "HEAD^{tree}")
	foreign := gitCapture(t, dir, nil, "commit-tree", tree, "-p", "HEAD", "-m", "a snapshot someone else took")
	gitRun(t, dir, "update-ref", preservedRefPrefix+foreign, foreign)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
	handle, err := gitVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, handle)

	dropped, err := gitVCS{}.PrunePreserved(t.Context(), dir, time.Now().Add(time.Hour))
	require.NoError(t, err)

	assert.Equal(t, []string{handle}, dropped, "pruning reported something other than its own capture")
	refs := gitListPreserved(t, dir)
	assert.Contains(t, refs, foreign, "pruning deleted a ref magus did not write")
	assert.NotContains(t, refs, handle, "pruning reported a handle it did not delete")
}

// Cutoffs an hour either side of now leave preserveRetention itself untested: the constant
// could be thirty SECONDS and every assertion stays green. So this runs through Preserve
// rather than passing PrunePreserved a cutoff of its own, and asserts both sides of the
// window, since only the survival half pins the length.
//
// The two ages are LITERAL days, not preserveRetention plus or minus a day: written against
// the constant they move with it, and 29 either side of thirty seconds is still one on each
// side. Measured by setting the constant to thirty seconds.
func TestGitPreserveRetentionBoundaryIsThirtyDays(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"})
	tree := gitCapture(t, dir, nil, "rev-parse", "HEAD^{tree}")

	// Aged through the committer date, which is what PrunePreserved keys on.
	aged := func(days int) string {
		when := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
		sha := gitCapture(t, dir, []string{"GIT_COMMITTER_DATE=" + when, "GIT_AUTHOR_DATE=" + when},
			"commit-tree", tree, "-p", "HEAD", "-m", preserveMessage)
		gitRun(t, dir, "update-ref", preservedRefPrefix+sha, sha)
		return sha
	}
	inside := aged(29)
	outside := aged(31)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
	_, err := gitVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)

	refs := gitListPreserved(t, dir)
	assert.Contains(t, refs, inside, "dropped a capture still inside the retention window")
	assert.NotContains(t, refs, outside, "kept a capture past the retention window")
}

// gitShimMovingARefOnce puts a `git` ahead of the real one on PATH that passes every
// command through and, the first time it sees a for-each-ref, moves ref to sha behind the
// caller's back. It is the only seam that opens the window between the listing
// PrunePreserved vets a ref on and the delete it issues afterwards.
func gitShimMovingARefOnce(t *testing.T, dir, ref, sha string) {
	t.Helper()
	real, err := exec.LookPath("git")
	require.NoError(t, err)
	shimDir := t.TempDir()
	marker := filepath.Join(shimDir, "moved")
	script := fmt.Sprintf(`#!/bin/sh
saw=
for a in "$@"; do
	if [ "$a" = for-each-ref ]; then saw=1; fi
done
%q "$@"
status=$?
if [ -n "$saw" ] && [ ! -e %q ]; then
	: > %q
	%q -C %q update-ref %q %q >/dev/null 2>&1
fi
exit $status
`, real, marker, marker, real, dir, ref, sha)
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "git"), []byte(script), 0o755))
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The name, the date and the subject all describe the commit a ref pointed at when
// for-each-ref answered, while `update-ref -d <ref>` with no old value deletes whatever it
// points at when the delete lands. In between, a capture magus would never have chosen is
// deletable, and it is user work with no other copy.
func TestGitPrunePreservedRefusesARefThatMoved(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"})
	tree := gitCapture(t, dir, nil, "rev-parse", "HEAD^{tree}")

	when := time.Now().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	stale := gitCapture(t, dir, []string{"GIT_COMMITTER_DATE=" + when, "GIT_AUTHOR_DATE=" + when},
		"commit-tree", tree, "-p", "HEAD", "-m", preserveMessage)
	gitRun(t, dir, "update-ref", preservedRefPrefix+stale, stale)
	// Today's capture, which passes no check the pruner makes and must survive.
	fresh := gitCapture(t, dir, nil, "commit-tree", tree, "-p", "HEAD", "-m", preserveMessage)

	gitShimMovingARefOnce(t, dir, preservedRefPrefix+stale, fresh)
	_, err := gitVCS{}.PrunePreserved(t.Context(), dir, time.Now().Add(-time.Hour))

	require.Error(t, err, "deleted a ref that moved after it was vetted")
	assert.Equal(t, fresh, gitCapture(t, dir, nil, "rev-parse", preservedRefPrefix+stale),
		"the capture the ref moved to is gone")
}

// git creates the scratch index, so nothing in the working tree records that it existed
// and only Preserve's own removal bounds it.
func TestGitPreserveLeavesNoTempIndexBehind(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"})
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
	handle, err := gitVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, handle)

	left, err := filepath.Glob(filepath.Join(tmp, "magus-preserve-index-*"))
	require.NoError(t, err)
	assert.Empty(t, left, "left the scratch index behind in the shared tmpdir")
}

// TestGitPreserveNeedsNoConfiguredIdentity reproduces the split that hid every preserve
// failure until CI: commit-tree refuses a name or email it guessed, so the whole feature
// worked on a developer box and could not run on a runner, where no gitconfig names
// anyone.
//
// user.useConfigOnly is what makes that reproducible anywhere. Emptying the config alone
// does not: git then guesses user@hostname and only REFUSES the guess where it cannot
// build a fully qualified one, so the same test passes on a laptop and fails on a runner,
// which is the split being fixed rather than a test of it.
func TestGitPreserveNeedsNoConfiguredIdentity(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"})
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(cfg, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644))
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	// Unset, not blanked: git rejects an empty ident with a different error, which would
	// pass this test for a reason it is not about.
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		if was, ok := os.LookupEnv(name); ok {
			t.Cleanup(func() { _ = os.Setenv(name, was) })
			require.NoError(t, os.Unsetenv(name))
		}
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
	handle, err := gitVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, handle)
	assert.Equal(t, "two", gitCapture(t, dir, nil, "show", handle+":a.txt"),
		"the capture does not hold the uncommitted work")
}

// TestEnsureMergeDriverIdempotent drives EnsureMergeDriver end to end against a real repo,
// which is where the accumulation actually bit: every magus invocation runs it, so a section
// appended rather than replaced grew .gitattributes by a block per command.
//
// EnsureMergeDriver builds its own git commands and does NOT route them through gitEnv, so
// this pins the ambient git environment itself. GIT_DIR is the one that matters: git exports
// it inside every hook and every `rebase --exec`, so running the suite from a pre-commit hook
// sent `git config merge.magus.driver` into whatever repo was being committed to, the test
// passing all the while, because it never looked. Pointing the variables at the fixture makes
// the target explicit rather than merely unset, and the config assertion below is what proves
// the write landed here.
func TestEnsureMergeDriverIdempotent(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", repo)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	outputGlobs := []string{"gen/**", "docs/gen/**"}
	attrsPath := filepath.Join(repo, ".gitattributes")

	changed, err := gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)
	assert.True(t, changed, "first call installs the section")

	changed, err = gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)
	assert.False(t, changed, "second call has nothing to do")

	// Assert the CONTENT, not just that two reads agree: with changed==false a write is
	// impossible, so comparing the two reads can only ever restate the line above. An
	// EnsureMergeDriver that wrote an empty section would satisfy that and fail a user.
	assertFile(t, attrsPath, generatedMarkers.section(
		"gen/** merge=magus linguist-generated\n"+
			"docs/gen/** merge=magus linguist-generated\n"), 0o644)

	// The registration is half of what EnsureMergeDriver promises, and reading it back from
	// the fixture is also what would catch the config escaping into another repository.
	assert.Contains(t, gitConfigValue(t, repo, "merge.magus.driver"), gitDriverArgs,
		"driver registered in the fixture repo")

	// Re-wiring is the other reason EnsureMergeDriver exists: a project that declares an
	// output later must be added, not left frozen at the shape the workspace had on the day
	// init ran. The steady state above cannot show that.
	changed, err = gitVCS{}.EnsureMergeDriver(t.Context(), repo, []string{"gen/**", "dist/**"})
	require.NoError(t, err)
	assert.True(t, changed, "a changed glob set re-wires")
	assertFile(t, attrsPath, generatedMarkers.section(
		"gen/** merge=magus linguist-generated\n"+
			"dist/** merge=magus linguist-generated\n"), 0o644)
}

// TestEnsureMergeDriverLeavesACRLFWorktreeClean pins the fix for the v0.4.1 windows release,
// which stamped every artifact `v0.4.1-dirty` while the other four platforms were clean.
//
// .gitattributes is the one TRACKED file magus rewrites on every workspace load. Git for
// Windows ships core.autocrlf=true and this repository declares no eol attribute, so
// checkout smudges the LF blob to CRLF on disk; writing the managed section back as LF then
// makes git report the file modified, and `git describe --dirty` says so.
//
// core.autocrlf is set on the fixture rather than mocked, so this runs the real smudge on
// any host: the checkout below produces CRLF everywhere, which is what makes the case
// reproducible off Windows. The final describe is the assertion that matters, because it is
// the exact call version() makes.
func TestEnsureMergeDriverLeavesACRLFWorktreeClean(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", repo)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	outputGlobs := []string{"gen/**", "docs/gen/**"}

	// Commit the section as an LF blob, the way every non-Windows contributor does.
	_, wanted, err := gitVCS{}.gitAttrsState(repo, outputGlobs)
	require.NoError(t, err)
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n", ".gitattributes": wanted})
	gitRun(t, repo, "config", "core.autocrlf", "true")
	gitRun(t, repo, "tag", "v0.4.1")

	// Re-checkout under autocrlf: this is the Windows runner's starting state.
	require.NoError(t, os.Remove(filepath.Join(repo, ".gitattributes")))
	gitRun(t, repo, "checkout", "--", ".gitattributes")
	onDisk, err := os.ReadFile(filepath.Join(repo, ".gitattributes"))
	require.NoError(t, err)
	require.Contains(t, string(onDisk), "\r\n", "the fixture reproduces the CRLF smudge")

	// changed is true here for a reason unrelated to the tracked file: a fresh clone has no
	// merge.magus.driver registered, and that write lands in .git/config.
	_, err = gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)

	after, err := os.ReadFile(filepath.Join(repo, ".gitattributes"))
	require.NoError(t, err)
	assert.Equal(t, string(onDisk), string(after),
		"the section is rewritten with the line ending the file already uses, so the bytes do not move")

	described, err := gitVCS{}.Describe(t.Context(), repo)
	require.NoError(t, err)
	assert.Equal(t, "v0.4.1", described, "a CRLF worktree is not dirt; v0.4.1 shipped as v0.4.1-dirty because it was")

	changed, err := gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)
	assert.False(t, changed, "and the steady state stays quiet rather than rewriting every load")
}

// TestEnsureMergeDriverIgnoresAmbientGitDir pins the escape gitEnviron exists to stop.
//
// GIT_DIR overrides both -C and cmd.Dir, and git exports it into every hook and
// `rebase --exec`. EnsureMergeDriver runs on workspace load, so before the scrub, a magus
// command invoked from a pre-commit hook registered the merge driver in whatever repository
// was being committed to, succeeding quietly, because nothing ever read the value back.
func TestEnsureMergeDriverIgnoresAmbientGitDir(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	bystander := t.TempDir()
	gitInitRepo(t, bystander, map[string]string{"magus.yaml": "version: 1\n"})

	// Point the ambient environment at the bystander, exactly as a git hook would.
	t.Setenv("GIT_DIR", filepath.Join(bystander, ".git"))
	t.Setenv("GIT_WORK_TREE", bystander)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	_, err := gitVCS{}.EnsureMergeDriver(t.Context(), repo, []string{"gen/**"})
	require.NoError(t, err)

	assert.Contains(t, gitConfigValue(t, repo, "merge.magus.driver"), gitDriverArgs,
		"the named repo is the one configured")
	assert.Empty(t, gitConfigValue(t, bystander, "merge.magus.driver"),
		"the repo named only by ambient GIT_DIR must be left alone")
}

// gitConfigValue reads one local config key, returning "" when it is unset. It reads with a
// scrubbed environment for the same reason the production path writes with one: an ambient
// GIT_DIR would otherwise answer about a different repository than the caller named.
func gitConfigValue(t *testing.T, repo, key string) string {
	t.Helper()
	cmd := gitExec(t.Context(), "-C", repo, "config", "--get", key)
	out, err := cmd.Output()
	if err != nil {
		return "" // git exits 1 for an unset key; any real failure surfaces as an empty value
	}
	return strings.TrimSpace(string(out))
}

// pathUnder decides whether a registration names a binary from THIS worktree, which is the
// difference between a deliberate local driver and another worktree's build.
func TestPathUnder(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "w", "repo")
	for _, tc := range []struct {
		name string
		p    string
		want bool
	}{
		{"the binary at the root", filepath.Join(root, "magus"), true},
		{"a binary in a subdirectory", filepath.Join(root, "hack", "driver.sh"), true},
		{"the root itself", root, true},
		{"an unclean path that still lands inside", filepath.Join(root, "hack", "..", "magus"), true},
		{"a sibling worktree", filepath.Join(string(filepath.Separator), "w", "other", "magus"), false},
		{"a prefix-sharing sibling", root + "-2" + string(filepath.Separator) + "magus", false},
		{"an installed release", filepath.Join(string(filepath.Separator), "usr", "local", "bin", "magus"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pathUnder(root, tc.p))
		})
	}
}

// A workspace that builds its own magus prefers it, and that preference has to be a reason
// to REPLACE what is registered. Reachability alone left a v0.3.0 release registered across
// 142 worktrees: it answered the -h probe, so the steady-state check returned early and
// nothing ever rewrote.
func TestDriverIsPreferredHere(t *testing.T) {
	root := t.TempDir()
	release := "/usr/local/bin/magus" + gitDriverArgs

	assert.True(t, driverIsPreferredHere(root, release),
		"with no local build there is nothing better to offer, so PATH keeps its registration")

	local := filepath.Join(root, "magus")
	require.NoError(t, os.WriteFile(local, []byte("#!/bin/sh\n"), 0o755))

	assert.False(t, driverIsPreferredHere(root, release),
		"a local build must displace a registration pointing outside the worktree")
	assert.True(t, driverIsPreferredHere(root, local+gitDriverArgs))
	assert.True(t, driverIsPreferredHere(root, filepath.Join(root, "magus-dev")+gitDriverArgs),
		"a deliberate pinned registration is from this worktree and must survive")
}

// TestRegisteredDriverReportsAbsence pins the two states git spells identically: `config`
// exits 1 with no output for an absent key, and 0 with no output for a key set to the
// empty value. Both are unusable, since every predicate downstream reads an executable
// path out of the command.
func TestRegisteredDriverReportsAbsence(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	setDriver := func(value string) {
		t.Helper()
		require.NoError(t, gitExec(t.Context(), "-C", repo, "config", "merge.magus.driver", value).Run())
	}

	cmd, ok := gitVCS{}.registeredDriver(t.Context(), repo)
	assert.False(t, ok, "a fresh clone has registered nothing")
	assert.Empty(t, cmd)

	setDriver("")
	cmd, ok = gitVCS{}.registeredDriver(t.Context(), repo)
	assert.False(t, ok, "a key set to the empty value names no executable either")
	assert.Empty(t, cmd)

	want := "/usr/local/bin/magus" + gitDriverArgs
	setDriver(want)
	cmd, ok = gitVCS{}.registeredDriver(t.Context(), repo)
	assert.True(t, ok)
	assert.Equal(t, want, cmd)
}

// conflictRepo builds a repo with a real, in-progress merge conflict: main and side both
// change shared.txt, and side deletes gone.txt that main changed. Both shapes matter:
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
// directly: a test through the flag alone passes with the parser broken.
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
// anything tracked not-ignored, which makes a generated file one side STOPPED tracking
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

// TestDriverProbeSilenceIsNotAnAnswer pins how the two callers read a probe that never
// reported: the registration is kept, the binary is refused. Only the keep is undone by the
// next Ensure.
func TestDriverProbeSilenceIsNotAnAnswer(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "magus")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	env, err := exec.LookPath("env")
	require.NoError(t, err)

	// Reaches the branch driverProbeBudget reaches, without spending 30s to get there.
	silent, cancel := context.WithCancel(t.Context())
	cancel()

	dispatches, answered := driverProbe(silent, fake, []string{"vcs", "merge-driver"})
	assert.False(t, dispatches)
	assert.False(t, answered, "a probe that was killed reported no exit status")

	assert.True(t, driverUsable(silent, env+" FOO=1 "+fake+" vcs merge-driver %O %A %B %L %P"),
		"an unproven registration is kept; rewriting would drop the wrapper for good")
	assert.False(t, driverExeAnswers(silent, fake),
		"an unproven binary is never registered; os.Executable dispatches by construction")
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
	assert.False(t, driverServes(t.Context(), newerRegistration, olderWanted),
		"an older binary must rewrite a verb it cannot dispatch, not keep it")

	assert.True(t, driverArgsMatch(deaf+" merge-driver %O %A %B %L %P", olderWanted),
		"its own spelling still short-circuits")
}

// TestDriverIsReachableHere: a driver registered by ANOTHER worktree points at a binary
// that exists and runs, so every rot check passes while merges resolve with a foreign
// build. Only "is this what I would have chosen" catches it.
func TestDriverIsReachableHere(t *testing.T) {
	root := t.TempDir()
	assert.False(t, driverIsReachableHere(t.Context(), root, ""))
	assert.False(t, driverIsReachableHere(t.Context(), root,
		"/Users/x/repo/.claude/worktrees/other-8f2a/magus vcs merge-driver %O"),
		"another worktree's binary is not this one's, however runnable it is")

	assert.True(t, driverIsReachableHere(t.Context(), root, "magus vcs merge-driver %O"),
		"a bare name resolves through PATH wherever it runs")

	// A binary inside THIS root is reachable by definition, which is what a deliberate
	// per-worktree registration names and what the rule above used to reject.
	assert.True(t, driverIsReachableHere(t.Context(), root,
		filepath.Join(root, "magus-dev")+" vcs merge-driver %O"))

	self, err := os.Executable()
	require.NoError(t, err)
	assert.True(t, driverIsReachableHere(t.Context(), root, self+" vcs merge-driver %O"))
	assert.True(t, driverIsReachableHere(t.Context(), root, `"`+self+`" vcs merge-driver %O`),
		"a quoted path is unwrapped before comparison")
}
