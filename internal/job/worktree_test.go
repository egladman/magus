package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type FollowCheckpointSuite struct {
	suite.Suite
	store *Store
	repo  *testkit.Repo
	row   types.Job
	start string
}

func TestFollowCheckpointSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(FollowCheckpointSuite))
}

func (s *FollowCheckpointSuite) SetupTest() {
	s.store, _, s.repo = worktreeStore(s.T())
	s.row = forkInto(s.T(), s.store, "w1", "")
	s.start = s.row.Worktree.Base
}

func (s *FollowCheckpointSuite) advance() string {
	s.repo.Git("commit", "-q", "--allow-empty", "-m", "newer")
	return s.repo.Head()
}

func (s *FollowCheckpointSuite) fork(checkpoint string) (types.JobWorktree, error) {
	wt, made, err := s.store.ForkWorktree(s.T().Context(), "w1", s.row.Worktree.Root, checkpoint, "")
	s.False(made)
	return wt, err
}

func (s *FollowCheckpointSuite) head() string {
	return s.repo.Git("-C", s.row.Worktree.Root, "rev-parse", "HEAD")
}

func (s *FollowCheckpointSuite) TestACleanCheckoutFastForwardsToADescendant() {
	newer := s.advance()
	wt, err := s.fork(newer)
	s.Require().NoError(err)
	s.Equal(newer, wt.Base)
	s.Equal(newer, s.head())
	s.Equal("w1", s.repo.Git("-C", wt.Root, "symbolic-ref", "--short", "HEAD"))
}

func (s *FollowCheckpointSuite) TestADirtyCheckoutIsRefusedNamingThePathAndStays() {
	newer := s.advance()
	s.Require().NoError(os.WriteFile(filepath.Join(s.row.Worktree.Root, "a.txt"), []byte("work\n"), 0o644))
	_, err := s.fork(newer)
	s.Require().ErrorContains(err, s.row.Worktree.Root)
	s.ErrorContains(err, "uncommitted work (a.txt)")
	s.Equal(s.start, s.head())
}

func (s *FollowCheckpointSuite) TestACheckpointThatLeavesCommitsBehindIsRefused() {
	newer := s.advance()
	s.repo.Git("-C", s.row.Worktree.Root, "commit", "-q", "--allow-empty", "-m", "the job's own")
	mine := s.head()
	_, err := s.fork(newer)
	s.Require().ErrorContains(err, "stays at")
	s.Equal(mine, s.head())
}

func (s *FollowCheckpointSuite) TestForkingAtItsOwnBaseKeepsItsCommits() {
	s.repo.Git("-C", s.row.Worktree.Root, "commit", "-q", "--allow-empty", "-m", "the job's own")
	mine := s.head()
	wt, err := s.fork(s.start)
	s.Require().NoError(err)
	s.Equal(s.start, wt.Base)
	s.Equal(mine, s.head())
}

func (s *FollowCheckpointSuite) TestAnUnknownCheckpointIsRefused() {
	_, err := s.fork("0123456789abcdef0123456789abcdef01234567")
	s.Require().ErrorContains(err, "cannot find")
	s.Equal(s.start, s.head())
}

// JJFollowCheckpointSuite holds a jj checkout, whose head is usually an
// empty commit on top of the revision it stands at.
type JJFollowCheckpointSuite struct {
	suite.Suite
	store *Store
	root  string
	row   types.Job
}

func TestJJFollowCheckpointSuite(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}
	suite.Run(t, new(JJFollowCheckpointSuite))
}

