package fixture

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/stretchr/testify/require"
)

// StateBase is the user state dir this process resolves, for a job.Location: under
// testkit.Isolate, a temp dir.
func StateBase(tb testing.TB) string {
	tb.Helper()
	base, err := xdg.StateDir()
	require.NoError(tb, err)
	return base
}

// SeedLeases isolates the environment, creates a workspace root and a cache dir, and seeds
// the job store with leases. The ledger lives in the per-repository state directory and
// the guard resolves it with no seam a test can reach, so the isolation is what keeps a
// test off the developer's own ledger. A lease with no validated stamp gets one, as a fork
// would give it, so a titled spawn of it is not refused as unvalidated. It returns the
// workspace root, then the cache dir the store lives under.
//
// The root is a git repository, so each fork records the authority ADR 0045 has
// the guard require of a leased call.
func SeedLeases(tb testing.TB, leases ...jobtypes.Job) (root, cacheDir string) {
	tb.Helper()
	testkit.Isolate(tb)
	root, cacheDir = testkit.NewRepository(tb, nil).Root(), tb.TempDir()
	// One declaration time for every lease, so which came first falls to the id.
	at := time.Now()
	store := job.NewStore(job.Location{CacheDir: cacheDir, StateBase: StateBase(tb), Root: root, Clock: func() time.Time { return at }})
	for _, lease := range leases {
		if lease.Validated == 0 {
			lease.Validated = 1
		}
		_, err := store.Update(tb.Context(), lease.ID, func(cur *jobtypes.Job) { *cur = lease })
		require.NoError(tb, err)
	}
	return root, cacheDir
}

// FleetLeases is the two-lease plan most guard cases grade against: two live workers with
// disjoint write paths, one of them declaring a denied subtree inside its own.
//
// Both are registered, because an unregistered lease is denied before any boundary is
// consulted. The order is fixed, and callers index it: [0] is lease-a, running and owning
// internal/ledger/**; [1] is lease-b, declared and owning cmd/magus/** minus its denied
// subtree.
func FleetLeases() []jobtypes.Job {
	return []jobtypes.Job{
		{
			ID:           "lease-a",
			Agent:        jobtypes.Instructed("own the ledger store\nacceptance: List stays cheap"),
			Paths:        jobtypes.Paths{Write: []string{"internal/ledger/**"}},
			State:        jobtypes.StateRunning,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  jobtypes.BaseMatch,
			Registered:   1,
		},
		{
			ID:    "lease-b",
			Agent: jobtypes.Instructed("grade writes in the guard"),
			Paths: jobtypes.Paths{
				Write: []string{"cmd/magus/**", "docs/guard.md"},
				Deny:  []string{"cmd/magus/gen/**"},
			},
			State:        jobtypes.StateDeclared,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  jobtypes.BaseMatch,
			Registered:   1,
		},
	}
}
