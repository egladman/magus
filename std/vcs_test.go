package std

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// The vcs accessors had no direct unit coverage at all: they were exercised only through
// interp integration tests, which run with a workspace on the context and a real checkout
// under the process cwd. That left the branch that matters least tested and most recently
// changed: what happens when there is NO VCS to ask.
//
// These pin the contract that replaced the empty-string sentinels: outside a checkout every
// accessor RAISES rather than handing back "", because "" is a value a branch name or a
// subject line can legitimately hold, and a caller that forgot to test for it would
// interpolate an empty commit into a version string with nothing to surface it.
//
// Note what "outside a checkout" means here, because it is not what it looks like:
// resolveVCS picks a DRIVER (git is on PATH), so it resolves even in a bare temp dir, and
// it is the git command that then fails. The v == nil branch needs git itself to be absent.
// So these run against a real driver failing on a non-repo, which is the path a user
// actually hits: building from a release tarball or a container context.

// chdirOutsideAnyRepo moves the process into a bare temp dir for the duration of the test,
// so nothing resolves a VCS by walking up from the magus checkout the tests run in.
func chdirOutsideAnyRepo(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

func TestVcsAccessorsRaiseWithNoVCS(t *testing.T) {
	chdirOutsideAnyRepo(t)
	ctx := context.Background()

	for name, call := range map[string]func() (string, error){
		"ref": func() (string, error) {
			ref, err := VcsRef(ctx, "")
			assert.Nil(t, ref, "no VCS is an error, not a checkout that names no ref")
			return "", err
		},
		"describe": func() (string, error) { return VcsDescribe(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			v, err := call()
			require.Error(t, err, "an unavailable value must raise, not return \"\"")
			assert.Empty(t, v, "the zero value still comes back alongside the error")
			assert.Contains(t, err.Error(), "git",
				"the message must name the VCS it failed to read, not just fail")
		})
	}
}

// TestVcsCommitRaisesWithNoVCS covers the record-returning accessor. Its docs used to tell
// callers to test a FIELD (c.date == "") to discover there was no commit, which is the
// same sentinel problem one level in.
func TestVcsCommitRaisesWithNoVCS(t *testing.T) {
	chdirOutsideAnyRepo(t)

	c, err := VcsCommit(context.Background(), "")
	require.Error(t, err)
	assert.Empty(t, c.ID, "no half-populated record to sniff")
	assert.Contains(t, err.Error(), "git")
}

// TestVcsHistoryRaisesWithNoVCS: an empty history and an unreadable one are different
// answers, and only one of them means "this project has no commits".
func TestVcsHistoryRaisesWithNoVCS(t *testing.T) {
	chdirOutsideAnyRepo(t)

	got, err := VcsHistory(context.Background(), 5, nil, false)
	require.Error(t, err)
	assert.Nil(t, got)
}

// TestVcsHistoryPassesPathsAndLimitThrough runs a real git repository: the query reaches
// the driver whole, so paths narrow both the commits and their files, and limit 0 is
// every commit rather than none.
func TestVcsHistoryPassesPathsAndLimitThrough(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"},
		{"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"},
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_SYSTEM", os.DevNull},
	} {
		t.Setenv(kv[0], kv[1])
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644))
	}
	git("init", "-q", "-b", "main")
	write("docs/a.md")
	write("other.txt")
	git("add", ".")
	git("commit", "-qm", "both")
	write("other.txt.2")
	git("add", ".")
	git("commit", "-qm", "outside")

	got, err := VcsHistory(WithCwd(context.Background(), dir), 0, []string{"docs"}, true)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "both", got[0].Subject)
	assert.Equal(t, []string{"docs/a.md"}, got[0].Files)

	all, err := VcsHistory(WithCwd(context.Background(), dir), 0, nil, false)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// A detached checkout names no ref, and says so with null: "" and "HEAD" both read as a
// branch to a caller that does not know git.
func TestVcsRefIsNullOnADetachedCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"},
		{"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"},
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_SYSTEM", os.DevNull},
	} {
		t.Setenv(kv[0], kv[1])
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	ctx := WithCwd(context.Background(), dir)

	ref, err := VcsRef(ctx, "")
	require.NoError(t, err)
	require.NotNil(t, ref)
	assert.Equal(t, "main", *ref)

	git("switch", "-q", "--detach")
	ref, err = VcsRef(ctx, "")
	require.NoError(t, err)
	assert.Nil(t, ref)
}

// gitTopicRepo makes dir a git repository whose branch topic adds file on top of main.
func gitTopicRepo(t *testing.T, dir, topic, file string) string {
	t.Helper()
	require.NoError(t, os.Mkdir(dir, 0o755))
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	git("switch", "-q", "-c", topic)
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644))
	git("add", ".")
	git("commit", "-qm", "add "+file)
	return dir
}

