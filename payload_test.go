package magus

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
)

// linkedWorktrees makes a git repository with a magusfile and one linked worktree per name,
// and returns the primary checkout and the worktrees in name order. Paths are symlink-free,
// so they compare equal to the roots a workspace records.
func linkedWorktrees(t *testing.T, names ...string) (primary string, worktrees []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	primary, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", primary}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(primary, "magusfile.buzz"), nil, 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for _, name := range names {
		tree := filepath.Join(base, name)
		git("worktree", "add", "-q", tree)
		worktrees = append(worktrees, tree)
	}
	return primary, worktrees
}

// A hook the host ran from the primary checkout stored its verdict there, while the worker
// it was shown to sits in a linked worktree: the ref still resolves from the worktree, and
// a miss names both stores it searched.
func TestPayloadByRefReadsThePrimaryCheckoutFromALinkedWorktree(t *testing.T) {
	primary, worktrees := linkedWorktrees(t, "worker")
	worktree := worktrees[0]

	primaryCache, err := ResolveCacheDir(primary)
	require.NoError(t, err)
	ref, _ := trail.WriteBlob(context.Background(), primaryCache, "grd", []byte("the full verdict"))
	require.NotEmpty(t, ref)

	opened, err := Inspect(context.Background(), worktree)
	require.NoError(t, err)
	m := opened.(*Magus)
	got, err := m.PayloadByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "the full verdict", string(got))

	missing := "grd" + strings.Repeat("0", 16)
	_, err = m.PayloadByRef(missing)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), m.CacheDir())
	assert.Contains(t, err.Error(), primaryCache)

	opened, err = Inspect(context.Background(), primary)
	require.NoError(t, err)
	_, err = opened.(*Magus).PayloadByRef(missing)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.NotContains(t, err.Error(), m.CacheDir(), "the primary checkout reads its own store alone")
}

// recordRun files, in the session store of the repository store belongs to, the fact
// `magus session` would hold for a target that ran in workspace and
// printed ref: the only thing that ties a ref to the checkout whose store has it.
func recordRun(t *testing.T, store, workspace, ref string) {
	t.Helper()
	dir, err := sessions.Dir(store)
	require.NoError(t, err)
	w, err := sessions.Open(dir, "inv"+strings.ToLower(strings.NewReplacer("/", "", "-", "").Replace(filepath.Base(workspace))), sessions.InvocationStart{Workspace: workspace})
	require.NoError(t, err)
	require.NoError(t, w.Append(sessions.KindTargetResult, sessions.TargetResult{Target: "lint", Project: ".", Outcome: sessions.OutcomeFail, Ref: ref}))
}

// storeRun puts a failed run's output and key inputs in workspace's own output store and
// returns the ref it prints.
func storeRun(t *testing.T, workspace string) string {
	t.Helper()
	dir, err := ResolveCacheDir(workspace)
	require.NoError(t, err)
	store := cache.NewOutputStore(dir)
	key := strings.Repeat("ab", 32)
	d, err := store.Persist(context.Background(), key, []byte("lint log\n"), cache.OutputDescriptor{Project: ".", Target: "lint", Failed: true, ErrMsg: "findings"})
	require.NoError(t, err)
	require.NoError(t, store.PersistKeyInputs(context.Background(), key, []string{"os:linux"}))
	return d.Ref
}

func storeFiles(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	files := map[string]int64{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = info.Size() + info.ModTime().UnixNano()
		return nil
	}))
	return files
}

