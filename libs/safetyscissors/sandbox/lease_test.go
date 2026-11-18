package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// A lease's row narrows a policy's writes on the checkout to the row's write paths.
type LeaseBoundarySuite struct {
	suite.Suite
	root, cache, outside string // resolved
	base                 *Policy
}

func TestLeaseBoundarySuite(t *testing.T) { suite.Run(t, new(LeaseBoundarySuite)) }

// SetupTest lays out a checkout with pkg/a/gen, pkg/a/keep and pkg/b, granted rwx
// whole beside a read of a directory outside it and a write of /dev/null.
func (s *LeaseBoundarySuite) SetupTest() {
	t := s.T()
	s.root = filesystem.ResolveRulePath(t.TempDir())
	s.cache = filesystem.ResolveRulePath(t.TempDir())
	s.outside = filesystem.ResolveRulePath(t.TempDir())
	for _, dir := range []string{"pkg/a/gen", "pkg/a/keep", "pkg/b"} {
		s.Require().NoError(os.MkdirAll(filepath.Join(s.root, filepath.FromSlash(dir)), 0o755))
	}
	base, err := New([]Bind{{Path: s.root, Mode: "rwx"}, {Path: s.outside}, {Path: "/dev/null", Mode: "rw"}})
	s.Require().NoError(err)
	s.base = base
}

func (s *LeaseBoundarySuite) worker(write, deny []string) job.Job {
	return job.Job{ID: "fleet/w1", Parent: "fleet/root", State: job.StateRunning, Paths: job.Paths{Write: write, Deny: deny}}
}

func (s *LeaseBoundarySuite) narrowed(row job.Job) *Policy {
	b, ok := LeaseBoundary(row, job.LeaseSourceMarker, s.root, s.cache)
	s.Require().True(ok, "row %s narrows", row.ID)
	return b.Apply(s.base)
}

func (s *LeaseBoundarySuite) writes(p *Policy, rel string) error {
	return p.Check(filesystem.Write, filepath.Join(s.root, filepath.FromSlash(rel)))
}

func (s *LeaseBoundarySuite) TestOnlyTheWritePathsAreWritable() {
	p := s.narrowed(s.worker([]string{"pkg/a/**"}, nil))

	id, from := p.Lease()
	s.Equal("fleet/w1", id)
	s.Equal(job.LeaseSourceMarker, from)
	s.Require().NoError(s.writes(p, "pkg/a/x.txt"))
	s.Error(s.writes(p, "pkg/b/x.txt"))
	s.Error(s.writes(p, "x.txt"))
	s.Require().NoError(p.Check(filesystem.Read, filepath.Join(s.root, "pkg", "b", "x.txt")), "a worker reads the tree it changes")
	s.Require().NoError(p.Check(filesystem.Write, filepath.Join(s.cache, "out")), "the writable dirs are granted back")
	s.Require().NoError(p.Check(filesystem.Write, "/dev/null"), "grants outside the checkout keep their writes")
	s.NoError(s.writes(s.base, "pkg/b/x.txt"), "the policy narrowed is left as it was")
}

func (s *LeaseBoundarySuite) TestADeniedPathInsideAWritePathCostsItsDirectory() {
	p := s.narrowed(s.worker([]string{"pkg/**"}, []string{"pkg/a/gen"}))

	s.Error(s.writes(p, "pkg/a/gen/x.txt"))
	s.Require().NoError(s.writes(p, "pkg/a/keep/x.txt"))
	s.Require().NoError(s.writes(p, "pkg/b/x.txt"))
	s.Error(s.writes(p, "pkg/a/new.txt"), "pkg/a cannot be granted without the denied pkg/a/gen")
}

func (s *LeaseBoundarySuite) TestAGlobThatMatchesNothingGrantsNothing() {
	p := s.narrowed(s.worker([]string{"pkg/nowhere/**"}, nil))
	s.Error(s.writes(p, "pkg/a/x.txt"))
	s.Error(s.writes(p, "x.txt"))
}

