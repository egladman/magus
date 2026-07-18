// Package jobs is the registry of the daemon's background maintenance jobs: the single source
// of truth that maps a job's stable name to the worker argv the daemon runs for it. It is the
// leaf shared by the producers that must agree on that mapping - the `server job <name>` CLI
// submitter, the magus.job.v1 JobService RPC handlers, the daemon's job dispatch (which admits
// exactly these worker argvs and rejects anything else submitted as a job), and the maintenance
// scheduler.
//
// A job is submitted through the same fire-and-forget proc mechanism as any adopted work
// (proc.SubmitJob), so it inherits that layer's coalescing (an identical in-flight job is
// deduplicated) and its Dashboard visibility. This package holds no execution logic; it only
// names the jobs and the commands they run, so a new job is one registry entry plus its worker
// command, with no second place to update.
package jobs

import (
	"slices"
	"strings"
)

// Job names. These are the stable identity of each job: the `server job <name>` leaf, the map key
// callers switch on, and (for the two rotate jobs) the leaf of their `server <name>` worker. They
// are exported constants rather than bare literals so a rename is a compile error at every use
// site instead of a silent miss (an unhandled switch case or a Lookup that returns !ok).
const (
	NameSyncGraph        = "sync-graph"
	NameRotateActivities = "rotate-activities"
	NameRotateLogs       = "rotate-logs"
	NameRotateMemory     = "rotate-memory"
	NameClearCache       = "clear-cache"
)

// Job is one named background job: a stable Name (the CLI leaf and the wire identity) and the
// Argv the daemon dispatches for it. Worker argvs reuse existing magus commands where one fits
// (sync-graph runs `graph build`, clear-cache runs `clean --cache`); a job with no existing
// command has a dedicated worker leaf (rotate-activities runs `server rotate-activities`).
type Job struct {
	Name string   // stable identifier: the `server job <name>` leaf and the RPC's job identity
	Desc string   // one-line human description, for `server job` listing and usage
	Argv []string // the worker command the daemon runs; the head token must be a real subcommand
}

// registry is the authoritative job set. Order is the display order for `server job` with no
// argument.
var registry = []Job{
	{
		Name: NameSyncGraph,
		Desc: "reconcile the knowledge graph to current source (rebuild and reindex)",
		Argv: []string{"graph", "build"},
	},
	{
		Name: NameRotateActivities,
		Desc: "trim the activity trail back to its cap and drop orphaned payload blobs",
		Argv: []string{"server", NameRotateActivities},
	},
	{
		Name: NameRotateLogs,
		Desc: "trim the invocation run-log journals back to their cap",
		Argv: []string{"server", NameRotateLogs},
	},
	{
		Name: NameRotateMemory,
		Desc: "compact the memory progress journal, archiving older entries beside it (decisions are never pruned)",
		Argv: []string{"server", NameRotateMemory},
	},
	{
		Name: NameClearCache,
		Desc: "invalidate cached build entries for the workspace",
		Argv: []string{"clean", "--cache"},
	},
}

// All returns the registered jobs in display order. It returns a copy, so a caller can never
// mutate the authoritative set through the returned slice.
func All() []Job { return slices.Clone(registry) }

// Lookup returns the job with the given name, or ok=false when the name is not registered.
func Lookup(name string) (Job, bool) {
	for _, j := range registry {
		if j.Name == name {
			return j, true
		}
	}
	return Job{}, false
}

// IsWorkerArgv reports whether argv exactly matches a registered job's worker command. The
// daemon's job dispatch uses it as an allowlist so a JobRequest can only run a recognized job's
// worker, never an arbitrary command handed to the fire-and-forget RPC.
func IsWorkerArgv(argv []string) bool {
	for _, j := range registry {
		if slices.Equal(j.Argv, argv) {
			return true
		}
	}
	return false
}

// ActionString is the canonical trail "action" for a job argv: the space-joined command. It is
// the ONE place that format is defined, so the producer that records a job run (the daemon's
// OnJobDone callback) and the consumers that look a run up by action (the scheduler's due-check
// and the JobService metadata) cannot drift apart on how a run is keyed.
func ActionString(argv []string) string { return strings.Join(argv, " ") }
