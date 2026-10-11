package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
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
var noFormatGlobs []types.Glob

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

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, types.MustParseGlobs("**/*.go"), gofmtList)
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
// format target's declared globs never reaches gofmtList at all: the narrowing happens
// before the (only) live check runs.
func TestCheckDriftForCommit_FormatGlobsScopeTheCheck(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.go", "package a\n", "first")
	writeAndCommit(t, dir, "vendor/b.go", "package a\n", "second")

	driver := resolveGitDriver(t, dir)
	classify := fixedClassify(nil)

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, types.MustParseGlobs("internal/**/*.go"), noGofmtList)
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

	notice, ok, err := checkDriftForCommit(context.Background(), dir, driver, classify, types.MustParseGlobs("**/*.go"), realGofmtList)
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

// TestBuildDriftNoticeUnpushedHead pins the amend case's exact wording: the commit is
// still unambiguous to name (it is HEAD), so the notice hands over a plain amend.
func TestBuildDriftNoticeUnpushedHead(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api"}}, driftUnpushedHead)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"Then fold it into abc1234 (unpushed, still HEAD):\n" +
		"  git commit --amend --no-edit"
	assert.Equal(t, want, got)
}

// TestBuildDriftNoticeUnpushedNotHead pins the fixup case: a plain amend would rewrite
// the wrong commit (HEAD, not abc1234), so the notice names a fixup targeted at the
// drifted hash, folded non-interactively via autosquash.
func TestBuildDriftNoticeUnpushedNotHead(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api"}}, driftUnpushedNotHead)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"Then fold it into abc1234 (unpushed, but no longer HEAD):\n" +
		"  git commit --fixup=abc1234 && GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash abc1234^"
	assert.Equal(t, want, got)
}

// TestBuildDriftNoticePushed pins the refusal case: NEITHER rewrite form appears,
// anywhere in the string, because abc1234 already left the repository.
func TestBuildDriftNoticePushed(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api"}}, driftPushed)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"abc1234 is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead."
	assert.Equal(t, want, got)
	// The prohibition sentence itself names "rebase" as one of the things not to do;
	// what must never appear is the actual REWRITE COMMAND.
	assert.NotContains(t, got, "--amend")
	assert.NotContains(t, got, "--fixup")
	assert.NotContains(t, got, "git rebase")
}

// TestBuildDriftNoticeMultipleProjects covers the multi-project regenerate command: one
// invocation naming every drifted project, not one per project.
func TestBuildDriftNoticeMultipleProjects(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api", "web"}}, driftUnpushedHead)
	assert.Contains(t, got, "magus run generate:rw api web")
	assert.Contains(t, got, "(api web changed with no matching regeneration)")
}

// TestBuildDriftNoticeFormattingOnly pins the formatting-only wording: MGS4009 (a
// sibling of MGS4006, minted for the commit-time question; see
// docs/reference/codes/race/MGS4009.md), a plain sentence naming the files, and
// format:rw as the remedy, distinct from generate:rw.
func TestBuildDriftNoticeFormattingOnly(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{unformatted: []string{"internal/foo.go"}}, driftUnpushedHead)
	want := "[MGS4009] commit abc1234 left formatting stale (gofmt would reformat): internal/foo.go\n" +
		"Reformat: magus run format:rw .\n" +
		"Then fold it into abc1234 (unpushed, still HEAD):\n" +
		"  git commit --amend --no-edit"
	assert.Equal(t, want, got)
	assert.NotContains(t, got, "MGS4006")
	assert.NotContains(t, got, "generate:rw")
}

// TestBuildDriftNoticeBothClasses pins that both classes appear in ONE notice, each
// carrying its own code and its own remedy, sharing the single amend-safety instruction
// at the end rather than repeating it per class.
func TestBuildDriftNoticeBothClasses(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{
		staleProjects: []string{"api"},
		unformatted:   []string{"internal/foo.go", "internal/bar.go"},
	}, driftPushed)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"[MGS4009] commit abc1234 also left formatting stale (gofmt would reformat): internal/foo.go, internal/bar.go\n" +
		"Reformat: magus run format:rw .\n" +
		"abc1234 is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead."
	assert.Equal(t, want, got)
}

const driftRangePatch = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n+b\n@@ -9 +9 @@\n-c\n+d\n"

// TestUnreadRangeNoticeCountsWhatNoMarkCovers pins the notice the drift job adds for the range a
// push sends: silent when there is nothing to read or nothing unread, a count otherwise, and
// "unknown", never "all unread", when the marks cannot be read. The command rides apart from the
// message and the load error rides as an error.
func TestUnreadRangeNoticeCountsWhatNoMarkCovers(t *testing.T) {
	first := changeset.ParseHunks(driftRangePatch)[0].Hunks[0].Digest
	next := hint.Diff.With("--unread", "--rev", "@{upstream}...HEAD")

	got, ok := unreadRangeNotice("@{upstream}...HEAD", driftRangePatch, []string{first}, nil)
	require.True(t, ok)
	assert.Equal(t, unreadNotice{msg: "1 of 2 hunks of this range (@{upstream}...HEAD) unread", next: next}, got)

	all := []string{first, changeset.ParseHunks(driftRangePatch)[0].Hunks[1].Digest}
	_, ok = unreadRangeNotice("@{upstream}...HEAD", driftRangePatch, all, nil)
	assert.False(t, ok, "a range read in full says nothing")
	_, ok = unreadRangeNotice("@{upstream}...HEAD", "", nil, nil)
	assert.False(t, ok, "an empty range says nothing")

	loadErr := errors.New("permission denied")
	got, ok = unreadRangeNotice("@{upstream}...HEAD", driftRangePatch, nil, loadErr)
	require.True(t, ok)
	assert.Equal(t, unreadNotice{
		msg:  "read state unknown for the 2 hunks of this range (@{upstream}...HEAD)",
		next: next,
		why:  "the read marks could not be read",
		err:  loadErr,
	}, got)
}

