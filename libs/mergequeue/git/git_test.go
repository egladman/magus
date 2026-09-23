package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue"
	"github.com/egladman/magus/libs/mergequeue/verdicts"
)

// Every fixture has two projects, app and lib, written by hand, and two derived files:
// app/gen/out.txt from app's source, and INDEX, one line from both, which any change
// to either regenerates, the way a root routing index is. The base's .gitattributes
// marks both derived.

var gitEnv = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitEnv...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func render(src string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(src), "\n", "|"))
}

// derive rewrites both derived files in dir from its sources.
func derive(dir string) error {
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
	if err := os.WriteFile(filepath.Join(dir, "app", "gen", "out.txt"), []byte(render(string(app))+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "INDEX"), []byte(render(string(app))+"+"+render(string(lib))+"\n"), 0o644)
}

// regenerate is what the regenerate hook does for this fixture.
func regenerate(_ context.Context, dir, _ string, _ mergequeue.Change, _ []string) error {
	return derive(dir)
}

type fixture struct {
	remote, dev, queue, apply string
	base                      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{remote: filepath.Join(root, "remote.git"), dev: filepath.Join(root, "dev")}
	gitIn(t, root, "init", "--quiet", "--bare", "-b", "main", f.remote)
	gitIn(t, root, "clone", "--quiet", f.remote, f.dev)
	f.write(t, map[string]string{"app/src.txt": "alpha\nbeta\ngamma\n", "lib/x.txt": "x\n",
		".gitattributes": "app/gen/** linguist-generated\nINDEX linguist-generated\n"})
	gitIn(t, f.dev, "add", "-A")
	gitIn(t, f.dev, "commit", "--quiet", "-m", "initial")
	gitIn(t, f.dev, "push", "--quiet", "origin", "HEAD:main")
	f.base = gitIn(t, f.dev, "rev-parse", "HEAD")
	f.queue, f.apply = filepath.Join(root, "queue"), filepath.Join(root, "apply")
	gitIn(t, root, "clone", "--quiet", f.remote, f.queue)
	gitIn(t, root, "clone", "--quiet", f.remote, f.apply)
	return f
}

