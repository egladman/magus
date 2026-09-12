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
	dir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"hooks":{"PreToolUse":[{"command":"magus session hook"}]}}`), 0o644))
}

func TestLeaseBindingPassesWithNoLeaseBound(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
		Status:  types.DoctorOK,
		Message: "no lease bound; the guard advises only",
	}, got)
}

func TestLeaseBindingFailsWhenTheMarkerAndTheEnvironmentDisagree(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)
	require.NoError(t, job.BindLease(cacheDir, "adj/marker"))
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=adj/from-env")

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
		Status:  types.DoctorFail,
		Message: `this session acts as lease "adj/from-env" while this checkout's marker binds "adj/marker"`,
		Details: []string{
			"a host runs its hooks with its own environment, so the guard reads the marker while this session reads BAGGAGE: the two grade different rows",
			"unset BAGGAGE, or bind this checkout to the lease the session acts as",
		},
	}, got)
}

func TestLeaseBindingReportsAnUnreadableLedgerAsUnknown(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	require.NoError(t, job.BindLease(cacheDir, "adj/live"))
	path, err := store.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorFail, got.Status)
	require.Equal(t, types.EvidenceUnknown, got.Evidence)
	require.Contains(t, got.Message, "could not read the job store")
}

func TestLeaseBindingFailsOnAnUnknownBoundID(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)
	require.NoError(t, job.BindLease(cacheDir, "adj/no-such-lease"))

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
		Status:  types.DoctorFail,
		Message: `lease "adj/no-such-lease" is bound here, and no row declares it`,
		Details: []string{
			"the guard grades every write here as an unattributed edit: advisory, never denied",
			"declare the row under this id: " + hint.JobFork.With("adj/no-such-lease", "--goal", "<goal>"),
		},
	}, got)
}

func TestLeaseBindingFailsOnATerminalBoundRow(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "adj/done", State: types.StatePass})
	require.NoError(t, job.BindLease(cacheDir, "adj/done"))

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
		Status:  types.DoctorFail,
		Message: `lease "adj/done" is bound here and not live (pass), so its lease-scoped rules are inert`,
		Details: []string{"every write here grades as an unattributed edit until this checkout binds a live lease"},
	}, got)
}

func TestLeaseBindingFailsOnARowWithNoState(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "adj/stateless"})
	require.NoError(t, job.BindLease(cacheDir, "adj/stateless"))

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
		Status:  types.DoctorFail,
		Message: `lease "adj/stateless" is bound here and not live (no state), so its lease-scoped rules are inert`,
		Details: []string{"every write here grades as an unattributed edit until this checkout binds a live lease"},
	}, got)
}

func TestLeaseBindingFailsOnALiveRowWithNoRegisteredBase(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	seed(t, store, types.Job{ID: "adj/live", State: types.StateRunning})
	require.NoError(t, job.BindLease(cacheDir, "adj/live"))

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
		Status:  types.DoctorFail,
		Message: `lease "adj/live" is bound here and live, but has no registered base, so the guard denies every write until one is recorded`,
		Details: []string{"record one: " + hint.VCSCheckpoint.With("-o", "name") + ", then exec it on this lease"},
	}, got)
}

func TestLeaseBindingAdvisesWhenNothingInvokesTheGuard(t *testing.T) {
	cacheDir, root := registeredLease(t, "adj/unwired")

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
		Status:  types.DoctorAdvice,
		Message: `lease "adj/unwired" is bound here, live and registered, but no host hook config in this checkout invokes the guard`,
		Details: []string{"the guard-wiring check names what is missing; the MCP surface is not checked here"},
	}, got)
}

func TestLeaseBindingPassesOnALiveRegisteredRowAHostHookJudges(t *testing.T) {
	cacheDir, root := registeredLease(t, "adj/enforcing")
	wireGuardHook(t, root)

	got := checkLeaseBinding(cacheDir, root, "")

	require.Equal(t, types.DoctorCheck{
		Name:    "lease-binding",
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
