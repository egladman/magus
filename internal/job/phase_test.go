package job

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

type phaseSuite struct{ suite.Suite }

func TestPhase(t *testing.T) { suite.Run(t, new(phaseSuite)) }

func (s *phaseSuite) TestAPhaseWithinItsBoundIsNotAFinding() {
	s.Empty(slowLine("landing sweep", landingSweepBound, landingSweepBound, "x"))
}

func (s *phaseSuite) TestAPhasePastItsBoundNamesTheTimeTheBoundAndTheCause() {
	s.Equal("slow: landing sweep took 6m52s against a 1m0s bound; likely cache misses", slowLine("landing sweep", 412*time.Second, time.Minute, "cache misses"))
}

// ticking is a store clock that moves step on every read.
func ticking(step time.Duration) func() time.Time {
	now := time.Unix(100_000, 0)
	return func() time.Time {
		now = now.Add(step)
		return now
	}
}

func (s *phaseSuite) TestASlowLandingSweepSaysWhichPhaseAndWhy() {
	store, notices := sweepStore(s.T(), 100_000, 0)
	store.clock = ticking(landingSweepBound + time.Second)
	var rows []job.Job
	for i := range 2 {
		rows = append(rows, job.Job{ID: fmt.Sprintf("exited-%d", i), State: job.StateExited, Checkpoint: "c0", CheckoutRoot: "/w", Updated: 90_000})
	}
	plant(s.T(), store, rows...)
	store.landed = func(context.Context, job.Job) string { return "" }

	states(s.T(), store)

	s.Contains(notices.String(), "job: slow: landing sweep took")
	s.Contains(notices.String(), "likely 2 landing probes, each running VCS subprocesses (budget 4 a round)")
}

func (s *phaseSuite) TestAFastLandingSweepSaysNothing() {
	store, notices := sweepStore(s.T(), 100_000, 0)
	plant(s.T(), store, job.Job{ID: "exited", State: job.StateExited, Checkpoint: "c0", CheckoutRoot: "/w", Updated: 90_000})
	store.landed = func(context.Context, job.Job) string { return "" }

	states(s.T(), store)

	s.NotContains(notices.String(), "slow:")
}

func (s *phaseSuite) TestAForkPhasePastItsBoundIsNamedOnTheStoresNotices() {
	store, notices := sweepStore(s.T(), 100_000, 0)
	store.clock = ticking(forkFootprintBound + time.Second)

	stop := store.phase("fork footprint grading", forkFootprintBound, func() string { return "reading 3 diffs" })
	stop()

	s.Equal("job: slow: fork footprint grading took 11s against a 10s bound; likely reading 3 diffs\n", notices.String())
}
