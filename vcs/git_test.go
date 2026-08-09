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
	changed, err := writeManagedHook(path, gitHookBody("post-checkout", "magus server sync"))
	require.NoError(t, err)
	assert.True(t, changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(body)
	assert.True(t, strings.HasPrefix(s, "#!/bin/sh"), "a new hook gets a shebang")
	assert.Contains(t, s, gitHookBegin)
	assert.Contains(t, s, "magus server sync")
	assert.Contains(t, s, `[ "$3" = "1" ]`, "post-checkout guards on the branch-checkout flag")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "the hook is executable")
}

func TestWriteManagedHookIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-merge")
	_, err := writeManagedHook(path, gitHookBody("post-merge", "magus server sync"))
	require.NoError(t, err)

	changed, err := writeManagedHook(path, gitHookBody("post-merge", "magus server sync"))
	require.NoError(t, err)
	assert.False(t, changed, "re-installing an unchanged section is a no-op")
}

func TestWriteManagedHookPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-rewrite")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho 'my own hook'\n"), 0o755))

	changed, err := writeManagedHook(path, gitHookBody("post-rewrite", "magus server sync"))
	require.NoError(t, err)
	assert.True(t, changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "echo 'my own hook'", "the user's hook body is preserved")
	assert.Contains(t, s, gitHookBegin, "the managed section is appended")
}

// TestParseChangesByCommit verifies the NUL-delimited `git log --name-only` parse:
// a NUL line opens a commit (hash, author, date); following non-empty lines are files.
func TestParseChangesByCommit(t *testing.T) {
	out := "\x00abc123\x00Ada\x002026-06-20T10:00:00Z\n\napi/main.go\napi/util.go\n" +
		"\x00def456\x00Babbage\x002026-06-19T09:00:00Z\n\nweb/app.ts\n"

	got := parseChangesByCommit(out)
	require.Len(t, got, 2)

	assert.Equal(t, "abc123", got[0].ID)
	assert.Equal(t, "Ada", got[0].Author)
	assert.Equal(t, time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), got[0].Date.UTC())
	assert.Equal(t, []string{"api/main.go", "api/util.go"}, got[0].Files)

	assert.Equal(t, "def456", got[1].ID)
	assert.Equal(t, "Babbage", got[1].Author)
	assert.Equal(t, []string{"web/app.ts"}, got[1].Files)
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
// repository to re-measure a number this function already determined - and that walk is
// the most object-hungry thing in these tests. It failed once in CI with
//
//	error: Could not read <sha>
//	fatal: Failed to traverse parents of commit <sha>
//
// which is the signature of a loose object file that was written and then could not be
// read back (reproduced exactly by deleting one file from .git/objects). Nothing in the
// test touches the origin between building it and counting it, the shallow clone provably
// leaves the origin's .git unchanged, and a redirected GIT_OBJECT_DIRECTORY reports
// different errors - so the surviving explanation is the runner losing a write under
// load, on the shard this repo has already measured at 7.5GB+6.1GB on a 16GB box. No
// assertion can make that correct; not depending on those objects is the available fix.
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
	args := []string{"clone", "--quiet", "--single-branch", "--branch", "feat"}
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
// back from a clone that never had the merge base to begin with - and the recovery must
// stay bounded rather than quietly turning into `git fetch --unshallow`.
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

// TestRecoverMergeBaseUnusableBase covers the two bases with nothing to fetch from: one
// with no remote segment at all, and one naming a remote this repository does not have.
// Neither may be guessed at, because the segment reaches `git fetch` as a repository
// argument - a URL sink.
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
// removed. The "keeps" half is the load-bearing one - a blanket prefix strip would pass a
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
