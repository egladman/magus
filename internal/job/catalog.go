package job

import (
	"slices"
	"strings"
)

// Catalog names. These are the stable identity of each daemon maintenance job: the
// `magus job run <name>` leaf, the map key callers switch on, and (for the rotate jobs)
// the leaf of their `server <name>` worker. They are exported constants rather than bare
// literals so a rename is a compile error at every use site instead of a silent miss.
const (
	NameSyncGraph        = "sync-graph"
	NameRotateActivities = "rotate-activities"
	NameRotateLogs       = "rotate-logs"
	NamePrunePreserved   = "prune-preserved"
	NameClearCache       = "clear-cache"
	NameCheckReview      = "check-review"
	NameCheckDrift       = "check-drift"
	NameRegenerateOwed   = "regenerate-owed"
)

// CatalogEntry is one named background maintenance job: a stable Name (the CLI leaf and
// the wire identity) and the Argv the daemon dispatches for it. Worker argvs reuse
// existing magus commands where one fits (sync-graph runs `graph build`, clear-cache runs
// `clean --cache`); a job with no existing command has a dedicated worker leaf
// (rotate-activities runs `server rotate-activities`).
//
// This is the leaf shared by the producers that must agree on that mapping: the
// `magus job run <name>` CLI submitter, the magus.job.v1alpha1 JobService RPC handlers,
// the daemon's job dispatch (which admits exactly these worker argvs and rejects anything
// else submitted as a job), and the maintenance scheduler. A job is submitted through the
// same fire-and-forget proc mechanism as any adopted work (proc.SubmitJob). This catalog
// holds no execution logic; it only names the jobs and the commands they run.
type CatalogEntry struct {
	Name string   // stable identifier: the `magus job run <name>` leaf and the RPC's job identity
	Desc string   // one-line human description, for listing and usage
	Argv []string // the worker command the daemon runs; the head token must be a real subcommand
}

// catalog is the authoritative maintenance-job set. Order is the display order for
// `magus job` with no argument.
var catalog = []CatalogEntry{
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
		Name: NamePrunePreserved,
		Desc: "drop the working-copy captures vcs checkpoint --preserve minted past their retention",
		Argv: []string{"server", NamePrunePreserved},
	},
	{
		Name: NameClearCache,
		Desc: "invalidate cached build entries for the workspace",
		Argv: []string{"clean", "--cache"},
	},
	{
		Name: NameCheckReview,
		Desc: "note when a review this tree took part in has merged",
		Argv: []string{"server", NameCheckReview},
	},
	{
		Name: NameCheckDrift,
		Desc: "notice, without blocking, when the last commit left generated output stale",
		Argv: []string{"server", NameCheckDrift},
	},
	{
		Name: NameRegenerateOwed,
		Desc: "regenerate and stage the generated files a finished merge or rebase kept one side of",
		Argv: []string{"server", NameRegenerateOwed},
	},
}

// All returns the registered catalog entries in display order. It returns a copy, so a
// caller can never mutate the authoritative set through the returned slice.
func All() []CatalogEntry { return slices.Clone(catalog) }

// Lookup returns the catalog entry with the given name, or ok=false when the name is not
// registered.
func Lookup(name string) (CatalogEntry, bool) {
	for _, j := range catalog {
		if j.Name == name {
			return j, true
		}
	}
	return CatalogEntry{}, false
}

// IsWorkerArgv reports whether argv exactly matches a registered job's worker command. The
// daemon's job dispatch uses it as an allowlist so a JobRequest can only run a recognized
// job's worker, never an arbitrary command handed to the fire-and-forget RPC.
func IsWorkerArgv(argv []string) bool {
	for _, j := range catalog {
		if slices.Equal(j.Argv, argv) {
			return true
		}
	}
	return false
}

// ActionString is the canonical trail "action" for a job argv: the space-joined command.
// It is the ONE place that format is defined, so the producer that records a job run (the
// daemon's OnJobDone callback) and the consumers that look a run up by action (the
// scheduler's due-check and the JobService metadata) cannot drift apart on how a run is keyed.
func ActionString(argv []string) string { return strings.Join(argv, " ") }
