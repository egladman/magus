package types

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/json"
)

// WorkKind is what holds a piece of work a person could lose.
type WorkKind string

const (
	// WorkCheckout is a registered checkout: its files and its head.
	WorkCheckout WorkKind = "checkout"
	// WorkBranch is a local branch holding unpublished revisions.
	WorkBranch WorkKind = "branch"
	// WorkStash is a stash entry.
	WorkStash WorkKind = "stash"
	// WorkPreserved is a capture Preserve anchored.
	WorkPreserved WorkKind = "preserved"
)

// WorkKinds is every kind, in the order a ledger lists them.
func WorkKinds() []WorkKind {
	return []WorkKind{WorkCheckout, WorkBranch, WorkStash, WorkPreserved}
}

// WorkState is what accounts for a piece of work, if anything does.
type WorkState string

const (
	// WorkUnaccounted is work nothing accounts for.
	WorkUnaccounted WorkState = "unaccounted"
	// WorkTracked is work a live job row holds.
	WorkTracked WorkState = "tracked"
	// WorkLocalOnly matches a work.local_only glob: never published.
	WorkLocalOnly WorkState = "local-only"
	// WorkAbsorbed merges into the base without changing its tree.
	WorkAbsorbed WorkState = "absorbed"
	// WorkEnded is work a person ended, with a reason, at its head.
	WorkEnded WorkState = "ended"
	// WorkClear holds nothing to lose: no uncommitted file and no
	// unpublished revision.
	WorkClear WorkState = "clear"
)

// WorkStates lists the states a ledger line may carry; a [WorkItem] whose
// State is not among them fails validation. Each call returns a new slice.
func WorkStates() []WorkState {
	return []WorkState{WorkUnaccounted, WorkTracked, WorkLocalOnly,
		WorkAbsorbed, WorkEnded, WorkClear}
}

// WorkItem is one line of the work ledger: a checkout, branch, stash or
// preserved capture of one repository, and what accounts for it. The last
// line per [WorkItem.Key] is the item.
type WorkItem struct {
	Kind WorkKind
	// Name is the checkout root, branch, stash ref or preserved handle.
	Name string
	// Repo is the primary checkout's root, so a reader of every ledger
	// can tell whose an item is.
	Repo  string
	State WorkState
	// Head is the revision the item holds; Base is what it was judged
	// absorbed against.
	Head, Base string
	// Dirty counts a checkout's uncommitted files.
	Dirty int
	// Commits counts revisions from Head that no remote or Base reaches.
	Commits int
	// Gone marks a registered checkout whose directory is gone.
	Gone bool
	// Why is what decided State: a job, a glob, an unanswered read.
	Why string
	// Reason is what the person said when they ended the item.
	Reason string
	// At is unix seconds when this line was written.
	At int64
	// Dropped ends the key: the item no longer exists.
	Dropped bool
}

// Key is "<kind>:<name>".
func (w WorkItem) Key() string {
	return string(w.Kind) + ":" + w.Name
}

type workItemWire struct {
	Kind    WorkKind  `json:"kind"`
	Name    string    `json:"name"`
	Repo    string    `json:"repo"`
	State   WorkState `json:"state"`
	Head    string    `json:"head,omitempty"`
	Base    string    `json:"base,omitempty"`
	Dirty   int       `json:"dirty,omitempty"`
	Commits int       `json:"commits,omitempty"`
	Gone    bool      `json:"gone,omitempty"`
	Why     string    `json:"why,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	At      int64     `json:"at"`
	Dropped bool      `json:"dropped,omitempty"`
}

// MarshalJSON refuses an unknown kind or state, so the ledger never
// holds a line its reader would refuse.
func (w WorkItem) MarshalJSON() ([]byte, error) {
	if err := w.valid(); err != nil {
		return nil, err
	}
	return json.Marshal(workItemWire(w))
}

// UnmarshalJSON refuses an unknown kind or state rather than guess one.
func (w *WorkItem) UnmarshalJSON(b []byte) error {
	var wire workItemWire
	if err := json.Unmarshal(b, &wire); err != nil {
		return fmt.Errorf("decode work item: %w", err)
	}
	item := WorkItem(wire)
	if err := item.valid(); err != nil {
		return err
	}
	*w = item
	return nil
}

func (w WorkItem) valid() error {
	if !slices.Contains(WorkKinds(), w.Kind) {
		return fmt.Errorf("work item %q: kind %q is not one of %v", w.Name, w.Kind, WorkKinds())
	}
	if !slices.Contains(WorkStates(), w.State) {
		return fmt.Errorf("work item %s: state %q is not one of %v", w.Key(), w.State, WorkStates())
	}
	return nil
}

// HeldRevision is a revision a repository holds apart from its branches.
type HeldRevision struct {
	// Kind is WorkStash or WorkPreserved.
	Kind WorkKind
	// Name is the backend's own name: a stash ref, a preserved handle.
	Name string
	Head string
}
