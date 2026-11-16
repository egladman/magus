package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// ForkSeatSuite pins that a fork's validation waits for a seat as a graph build does, and
// is refused at budget_wait with the budget's exit status.
type ForkSeatSuite struct {
	suite.Suite
}

func TestForkSeatSuite(t *testing.T) {
	suite.Run(t, new(ForkSeatSuite))
}

// fullBudget seats nothing: every claim waits until its wait ends, and the ask at the
// bound finds the budget still held.
type fullBudget struct {
	claims []types.MachineClaim
}

func (b *fullBudget) Claim(ctx context.Context, c types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error) {
	b.claims = append(b.claims, c)
	parked(b.held())
	<-ctx.Done()
	return types.MachineVerdict{}, ctx.Err()
}

func (b *fullBudget) Request(context.Context, types.MachineClaim) (types.MachineVerdict, error) {
	return b.held(), nil
}

// held is the verdict on a claim another invocation's graph build keeps out.
func (*fullBudget) held() types.MachineVerdict {
	return types.MachineVerdict{Fits: true, BudgetMB: 4096, HeldMB: 4096, Holders: []types.MachineClaimant{{Project: ".", Target: "graph-build", MemoryMB: 4096, PID: 1}}}
}

func (*fullBudget) Release(context.Context, string) {}
func (*fullBudget) Overrun(context.Context, string) {}

func (*fullBudget) HoldGroup(context.Context, string, int, string) (string, error) {
	return "", nil
}

func (*fullBudget) ReleaseHold(context.Context, string) error { return nil }

func (s *ForkSeatSuite) SetupTest() {
	s.T().Cleanup(snapshotGlobals())
	testkit.Isolate(s.T())
}

func (s *ForkSeatSuite) TestAFullBudgetRefusesTheForkWithExit75() {
	globalCfg.Broker = types.BrokerBestEffort
	globalCfg.BudgetWait = 10 * time.Millisecond
	budget := &fullBudget{}

	dir := s.T().TempDir()
	_, err := admitForkValidation(s.T().Context(), budget, dir)

	s.Require().ErrorIs(err, types.MachineBudgetExhausted)
	var exit types.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(cache.ExitCodeMachineBusy, exit.Code)
	s.Equal(75, exit.Code)
	s.Require().Len(budget.claims, 1)
	got := budget.claims[0]
	got.PID, got.Invocation, got.Ancestors, got.WaitBoundMS = 0, "", nil, 0
	s.Equal(types.MachineClaim{Project: ".", Target: "job-fork", MemoryMB: guard.ForkValidationMemoryMB, Slots: 1, Dir: resolveRootOrEmpty(dir)}, got)
}

func (s *ForkSeatSuite) TestBrokerOffClaimsNothing() {
	globalCfg.Broker = types.BrokerOff
	budget := &fullBudget{}

	release, err := admitForkValidation(s.T().Context(), budget, s.T().TempDir())

	s.Require().NoError(err)
	release()
	s.Empty(budget.claims)
}
