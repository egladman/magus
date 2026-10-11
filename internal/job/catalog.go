package job

import (
	"slices"
	"strings"
)

// Catalog names. These are the stable identity of each server maintenance job: the
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
)

// CatalogEntry is one named background maintenance job: a stable Name (the CLI leaf and
// the wire identity) and the Argv the server dispatches for it. Worker argvs reuse
// existing magus commands where one fits (sync-graph runs `graph build`, clear-cache runs
// `clean --cache`); a job with no existing command has a dedicated worker leaf
// (rotate-activities runs `server rotate-activities`).
//
// This is the leaf shared by the producers that must agree on that mapping: the
// `magus job run <name>` CLI submitter, the magus.job.v1alpha1 JobService RPC handlers,
// the server's job dispatch (which admits exactly these worker argvs and rejects anything
// else submitted as a job), and the maintenance scheduler. A job is submitted through the
// same fire-and-forget proc mechanism as any adopted work (proc.SubmitJob). This catalog
// holds no execution logic; it only names the jobs and the commands they run.
type CatalogEntry struct {
	Name string   // stable identifier: the `magus job run <name>` leaf and the RPC's job identity
	Desc string   // one-line human description, for listing and usage
	Argv []string // the worker command the server runs; the head token must be a real subcommand
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
		Desc: "notice, without blocking, when the last commit left generated output stale, and how many hunks a push sends are unread",
		Argv: []string{"server", NameCheckDrift},
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

// IsWorkerArgv reports whether argv is a registered job's worker command: its Argv exactly, or
// for check-drift its Argv followed by the arguments a [DriftHook] writes. The server's job
// dispatch uses it as an allowlist so a JobRequest can only run a recognized job's worker,
// never an arbitrary command handed to the fire-and-forget RPC.
func IsWorkerArgv(argv []string) bool {
	for _, j := range catalog {
		if slices.Equal(j.Argv, argv) {
			return true
		}
		if j.Name == NameCheckDrift && len(argv) > len(j.Argv) && slices.Equal(j.Argv, argv[:len(j.Argv)]) {
			_, ok := ParseDriftHook(argv[len(j.Argv):])
			return ok
		}
	}
	return false
}

// The hooks that run check-drift, and the flags its worker takes after its Argv.
const (
	DriftHookPostCommit = "post-commit"
	DriftHookPrePush    = "pre-push"

	driftHookFlag   = "--hook="
	driftRemoteFlag = "--remote="
	driftPushFlag   = "--push="
)

// DriftHook is the VCS hook a check-drift run is for. A commit carries only Hook; a push
// also names the remote and each ref it sends.
type DriftHook struct {
	Hook   string
	Remote string
	Pushes []DriftPush
}

// DriftPush is one ref a push sends, as git's pre-push hook reads it: Remote is the object
// the remote holds now, all zeros for a ref it does not have yet, and Local the one sent.
type DriftPush struct {
	Remote string
	Local  string
}

// Argv is the check-drift worker command for h, which [ParseDriftHook] reads back.
func (h DriftHook) Argv() []string {
	entry, _ := Lookup(NameCheckDrift)
	argv := append(slices.Clone(entry.Argv), driftHookFlag+h.Hook)
	if h.Remote != "" {
		argv = append(argv, driftRemoteFlag+h.Remote)
	}
	for _, p := range h.Pushes {
		argv = append(argv, driftPushFlag+p.Remote+":"+p.Local)
	}
	return argv
}

// ParseDriftHook reads the arguments [DriftHook.Argv] appends to check-drift's Argv. ok is
// false for anything it would not have written: an unknown hook or flag, a remote name
// that could pass for an option, or an object id that is not a full hex hash.
func ParseDriftHook(args []string) (h DriftHook, ok bool) {
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, driftHookFlag):
			h.Hook = strings.TrimPrefix(arg, driftHookFlag)
			if h.Hook != DriftHookPostCommit && h.Hook != DriftHookPrePush {
				return DriftHook{}, false
			}
		case strings.HasPrefix(arg, driftRemoteFlag):
			h.Remote = strings.TrimPrefix(arg, driftRemoteFlag)
			if !remoteName(h.Remote) {
				return DriftHook{}, false
			}
		case strings.HasPrefix(arg, driftPushFlag):
			remote, local, found := strings.Cut(strings.TrimPrefix(arg, driftPushFlag), ":")
			if !found || !objectID(remote) || !objectID(local) {
				return DriftHook{}, false
			}
			h.Pushes = append(h.Pushes, DriftPush{Remote: remote, Local: local})
		default:
			return DriftHook{}, false
		}
	}
	return h, h.Hook != ""
}

// objectID reports whether s is a full SHA-1 or SHA-256 object id in lowercase hex.
func objectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// remoteName reports whether s can name a git remote here. A leading dash is refused because
// the name reaches git's argv as part of a revision.
func remoteName(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._/-", r) {
			return false
		}
	}
	return true
}

// ActionString is the canonical trail "action" for a job argv: the space-joined command.
// It is the ONE place that format is defined, so the producer that records a job run (the
// server's OnJobDone callback) and the consumers that look a run up by action (the
// scheduler's due-check and the JobService metadata) cannot drift apart on how a run is keyed.
func ActionString(argv []string) string { return strings.Join(argv, " ") }
