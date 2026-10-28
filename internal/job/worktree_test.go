package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worktreeStore is a job store over a fresh git repository, and that repository.
func worktreeStore(t *testing.T) (*Store, Location, *testkit.Repo) {
	t.Helper()
	repo := execRepository(t)
	loc := Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: repo.Root()}
	return NewStore(loc), loc, repo
}

// forkInto makes job id's checkout in a fresh directory, declares the job and records the
// checkout on it, the way `magus job fork --checkout` does.
func forkInto(t *testing.T, s *Store, id, binary string) types.Job {
	t.Helper()
	wt, made, err := s.ForkWorktree(t.Context(), id, filepath.Join(t.TempDir(), id), "", binary)
	require.NoError(t, err)
	require.True(t, made)
	seed(t, s, types.Job{ID: id, State: types.StateDeclared, WritePaths: []string{"a.txt"}})
	row, err := s.RecordWorktree(t.Context(), id, wt)
	require.NoError(t, err)
	return row
}

func TestForkWorktreeMakesTheJobsCheckoutAtTheForkersRevision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, repo := worktreeStore(t)

	row := forkInto(t, s, "w1", "")
	wt := *row.Worktree
	assert.Equal(t, repo.Head(), wt.Base)
	assert.FileExists(t, filepath.Join(wt.Root, "a.txt"), "the checkout holds the revision it was made at")
	assert.Equal(t, "w1", repo.Git("-C", wt.Root, "symbolic-ref", "--short", "HEAD"), "the job commits on a branch named after it")
	assert.Empty(t, wt.Binary)
	assert.True(t, wt.Made, "the fork made it, so it is magus's to remove")
	assert.NoFileExists(t, filepath.Join(wt.Root, "magus"), "a forker running magus from PATH places none")

	again, made, err := s.ForkWorktree(ctx, "w1", wt.Root, "", "")
	require.NoError(t, err)
	assert.False(t, made, "forking the job again into its own checkout makes nothing")
	assert.Equal(t, wt, again)

	_, _, err = s.ForkWorktree(ctx, "w1", filepath.Join(t.TempDir(), "elsewhere"), "", "")
	require.ErrorContains(t, err, fmt.Sprintf("already owns the checkout %s, which its fork made", wt.Root))
}

func TestForkWorktreeRefusesACheckpointWithUncommittedChanges(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	dir := filepath.Join(t.TempDir(), "w1")

	_, _, err := s.ForkWorktree(t.Context(), "w1", dir, repo.Head()+"+00112233445566778899aabbccddeeff", "")
	require.ErrorContains(t, err, "uncommitted changes")
	assert.NoDirExists(t, dir)
}

// A row forked from inside an integrator's existing checkout got no worktree record, and the
// guard then refused magus there as a sibling; --checkout naming it adopts it instead.
func TestForkWorktreeAdoptsALinkedCheckoutThatAlreadyStands(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, repo := worktreeStore(t)
	dir := filepath.Join(t.TempDir(), "integrator")
	repo.Git("worktree", "add", "-q", "-b", "integrator", dir)

	wt, made, err := s.ForkWorktree(ctx, "finish", dir, "", "")
	require.NoError(t, err)
	assert.False(t, made, "an adopted checkout is not the fork's to discard")
	source := wt.Source
	assert.True(t, samePath(repo.Root(), source), "removal runs from the primary checkout, not from inside the adopted one")
	assert.Equal(t, types.JobWorktree{Root: dir, Source: source, Base: repo.Head(), Created: wt.Created}, wt)

	linked, err := s.LinkedCheckout(ctx, dir)
	require.NoError(t, err)
	assert.True(t, linked)
	primary, err := s.LinkedCheckout(ctx, repo.Root())
	require.NoError(t, err)
	assert.False(t, primary)

	seed(t, s, types.Job{ID: "finish", State: types.StateDeclared, WritePaths: []string{"a.txt"}})
	_, err = s.RecordWorktree(ctx, "finish", wt)
	require.NoError(t, err)
	owned, err := s.OwnedCheckout(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, owned)
	none, err := s.OwnedCheckout(repo.Root())
	require.NoError(t, err)
	assert.Empty(t, none)

	_, _, err = s.ForkWorktree(ctx, "other", t.TempDir(), "", "")
	require.ErrorContains(t, err, "is not a linked checkout of this repository")

	// The job adopted its checkout rather than owning it, so forking it elsewhere rebinds it.
	moved := filepath.Join(t.TempDir(), "moved")
	again, made, err := s.ForkWorktree(ctx, "finish", moved, "", "")
	require.NoError(t, err)
	assert.True(t, made)
	assert.Equal(t, moved, again.Root)
	assert.DirExists(t, dir, "the adopted checkout stands")
}

