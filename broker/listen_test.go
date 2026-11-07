package broker

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

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

// ProtocolSocketSuite pins one broker socket per protocol, and the warning
// that another protocol's broker splits the machine's budget.
type ProtocolSocketSuite struct {
	suite.Suite
	dir string
}

func TestProtocolSocketSuite(t *testing.T) {
	suite.Run(t, new(ProtocolSocketSuite))
}

// SetupTest makes a short directory under $TMPDIR: darwin caps a unix socket
// path at 104 bytes, and t.TempDir passes it by adding the test name.
func (s *ProtocolSocketSuite) SetupTest() {
	dir, err := os.MkdirTemp("", "bk")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	s.dir = dir
}

// listen binds through endpoint: a linux sandbox's $TMPDIR alone passes
// sun_path, which endpoint reaches there.
func (s *ProtocolSocketSuite) listen(name string) {
	ln, err := endpoint.ListenUnix(filepath.Join(s.dir, name))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = ln.Close() })
}

func (s *ProtocolSocketSuite) TestTheSocketNamesTheProtocol() {
	s.Equal(fmt.Sprintf("broker-%d.sock", ProtocolVersion), SocketName)
}

func (s *ProtocolSocketSuite) TestAnotherProtocolsLiveBrokerIsListed() {
	s.listen(SocketName)
	s.listen("broker.sock")
	s.Equal([]string{filepath.Join(s.dir, "broker.sock")}, OtherBrokers(s.T().Context(), s.dir))
}

func (s *ProtocolSocketSuite) TestADeadSocketIsNotListed() {
	ln, err := endpoint.ListenUnix(filepath.Join(s.dir, "broker-4.sock"))
	s.Require().NoError(err)
	ln.SetUnlinkOnClose(false)
	s.Require().NoError(ln.Close())
	s.Empty(OtherBrokers(s.T().Context(), s.dir))
}

func (s *ProtocolSocketSuite) TestTheWarningSaysTheBudgetIsNotShared() {
	got := OtherBrokersWarning([]string{"/run/magus/broker.sock"})
	s.Contains(got, "/run/magus/broker.sock")
	s.Contains(got, "budget is not shared")
	s.Empty(OtherBrokersWarning(nil))
}

// A protocol bump moves the socket with it, so two protocols never share one.
func TestTheSocketNameNamesTheProtocol(t *testing.T) {
	assert.Equal(t, fmt.Sprintf("broker-%d.sock", ProtocolVersion), SocketName)
}
