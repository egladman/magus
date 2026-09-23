package vcs

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
)

// Every staging fixture has two projects, app and lib, written by hand, and two derived
// files: app/gen/out.txt from app's source, and INDEX, one line from both, which any
// change to either regenerates, the way a root routing index is. The base's
// .gitattributes marks both derived.

const stageDerived = "linguist-generated"

var stageAuthor = types.Person{Name: "stage", Email: "stage@example.invalid"}

var stageFixtureEnv = []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}

func stageGitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), stageFixtureEnv...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func renderStage(src string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(src), "\n", "|"))
}

// deriveStage rewrites both derived files in dir from its sources.
func deriveStage(dir string) error {
	app, err := os.ReadFile(filepath.Join(dir, "app", "src.txt"))
	if err != nil {
		return err
	}
	lib, err := os.ReadFile(filepath.Join(dir, "lib", "x.txt"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "app", "gen"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "gen", "out.txt"), []byte(renderStage(string(app))+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "INDEX"), []byte(renderStage(string(app))+"+"+renderStage(string(lib))+"\n"), 0o644)
}

func regenerateStage(_ context.Context, dir string, _ []string) error { return deriveStage(dir) }

type stageFixture struct {
	remote, dev, queue, apply string
	base                      string
}

// stageChange is one pull request's branch as the fixture opened it.
type stageChange struct {
	id, head, ref, branch string
}

func newStageFixture(t *testing.T) *stageFixture {
	t.Helper()
	// The drivers' own git calls read identity and config from the environment.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_COMMITTER_NAME", "queue-bot")
	t.Setenv("GIT_COMMITTER_EMAIL", "bot@example.invalid")
	root := t.TempDir()
	f := &stageFixture{remote: filepath.Join(root, "remote.git"), dev: filepath.Join(root, "dev")}
	stageGitIn(t, root, "init", "--quiet", "--bare", "-b", "main", f.remote)
	stageGitIn(t, root, "clone", "--quiet", f.remote, f.dev)
	f.write(t, map[string]string{"app/src.txt": "alpha\nbeta\ngamma\n", "lib/x.txt": "x\n",
		".gitattributes": "app/gen/** linguist-generated\nINDEX linguist-generated\n"})
	stageGitIn(t, f.dev, "add", "-A")
	stageGitIn(t, f.dev, "commit", "--quiet", "-m", "initial")
	stageGitIn(t, f.dev, "push", "--quiet", "origin", "HEAD:main")
	f.base = stageGitIn(t, f.dev, "rev-parse", "HEAD")
	f.queue, f.apply = filepath.Join(root, "queue"), filepath.Join(root, "apply")
	stageGitIn(t, root, "clone", "--quiet", f.remote, f.queue)
	stageGitIn(t, root, "clone", "--quiet", f.remote, f.apply)
	return f
}