// A dir argument reads the repository holding it, not the one holding the cwd: a guard
// rule runs in the hook process's checkout while the caller pushes from its own.
func TestVcsDirArgumentsReadTheRepositoryHoldingDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"},
		{"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"},
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_SYSTEM", os.DevNull},
	} {
		t.Setenv(kv[0], kv[1])
	}
	parent := t.TempDir()
	hook := gitTopicRepo(t, filepath.Join(parent, "hook"), "alpha", "a.txt")
	caller := gitTopicRepo(t, filepath.Join(parent, "caller"), "beta", "b.txt")
	ctx := WithCwd(context.Background(), hook)

	for name, dir := range map[string]string{"absolute": caller, "relative to the cwd": filepath.Join("..", "caller")} {
		t.Run(name, func(t *testing.T) {
			ref, err := VcsRef(ctx, dir)
			require.NoError(t, err)
			require.NotNil(t, ref)
			assert.Equal(t, "beta", *ref)

			files, err := VcsChangedFiles(ctx, "main", dir)
			require.NoError(t, err)
			require.Len(t, files, 1)
			assert.Equal(t, "b.txt", files[0].Value)
			assert.Equal(t, canonical(t, caller), canonical(t, files[0].Base))

			regions, err := VcsRegions(ctx, "main", dir)
			require.NoError(t, err)
			require.Len(t, regions, 1)
			assert.Equal(t, "b.txt", regions[0].File.Path)
		})
	}

	ref, err := VcsRef(ctx, "")
	require.NoError(t, err)
	require.NotNil(t, ref)
	assert.Equal(t, "alpha", *ref, "no dir still reads the cwd's repository")
	files, err := VcsChangedFiles(ctx, "main", "")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "a.txt", files[0].Value)
}

// dirBackend builds, for one VCS, a repository whose named ref "topic" adds b.txt on top of
// a first revision that parent names.
type dirBackend struct {
	name, bin, parent, base string
	init                    func(t *testing.T, dir string)
}

func dirBackends() []dirBackend {
	run := func(t *testing.T, dir, bin string, args ...string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s %v: %s", bin, args, out)
	}
	write := func(t *testing.T, dir, name string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644))
	}
	return []dirBackend{
		{"git", "git", "HEAD~1", "origin/main", func(t *testing.T, dir string) {
			run(t, dir, "git", "init", "-q", "-b", "main")
			write(t, dir, "a.txt")
			run(t, dir, "git", "add", ".")
			run(t, dir, "git", "commit", "-qm", "a")
			run(t, dir, "git", "switch", "-q", "-c", "topic")
			write(t, dir, "b.txt")
			run(t, dir, "git", "add", ".")
			run(t, dir, "git", "commit", "-qm", "b")
		}},
		{"hg", "hg", ".^", "default", func(t *testing.T, dir string) {
			run(t, dir, "hg", "init")
			write(t, dir, "a.txt")
			run(t, dir, "hg", "commit", "-qAm", "a", "-u", "test")
			run(t, dir, "hg", "branch", "-q", "topic")
			write(t, dir, "b.txt")
			run(t, dir, "hg", "commit", "-qAm", "b", "-u", "test")
		}},
		{"sl", "sl", ".^", "remote/main", func(t *testing.T, dir string) {
			run(t, dir, "sl", "init", ".")
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".sl", "config"),
				[]byte("[ui]\nusername = Magus Test <magus@example.com>\n"), 0o644))
			write(t, dir, "a.txt")
			run(t, dir, "sl", "commit", "-qAm", "a")
			run(t, dir, "sl", "bookmark", "topic")
			write(t, dir, "b.txt")
			run(t, dir, "sl", "commit", "-qAm", "b")
		}},
		{"jj", "jj", "@-", "trunk()", func(t *testing.T, dir string) {
			run(t, dir, "jj", "git", "init")
			write(t, dir, "a.txt")
			run(t, dir, "jj", "commit", "-m", "a")
			write(t, dir, "b.txt")
			run(t, dir, "jj", "describe", "-m", "b")
			run(t, dir, "jj", "bookmark", "create", "topic", "-r", "@")
		}},
	}
}

