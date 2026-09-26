//go:build linux

package endpoint

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A socket path far past sun_path is bound and dialed through its directory, and the
// socket is where the path says: the listener reports it, a byte crosses it, and
// closing the listener removes it.
func TestAPathLongerThanSunPathBindsAndDialsOnLinux(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 200 {
		dir = filepath.Join(dir, strings.Repeat("d", 50))
	}
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "magus-99999-0123abcd.sock")
	ep := Endpoint{Scheme: "unix", Addr: path}

	ln, err := ep.Listen()
	require.NoError(t, err)
	assert.Equal(t, path, ln.Addr().String())
	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, os.ModeSocket, info.Mode().Type())

	got := make(chan byte, 1)
	go func() {
		defer close(got)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b := make([]byte, 1)
		if _, err := io.ReadFull(conn, b); err == nil {
			got <- b[0]
		}
	}()
	conn, err := ep.Dial(t.Context())
	require.NoError(t, err)
	_, err = conn.Write([]byte{42})
	require.NoError(t, err)
	assert.Equal(t, byte(42), <-got)
	require.NoError(t, conn.Close())

	require.NoError(t, ln.Close())
	assert.NoFileExists(t, path)
}

// A name reached through its directory still has to fit after /proc/self/fd/<fd>/.
func TestASocketNameTooLongForSunPathIsRefusedOnLinux(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("s", 100)+".sock")
	_, err := Endpoint{Scheme: "unix", Addr: path}.Listen()
	assert.ErrorContains(t, err, "is 105 bytes, and linux holds at most 107")
}
