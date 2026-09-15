package job

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookup_KnownJobReturnsWholeEntry(t *testing.T) {
	got, ok := Lookup("rotate-activities")
	require.True(t, ok)
	require.Equal(t, CatalogEntry{
		Name: "rotate-activities",
		Desc: "trim the activity trail back to its cap and drop orphaned payload blobs",
		Argv: []string{"server", "rotate-activities"},
	}, got)
}

func TestLookup_UnknownJobIsZeroValue(t *testing.T) {
	got, ok := Lookup("no-such-job")
	require.False(t, ok)
	require.Equal(t, CatalogEntry{}, got)
}

func TestAll_IsTheRegistryInOrder(t *testing.T) {
	require.Equal(t, []CatalogEntry{
		{Name: "sync-graph", Desc: "reconcile the knowledge graph to current source (rebuild and reindex)", Argv: []string{"graph", "build"}},
		{Name: "rotate-activities", Desc: "trim the activity trail back to its cap and drop orphaned payload blobs", Argv: []string{"server", "rotate-activities"}},
		{Name: "rotate-logs", Desc: "trim the invocation run-log journals back to their cap", Argv: []string{"server", "rotate-logs"}},
		{Name: "prune-preserved", Desc: "drop the working-copy captures vcs checkpoint --preserve minted past their retention", Argv: []string{"server", "prune-preserved"}},
		{Name: "clear-cache", Desc: "invalidate cached build entries for the workspace", Argv: []string{"clean", "--cache"}},
		{Name: "check-review", Desc: "note when a review this tree took part in has merged", Argv: []string{"server", "check-review"}},
		{Name: "check-drift", Desc: "notice, without blocking, when the last commit left generated output stale", Argv: []string{"server", "check-drift"}},
	}, All())
}

func TestIsWorkerArgv(t *testing.T) {
	// Every registered job's worker argv is admitted; a near-miss and an arbitrary command are not.
	for _, j := range All() {
		require.True(t, IsWorkerArgv(j.Argv), "worker argv %v must be admitted", j.Argv)
	}
	require.False(t, IsWorkerArgv([]string{"graph"}))                 // prefix of a worker, not the whole argv
	require.False(t, IsWorkerArgv([]string{"graph", "build", "--x"})) // superset of a worker argv
	require.False(t, IsWorkerArgv([]string{"clean"}))                 // clean without --cache is not a job
	require.False(t, IsWorkerArgv([]string{"run", "rm", "-rf", "/"})) // arbitrary command rejected
	require.False(t, IsWorkerArgv(nil))
}