// TestPushedUnreadReadsExactlyThePushedRange runs against a real repository: the range is what
// the push sends against what the remote held, a commit made after the push started is not in
// it, a ref the remote does not have yet reads against the remote's default branch, and a
// range that cannot be read adds no line.
func TestPushedUnreadReadsExactlyThePushedRange(t *testing.T) {
	dir := initGitRepo(t)
	writeAndCommit(t, dir, "a.txt", "a\n", "first")
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{})
	require.NoError(t, err)
	cache := t.TempDir()
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")
	runGit(t, dir, "remote", "add", "origin", remote)
	rev := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir, "rev-parse"}, args...)...).Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}
	runGit(t, dir, "push", "-u", "origin", "HEAD")
	runGit(t, dir, "remote", "set-head", "origin", rev("--abbrev-ref", "HEAD"))
	held := rev("HEAD")
	writeAndCommit(t, dir, "a.txt", "a\nb\n", "second")
	sent := rev("HEAD")
	writeAndCommit(t, dir, "b.txt", "b\n", "third, made while the push ran")
	zero := strings.Repeat("0", 40)

	got := pushedUnread(context.Background(), res, dir, cache, "origin", []job.DriftPush{
		{Remote: held, Local: sent},
		{Remote: zero, Local: sent},
		{Remote: strings.Repeat("e", 40), Local: sent},
	})
	assert.Equal(t, []unreadNotice{
		{
			msg:  "1 of 1 hunks of this range (" + held[:12] + "..." + sent[:12] + ") unread",
			next: hint.Diff.With("--unread", "--rev", held[:12]+"..."+sent[:12]),
		},
		{
			msg:  "1 of 1 hunks of this range (origin/HEAD..." + sent[:12] + ") unread",
			next: hint.Diff.With("--unread", "--rev", "origin/HEAD..."+sent[:12]),
		},
	}, got)
	assert.Nil(t, pushedUnread(context.Background(), res, dir, cache, "origin", nil), "a commit's run sends no range")
}

// TestCheckDriftJobArgvHandsTheServerThePushedRefs pins what `job run check-drift <hook>` submits:
// a commit names its hook and nothing else, a push names its remote and every ref git says it
// sends except a deletion, and every argv it builds is one the server's job dispatch admits.
func TestCheckDriftJobArgvHandsTheServerThePushedRefs(t *testing.T) {
	a, b, zero := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("0", 40)
	stdin := "refs/heads/topic " + a + " refs/heads/topic " + b + "\n" +
		"refs/heads/new " + a + " refs/heads/new " + zero + "\n" +
		"(delete) " + zero + " refs/heads/gone " + b + "\n"

	cases := []struct {
		name  string
		args  []string
		stdin string
		want  []string
	}{
		{"no hook", nil, "", []string{"server", "check-drift"}},
		{"commit", []string{"post-commit"}, "", []string{"server", "check-drift", "--hook=post-commit"}},
		{"push", []string{"pre-push", "origin", "git@example.com:o/r.git"}, stdin, []string{
			"server", "check-drift", "--hook=pre-push", "--remote=origin",
			"--push=" + b + ":" + a, "--push=" + zero + ":" + a,
		}},
		{"push to a url", []string{"pre-push", "git@example.com:o/r.git", "git@example.com:o/r.git"}, stdin, []string{
			"server", "check-drift", "--hook=pre-push",
			"--push=" + b + ":" + a, "--push=" + zero + ":" + a,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkDriftJobArgv(tc.args, strings.NewReader(tc.stdin))
			assert.Equal(t, tc.want, got)
			assert.True(t, job.IsWorkerArgv(got), "the server must admit %v", got)
		})
	}
}

// TestParseCheckDriftArgsReadsWhatTheJobSubmits pins that the worker reads back exactly the hook
// the submitter wrote, and that a run with no flags, the one an older drift section starts, is a
// commit's.
func TestParseCheckDriftArgsReadsWhatTheJobSubmits(t *testing.T) {
	want := job.DriftHook{Hook: job.DriftHookPrePush, Remote: "origin", Pushes: []job.DriftPush{
		{Remote: strings.Repeat("b", 40), Local: strings.Repeat("a", 40)},
	}}
	got, err := parseCheckDriftArgs(want.Argv()[2:])
	require.NoError(t, err)
	assert.Equal(t, want, got)

	plain, err := parseCheckDriftArgs(nil)
	require.NoError(t, err)
	assert.Equal(t, job.DriftHook{Hook: job.DriftHookPostCommit}, plain)
}

// TestIsWorkerArgvRefusesCheckDriftArgumentsItDidNotWrite pins the admission rule a JobRequest
// meets: check-drift's flags are allowed, but nothing else rides along with them.
func TestIsWorkerArgvRefusesCheckDriftArgumentsItDidNotWrite(t *testing.T) {
	a := strings.Repeat("a", 40)
	for _, rest := range [][]string{
		{"--hook=post-merge"},
		{"--remote=origin"},
		{"--hook=pre-push", "--remote=-upload-pack=x"},
		{"--hook=pre-push", "--remote=git@example.com:o/r.git"},
		{"--hook=pre-push", "--push=HEAD:" + a},
		{"--hook=pre-push", "--push=" + a},
		{"--hook=pre-push", "--other"},
	} {
		assert.False(t, job.IsWorkerArgv(append([]string{"server", "check-drift"}, rest...)), "%v", rest)
	}
}
