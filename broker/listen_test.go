package broker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc/endpoint"
)

func TestListenLosesToALiveBroker(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr)
	_, err := Listen(t.Context(), addr)
	assert.ErrorIs(t, err, ErrRunning, "bind is the lock: a second broker exits instead of serving")
}

func TestListenReclaimsADeadSocket(t *testing.T) {
	addr := testAddr(t)
	path := addr[len("unix://"):]
	ln, err := endpoint.ListenUnix(path)
	require.NoError(t, err)
	// Leave the file behind the way a killed broker does.
	ln.SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	require.FileExists(t, path)

	ln2, err := Listen(t.Context(), addr)
	require.NoError(t, err)
	_ = ln2.Close()
}
