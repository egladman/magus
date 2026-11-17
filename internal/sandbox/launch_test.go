package sandbox

import (
	"testing"

	"github.com/stretchr/testify/suite"

	scissors "github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// Best-effort takes the kernel layer wherever there is one and runs without it where
// there is not; required refuses below RequiredABI, where truncation is not denied.
type KernelConfinesSuite struct{ suite.Suite }

func TestKernelConfinesSuite(t *testing.T) { suite.Run(t, new(KernelConfinesSuite)) }

func answering(mode types.SandboxMode, abi int, err error) *Policy {
	return &Policy{Mode: mode, Workspace: "/ws", probe: func() (int, error) { return abi, err }}
}

func (s *KernelConfinesSuite) TestANilPolicyConfinesNothing() {
	var off *Policy
	ok, err := off.KernelConfines()
	s.Require().NoError(err)
	s.False(ok)
}

func (s *KernelConfinesSuite) TestWithoutLandlock() {
	ok, err := answering(types.SandboxModeBestEffort, 0, &scissors.UnsupportedError{}).KernelConfines()
	s.Require().NoError(err)
	s.False(ok)

	_, err = answering(types.SandboxModeRequired, 0, &scissors.UnsupportedError{}).KernelConfines()
	s.Require().ErrorIs(err, types.SandboxRequired)
	s.ErrorContains(err, "/ws")
}

func (s *KernelConfinesSuite) TestBelowTheRequiredABI() {
	ok, err := answering(types.SandboxModeBestEffort, 2, nil).KernelConfines()
	s.Require().NoError(err)
	s.True(ok, "best-effort takes an old kernel's partial confinement")

	_, err = answering(types.SandboxModeRequired, 2, nil).KernelConfines()
	s.Require().ErrorIs(err, types.SandboxRequired)
	s.ErrorContains(err, "ABI is 2")
}

func (s *KernelConfinesSuite) TestAtTheRequiredABI() {
	ok, err := answering(types.SandboxModeRequired, scissors.RequiredABI, nil).KernelConfines()
	s.Require().NoError(err)
	s.True(ok)
}

// Command hands the module the policy's rules under its mode, and confines with
// landlock alone.
type CommandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) { suite.Run(t, new(CommandSuite)) }

func (s *CommandSuite) TestANilPolicyIsPlain() {
	cmd, err := Command(s.T().Context(), nil, "true")
	s.Require().NoError(err)
	s.Equal([]string{"true"}, cmd.Args)
	s.Empty(cmd.ExtraFiles)
}

func (s *CommandSuite) TestAHostWithoutLandlockIsRefused() {
	cmd, err := Command(s.T().Context(), answering(types.SandboxModeBestEffort, 0, &scissors.UnsupportedError{}), "true")
	var unsupported *scissors.UnsupportedError
	s.Require().ErrorAs(err, &unsupported, "a macOS host is refused too: magus does not confine with sandbox-exec")
	s.Nil(cmd)
}

func (s *CommandSuite) TestTheModeMapsOntoTheModule() {
	for mode, refuses := range map[types.SandboxMode]bool{
		types.SandboxModeRequired:   true,
		types.SandboxModeBestEffort: false,
		types.SandboxModeOff:        false,
	} {
		confined, err := answering(mode, 0, nil).confinement()
		s.Require().NoError(err)
		_, err = confined.Confines()
		if refuses {
			var unconfined *scissors.UnconfinedError
			s.ErrorAs(err, &unconfined, "%s refuses a host it cannot confine", mode)
		} else {
			s.NoError(err, "%s runs the child unconfined", mode)
		}
	}
}

func (s *CommandSuite) TestEveryRuleMagusBuildsHasABindMode() {
	p := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{
		{Path: "/ro", Read: true},
		{Path: "/rw", Read: true, Write: true, Create: true},
		{Path: "/rx", Read: true, Exec: true},
		{Path: "/rwx", Read: true, Write: true, Exec: true},
	}}, Lease: "fleet/w1"}
	confined, err := p.confinement()
	s.Require().NoError(err)
	s.Equal(p.FS.Rules, confined.Rules())
	id, _ := confined.Lease()
	s.Equal("fleet/w1", id)
}

func (s *CommandSuite) TestAWriteWithoutReadHasNoBindMode() {
	_, err := (&Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: "/w", Write: true}}}}).confinement()
	s.ErrorContains(err, "without read")
}