func (s *JJFollowCheckpointSuite) jj(dir string, args ...string) string {
	cmd := exec.Command("jj", append([]string{"--config", "user.name=t", "--config", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	s.Require().NoError(err, "jj %v", args)
	return strings.TrimSpace(string(out))
}

// commit closes a new file at dir's working copy, returning that commit.
func (s *JJFollowCheckpointSuite) commit(dir, name string) string {
	s.Require().NoError(os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644))
	s.jj(dir, "commit", "-m", name)
	return s.parent(dir)
}

func (s *JJFollowCheckpointSuite) parent(dir string) string {
	return s.jj(dir, "log", "-r", "@-", "--no-graph", "-T", "commit_id")
}

func (s *JJFollowCheckpointSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.jj(s.root, "git", "init")
	base := s.commit(s.root, "a.txt")
	s.store = NewStore(Location{StateBase: s.T().TempDir(), CacheDir: s.T().TempDir(), Root: s.root})
	dir := filepath.Join(s.T().TempDir(), "w1")
	wt, made, err := s.store.ForkWorktree(s.T().Context(), "w1", dir, base, "")
	s.Require().NoError(err)
	s.Require().True(made)
	seed(s.T(), s.store, types.Job{ID: "w1", State: types.StateDeclared, WritePaths: []string{"a.txt"}})
	s.row, err = s.store.RecordWorktree(s.T().Context(), "w1", wt)
	s.Require().NoError(err)
}

func (s *JJFollowCheckpointSuite) fork(checkpoint string) (types.JobWorktree, error) {
	wt, made, err := s.store.ForkWorktree(s.T().Context(), "w1", s.row.Worktree.Root, checkpoint, "")
	s.False(made)
	return wt, err
}

func (s *JJFollowCheckpointSuite) TestAHeldCheckoutFastForwardsToADescendant() {
	newer := s.commit(s.root, "b.txt")
	wt, err := s.fork(newer)
	s.Require().NoError(err)
	s.Equal(newer, wt.Base)
	s.Equal(newer, s.parent(wt.Root))
}

func (s *JJFollowCheckpointSuite) TestACheckpointThatLeavesCommitsBehindIsRefused() {
	newer := s.commit(s.root, "b.txt")
	mine := s.commit(s.row.Worktree.Root, "c.txt")
	_, err := s.fork(newer)
	s.Require().ErrorContains(err, "stays at")
	s.Equal(mine, s.parent(s.row.Worktree.Root))
}

func TestAdoptingAtAnAncestorOfTheHeadStaysAtTheHead(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	old := repo.Head()
	repo.Git("commit", "-q", "--allow-empty", "-m", "newer")
	dir := filepath.Join(t.TempDir(), "shared")
	repo.Git("worktree", "add", "-q", "-b", "shared", dir)

	wt, made, err := s.ForkWorktree(t.Context(), "child", dir, old, "")
	require.NoError(t, err)
	assert.False(t, made)
	assert.Equal(t, repo.Head(), wt.Base)
}

func TestInstallProjectsNamesOwnersWithAnInstallTarget(t *testing.T) {
	t.Parallel()
	projects := []*types.Project{
		{Path: "."},
		{Path: "docs", MagusfileTargets: []string{"install"}},
		{Path: "libs/xdg"},
	}
	row := types.Job{WritePaths: []string{"docs/a.md", "docs/b.md", "libs/xdg/x.go", "main.go"}}
	assert.Equal(t, "docs", InstallProjects(t.TempDir(), row, projects))
}

func TestInstallProjectsSaysNoneForAGoOnlyJob(t *testing.T) {
	t.Parallel()
	projects := []*types.Project{{Path: "."}, {Path: "docs", MagusfileTargets: []string{"install"}}}
	assert.Equal(t, "none", InstallProjects(t.TempDir(), types.Job{WritePaths: []string{"main.go"}}, projects))
}

func TestAGoalAddedDuringAForkIsSaidOnce(t *testing.T) {
	t.Parallel()
	s, _ := validateWorkspace(t)
	var notes strings.Builder
	s.notices = &notes
	merge := func(u *types.Job) { u.WritePaths, u.Check = []string{"cli/registry.go"}, forkCheck() }

	_, err := ValidateFork(t.Context(), s.Quiet(), "w", merge, config.Jobs{}, nil)
	require.NoError(t, err)
	_, _ = ForkMerge(t.Context(), s, "w", merge, config.Jobs{}, nil)
	assert.Equal(t, 1, strings.Count(notes.String(), `added the goal "lint"`), notes.String())
}

func TestInstallProjectsSaysNoneForAReadOnlyJob(t *testing.T) {
	t.Parallel()
	projects := []*types.Project{{Path: "docs", MagusfileTargets: []string{"install"}}}
	assert.Equal(t, "none", InstallProjects(t.TempDir(), types.Job{ReadOnly: true}, projects))
}

func TestPlaceBuiltBinaryFindsNoneWhenExeCannotAnswer(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	placed, err := s.PlaceBuiltBinary(t.Context(), filepath.Join(t.TempDir(), "absent"), repo.Head(), t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, placed)
}

// Soft paths come from the symbol index, so warm must raise what cold does.
func TestSoftPathsRaiseNoWorkspaceProblem(t *testing.T) {
	t.Parallel()
	store, _ := validateWorkspace(t)
	cold := types.Job{ID: "w", WritePaths: []string{"docs/site/page.buzz"}, ReadOnly: false, Check: &types.LeaseCheck{Target: "site-generate", Project: "docs"}}
	warm := cold.Clone()
	warm.WritePaths = append(warm.WritePaths, "cli/registry_test.go")
	warm.SoftPaths = []string{"cli/registry_test.go"}

	coldList, _ := workspaceProblems(t.Context(), store, cold, cold, false, nil)
	warmList, _ := workspaceProblems(t.Context(), store, warm, warm, false, nil)
	assert.Equal(t, codes(coldList), codes(warmList))
	assert.NotContains(t, codes(warmList), types.GateNotRun)

	warm.SoftPaths = nil
	declared, _ := workspaceProblems(t.Context(), store, warm, warm, false, nil)
	assert.Contains(t, codes(declared), types.GateNotRun, "the same path, declared, raises its gate")
}

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
	require.NoError(t, os.WriteFile(bin, []byte(versionScript("build 1", build)), 0o755))
	return bin, types.WithMagusBuild(t.Context(), build)
}

// versionScript answers `version -o json` as a magus built as build.
func versionScript(marker string, build types.MagusBuild) string {
	return fmt.Sprintf("#!/bin/sh\n# %s\necho '{\"version\":%q,\"commit\":%q}'\n", marker, build.Version, build.Commit)
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
	assert.Contains(t, string(body), "# build 1\n")
	info, err := os.Stat(wt.Binary)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "the placed binary runs")
}

