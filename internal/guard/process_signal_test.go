package guard

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// standInGroups makes the broker's recorded groups groups, and every process its own
// group's leader, for the test's length.
func standInGroups(t *testing.T, groups []broker.Group, err error) {
	t.Helper()
	wasGroups, wasProcessGroup := recordedGroups, processGroup
	recordedGroups = func(context.Context) ([]broker.Group, error) { return groups, err }
	processGroup = func(pid int) (int, error) { return pid, nil }
	t.Cleanup(func() { recordedGroups, processGroup = wasGroups, wasProcessGroup })
}

// Neither a PID nor a process name proves the caller owns it.
func TestGuardDeniesProcessSignals(t *testing.T) {
	standInGroups(t, nil, broker.ErrUnavailable)
	for _, command := range []string{
		`pkill -f 'magus job fork' ; ./magus server status`,
		`pkill -f './magus mcp' 2>/dev/null`,
		`kill 27873 32452 34431 2>/dev/null`,
		`kill -TERM 30940`,
		`kill -9 -- -1234`,
		`/bin/kill -s TERM 30940`,
		`killall magus`,
		`bash -c 'pkill -f magus'`,
		`env X=1 /usr/bin/pkill -f magus`,
		`pkill -- --help`,
		`killall -- --version`,
		`kill -- -l`,
	} {
		t.Run(command, func(t *testing.T) {
			v := Evaluate(t.Context(), strict(testDependencies()), command)
			require.Equal(t, denyRuleName("process-signal"), v.Rule.Name)
			require.NotEmpty(t, v.Deny)
			assert.Contains(t, v.Deny, "cancel")
			require.Len(t, v.Next, 1)
			assert.Equal(t, hint.ServerStatus.String(), v.Next[0].Run)
			assert.Empty(t, Evaluate(t.Context(), strict(testDependencies()), v.Next[0].Run).Deny)
		})
	}
	deps := advising(testDependencies(), "process-signal")
	assert.Empty(t, Evaluate(t.Context(), deps, "kill 30940").Deny)
	assert.NotEmpty(t, advised(Evaluate(t.Context(), deps, "kill 30940")))
}

func TestGuardAllowsSignalDocumentationAndMentions(t *testing.T) {
	for _, command := range []string{
		`echo "pkill -f magus"`,
		`printf '%s\n' 'kill 30940'`,
		`kill -l`,
		`kill -l TERM`,
		`kill -L`,
		`kill --list`,
		`kill --help`,
		`pkill --help`,
		`killall --version`,
	} {
		assert.NotEqual(t, denyRuleName("process-signal"), Evaluate(t.Context(), strict(testDependencies()), command).Rule.Name, command)
	}
}

func TestGuardDeniesSingleProcessProbe(t *testing.T) {
	for _, command := range []string{
		`kill -0 4242`,
		`kill -s 0 4242`,
		`kill --signal=0 4242`,
		`sleep 1m; kill -0 4242`,
		`kill -0 4242 && ./magus run go-test .`,
	} {
		v := Evaluate(t.Context(), strict(testDependencies()), command)
		assert.Equal(t, denyRuleProcessPoll, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}

// ProcessSignalSuite pins the remedy when a signaled process is in a group the broker
// recorded for a job: `magus job stop <job>`, never a guess.
type ProcessSignalSuite struct{ suite.Suite }

func TestProcessSignalSuite(t *testing.T) { suite.Run(t, new(ProcessSignalSuite)) }

func (s *ProcessSignalSuite) evaluate(command string) ShellVerdict {
	return Evaluate(s.T().Context(), strict(testDependencies()), command)
}

func (s *ProcessSignalSuite) TestARecordedGroupsRemedyIsJobStop() {
	standInGroups(s.T(), []broker.Group{{PGID: 4242, Lease: "plan/a", Alive: true}, {PGID: 5151, Lease: "plan/b"}}, nil)
	for _, command := range []string{`kill 4242`, `kill -TERM 4242`, `kill -9 -- -4242`, `kill -s KILL -4242`, `/bin/kill 7 4242`} {
		v := s.evaluate(command)
		s.Equal(denyRuleProcessSignal, v.Rule.Name, command)
		s.Contains(v.Deny, "recorded for job plan/a", command)
		s.Require().Len(v.Next, 1, command)
		s.Equal(hint.JobStop.With("plan/a"), v.Next[0].Run, command)
	}
}

func (s *ProcessSignalSuite) TestEveryJobASignalReachesIsNamed() {
	standInGroups(s.T(), []broker.Group{{PGID: 4242, Lease: "plan/b"}, {PGID: 5151, Lease: "plan/a"}}, nil)
	v := s.evaluate(`kill 4242 5151`)
	s.Require().Len(v.Next, 2)
	s.Equal(hint.JobStop.With("plan/a"), v.Next[0].Run)
	s.Equal(hint.JobStop.With("plan/b"), v.Next[1].Run)
}

func (s *ProcessSignalSuite) TestAProcessInNoRecordedGroupKeepsTheHostRemedy() {
	standInGroups(s.T(), []broker.Group{{PGID: 4242, Lease: "plan/a"}, {PGID: 6000}}, nil)
	for _, command := range []string{`kill 9999`, `kill 6000`, `kill -4242`, `pkill -f magus`} {
		v := s.evaluate(command)
		s.Equal(denyRuleProcessSignal, v.Rule.Name, command)
		s.Require().Len(v.Next, 1, command)
		s.Equal(hint.ServerStatus.String(), v.Next[0].Run, command)
	}
}

func (s *ProcessSignalSuite) TestAnUnreadableBrokerIsSaid() {
	standInGroups(s.T(), nil, errors.New("connection reset"))
	v := s.evaluate(`kill 4242`)
	s.Equal(denyRuleProcessSignal, v.Rule.Name)
	s.Contains(v.Why, "could not read the process groups its broker recorded")
	s.Contains(v.Why, "connection reset")
	s.Equal(hint.ServerStatus.String(), v.Next[0].Run)
}

func (s *ProcessSignalSuite) TestSignalTargetsReadsKillsOperands() {
	pids, groups := signalTargets([]hint.Invocation{
		{Name: "kill", Args: []string{"-9", "-12", "34"}},
		{Name: "kill", Args: []string{"--", "-56"}},
		{Name: "kill", Args: []string{"-s", "TERM", "78"}},
		{Name: "pkill", Args: []string{"90"}},
	})
	s.Equal([]int{34, 78}, pids)
	s.Equal([]int{12, 56}, groups)
}
