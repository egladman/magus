//go:build unix

package ledger

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

// TestStoreDigestRefusesWhatItCannotHash covers the paths digest must not hash. Each of
// them used to answer "absent", which says the releaser DELETED the file - the one
// reading that sends the next agent looking in the wrong place - and the fifo did not
// answer at all.
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

	s := NewStore(Location{CacheDir: t.TempDir(), Root: root})
	_, err = s.Put(ctx, types.Delegation{
		ID:         "u1",
		OwnedPaths: []string{"escape.go", "pipe", "big.bin"},
		State:      types.StateRunning,
	})
	require.NoError(t, err)

	// Released off the test goroutine with a deadline, because the failure this is
	// really about is a HANG: os.Open on a fifo blocks until somebody writes to it, and
	// it would block holding the store's mutex, wedging every other ledger caller.
	type result struct {
		delegation types.Delegation
		err        error
	}
	done := make(chan result, 1)
	go func() {
		u, uerr := s.Update(ctx, "u1", func(u *types.Delegation) { u.OwnedPaths = nil })
		done <- result{delegation: u, err: uerr}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("releasing a fifo blocked; every ledger operation waits behind that lock")
	}
	require.NoError(t, got.err)

	digests := make(map[string]string, len(got.delegation.Releases))
	for _, r := range got.delegation.Releases {
		digests[r.Path] = r.Digest
	}
	assert.Equal(t, types.DigestAbsent, digests["escape.go"],
		"a link out of the workspace is not this workspace's content, and is never followed")
	assert.Equal(t, types.DigestUnreadable, digests["pipe"])
	assert.Equal(t, types.DigestUnreadable, digests["big.bin"])
}

// A symlink INSIDE the root is ordinary content, so resolving links must not turn every
// link into an escape - including the one every macOS temp dir is reached through, where
// the root itself resolves to a different path than it was given.
func TestStoreDigestFollowsALinkThatStaysInside(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.go"), []byte("package real\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(root, "real.go"), filepath.Join(root, "link.go")))

	s := NewStore(Location{CacheDir: t.TempDir(), Root: root})
	_, err := s.Put(ctx, types.Delegation{ID: "u1", OwnedPaths: []string{"link.go"}})
	require.NoError(t, err)
	stored, err := s.Update(ctx, "u1", func(u *types.Delegation) { u.OwnedPaths = nil })
	require.NoError(t, err)
	require.Len(t, stored.Releases, 1)
	assert.Equal(t, "sha256:"+hashOf(t, filepath.Join(root, "real.go")), stored.Releases[0].Digest)
}