func TestForkWorktreeSkipsAForkersBinaryBuiltElsewhere(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	bin, ctx := forkersBinary(t, types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: "0123456789abcdef0123456789abcdef01234567"})

	wt, made, err := s.ForkWorktree(ctx, "stale", filepath.Join(t.TempDir(), "stale"), repo.Head(), bin)
	require.NoError(t, err)
	require.True(t, made)
	assert.Empty(t, wt.Binary, "a binary built at another commit is left for provisioning")
	assert.NoFileExists(t, filepath.Join(wt.Root, "magus"))
}

func TestBuiltAtJudgesABinaryByItsOwnVersion(t *testing.T) {
	t.Parallel()
	_, ctx := forkersBinary(t, types.MagusBuild{})
	at := "0123456789abcdef0123456789abcdef01234567"
	clean, _ := forkersBinary(t, types.MagusBuild{Version: "v0.5.0", Commit: at})
	dirty, _ := forkersBinary(t, types.MagusBuild{Version: "v0.5.0-dirty", Commit: at})
	other, _ := forkersBinary(t, types.MagusBuild{Version: "v0.5.0", Commit: "fedcba9876543210fedcba9876543210fedcba98"})

	assert.True(t, BuiltAt(ctx, clean, at))
	assert.False(t, BuiltAt(ctx, dirty, at), "uncommitted changes")
	assert.False(t, BuiltAt(ctx, other, at), "another commit")
	assert.False(t, BuiltAt(ctx, filepath.Join(t.TempDir(), "absent"), at), "no binary")
	assert.False(t, BuiltAt(ctx, "", at), "no path")
}

func TestAdoptedCheckoutWithAStaleBinaryIsNotBuiltAtItsBase(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	dir := filepath.Join(t.TempDir(), "adopted")
	first, ctx := forkersBinary(t, types.MagusBuild{Version: "v0.5.0", Commit: repo.Head()})
	made, _, err := s.ForkWorktree(ctx, "maker", dir, "", first)
	require.NoError(t, err)
	stale := types.MagusBuild{Version: "v0.5.0", Commit: "0123456789abcdef0123456789abcdef01234567"}
	require.NoError(t, os.WriteFile(made.Binary, []byte(versionScript("stale", stale)), 0o755))

	wt, madeNow, err := s.ForkWorktree(ctx, "adopter", dir, "", first)
	require.NoError(t, err)
	assert.False(t, madeNow)
	assert.NotEmpty(t, wt.Binary, "the adopted checkout holds a ./magus")
	assert.False(t, BuiltAt(ctx, wt.Binary, wt.Base), "which the fork must replace")
}

