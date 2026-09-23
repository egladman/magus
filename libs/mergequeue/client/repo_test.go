package client

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

	"github.com/egladman/magus/libs/mergequeue"
)

// Every fixture has two projects, app and lib, written by hand, and two derived files:
// app/gen/out.txt from app's source, and INDEX, one line from both, which any change
// to either regenerates, the way a root routing index is. The base's .gitattributes
// marks both derived. The version-control operations themselves are tested in magus's
// vcs package; these pin what the queue sees of them.

var fixtureEnv = []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fixtureEnv...)
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
	// The Repo's own git calls read identity and config from the environment.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_COMMITTER_NAME", "queue-bot")
	t.Setenv("GIT_COMMITTER_EMAIL", "bot@example.invalid")
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

func (f *fixture) repo(t *testing.T, root string) *Repo {
	t.Helper()
	r, err := NewRepo(t.Context(), Config{Root: root, Remote: "origin", Scratch: t.TempDir()})
	require.NoError(t, err)
	return r
}

// merge pushes c's head to main as a fast-forward, the way a merge would leave it.
func (f *fixture) merge(t *testing.T, c mergequeue.Change) {
	t.Helper()
	gitIn(t, f.dev, "push", "--quiet", "origin", c.Head+":refs/heads/main")
}

func TestNewRepoRefusesWhatIsNoCheckout(t *testing.T) {
	_, err := NewRepo(t.Context(), Config{Root: t.TempDir(), Remote: "origin"})
	require.Error(t, err)
	_, err = NewRepo(t.Context(), Config{Root: t.TempDir()})
	require.EqualError(t, err, "a Repo needs a Root and a Remote")
}

// A backend that declares the staging capabilities but cannot perform them is refused
// when the Repo is built, named, rather than on the first stage.
func TestNewRepoRefusesABackendThatCannotStage(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".jj"), 0o755))
	_, err := NewRepo(t.Context(), Config{Root: root, Remote: "origin"})
	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.ErrorContains(t, err, "jj does not support RevisionFetcher.LookupRemote")
}

func TestRemoteURLNamesAConfiguredRemoteOrTheValueAsGiven(t *testing.T) {
	f := newFixture(t)
	url, err := f.repo(t, f.queue).RemoteURL(t.Context())
	require.NoError(t, err)
	assert.Equal(t, f.remote, url)

	byURL, err := NewRepo(t.Context(), Config{Root: f.queue, Remote: f.remote})
	require.NoError(t, err)
	url, err = byURL.RemoteURL(t.Context())
	require.NoError(t, err)
	assert.Equal(t, f.remote, url)
}

func TestConflictsAndRefusalsReachTheQueueAsItsOwnErrors(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	c := f.change(t, "3", "cat", f.base, map[string]string{"app/src.txt": "alpha-c\nbeta\ngamma\n"})
	r := f.repo(t, f.queue)
	ctx := t.Context()
	require.NoError(t, r.FetchHead(ctx, a))
	require.NoError(t, r.FetchHead(ctx, c))
	one, err := r.Stage(ctx, f.base, f.base, a, regenerate)
	require.NoError(t, err)

	var ce *mergequeue.ConflictError
	require.ErrorAs(t, r.CheckMerge(ctx, one.Commit, c), &ce)
	assert.Equal(t, c.ID, ce.Conflict.Change.ID)
	assert.Equal(t, []string{"app/src.txt"}, ce.Conflict.Paths)
	_, err = r.Stage(ctx, f.base, one.Commit, c, regenerate)
	require.ErrorAs(t, err, &ce)

	_, err = r.Stage(ctx, f.base, f.base, a, func(_ context.Context, dir, _ string, _ mergequeue.Change, _ []string) error {
		return os.WriteFile(filepath.Join(dir, "lib", "x.txt"), []byte("rewritten\n"), 0o644)
	})
	var refused *mergequeue.RefusedError
	require.ErrorAs(t, err, &refused, "the change is kicked back, the run goes on")
	assert.Equal(t, "regeneration wrote files that are not derived: lib/x.txt", refused.Reason)
}

func TestUpdateBranchRefusalsReachTheQueueAsKickBacksAndWaits(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	evil := f.change(t, "9", "eve", f.base, map[string]string{"lib/x.txt": "y\n", "lib/extra.txt": "surprise\n"})
	r := f.repo(t, f.apply)
	ctx := t.Context()
	require.NoError(t, r.FetchHead(ctx, a))
	require.NoError(t, r.FetchHead(ctx, evil))
	tree, err := r.TreeOf(ctx, evil.Head)
	require.NoError(t, err)
	_, err = r.UpdateBranch(ctx, f.base, f.base, a, tree)
	var refused *mergequeue.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Reason, "lib/extra.txt")
}

func TestChangedAndSquashMessageDescribeOnlyTheChangesOwnCommits(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	r := f.repo(t, f.queue)
	require.NoError(t, r.FetchHead(t.Context(), a))
	paths, err := r.Changed(t.Context(), f.base, a.Head)
	require.NoError(t, err)
	assert.Equal(t, []string{"INDEX", "lib/x.txt"}, paths)
	msg, err := r.SquashMessage(t.Context(), f.base, a.Head)
	require.NoError(t, err)
	assert.Equal(t, "* change 1", msg)
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

// End to end over real git: planning admits two changes, validation stages them
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
	ctx := t.Context()

	stager := f.repo(t, f.queue)
	planner := mergequeue.NewPlanner(stager)
	planner.Provider, planner.Depth = host, 2
	plan, err := planner.Run(ctx, mergequeue.Changes{Schema: mergequeue.SchemaChanges, Base: "main", Changes: []mergequeue.Change{a, b}})
	require.NoError(t, err)
	require.Equal(t, [][]string{{"1", "2"}}, [][]string{{plan.Partitions[0][0].ID, plan.Partitions[0][1].ID}})

	dir := &mergequeue.VerdictDir{Path: t.TempDir(), Export: stager.ExportStage}
	v := mergequeue.NewValidator(stager, greenGate{}, dir)
	v.Regenerate = regenerate
	require.NoError(t, v.Run(ctx, plan))
	require.NoError(t, dir.MarkDone())
	f2, err := os.Open(filepath.Join(dir.Path, "2", mergequeue.VerdictFile))
	require.NoError(t, err)
	two, err := mergequeue.ReadVerdict(f2)
	require.NoError(t, f2.Close())
	require.NoError(t, err)

	mr, err := NewRepo(ctx, Config{Root: f.apply, Remote: "origin"})
	require.NoError(t, err)
	require.NoError(t, mergequeue.NewApplier(host, mr, &mergequeue.VerdictDir{Path: dir.Path, Follow: true}).Run(ctx, plan))
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