// A child forked from outside its parent's checkout inherits the checkout the parent's fork
// recorded; one the parent was only taken in is named, not inherited.
func TestParentCheckoutIsWhereTheParentWorksWhenNotHere(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	parent := forkInto(t, s, "parent", "")
	elsewhere := t.TempDir()
	seed(t, s, types.Job{ID: "taken", State: types.StateRunning, ReadOnly: true, CheckoutRoot: elsewhere})
	seed(t, s, types.Job{ID: "untaken", State: types.StateDeclared, ReadOnly: true})

	for name, tc := range map[string]struct {
		here, parent, dir string
		bound             bool
	}{
		"a parent's fork-made checkout":    {here: repo.Root(), parent: "parent", dir: parent.Worktree.Root, bound: true},
		"forked inside the parent's":       {here: parent.Worktree.Root, parent: "parent"},
		"a parent taken in another":        {here: repo.Root(), parent: "taken", dir: elsewhere},
		"a parent nobody took":             {here: repo.Root(), parent: "untaken"},
		"a parent the store does not know": {here: repo.Root(), parent: "nobody"},
	} {
		dir, bound, err := s.ParentCheckout(tc.here, tc.parent)
		require.NoError(t, err, name)
		assert.Equal(t, tc.dir, dir, name)
		assert.Equal(t, tc.bound, bound, name)
	}
}

func TestMirroredBinaryIsOnlyTheForkingCheckoutsOwnMagus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	local := filepath.Join(root, "magus")
	require.NoError(t, os.WriteFile(local, []byte("local\n"), 0o755))
	onPath := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(onPath, []byte("local\n"), 0o755))

	assert.Equal(t, local, MirroredBinary(local, root))
	assert.Empty(t, MirroredBinary(onPath, root), "a magus on PATH is not the checkout's own, however alike")
	assert.Empty(t, MirroredBinary(onPath, t.TempDir()), "a checkout holding no ./magus has none to mirror")
}

// forkersBinary is a stand-in ./magus, and a context saying the running magus was built as
// build.
func forkersBinary(t *testing.T, build types.MagusBuild) (string, context.Context) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(bin, []byte("build 1\n"), 0o755))
	return bin, types.WithMagusBuild(t.Context(), build)
}

func TestForkWorktreePlacesTheForkersBinary(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	bin, ctx := forkersBinary(t, types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: repo.Head()})

	wt, made, err := s.ForkWorktree(ctx, "w1", filepath.Join(t.TempDir(), "w1"), "", bin)
	require.NoError(t, err)
	require.True(t, made)
	assert.Equal(t, filepath.Join(wt.Root, "magus"), wt.Binary)
	body, err := os.ReadFile(wt.Binary)
	require.NoError(t, err)
	assert.Equal(t, "build 1\n", string(body))
	info, err := os.Stat(wt.Binary)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "the placed binary runs")
}

