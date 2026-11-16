package guard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

type GradeCheckSuite struct {
	suite.Suite
	row types.Job
}

func TestGradeCheckSuite(t *testing.T) { suite.Run(t, new(GradeCheckSuite)) }

func (s *GradeCheckSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.row = types.Job{ID: "w", Check: &types.LeaseCheck{Target: "go-build", Project: "."}}
}

// A workspace whose rules did not load defers the grade, and says so rather than refuse on a
// verdict nobody reached.
func (s *GradeCheckSuite) TestAWorkspaceThatDidNotLoadDefersTheGrade() {
	verdict := gradeCheck(s.T().Context(), Dependencies{LoadFailure: errors.New("magusfile.buzz:3: expected ';'")}, s.T().TempDir(), s.row)

	s.Equal(job.CheckVerdict{Deferred: "the workspace's rules did not load: magusfile.buzz:3: expected ';'"}, verdict)
}

func (s *GradeCheckSuite) TestARealDenyStillRefuses() {
	denies := func(_ context.Context, req types.CommandRequest, _ hint.Gate) (types.GuardVerdict, error) {
		if strings.Contains(req.Command, "go-build") {
			return types.GuardVerdict{Decision: types.GuardDeny, Reason: "no builds"}, nil
		}
		return types.GuardVerdict{}, nil
	}

	verdict := gradeCheck(s.T().Context(), Dependencies{CommandRule: denies}, s.T().TempDir(), s.row)

	s.True(verdict.Denied)
	s.Empty(verdict.Deferred)
}
