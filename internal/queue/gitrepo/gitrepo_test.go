package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue"
)

// Every fixture is one project, app: app/src.txt is written by hand and app/gen/out.txt
// is derived from it, as one line, so two changes to the source always conflict in the
// derived file even when they touch different source lines.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func derived(path string) (string, string, bool) {
	project, rest, _ := strings.Cut(path, "/")
	if strings.HasPrefix(rest, "gen/") {
		return "generate", project, true
	}
	return "", "", false
}

func render(src string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(src), "\n", "|")) + "\n"
}

func regenerate(_ context.Context, dir, _ string, projects []string) error {
	for _, p := range projects {
		src, err := os.ReadFile(filepath.Join(dir, p, "src.txt"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, p, "gen", "out.txt"), []byte(render(string(src))), 0o644); err != nil {
			return err
		}
	}
	return nil
}

type fixture struct {
	remote, dev, queue, land string
	base                     string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{remote: filepath.Join(root, "remote.git"), dev: filepath.Join(root, "dev")}
	gitIn(t, root, "init", "--quiet", "--bare", "-b", "main", f.remote)
	gitIn(t, root, "clone", "--quiet", f.remote, f.dev)
	f.write(t, map[string]string{"app/src.txt": "alpha\nbeta\ngamma\n", "lib/x.txt": "x\n"})
	gitIn(t, f.dev, "add", "-A")
	gitIn(t, f.dev, "commit", "--quiet", "-m", "initial")
	gitIn(t, f.dev, "push", "--quiet", "origin", "HEAD:main")
	f.base = gitIn(t, f.dev, "rev-parse", "HEAD")
	f.queue, f.land = filepath.Join(root, "queue"), filepath.Join(root, "land")
	gitIn(t, root, "clone", "--quiet", f.remote, f.queue)
	gitIn(t, root, "clone", "--quiet", f.remote, f.land)
	return f
}

