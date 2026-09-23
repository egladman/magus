package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// DefaultStatusContext is the commit status [Landing] posts when none is configured.
// Branch protection requires it, so nothing reaches the base branch around the queue.
const DefaultStatusContext = "merge-queue"

// Change is one open change carrying merge intent.
type Change struct {
	ID     string `json:"id"`               // provider identifier, opaque to the queue ("482")
	Repo   string `json:"repo,omitempty"`   // provider's name for the repository, handed back on every call
	Head   string `json:"head"`             // head commit the intent was expressed at
	Ref    string `json:"ref,omitempty"`    // ref that fetches Head from the remote; empty fetches Head by sha
	Branch string `json:"branch,omitempty"` // head branch the queue may push a regeneration to; empty when it may not
	Base   string `json:"base,omitempty"`   // branch the change targets
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	// Fork marks a change from another repository. The queue refuses those: it cannot
	// push their regeneration, and their authors hold no write access to vouch for them.
	Fork bool `json:"fork,omitempty"`
	// Affected is the set of projects (or any other unit the caller partitions by) the
	// change can affect. Omitted or null means unknown, which overlaps every change.
	Affected []string `json:"affected"`
	// Unbounded, when set, says why Affected is not a proof: the change edits the
	// declarations the set was computed from, or files nothing claims. An unbounded
	// change overlaps every change.
	Unbounded string `json:"unbounded,omitempty"`
}

// Label is how the change is named in reports.
func (c Change) Label() string {
	if c.Title == "" {
		return "#" + c.ID
	}
	return "#" + c.ID + " (" + c.Title + ")"
}

// Proven reports whether Affected bounds everything the change can reach.
func (c Change) Proven() bool { return c.Affected != nil && c.Unbounded == "" }

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

// Status is one commit status the queue posts.
type Status struct {
	Context     string
	State       State
	Description string
}

// ListQuery scopes [Provider.List].
type ListQuery struct {
	Base   string // branch the queue merges into
	Remote string // URL of the remote, for the provider to name its repository
}

// Provider is the forge side of the queue. [Planner] calls only List and ApprovalAt;
// the rest write, and only [Landing] calls them.
type Provider interface {
	// Name identifies the provider to a human. It must not dial.
	Name() string
	// List returns the open changes carrying merge intent against q.Base, in queue order.
	List(ctx context.Context, q ListQuery) ([]Change, error)
	// ApprovalAt reports c's review state at sha exactly.
	ApprovalAt(ctx context.Context, c Change, sha string) (Approval, error)
	// PostStatus sets s on sha.
	PostStatus(ctx context.Context, c Change, sha string, s Status) error
	// Merge lands c as one commit at exactly sha, with the change's own merge method and
	// its author as the author. message is the body for a squash when the author set
	// none. It errors when the host refused, including when c's head is no longer sha.
	Merge(ctx context.Context, c Change, sha, message string) error
	// KickBack removes c's merge intent and posts report to its author.
	KickBack(ctx context.Context, c Change, sha, report string) error
}

// Conflict is a textual conflict in files that are not derived: a real code conflict,
// which the queue never resolves.
type Conflict struct {
	Change Change   // the change whose addition conflicted
	Paths  []string // the conflicted source files
	With   []string // base-branch commits touching Paths ("abc123 subject")
}

// ConflictError reports a [Conflict].
type ConflictError struct{ Conflict Conflict }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s conflicts in %s", e.Conflict.Change.Label(), strings.Join(e.Conflict.Paths, ", "))
}

func asConflict(err error) (Conflict, bool) {
	var ce *ConflictError
	if errors.As(err, &ce) {
		return ce.Conflict, true
	}
	return Conflict{}, false
}

// RefusedError is a refusal the author has to fix, such as derived files the queue
// must regenerate on a branch it cannot push to. The change is kicked back with Reason.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

// Stage is one speculative staging commit and the directory it is checked out in.
type Stage struct {
	Commit string
	Dir    string
}

// GateResult is one gate run's outcome.
type GateResult struct {
	Green   bool
	Summary string // one line naming what failed, for the kick-back report
}

// Gate validates a stage. below is the commit the stage was built on: everything
// beneath it is validated by the stages below, so a gate need run only what the top
// change adds. An error means the gate could not run at all; a red gate is a result.
type Gate interface {
	Validate(ctx context.Context, s Stage, below string, c Change) (GateResult, error)
}

// Stager is the version-control side of planning and validation.
type Stager interface {
	// Tip fetches branch and returns its tip.
	Tip(ctx context.Context, branch string) (string, error)
	// Fetch makes c.Head available locally.
	Fetch(ctx context.Context, c Change) error
	// Changed lists the paths head changes since its merge base with base.
	Changed(ctx context.Context, base, head string) ([]string, error)
	// Overlap merges c onto base without touching any checkout and returns a
	// *[ConflictError] when a source file conflicts. Derived files are not reported.
	Overlap(ctx context.Context, base string, c Change) error
	// Build checks out on in a directory of its own, merges c, regenerates the derived
	// files c touches, and commits. base is the plan's base commit, whose attributes say
	// which files are derived. Safe for concurrent use. A source conflict is a
	// *[ConflictError].
	Build(ctx context.Context, base, on string, c Change) (Stage, error)
	// Discard removes a stage's directory; its commit stays in the object store.
	Discard(ctx context.Context, s Stage) error
	// Message is the squash body for head: its own commits since base.
	Message(ctx context.Context, base, head string) (string, error)
}

// Prepared is what [Lander.Prepare] chose to merge for one change.
type Prepared struct {
	// Merge is the commit the provider merges: the change's own head when a plain merge
	// already yields the validated tree, else a commit Prepare pushed to its branch.
	Merge string
}

// Lander is the landing side's version control. It runs git plumbing only, never a
// build, so the job holding the write credential executes no change's code.
type Lander interface {
	Tip(ctx context.Context, branch string) (string, error)
	Fetch(ctx context.Context, c Change) error
	// Import loads the stage commits validation exported to file, without creating any ref.
	Import(ctx context.Context, file string) error
	// Expect is the tree the base branch must carry once the change validated at stage
	// lands on now: stage's changes since base, merged onto now. A conflict is a
	// *[ConflictError].
	Expect(ctx context.Context, base, now, stage string) (string, error)
	// Prepare picks what to merge so that landing c on now yields tree. A difference from
	// c's plain merge outside the files base marks derived is a *[RefusedError]: the
	// queue only ever adds regenerated files to what was approved.
	Prepare(ctx context.Context, base, now string, c Change, tree string) (Prepared, error)
	// TreeOf returns rev's tree.
	TreeOf(ctx context.Context, rev string) (string, error)
}
