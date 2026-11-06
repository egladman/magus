package broker

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

// ProtocolSocketSuite pins one broker socket per protocol, and the warning
// that another protocol's broker splits the machine's budget.
type ProtocolSocketSuite struct {
	suite.Suite
	dir string
}

func TestProtocolSocketSuite(t *testing.T) {
	suite.Run(t, new(ProtocolSocketSuite))
}

// SetupTest makes a short directory: macOS caps a unix socket path at
// 104 bytes, and t.TempDir under $TMPDIR is longer.
func (s *ProtocolSocketSuite) SetupTest() {
	dir, err := os.MkdirTemp("/tmp", "bk")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	s.dir = dir
}

func (s *ProtocolSocketSuite) listen(name string) {
	ln, err := net.Listen("unix", filepath.Join(s.dir, name))
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
	ln, err := net.Listen("unix", filepath.Join(s.dir, "broker-4.sock"))
	s.Require().NoError(err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	s.Require().NoError(ln.Close())
	s.Empty(OtherBrokers(s.T().Context(), s.dir))
}

func (s *ProtocolSocketSuite) TestTheWarningSaysTheBudgetIsNotShared() {
	got := OtherBrokersWarning([]string{"/run/magus/broker.sock"})
	s.Contains(got, "/run/magus/broker.sock")
	s.Contains(got, "budget is not shared")
	s.Empty(OtherBrokersWarning(nil))
}