// write edits files in the dev clone and regenerates, the way an author running the
// generator would.
func (f *fixture) write(t *testing.T, files map[string]string) {
	t.Helper()
	for p, body := range files {
		abs := filepath.Join(f.dev, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	require.NoError(t, derive(f.dev))
}

// change opens a pull request's branch from base: returns the change as a provider lists it.
func (f *fixture) change(t *testing.T, id, author, from string, files map[string]string) mergequeue.Change {
	t.Helper()
	gitIn(t, f.dev, "checkout", "--quiet", "-B", "pr"+id, from)
	f.write(t, files)
	gitIn(t, f.dev, "add", "-A")
	gitIn(t, f.dev, "-c", "user.name="+author, "commit", "--quiet", "--author", author+" <"+author+"@example.invalid>", "-m", "change "+id)
	gitIn(t, f.dev, "push", "--quiet", "--force", "origin", "pr"+id)
	return mergequeue.Change{ID: id, Head: gitIn(t, f.dev, "rev-parse", "HEAD"), Ref: "refs/heads/pr" + id,
		Branch: "pr" + id, Base: "main", Title: "change " + id, Author: author, Affected: []string{"app"}}
}

var repoEnv = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	"GIT_COMMITTER_NAME=queue-bot", "GIT_COMMITTER_EMAIL=bot@example.invalid"}

func (f *fixture) staging(t *testing.T) *StagingRepo {
	t.Helper()
	r, err := NewStagingRepo(Config{Root: f.queue, Remote: "origin", Env: repoEnv}, t.TempDir(), regenerate)
	require.NoError(t, err)
	return r
}

func (f *fixture) merging(t *testing.T) *MergingRepo {
	t.Helper()
	r, err := NewMergingRepo(Config{Root: f.apply, Remote: "origin", Env: repoEnv})
	require.NoError(t, err)
	return r
}

func show(t *testing.T, dir, rev, path string) string {
	return gitIn(t, dir, "show", rev+":"+path)
}

func TestStageStacksChangesAndRegeneratesTheDerivedFileTheyBothChanged(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	r := f.staging(t)
	ctx := context.Background()
	require.NoError(t, r.FetchHead(ctx, a))
	require.NoError(t, r.FetchHead(ctx, b))
	one, err := r.Stage(ctx, f.base, f.base, a)
	require.NoError(t, err)
	two, err := r.Stage(ctx, f.base, one.Commit, b)
	require.NoError(t, err)
	assert.Equal(t, "alpha-a\nbeta\ngamma-b", show(t, f.queue, two.Commit, "app/src.txt"))
	assert.Equal(t, "ALPHA-A|BETA|GAMMA-B", show(t, f.queue, two.Commit, "app/gen/out.txt"), "regenerated, not merged")
	assert.DirExists(t, two.Dir)
	require.NoError(t, r.Discard(ctx, two))
	assert.NoDirExists(t, two.Dir)

	again := f.staging(t)
	one2, err := again.Stage(ctx, f.base, f.base, a)
	require.NoError(t, err)
	assert.Equal(t, one.Commit, one2.Commit, "the same stack on the same base is the same commit in any job")
}

func TestAChangeCannotDeclareItsOwnSourcesDerived(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "a\n"})
	sneaky := f.change(t, "2", "eve", f.base, map[string]string{"lib/x.txt": "e\n", ".gitattributes": "app/gen/** linguist-generated\nINDEX linguist-generated\nlib/** linguist-generated\n"})
	r := f.staging(t)
	ctx := context.Background()
	require.NoError(t, r.FetchHead(ctx, a))
	require.NoError(t, r.FetchHead(ctx, sneaky))
	one, err := r.Stage(ctx, f.base, f.base, a)
	require.NoError(t, err)
	_, err = r.Stage(ctx, f.base, one.Commit, sneaky)
	var ce *mergequeue.ConflictError
	require.ErrorAs(t, err, &ce, "attributes are read at the base, not from the change")
	assert.Equal(t, []string{"lib/x.txt"}, ce.Conflict.Paths)
}

func TestASourceConflictIsReportedWithTheCommitsThatCausedIt(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	c := f.change(t, "3", "cat", f.base, map[string]string{"app/src.txt": "alpha-c\nbeta\ngamma\n"})
	r := f.staging(t)
	ctx := context.Background()
	require.NoError(t, r.FetchHead(ctx, a))
	require.NoError(t, r.FetchHead(ctx, c))
	one, err := r.Stage(ctx, f.base, f.base, a)
	require.NoError(t, err)

	err = r.CheckMerge(ctx, one.Commit, c)
	var ce *mergequeue.ConflictError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, []string{"app/src.txt"}, ce.Conflict.Paths, "the derived files are not a conflict")
	require.Len(t, ce.Conflict.With, 1)
	assert.Contains(t, ce.Conflict.With[0], "change 1", "the commit on the base side, not the staging merge")

	_, err = r.Stage(ctx, f.base, one.Commit, c)
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, []string{"app/src.txt"}, ce.Conflict.Paths)
}

func TestARegenerationThatWritesASourceIsRefused(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	r, err := NewStagingRepo(Config{Root: f.queue, Remote: "origin", Env: repoEnv}, t.TempDir(),
		func(_ context.Context, dir, _ string, _ mergequeue.Change, _ []string) error {
			return os.WriteFile(filepath.Join(dir, "lib", "x.txt"), []byte("rewritten\n"), 0o644)
		})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, r.FetchHead(ctx, a))
	_, err = r.Stage(ctx, f.base, f.base, a)
	var refused *mergequeue.RefusedError
	require.ErrorAs(t, err, &refused, "the change is kicked back, the run goes on")
	assert.Equal(t, "regeneration wrote files that are not derived: lib/x.txt", refused.Reason)
}

