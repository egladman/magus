package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookup_KnownJobReturnsWholeEntry(t *testing.T) {
	got, ok := Lookup("rotate-activities")
	require.True(t, ok)
	require.Equal(t, Job{
		Name: "rotate-activities",
		Desc: "trim the activity trail back to its cap and drop orphaned payload blobs",
		Argv: []string{"server", "rotate-activities"},
	}, got)
}

func TestLookup_UnknownJobIsZeroValue(t *testing.T) {
	got, ok := Lookup("no-such-job")
	require.False(t, ok)
	require.Equal(t, Job{}, got)
}

func TestAll_IsTheRegistryInOrder(t *testing.T) {
	require.Equal(t, []Job{
		{Name: "sync-graph", Desc: "reconcile the knowledge graph to current source (rebuild and reindex)", Argv: []string{"graph", "build"}},
		{Name: "rotate-activities", Desc: "trim the activity trail back to its cap and drop orphaned payload blobs", Argv: []string{"server", "rotate-activities"}},
		{Name: "rotate-logs", Desc: "trim the invocation run-log journals back to their cap", Argv: []string{"server", "rotate-logs"}},
		{Name: "clear-cache", Desc: "invalidate cached build entries for the workspace", Argv: []string{"clean", "--cache"}},
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
