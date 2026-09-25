// Package types holds the merge queue's contract: the documents a queue run reads and
// writes, what a provider, a CI system, a build tool and a gate answer, and the version
// control each step of a run gets. It imports only the standard library and magus's own
// types, so a provider or a test can build on it without the steps that act on it.
package types

import (
	"errors"
	"fmt"
	"strings"
)

// MergeMethod is how the provider merges a change, chosen by its author.
type MergeMethod string

const (
	MethodMerge  MergeMethod = "merge"  // one merge commit whose second parent is the head
	MethodSquash MergeMethod = "squash" // one new commit on the base holding the change's delta
	MethodRebase MergeMethod = "rebase" // the change's own commits replayed onto the base
)

// Valid reports whether m is one of the three merge methods.
func (m MergeMethod) Valid() bool {
	return m == MethodMerge || m == MethodSquash || m == MethodRebase
}

// Change is one open change carrying merge intent.
type Change struct {
	ID     string `json:"id"`               // provider identifier, opaque to the queue ("482"); see [CheckID]
	Repo   string `json:"repo,omitempty"`   // provider's name for the repository, handed back on every call
	Head   string `json:"head"`             // head commit the intent was expressed at
	Ref    string `json:"ref,omitempty"`    // ref that fetches Head from the remote; empty fetches Head by commit
	Branch string `json:"branch,omitempty"` // head branch the queue may push an update commit to; empty when it may not
	Base   string `json:"base"`             // branch the change targets
	Title  string `json:"title,omitempty"`
	// Method is how the change merges. Required: a provider default the queue cannot see
	// would make the merged shape a guess.
	Method MergeMethod `json:"method"`
	// Parent is the change the provider says this one is stacked on. It is a hint:
	// planning derives stacks from ancestry and holds a change whose hint disagrees.
	Parent string `json:"parent,omitempty"`
	// Fork marks a change from another repository. The queue refuses those: it cannot
	// push their update commits, and their authors hold no write access to vouch for them.
	Fork bool `json:"fork"`
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
	// unmerged, so it merges first.
	StackBase string `json:"stack_base,omitempty"`
	Below     string `json:"below,omitempty"`
	// AuthorRegenerates is planning's too, set on every change it admits: the generated
	// files the change touches that the build tool cannot prove regenerate without
	// running the change's code, so only its author can regenerate them. An applier
	// shows [FlagChangesGenerator] on an admitted change exactly when it holds any.
	AuthorRegenerates []string `json:"author_regenerates,omitempty"`
}

// Label is how the change is named in reports.
func (c Change) Label() string {
	if c.Title == "" {
		return "#" + c.ID
	}
	return "#" + c.ID + " (" + c.Title + ")"
}

// Check reports whether c can be handed to a version control system and used as a
// directory name: an id [CheckID] accepts, full commit ids as head and stack base, refs
// and branches in git's grammar, a base, and a merge method. Every field reaches a VCS
// command line, so a value that could read as an option or a refspec is refused here
// rather than quoted there.
func (c Change) Check() error {
	if err := CheckID(c.ID); err != nil {
		return err
	}
	if !IsObjectID(c.Head) {
		return fmt.Errorf("%s: head %q is not a full commit id", c.Label(), c.Head)
	}
	if c.StackBase != "" && !IsObjectID(c.StackBase) {
		return fmt.Errorf("%s: stack base %q is not a full commit id", c.Label(), c.StackBase)
	}
	if !c.Method.Valid() {
		return fmt.Errorf("%s: merge method %q, want merge, squash or rebase", c.Label(), c.Method)
	}
	if c.Ref != "" {
		if !strings.HasPrefix(c.Ref, "refs/") {
			return fmt.Errorf("%s: ref %q does not start with refs/", c.Label(), c.Ref)
		}
		if err := checkRefName(c.Ref); err != nil {
			return fmt.Errorf("%s: ref: %w", c.Label(), err)
		}
	}
	if err := checkBranch(c.Base); err != nil {
		return fmt.Errorf("%s: base: %w", c.Label(), err)
	}
	if c.Branch != "" {
		if err := checkBranch(c.Branch); err != nil {
			return fmt.Errorf("%s: branch: %w", c.Label(), err)
		}
	}
	for _, id := range []struct{ name, v string }{{"parent", c.Parent}, {"below", c.Below}} {
		if id.v == "" {
			continue
		}
		if err := CheckID(id.v); err != nil {
			return fmt.Errorf("%s: %s: %w", c.Label(), id.name, err)
		}
	}
	return nil
}

