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

// Every fixture has two projects, app and lib, written by hand, and two generated files:
// app/gen/out.txt from app's source, and INDEX, one line from both, which any change
// to either regenerates, the way a root routing index is. The base's .gitattributes
// marks both generated. The version-control operations themselves are tested in magus's
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

// derive rewrites both generated files in dir from its sources.
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
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
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
		Branch: "pr" + id, Base: "main", Title: "change " + id, Author: author, Method: mergequeue.MethodSquash, Affected: []string{"app"}}
}

func (f *fixture) repo(t *testing.T, root string) *Repo {
	t.Helper()
	r, err := NewRepo(t.Context(), Config{Root: root, Remote: "origin"})
	require.NoError(t, err)
	return r
}

func TestNewRepoRefusesWhatIsNoCheckout(t *testing.T) {
	_, err := NewRepo(t.Context(), Config{Root: t.TempDir(), Remote: "origin"})
	require.Error(t, err)
	_, err = NewRepo(t.Context(), Config{Root: t.TempDir()})
	require.EqualError(t, err, "a Repo needs a Root and a Remote")
}

// A backend that declares the capabilities but cannot perform them is refused when the
// Repo is built, named, rather than on the first candidate.
func TestNewRepoRefusesABackendThatCannotServeTheQueue(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".jj"), 0o755))
	_, err := NewRepo(t.Context(), Config{Root: root, Remote: "origin"})
	require.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestARemoteIsAConfiguredNameNeverAURL(t *testing.T) {
	f := newFixture(t)
	url, err := f.repo(t, f.queue).RemoteURL(t.Context())
	require.NoError(t, err)
	assert.Equal(t, f.remote, url)

	_, err = NewRepo(t.Context(), Config{Root: f.queue, Remote: f.remote})
	require.ErrorContains(t, err, "is configured")
}

func TestRangeFilesAndTreesReadTheCheckout(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	r := f.repo(t, f.queue)
	ctx := t.Context()
	require.NoError(t, r.FetchCommit(ctx, a.Head))
	paths, err := r.RangeFiles(ctx, f.base, a.Head)
	require.NoError(t, err)
	assert.Equal(t, []string{"INDEX", "lib/x.txt"}, paths)
	tip, err := r.FetchRef(ctx, "refs/heads/main")
	require.NoError(t, err)
	assert.Equal(t, f.base, tip)
	c, err := r.FindCommit(ctx, a.Head)
	require.NoError(t, err)
	assert.Equal(t, mergequeue.Commit{ID: a.Head, Parents: []string{f.base}, Author: mergequeue.Person{Name: "ann", Email: "ann@example.invalid"}, Subject: "change 1"}, c)
}

// fakeHost approves everything and merges the way GitHub squashes: one commit per
// change, authored by the change's author, on the remote's main.
type fakeHost struct {
	t      *testing.T
	merger string
	merged []string
}

func (h *fakeHost) Describe(context.Context, mergequeue.ListQuery) (mergequeue.Capabilities, error) {
	return mergequeue.Capabilities{StackMerge: mergequeue.StackMergeSequential, Methods: []mergequeue.MergeMethod{mergequeue.MethodSquash}}, nil
}

func (h *fakeHost) ListChanges(context.Context, mergequeue.ListQuery) (mergequeue.Changes, error) {
	return mergequeue.Changes{}, nil
}

func (h *fakeHost) ApprovalAt(_ context.Context, c mergequeue.Change, _ string) (mergequeue.Approval, error) {
	return mergequeue.Approval{Approved: true, Head: c.Head, Base: "main", Method: c.Method}, nil
}

func (h *fakeHost) PostStatus(context.Context, mergequeue.Change, string, mergequeue.CommitStatus) error {
	return nil
}

func (h *fakeHost) Retarget(context.Context, mergequeue.Change, string) error { return nil }

func (h *fakeHost) KickBack(context.Context, mergequeue.Change, string, mergequeue.Kick) error {
	return nil
}

