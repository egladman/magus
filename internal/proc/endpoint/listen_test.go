package endpoint

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// longPath returns a path past sun_path on linux, where the /proc/self/fd trick can
// reach it, and a short one elsewhere, where nothing can. It starts from a short-named
// temp dir, not t.TempDir(), whose embedded test name alone can cross the limit.
func longPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ep")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if runtime.GOOS == "linux" {
		for len(dir) < 200 {
			dir = filepath.Join(dir, strings.Repeat("d", 50))
		}
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	return filepath.Join(dir, name)
}

// A caller that needs the concrete *net.UnixListener - to duplicate its descriptor, or
// drive SetUnlinkOnClose - still reaches a path past sun_path, and DialUnix reaches the
// same listener back.
func TestListenUnixBindsAndDialsALongPath(t *testing.T) {
	path := longPath(t, "magus-99999-0123abcd.sock")

	ln, err := ListenUnix(path)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, os.ModeSocket, info.Mode().Type())

	accepted := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()
	conn, err := DialUnix(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.NoError(t, <-accepted)
}

// ListenUnixgram binds a datagram socket the same way ListenUnix binds a stream one.
func TestListenUnixgramBindsALongPath(t *testing.T) {
	path := longPath(t, "magus-gram.sock")

	conn, err := ListenUnixgram(path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, os.ModeSocket, info.Mode().Type())
}
