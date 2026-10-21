package magus

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
)

// linkedWorktrees makes a git repository with a magusfile and one linked worktree per name,
// and returns the primary checkout and the worktrees in name order. Paths are symlink-free,
// so they compare equal to the roots a workspace records.
func linkedWorktrees(t *testing.T, names ...string) (primary string, worktrees []string) {
	t.Helper()
	repo := testkit.NewRepo(t, map[string]string{"magusfile.buzz": ""})
	for _, name := range names {
		worktrees = append(worktrees, repo.AddWorktree(name).Root())
	}
	return repo.Root(), worktrees
}

// recordSessionRun files, in the session store of the repository repoRoot belongs to, the fact
// `magus session` would hold for a target that ran in workspace and printed ref: the only
// thing that ties a ref to the checkout whose store has it.
func recordSessionRun(t *testing.T, repoRoot, workspace, ref string) {
	t.Helper()
	dir, err := sessions.Dir(repoRoot)
	require.NoError(t, err)
	w, err := sessions.Open(dir, "inv"+strings.ToLower(strings.NewReplacer("/", "", "-", "").Replace(filepath.Base(workspace))), sessions.InvocationStart{Workspace: workspace})
	require.NoError(t, err)
	require.NoError(t, w.Append(sessions.KindTargetResult, sessions.TargetResult{Target: "lint", Project: ".", Outcome: sessions.OutcomeFail, Ref: ref}))
}