func (h *fakeHost) MergeChange(_ context.Context, c mergequeue.Change, m mergequeue.MergeRequest) error {
	gitIn(h.t, h.merger, "fetch", "--quiet", "origin", "main", c.Ref)
	gitIn(h.t, h.merger, "checkout", "--quiet", "-B", "main", "origin/main")
	gitIn(h.t, h.merger, "merge", "--quiet", "--squash", m.Commit)
	gitIn(h.t, h.merger, "commit", "--quiet", "--author", c.Author+" <"+c.Author+"@example.invalid>",
		"-m", c.Title+" (#"+c.ID+")", "-m", m.Message)
	gitIn(h.t, h.merger, "push", "--quiet", "origin", "main")
	h.merged = append(h.merged, c.ID)
	return nil
}

type greenGate struct{}

func (greenGate) Validate(context.Context, mergequeue.Candidate, string, mergequeue.Change) (mergequeue.GateResult, error) {
	return mergequeue.GateResult{Green: true}, nil
}

// End to end over real git: planning admits two changes, validation builds candidates
// speculatively and exports each green one as it is decided; a separate checkout that
// never builds anything imports them and merges both, each as its own squash commit by
// its own author, the second through a pushed update commit, and main ends on the
// validated tree.
func TestValidatedChangesMergeOneCommitEachAndMainCarriesTheValidatedTree(t *testing.T) {
	t.Skip("TODO(merge-queue): needs vcs's TreeMerger, CommitWriter, Pusher and the rest of the capability redesign")
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	merger := filepath.Join(t.TempDir(), "merger")
	gitIn(t, filepath.Dir(merger), "clone", "--quiet", f.remote, merger)
	host := &fakeHost{t: t, merger: merger}
	ctx := t.Context()

	queue := f.repo(t, f.queue)
	planner := mergequeue.NewPlanner(queue)
	planner.Provider, planner.Depth = host, 2
	plan, err := planner.Run(ctx, mergequeue.Changes{Schema: mergequeue.SchemaChanges, Base: "main", Changes: []mergequeue.Change{a, b}})
	require.NoError(t, err)
	require.Equal(t, [][]string{{"1", "2"}}, [][]string{{plan.Partitions[0][0].ID, plan.Partitions[0][1].ID}})

	dir := &mergequeue.VerdictDir{Path: t.TempDir(), Export: func(ctx context.Context, file, base, cand string) error {
		return queue.Bundle(ctx, file, mergequeue.BundleRange{Base: base, Head: cand})
	}}
	v := mergequeue.NewValidator(queue, greenGate{}, dir)
	v.Regenerate, v.Scratch = regenerate, t.TempDir()
	require.NoError(t, v.Run(ctx, plan))
	require.NoError(t, dir.MarkDone())
	f2, err := os.Open(filepath.Join(dir.Path, "2", mergequeue.VerdictFile))
	require.NoError(t, err)
	two, err := mergequeue.ReadVerdict(f2)
	require.NoError(t, f2.Close())
	require.NoError(t, err)

	require.NoError(t, mergequeue.NewApplier(host, f.repo(t, f.apply), &mergequeue.VerdictDir{Path: dir.Path, Follow: true}).Run(ctx, plan))
	assert.Equal(t, []string{"1", "2"}, host.merged)

	log := gitIn(t, merger, "log", "--format=%an|%s", f.base+"..origin/main")
	assert.Equal(t, "bob|change 2 (#2)\nann|change 1 (#1)", log, "one commit per change, each by its author")
	assert.Equal(t, gitIn(t, f.queue, "rev-parse", two.Candidate+"^{tree}"), gitIn(t, merger, "rev-parse", "origin/main^{tree}"))

	// #2 conflicted with #1 in the generated files, so its update commit was pushed to
	// its branch: authored by bob, committed by the queue.
	pushed := gitIn(t, merger, "log", "-1", "--format=%an|%cn|%P", "origin/pr2")
	assert.True(t, strings.HasPrefix(pushed, "bob|merge queue|"+b.Head+" "), pushed)
	assert.NoDirExists(t, filepath.Join(f.apply, ".git", "worktrees"), "applying built nothing")
}