// A checkout holding the bootstrap script builds its own ./magus, so the forker's is never
// copied into it, whatever it was built from: a dirty or older forker never blocks the fork.
// A workspace without the script takes the forker's binary as a tool, at any checkpoint.
func TestForkWorktreeCopiesNoBinaryIntoACheckoutThatBuildsItsOwn(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	built := repo.Head()
	builds := repo.Commit("builds its own", map[string]string{CheckoutBootstrapScript: "namespace b;\n"})

	for name, build := range map[string]types.MagusBuild{
		"a build from uncommitted changes":          {Version: "v0.5.0-1-gabc-dirty", Commit: built},
		"an unstamped build":                        {Version: "unknown", Commit: "unknown"},
		"a build at a commit this repository lacks": {Version: "v0.5.0-1-gabc", Commit: "0123456789abcdef0123456789abcdef01234567"},
	} {
		t.Run(name, func(t *testing.T) {
			bin, ctx := forkersBinary(t, build)
			wt, made, err := s.ForkWorktree(ctx, "own-"+strings.ReplaceAll(name, " ", "-"), filepath.Join(t.TempDir(), "w"), builds, bin)
			require.NoError(t, err)
			require.True(t, made)
			assert.True(t, BuildsItsOwnMagus(wt.Root))
			assert.Empty(t, wt.Binary, "the checkout builds its own")
			assert.NoFileExists(t, filepath.Join(wt.Root, "magus"))
		})
	}

	bin, ctx := forkersBinary(t, types.MagusBuild{Version: "v0.5.0-1-gabc-dirty", Commit: builds})
	wt, made, err := s.ForkWorktree(ctx, "tool", filepath.Join(t.TempDir(), "tool"), built, bin)
	require.NoError(t, err)
	require.True(t, made)
	assert.False(t, BuildsItsOwnMagus(wt.Root))
	assert.Equal(t, filepath.Join(wt.Root, "magus"), wt.Binary, "a checkout that cannot build one takes the forker's")
}

func TestExecTakesAJobInItsOwnCheckoutAndNowhereElse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, loc, _ := worktreeStore(t)
	row := forkInto(t, s, "w1", "")

	_, err := s.Exec(ctx, "w1")
	require.ErrorIs(t, err, ErrHeldElsewhere)
	assert.ErrorContains(t, err, row.Worktree.Root)

	taken, err := NewStore(Location{StateBase: loc.StateBase, CacheDir: loc.CacheDir, Root: row.Worktree.Root}).Exec(ctx, "w1")
	require.NoError(t, err)
	assert.Equal(t, row.Worktree.Root, taken.CheckoutRoot)
	assert.Equal(t, types.StateRunning, taken.State)
}

// A worker the host isolated in a fresh linked checkout takes a job nobody took, whose fork
// adopted another checkout, where it runs: the job is bound there, at its checkpoint.
func TestExecBindsAnUntakenAdoptedJobToAFreshCheckoutAtItsCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, loc, repo := worktreeStore(t)
	older := repo.Head()
	checkpoint := repo.Commit("later", map[string]string{"b.txt": "b\n"})
	linked := func(name, rev string) string {
		dir := filepath.Join(t.TempDir(), name)
		repo.Git("worktree", "add", "-q", "--detach", dir, rev)
		resolved, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		return resolved
	}
	landing := linked("landing", checkpoint)
	for _, id := range []string{"w1", "w2"} {
		seed(t, s, types.Job{ID: id, State: types.StateDeclared, Checkpoint: checkpoint, WritePaths: []string{"a.txt"}})
		wt, made, err := s.ForkWorktree(ctx, id, landing, "", "")
		require.NoError(t, err)
		require.False(t, made)
		_, err = s.RecordWorktree(ctx, id, wt)
		require.NoError(t, err)
	}
	at := func(dir string) *Store {
		return NewStore(Location{StateBase: loc.StateBase, CacheDir: loc.CacheDir, Root: dir})
	}

	stale := linked("stale", older)
	_, err := at(stale).Exec(ctx, "w1")
	require.ErrorIs(t, err, ErrHeldElsewhere)
	assert.ErrorContains(t, err, "once this checkout is at its checkpoint "+checkpoint)

	fresh := linked("fresh", checkpoint)
	taken, err := at(fresh).Exec(ctx, "w1")
	require.NoError(t, err)
	assert.Equal(t, fresh, taken.CheckoutRoot)
	assert.Equal(t, fresh, taken.Worktree.Root)
	assert.False(t, taken.Worktree.Made, "the host made it, so magus never removes it")

	_, err = at(landing).Exec(ctx, "w2")
	require.NoError(t, err)
	_, err = at(fresh).Exec(ctx, "w2")
	require.ErrorIs(t, err, ErrHeldElsewhere, "a job somebody took stays where it was taken")
}

