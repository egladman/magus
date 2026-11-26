package guard

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

type GradeCheckSuite struct {
	suite.Suite
	row jobtypes.Job
}

func TestGradeCheckSuite(t *testing.T) { suite.Run(t, new(GradeCheckSuite)) }

func (s *GradeCheckSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.row = jobtypes.Job{ID: "w", Check: rawCheck("go-build", ".")}
}

// A workspace whose rules did not load defers the grade, and says so rather than refuse on a
// verdict nobody reached.
func (s *GradeCheckSuite) TestAWorkspaceThatDidNotLoadDefersTheGrade() {
	verdict := gradeCheck(s.T().Context(), Dependencies{LoadFailure: errors.New("magusfile.buzz:3: expected ';'")}, s.T().TempDir(), s.row)

	s.Equal(job.CheckVerdict{Deferred: "the workspace's rules did not load: magusfile.buzz:3: expected ';'"}, verdict)
}

func (s *GradeCheckSuite) TestARealDenyStillRefuses() {
	denies := func(_ context.Context, req types.CommandRequest, _ hint.Gate) (verdict.Verdict, error) {
		if strings.Contains(req.Command, "go-build") {
			return verdict.Verdict{Decision: verdict.Deny, Reason: "no builds"}, nil
		}
		return verdict.Verdict{}, nil
	}

	verdict := gradeCheck(s.T().Context(), Dependencies{CommandRule: denies}, s.T().TempDir(), s.row)

	s.True(verdict.Denied)
	s.Empty(verdict.Deferred)
}

// A re-fork into a new checkout grades from root, as a first fork does. The new directory has
// no ./magus yet.
func (s *GradeCheckSuite) TestAReforkIsGradedFromRootUntilItsCheckoutExists() {
	root := s.T().TempDir()
	var dirs []string
	rule := func(_ context.Context, req types.CommandRequest, _ hint.Gate) (verdict.Verdict, error) {
		if req.Lease != nil && req.Lease.Worktree != nil {
			dirs = append(dirs, req.Lease.Worktree.Root)
		}
		return verdict.Verdict{}, nil
	}
	s.row.Worktree = &jobtypes.Worktree{Root: filepath.Join(s.T().TempDir(), "new-checkout")}
	s.row.CheckoutRoot = filepath.Join(s.T().TempDir(), "old-checkout")

	verdict := gradeCheck(s.T().Context(), Dependencies{CommandRule: rule}, root, s.row)

	s.Equal(job.CheckVerdict{}, verdict)
	s.NotEmpty(dirs)
	for _, d := range dirs {
		s.Equal(root, d)
	}
}