// write edits files in the dev clone, regenerating app's derived file the way an author
// running generate would.
func (f *fixture) write(t *testing.T, files map[string]string) {
	t.Helper()
	for p, body := range files {
		abs := filepath.Join(f.dev, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	if src, ok := files["app/src.txt"]; ok {
		require.NoError(t, os.MkdirAll(filepath.Join(f.dev, "app", "gen"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(f.dev, "app", "gen", "out.txt"), []byte(render(src)), 0o644))
	}
}

// change opens a pull request's branch from base: returns the change as a provider lists it.
func (f *fixture) change(t *testing.T, id, author, from string, files map[string]string) queue.Change {
	t.Helper()
	gitIn(t, f.dev, "checkout", "--quiet", "-B", "pr"+id, from)
	f.write(t, files)
	gitIn(t, f.dev, "add", "-A")
	gitIn(t, f.dev, "-c", "user.name="+author, "commit", "--quiet", "--author", author+" <"+author+"@example.invalid>", "-m", "change "+id)
	gitIn(t, f.dev, "push", "--quiet", "--force", "origin", "pr"+id)
	return queue.Change{ID: id, Head: gitIn(t, f.dev, "rev-parse", "HEAD"), Ref: "refs/heads/pr" + id,
		Branch: "pr" + id, Base: "main", Title: "change " + id, Author: author}
}

func (f *fixture) repo(t *testing.T, root string) *Repo {
	return &Repo{Root: root, Remote: "origin", Scratch: t.TempDir(), Derived: derived, Regenerate: regenerate,
		Env: []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_COMMITTER_NAME=queue-bot", "GIT_COMMITTER_EMAIL=bot@example.invalid"}}
}

func show(t *testing.T, dir, rev, path string) string {
	return gitIn(t, dir, "show", rev+":"+path)
}

func TestBuildStacksChangesAndRegeneratesTheDerivedFileTheyBothChanged(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	r := f.repo(t, f.queue)
	ctx := context.Background()
	require.NoError(t, r.Fetch(ctx, a))
	require.NoError(t, r.Fetch(ctx, b))
	one, err := r.Build(ctx, f.base, a)
	require.NoError(t, err)
	two, err := r.Build(ctx, one.Commit, b)
	require.NoError(t, err)
	assert.Equal(t, "alpha-a\nbeta\ngamma-b", show(t, f.queue, two.Commit, "app/src.txt"))
	assert.Equal(t, "ALPHA-A|BETA|GAMMA-B", show(t, f.queue, two.Commit, "app/gen/out.txt"), "regenerated, not merged")
	assert.DirExists(t, two.Dir)
	require.NoError(t, r.Discard(ctx, two))
	assert.NoDirExists(t, two.Dir)
}

func TestASourceConflictIsReportedWithTheCommitsThatCausedIt(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	c := f.change(t, "3", "cat", f.base, map[string]string{"app/src.txt": "alpha-c\nbeta\ngamma\n"})
	r := f.repo(t, f.queue)
	ctx := context.Background()
	require.NoError(t, r.Fetch(ctx, a))
	require.NoError(t, r.Fetch(ctx, c))
	one, err := r.Build(ctx, f.base, a)
	require.NoError(t, err)

	err = r.Overlap(ctx, one.Commit, c)
	var ce *queue.ConflictError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, []string{"app/src.txt"}, ce.Conflict.Paths, "the derived file is not a conflict")
	require.Len(t, ce.Conflict.With, 1)
	assert.Contains(t, ce.Conflict.With[0], "change 1", "the commit on the base side, not the staging merge")

	_, err = r.Build(ctx, one.Commit, c)
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, []string{"app/src.txt"}, ce.Conflict.Paths)
}

func TestChangedAndMessageDescribeOnlyTheChangesOwnCommits(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	r := f.repo(t, f.queue)
	ctx := context.Background()
	require.NoError(t, r.Fetch(ctx, a))
	paths, err := r.Changed(ctx, f.base, a.Head)
	require.NoError(t, err)
	assert.Equal(t, []string{"lib/x.txt"}, paths)
	msg, err := r.Message(ctx, f.base, a.Head)
	require.NoError(t, err)
	assert.Equal(t, "* change 1", msg)
}

// fakeHost lists changes, approves everything, and merges the way GitHub squashes: one
// commit per change, authored by the change's author, on the remote's main.
type fakeHost struct {
	t       *testing.T
	f       *fixture
	merger  string
	changes []queue.Change
	merged  []string
}

func (h *fakeHost) Name() string { return "fake" }
func (h *fakeHost) List(context.Context, queue.ListQuery) ([]queue.Change, error) {
	return h.changes, nil
}

func (h *fakeHost) ApprovalAt(_ context.Context, c queue.Change, _ string) (queue.Approval, error) {
	head := gitIn(h.t, h.f.dev, "ls-remote", "origin", "refs/heads/"+c.Branch)
	head, _, _ = strings.Cut(head, "\t")
	if head != c.Head {
		// The queue pushed a regeneration on top: GitHub reports that as the head.
		return queue.Approval{Approved: true, Head: c.Head}, nil
	}
	return queue.Approval{Approved: true, Head: head}, nil
}

func (h *fakeHost) PostStatus(context.Context, queue.Change, string, queue.Status) error { return nil }
func (h *fakeHost) KickBack(context.Context, queue.Change, string, string) error        { return nil }

func (h *fakeHost) Merge(_ context.Context, c queue.Change, sha, message string) error {
	gitIn(h.t, h.merger, "fetch", "--quiet", "origin", "main", c.Ref)
	gitIn(h.t, h.merger, "checkout", "--quiet", "-B", "main", "origin/main")
	gitIn(h.t, h.merger, "merge", "--quiet", "--squash", sha)
	gitIn(h.t, h.merger, "commit", "--quiet", "--author", c.Author+" <"+c.Author+"@example.invalid>",
		"-m", c.Title+" (#"+c.ID+")", "-m", message)
	gitIn(h.t, h.merger, "push", "--quiet", "origin", "main")
	h.merged = append(h.merged, c.ID)
	return nil
}

type greenGate struct{}

func (greenGate) Validate(context.Context, queue.Stage, string) (queue.GateResult, error) {
	return queue.GateResult{Green: true}, nil
}

type oneProject struct{}

func (oneProject) Closure(context.Context, []string) (queue.Closure, error) {
	return queue.Closure{Projects: []string{"app"}, Proven: true}, nil
}

// End to end over real git: validation stages two changes speculatively and bundles the
// stages; a separate checkout that never builds anything imports the bundle and lands
// both, each as its own squash commit by its own author, the second through a pushed
// regeneration, and main ends on the validated tree.
func TestValidatedChangesLandOneCommitEachAndMainCarriesTheValidatedTree(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"app/src.txt": "alpha-a\nbeta\ngamma\n"})
	b := f.change(t, "2", "bob", f.base, map[string]string{"app/src.txt": "alpha\nbeta\ngamma-b\n"})
	merger := filepath.Join(t.TempDir(), "merger")
	gitIn(t, filepath.Dir(merger), "clone", "--quiet", f.remote, merger)
	host := &fakeHost{t: t, f: f, merger: merger, changes: []queue.Change{a, b}}
	ctx := context.Background()

	stager := f.repo(t, f.queue)
	m, err := (&queue.Validation{Provider: host, Stager: stager, Graph: oneProject{}, Gate: greenGate{},
		Base: "main", Depth: 2}).Run(ctx)
	require.NoError(t, err)
	require.Len(t, m.Changes, 2)
	require.Equal(t, queue.DecisionLand, m.Changes[1].Decision)
	stageAB := m.Changes[1].Stage
	bundle := filepath.Join(t.TempDir(), "stages.bundle")
	require.NoError(t, stager.Bundle(ctx, bundle, m.BaseSHA, []string{m.Changes[0].Stage, stageAB}))

	lander := f.repo(t, f.land)
	require.NoError(t, lander.Unbundle(ctx, bundle))
	res, err := (&queue.Landing{Provider: host, Lander: lander}).Run(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, host.merged)
	for _, r := range res {
		assert.Equal(t, queue.OutcomeMerged, r.Outcome, r.Reason)
	}

	log := gitIn(t, merger, "log", "--format=%an|%s", f.base+"..origin/main")
	assert.Equal(t, "bob|change 2 (#2)\nann|change 1 (#1)", log, "one commit per change, each by its author")
	assert.Equal(t, gitIn(t, f.queue, "rev-parse", stageAB+"^{tree}"), gitIn(t, merger, "rev-parse", "origin/main^{tree}"))

	// #2 conflicted with #1 in the derived file, so its regeneration was pushed to its
	// branch: authored by bob, with the bot only as committer.
	pushed := gitIn(t, merger, "log", "-1", "--format=%an|%cn|%P", "origin/pr2")
	assert.True(t, strings.HasPrefix(pushed, "bob|queue-bot|"+b.Head+" "), pushed)
	assert.NoDirExists(t, filepath.Join(f.land, ".git", "worktrees"), "landing built nothing")
}

func TestPrepareRefusesATreeThatDiffersOutsideDerivedFiles(t *testing.T) {
	f := newFixture(t)
	a := f.change(t, "1", "ann", f.base, map[string]string{"lib/x.txt": "y\n"})
	evil := f.change(t, "9", "eve", f.base, map[string]string{"lib/x.txt": "y\n", "lib/extra.txt": "surprise\n"})
	r := f.repo(t, f.land)
	ctx := context.Background()
	require.NoError(t, r.Fetch(ctx, a))
	require.NoError(t, r.Fetch(ctx, evil))
	tree, err := r.TreeOf(ctx, evil.Head)
	require.NoError(t, err)
	_, err = r.Prepare(ctx, f.base, a, tree)
	var refused *queue.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Reason, "lib/extra.txt")
}
