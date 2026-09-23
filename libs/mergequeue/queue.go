package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// DefaultStatusContext is the commit status an [Applier] posts when none is configured.
const DefaultStatusContext = "merge-queue"

// Change is one open change carrying merge intent.
type Change struct {
	ID     string `json:"id"`               // provider identifier, opaque to the queue ("482"); see [CheckID]
	Repo   string `json:"repo,omitempty"`   // provider's name for the repository, handed back on every call
	Head   string `json:"head"`             // head commit the intent was expressed at
	Ref    string `json:"ref,omitempty"`    // ref that fetches Head from the remote; empty fetches Head by commit
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
	// UnboundedBy, when set, says why Affected is not a proof: the change edits the
	// declarations the set was computed from, or files nothing claims. An unbounded
	// change overlaps every change.
	UnboundedBy string `json:"unbounded_by,omitempty"`
}

// Label is how the change is named in reports.
func (c Change) Label() string {
	if c.Title == "" {
		return "#" + c.ID
	}
	return "#" + c.ID + " (" + c.Title + ")"
}

// Proven reports whether Affected bounds everything the change can reach.
func (c Change) Proven() bool { return c.Affected != nil && c.UnboundedBy == "" }

// Approval is the review state of a change at one exact commit.
type Approval struct {
	Approved bool
	Head     string // the change's current head, which may differ from the commit asked about; never empty
	Reason   string // why not approved, when it is not
}

// CommitState is a commit status's state.
type CommitState string

const (
	StatePending CommitState = "pending"
	StateSuccess CommitState = "success"
	StateFailure CommitState = "failure"
)

// CommitStatus is one commit status the queue posts.
type CommitStatus struct {
	Context     string
	State       CommitState
	Description string
}

// ListQuery scopes [Provider.ListChanges].
type ListQuery struct {
	Base   string // branch the queue merges into
	Remote string // URL of the remote, for the provider to name its repository
}

