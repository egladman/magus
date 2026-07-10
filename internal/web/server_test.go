package web

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerEphemeralPort confirms the zero-value Config.Port binds a real ephemeral port on
// loopback.
func TestServerEphemeralPort(t *testing.T) {
	bs, err := StartBlob(Config{Origin: "https://example.test"}, "/b", "text/plain", []byte("x"))
	require.NoError(t, err)
	defer bs.WaitServed(canceledCtx())

	host, portStr, err := net.SplitHostPort(bs.Addr())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	port, _ := strconv.Atoi(portStr)
	assert.Greater(t, port, 0, "port 0 config binds a real ephemeral port")
}

// TestServerPinnedPort confirms a non-zero Config.Port is honored (binds exactly that port).
func TestServerPinnedPort(t *testing.T) {
	// Discover a currently-free port, then ask the server to bind it.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	want := probe.Addr().(*net.TCPAddr).Port
	require.NoError(t, probe.Close())

	bs, err := StartBlob(Config{Origin: "https://example.test", Port: want}, "/b", "text/plain", []byte("x"))
	if err != nil {
		t.Skipf("pinned port %d was taken between probe and bind: %v", want, err)
	}
	defer bs.WaitServed(canceledCtx())

	_, portStr, err := net.SplitHostPort(bs.Addr())
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(want), portStr, "Config.Port is bound exactly")
}
