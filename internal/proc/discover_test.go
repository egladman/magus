package proc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStableSocketName(t *testing.T) {
	assert.Equal(t, "magus-daemon.sock", StableSocketName())
}

// isolateSockDir points SockDir() at a fresh empty dir so a real daemon on the
// developer's machine can never leak into these tests.
func isolateSockDir(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	dir := SockDir()
	require.DirExists(t, dir)
	return dir
}

func TestLookupStableSocket_AbsentWhenNoLiveSocket(t *testing.T) {
	isolateSockDir(t)
	addr, ok := LookupStableSocket(context.Background())
	assert.False(t, ok)
	assert.Empty(t, addr)
}

func TestDiscoverSocket_NoneFound(t *testing.T) {
	isolateSockDir(t)
	_, err := DiscoverSocket(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no running magus proc server")
}

func TestDiscoverSocket_SkipsNonSocketsAndNonMatches(t *testing.T) {
	dir := isolateSockDir(t)
	// A plain file matching the magus-*.sock name is not a live socket: isSocketLive
	// dials it and fails, so it is filtered out. A non-matching name and a stale stable
	// socket file are skipped by the name filters. Net result: still "none found".
	for _, name := range []string{"magus-stale.sock", "unrelated.txt", StableSocketName()} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}
	_, err := DiscoverSocket(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no running magus proc server")
}

func TestDecodeWireError(t *testing.T) {
	// Each server-side error STRING round-trips back to its typed sentinel so
	// errors.Is keeps working across the wire.
	for _, sentinel := range []error{ErrProtocolMismatch, ErrVersionMismatch, ErrCycleDetected} {
		assert.ErrorIs(t, decodeWireError(sentinel.Error()), sentinel)
	}

	// ErrNotAdoptable is carried as a prefix plus context; the sentinel is preserved
	// and the trailing detail is kept in the message.
	got := decodeWireError(ErrNotAdoptable.Error() + ": run only")
	assert.ErrorIs(t, got, ErrNotAdoptable)
	assert.Contains(t, got.Error(), "run only")

	// An unrecognized message becomes a plain error, matching none of the sentinels.
	plain := decodeWireError("something else entirely")
	assert.NotErrorIs(t, plain, ErrProtocolMismatch)
	assert.Equal(t, "something else entirely", plain.Error())
	assert.False(t, errors.Is(plain, ErrNotAdoptable))
}

// TestDiscoverSocketReapsDeadSockets covers the sweep. Sockets are named
// magus-<pid>-<rand>.sock, so no new server ever collides with an old path and the
// EADDRINUSE reclaim in Server.Start can never fire; without this, a machine accumulates
// dead sockets until someone notices (39 on the one that prompted it).
func TestDiscoverSocketReapsDeadSockets(t *testing.T) {
	dir := isolateSockDir(t)

	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, nil, 0o600))
		when := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(p, when, when))
		return p
	}

	old := write("magus-111-deadbeef.sock", time.Hour)
	fresh := write("magus-222-cafebabe.sock", time.Second)
	unrelated := write("notes.txt", time.Hour)

	_, err := DiscoverSocket(context.Background())
	require.Error(t, err, "no live socket, so discovery still reports none found")

	assert.NoFileExists(t, old, "a long-dead socket is swept")
	assert.FileExists(t, fresh,
		"inside the grace period: a server may have created the file and not yet called listen")
	assert.FileExists(t, unrelated, "only magus-*.sock is ever touched")
}
