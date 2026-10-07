package proc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPoolSocketName(t *testing.T) {
	assert.True(t, IsPoolSocketName("magus-1314-2a844690.sock"))
	assert.False(t, IsPoolSocketName("server.sock"))
	assert.False(t, IsPoolSocketName("broker.sock"))
	assert.False(t, IsPoolSocketName("magus-stale.sock"))
}

func TestPoolSocketPath(t *testing.T) {
	assert.Equal(t, "/tmp/magus-9-abcd.sock", PoolSocketPath("unix:///tmp/magus-9-abcd.sock"))
	assert.Equal(t, "/tmp/magus-9-abcd.sock", PoolSocketPath("/tmp/magus-9-abcd.sock"))
	assert.Empty(t, PoolSocketPath("unix:///tmp/server.sock"))
	assert.Empty(t, PoolSocketPath("unix:///tmp/broker.sock"))
	assert.Empty(t, PoolSocketPath(""))
}

func TestStalePoolsIn(t *testing.T) {
	// Short prefix: t.TempDir() paths exceed darwin's unix socket limit once the
	// magus-<pid>-<rand>.sock name is appended (same constraint as privateSockDir).
	root, err := os.MkdirTemp("", "mg_")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("XDG_RUNTIME_DIR", root)
	t.Setenv("MAGUS_PROC_SOCKET", "")
	dir := SockDir()

	addr := "unix://" + filepath.Join(dir, "magus-424242-deadbeef.sock")
	srv, err := New(Options{
		Address: addr,
		Version: "stale-build",
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	ctx := context.Background()
	got, err := StalePoolsIn(ctx, dir, "current-build")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, StalePool{Addr: addr, ParentPID: got[0].ParentPID, Version: "stale-build"}, got[0])

	same, err := StalePoolsIn(ctx, dir, "stale-build")
	require.NoError(t, err)
	assert.Empty(t, same, "matching version is not stale")

	empty, err := StalePoolsIn(ctx, dir, "")
	require.NoError(t, err)
	assert.Empty(t, empty, "empty selfVersion disables the check")
}