// Provider is the forge side of the queue. A [Planner] calls only ListChanges (through
// the caller) and ApprovalAt; the rest write, and only an [Applier] calls them.
type Provider interface {
	// ListChanges returns the open changes carrying merge intent against q.Base, in queue
	// order.
	ListChanges(ctx context.Context, q ListQuery) ([]Change, error)
	// ApprovalAt reports c's review state at commit exactly.
	ApprovalAt(ctx context.Context, c Change, commit string) (Approval, error)
	// PostStatus sets s on commit.
	PostStatus(ctx context.Context, c Change, commit string, s CommitStatus) error
	// MergeChange merges c as one commit at exactly commit, with the change's own merge
	// method and its author as the author. message is the body for a squash when the
	// author set none. It errors when the host refused, including when c's head is no
	// longer commit.
	MergeChange(ctx context.Context, c Change, commit, message string) error
	// KickBack removes c's merge intent and posts report to its author.
	KickBack(ctx context.Context, c Change, commit, report string) error
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

// RefusedError is a refusal the author has to fix, such as a regeneration that failed on
// the change's code, or derived files the queue must regenerate on a branch it cannot
// push to. The change is kicked back with Reason.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

// WaitError says a change cannot go further now for a reason that says nothing against
// it, such as its branch moving. The change waits and is retried on the next run.
type WaitError struct{ Reason string }

func (e *WaitError) Error() string { return e.Reason }

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

// Gate validates a stage. onto is the commit the stage was built onto: everything
// beneath it is validated by the stages below, so a gate need run only what the top
// change adds. An error means the gate could not run, a failure of the machine rather
// than the change; a red gate is a result.
type Gate interface {
	Validate(ctx context.Context, s Stage, onto string, c Change) (GateResult, error)
}

// AffectedFunc answers what a change can affect from the paths it changes: the units it
// reaches, or why that set is not a proof.
type AffectedFunc func(ctx context.Context, c Change, paths []string) (affected []string, unboundedBy string, err error)

// RegenerateFunc rewrites the derived files in a stage's checkout at dir. onto is the
// commit the stage was built onto and paths are the derived files the change touched or
// conflicted in. A *[RefusedError] says the change's own code failed to regenerate; any
// other error is the machine's.
type RegenerateFunc func(ctx context.Context, dir, onto string, c Change, paths []string) error

// StagingRepo is the version-control side of planning and validation. It merges and
// regenerates, so it runs the changes' code, and must never hold a write credential.
type StagingRepo interface {
	// FetchTip fetches branch and returns its tip.
	FetchTip(ctx context.Context, branch string) (string, error)
	// FetchHead makes c.Head available locally.
	FetchHead(ctx context.Context, c Change) error
	// ReviewTarget is the commit a review of head covers; see [MergingRepo.ReviewTarget].
	ReviewTarget(ctx context.Context, tip, head string) (string, error)
	// Changed lists the paths head changes since its merge base with onto.
	Changed(ctx context.Context, onto, head string) ([]string, error)
	// CheckMerge merges c onto baseCommit without touching any checkout and returns a
	// *[ConflictError] when a source file conflicts. Derived files are not reported.
	CheckMerge(ctx context.Context, baseCommit string, c Change) error
	// Stage checks out onto in a directory of its own, merges c, regenerates the derived
	// files c touches, and commits. baseCommit is the plan's, whose attributes say which
	// files are derived. Safe for concurrent use. A source conflict is a
	// *[ConflictError]; a regeneration the change's code broke is a *[RefusedError].
	Stage(ctx context.Context, baseCommit, onto string, c Change) (Stage, error)
	// Discard removes a stage's directory; its commit stays in the object store.
	Discard(ctx context.Context, s Stage) error
	// SquashMessage is the squash body for head: its own commits since baseCommit.
	SquashMessage(ctx context.Context, baseCommit, head string) (string, error)
}

// MergingRepo is the version control of the step that merges. It runs git plumbing only,
// never a build, so the job holding the write credential executes no change's code.
type MergingRepo interface {
	FetchTip(ctx context.Context, branch string) (string, error)
	FetchHead(ctx context.Context, c Change) error
	// ReviewTarget is the commit a review of head covers: head itself, or, when head is an
	// update commit UpdateBranch pushed (or any merge of the base branch into an approved
	// commit), the commit beneath it. See the git package for the rule.
	ReviewTarget(ctx context.Context, tip, head string) (string, error)
	// ImportBundle loads the stage commits validation exported to file, creating no ref.
	ImportBundle(ctx context.Context, file string) error
	// Predict is the tree the base branch must carry once the change validated at stage
	// merges on tip: stage's changes since baseCommit, merged onto tip. onto is the commit
	// the stage was built onto. A file that both the stage and what merged since onto
	// changed is a combination nobody validated, reported as a *[ConflictError].
	Predict(ctx context.Context, baseCommit, tip, onto, stage string) (tree string, err error)
	// UpdateBranch returns the commit to merge so that merging c on tip yields tree:
	// c.Head when a plain merge already does, else an update commit it pushes to
	// c.Branch, merging tip into c the way a forge's "Update branch" does. A difference
	// from c's plain merge outside the files baseCommit marks derived is a
	// *[RefusedError]; a branch that moved or was deleted is a *[WaitError].
	UpdateBranch(ctx context.Context, baseCommit, tip string, c Change, tree string) (commit string, err error)
	// TreeOf returns rev's tree.
	TreeOf(ctx context.Context, rev string) (string, error)
}

// VerdictSink receives each verdict the moment validation decides it. Record may be
// called from several goroutines at once.
type VerdictSink interface {
	Record(ctx context.Context, v Verdict) error
}

// VerdictSource supplies validation's verdicts to an [Applier] as they appear.
type VerdictSource interface {
	// Poll returns the verdicts that appeared since the last call, and whether no more
	// will arrive. A source must read its end-of-verdicts signal before the verdicts, so
	// a final poll sees everything.
	Poll(ctx context.Context) (fresh []Verdict, done bool, err error)
}
