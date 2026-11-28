package replay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type StoppedRunsSuite struct {
	suite.Suite
	dir string
}

func TestStoppedRuns(t *testing.T) { suite.Run(t, new(StoppedRunsSuite)) }

func (s *StoppedRunsSuite) SetupTest() { s.dir = s.T().TempDir() }

// stopped writes a record of ref that began proc.exec and never ended it. Close
// drops the lock as a dead writer's is dropped.
func (s *StoppedRunsSuite) stopped(ref string, h Header) {
	r, err := OpenLog(s.dir, ref, h)
	s.Require().NoError(err)
	s.Require().NoError(r.begin(call(0, -1, "os.time")))
	s.Require().NoError(r.end(0, callReturned, outcome{Result: "1"}))
	s.Require().NoError(r.begin(call(1, -1, "proc.exec")))
	s.Require().NoError(r.Close())
}

func (s *StoppedRunsSuite) TestARunWhoseWriterIsGoneIsListedWithItsOpenCallAndCommands() {
	s.stopped("out1", Header{Script: "hack/dev/land.buzz", Args: []string{"--dry-run", "it's"}})

	runs, err := StoppedRuns(s.dir)
	s.Require().NoError(err)
	open := call(1, -1, "proc.exec")
	s.Equal([]StoppedRun{{
		Ref:     "out1",
		Script:  "hack/dev/land.buzz",
		Args:    []string{"--dry-run", "it's"},
		Open:    &open,
		Resume:  `magus buzz --record hack/dev/land.buzz -- --dry-run 'it'\''s'`,
		Restart: `magus buzz --record --restart hack/dev/land.buzz -- --dry-run 'it'\''s'`,
	}}, runs)
}

func (s *StoppedRunsSuite) TestAFinishedRunIsNotListed() {
	r, err := OpenLog(s.dir, "out1", Header{Script: "run.buzz"})
	s.Require().NoError(err)
	s.Require().NoError(r.Finish())
	s.Require().NoError(r.Close())

	runs, err := StoppedRuns(s.dir)
	s.Require().NoError(err)
	s.Empty(runs)
}

func (s *StoppedRunsSuite) TestARunALiveProcessWritesIsNotListed() {
	r, err := OpenLog(s.dir, "out1", Header{Script: "run.buzz"})
	s.Require().NoError(err)
	defer func() { s.Require().NoError(r.Close()) }()

	runs, err := StoppedRuns(s.dir)
	s.Require().NoError(err)
	s.Empty(runs, "its writer holds the lock")
}

func (s *StoppedRunsSuite) TestARunStoppedBetweenCallsHasNoOpenCall() {
	r, err := OpenLog(s.dir, "out1", Header{Script: "run.buzz"})
	s.Require().NoError(err)
	s.Require().NoError(r.Close())

	runs, err := StoppedRuns(s.dir)
	s.Require().NoError(err)
	s.Equal([]StoppedRun{{
		Ref: "out1", Script: "run.buzz",
		Resume: "magus buzz --record run.buzz", Restart: "magus buzz --record --restart run.buzz",
	}}, runs)
}

func (s *StoppedRunsSuite) TestAnUnreadableRecordIsReportedBesideTheRest() {
	s.stopped("out1", Header{Script: "run.buzz"})
	bad := filepath.Join(s.dir, "out2", recordFile)
	s.Require().NoError(os.MkdirAll(filepath.Dir(bad), 0o755))
	s.Require().NoError(os.WriteFile(bad, []byte("not json\n"), 0o644))

	runs, err := StoppedRuns(s.dir)
	s.Require().Error(err)
	s.Contains(err.Error(), "out2")
	s.Require().Len(runs, 1)
	s.Equal("out1", runs[0].Ref)
}

func (s *StoppedRunsSuite) TestAMissingDirHoldsNoRuns() {
	runs, err := StoppedRuns(filepath.Join(s.dir, "absent"))
	s.Require().NoError(err)
	s.Empty(runs)
}