// write edits files in the dev clone and regenerates, the way an author running the
// generator would.
func (f *stageFixture) write(t *testing.T, files map[string]string) {
	t.Helper()
	for p, body := range files {
		abs := filepath.Join(f.dev, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	require.NoError(t, deriveStage(f.dev))
}

func (f *stageFixture) change(t *testing.T, id, author, from string, files map[string]string) stageChange {
	t.Helper()
	stageGitIn(t, f.dev, "checkout", "--quiet", "-B", "pr"+id, from)
	f.write(t, files)
	stageGitIn(t, f.dev, "add", "-A")
	stageGitIn(t, f.dev, "-c", "user.name="+author, "commit", "--quiet", "--author", author+" <"+author+"@example.invalid>", "-m", "change "+id)
	stageGitIn(t, f.dev, "push", "--quiet", "--force", "origin", "pr"+id)
	return stageChange{id: id, head: stageGitIn(t, f.dev, "rev-parse", "HEAD"), ref: "refs/heads/pr" + id, branch: "pr" + id}
}

// merge pushes c's head to main as a fast-forward, the way a merge would leave it.
func (f *stageFixture) merge(t *testing.T, c stageChange) {
	t.Helper()
	stageGitIn(t, f.dev, "push", "--quiet", "origin", c.head+":refs/heads/main")
}

func (f *stageFixture) fetch(t *testing.T, root string, cs ...stageChange) {
	t.Helper()
	for _, c := range cs {
		require.NoError(t, gitVCS{}.FetchRevision(t.Context(), root, "origin", c.head, c.ref))
	}
}

func (f *stageFixture) stage(t *testing.T, onto string, c stageChange, regen func(context.Context, string, []string) error) (string, string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "stage-"+c.id)
	commit, err := gitVCS{}.BuildStage(t.Context(), f.queue, types.StageSpec{
		Dir: dir, Derived: stageDerived, Base: f.base, Onto: onto, Rev: c.head,
		Message: "stage #" + c.id, Author: stageAuthor, Regenerate: regen,
	})
	return commit, dir, err
}

func TestStageStacksChangesAndRegeneratesTheDerivedFileTheyBothChanged(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	f.fetch(t, f.queue, a, b)
	one, _, err := f.stage(t, f.base, a, regenerateStage)
	require.NoError(t, err)
	two, dir, err := f.stage(t, one, b, regenerateStage)
	require.NoError(t, err)
	assert.Equal(t, "alpha-a\nbeta\ngamma-b", stageGitIn(t, f.queue, "show", two+":app/src.txt"))
	assert.Equal(t, "ALPHA-A|BETA|GAMMA-B", stageGitIn(t, f.queue, "show", two+":app/gen/out.txt"), "regenerated, not merged")
	assert.DirExists(t, dir)
	require.NoError(t, gitVCS{}.RemoveStage(t.Context(), f.queue, dir))
	assert.NoDirExists(t, dir)

	again, _, err := f.stage(t, f.base, a, regenerateStage)
	require.NoError(t, err)
	assert.Equal(t, one, again, "the same stack on the same base is the same commit in any repository")
}

func TestAChangeCannotDeclareItsOwnSourcesDerived(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "a\n"})
	sneaky := f.change(t, "2", "eve", f.base, map[string]string{"lib/x.txt": "e\n", ".gitattributes": "app/gen/** linguist-generated\nINDEX linguist-generated\nlib/** linguist-generated\n"})
	f.fetch(t, f.queue, a, sneaky)
	one, _, err := f.stage(t, f.base, a, regenerateStage)
	require.NoError(t, err)
	_, dir, err := f.stage(t, one, sneaky, regenerateStage)
	var ce *types.MergeConflictError
	require.ErrorAs(t, err, &ce, "attributes are read at the base, not from the change")
	assert.Equal(t, []string{"lib/x.txt"}, ce.Paths)
	assert.NoDirExists(t, dir, "a failed stage removes its checkout")
}

func TestASourceConflictIsReportedWithTheCommitsThatCausedIt(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	c := f.change(t, "3", "cat", f.base, map[string]string{"app/src.txt": "alpha-c\nbeta\ngamma\n"})
	f.fetch(t, f.queue, a, c)
	one, _, err := f.stage(t, f.base, a, regenerateStage)
	require.NoError(t, err)

	err = gitVCS{}.CheckMerge(t.Context(), f.queue, stageDerived, one, c.head)
	var ce *types.MergeConflictError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, []string{"app/src.txt"}, ce.Paths, "the derived files are not a conflict")
	require.Len(t, ce.With, 1)
	assert.Contains(t, ce.With[0], "change 1", "the commit on the base side, not the staging merge")

	_, _, err = f.stage(t, one, c, regenerateStage)
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, []string{"app/src.txt"}, ce.Paths)
}