func TestChangedAndSquashMessageDescribeOnlyTheChangesOwnCommits(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	r := f.staging(t)
	ctx := context.Background()
	require.NoError(t, r.FetchHead(ctx, a))
	paths, err := r.Changed(ctx, f.base, a.Head)
	require.NoError(t, err)
	assert.Equal(t, []string{"INDEX", "lib/x.txt"}, paths)
	msg, err := r.SquashMessage(ctx, f.base, a.Head)
	require.NoError(t, err)
	assert.Equal(t, "* change 1", msg)
}

// merge pushes c's head to main as a fast-forward, the way a merge would leave it.
func (f *fixture) merge(t *testing.T, c mergequeue.Change) {
	t.Helper()
	gitIn(t, f.dev, "push", "--quiet", "origin", c.Head+":refs/heads/main")
}

// Two disjoint partitions both regenerate INDEX. #1 merges; #2's stage regenerated INDEX
// on the old base, so taking the stage's copy would lose #1's line from it, and the
// after-merge tree check would pass, since the prediction itself is what is wrong.
// Before, Predict took the stage's side of every derived conflict.
func TestPredictRefusesADerivedFileThatWhatMergedAlsoRegenerated(t *testing.T) {
	f := newFixture(t)
	one := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "x-one\n"})
	two := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha-two\nbeta\ngamma\n"})
	ctx := context.Background()
	stager := f.staging(t)
	require.NoError(t, stager.FetchHead(ctx, two))
	st, err := stager.Stage(ctx, f.base, f.base, two)
	require.NoError(t, err)
	bundle := filepath.Join(t.TempDir(), "stage.bundle")
	require.NoError(t, stager.Export(ctx, bundle, f.base, st.Commit))

	f.merge(t, one)
	mr := f.merging(t)
	require.NoError(t, mr.ImportBundle(ctx, bundle))
	tip, err := mr.FetchTip(ctx, "main")
	require.NoError(t, err)
	_, err = mr.Predict(ctx, f.base, tip, f.base, st.Commit)
	var ce *mergequeue.ConflictError
	require.ErrorAs(t, err, &ce, "INDEX with both lines was never validated")
	assert.Equal(t, []string{"INDEX"}, ce.Conflict.Paths)
}

// Within a partition the stage was built onto exactly what merged, so its derived files
// are right even where they conflict with the base.
func TestPredictTakesTheStagesDerivedFilesWhenItWasBuiltOntoTheTip(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	ctx := context.Background()
	stager := f.staging(t)
	require.NoError(t, stager.FetchHead(ctx, a))
	require.NoError(t, stager.FetchHead(ctx, b))
	one, err := stager.Stage(ctx, f.base, f.base, a)
	require.NoError(t, err)
	two, err := stager.Stage(ctx, f.base, one.Commit, b)
	require.NoError(t, err)
	bundle := filepath.Join(t.TempDir(), "stage.bundle")
	require.NoError(t, stager.Export(ctx, bundle, f.base, two.Commit))

	gitIn(t, f.queue, "push", "--quiet", "origin", one.Commit+":refs/heads/main")
	mr := f.merging(t)
	require.NoError(t, mr.ImportBundle(ctx, bundle))
	tip, err := mr.FetchTip(ctx, "main")
	require.NoError(t, err)
	tree, err := mr.Predict(ctx, f.base, tip, one.Commit, two.Commit)
	require.NoError(t, err)
	want, err := mr.TreeOf(ctx, two.Commit)
	require.NoError(t, err)
	assert.Equal(t, want, tree)
}

