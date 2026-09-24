package types

import (
	"context"
	"errors"

	magustypes "github.com/egladman/magus/types"
)

// ReadVCS is the version control planning gets: it fetches from the remote into the
// local store and reads what the store holds, and changes no checkout, branch or remote
// ref.
type ReadVCS interface {
	magustypes.RevisionFetcher
	magustypes.AncestryReporter
	magustypes.RangeReporter
	magustypes.TreeReporter
	magustypes.TreeMerger
	// FindCommit is magus's VCSDriver's own; no capability carries it.
	FindCommit(ctx context.Context, dir, rev string) (magustypes.Commit, error)
}

// BuildVCS is the version control validation gets: ReadVCS, plus checkouts of its own
// in which it merges and commits locally. It reaches the remote only to fetch.
type BuildVCS interface {
	ReadVCS
	magustypes.CheckoutProvisioner
	magustypes.MergeStarter
	magustypes.ConflictResolver
	magustypes.CommitWriter
	// DirtyFiles is magus's VCSDriver's own; no capability carries it.
	DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error)
}

// PushVCS is the version control applying gets: BuildVCS, plus a push under a lease.
type PushVCS interface {
	BuildVCS
	magustypes.Pusher
}

// Candidate is one speculative merge commit and the checkout it was built in.
type Candidate struct {
	Commit string
	Dir    string
	// Scratch is a directory private to this candidate, for the caches and temporary
	// files of the hooks run on it: nothing another candidate's hooks wrote is in it.
	Scratch string
}

// GateResult is one gate run's outcome.
type GateResult struct {
	Green   bool
	Summary string // one line naming what failed, for the kick-back report
}

// Gate validates a candidate. onto is the commit the candidate was built onto:
// everything beneath it is validated by the candidates below, so a gate need run only
// what the top change adds. An error means the gate could not run, a failure the queue
// can prove is the machine's; anything the change's code did is a result.
type Gate interface {
	Validate(ctx context.Context, cand Candidate, onto string, c Change) (GateResult, error)
}

// BuildFacts is the build tool's side of the queue, read from the base's declarations,
// never from a change's.
type BuildFacts interface {
	// Affected returns the units (projects, or whatever the caller partitions by) paths
	// reach, and, when that set is not a proof, why: the paths edit the declarations it
	// was computed from, or files nothing claims. A nil affected set is unbounded.
	Affected(ctx context.Context, c Change, paths []string) (affected []string, unboundedBy string, err error)
	// Outputs reports which of paths some target declares as its output. Only those are
	// generated: a path a VCS attribute or anything else marks generated is source,
	// and a reviewer has to see it.
	Outputs(ctx context.Context, paths []string) (map[string]bool, error)
	// EditedInPlace reports which of paths some target declares it edits in place: a
	// hand-written file carrying a generated region. A regeneration may rewrite one, but
	// it is source everywhere else, so a conflict in one is never settled by
	// regenerating.
	EditedInPlace(ctx context.Context, paths []string) (map[string]bool, error)
	// Generation reports what regenerating outputs runs, and which of changed it would
	// run as code.
	Generation(ctx context.Context, outputs, changed []string) (Generation, error)
}

// Generation is the build tool's account of regenerating some outputs.
type Generation struct {
	// Units are what the build tool regenerates the outputs by, handed to the
	// regeneration hook.
	Units []string
	// Code lists the changed paths the regeneration would run as code: its targets'
	// definitions, their spell and op sources, toolchain pins and lockfiles, and every
	// code input those targets read.
	Code []string
	// Unbounded, when set, says why the build tool cannot bound what the regeneration
	// runs.
	Unbounded string
}

// Regeneration is one run of a regeneration hook.
type Regeneration struct {
	Dir     string // the checkout to regenerate in
	Scratch string // a directory private to that checkout
	Onto    string // the commit the checkout's change was merged onto
	Change  Change
	Paths   []string // the generated files to rewrite
	// Units are what the build tool regenerates Paths by. Set only when the caller
	// proved regenerating them runs none of the change's code.
	Units []string
}

// RegenerateFunc rewrites generated files in a checkout. A *[RefusedError] says the
// regeneration failed on the change's code; any other error is the machine's. Without
// one, a generated file both sides changed keeps the change's side, and one either side
// deleted stays deleted.
type RegenerateFunc func(ctx context.Context, r Regeneration) error

// ErrNoCommitter stops applying when a change needs an update commit and neither the
// provider nor the applier's configuration names who commits it. The change waits with
// [CodeWaitNoCommitter].
var ErrNoCommitter = errors.New("no committer for the update commit: the provider names none and none was configured")

// RefusedError is a refusal the author has to fix, such as a regeneration that failed on
// the change's code. The change is kicked back with Reason, Paths name the files at issue
// when there are any, and Remedy, when set, tells the author what to do, in the report.
type RefusedError struct {
	Reason string
	Paths  []string
	Remedy string
}

func (e *RefusedError) Error() string { return e.Reason }

// VerdictSource supplies validation's verdicts to applying as they appear.
type VerdictSource interface {
	// Poll returns what appeared since the last call. A source must read its
	// end-of-verdicts signal before the verdicts, so a final poll sees everything.
	Poll(ctx context.Context) (VerdictBatch, error)
}

// VerdictBatch is one [VerdictSource.Poll].
type VerdictBatch struct {
	Verdicts []Verdict
	// Rejected are the changes whose verdict could not be read. Each waits alone.
	Rejected []RejectedVerdict
	// Done says no more verdicts will arrive.
	Done bool
}

// RejectedVerdict is a change whose verdict a [VerdictSource] could not read.
type RejectedVerdict struct {
	Change string
	Reason string
}