// A checkout holding the bootstrap script builds its own ./magus, so the forker's is never
// copied into it, whatever it was built from: a dirty or older forker never blocks the fork.
// A workspace without the script takes the forker's binary as a tool
// when it is a clean build at the fork's base.
func TestForkWorktreeCopiesNoBinaryIntoACheckoutThatBuildsItsOwn(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	built := repo.Head()
	builds := repo.Commit("builds its own", map[string]string{"magus.yaml": hookConfig, testHook: "namespace b;\n"})

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

	bin, ctx := forkersBinary(t, types.MagusBuild{Version: "v0.5.0", Commit: built})
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
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), "magus.yaml"), []byte(hookConfig), 0o644))
	ctx := types.WithMagusBuild(t.Context(), types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: built})

	for name, tc := range map[string]struct {
		exe, checkpoint, want string
	}{
		"a checkpoint past a linked package": {exe: local, checkpoint: linked, want: CheckoutBootstrap(testHook, repo.Root())},
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

// testHook is the checkout.bootstrap hookConfig declares.
const (
	testHook   = "hack/dev/bootstrap-checkout.buzz"
	hookConfig = "checkout:\n  bootstrap: " + testHook + "\n"
)

type CheckoutHookSuite struct {
	suite.Suite
	root string
}

func TestCheckoutHookSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(CheckoutHookSuite))
}

func (s *CheckoutHookSuite) SetupTest() {
	s.root = s.T().TempDir()
}

func (s *CheckoutHookSuite) writeConfig(body string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus.yaml"), []byte(body), 0o644))
}

func (s *CheckoutHookSuite) TestTheDeclaredHookIsRead() {
	s.writeConfig(hookConfig)
	hook, err := CheckoutHook(s.root)
	s.Require().NoError(err)
	s.Equal(testHook, hook)
}

func (s *CheckoutHookSuite) TestAWorkspaceDeclaringNoneHasNone() {
	hook, err := CheckoutHook(s.root)
	s.Require().NoError(err)
	s.Empty(hook)
}

func (s *CheckoutHookSuite) TestAHookOutsideTheWorkspaceIsAnError() {
	s.writeConfig("checkout:\n  bootstrap: ../elsewhere.buzz\n")
	_, err := CheckoutHook(s.root)
	s.Require().EqualError(err, fmt.Sprintf("job: checkout.bootstrap %q in %s leaves the workspace", "../elsewhere.buzz", s.root))
}

func (s *CheckoutHookSuite) TestTheCommandNamesTheHook() {
	s.Equal("./magus buzz "+testHook+" -- --checkout /w", CheckoutBootstrap(testHook, "/w"))
}

func (s *CheckoutHookSuite) TestNoHookSaysNoneIsDeclared() {
	s.Equal("no checkout.bootstrap declared", CheckoutBootstrap("", "/w"))
}

func (s *CheckoutHookSuite) TestACallMatchesTheHookByItsPathSuffix() {
	s.True(RunsCheckoutHook(filepath.Join(s.root, testHook), testHook))
}

func (s *CheckoutHookSuite) TestNoHookMatchesNoCall() {
	s.False(RunsCheckoutHook(testHook, ""))
}

func (s *CheckoutHookSuite) TestAHookDeclaredButMissingBuildsNothing() {
	s.writeConfig(hookConfig)
	s.False(BuildsItsOwnMagus(s.root))
}

type magusCall struct {
	dir  string
	args string
}

type ReviewCheckoutSuite struct {
	suite.Suite
	repo  *testkit.Repo
	r     ReviewCheckout
	calls []magusCall
	fail  string
}

func TestReviewCheckoutSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ReviewCheckoutSuite))
}

func (s *ReviewCheckoutSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"})
	s.calls, s.fail = nil, ""
	drv, why := resolveDriver(s.T().Context(), s.repo.Root())
	s.Require().NotNil(drv, why)
	s.r = ReviewCheckout{
		Root:   s.repo.Root(),
		Base:   s.T().TempDir(),
		Driver: drv,
		Installs: func(context.Context, string) ([]string, error) {
			return []string{".", "console"}, nil
		},
		Magus: func(_ context.Context, dir string, args ...string) error {
			joined := strings.Join(args, " ")
			s.calls = append(s.calls, magusCall{dir: dir, args: joined})
			if joined == s.fail {
				return errors.New("exit status 1")
			}
			return nil
		},
	}
}

func (s *ReviewCheckoutSuite) declareHook() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.repo.Root(), "magus.yaml"), []byte(hookConfig), 0o644))
}

func (s *ReviewCheckoutSuite) dir(rev string) string {
	return filepath.Join(s.r.Base, "checkout-"+rev[:12])
}