func (s *LeaseBoundarySuite) TestALiteralPathToCreateGrantsItsDirectory() {
	p := s.narrowed(s.worker([]string{"pkg/a/new.go", "pkg/nowhere/**"}, nil))

	s.Require().NoError(s.writes(p, "pkg/a/new.go"))
	s.Require().NoError(s.writes(p, "pkg/a/sibling.go"), "the grant is the directory the file lands in")
	s.Error(s.writes(p, "pkg/b/x.txt"))
	s.Error(s.writes(p, "pkg/nowhere/x.txt"))
}

func (s *LeaseBoundarySuite) TestALiteralPathUnderAMissingDirectoryGrantsNoAncestor() {
	b, ok := LeaseBoundary(s.worker([]string{"pkg/a/missing/deeper/new.go"}, nil), job.LeaseSourceMarker, s.root)
	s.Require().True(ok)
	p := b.Apply(s.base)

	s.Zero(b.Granted())
	s.Error(s.writes(p, "pkg/a/missing/deeper/new.go"))
	s.Error(s.writes(p, "pkg/a/x.txt"), "pkg/a is two missing levels up, not the file's directory")
}

func (s *LeaseBoundarySuite) TestNothingOutsideTheCheckoutIsGranted() {
	s.Require().NoError(os.Symlink(s.outside, filepath.Join(s.root, "link")))
	p := s.narrowed(s.worker([]string{fmt.Sprintf("../%s/x.txt", filepath.Base(s.outside)), "link/x.txt", "nowhere/deeper/new.go", "."}, nil))

	s.Error(p.Check(filesystem.Write, filepath.Join(s.outside, "x.txt")), "a `..` path is not a grant on the sibling")
	s.Error(s.writes(p, "link/x.txt"), "a symlink out of the checkout is not a grant on its target")
	s.Error(s.writes(p, "x.txt"), "no existing ancestor below the root grants nothing, not the root")
	s.Error(s.writes(p, "pkg/b/x.txt"))
}

func (s *LeaseBoundarySuite) TestAReadOnlyRowGrantsNoWritesAtAnyPlace() {
	for _, row := range []job.Job{
		{ID: "fleet/root", State: job.StateRunning, ReadOnly: true},
		{ID: "fleet/r1", Parent: "fleet/root", State: job.StateRunning, ReadOnly: true, Paths: job.Paths{Write: []string{"pkg/a/**"}}},
	} {
		p := s.narrowed(row)
		s.Error(s.writes(p, "pkg/a/x.txt"), row.ID)
		s.Require().NoError(p.Check(filesystem.Read, filepath.Join(s.root, "pkg", "a", "x.txt")), row.ID)
		s.Require().NoError(p.Check(filesystem.Write, filepath.Join(s.cache, "out")), row.ID)
	}
}

func (s *LeaseBoundarySuite) TestARowWithNoNarrowerBoundaryNarrowsNothing() {
	write := job.Paths{Write: []string{"pkg/a/**"}}
	for name, row := range map[string]job.Job{
		"root lease":        {ID: "fleet/root", State: job.StateRunning, Paths: write},
		"empty write paths": {ID: "fleet/w1", Parent: "fleet/root", State: job.StateRunning},
		"terminal row":      {ID: "fleet/w1", Parent: "fleet/root", State: job.StatePass, Paths: write},
		"no state":          {ID: "fleet/w1", Parent: "fleet/root", Paths: write},
	} {
		b, ok := LeaseBoundary(row, job.LeaseSourceMarker, s.root)
		s.False(ok, name)
		s.Equal(Boundary{}, b, name)
	}
}

func (s *LeaseBoundarySuite) TestTheZeroBoundaryAndANilPolicy() {
	s.Nil(Boundary{}.Apply(nil))
	s.Equal(s.base.Rules(), Boundary{}.Apply(s.base).Rules(), "the zero Boundary narrows nothing")
}
