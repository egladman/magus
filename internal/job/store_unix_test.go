//go:build unix

// This file has no store_unix.go twin on purpose: it tests store.go's digest against a
// fifo and a symlink, which only a unix build can create.

package job

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreDigestRefusesWhatItCannotHash covers the paths digest must not hash. "absent"
// says the releaser DELETED the file, which sends the next agent looking in the wrong
// place, and a released fifo blocks os.Open while the store's mutex is held.
//
// Unix-only because it needs a fifo and a symlink; the rules they prove are not.
func TestStoreDigestRefusesWhatItCannotHash(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	require.NoError(t, os.WriteFile(outside, []byte("package outside\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape.go")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644))
	big, err := os.Create(filepath.Join(root, "big.bin"))
	require.NoError(t, err)
	require.NoError(t, big.Truncate(maxDigestBytes+1), "sparse, so the cap is exercised without writing the bytes")
	require.NoError(t, big.Close())

	s := tmpStore(t, root)
	seed(t, s, types.Job{
		ID:         "u1",
		WritePaths: []string{"escape.go", "pipe", "big.bin"},
		State:      types.StateRunning,
	})

	// Released off the test goroutine with a deadline, because the failure this is
	// really about is a HANG: os.Open on a fifo blocks until somebody writes to it, and
	// it would block holding the store's mutex, wedging every other ledger caller.
	type result struct {
		lease types.Job
		err   error
	}
	done := make(chan result, 1)
	go func() {
		u, uerr := s.Update(ctx, "u1", func(u *types.Job) { u.WritePaths = nil })
		done <- result{lease: u, err: uerr}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("releasing a fifo blocked; every ledger operation waits behind that lock")
	}
	require.NoError(t, got.err)

	digests := make(map[string]string, len(got.lease.Releases))
	for _, r := range got.lease.Releases {
		digests[r.Path] = r.Digest
	}
	assert.Equal(t, types.DigestAbsent, digests["escape.go"],
		"a link out of the workspace is not this workspace's content, and is never followed")
	assert.Equal(t, types.DigestUnreadable, digests["pipe"])
	assert.Equal(t, types.DigestUnreadable, digests["big.bin"])
}

// A symlink INSIDE the root is ordinary content, so resolving links must not turn every
// link into an escape, including the one every macOS temp dir is reached through, where
// the root itself resolves to a different path than it was given.
func TestStoreDigestFollowsALinkThatStaysInside(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.go"), []byte("package real\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(root, "real.go"), filepath.Join(root, "link.go")))

	s := tmpStore(t, root)
	seed(t, s, types.Job{ID: "u1", WritePaths: []string{"link.go"}})
	stored, err := s.Update(ctx, "u1", func(u *types.Job) { u.WritePaths = nil })
	require.NoError(t, err)
	require.Len(t, stored.Releases, 1)
	assert.Equal(t, "sha256:"+hashOf(t, filepath.Join(root, "real.go")), stored.Releases[0].Digest)
}

// TestDeleteRemovesOneRowAndRefusesATerminalOneWithoutForce pins the difference between
// removing a row and ENDING a job. Exit records what happened and the row is the account
// of it; rm is for a row that should not exist, so the account is protected by default.
func TestDeleteRemovesOneRowAndRefusesATerminalOneWithoutForce(t *testing.T) {
	loc := tmpLoc(t, t.TempDir())
	store := NewStore(loc)

	for _, id := range []string{"keep", "drop"} {
		_, err := store.Update(t.Context(), id, func(row *types.Job) { row.WritePaths = []string{"internal/job"} })
		require.NoError(t, err)
	}

	dropped, err := store.Delete(t.Context(), "drop", false)
	require.NoError(t, err)
	assert.Equal(t, "drop", dropped.ID)

	rows, err := store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "the other row is untouched, which is the difference from Clear")
	assert.Equal(t, "keep", rows[0].ID)

	_, err = store.Delete(t.Context(), "gone", false)
	assert.ErrorContains(t, err, `there is no job "gone"`)

	_, err = store.Update(t.Context(), "keep", func(row *types.Job) { row.State = types.StatePass })
	require.NoError(t, err)
	_, err = store.Delete(t.Context(), "keep", false)
	assert.ErrorContains(t, err, "the record of what happened", "a terminal row is the account of the work")

	_, err = store.Delete(t.Context(), "keep", true)
	require.NoError(t, err, "--force is the caller saying they meant it")
	rows, err = store.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// TestDeleteRefusesABoundHolder pins that a worker cannot remove the record that work was
// handed to it. Ending its own row stays allowed; that is the report it owes.
func TestDeleteRefusesABoundHolder(t *testing.T) {
	loc := tmpLoc(t, t.TempDir())
	store := NewStore(loc)
	_, err := store.Update(t.Context(), "held", func(row *types.Job) { row.WritePaths = []string{"internal/job"} })
	require.NoError(t, err)

	bound := NewStore(Location{CacheDir: loc.CacheDir, Root: loc.Root, StateBase: loc.StateBase, Actor: &Actor{Lease: "held"}})
	_, err = bound.Delete(t.Context(), "held", true)

	var refused *RefusedError
	require.ErrorAs(t, err, &refused, "a holder deleting its own row is a refusal, not a failure")
	assert.Contains(t, refused.Rule, "handed out")
}
