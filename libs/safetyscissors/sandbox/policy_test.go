package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

type PolicySuite struct {
	suite.Suite
	dir string // resolved
}

func TestPolicySuite(t *testing.T) { suite.Run(t, new(PolicySuite)) }

func (s *PolicySuite) SetupTest() {
	s.dir = filesystem.ResolveRulePath(s.T().TempDir())
}

func (s *PolicySuite) TestEachModeCompilesToItsRights() {
	p, err := New([]Bind{
		{Path: filepath.Join(s.dir, "ro")},
		{Path: filepath.Join(s.dir, "rw"), Mode: "rw", Create: true},
		{Path: filepath.Join(s.dir, "rx"), Mode: "rx", Create: true},
		{Path: filepath.Join(s.dir, "rwx"), Mode: "rwx"},
	})
	s.Require().NoError(err)
	s.Equal([]filesystem.Rule{
		{Path: filepath.Join(s.dir, "ro"), Read: true},
		{Path: filepath.Join(s.dir, "rw"), Read: true, Write: true, Create: true},
		{Path: filepath.Join(s.dir, "rx"), Read: true, Exec: true},
		{Path: filepath.Join(s.dir, "rwx"), Read: true, Write: true, Exec: true},
	}, p.Rules(), "Create holds only where the mode writes")
}

func (s *PolicySuite) TestABindIsResolvedThroughItsLinks() {
	target := filepath.Join(s.dir, "target")
	s.Require().NoError(os.Mkdir(target, 0o700))
	link := filepath.Join(s.dir, "link")
	s.Require().NoError(os.Symlink(target, link))

	p, err := New([]Bind{{Path: link, Mode: "rw"}})
	s.Require().NoError(err)
	s.Equal([]filesystem.Rule{{Path: target, Read: true, Write: true}}, p.Rules())
}

func (s *PolicySuite) TestAnUnknownModeAndAnEmptyPathAreRefused() {
	p, err := New([]Bind{{Path: s.dir, Mode: "RW"}, {Mode: "ro"}})
	s.Nil(p)
	s.Require().ErrorContains(err, `unknown mode "RW"`)
	s.ErrorContains(err, "empty path")
}

func (s *PolicySuite) TestCheckRefusesOutsideTheBinds() {
	p, err := New([]Bind{{Path: s.dir}}, WithLease("fleet/w1", job.LeaseSourceMarker))
	s.Require().NoError(err)
	s.Require().NoError(p.Check(filesystem.Read, filepath.Join(s.dir, "f")))

	err = p.Check(filesystem.Write, filepath.Join(s.dir, "f"))
	var denied *DeniedError
	s.Require().ErrorAs(err, &denied)
	s.ErrorIs(err, filesystem.ErrDenied)
	s.Equal(DeniedError{
		Access: filesystem.Write, Path: filepath.Join(s.dir, "f"),
		Lease: "fleet/w1", LeaseFrom: job.LeaseSourceMarker, Err: denied.Err,
	}, *denied)
}

func (s *PolicySuite) TestANilPolicyPermitsNothing() {
	var p *Policy
	var refused *NilPolicyError
	s.Require().ErrorAs(p.Check(filesystem.Write, "/anywhere"), &refused)
	s.Equal("Check", refused.Op)
	s.Nil(p.Rules())
	id, from := p.Lease()
	s.Empty(id)
	s.Empty(from)
}

func (s *PolicySuite) TestModesNameThemselves() {
	s.Equal("required", Required.String())
	s.Equal("best-effort", BestEffort.String())
	s.Equal("mode(7)", Mode(7).String())
}