// An update commit the queue pushed, or the base merged in by the author, adds nothing
// a reviewer did not see, so a review of the commit beneath it covers it. Before, an
// update commit left behind by a refused merge was a head nobody had approved, and the
// change waited forever.
func TestReviewTargetSeesThroughAMergeOfTheBase(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	other := f.change(t, "9", "ola", f.base, map[string]string{"lib/x.txt": "x-9\n"})
	f.merge(t, other)

	gitIn(t, f.dev, "checkout", "--quiet", "pr1")
	gitIn(t, f.dev, "fetch", "--quiet", "origin", "main")
	// INDEX conflicts, as a derived file does; the author regenerates it.
	cmd := exec.Command("git", "merge", "--quiet", "--no-edit", "origin/main")
	cmd.Dir, cmd.Env = f.dev, append(os.Environ(), gitEnv...)
	_ = cmd.Run()
	require.NoError(t, derive(f.dev))
	gitIn(t, f.dev, "add", "-A")
	gitIn(t, f.dev, "commit", "--quiet", "--no-edit")
	merged := gitIn(t, f.dev, "rev-parse", "HEAD")
	f.write(t, map[string]string{"lib/x.txt": "sneaked in\n"})
	gitIn(t, f.dev, "add", "-A")
	gitIn(t, f.dev, "commit", "--quiet", "--amend", "--no-edit")
	evil := gitIn(t, f.dev, "rev-parse", "HEAD")
	gitIn(t, f.dev, "push", "--quiet", "--force", "origin", "pr1", merged+":refs/heads/merged")

	r := f.merging(t)
	ctx := context.Background()
	tip, err := r.FetchTip(ctx, "main")
	require.NoError(t, err)
	gitIn(t, f.apply, "fetch", "--quiet", "origin", "pr1", "merged")
	got, err := r.ReviewTarget(ctx, tip, merged)
	require.NoError(t, err)
	assert.Equal(t, a.Head, got)
	got, err = r.ReviewTarget(ctx, tip, evil)
	require.NoError(t, err)
	assert.Equal(t, evil, got, "a merge that also edits a source is its own change")
}

func TestUpdateBranchNeverRecreatesADeletedBranch(t *testing.T) {
	f := newFixture(t)
	one := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "x-one\n"})
	two := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha-two\nbeta\ngamma\n"})
	f.merge(t, one)
	ctx := context.Background()
	stager := f.staging(t)
	tip, err := stager.FetchTip(ctx, "main")
	require.NoError(t, err)
	require.NoError(t, stager.FetchHead(ctx, two))
	st, err := stager.Stage(ctx, f.base, tip, two)
	require.NoError(t, err)
	tree, err := stager.TreeOf(ctx, st.Commit)
	require.NoError(t, err)
	bundle := filepath.Join(t.TempDir(), "stage.bundle")
	require.NoError(t, stager.Export(ctx, bundle, f.base, st.Commit))

	mr := f.merging(t)
	require.NoError(t, mr.FetchHead(ctx, two))
	gitIn(t, f.dev, "push", "--quiet", "origin", ":pr2")
	_, err = mr.FetchTip(ctx, "main")
	require.NoError(t, err)
	require.NoError(t, mr.ImportBundle(ctx, bundle))
	_, err = mr.UpdateBranch(ctx, f.base, tip, two, tree)
	var wait *mergequeue.WaitError
	require.ErrorAs(t, err, &wait)
	assert.NotContains(t, gitIn(t, f.dev, "ls-remote", "--heads", "origin"), "pr2")
}

// fakeHost approves everything and merges the way GitHub squashes: one commit per
// change, authored by the change's author, on the remote's main.
type fakeHost struct {
	t      *testing.T
	merger string
	merged []string
}

func (h *fakeHost) ListChanges(context.Context, mergequeue.ListQuery) ([]mergequeue.Change, error) {
	return nil, nil
}

func (h *fakeHost) ApprovalAt(_ context.Context, c mergequeue.Change, _ string) (mergequeue.Approval, error) {
	return mergequeue.Approval{Approved: true, Head: c.Head}, nil
}

func (h *fakeHost) PostStatus(context.Context, mergequeue.Change, string, mergequeue.CommitStatus) error {
	return nil
}

func (h *fakeHost) KickBack(context.Context, mergequeue.Change, string, string) error { return nil }

func (h *fakeHost) MergeChange(_ context.Context, c mergequeue.Change, commit, message string) error {
	gitIn(h.t, h.merger, "fetch", "--quiet", "origin", "main", c.Ref)
	gitIn(h.t, h.merger, "checkout", "--quiet", "-B", "main", "origin/main")
	gitIn(h.t, h.merger, "merge", "--quiet", "--squash", commit)
	gitIn(h.t, h.merger, "commit", "--quiet", "--author", c.Author+" <"+c.Author+"@example.invalid>",
		"-m", c.Title+" (#"+c.ID+")", "-m", message)
	gitIn(h.t, h.merger, "push", "--quiet", "origin", "main")
	h.merged = append(h.merged, c.ID)
	return nil
}