func TestARegenerationThatWritesASourceIsRefused(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	f.fetch(t, f.queue, a)
	_, _, err := f.stage(t, f.base, a, func(_ context.Context, dir string, _ []string) error {
		return os.WriteFile(filepath.Join(dir, "lib", "x.txt"), []byte("rewritten\n"), 0o644)
	})
	var nd *types.NotDerivedError
	require.ErrorAs(t, err, &nd)
	assert.Equal(t, []string{"lib/x.txt"}, nd.Paths)
}

func TestChangedSinceAndCommitSubjectsDescribeOnlyTheChangesOwnCommits(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	f.fetch(t, f.queue, a)
	paths, err := gitVCS{}.ChangedSince(t.Context(), f.queue, f.base, a.head)
	require.NoError(t, err)
	assert.Equal(t, []string{"INDEX", "lib/x.txt"}, paths)
	subjects, err := gitVCS{}.CommitSubjects(t.Context(), f.queue, f.base, a.head)
	require.NoError(t, err)
	assert.Equal(t, []string{"change 1"}, subjects)
}

func TestFetchBranchAndLookupRemote(t *testing.T) {
	f := newStageFixture(t)
	tip, err := gitVCS{}.FetchBranch(t.Context(), f.queue, "origin", "main")
	require.NoError(t, err)
	assert.Equal(t, f.base, tip)
	url, ok, err := gitVCS{}.LookupRemote(t.Context(), f.queue, "origin")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, f.remote, url)
	_, ok, err = gitVCS{}.LookupRemote(t.Context(), f.queue, "https://example.invalid/r.git")
	require.NoError(t, err, "a URL is not a configured remote, which is not an error")
	assert.False(t, ok)
}

// A FetchRevision whose ref moved past the revision falls back to fetching the revision.
func TestFetchRevisionFallsBackWhenTheRefMoved(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "a\n"})
	f.change(t, "1", "ann", a.head, map[string]string{"lib/x.txt": "a2\n"})
	require.NoError(t, gitVCS{}.FetchRevision(t.Context(), f.queue, "origin", a.head, a.ref))
	assert.True(t, stageHas(t.Context(), f.queue, a.head))
}

// Two disjoint changes both regenerate INDEX. #1 merges; #2's stage regenerated INDEX on
// the old base, so taking the stage's copy would lose #1's line from it, and the
// after-merge tree check would pass, since the prediction itself is what is wrong.
func TestPredictMergeRefusesADerivedFileThatWhatMergedAlsoRegenerated(t *testing.T) {
	f := newStageFixture(t)
	one := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "x-one\n"})
	two := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha-two\nbeta\ngamma\n"})
	f.fetch(t, f.queue, two)
	st, _, err := f.stage(t, f.base, two, regenerateStage)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "stage")
	require.NoError(t, gitVCS{}.ExportStage(t.Context(), f.queue, file, f.base, st))

	f.merge(t, one)
	require.NoError(t, gitVCS{}.ImportStage(t.Context(), f.apply, file))
	tip, err := gitVCS{}.FetchBranch(t.Context(), f.apply, "origin", "main")
	require.NoError(t, err)
	_, err = gitVCS{}.PredictMerge(t.Context(), f.apply, f.base, tip, f.base, st)
	var ce *types.MergeConflictError
	require.ErrorAs(t, err, &ce, "INDEX with both lines was never validated")
	assert.Equal(t, []string{"INDEX"}, ce.Paths)
}

// Within a partition the stage was built onto exactly what merged, so its derived files
// are right even where they conflict with the base.
func TestPredictMergeTakesTheStagesDerivedFilesWhenItWasBuiltOntoTheTip(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	f.fetch(t, f.queue, a, b)
	one, _, err := f.stage(t, f.base, a, regenerateStage)
	require.NoError(t, err)
	two, _, err := f.stage(t, one, b, regenerateStage)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "stage")
	require.NoError(t, gitVCS{}.ExportStage(t.Context(), f.queue, file, f.base, two))

	stageGitIn(t, f.queue, "push", "--quiet", "origin", one+":refs/heads/main")
	require.NoError(t, gitVCS{}.ImportStage(t.Context(), f.apply, file))
	tip, err := gitVCS{}.FetchBranch(t.Context(), f.apply, "origin", "main")
	require.NoError(t, err)
	tree, err := gitVCS{}.PredictMerge(t.Context(), f.apply, f.base, tip, one, two)
	require.NoError(t, err)
	want, err := gitVCS{}.TreeOf(t.Context(), f.apply, two)
	require.NoError(t, err)
	assert.Equal(t, want, tree)
}