// Every backend honors dir: from a cwd in a git repository, a dir in another VCS's
// repository answers with that VCS's ref, changed files and default base. A backend whose
// binary is absent skips.
func TestVcsDirReadsEachBackendsRepository(t *testing.T) {
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"},
		{"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"},
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_SYSTEM", os.DevNull},
		{"HGRCPATH", os.DevNull}, {"JJ_USER", "t"}, {"JJ_EMAIL", "t@t"},
		{"MAGUS_VCS_NAME", ""}, {"MAGUS_VCS_BASE_REF", ""},
	} {
		t.Setenv(kv[0], kv[1])
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	hook := gitTopicRepo(t, filepath.Join(t.TempDir(), "hook"), "alpha", "a.txt")
	ctx := WithCwd(context.Background(), hook)
	for _, b := range dirBackends() {
		t.Run(b.name, func(t *testing.T) {
			if _, err := exec.LookPath(b.bin); err != nil {
				t.Skipf("%s not available", b.bin)
			}
			t.Setenv("MAGUS_VCS_"+strings.ToUpper(b.name)+"_BASE_REF", "")
			dir := filepath.Join(t.TempDir(), b.name)
			require.NoError(t, os.Mkdir(dir, 0o755))
			b.init(t, dir)

			ref, err := VcsRef(ctx, dir)
			require.NoError(t, err)
			require.NotNil(t, ref)
			assert.Equal(t, "topic", *ref)

			files, err := VcsChangedFiles(ctx, b.parent, dir)
			require.NoError(t, err)
			require.Len(t, files, 1)
			assert.Equal(t, "b.txt", files[0].Value)

			base, err := VcsBase(ctx, dir)
			require.NoError(t, err)
			assert.Equal(t, b.base, base)
		})
	}
}

// A dir that does not exist raises instead of falling back to the cwd's repository, which
// would answer for a checkout the caller did not name.
func TestVcsDirArgumentsRaiseForAMissingDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	ctx := WithCwd(context.Background(), t.TempDir())
	missing := filepath.Join(t.TempDir(), "gone")

	ref, err := VcsRef(ctx, missing)
	require.ErrorContains(t, err, "vcs.ref: dir")
	assert.Nil(t, ref)
	files, err := VcsChangedFiles(ctx, "", missing)
	require.ErrorContains(t, err, "vcs.changedFiles: dir")
	assert.Nil(t, files)
	regions, err := VcsRegions(ctx, "", missing)
	require.ErrorContains(t, err, "vcs.regions: dir")
	assert.Nil(t, regions)
	base, err := VcsBase(ctx, missing)
	require.ErrorContains(t, err, "vcs.base: dir")
	assert.Empty(t, base)
	_, err = VcsBase(ctx, "")
	require.NoError(t, err, "with no dir, base never raises")
}

func canonical(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

// A repository's first commit is made on an unborn branch, and hack/git-hooks/commit-msg.buzz
// falls back to symbolic-ref only because vcs\ref raises there.
func TestVcsRefRaisesOnAnUnbornBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))

	ref, err := VcsRef(WithCwd(context.Background(), dir), "")
	require.Error(t, err)
	assert.Nil(t, ref)
}

// TestVcsIsDirtyRaisesWhenTheProbeFails is the most important one here. is_dirty is the
// drift-gate primitive (a generate target asks it "did my output change?"), and it used to
// answer false when the git status probe FAILED. That is a gate reporting clean after a
// check that never ran, which is the one thing a gate must never do quietly.
//
// The v == nil case (no git at all) still returns false, deliberately: that is a known
// state which genuinely answers the question. Only a failed probe raises.
func TestVcsIsDirtyRaisesWhenTheProbeFails(t *testing.T) {
	chdirOutsideAnyRepo(t)

	_, err := VcsIsDirty(context.Background(), nil)
	require.Error(t, err, "a failed status probe must not read as a clean tree")
	assert.Contains(t, err.Error(), "status")
}

// TestVcsNameNeverRaises: name is the detection half of the pair and must stay answerable
// without a catch, the same split as os.env and os.lookupEnv, and what lets the accessors
// above afford to raise.
//
// It reports the resolved DRIVER, so in this bare temp dir it is still "git": the driver
// resolved, the repository is what is missing. Asking it whether a directory is a checkout
// would be a different question, and this is not it.
func TestVcsNameNeverRaises(t *testing.T) {
	chdirOutsideAnyRepo(t)

	_, err := VcsName(context.Background())
	require.NoError(t, err, "detection must never raise; that is its whole job")
}

