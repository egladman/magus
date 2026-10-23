package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worktreeStore is a job store over a fresh git repository, and that repository.
func worktreeStore(t *testing.T) (*Store, Location, *testkit.Repo) {
	t.Helper()
	repo := execRepo(t)
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
	assert.Empty(t, wt.Binary)
	assert.NoFileExists(t, filepath.Join(wt.Root, "magus"), "a forker running magus from PATH places none")

	again, made, err := s.ForkWorktree(ctx, "w1", wt.Root, "", "")
	require.NoError(t, err)
	assert.False(t, made, "forking the job again into its own checkout makes nothing")
	assert.Equal(t, wt, again)

	_, _, err = s.ForkWorktree(ctx, "w1", filepath.Join(t.TempDir(), "elsewhere"), "", "")
	require.ErrorContains(t, err, "already owns the checkout "+wt.Root)
}

func TestForkWorktreeRefusesACheckpointWithUncommittedChanges(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	dir := filepath.Join(t.TempDir(), "w1")

	_, _, err := s.ForkWorktree(t.Context(), "w1", dir, repo.Head()+"+00112233445566778899aabbccddeeff", "")
	require.ErrorContains(t, err, "uncommitted changes")
	assert.NoDirExists(t, dir)
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

// This repository holds no ./cmd/magus, so whether a build input differs between commits
// cannot be told: the fork makes the checkout at any checkpoint and places the binary only at
// the commit it was built at; elsewhere CheckoutBootstrap builds one.
func TestForkWorktreePlacesABinaryWhoseInputsCannotBeListedOnlyAtItsCommit(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	built := repo.Head()
	later := repo.Commit("later", map[string]string{"b.txt": "b\n"})

	for name, tc := range map[string]struct {
		build      types.MagusBuild
		checkpoint string
		base       string
	}{
		"the forker's revision is past the build": {build: types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: built}, base: later},
		"a checkpoint past the build":             {build: types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: built}, checkpoint: later, base: later},
		"a checkpoint before the build":           {build: types.MagusBuild{Version: "v0.5.0-2-gdef", Commit: later}, checkpoint: built, base: built},
	} {
		t.Run(name, func(t *testing.T) {
			bin, ctx := forkersBinary(t, tc.build)
			wt, made, err := s.ForkWorktree(ctx, name, filepath.Join(t.TempDir(), "w"), tc.checkpoint, bin)
			require.NoError(t, err)
			require.True(t, made)
			assert.Equal(t, tc.base, wt.Base)
			assert.Empty(t, wt.Binary, "a binary built elsewhere is not placed")
			assert.NoFileExists(t, filepath.Join(wt.Root, "magus"))
		})
	}

	for name, tc := range map[string]struct {
		build      types.MagusBuild
		checkpoint string
		want       string
	}{
		"an unstamped build": {
			build: types.MagusBuild{Version: "unknown", Commit: "unknown"}, checkpoint: built,
			want: "records no commit it was built from",
		},
		"a build from uncommitted changes": {
			build: types.MagusBuild{Version: "v0.5.0-1-gabc-dirty", Commit: built}, checkpoint: built,
			want: "built from changes to " + built + " nobody committed",
		},
		"a build at a commit this repository lacks": {
			build: types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: "0123456789abcdef0123456789abcdef01234567"}, checkpoint: built,
			want: "built at 0123456789abcdef0123456789abcdef01234567, which git cannot find",
		},
	} {
		t.Run(name, func(t *testing.T) {
			bin, ctx := forkersBinary(t, tc.build)
			dir := filepath.Join(t.TempDir(), "w1")
			_, _, err := s.ForkWorktree(ctx, "w1", dir, tc.checkpoint, bin)
			require.ErrorContains(t, err, tc.want)
			assert.NoDirExists(t, dir, "nothing is made for a binary whose build cannot be compared")
		})
	}

	bin, ctx := forkersBinary(t, types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: built[:9]})
	wt, made, err := s.ForkWorktree(ctx, "w2", filepath.Join(t.TempDir(), "w2"), built, bin)
	require.NoError(t, err, "an abbreviated build commit resolves to the checkpoint")
	require.True(t, made)
	assert.Equal(t, built, wt.Base)
	assert.FileExists(t, wt.Binary)

	_, made, err = s.ForkWorktree(t.Context(), "w3", filepath.Join(t.TempDir(), "w3"), "", "")
	require.NoError(t, err, "a forker running magus from PATH places nothing, so nothing is compared")
	assert.True(t, made)
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

