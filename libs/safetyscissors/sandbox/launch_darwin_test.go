package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// A child Command starts runs under sandbox-exec and reaches only what its binds name.
type sandboxExecSuite struct {
	suite.Suite
	inside, outside string
}

func TestSandboxExecSuite(t *testing.T) { suite.Run(t, new(sandboxExecSuite)) }

func (s *sandboxExecSuite) SetupTest() {
	if _, err := defaultProbe(); err != nil {
		s.T().Skipf("sandbox-exec unavailable: %v", err)
	}
	s.inside = filesystem.ResolveRulePath(s.T().TempDir())
	s.outside = filesystem.ResolveRulePath(s.T().TempDir())
	for _, dir := range []string{s.inside, s.outside} {
		s.Require().NoError(os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600))
	}
}

func (s *sandboxExecSuite) run(p *Policy, name string, args ...string) error {
	cmd, err := Command(s.T().Context(), p, name, args...)
	s.Require().NoError(err)
	s.Equal(sandboxExec, cmd.Path)
	return cmd.Run()
}

func (s *sandboxExecSuite) policy(binds ...Bind) *Policy {
	p, err := New(append([]Bind{{Path: "/bin", Mode: "rx"}}, binds...))
	s.Require().NoError(err)
	return p
}

func (s *sandboxExecSuite) TestAReadInsideTheBindsSucceeds() {
	s.NoError(s.run(s.policy(Bind{Path: s.inside}), "/bin/cat", filepath.Join(s.inside, "f")))
}

func (s *sandboxExecSuite) TestAReadOutsideTheBindsIsDenied() {
	s.Error(s.run(s.policy(Bind{Path: s.inside}), "/bin/cat", filepath.Join(s.outside, "f")))
}

func (s *sandboxExecSuite) TestAWriteNeedsAWriteBind() {
	script := "echo y > " + filepath.Join(s.inside, "g")
	s.Error(s.run(s.policy(Bind{Path: s.inside}), "/bin/sh", "-c", script))
	s.NoError(s.run(s.policy(Bind{Path: s.inside, Mode: "rw"}), "/bin/sh", "-c", script))
}

func (s *sandboxExecSuite) TestAnExecOutsideTheBindsIsDenied() {
	s.Error(s.run(s.policy(), "/usr/bin/true"))
}

// The base profile alone is enough for a process to start: dyld, the shared cache and
// libSystem's initializers reach only what seatbeltBase grants.
func (s *sandboxExecSuite) TestTheBaseProfileStartsAProcess() {
	s.NoError(s.run(s.policy(Bind{Path: "/usr/bin/true", Mode: "rx"}), "/usr/bin/true"))
}

// A link the caller runs execs the file it resolves to, which the rules name.
func (s *sandboxExecSuite) TestASymlinkedExecTargetRuns() {
	link := filepath.Join(filesystem.ResolveRulePath(s.T().TempDir()), "echo")
	s.Require().NoError(os.Symlink("/bin/echo", link))
	s.NoError(s.run(s.policy(), link, "ok"))
}
