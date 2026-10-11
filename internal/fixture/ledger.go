package fixture

import (
	"testing"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// Ledger isolates the environment, creates a workspace root and a cache dir, and seeds
// the job store with leases. The ledger lives in the per-repository state directory and
// the guard resolves it with no seam a test can reach, so the isolation is what keeps a
// test off the developer's own ledger. It returns the workspace root, then the cache dir
// the store lives under.
func Ledger(tb testing.TB, leases ...types.Job) (root, cacheDir string) {
	tb.Helper()
	testkit.Isolate(tb)
	root, cacheDir = tb.TempDir(), tb.TempDir()
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root})
	for _, lease := range leases {
		_, err := store.Update(tb.Context(), lease.ID, func(cur *types.Job) { *cur = lease })
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
func FleetLeases() []types.Job {
	return []types.Job{
		{
			ID:           "lease-a",
			Criteria:     "own the ledger store\nacceptance: List stays cheap",
			WritePaths:   []string{"internal/ledger/**"},
			State:        types.StateRunning,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  types.BaseMatch,
			Registered:   1,
		},
		{
			ID:           "lease-b",
			Criteria:     "grade writes in the guard",
			WritePaths:   []string{"cmd/magus/**", "docs/guard.md"},
			DenyPaths:    []string{"cmd/magus/gen/**"},
			State:        types.StateDeclared,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  types.BaseMatch,
			Registered:   1,
		},
	}
}