func (s *ReviewCheckoutSuite) TestTheRevisionIsCheckedOutUnderTheBase() {
	head := s.repo.Head()
	rec, err := s.r.Checkout(s.T().Context(), head, "", true)
	s.Require().NoError(err)
	s.Equal(ReviewCheckoutRecord{
		Path:      s.dir(head),
		Revision:  head,
		VCS:       "git",
		Bootstrap: ReviewBootstrap{Install: []string{}, HookNote: skippedBootstrap},
		Remove:    "magus vcs checkout --remove " + s.dir(head),
	}, rec)
	s.FileExists(filepath.Join(rec.Path, "a.txt"))
}

func (s *ReviewCheckoutSuite) TestNoBootstrapRunsNothing() {
	_, err := s.r.Checkout(s.T().Context(), s.repo.Head(), "", true)
	s.Require().NoError(err)
	s.Empty(s.calls)
}

func (s *ReviewCheckoutSuite) TestBootstrapRunsInstallsThenHarnessesThenTheHook() {
	s.declareHook()
	head := s.repo.Head()
	rec, err := s.r.Checkout(s.T().Context(), head, "", false)
	s.Require().NoError(err)
	dir := s.dir(head)
	s.Equal([]magusCall{
		{dir: dir, args: "run install ."},
		{dir: dir, args: "run install console"},
		{dir: dir, args: "agent harness install"},
		{dir: s.repo.Root(), args: fmt.Sprintf("buzz %s -- --checkout %s --install none", testHook, dir)},
	}, s.calls)
	s.Equal(ReviewBootstrap{Ran: true, Install: []string{".", "console"}, Harnesses: true, Hook: testHook}, rec.Bootstrap)
}

func (s *ReviewCheckoutSuite) TestNoHookDeclaredSaysSo() {
	rec, err := s.r.Checkout(s.T().Context(), s.repo.Head(), "", false)
	s.Require().NoError(err)
	s.Equal(ReviewBootstrap{Ran: true, Install: []string{".", "console"}, Harnesses: true, HookNote: "no checkout.bootstrap declared"}, rec.Bootstrap)
}

func (s *ReviewCheckoutSuite) TestAFailedStepKeepsTheCheckoutAndNamesItsRemoval() {
	s.fail = "agent harness install"
	head := s.repo.Head()
	rec, err := s.r.Checkout(s.T().Context(), head, "", false)
	dir := s.dir(head)
	s.Require().EqualError(err, fmt.Sprintf("vcs checkout: readying %s failed, so it stays, and `magus vcs checkout --remove %s` removes it: magus agent harness install: exit status 1", dir, dir))
	s.Equal(dir, rec.Path)
	s.DirExists(dir)
}

func (s *ReviewCheckoutSuite) TestARevisionThatIsNotThereIsRefused() {
	_, err := s.r.Checkout(s.T().Context(), "0123456789abcdef0123456789abcdef01234567", "", true)
	s.Require().ErrorContains(err, "vcs checkout: git cannot find 0123456789abcdef0123456789abcdef01234567")
}

func (s *ReviewCheckoutSuite) TestFetchBringsInARefTheCheckoutNeeds() {
	upstream := testkit.NewRepository(s.T(), map[string]string{"pr.txt": "pr\n"})
	head := upstream.Head()
	upstream.Git("update-ref", "refs/pull/7/head", head)
	s.repo.Git("remote", "add", "origin", upstream.Root())

	rec, err := s.r.Checkout(s.T().Context(), head, "refs/pull/7/head", true)
	s.Require().NoError(err)
	s.Equal(head, rec.Revision)
	s.FileExists(filepath.Join(rec.Path, "pr.txt"))
}

func (s *ReviewCheckoutSuite) TestRemoveRemovesTheCheckout() {
	rec, err := s.r.Checkout(s.T().Context(), s.repo.Head(), "", true)
	s.Require().NoError(err)
	s.Require().NoError(s.r.Remove(s.T().Context(), rec.Path))
	s.NoDirExists(rec.Path)
}

func (s *ReviewCheckoutSuite) TestRemoveRefusesADirectoryOutsideTheBase() {
	elsewhere := s.T().TempDir()
	err := s.r.Remove(s.T().Context(), elsewhere)
	s.Require().EqualError(err, fmt.Sprintf("vcs checkout --remove: %s is not under %s, where magus vcs checkout makes its checkouts", elsewhere, s.r.Base))
}

func (s *ReviewCheckoutSuite) TestRemoveRefusesTheBaseItself() {
	err := s.r.Remove(s.T().Context(), s.r.Base)
	s.Require().ErrorContains(err, "is not under "+s.r.Base)
}