type greenGate struct{}

func (greenGate) Validate(context.Context, mergequeue.Stage, string, mergequeue.Change) (mergequeue.GateResult, error) {
	return mergequeue.GateResult{Green: true}, nil
}

// End to end over real git, living here because only this package can drive both repos
// against one fixture: planning admits two changes, validation stages them
// speculatively and exports each green stage as it is decided; a separate checkout that
// never builds anything imports the stages and merges both, each as its own squash commit
// by its own author, the second through a pushed regeneration, and main ends on the
// validated tree.
func TestValidatedChangesMergeOneCommitEachAndMainCarriesTheValidatedTree(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	merger := filepath.Join(t.TempDir(), "merger")
	gitIn(t, filepath.Dir(merger), "clone", "--quiet", f.remote, merger)
	host := &fakeHost{t: t, merger: merger}
	ctx := context.Background()

	stager := f.staging(t)
	planner := mergequeue.NewPlanner(stager)
	planner.Provider, planner.Depth = host, 2
	plan, err := planner.Run(ctx, mergequeue.Changes{Schema: mergequeue.SchemaChanges, Base: "main", Changes: []mergequeue.Change{a, b}})
	require.NoError(t, err)
	require.Equal(t, [][]string{{"1", "2"}}, [][]string{{plan.Partitions[0][0].ID, plan.Partitions[0][1].ID}})

	dir := &verdicts.Dir{Path: t.TempDir(), Export: stager.Export}
	require.NoError(t, mergequeue.NewValidator(stager, greenGate{}, dir).Run(ctx, plan))
	require.NoError(t, dir.MarkDone())
	f2, err := os.Open(filepath.Join(dir.Path, "2", verdicts.VerdictFile))
	require.NoError(t, err)
	two, err := mergequeue.ReadVerdict(f2)
	require.NoError(t, f2.Close())
	require.NoError(t, err)

	mr := f.merging(t)
	require.NoError(t, mergequeue.NewApplier(host, mr, &verdicts.Dir{Path: dir.Path, Follow: true}).Run(ctx, plan))
	assert.Equal(t, []string{"1", "2"}, host.merged)

	log := gitIn(t, merger, "log", "--format=%an|%s", f.base+"..origin/main")
	assert.Equal(t, "bob|change 2 (#2)\nann|change 1 (#1)", log, "one commit per change, each by its author")
	assert.Equal(t, gitIn(t, f.queue, "rev-parse", two.Stage+"^{tree}"), gitIn(t, merger, "rev-parse", "origin/main^{tree}"))

	// #2 conflicted with #1 in the derived files, so its regeneration was pushed to its
	// branch: authored by bob, with the bot only as committer.
	pushed := gitIn(t, merger, "log", "-1", "--format=%an|%cn|%P", "origin/pr2")
	assert.True(t, strings.HasPrefix(pushed, "bob|queue-bot|"+b.Head+" "), pushed)
	assert.NoDirExists(t, filepath.Join(f.apply, ".git", "worktrees"), "applying built nothing")
}

func TestUpdateBranchRefusesATreeThatDiffersOutsideDerivedFiles(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	evil := f.change(t, "9", "eve", f.base, map[string]string{"lib/x.txt": "y\n", "lib/extra.txt": "surprise\n"})
	r := f.merging(t)
	ctx := context.Background()
	require.NoError(t, r.FetchHead(ctx, a))
	require.NoError(t, r.FetchHead(ctx, evil))
	tree, err := r.TreeOf(ctx, evil.Head)
	require.NoError(t, err)
	_, err = r.UpdateBranch(ctx, f.base, f.base, a, tree)
	var refused *mergequeue.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Reason, "lib/extra.txt")
}