// An update commit a queue pushed, or the base merged in by the author, adds nothing a
// reviewer did not see, so a review of the commit beneath it covers it.
func TestReviewTargetSeesThroughAMergeOfTheBase(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	other := f.change(t, "9", "ola", f.base, map[string]string{"lib/x.txt": "x-9\n"})
	f.merge(t, other)

	stageGitIn(t, f.dev, "checkout", "--quiet", "pr1")
	stageGitIn(t, f.dev, "fetch", "--quiet", "origin", "main")
	// INDEX conflicts, as a derived file does; the author regenerates it.
	cmd := exec.Command("git", "merge", "--quiet", "--no-edit", "origin/main")
	cmd.Dir, cmd.Env = f.dev, append(os.Environ(), stageFixtureEnv...)
	_ = cmd.Run()
	require.NoError(t, deriveStage(f.dev))
	stageGitIn(t, f.dev, "add", "-A")
	stageGitIn(t, f.dev, "commit", "--quiet", "--no-edit")
	merged := stageGitIn(t, f.dev, "rev-parse", "HEAD")
	f.write(t, map[string]string{"lib/x.txt": "sneaked in\n"})
	stageGitIn(t, f.dev, "add", "-A")
	stageGitIn(t, f.dev, "commit", "--quiet", "--amend", "--no-edit")
	evil := stageGitIn(t, f.dev, "rev-parse", "HEAD")
	stageGitIn(t, f.dev, "push", "--quiet", "--force", "origin", "pr1", merged+":refs/heads/merged")

	tip, err := gitVCS{}.FetchBranch(t.Context(), f.apply, "origin", "main")
	require.NoError(t, err)
	stageGitIn(t, f.apply, "fetch", "--quiet", "origin", "pr1", "merged")
	got, err := gitVCS{}.ReviewTarget(t.Context(), f.apply, stageDerived, tip, merged)
	require.NoError(t, err)
	assert.Equal(t, a.head, got)
	got, err = gitVCS{}.ReviewTarget(t.Context(), f.apply, stageDerived, tip, evil)
	require.NoError(t, err)
	assert.Equal(t, evil, got, "a merge that also edits a source is its own change")
}

func TestUpdateBranchNeverRecreatesADeletedBranch(t *testing.T) {
	f := newStageFixture(t)
	one := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "x-one\n"})
	two := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha-two\nbeta\ngamma\n"})
	f.merge(t, one)
	tip, err := gitVCS{}.FetchBranch(t.Context(), f.queue, "origin", "main")
	require.NoError(t, err)
	f.fetch(t, f.queue, two)
	st, _, err := f.stage(t, tip, two, regenerateStage)
	require.NoError(t, err)
	tree, err := gitVCS{}.TreeOf(t.Context(), f.queue, st)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "stage")
	require.NoError(t, gitVCS{}.ExportStage(t.Context(), f.queue, file, f.base, st))

	f.fetch(t, f.apply, two)
	stageGitIn(t, f.dev, "push", "--quiet", "origin", ":pr2")
	_, err = gitVCS{}.FetchBranch(t.Context(), f.apply, "origin", "main")
	require.NoError(t, err)
	require.NoError(t, gitVCS{}.ImportStage(t.Context(), f.apply, file))
	_, err = gitVCS{}.UpdateBranch(t.Context(), f.apply, "origin", types.BranchUpdate{
		Derived: stageDerived, Base: f.base, Tip: tip, Head: two.head, Branch: two.branch, Tree: tree, Message: "update",
	})
	require.ErrorIs(t, err, types.ErrBranchMoved)
	assert.NotContains(t, stageGitIn(t, f.dev, "ls-remote", "--heads", "origin"), "pr2")
}