func TestBuildInputsAreTheChangedPathsALinkedPackageReaches(t *testing.T) {
	t.Parallel()
	root := filepath.FromSlash("/w/a")
	listed := root + "|VERSION|banner.txt\n" +
		filepath.Join(root, "cmd", "magus") + "\n" +
		filepath.Join(root, "internal", "job") + "\n" +
		"\n" +
		filepath.FromSlash("/usr/local/go/src/fmt") + "\n" +
		filepath.FromSlash("/home/u/go/pkg/mod/x@v1/y") + "\n"
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

// goModuleRepo is a git checkout holding a Go module whose ./cmd/magus links lib, beside a
// package nothing links and a docs file.
func goModuleRepo(t *testing.T) *testkit.Repo {
	t.Helper()
	return testkit.NewRepo(t, map[string]string{
		"go.mod":            "module example.com/m\n\ngo 1.26\n",
		"cmd/magus/main.go": "package main\n\nimport \"example.com/m/lib\"\n\nfunc main() { lib.Run() }\n",
		"lib/lib.go":        "package lib\n\nfunc Run() {}\n",
		"lib/lib_test.go":   "package lib\n",
		"unlinked/u.go":     "package unlinked\n",
		"docs/a.md":         "a\n",
	})
}

func TestForkWorktreePlacesABinaryWhoseBuildInputsMatchTheBase(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		files  map[string]string
		placed bool
	}{
		"a docs edit":                   {files: map[string]string{"docs/a.md": "b\n"}, placed: true},
		"a test file":                   {files: map[string]string{"lib/lib_test.go": "package lib\n\n"}, placed: true},
		"a package nothing links":       {files: map[string]string{"unlinked/u.go": "package unlinked\n\n"}, placed: true},
		"a linked package's source":     {files: map[string]string{"lib/lib.go": "package lib\n\nfunc Run() { _ = 1 }\n"}},
		"a file a linked package holds": {files: map[string]string{"lib/data.txt": "x\n"}},
		"the module file":               {files: map[string]string{"go.mod": "module example.com/m\n\ngo 1.26.0\n"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := goModuleRepo(t)
			s := NewStore(Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: repo.Root()})
			built := repo.Head()
			later := repo.Commit(name, tc.files)
			bin, ctx := forkersBinary(t, types.MagusBuild{Version: "v0.5.0-1-gabc", Commit: built})
			wt, made, err := s.ForkWorktree(ctx, "past", filepath.Join(t.TempDir(), "w"), later, bin)
			require.NoError(t, err)
			require.True(t, made)
			assert.Equal(t, later, wt.Base)
			assert.Equal(t, tc.placed, wt.Binary != "", "placed")

			// A binary built past the base is compared the same way.
			bin, ctx = forkersBinary(t, types.MagusBuild{Version: "v0.5.0-2-gdef", Commit: later})
			wt, _, err = s.ForkWorktree(ctx, "before", filepath.Join(t.TempDir(), "w"), built, bin)
			require.NoError(t, err)
			assert.Equal(t, built, wt.Base)
			assert.Equal(t, tc.placed, wt.Binary != "", "placed at the older base")
		})
	}
}

func TestChangedBuildInputsFailsWhereThePackagesCannotBeListed(t *testing.T) {
	t.Parallel()
	repo := execRepo(t)
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
