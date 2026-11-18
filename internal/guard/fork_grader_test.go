package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// ForkGraderSuite pins that a fork reads back a grade it has already reached, and that the
// verdicts it reads back are the ones judging afresh reaches.
type ForkGraderSuite struct {
	suite.Suite
	root   string
	judged atomic.Int64
}

func TestForkGraderSuite(t *testing.T) { suite.Run(t, new(ForkGraderSuite)) }

func (s *ForkGraderSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.root = s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magusfile.buzz"), []byte("// rules\n"), 0o600))
	s.judged.Store(0)
}

// load names the root's magusfile as the one file the rules came from and denies go-build,
// counting each command it judges.
func (s *ForkGraderSuite) load(context.Context, string) (Dependencies, *WorkspaceRules) {
	files := []string{filepath.Join(s.root, "magusfile.buzz")}
	return Dependencies{
		Policy: func() PolicyState { return PolicyState{Files: files} },
		CommandRule: func(_ context.Context, req types.CommandRequest, _ hint.Gate) (verdict.Verdict, error) {
			s.judged.Add(1)
			if strings.Contains(req.Command, "go-build") {
				return verdict.Verdict{Decision: verdict.Deny, Reason: "no builds"}, nil
			}
			return verdict.Verdict{}, nil
		},
	}, nil
}

func (s *ForkGraderSuite) row(target string, noCharms bool) job.Job {
	check := rawCheck(target, ".")
	if noCharms {
		check = codec.RawMessage(fmt.Sprintf(`{"target":%q,"project":".","no_default_charms":true}`, target))
	}
	return job.Job{ID: "w", Check: check}
}

func (s *ForkGraderSuite) TestAGradeReachedOnceIsReadBack() {
	g := NewForkGrader(nil, s.load)
	row := s.row("go-build", false)

	first := g.Grade(s.T().Context(), s.root, row)
	judged := s.judged.Load()
	again := g.Grade(s.T().Context(), s.root, row)

	s.True(first.Denied)
	s.Equal(first, again)
	s.Equal(judged, s.judged.Load(), "the second grade is read back, not judged")
	s.Equal(1, g.verdicts.Judged())
}

func (s *ForkGraderSuite) TestTheCharmlessFormIsGradedOnItsOwn() {
	g := NewForkGrader(nil, s.load)

	g.Grade(s.T().Context(), s.root, s.row("go-test", false))
	g.Grade(s.T().Context(), s.root, s.row("go-test", true))

	s.Equal(2, g.verdicts.Judged())
}

func (s *ForkGraderSuite) TestRulesThatNameNoFilesAreJudgedEveryTime() {
	load := func(context.Context, string) (Dependencies, *WorkspaceRules) {
		return Dependencies{CommandRule: func(context.Context, types.CommandRequest, hint.Gate) (verdict.Verdict, error) {
			return verdict.Verdict{}, nil
		}}, nil
	}
	g := NewForkGrader(nil, load)
	row := s.row("go-test", false)

	g.Grade(s.T().Context(), s.root, row)
	g.Grade(s.T().Context(), s.root, row)

	s.Equal(2, g.verdicts.Judged())
}

// A deferral is not kept: rules that did not load may load at the next grade, so it judges
// again even though the rules' files, and so the key, are unchanged.
func (s *ForkGraderSuite) TestADeferredGradeIsJudgedAgain() {
	files := []string{filepath.Join(s.root, "magusfile.buzz")}
	load := func(context.Context, string) (Dependencies, *WorkspaceRules) {
		return Dependencies{
			LoadFailure: errors.New("magusfile.buzz:1: expected ';'"),
			Policy:      func() PolicyState { return PolicyState{Files: files} },
		}, nil
	}
	g := NewForkGrader(nil, load)
	row := s.row("go-build", false)
	s.Require().NotEmpty(gradeKey(s.root, g.rules.current(s.T().Context(), s.root), row), "the grade has a key, so only the deferral keeps it out of the memo")

	s.NotEmpty(g.Grade(s.T().Context(), s.root, row).Deferred)
	s.NotEmpty(g.Grade(s.T().Context(), s.root, row).Deferred)

	s.Equal(2, g.verdicts.Judged())
}