func TestUpdateBranchPushesAnUpdateAuthoredByTheHeadsAuthor(t *testing.T) {
	f := newStageFixture(t)
	one := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	two := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	f.merge(t, one)
	tip, err := gitVCS{}.FetchBranch(t.Context(), f.queue, "origin", "main")
	require.NoError(t, err)
	f.fetch(t, f.queue, two)
	st, _, err := f.stage(t, tip, two, regenerateStage)
	require.NoError(t, err)
	tree, err := gitVCS{}.TreeOf(t.Context(), f.queue, st)
	require.NoError(t, err)

	u := types.BranchUpdate{Derived: stageDerived, Base: f.base, Tip: tip, Head: two.head, Tree: tree, Message: "update"}
	_, err = gitVCS{}.UpdateBranch(t.Context(), f.queue, "origin", u)
	require.ErrorIs(t, err, types.ErrNoBranch, "INDEX needs regenerating and there is no branch to push it to")

	u.Branch = two.branch
	update, err := gitVCS{}.UpdateBranch(t.Context(), f.queue, "origin", u)
	require.NoError(t, err)
	pushed := stageGitIn(t, f.dev, "ls-remote", "origin", "refs/heads/pr2")
	assert.True(t, strings.HasPrefix(pushed, update), pushed)
	assert.Equal(t, "bob|queue-bot|"+two.head+" "+tip, stageGitIn(t, f.queue, "log", "-1", "--format=%an|%cn|%P", update))
}

func TestUpdateBranchRefusesATreeThatDiffersOutsideDerivedFiles(t *testing.T) {
	f := newStageFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	evil := f.change(t, "9", "eve", f.base, map[string]string{"lib/x.txt": "y\n", "lib/extra.txt": "surprise\n"})
	f.fetch(t, f.apply, a, evil)
	tree, err := gitVCS{}.TreeOf(t.Context(), f.apply, evil.head)
	require.NoError(t, err)
	_, err = gitVCS{}.UpdateBranch(t.Context(), f.apply, "origin", types.BranchUpdate{
		Derived: stageDerived, Base: f.base, Tip: f.base, Head: a.head, Branch: a.branch, Tree: tree, Message: "update",
	})
	var nd *types.NotDerivedError
	require.ErrorAs(t, err, &nd)
	assert.Equal(t, []string{"lib/extra.txt"}, nd.Paths)
}

// A GIT_DIR exported by a hook must not send a stage's git calls into another repository.
func TestStagingIgnoresARedirectingEnvironment(t *testing.T) {
	f := newStageFixture(t)
	bystander := t.TempDir()
	stageGitIn(t, bystander, "init", "--quiet")
	t.Setenv("GIT_DIR", filepath.Join(bystander, ".git"))
	t.Setenv("GIT_WORK_TREE", bystander)
	tip, err := gitVCS{}.FetchBranch(t.Context(), f.queue, "origin", "main")
	require.NoError(t, err)
	assert.Equal(t, f.base, tip)
	out, err := exec.Command("git", "--git-dir", filepath.Join(bystander, ".git"), "for-each-ref").Output()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "the bystander gained no refs")
}

func TestStagingRefusesARevisionThatReadsAsAnOption(t *testing.T) {
	f := newStageFixture(t)
	err := gitVCS{}.FetchRevision(t.Context(), f.queue, "origin", "--upload-pack=evil", "")
	require.Error(t, err)
	_, err = gitVCS{}.TreeOf(t.Context(), f.queue, "--output=x")
	require.Error(t, err)
}
