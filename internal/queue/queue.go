// Package queue is magus's merge queue engine.
//
// The engine takes every change carrying merge intent at its head commit, stages it on
// the base branch, regenerates derived files there, validates the result, and merges
// through the host only on green. It holds no host knowledge: a [Provider] (a Buzz
// spell, bridged in internal/interp/bindings) talks to GitHub or GitLab, a [Repo] does
// the version-control work, a [Graph] answers which projects a change reaches, and a
// [Validator] runs the gate. The same split cache.RemoteBackend draws for the cache.
//
// Changes whose affected closures are pairwise disjoint are validated once together
// and merge independently: a failure in one never re-runs the other. Overlapping
// changes stack in queue order and bisect on failure.
package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// StatusContext is the commit status the queue owns. Branch protection requires it, so
// nothing reaches the base branch around the queue.
const StatusContext = "magus/queue"

// Change is one open change carrying merge intent, as the provider reported it.
type Change struct {
	ID     string // provider identifier, opaque to the engine ("482")
	Repo   string // provider's name for the repository, handed back on every call
	Head   string // head commit the intent was expressed at
	Ref    string // ref that fetches Head from the remote; empty fetches Head by sha
	Branch string // head branch the queue may push a regeneration to; empty when it may not
	Base   string // branch the change targets
	Title  string
	Author string
}

// Label is how the change is named in output and reports.
func (c Change) Label() string {
	if c.Title == "" {
		return "#" + c.ID
	}
	return "#" + c.ID + " (" + c.Title + ")"
}

// Approval is the review state of a change at one exact commit.
type Approval struct {
	Approved  bool
	Head      string // the change's current head, which may differ from the commit asked about
	Required  int    // approvals the host requires
	Approvals int    // approvals recorded at the commit asked about
	Reason    string // why not approved, when it is not
}

// State is a commit-status state.
type State string

const (
	StatePending State = "pending"
	StateSuccess State = "success"
	StateFailure State = "failure"
)

// Status is one commit status the queue posts under [StatusContext].
type Status struct {
	State       State
	Description string
}

// ListQuery scopes [Provider.List].
type ListQuery struct {
	Base   string // branch the queue merges into
	Remote string // URL of the remote the checkout fetches from, for the provider to name its repository
}

// Provider is the host side of the queue, shaped like cache.RemoteBackend. Every
// method is a network call; none may be skipped by an implementation, because a
// queue that cannot merge or kick back holds every change forever.
type Provider interface {
	// Name identifies the provider to a human. It must not dial.
	Name() string
	// List returns the open changes carrying merge intent against q.Base, in queue order.
	List(ctx context.Context, q ListQuery) ([]Change, error)
	// ApprovalAt reports c's review state at sha exactly.
	ApprovalAt(ctx context.Context, c Change, sha string) (Approval, error)
	// PostStatus sets [StatusContext] on sha.
	PostStatus(ctx context.Context, c Change, sha string, s Status) error
	// Merge merges c at sha through the host, keeping c's author as the author. It
	// errors when the host refused, including when c's head is no longer sha.
	Merge(ctx context.Context, c Change, sha string) error
	// KickBack removes c's merge intent and posts report to its author.
	KickBack(ctx context.Context, c Change, sha, report string) error
}

// Conflict is a textual conflict in files no target regenerates: a real code conflict,
// which the queue never resolves.
type Conflict struct {
	Change Change   // the change whose addition conflicted
	Paths  []string // the conflicted source files
	With   []string // what it conflicts with: base-branch commits touching Paths ("abc123 subject")
}

// ConflictError reports a [Conflict] from a [Repo] call.
type ConflictError struct{ Conflict Conflict }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s conflicts in %s", e.Conflict.Change.Label(), strings.Join(e.Conflict.Paths, ", "))
}

// asConflict unwraps a [ConflictError].
func asConflict(err error) (Conflict, bool) {
	var ce *ConflictError
	if errors.As(err, &ce) {
		return ce.Conflict, true
	}
	return Conflict{}, false
}

// RefusedError is a [Repo.Land] refusal the author has to fix, such as generated files
// the queue must regenerate on a branch it cannot push to. The change is kicked back
// with Reason.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

// Landing is what [Repo.Land] prepared for one change.
type Landing struct {
	// Merge is the commit the provider merges: the change's own head when merging it
	// reproduces the regenerated staging tree, else a commit Land pushed to the change.
	Merge string
	// Expect is a commit whose tree the base branch must carry after the merge.
	Expect string
}

// Repo is the version-control side of the queue.
type Repo interface {
	// Tip fetches branch and returns its tip.
	Tip(ctx context.Context, branch string) (string, error)
	// Fetch makes c.Head available locally.
	Fetch(ctx context.Context, c Change) error
	// Changed lists the paths head changes since its merge base with base.
	Changed(ctx context.Context, base, head string) ([]string, error)
	// Overlap merges changes onto base in order without touching the checkout, and
	// returns a *[ConflictError] for the first one that conflicts in a source file.
	// Conflicts in derived files are not reported: staging regenerates them.
	Overlap(ctx context.Context, base string, changes []Change) error
	// Stage builds base plus changes in order, regenerates derived files, and returns
	// the staging commit. A source conflict is a *[ConflictError].
	Stage(ctx context.Context, base string, changes []Change) (string, error)
	// Land prepares c to merge onto base; see [Landing].
	Land(ctx context.Context, base string, c Change) (Landing, error)
	// SameTree reports whether two commits carry identical trees.
	SameTree(ctx context.Context, a, b string) (bool, error)
}

// Closure is the set of projects a change can affect.
type Closure struct {
	Projects []string
	// Proven is false when the graph cannot vouch for Projects: a change to the
	// declarations themselves, or files no project claims. An unproven change overlaps
	// every other change.
	Proven bool
	Why    string // why unproven
}

// Graph computes affected closures from the base branch's declarations.
type Graph interface {
	Closure(ctx context.Context, paths []string) (Closure, error)
}

// Verdict is one validation outcome.
type Verdict struct {
	Green   bool
	Summary string // one line naming what failed, for the kick-back report
}

// Validator runs the gate on a staging commit. An error means the gate could not run
// at all, which stops the queue; a red gate is a Verdict.
type Validator interface {
	Validate(ctx context.Context, commit, base string) (Verdict, error)
}
