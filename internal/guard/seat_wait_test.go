package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Every way MGS3009's hint and the budget denies teach a wait passes a worker: the wait piped
// into the refused call, a run or a `graph build`, or run before it with `&&`. A wait that
// gates no magus call still waits for nothing.
func TestSleepPollPassesEveryWaitTheBudgetHintTeaches(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	for _, line := range []string{
		"./magus status --wait | ./magus run diagrams_generate docs",
		"./magus status --wait | ./magus graph build --silent",
		"./magus status --wait && ./magus graph build --silent",
		"./magus status --wait && ./magus run diagrams_generate docs",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.NotEqual(t, string(denyRuleSleepPoll), v.Rule, "%s: %s", line, v.Reason)
	}
	for _, line := range []string{
		"./magus status --wait; ./magus graph build --silent",
		"./magus status --wait | ./magus status",
		"./magus status --wait && echo ready",
		"./magus status --watch=15s && ./magus graph build --silent",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(v), line)
	}
}

// A retry the wait gates with `&&` is not the poll denyRefusedRetry refuses; `;` still is.
func TestRefusedRetryPassesARunTheWaitGates(t *testing.T) {
	cacheDir := t.TempDir()
	runsDir := filepath.Join(cacheDir, "runs")
	require.NoError(t, os.MkdirAll(runsDir, 0o755))
	writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-10*time.Second), true, "run", "lint", ".")
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: cacheDir})

	assert.False(t, denyRefusedRetry(ctx, Dependencies{}, "", "./magus status --wait && ./magus run lint .").refused())
	assert.True(t, denyRefusedRetry(ctx, Dependencies{}, "", "./magus status --wait; ./magus run lint .").refused())
}

// The waits the budget denies and MGS3009 teach, and the bounded background run check-timeout
// serves, pass the guard for every role they are served to.
func TestEveryServedWaitPassesTheGuardForEveryRole(t *testing.T) {
	ctx, roles := servedRoles(t)
	deps := strict(loadingWorkspace(t))
	deps.Spells = testDependencies().Spells
	wait := hint.Status.With("--wait")
	for _, role := range roles {
		for id, run := range map[string]string{
			"wait piped into a run":    wait + " | magus run go::go-test . -- ./internal/ledger/",
			"wait piped into a build":  wait + " | magus graph build --silent",
			"wait before a build":      wait + " && magus graph build --silent",
			"bounded background check": "./magus job exec --timeout 18m0s -- ./magus run go::go-test . -- -run TestLedger",
		} {
			t.Run(role.name+"/"+id, func(t *testing.T) {
				call := servedAs(Request{}, role)
				call.CallBackground = id == "bounded background check"
				gradeServed(t, ctx, deps, call, role.name, id, run)
			})
		}
	}
}

// A wait on the pid of a run holding a seat spends the budget wait outside the call that
// runs; a pid no run holds is some other process.
func TestSleepPollRefusesAProcessWaitOnARunHoldingASeat(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	standBudget(t, types.MachineSnapshot{Holders: []types.MachineClaimant{{PID: 4242, Target: "go-test", Project: "."}}})
	for _, line := range []string{
		"caffeinate -w 4242",
		"caffeinate -i -w 4242",
		"wait 4242",
		"lsof -p 4242",
		"kill -0 4242 && ./magus run go-test .",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(v), line)
	}
	for _, line := range []string{
		"caffeinate -w 9999",
		"wait 9999",
		"lsof -p 9999",
		"caffeinate -i ./magus run go-test .",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.NotEqual(t, string(denyRuleSleepPoll), v.Rule, "%s: %s", line, v.Reason)
	}
}

// The unbound root is refused a run, so its retry remedy forks a job rather than piping
// into the run.
func TestRefusedRetryServesTheRootAForkedJob(t *testing.T) {
	cacheDir := t.TempDir()
	runsDir := filepath.Join(cacheDir, "runs")
	require.NoError(t, os.MkdirAll(runsDir, 0o755))
	writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-10*time.Second), true, "run", "lint", ".")
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: cacheDir})

	root := denyRefusedRetry(ctx, Dependencies{}, "", "./magus run lint .")
	require.True(t, root.refused())
	assert.Contains(t, root.Say, hint.JobFork.String())

	leased := denyRefusedRetry(ctx, Dependencies{lease: "figures"}, "", "./magus run lint .")
	require.True(t, leased.refused())
	assert.Contains(t, leased.Say, "status --wait | ")
	assert.NotContains(t, leased.Say, hint.JobFork.String())
}