func TestEndWorktreesRemovesACleanCheckoutOnceItsJobEnds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, _ := worktreeStore(t)
	row := forkInto(t, s, "w1", "")

	changed, err := s.EndWorktrees(ctx)
	require.NoError(t, err)
	assert.Empty(t, changed, "a live job keeps its checkout")
	assert.DirExists(t, row.Worktree.Root)

	_, err = Exit(ctx, s, "w1", nil, nil)
	require.NoError(t, err)
	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.NotZero(t, changed[0].Worktree.Removed)
	assert.Empty(t, changed[0].Worktree.Kept)
	assert.NoDirExists(t, row.Worktree.Root)

	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	assert.Empty(t, changed, "a removed checkout is not removed twice")
}

// The holder exits from inside its checkout, which stays and says how it goes; the next call
// from elsewhere removes it.
func TestEndWorktreesKeepsTheCheckoutTheCallRunsInAndSaysSo(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, loc, _ := worktreeStore(t)
	row := forkInto(t, s, "w1", "")
	inside := NewStore(Location{StateBase: loc.StateBase, CacheDir: loc.CacheDir, Root: row.Worktree.Root})
	_, err := Exit(ctx, inside, "w1", nil, nil)
	require.NoError(t, err)

	changed, err := inside.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.Contains(t, changed[0].Worktree.Kept, "this call runs in it; `magus job wait w1` from another checkout removes it")
	assert.DirExists(t, row.Worktree.Root)

	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.NotZero(t, changed[0].Worktree.Removed)
	assert.NoDirExists(t, row.Worktree.Root)
}

func TestEndWorktreesKeepsACheckoutHoldingWorkAndSaysWhy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, repo := worktreeStore(t)
	dirty := forkInto(t, s, "dirty", "")
	require.NoError(t, os.WriteFile(filepath.Join(dirty.Worktree.Root, "b.txt"), []byte("b\n"), 0o644))
	stranded := forkInto(t, s, "stranded", "")
	repo.Git("-C", stranded.Worktree.Root, "switch", "-q", "--detach")
	repo.Git("-C", stranded.Worktree.Root, "commit", "-q", "--allow-empty", "-m", "local")
	for _, id := range []string{"dirty", "stranded"} {
		_, err := Exit(ctx, s, id, nil, nil)
		require.NoError(t, err)
	}

	changed, err := s.EndWorktrees(ctx)
	require.NoError(t, err)
	require.Len(t, changed, 2)
	kept := map[string]types.JobWorktree{}
	for _, row := range changed {
		kept[row.ID] = *row.Worktree
	}
	assert.Contains(t, kept["dirty"].Kept, "b.txt")
	assert.Contains(t, kept["stranded"].Kept, "on no branch, bookmark, remote or its base")
	for id, wt := range kept {
		assert.Zero(t, wt.Removed, id)
		assert.DirExists(t, wt.Root, id)
	}

	changed, err = s.EndWorktrees(ctx)
	require.NoError(t, err)
	assert.Empty(t, changed, "a reason that has not changed is not recorded again")
}

func TestDeleteEndsTheCheckoutTheRowOwns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, _ := worktreeStore(t)
	clean := forkInto(t, s, "clean", "")
	dirty := forkInto(t, s, "dirty", "")
	require.NoError(t, os.WriteFile(filepath.Join(dirty.Worktree.Root, "b.txt"), []byte("b\n"), 0o644))

	_, err := s.Delete(ctx, "dirty", false)
	require.ErrorContains(t, err, "b.txt", "a checkout holding work stays, and so does its row")
	assert.DirExists(t, dirty.Worktree.Root)

	dropped, err := s.Delete(ctx, "clean", false)
	require.NoError(t, err)
	assert.NoDirExists(t, clean.Worktree.Root, "a clean checkout goes with its row")
	assert.NotZero(t, dropped.Worktree.Removed)

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "dirty", rows[0].ID)
}