// TestResolveVCSDoesNotCacheAnError pins that a failed vcs.Resolve (here: an
// unresolvable explicit VCS name from a misconfigured MAGUS_VCS_NAME) is not
// remembered as "no VCS" for the cwd. resolveVCS keys its cache on cwd alone, so
// before the fix the first call's error still set vcsCwdKey, and every later call
// for the same cwd short-circuited to the stale (nil, "") even after whatever
// caused the failure was gone.
func TestResolveVCSDoesNotCacheAnError(t *testing.T) {
	chdirOutsideAnyRepo(t)
	ctx := context.Background()

	t.Setenv("MAGUS_VCS_NAME", "totally-bogus-vcs-name")
	v, base := resolveVCS(ctx)
	require.Nil(t, v, "an unknown explicit VCS name must not resolve a driver")
	assert.Empty(t, base)

	// Clear the bad name; the SAME cwd must resolve normally now (git, per
	// TestVcsNameNeverRaises' note that resolution succeeds even in a bare temp
	// dir). Before the fix this returned the poisoned cached (nil, "") forever.
	t.Setenv("MAGUS_VCS_NAME", "")
	v, _ = resolveVCS(ctx)
	assert.NotNil(t, v, "a transient resolve error must not be cached as \"no VCS\" for this cwd")
}

func TestOsWhichRaisesForAMissingCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("resolves a real command", func(t *testing.T) {
		got, err := OsWhich(ctx, "sh")
		require.NoError(t, err)
		assert.NotEmpty(t, got)
	})

	t.Run("raises for a missing one", func(t *testing.T) {
		// Previously "", so a caller that skipped the equality check passed the empty
		// string straight into an exec or a path join.
		got, err := OsWhich(ctx, "definitely-no-such-cmd-zzz")
		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "not on PATH")
	})
}

func TestFsExistsDistinguishesAbsenceFromDenial(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t.Run("reports a real file", func(t *testing.T) {
		p := dir + "/present.txt"
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		got, err := FsExists(ctx, p)
		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("absence is false, not an error", func(t *testing.T) {
		// The distinction the sandbox change turns on: genuinely absent still answers
		// the question, so it stays a plain false. Only a DENIED path raises, because
		// then the question was never answered at all.
		got, err := FsExists(ctx, dir+"/definitely-absent.txt")
		require.NoError(t, err)
		assert.False(t, got)
	})
}

// The per-backend prefix stripping lives in the drivers, where each one knows its own format
// instead of being keyed on its NAME: a switch on the name silently gives any backend
// outside it git's parsing. vcs.TestParityDirtyFilesReturnsPaths pins the rule against every
// real binary; there is nothing for std to strip.

// vcs.regions is the job footprint handed to scripts, so it must name the declaration a new
// function adds as itself, which a hunk header gets wrong.
func TestVcsRegionsNamesEachChangedDeclaration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"},
		{"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"},
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_SYSTEM", os.DevNull},
	} {
		t.Setenv(kv[0], kv[1])
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	git("init", "-q", "-b", "main")
	write(".gitattributes", "*.go diff=golang\n")
	write("a.go", "package a\n\nfunc One() int {\n\treturn 1\n}\n")
	git("add", ".")
	git("commit", "-qm", "base")
	write("a.go", "package a\n\nfunc One() int {\n\treturn 2\n}\n\nfunc Two() int {\n\treturn One()\n}\n")

	got, err := VcsRegions(WithCwd(context.Background(), dir), "main", "")
	require.NoError(t, err)
	var placed []string
	for _, r := range got {
		placed = append(placed, fmt.Sprintf("%s %s %d-%d %s", r.File.Path, r.Side, r.Lines[0], r.Lines[1], r.Declaration))
	}
	assert.Equal(t, []string{
		"a.go old 4-4 func One() int {",
		"a.go new 4-6 func One() int {",
		"a.go new 7-8 func Two() int {",
	}, placed)
}

// vcs.cmd asks the lease gate before it runs anything, with the directory the command
// would run in, so a worker cannot reach past lease-vcs through the escape hatch. Not
// parallel: the job store resolves the per-repository state directory from the environment.
func TestVcsCmdAsksTheLeaseGate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	testkit.Isolate(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))

	ws := types.WithWorkspace(WithCwd(t.Context(), dir), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: dir})
	_, err = MagusPutJob(ws, "worker", map[string]any{"criteria": "ship it", "state": "running"})
	require.NoError(t, err)
	var asked []string
	refusal := errors.New("lease-vcs: refused")
	withVCSLeaseGate(t, func(_ context.Context, row types.Job, backend string, args []string, at string) error {
		asked = append(asked, row.ID+" "+backend+" "+strings.Join(args, " ")+" "+at)
		return refusal
	})

	_, err = VcsCmd(proc.WithLease(ws, "worker"), []string{"push", "origin", "main"}, map[string]any{"quiet": true})
	require.ErrorIs(t, err, refusal)
	assert.Equal(t, []string{"worker git push origin main " + dir}, asked)

	res, err := VcsCmd(ws, []string{"status", "--porcelain"}, map[string]any{"quiet": true})
	require.NoError(t, err, "no acting lease, so the gate is not asked")
	assert.True(t, res.OK)
	assert.Len(t, asked, 1)
}
