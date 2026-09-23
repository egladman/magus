package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// DefaultStatusContext is the commit status an [Applier] posts when none is configured.
const DefaultStatusContext = "merge-queue"

// MergeMethod is how the provider lands a change, chosen by its author.
type MergeMethod string

const (
	MethodMerge  MergeMethod = "merge"  // one merge commit whose second parent is the head
	MethodSquash MergeMethod = "squash" // one new commit on the base holding the change's delta
	MethodRebase MergeMethod = "rebase" // the change's own commits replayed onto the base
)

func (m MergeMethod) valid() bool {
	return m == MethodMerge || m == MethodSquash || m == MethodRebase
}

// Change is one open change carrying merge intent.
type Change struct {
	ID     string `json:"id"`               // provider identifier, opaque to the queue ("482"); see [CheckID]
	Repo   string `json:"repo,omitempty"`   // provider's name for the repository, handed back on every call
	Head   string `json:"head"`             // head commit the intent was expressed at
	Ref    string `json:"ref,omitempty"`    // ref that fetches Head from the remote; empty fetches Head by commit
	Branch string `json:"branch,omitempty"` // head branch the queue may push an update commit to; empty when it may not
	Base   string `json:"base,omitempty"`   // branch the change targets
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	// Method is how the change lands. Required: a provider default the queue cannot see
	// would make the landed shape a guess.
	Method MergeMethod `json:"method"`
	// Parent is the change the provider says this one is stacked on. It is a hint:
	// planning derives stacks from ancestry and holds a change whose hint disagrees.
	Parent string `json:"parent,omitempty"`
	// Fork marks a change from another repository. The queue refuses those: it cannot
	// push their update commits, and their authors hold no write access to vouch for them.
	Fork bool `json:"fork,omitempty"`
	// Affected is the set of projects (or any other unit the caller partitions by) the
	// change can affect. Omitted or null means unknown, which overlaps every change.
	Affected []string `json:"affected"`
	// UnboundedBy, when set, says why Affected is not a proof: the change edits the
	// declarations the set was computed from, or files nothing claims. An unbounded
	// change overlaps every change.
	UnboundedBy string `json:"unbounded_by,omitempty"`

	// StackBase and Below are planning's, overwritten on any input. StackBase is the
	// head of the change this one is stacked on, the base its own delta is measured
	// from; empty when it is not stacked. Below is that change's id while it is still
	// unlanded, so it merges first.
	StackBase string `json:"stack_base,omitempty"`
	Below     string `json:"below,omitempty"`
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

// Landed is a change that already merged whose head an open change may still carry: a
// squash or a rebase leaves the head off the base branch, and a change stacked on it
// has to be merged against that head rather than its natural merge base.
type Landed struct {
	ID     string      `json:"id"`
	Head   string      `json:"head"`   // head it merged at
	Commit string      `json:"commit"` // commit on the base branch carrying it
	Method MergeMethod `json:"method"`
}

// Approval is the review state of a change at one exact commit.
type Approval struct {
	Approved bool
	Head     string // the change's current head, which may differ from the commit asked about; never empty
	Reason   string // why not approved, when it is not
	// Base is the branch the change targets now and Method the merge method it will
	// land with; both required, since a change merges into its own base with its own
	// method whatever the plan said.
	Base   string
	Method MergeMethod
	// ApprovedAt is an older commit the change holds its approvals at, when it holds
	// none at the commit asked about. The queue carries them over only when the newer
	// commit is that one rebased without conflicts and without changing its diff.
	ApprovedAt string
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

// ListQuery scopes [Provider.ListChanges] and [Provider.Describe].
type ListQuery struct {
	Base   string // branch the queue merges into
	Remote string // URL of the remote, for the provider to name its repository
}

// StackMerge is how a provider lands a stack of changes.
type StackMerge string

const (
	// StackMergeSequential lands one change per call.
	StackMergeSequential StackMerge = "sequential"
	// StackMergeAtomic lands a run of stacked changes in one call, which the provider
	// completes or stops partway through, never reordering.
	StackMergeAtomic StackMerge = "atomic"
)

// Capabilities is what the provider supports.
type Capabilities struct {
	StackMerge StackMerge
	// LinearStacks says a stacked change's branch must stay a linear line of commits on
	// its base, so an update commit the queue pushes there is linear too.
	LinearStacks bool
	Methods      []MergeMethod // the merge methods the repository allows
}

func (c Capabilities) check() error {
	if c.StackMerge != StackMergeSequential && c.StackMerge != StackMergeAtomic {
		return fmt.Errorf("the provider describes stack merging as %q; want %q or %q", c.StackMerge, StackMergeSequential, StackMergeAtomic)
	}
	if len(c.Methods) == 0 {
		return errors.New("the provider allows no merge method")
	}
	for _, m := range c.Methods {
		if !m.valid() {
			return fmt.Errorf("the provider allows merge method %q; want merge, squash or rebase", m)
		}
	}
	return nil
}

func (c Capabilities) allows(m MergeMethod) bool { return slices.Contains(c.Methods, m) }

// MergeRequest is one [Provider.MergeChange] call.
type MergeRequest struct {
	Commit  string // the head the change must still be at; the merge is pinned to it
	Message string // squash body when the author set none
	// Through, when set, is the lowest change of a stack run landing in this one call
	// ([StackMergeAtomic]); every change from it up to this one lands.
	Through string
}

// Kick is what a kick-back tells the author and the provider: a closed Code a provider
// can act on, the rendered Report, and the facts the report was rendered from.
type Kick struct {
	Code      Code
	Report    string
	Paths     []string // the files at issue: conflicting, or outside what may differ
	With      []string // base-branch commits touching Paths ("abc123 subject")
	Candidate string   // the candidate it was validated in, when one was built
}

// Provider is where changes are reviewed and merged, GitHub and the like. A [Planner]
// calls Describe and ApprovalAt; the rest write, and only an [Applier] calls them.
type Provider interface {
	// Describe reports what the provider supports.
	Describe(ctx context.Context, q ListQuery) (Capabilities, error)
	// ListChanges returns the open changes carrying merge intent against q.Base, in
	// queue order, and the recently landed changes an open one may be stacked on.
	ListChanges(ctx context.Context, q ListQuery) (Changes, error)
	// ApprovalAt reports c's review state at commit exactly.
	ApprovalAt(ctx context.Context, c Change, commit string) (Approval, error)
	// PostStatus sets s on commit.
	PostStatus(ctx context.Context, c Change, commit string, s CommitStatus) error
	// Retarget points c at base. Already targeting it is success.
	Retarget(ctx context.Context, c Change, base string) error
	// MergeChange lands c with its own merge method and its author as the author. It
	// errors when the host refused, including when c's head is no longer m.Commit.
	MergeChange(ctx context.Context, c Change, m MergeRequest) error
	// KickBack removes c's merge intent and tells its author why.
	KickBack(ctx context.Context, c Change, commit string, k Kick) error
}

// Conflict is a textual conflict in files that are not generated: a real code conflict,
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
// the change's code. The change is kicked back with Reason, and Paths name the files at
// issue when there are any.
type RefusedError struct {
	Reason string
	Paths  []string
}

func (e *RefusedError) Error() string { return e.Reason }

// WaitError says a change cannot go further now for a reason that says nothing against
// it, such as its branch moving. The change waits and is retried on the next run.
type WaitError struct {
	Code   Code
	Reason string
}

func (e *WaitError) Error() string { return e.Reason }

// Candidate is one speculative merge commit and the checkout it was built in.
type Candidate struct {
	Commit string
	Dir    string
}

// GateResult is one gate run's outcome.
type GateResult struct {
	Green   bool
	Summary string // one line naming what failed, for the kick-back report
}

// Gate validates a candidate. onto is the commit the candidate was built onto:
// everything beneath it is validated by the candidates below, so a gate need run only
// what the top change adds. An error means the gate could not run, a failure of the
// machine rather than the change; a red gate is a result.
type Gate interface {
	Validate(ctx context.Context, cand Candidate, onto string, c Change) (GateResult, error)
}

// BuildFacts is the build tool's side of the queue: what a change can affect, from the
// paths it changes. It reads the base, never a change's code.
type BuildFacts interface {
	// Affected returns the units (projects, or whatever the caller partitions by) paths
	// reach, and, when that set is not a proof, why: the paths edit the declarations it
	// was computed from, or files nothing claims. A nil affected set is unbounded.
	Affected(ctx context.Context, c Change, paths []string) (affected []string, unboundedBy string, err error)
}

// RegenerateFunc rewrites the generated files in a checkout at dir. onto is the commit
// the checkout's change was merged onto and paths are the generated files to rewrite.
// A *[RefusedError] says the change's own code failed to regenerate; any other error is
// the machine's. Without one, a generated file both sides changed keeps the change's
// side, and one either side deleted stays deleted.
type RegenerateFunc func(ctx context.Context, dir, onto string, c Change, paths []string) error

// ExportFunc writes the commits an [Applier] needs for candidate, from baseCommit up,
// into file.
type ExportFunc func(ctx context.Context, file, baseCommit, candidate string) error

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
