package mergequeue

import (
	"context"
	"errors"
	"time"
)

// VCS is the version control the queue drives: facts about revisions, and the few writes
// a candidate, an update commit and a hand-over between jobs need. The queue composes
// every merge, check and push from these; an implementation decides nothing.
//
// Revisions are full commit ids unless a method names a ref or a tree. dir is a checkout
// [VCS.CreateCheckout] made. Paths, as arguments and results, are repository-relative
// with forward slashes. Implementations must be safe for concurrent use; calls on one
// checkout are never concurrent.
type VCS interface {
	// FetchRef fetches ref, a full ref name ("refs/pull/482/head"), from the remote and
	// returns the commit it named. It touches no branch, tag or remote-tracking ref.
	FetchRef(ctx context.Context, ref string) (string, error)
	// FetchCommit makes the commit id present locally, fetching nothing when it is.
	FetchCommit(ctx context.Context, id string) error
	// IsAncestor reports whether ancestor is reachable from descendant; a commit is its
	// own ancestor.
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	// RangeFiles lists the paths head changed since it diverged from base.
	RangeFiles(ctx context.Context, base, head string) ([]string, error)
	// RangeCommits returns the commits reachable from head and not from base, newest
	// first. paths, when non-empty, keeps the commits that changed one of them.
	RangeCommits(ctx context.Context, base, head string, paths []string) ([]Commit, error)
	// FindCommit describes one commit.
	FindCommit(ctx context.Context, rev string) (Commit, error)
	// TreeID returns rev's tree id: two revisions with one tree hold the same files.
	TreeID(ctx context.Context, rev string) (string, error)
	// DiffTrees lists the paths whose content differs between a and b, each a tree id or
	// a commit. A rename is its old path and its new one.
	DiffTrees(ctx context.Context, a, b string) ([]string, error)
	// MergeTrees three-way merges m without a checkout. A conflict is a result, never an
	// error.
	MergeTrees(ctx context.Context, m TreeMerge) (TreeMergeResult, error)
	// GeneratedPaths reports which of paths rev marks generated (git's
	// linguist-generated), read from rev alone, never from a working copy or the box.
	GeneratedPaths(ctx context.Context, rev string, paths []string) (map[string]bool, error)

	// CreateCheckout materializes rev, detached, in dir, which must not exist.
	CreateCheckout(ctx context.Context, dir, rev string) error
	// RemoveCheckout removes a checkout CreateCheckout made; its commits stay.
	RemoveCheckout(ctx context.Context, dir string) error
	// StartMerge merges rev into dir's checkout from their natural merge base without
	// committing, leaving conflicts for Conflicts to report.
	StartMerge(ctx context.Context, dir, rev string) error
	// AbortMerge abandons dir's merge in progress.
	AbortMerge(ctx context.Context, dir string) error
	// Conflicts lists the unresolved paths of dir's merge in progress.
	Conflicts(ctx context.Context, dir string) ([]ConflictedPath, error)
	// KeepIncoming takes the merged revision's side of paths, marking nothing resolved.
	KeepIncoming(ctx context.Context, dir string, paths []string) error
	// MarkResolved records paths with their working-tree content.
	MarkResolved(ctx context.Context, dir string, paths []string) error
	// RemoveConflicts resolves paths by deleting them.
	RemoveConflicts(ctx context.Context, dir string, paths []string) error
	// DirtyFiles lists the paths dir's working tree changed since its last commit,
	// untracked files included.
	DirtyFiles(ctx context.Context, dir string) ([]string, error)
	// Commit records dir's working tree, or the merge in progress, and returns the commit
	// id. No hook runs and nothing is signed.
	Commit(ctx context.Context, dir string, c CheckoutCommit) (string, error)
	// CommitTree records c without a checkout and returns its id; it creates no ref.
	CommitTree(ctx context.Context, c TreeCommit) (string, error)
	// Push sets p.Ref on the remote to p.To while it is still at p.Expected. It never
	// creates the ref: a moved or deleted one is [ErrStaleLease]. Any other refusal is
	// the remote's, and retrying will not change it.
	Push(ctx context.Context, p PushLease) error
	// Bundle writes r.Head and every commit beneath it that r.Base lacks into file.
	Bundle(ctx context.Context, file string, r BundleRange) error
	// Unbundle loads a file Bundle wrote, creating no ref.
	Unbundle(ctx context.Context, file string) error
}

// ErrStaleLease is [VCS.Push] finding the remote ref moved or deleted since the caller
// last looked.
var ErrStaleLease = errors.New("the remote ref is not at the expected revision")

// Person is a commit's author or committer.
type Person struct {
	Name  string
	Email string
}

// Commit is what the queue reads of one commit.
type Commit struct {
	ID      string
	Parents []string // the first is the first parent
	Author  Person
	Subject string
}

// TreeMerge names one three-way merge for [VCS.MergeTrees].
type TreeMerge struct {
	// Base is the merge base. Empty means the natural merge base of Ours and Theirs; the
	// queue names one to merge only what Theirs added since a known point.
	Base         string
	Ours, Theirs string
}

// TreeMergeResult is what [VCS.MergeTrees] produced.
type TreeMergeResult struct {
	// Tree is the merged tree. With Conflicts it still names one, carrying conflict
	// markers.
	Tree      string
	Conflicts []string
}

// ConflictedPath is one unresolved path of a merge in a checkout.
type ConflictedPath struct {
	Path string
	// Deleted says one side or both deleted it, so only a deletion or a regeneration
	// settles it.
	Deleted bool
}

// CommitMeta is what every commit the queue writes carries. Author and Committer are
// required: nothing reads the box's identity, so one call yields one commit anywhere.
type CommitMeta struct {
	Message           string
	Author, Committer Person
	Date              time.Time // zero means now
}

// CheckoutCommit names one commit for [VCS.Commit].
type CheckoutCommit struct {
	CommitMeta
	// Paths, when set, are recorded from the working tree first. Empty commits what is
	// already recorded, which after a resolved merge is the merge.
	Paths []string
}

// TreeCommit names one commit for [VCS.CommitTree].
type TreeCommit struct {
	CommitMeta
	Tree    string
	Parents []string
}

// PushLease names one push for [VCS.Push].
type PushLease struct {
	Ref      string // full ref name
	To       string // commit id the ref is set to
	Expected string // commit id the ref must still hold
}

// BundleRange names the commits [VCS.Bundle] writes.
type BundleRange struct{ Base, Head string }