// MergedChange is a change that already merged whose head an open change may still
// carry: a squash or a rebase leaves the head off the base branch, and a change stacked
// on it has to be merged against that head rather than its natural merge base.
type MergedChange struct {
	ID     string      `json:"id"`
	Head   string      `json:"head"`   // head it merged at
	Commit string      `json:"commit"` // commit on the base branch carrying it
	Method MergeMethod `json:"method"`
}

func (m MergedChange) check() error {
	if err := CheckID(m.ID); err != nil {
		return err
	}
	if !IsObjectID(m.Head) || !IsObjectID(m.Commit) {
		return fmt.Errorf("#%s: head %q and commit %q must be full commit ids", m.ID, m.Head, m.Commit)
	}
	if !m.Method.Valid() {
		return fmt.Errorf("#%s: merge method %q, want merge, squash or rebase", m.ID, m.Method)
	}
	return nil
}

// UnqueuedChange is an open change carrying no merge intent. A queued change carrying
// its head waits: merging it would merge this one's commits, which nobody queued.
type UnqueuedChange struct {
	ID   string `json:"id"`
	Repo string `json:"repo,omitempty"` // provider's name for the repository, handed back to [Provider.Mark]
	Head string `json:"head"`
	// Mark is the mark the change shows now. An applier clears [MarkQueued] from it: the
	// change left the queue without the queue seeing it go.
	Mark Mark `json:"mark,omitempty"`
}

func (u UnqueuedChange) check() error {
	if err := CheckID(u.ID); err != nil {
		return err
	}
	if !IsObjectID(u.Head) {
		return fmt.Errorf("#%s: head %q is not a full commit id", u.ID, u.Head)
	}
	if !u.Mark.Valid() {
		return fmt.Errorf("#%s: mark %q, want queued, rejected or none", u.ID, u.Mark)
	}
	return nil
}

// CheckID accepts 1 to 128 ASCII letters, digits, '.', '_' and '-', not starting with
// '.' or '-'. An id names a directory and an artifact, so "..", a separator, or a
// leading dot (which marks an entry in progress) would escape or hide it.
func CheckID(id string) error {
	if id == "" || len(id) > 128 || id[0] == '.' || id[0] == '-' {
		return fmt.Errorf("change id %q is empty, too long, or starts with '.' or '-'", id)
	}
	for i := range len(id) {
		if b := id[i]; !idByte(b) {
			return fmt.Errorf("change id %q holds %q, want letters, digits, '.', '_' and '-'", id, b)
		}
	}
	return nil
}

func idByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '.' || b == '_' || b == '-'
}

// IsObjectID reports whether s is a full SHA-1 or SHA-256 object id in lowercase hex.
func IsObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := range len(s) {
		if b := s[i]; (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

// checkBranch accepts what `git check-ref-format --branch` does, which the queue holds
// every version control system to.
func checkBranch(name string) error {
	switch {
	case name == "":
		return errors.New("empty branch name")
	case strings.HasPrefix(name, "refs/"):
		return fmt.Errorf("branch %q is a full ref, want the branch alone", name)
	case name == "HEAD":
		return errors.New(`"HEAD" is not a branch name`)
	}
	return checkRefName(name)
}

// checkRefName applies git check-ref-format's rules, plus no leading '-'.
func checkRefName(name string) error {
	bad := func(why string) error { return fmt.Errorf("%q %s", name, why) }
	switch {
	case name == "@":
		return bad("is '@'")
	case strings.HasPrefix(name, "-"):
		return bad("starts with '-'")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return bad("has an empty component")
	case strings.HasSuffix(name, "."):
		return bad("ends with '.'")
	case strings.Contains(name, "..") || strings.Contains(name, "@{"):
		return bad("holds '..' or '@{'")
	}
	for _, comp := range strings.Split(name, "/") {
		if strings.HasPrefix(comp, ".") || strings.HasSuffix(comp, ".lock") {
			return bad("has a component starting with '.' or ending with '.lock'")
		}
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return bad(fmt.Sprintf("holds %q", r))
		}
	}
	return nil
}