func (s *ReviewCheckoutSuite) TestTheRecordKeepsTheFieldNamesTheConsoleReads() {
	data, err := json.Marshal(ReviewCheckoutRecord{Bootstrap: ReviewBootstrap{Install: []string{}}})
	s.Require().NoError(err)
	s.JSONEq(`{"path":"","revision":"","vcs":"","bootstrap":{"ran":false,"install":[],"harnesses":false,"hook":"","hook_note":""},"remove":""}`, string(data))
}

func (s *ReviewCheckoutSuite) TestInstallTargetsListsOnlyProjectsDeclaringInstall() {
	projects := []*types.Project{
		{Path: "web", MagusfileTargets: []string{"build", "install"}},
		{Path: ".", MagusfileTargets: []string{"install"}},
		{Path: "docs", MagusfileTargets: []string{"build"}},
	}
	s.Equal([]string{".", "web"}, InstallTargets(projects))
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

type ForkAgainSuite struct {
	suite.Suite
	store *Store
	repo  *testkit.Repo
	start string
}

func TestForkAgainSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ForkAgainSuite))
}

func (s *ForkAgainSuite) SetupTest() {
	s.store, _, s.repo = worktreeStore(s.T())
	s.start = s.repo.Head()
}

func (s *ForkAgainSuite) fork(checkpoint string) (types.JobWorktree, error) {
	wt, _, err := s.store.ForkWorktree(s.T().Context(), "w1", filepath.Join(s.T().TempDir(), "w1"), checkpoint, "")
	return wt, err
}

func (s *ForkAgainSuite) branches() string {
	return s.repo.Git("branch", "--list", "w1")
}

func (s *ForkAgainSuite) TestADiscardedForkLeavesNoBranchAndTheIdForksAgainAtANewerRev() {
	wt, err := s.fork("")
	s.Require().NoError(err)
	s.Equal("w1", s.repo.Git("-C", wt.Root, "symbolic-ref", "--short", "HEAD"))
	s.Require().NoError(s.store.DiscardWorktree(s.T().Context(), wt))
	s.NoDirExists(wt.Root)
	s.Empty(s.branches(), "the discarded checkout's branch is gone")

	s.repo.Git("commit", "-q", "--allow-empty", "-m", "newer")
	newer := s.repo.Head()
	again, err := s.fork(newer)
	s.Require().NoError(err)
	s.Equal(newer, again.Base)
}

func (s *ForkAgainSuite) TestAStrandedBranchWithNoCommitsOfItsOwnMovesToTheNewRev() {
	wt, err := s.fork("")
	s.Require().NoError(err)
	s.repo.Git("worktree", "remove", "--force", "--force", wt.Root)
	s.Require().NotEmpty(s.branches(), "a failed removal elsewhere left the branch")

	s.repo.Git("commit", "-q", "--allow-empty", "-m", "newer")
	newer := s.repo.Head()
	again, err := s.fork(newer)
	s.Require().NoError(err)
	s.Equal(newer, s.repo.Git("-C", again.Root, "rev-parse", "HEAD"))
	s.Equal("w1", s.repo.Git("-C", again.Root, "symbolic-ref", "--short", "HEAD"))
}

func (s *ForkAgainSuite) TestABranchWithItsOwnCommitIsRefusedNamingItAndStays() {
	wt, err := s.fork("")
	s.Require().NoError(err)
	s.repo.Git("-C", wt.Root, "commit", "-q", "--allow-empty", "-m", "the job's own work")
	mine := s.repo.Git("rev-parse", "w1")
	s.repo.Git("worktree", "remove", "--force", "--force", wt.Root)

	s.repo.Git("commit", "-q", "--allow-empty", "-m", "newer")
	_, err = s.fork(s.repo.Head())
	s.Require().ErrorContains(err, "branch w1")
	s.ErrorContains(err, "the job's own work")
	s.Equal(mine, s.repo.Git("rev-parse", "w1"))
}

func (s *ForkAgainSuite) TestDiscardKeepsABranchThatMovedPastTheBase() {
	wt, err := s.fork("")
	s.Require().NoError(err)
	s.repo.Git("-C", wt.Root, "commit", "-q", "--allow-empty", "-m", "the job's own work")
	s.Require().NoError(s.store.DiscardWorktree(s.T().Context(), wt))
	s.NotEmpty(s.branches())
}