func TestBuildInputsAreTheChangedPathsALinkedPackageReaches(t *testing.T) {
	t.Parallel()
	root := filepath.FromSlash("/w/a")
	listed := fmt.Sprintf("%s|VERSION|banner.txt\n%s\n%s\n\n%s\n%s\n",
		root,
		filepath.Join(root, "cmd", "magus"),
		filepath.Join(root, "internal", "job"),
		filepath.FromSlash("/usr/local/go/src/fmt"),
		filepath.FromSlash("/home/u/go/pkg/mod/x@v1/y"))
	scopes := packageScopes(listed, root)
	assert.Equal(t, []string{"./", "VERSION", "banner.txt", "cmd/magus/", "internal/job/"}, scopes)

	for _, tc := range []struct {
		path  string
		input bool
	}{
		{"internal/job/worktree.go", true},
		{"cmd/magus/main.go", true},
		{"main.go", true},
		{"go.mod", true},
		{"go.sum", true},
		{"go.work", true},
		{"VERSION", true},
		{"internal/job/gen/job.schema.json", true},
		{"internal/job/worktree_test.go", false},
		{"internal/other/x.go", false},
		{"libs/embed/train/go.mod", false},
		{"docs/index.md", false},
		{"MAGUS.md", false},
		{"console/src/app.ts", false},
		{"changes/unreleased/x.md", false},
	} {
		assert.Equal(t, tc.input, len(buildInputs([]string{tc.path}, scopes)) == 1, tc.path)
	}
}

// goModuleRepository is a git checkout holding a Go module whose ./cmd/magus links lib, beside a
// package nothing links and a docs file.
func goModuleRepository(t *testing.T) *testkit.Repo {
	t.Helper()
	return testkit.NewRepository(t, map[string]string{
		"go.mod":            "module example.com/m\n\ngo 1.26\n",
		"cmd/magus/main.go": "package main\n\nimport \"example.com/m/lib\"\n\nfunc main() { lib.Run() }\n",
		"lib/lib.go":        "package lib\n\nfunc Run() {}\n",
		"lib/lib_test.go":   "package lib\n",
		"unlinked/u.go":     "package unlinked\n",
		"docs/a.md":         "a\n",
	})
}

// A fork with no checkout of its own lands in the forking checkout, whose ./magus judges
// the job: built before the checkpoint's Go build inputs, it names the bootstrap.
func TestLandingBootstrapNamesTheBuildAStaleMagusNeeds(t *testing.T) {
	t.Parallel()
	repo := goModuleRepository(t)
	s := NewStore(Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: repo.Root()})
	built := repo.Head()
	docs := repo.Commit("docs", map[string]string{"docs/a.md": "b\n"})
	linked := repo.Commit("linked", map[string]string{"lib/lib.go": "package lib\n\nfunc Run() { _ = 1 }\n"})
	local := filepath.Join(repo.Root(), "magus")
	require.NoError(t, os.WriteFile(local, []byte("build 1\n"), 0o755))
	ctx := types.WithMagusBuild(t.Context(), types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: built})

	for name, tc := range map[string]struct {
		exe, checkpoint, want string
	}{
		"a checkpoint past a linked package": {exe: local, checkpoint: linked, want: CheckoutBootstrap(repo.Root())},
		"a checkpoint past a docs edit":      {exe: local, checkpoint: docs},
		"the commit it was built at":         {exe: local, checkpoint: built},
		"a magus on PATH":                    {exe: filepath.Join(t.TempDir(), "magus"), checkpoint: linked},
	} {
		got, err := s.LandingBootstrap(ctx, tc.exe, tc.checkpoint)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, got, name)
	}

	unknown := types.WithMagusBuild(t.Context(), types.MagusBuild{Version: "dev", Commit: "unknown"})
	_, err := s.LandingBootstrap(unknown, local, linked)
	require.ErrorContains(t, err, "records no commit it was built from")
}

func TestCheckoutBootstrapNamesTheScript(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "./magus buzz hack/dev/bootstrap-checkout.buzz -- --checkout /w", CheckoutBootstrap("/w"))
}

func TestChangedBuildInputsFailsWhereThePackagesCannotBeListed(t *testing.T) {
	t.Parallel()
	repo := execRepository(t)
	built := repo.Head()
	later := repo.Commit("later", map[string]string{"b.txt": "b\n"})
	s := NewStore(Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: repo.Root()})
	drv, why := resolveDriver(t.Context(), repo.Root())
	require.NotNil(t, drv, why)

	_, err := ChangedBuildInputs(t.Context(), drv, s.root, built, later)
	require.ErrorContains(t, err, "go list -deps ./cmd/magus")

	inputs, err := ChangedBuildInputs(t.Context(), drv, s.root, later, later)
	require.NoError(t, err, "equal commits list nothing")
	assert.Empty(t, inputs)
}
