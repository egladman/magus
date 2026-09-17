package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmpLedger isolates the real user state directory behind XDG_STATE_HOME, the knob
// internal/ledger's tests and this package's other tests use, and clears BAGGAGE so a
// test run under a leased worker grades the row it planted rather than that worker's.
func tmpLedger(t *testing.T) (cacheDir, root string, store *job.Store) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(trail.EnvBaggage, "")
	cacheDir, root = t.TempDir(), t.TempDir()
	// Pinned unbound: these rows are the orchestrator's, and resolving the actor from
	// the environment would make every write depend on whether this run is leased.
	actor := job.Actor{}
	return cacheDir, root, job.NewStore(job.Location{CacheDir: cacheDir, Root: root, Actor: &actor})
}

// seed writes a row whole, the way a fixture means it: every field this test declared and
// nothing carried over from a previous one.
func seed(t *testing.T, s *job.Store, row types.Job) types.Job {
	t.Helper()
	stored, err := s.Update(context.Background(), row.ID, func(cur *types.Job) { *cur = row })
	require.NoError(t, err)
	return stored
}

// wireGuardHook plants the one host hook config shape guardHookConfigs looks for, so a
// case can separate "bound and registered" from "bound, registered and judged".
func wireGuardHook(t *testing.T, root string) {
	t.Helper()
	writeCheckpointHarness(t, root, guardedHarnessConfig())
}

func TestBoundLeasePassesWithNoLeaseBound(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorOK,
		Message: "no lease bound; the guard advises only",
	}, got)
}

func TestBoundLeaseFailsWhenTheMarkerAndTheEnvironmentDisagree(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)
	require.NoError(t, job.BindLease(cacheDir, "adj/marker"))
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=adj/from-env")

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorFail,
		Message: `this checkout's marker binds lease "adj/marker" while the environment claims "adj/from-env"`,
		Details: []string{
			"the marker is what `magus job exec` wrote here, so magus grades every write under adj/marker and ignores the claim: a record of where the work is beats an assertion a shell can rewrite",
			"unset BAGGAGE, or take the lease you mean here with `magus job exec adj/from-env`",
		},
	}, got)
}

// TestActingLeasePrefersTheMarkerOverTheEnvironment pins the precedence the check above
// reports on. The marker is written into a checkout by `job exec`; the environment member
// is a claim the worker makes about itself, and letting the claim win meant a worker
// bound to one job could be graded against another's lanes by exporting its id.
func TestActingLeasePrefersTheMarkerOverTheEnvironment(t *testing.T) {
	cacheDir, _, _ := tmpLedger(t)
	require.NoError(t, job.BindLease(cacheDir, "adj/marker"))
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=adj/from-env")

	assert.Equal(t, "adj/marker", job.ActingLease(cacheDir))

	// With no marker the environment is the only answer there is, which stays true: a
	// checkout nobody bound is the unattributed case the guard fails open on.
	assert.Equal(t, "adj/from-env", job.ActingLease(t.TempDir()))
}

func TestBoundLeaseReportsAnUnreadableLedgerAsUnknown(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	require.NoError(t, job.BindLease(cacheDir, "adj/live"))
	path, err := store.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorFail, got.Status)
	require.Equal(t, types.EvidenceUnknown, got.Evidence)
	require.Contains(t, got.Message, "could not read the job store")
}

func TestBoundLeaseFailsOnAnUnknownBoundID(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)
	require.NoError(t, job.BindLease(cacheDir, "adj/no-such-lease"))

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorFail,
		Message: `lease "adj/no-such-lease" is bound here, and no row declares it`,
		Details: []string{
			"the guard grades every write here as an unattributed edit: advisory, never denied",
			"declare the row under this id: " + hint.JobFork.With("adj/no-such-lease", "--criteria", "<criteria>"),
		},
	}, got)
}

func TestBoundLeaseFailsOnATerminalBoundRow(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "adj/done", State: types.StatePass})
	require.NoError(t, job.BindLease(cacheDir, "adj/done"))

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorFail,
		Message: `lease "adj/done" is bound here and not live (pass), so its lease-scoped rules are inert`,
		Details: []string{"every write here grades as an unattributed edit until this checkout binds a live lease"},
	}, got)
}

func TestBoundLeaseFailsOnARowWithNoState(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "adj/stateless"})
	require.NoError(t, job.BindLease(cacheDir, "adj/stateless"))

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorFail,
		Message: `lease "adj/stateless" is bound here and not live (no state), so its lease-scoped rules are inert`,
		Details: []string{"every write here grades as an unattributed edit until this checkout binds a live lease"},
	}, got)
}

func TestBoundLeaseFailsOnALiveRowWithNoRegisteredBase(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "adj/live", State: types.StateRunning})
	require.NoError(t, job.BindLease(cacheDir, "adj/live"))

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorFail,
		Message: `lease "adj/live" is bound here and live, but has no registered base, so the guard denies every write until one is recorded`,
		Details: []string{"record one: " + hint.VCSCheckpoint.With("-o", "name") + ", then exec it on this lease"},
	}, got)
}

func TestBoundLeaseAdvisesWhenNothingInvokesTheGuard(t *testing.T) {
	cacheDir, root := registeredLease(t, "adj/unwired")

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorAdvice,
		Message: `lease "adj/unwired" is bound here, live and registered, but no host hook config in this checkout invokes the guard`,
		Details: []string{"the guard-wiring check names what is missing; the MCP surface is not checked here"},
	}, got)
}

func TestBoundLeasePassesOnALiveRegisteredRowAHostHookJudges(t *testing.T) {
	cacheDir, root := registeredLease(t, "adj/enforcing")
	wireGuardHook(t, root)

	got := checkBoundLease(context.Background(), cacheDir, root)

	require.Equal(t, types.DoctorCheck{
		Name:    "bound-lease",
		Status:  types.DoctorOK,
		Message: `lease "adj/enforcing" is bound here, live, registered, and a host hook is wired to judge it`,
	}, got)
}

// registeredLease seeds a live registered row and binds the checkout to it afterwards,
// which is the state every leg past the ledger reads. Seeding first keeps the writes the
// orchestrator's however the store resolves its actor.
func registeredLease(t *testing.T, id string) (cacheDir, root string) {
	t.Helper()
	cacheDir, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: id, State: types.StateRunning, Checkpoint: "abc123"})
	_, err := store.Exec(context.Background(), id, "abc123")
	require.NoError(t, err)
	require.NoError(t, job.BindLease(cacheDir, id))
	return cacheDir, root
}
