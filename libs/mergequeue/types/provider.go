package types

import (
	"context"
	"errors"
	"fmt"
	"slices"

	magustypes "github.com/egladman/magus/types"
)

// Provider is where changes are reviewed and merged, GitHub and the like. Planning
// calls Describe and ApprovalAt; applying also calls ListGreen and the rest, which write.
type Provider interface {
	// Describe reports what the provider supports.
	Describe(ctx context.Context, q ListQuery) (Capabilities, error)
	// ListChanges returns the open changes carrying merge intent against q.Base in
	// queue order, the merged changes an open one carries the head of, and the open
	// changes carrying no intent.
	ListChanges(ctx context.Context, q ListQuery) (Changes, error)
	// ApprovalAt reports c's review state at commit exactly.
	ApprovalAt(ctx context.Context, c Change, commit string) (Approval, error)
	// ListGreen returns every open change whose head carries the commit status
	// statusContext at success, whatever branch it targets, since a stacked change is
	// pointed at the base once the change beneath it merges.
	ListGreen(ctx context.Context, q ListQuery, statusContext string) ([]GreenChange, error)
	// PostStatus sets s on commit.
	PostStatus(ctx context.Context, c Change, commit string, s CommitStatus) error
	// Retarget points c at base. Already targeting it is success.
	Retarget(ctx context.Context, c Change, base string) error
	// MergeChange sees c merged with its own merge method, by the provider on its own or
	// on this call, and says which. It errors when the provider refused, including when
	// c's head is no longer m.Commit or a change in m.Through is no longer at its pinned
	// head.
	MergeChange(ctx context.Context, c Change, m MergeOptions) (MergeResult, error)
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

// GreenChange is an open change whose head carries the queue's status at success. It
// holds what [Provider.PostStatus] needs to set that status again.
type GreenChange struct {
	ID   string
	Repo string
	Head string
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

	// The fields below are Describe's alone. StatusContext asks it for a [Setup]: what
	// the base requires and who the write credential posts as. App names the app whose
	// credential the queue writes with (github: a GitHub App's slug); empty is the
	// provider's default credential. SetupSteps also asks for the steps that finish
	// wiring the queue, which cost the provider more reads, some needing permissions an
	// apply job does not hold.
	StatusContext string
	App           string
	SetupSteps    bool
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
	// Setup is nil unless [ListQuery.StatusContext] asked for it and the provider reports
	// one.
	Setup *Setup
}

// Setup is how the queue is wired on the provider as the provider reads it now, and the
// steps that finish wiring it. The queue never runs a step: a person does.
type Setup struct {
	// StatusContext is the commit status the queue posts, as asked.
	StatusContext string `json:"status_context"`
	// Credential is who the write credential posts statuses as.
	Credential Integration `json:"credential"`
	// RequiredChecks are the checks the base requires before a change merges, the queue's
	// own status among them once it is wired.
	RequiredChecks []RequiredCheck `json:"required_checks"`
	// Settings are the provider's repository settings the queue depends on.
	Settings []Setting `json:"settings,omitempty"`
	// App is the app the credential belongs to, when [ListQuery.App] named one and steps
	// were asked for.
	App *App `json:"app,omitempty"`
	// Steps are what a person runs or opens, in order; empty when the provider reports
	// nothing left to do or steps were not asked for.
	Steps []SetupStep `json:"steps"`
}

// Integration is who posts a commit status: a GitHub App's id and name, say.
type Integration struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// String renders i as "id (name)", or the id alone when it has no name.
func (i Integration) String() string {
	if i.Name == "" {
		return i.ID
	}
	return i.ID + " (" + i.Name + ")"
}

// RequiredCheck is one check the base requires.
type RequiredCheck struct {
	Context string `json:"context"`
	// Integration is the only integration whose status counts; empty accepts anyone's.
	Integration string `json:"integration,omitempty"`
	// Events are the events the provider saw report this check, when steps were asked
	// for ("pull_request"); empty when it saw none report it.
	Events []string `json:"events,omitempty"`
}

// Setting is one repository setting: its value now and the value the queue needs.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Want  string `json:"want"`
}

// App is the app a write credential belongs to and where a person manages it.
type App struct {
	Slug            string `json:"slug"`
	ID              string `json:"id"`
	ClientID        string `json:"client_id,omitempty"`
	RegistrationURL string `json:"registration_url,omitempty"`
	InstallURL      string `json:"install_url,omitempty"`
	// Environment, Variable and Secret name where the queue job reads the app's
	// credential from.
	Environment string `json:"environment,omitempty"`
	Variable    string `json:"variable,omitempty"`
	Secret      string `json:"secret,omitempty"`
}

// SetupStep is one thing a person does: run Command, or open URL.
type SetupStep struct {
	Title   string `json:"title"`
	Command string `json:"command,omitempty"`
	URL     string `json:"url,omitempty"`
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
	if c.Setup != nil {
		return c.Setup.Check()
	}
	return nil
}

// Check reports whether s names the status it describes, who the credential posts as,
// and steps a person can follow.
func (s Setup) Check() error {
	switch {
	case s.StatusContext == "":
		return errors.New("provider describes a setup for no status context")
	case s.Credential.ID == "":
		return errors.New("provider describes a setup without the integration its credential posts as")
	}
	for _, rc := range s.RequiredChecks {
		if rc.Context == "" {
			return errors.New("provider describes a required check with no context")
		}
	}
	for _, st := range s.Steps {
		if st.Title == "" || (st.Command == "") == (st.URL == "") {
			return fmt.Errorf("provider describes setup step %q, which needs a title and exactly one of a command or a URL", st.Title)
		}
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

// MergeResult is what a [Provider.MergeChange] that merged reports.
type MergeResult struct {
	// ByProvider says the provider merged the change on its own, as GitHub's auto-merge
	// does on behalf of whoever enabled it, rather than on the queue's call. What a merge
	// starts on the provider's side, a CI run on the base say, can differ between the two.
	ByProvider bool
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
	// Source is the validation run apply followed, as the provider names it
	// ("acme/widgets/runs/7"); empty when apply read a directory.
	Source string
	// Reproduce is how to run what validation ran on the change's candidate again; nil
	// when validation did not decide the kick-back, as for a conflict planning found.
	Reproduce *Reproduction
}

// Reproduction is the hook command lines validation ran on a candidate, as given to
// `magus queue validate`: run with its --gate and --regenerate, and --only the change,
// they build and gate that candidate again.
type Reproduction struct {
	Gate       string
	Regenerate string // empty when validation ran no regeneration
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