// persistedRunRef puts a failed run's output and key inputs in workspace's own output store and
// returns the ref it prints.
func persistedRunRef(t *testing.T, workspace string) string {
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

// fileFingerprints maps every file under dir to its size plus modification time, so two
// calls compare equal only when nothing under dir was written.
func fileFingerprints(t *testing.T, dir string) map[string]int64 {
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
	testkit.Isolate(t)
	_, trees := linkedWorktrees(t, "worker", "orchestrator")
	worker, orchestrator := trees[0], trees[1]
	ref := persistedRunRef(t, orchestrator)
	recordSessionRun(t, worker, orchestrator, ref)
	holderCache, err := ResolveCacheDir(orchestrator)
	require.NoError(t, err)
	before := fileFingerprints(t, holderCache)

	var notices bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&notices, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx := t.Context()
	opened, err := Inspect(ctx, worker, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	m := opened.(*Magus)

	data, desc, err := m.OutputByRef(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "lint log\n", string(data))
	assert.Equal(t, "lint", desc.Target)
	assert.Contains(t, notices.String(), orchestrator, "stderr says which checkout was read")

	data, _, err = m.OutputByRefRemote(ctx, ref[:8])
	require.NoError(t, err, "a unique prefix resolves in the other checkout as it does locally")
	assert.Equal(t, "lint log\n", string(data))

	d, err := m.OutputDescriptorByRef(ctx, ref)
	require.NoError(t, err)
	assert.True(t, d.Failed)
	assert.Equal(t, "findings", d.ErrMsg)

	attempts, err := m.OutputAttempts(ctx, ref)
	require.NoError(t, err)
	assert.Len(t, attempts, 1)

	lines, err := m.OutputKeyInputs(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, []string{"os:linux"}, lines)

	workerCache, err := ResolveCacheDir(worker)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(workerCache, "outputs"), "nothing is copied into the reading checkout")
	assert.Equal(t, before, fileFingerprints(t, holderCache), "the other checkout's store is read, never written")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = m.OutputByRef(cancelled, ref)
	require.ErrorIs(t, err, context.Canceled, "an ended context stops the search of other checkouts")
}

// A portable ref is the same in every checkout that ran the key. The key inputs belong with
// the descriptor the caller already read: a checkout whose descriptor resolves but whose run
// predates key-input persistence answers "no lines", and the search must not carry on to
// another checkout's lines for the same ref.
func TestOutputKeyInputsStopAtTheCheckoutWhoseDescriptorResolved(t *testing.T) {
	primary, trees := linkedWorktrees(t, "worker", "orchestrator")
	worker, orchestrator := trees[0], trees[1]
	ref := persistedRunRef(t, primary)

	dir, err := ResolveCacheDir(orchestrator)
	require.NoError(t, err)
	d, err := cache.NewOutputStore(dir).Persist(context.Background(), strings.Repeat("ab", 32), []byte("lint log\n"),
		cache.OutputDescriptor{Project: ".", Target: "lint", Failed: true})
	require.NoError(t, err)
	require.Equal(t, ref, d.Ref, "the same key mints the same portable ref")
	recordSessionRun(t, worker, orchestrator, ref)

	ctx := t.Context()
	opened, err := Inspect(ctx, worker, WithSpawnRule(TierCeiling))
	require.NoError(t, err)

	_, err = opened.(*Magus).OutputKeyInputs(ctx, ref)

	assert.ErrorIs(t, err, fs.ErrNotExist)
}

// A miss says which checkout to read the ref in when the session store knows one whose
// store was not read: it may be gone with its worktree. One that was read and missed (the
// output aged out) is named among the stores consulted, never as where to read it. An
// unrecorded ref still names the command.
func TestOutputMissNamesTheCheckoutThatRanTheRef(t *testing.T) {
	testkit.Isolate(t)
	_, trees := linkedWorktrees(t, "worker", "orchestrator")
	worker, orchestrator := trees[0], trees[1]
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	const (
		goneRef  = "outdead00000001"
		agedRef  = "outa9ed00000002"
		otherRef = "outfeed00000003"
	)
	recordSessionRun(t, worker, gone, goneRef)
	recordSessionRun(t, worker, orchestrator, agedRef)

	ctx := t.Context()
	opened, err := Inspect(ctx, worker, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	m := opened.(*Magus)
	lookups := map[string]func(ref string) error{
		"OutputByRef":           func(ref string) error { _, _, err := m.OutputByRef(ctx, ref); return err },
		"OutputByRefRemote":     func(ref string) error { _, _, err := m.OutputByRefRemote(ctx, ref); return err },
		"OutputDescriptorByRef": func(ref string) error { _, err := m.OutputDescriptorByRef(ctx, ref); return err },
		"OutputAttempts":        func(ref string) error { _, err := m.OutputAttempts(ctx, ref); return err },
	}
	for name, lookup := range lookups {
		t.Run(name, func(t *testing.T) {
			var missing *cache.RefNotFoundError

			err := lookup(goneRef)
			require.ErrorIs(t, err, fs.ErrNotExist)
			require.ErrorAs(t, err, &missing)
			assert.Equal(t, gone, missing.UnreadCheckout)
			assert.True(t, missing.UnreadCheckoutGone)
			var again *cache.RefNotFoundError
			require.ErrorAs(t, err, &again)
			assert.Same(t, missing, again, "the miss is one error, not rebuilt on every unwrap")
			assert.Contains(t, err.Error(), "recorded by "+gone+", which is no longer on disk")
			assert.Contains(t, err.Error(), "`cd "+gone+" && magus query output "+goneRef+"`")

			err = lookup(agedRef)
			require.ErrorIs(t, err, fs.ErrNotExist)
			require.ErrorAs(t, err, &missing)
			assert.Empty(t, missing.UnreadCheckout, "its store was read and missed, so reading it there cannot help")
			assert.Contains(t, err.Error(), "checkout "+orchestrator, "the store read is named among those consulted")
			assert.NotContains(t, err.Error(), "cd "+orchestrator)

			err = lookup(otherRef)
			require.ErrorIs(t, err, fs.ErrNotExist)
			require.ErrorAs(t, err, &missing)
			assert.Empty(t, missing.UnreadCheckout)
			assert.Contains(t, err.Error(), "if another checkout ran it, read it there with `magus query output "+otherRef+"`")
		})
	}
}

// A fact may record this checkout under a symlinked path; it is still this checkout, so it
// is neither searched a second time nor named as where the ref lives.
func TestRecordedCheckoutsLeaveOutThisCheckoutThroughASymlink(t *testing.T) {
	testkit.Isolate(t)
	_, trees := linkedWorktrees(t, "worker", "orchestrator")
	worker, orchestrator := trees[0], trees[1]
	alias := filepath.Join(t.TempDir(), "worker-link")
	require.NoError(t, os.Symlink(worker, alias))
	const ref = "outbeef00000004"
	recordSessionRun(t, worker, alias, ref)
	recordSessionRun(t, worker, orchestrator, ref)

	opened, err := Inspect(t.Context(), worker, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	assert.Equal(t, []string{orchestrator}, opened.(*Magus).recordedCheckouts(t.Context(), ref))
}

// A session store that cannot be read leaves the other checkouts unconsulted; the lookup
// says so instead of answering as though no other checkout ran the ref.
func TestRecordedCheckoutsWarnWhenTheStoreCannotBeRead(t *testing.T) {
	testkit.Isolate(t)
	_, trees := linkedWorktrees(t, "worker")
	opened, err := Inspect(t.Context(), trees[0], WithSpawnRule(TierCeiling))
	require.NoError(t, err)

	var notices bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&notices, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")

	assert.Empty(t, opened.(*Magus).recordedCheckouts(t.Context(), "outbeef00000005"))
	assert.Contains(t, notices.String(), "other checkouts were not consulted")
}

// A fact's ref is matched by prefix either way, but only between ref-shaped strings: the
// store resolves nothing for a bare "out", so neither may a fact.
func TestRecordedRefMatchesOnlyRefShapedPrefixes(t *testing.T) {
	cases := []struct {
		recorded, ref string
		want          bool
	}{
		{"outab12cd34", "outab12", true},
		{"outab12", "outab12cd34", true},
		{"outab12cd34", "out", false},
		{"outab12cd34", "outff", false},
		{"", "outab12", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, recordedRefMatches(c.recorded, c.ref), "recorded %q, ref %q", c.recorded, c.ref)
	}
}

// An imported record's failed_need_target and failed_need_ref survive the read when real; a
// forged pair is made harmless (the target neutralized, the ref dropped unless minted)
// and the record is still read; only an oversized target is refused, by the descriptor check.
func TestOutputImportReadsTheFailedNeed(t *testing.T) {
	got, err := readRecords(outputRecordLine(t, func(r map[string]any) {
		r["failed_need_target"] = "lint-go"
		r["failed_need_ref"] = "out0123456789ab"
	}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, [2]string{"lint-go", "out0123456789ab"}, [2]string{got[0].FailedNeedTarget, got[0].FailedNeedRef})

	got, err = readRecords(outputRecordLine(t, func(r map[string]any) {
		r["failed_need_target"] = "lint\x1b]0;t\x07"
		r["failed_need_ref"] = "x; rm -rf ~"
	}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, [2]string{"lint<U+001B>]0;t<U+0007>", ""}, [2]string{got[0].FailedNeedTarget, got[0].FailedNeedRef})

	got, err = readRecords(outputRecordLine(t, func(r map[string]any) { r["failed_need_target"] = "lint\n" }))
	require.NoError(t, err, "a line break in the member is escaped, not refused")
	require.Len(t, got, 1)
	assert.Equal(t, "lint<U+000A>", got[0].FailedNeedTarget)

	_, err = readRecords(outputRecordLine(t, func(r map[string]any) { r["failed_need_target"] = strings.Repeat("n", 513) }))
	assert.ErrorContains(t, err, "line 1: failed_need_target: longer than")
}
