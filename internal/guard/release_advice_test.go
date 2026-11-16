package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// fileReleaseRequest puts a release request on holder's row.
func fileReleaseRequest(t *testing.T, ctx context.Context, holder string) string {
	t.Helper()
	id := "att-" + holder
	filed, err := job.NewStore(hookLocation(ctx, Dependencies{}).jobs()).FileReleaseRequest(ctx, holder, types.JobReleaseRequest{
		ID:      id,
		File:    "internal/ledger/store.go",
		Wanted:  []string{"internal/ledger/store.go"},
		By:      "lease-b",
		Message: fmt.Sprintf("job %[1]s holds internal/ledger/store.go. %[1]s has changed nothing under internal/ledger/**", holder),
	})
	require.NoError(t, err)
	require.True(t, filed)
	return id
}

func TestReleaseRequestedAdvisesTheHolderOnce(t *testing.T) {
	call := func(ctx context.Context, session, lease string) types.GuardJudgment {
		return Judge(ctx, Dependencies{}, Request{Input: bashCall(t, session, "", "ls"), Host: "claude-code", Lease: lease})
	}

	t.Run("an open request for the lease advises once", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		id := fileReleaseRequest(t, ctx, "lease-a")
		v := call(ctx, "holder", "lease-a")
		assert.Contains(t, v.Context, fmt.Sprintf("a fork under lease lease-b was refused for internal/ledger/store.go (request %s)", id))
		assert.Contains(t, v.Context, "lease-a has changed nothing under internal/ledger/**")
		assert.Contains(t, v.Context, "magus job apply -f <record>")
		require.Equal(t, types.GuardJudgment{
			APIVersion: agent.GuardAPIVersion, Decision: decisionAdvise, Context: v.Context,
			Rule: string(advisoryReleaseRequested), Lease: "lease-a", LeaseFrom: types.LeaseSourceFlag,
		}, v, v.Context)
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "holder", "lease-a").Rule, "said once a session")
	})

	t.Run("another lease's request is silent", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		fileReleaseRequest(t, ctx, "lease-b")
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "holder", "lease-a").Rule)
	})

	t.Run("a disposed request is silent", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		id := fileReleaseRequest(t, ctx, "lease-a")
		require.NoError(t, job.NewStore(hookLocation(ctx, Dependencies{}).jobs()).CloseReleaseRequest(ctx, id))
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "holder", "lease-a").Rule)
	})

	t.Run("a session with no lease is silent", func(t *testing.T) {
		ctx, _ := fleetFixture(t, fixture.FleetLeases()...)
		fileReleaseRequest(t, ctx, "lease-a")
		assert.NotEqual(t, string(advisoryReleaseRequested), call(ctx, "nobody", "").Rule)
	})
}

// ReleaseAdviceSuite pins that a leased hook reads a request off the row.
type ReleaseAdviceSuite struct {
	suite.Suite
}

func TestReleaseAdviceSuite(t *testing.T) {
	suite.Run(t, new(ReleaseAdviceSuite))
}

func (s *ReleaseAdviceSuite) TestTheMarkerIsPerLeaseNotPerSession() {
	rows := fixture.FleetLeases()
	rows[1].State, rows[1].Paths.Write, rows[1].Paths.Deny = types.StateRunning, rows[0].Paths.Write, nil
	ctx, _ := fleetFixture(s.T(), rows...)
	fileReleaseRequest(s.T(), ctx, "lease-a")
	fileReleaseRequest(s.T(), ctx, "lease-b")
	call := func(lease string) string {
		return Judge(ctx, Dependencies{}, Request{Input: bashCall(s.T(), "holder", "", "ls"), Host: "claude-code", Lease: lease}).Rule
	}
	s.Equal(string(advisoryReleaseRequested), call("lease-a"))
	s.Equal(string(advisoryReleaseRequested), call("lease-b"), "one session, two leases")
	s.NotEqual(string(advisoryReleaseRequested), call("lease-a"), "said once a lease")
}

// A pipe blocks whoever opens it, so a store fold never answers.
func (s *ReleaseAdviceSuite) TestALeasedCallOpensNoSessionFile() {
	if _, err := exec.LookPath("mkfifo"); err != nil {
		s.T().Skip("no mkfifo here")
	}
	ctx, _ := fleetFixture(s.T(), fixture.FleetLeases()...)
	dir, err := sessions.Dir(hookLocation(ctx, Dependencies{}).workspace)
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	pipe := filepath.Join(dir, "pipe.jsonl")
	s.Require().NoError(exec.Command("mkfifo", pipe).Run())
	s.T().Cleanup(func() {
		if f, err := os.OpenFile(pipe, os.O_RDWR, 0); err == nil {
			_ = f.Close()
		}
	})

	input := bashCall(s.T(), "holder", "", "ls")
	done := make(chan types.GuardJudgment, 1)
	go func() {
		done <- Judge(ctx, Dependencies{}, Request{Input: input, Host: "claude-code", Lease: "lease-a"})
	}()

	select {
	case v := <-done:
		s.Equal("lease-a", v.Lease)
	case <-time.After(15 * time.Second):
		s.Fail("the judgment opened a session file and blocked on it")
	}
}

func (s *ReleaseAdviceSuite) TestAGrantedHoldNamesTheForkAndWhatIsLeft() {
	row := fixture.FleetLeases()[0]
	row.Paths.Write = []string{"internal/ledger/store.go", "internal/ledger/store.go#Open"}
	row.Releases = []types.JobRelease{{Path: "internal/ledger/store.go", Narrowed: true, TakenBy: "lease-b"}}
	s.Equal("magus workspace: lease lease-a now holds part of a file by declaration.\n"+
		"internal/ledger/store.go: fork lease-b was granted declarations you had not touched; you hold it by internal/ledger/store.go#Open\n"+
		"A write to an unclaimed declaration claims it.", adviseLeaseNarrowed(row).Say)
}

func (s *ReleaseAdviceSuite) TestAGrantedHoldWithNoClaimSaysSo() {
	row := fixture.FleetLeases()[0]
	row.Paths.Write = []string{"internal/ledger/store.go"}
	row.Releases = []types.JobRelease{{Path: "internal/ledger/store.go", Narrowed: true, TakenBy: "lease-b"}}
	s.Contains(adviseLeaseNarrowed(row).Say, "you hold it by no declaration yet")
}

func (s *ReleaseAdviceSuite) TestAnEndedRowIsSilent() {
	row := fixture.FleetLeases()[0]
	row.State = types.StateExited
	row.Releases = []types.JobRelease{{Path: "internal/ledger/store.go", Narrowed: true, TakenBy: "lease-b"}}
	s.Empty(adviseLeaseNarrowed(row).Say)
}

func (s *ReleaseAdviceSuite) TestAHoldNarrowedByItsOwnWriteIsSilent() {
	row := fixture.FleetLeases()[0]
	row.Releases = []types.JobRelease{{Path: "magusfile.buzz", Narrowed: true}}
	s.Empty(adviseLeaseNarrowed(row).Say)
}
