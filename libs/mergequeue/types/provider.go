package types

import (
	"context"
	"errors"
	"fmt"
	"slices"

	magustypes "github.com/egladman/magus/types"
)

// Provider is where changes are reviewed and merged, GitHub and the like. Planning
// calls Describe and ApprovalAt; the rest write, and only applying calls them.
type Provider interface {
	// Describe reports what the provider supports.
	Describe(ctx context.Context, q ListQuery) (Capabilities, error)
	// ListChanges returns the open changes carrying merge intent against q.Base in
	// queue order, the merged changes an open one carries the head of, and the open
	// changes carrying no intent.
	ListChanges(ctx context.Context, q ListQuery) (Changes, error)
	// ApprovalAt reports c's review state at commit exactly.
	ApprovalAt(ctx context.Context, c Change, commit string) (Approval, error)
	// PostStatus sets s on commit.
	PostStatus(ctx context.Context, c Change, commit string, s CommitStatus) error
	// Retarget points c at base. Already targeting it is success.
	Retarget(ctx context.Context, c Change, base string) error
	// MergeChange merges c with its own merge method. It errors when the provider
	// refused, including when c's head is no longer m.Commit or a change in m.Through is
	// no longer at its pinned head.
	MergeChange(ctx context.Context, c Change, m MergeOptions) error
	// KickBack removes c's merge intent, wherever it lives, and tells its author why.
	KickBack(ctx context.Context, c Change, commit string, k Kick) error
}

// Approval is the review state of a change at one exact commit, and what the provider
// says of the change now.
type Approval struct {
	Approved bool
	Head     string // the change's current head, which may differ from the commit asked about; never empty
	Reason   string // why not approved, when it is not
	// Base is the branch the change targets now and Method the merge method it will
	// merge with; both required, since a change merges into its own base with its own
	// method whatever the plan said.
	Base   string
	Method MergeMethod
	// Queued says the change still carries merge intent. The queue merges nothing whose
	// author withdrew it.
	Queued bool
	// ApprovedCommit is an older commit the change holds its approvals at, when it holds
	// none at the commit asked about. The queue carries them over only when the newer
	// commit is that one rebased without conflicts and without changing its diff.
	ApprovedCommit string
	// BranchSharedWith lists the other open changes whose head branch is this change's
	// branch. The queue pushes no update commit to a shared branch.
	BranchSharedWith []string
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
	Base      string // branch the queue merges into
	RemoteURL string // the remote's URL, for the provider to name its repository
}

// StackMerge is how a provider merges a stack of changes.
type StackMerge string

const (
	// StackMergeSequential merges one change per call.
	StackMergeSequential StackMerge = "sequential"
	// StackMergeAtomic merges a run of stacked changes in one call, which the provider
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
	// QueueLabel is the prefix of the label that queues a change, followed by its merge
	// method ("queue: squash"); empty when the provider queues changes some other way.
	QueueLabel string
	// Committer is the identity the provider's automation pushes as, which commits what
	// the queue writes to a change's branch. Zero when the provider names none.
	Committer magustypes.Person
}

// Check reports whether c names a known stack merge and at least one valid merge method.
func (c Capabilities) Check() error {
	if c.StackMerge != StackMergeSequential && c.StackMerge != StackMergeAtomic {
		return fmt.Errorf("provider describes stack merging as %q, want %q or %q", c.StackMerge, StackMergeSequential, StackMergeAtomic)
	}
	if len(c.Methods) == 0 {
		return errors.New("provider allows no merge method")
	}
	for _, m := range c.Methods {
		if !m.Valid() {
			return fmt.Errorf("provider allows merge method %q, want merge, squash or rebase", m)
		}
	}
	if c.Committer != (magustypes.Person{}) && (c.Committer.Name == "" || c.Committer.Email == "") {
		return fmt.Errorf("provider names committer %q <%s>, which needs a name and an email", c.Committer.Name, c.Committer.Email)
	}
	return nil
}

// Allows reports whether the repository allows merging with m.
func (c Capabilities) Allows(m MergeMethod) bool { return slices.Contains(c.Methods, m) }

// MergeOptions is one [Provider.MergeChange] call.
type MergeOptions struct {
	Commit  string // the head the change must still be at; the merge is pinned to it
	Message string // squash body when the author set none
	// Through, when set, is the run of stacked changes beneath this one that merge in
	// the same call ([StackMergeAtomic]), lowest first, each pinned to the head it must
	// still be at.
	Through []PinnedChange
}

// PinnedChange is a change and the head it must still be at.
type PinnedChange struct {
	ID     string
	Commit string
}

// Kick is what a kick-back tells the author and the provider: a closed Code a provider
// can act on, the rendered Report, and the facts the report was rendered from.
type Kick struct {
	Code            Code
	Report          string
	Paths           []string // the files at issue: conflicting, or outside what may differ
	With            []string // base-branch commits touching Paths ("abc123 subject")
	CandidateCommit string   // the candidate it was validated in, when one was built
}

// ArtifactLister is the CI system's side of the queue: what one validation run has
// uploaded.
type ArtifactLister interface {
	// ListArtifacts lists the artifacts source has uploaded so far. Complete must be read
	// before the listing, so a listing that says complete holds everything.
	ListArtifacts(ctx context.Context, source string) (ArtifactListing, error)
}

// ArtifactListing is one listing of a validation run's artifacts.
type ArtifactListing struct {
	// Complete says the run has finished, so nothing more will be uploaded.
	Complete bool
	// Headers are what a download needs, its credential included. They are sent to each
	// artifact's URL and dropped on a redirect to another host.
	Headers   map[string]string
	Artifacts []Artifact
}

// Artifact is one uploaded zip archive.
type Artifact struct {
	Name string
	URL  string // https
}