// A ref a worker is handed was printed by another worktree's run. The session store is shared
// by every checkout of the repository and records which one ran it, so every ref lookup reads
// that checkout's output store, read-only, and says so.
func TestOutputLookupsReadTheCheckoutThatRanTheRef(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, trees := linkedWorktrees(t, "worker", "orchestrator")
	worker, orchestrator := trees[0], trees[1]
	ref := storeRun(t, orchestrator)
	recordRun(t, worker, orchestrator, ref)
	holderCache, err := ResolveCacheDir(orchestrator)
	require.NoError(t, err)
	before := storeFiles(t, holderCache)

	var notices bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&notices, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	opened, err := Inspect(context.Background(), worker)
	require.NoError(t, err)
	m := opened.(*Magus)

	data, desc, err := m.OutputByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "lint log\n", string(data))
	assert.Equal(t, "lint", desc.Target)
	assert.Contains(t, notices.String(), orchestrator, "stderr says which checkout was read")

	data, _, err = m.OutputByRefRemote(context.Background(), ref[:8])
	require.NoError(t, err, "a unique prefix resolves in the holder as it does locally")
	assert.Equal(t, "lint log\n", string(data))

	d, err := m.OutputDescriptorByRef(ref)
	require.NoError(t, err)
	assert.True(t, d.Failed)
	assert.Equal(t, "findings", d.ErrMsg)

	attempts, err := m.OutputAttempts(ref)
	require.NoError(t, err)
	assert.Len(t, attempts, 1)

	lines, err := m.OutputKeyInputs(ref)
	require.NoError(t, err)
	assert.Equal(t, []string{"os:linux"}, lines)

	workerCache, err := ResolveCacheDir(worker)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(workerCache, "outputs"), "nothing is copied into the reading checkout")
	assert.Equal(t, before, storeFiles(t, holderCache), "the holder's store is read, never written")
}

// A miss says who holds the ref when the session store knows, and how to read it there. The
// holder may be gone with its checkout, or alive with the output aged out; an unrecorded ref
// still names the command.
func TestOutputMissNamesTheCheckoutThatRanTheRef(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, trees := linkedWorktrees(t, "worker", "orchestrator")
	worker, orchestrator := trees[0], trees[1]
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	const (
		goneRef  = "outdead00000001"
		agedRef  = "outaged00000002"
		otherRef = "outfeed00000003"
	)
	recordRun(t, worker, gone, goneRef)
	recordRun(t, worker, orchestrator, agedRef)

	opened, err := Inspect(context.Background(), worker)
	require.NoError(t, err)
	m := opened.(*Magus)
	lookups := map[string]func(ref string) error{
		"OutputByRef":           func(ref string) error { _, _, err := m.OutputByRef(ref); return err },
		"OutputByRefRemote":     func(ref string) error { _, _, err := m.OutputByRefRemote(context.Background(), ref); return err },
		"OutputDescriptorByRef": func(ref string) error { _, err := m.OutputDescriptorByRef(ref); return err },
		"OutputAttempts":        func(ref string) error { _, err := m.OutputAttempts(ref); return err },
	}
	for name, lookup := range lookups {
		t.Run(name, func(t *testing.T) {
			var missing *cache.RefNotFoundError

			err := lookup(goneRef)
			require.ErrorIs(t, err, fs.ErrNotExist)
			require.ErrorAs(t, err, &missing)
			assert.Equal(t, gone, missing.Holder)
			assert.True(t, missing.HolderGone)
			assert.Contains(t, err.Error(), "recorded by "+gone+", which is no longer on disk")
			assert.Contains(t, err.Error(), "`cd "+gone+" && magus query output "+goneRef+"`")

			err = lookup(agedRef)
			require.ErrorIs(t, err, fs.ErrNotExist)
			require.ErrorAs(t, err, &missing)
			assert.Equal(t, orchestrator, missing.Holder)
			assert.False(t, missing.HolderGone)
			assert.Contains(t, err.Error(), "checkout "+orchestrator, "the holder's store was read and named")
			assert.Contains(t, err.Error(), "`cd "+orchestrator+" && magus query output "+agedRef+"`")

			err = lookup(otherRef)
			require.ErrorIs(t, err, fs.ErrNotExist)
			require.ErrorAs(t, err, &missing)
			assert.Empty(t, missing.Holder)
			assert.Contains(t, err.Error(), "if another checkout ran it, read it there with `magus query output "+otherRef+"`")
		})
	}
}
