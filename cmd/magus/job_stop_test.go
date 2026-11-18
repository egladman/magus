package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/libs/safetyscissors/budget"
)

type JobStopCommandSuite struct{ suite.Suite }

func TestJobStopCommandSuite(t *testing.T) { suite.Run(t, new(JobStopCommandSuite)) }

func (s *JobStopCommandSuite) SetupTest() { privateSockDir(s.T()) }

// serveBroker runs a broker at the default address for the test's length.
func (s *JobStopCommandSuite) serveBroker() {
	ln, err := broker.Listen(s.T().Context(), broker.DefaultAddr())
	s.Require().NoError(err)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- broker.Serve(ctx, ln, broker.WithCapacity(2048, 4)) }()
	s.T().Cleanup(func() { cancel(); <-done })
}

func (s *JobStopCommandSuite) TestRequiresOneJob() {
	s.Require().ErrorContains(jobStop(s.T().Context(), nil), "requires exactly one job")
	s.Require().ErrorContains(jobStop(s.T().Context(), []string{"a", "b"}), "requires exactly one job")
}

func (s *JobStopCommandSuite) TestSaysNoBrokerRecordedAnything() {
	err := jobStop(s.T().Context(), []string{"plan/a"})
	s.Require().ErrorIs(err, broker.ErrUnavailable)
	s.Contains(err.Error(), "no broker is running, so none recorded a process group for job plan/a")
}

func (s *JobStopCommandSuite) TestRefusesAJobWithNoRecordedGroup() {
	s.serveBroker()
	c, err := broker.Dial(s.T().Context(), broker.DefaultAddr())
	s.Require().NoError(err)
	defer func() { _ = c.Close() }()
	_, err = c.Request(s.T().Context(), budget.Claim{Project: ".", Target: "test", Slots: 1})
	s.Require().NoError(err)

	err = jobStop(s.T().Context(), []string{"plan/a"})
	var refusal *broker.Error
	s.Require().ErrorAs(err, &refusal)
	s.Equal(broker.CodeNoGroup, refusal.Code)
}

func (s *JobStopCommandSuite) TestPrintsWhatItSignalledReleasedAndFreed() {
	var out bytes.Buffer
	printJobStop(&out, broker.JobStop{
		Lease:   "plan/a",
		Stopped: []broker.Group{{PGID: 40, Command: "go test ./..."}},
		Gone:    []broker.Group{{PGID: 41, Command: "sleep 30"}},
		Seats:   1,
	})
	s.Equal("job plan/a: signaled process group 40 (`go test ./...`): SIGTERM now, SIGKILL after a grace\n"+
		"job plan/a: released process group 41 (`sleep 30`), which had already ended\n"+
		"job plan/a: freed 1 validation seat(s)\n", out.String())
}
