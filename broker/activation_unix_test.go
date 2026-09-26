//go:build unix

package broker

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// dupFD hands adoptListener its own descriptor, as a supervisor would, so the test's
// *os.File keeps sole ownership of the original.
func dupFD(t *testing.T, f *os.File) int {
	t.Helper()
	fd, err := unix.Dup(int(f.Fd()))
	require.NoError(t, err)
	return fd
}

func TestActivationAdoptsAListeningUnixSocket(t *testing.T) {
	path := strings.TrimPrefix(testAddr(t), "unix://")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	f, err := ln.(*net.UnixListener).File()
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	adopted, err := adoptListener(dupFD(t, f))
	require.NoError(t, err)
	defer func() { _ = adopted.Close() }()
	require.NoError(t, servesAddr(adopted, "unix://"+path))
	assert.Error(t, servesAddr(adopted, "unix://"+filepath.Join(filepath.Dir(path), "elsewhere.sock")),
		"a socket runs do not dial holds capacity nobody asks for")

	go func() {
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
		}
	}()
	conn, err := adopted.Accept()
	require.NoError(t, err)
	_ = conn.Close()
}

func TestActivationRefusesWhatIsNotAListeningUnixStream(t *testing.T) {
	dir := t.TempDir()

	regular, err := os.Create(filepath.Join(dir, "file"))
	require.NoError(t, err)
	defer func() { _ = regular.Close() }()

	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = tcp.Close() }()
	tcpFile, err := tcp.(*net.TCPListener).File()
	require.NoError(t, err)
	defer func() { _ = tcpFile.Close() }()

	gram, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: strings.TrimPrefix(testAddr(t), "unix://"), Net: "unixgram"})
	require.NoError(t, err)
	defer func() { _ = gram.Close() }()
	gramFile, err := gram.File()
	require.NoError(t, err)
	defer func() { _ = gramFile.Close() }()

	streamPath := strings.TrimPrefix(testAddr(t), "unix://")
	stream, err := net.Listen("unix", streamPath)
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()
	client, err := net.Dial("unix", streamPath)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	clientFile, err := client.(*net.UnixConn).File()
	require.NoError(t, err)
	defer func() { _ = clientFile.Close() }()

	_, acceptConnErr := unix.GetsockoptInt(int(clientFile.Fd()), unix.SOL_SOCKET, unix.SO_ACCEPTCONN)
	for name, f := range map[string]*os.File{
		"a regular file":     regular,
		"a tcp listener":     tcpFile,
		"a datagram socket":  gramFile,
		"a connected socket": clientFile,
	} {
		t.Run(name, func(t *testing.T) {
			if f == clientFile && errors.Is(acceptConnErr, unix.ENOPROTOOPT) {
				t.Skip("this platform cannot tell a listening socket from a connected one before Accept")
			}
			ln, err := adoptListener(dupFD(t, f))
			assert.Error(t, err)
			assert.Nil(t, ln)
		})
	}
}

// TestActivationServesTheSupervisorsSocket runs the whole handover the way systemd does
// it: the socket is bound before the broker exists, arrives as descriptor 3 with
// LISTEN_FDS=1, and the broker serves it without binding anything itself.
func TestActivationServesTheSupervisorsSocket(t *testing.T) {
	addr := testAddr(t)
	path := strings.TrimPrefix(addr, "unix://")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	f, err := ln.(*net.UnixListener).File()
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), activatedEnv+"="+addr, envListenFDs+"=1")
	cmd.ExtraFiles = []*os.File{f}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()

	c := dial(t, addr)
	st, err := c.Status(t.Context())
	require.NoError(t, err, stderr.String())
	assert.Equal(t, cmd.Process.Pid, st.PID, "the broker the supervisor started answers on the supervisor's socket")
	require.NoError(t, c.Shutdown(t.Context()))
	select {
	case err := <-exited:
		require.NoError(t, err, stderr.String())
	case <-time.After(5 * time.Second):
		t.Fatal("the activated broker did not stop")
	}
	assert.FileExists(t, path, "the supervisor's socket outlives the broker it started")
}
