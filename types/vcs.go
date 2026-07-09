package types

import (
	"context"
	"errors"
	"time"
)

// VCSDriver describes a version control system.
type VCSDriver interface {
	Name() string
	Claims() []string
	Base() string
	// Root, Diff, and Metadata operate on the repository containing dir. An empty
	// dir uses the process working directory. Passing an explicit dir is required
	// for correctness when work runs concurrently, since the process cwd is global.
	Root(ctx context.Context, dir string) (string, error)
	Diff(ctx context.Context, dir, base string) ([]string, error)
	Bisect(ctx context.Context, dir string, opts BisectOptions) (Culprit, error)
	DiffCommands(ctx context.Context, dir, base string) (DiffCommandHints, error)
	Metadata(ctx context.Context, dir string) (VCSMeta, error)
	// Dirty reports whether the working tree has uncommitted changes. When paths
	// is non-empty the probe is scoped to those pathspecs (interpreted relative to
	// dir, the same as the VCS's own CLI); empty checks the whole repository. It is
	// the path-scoped counterpart to Metadata's repo-wide IsDirty.
	Dirty(ctx context.Context, dir string, paths []string) (bool, error)
	// DirtyFiles is Dirty with the detail: the changed entries as the backend's
	// status lines (git porcelain, hg status, jj diff --name-only), one per line,
	// nil when clean. Dirty is defined in terms of this; callers that report *what*
	// changed use these lines.
	DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error)
	// FindCommit looks up a revision (a VCS-native rev expression; empty means
	// the current revision) and returns its normalized Commit.
	FindCommit(ctx context.Context, dir, rev string) (Commit, error)
	// History returns up to limit recent commits, newest first.
	History(ctx context.Context, dir string, limit int) ([]Commit, error)
	// Describe returns a human-readable version string derived from the nearest
	// tag (git's `describe --tags --always --dirty`: tag, else short id, with a
	// -dirty suffix for a modified tree). Tags are a git-shaped concept; a backend
	// without an equivalent returns "" rather than faking one. Callers treat "" as
	// "no describe available" and fall back (e.g. to a short hash); a magus author
	// needing backend-specific behavior reaches for vcs.exe().
	Describe(ctx context.Context, dir string) (string, error)
}

// Person identifies who authored a revision.
type Person struct {
	Name  string
	Email string
}

// Commit is a VCS-agnostic snapshot of one revision. Every field is meaningful
// for every backend (git, hg, jj); concepts a single VCS lacks (jj's change id,
// git's author/committer split) are deliberately not modeled here. Reach for
// vcs.exe() for VCS-specific work.
type Commit struct {
	// ID is the content/revision identifier: git SHA, hg node, jj commit_id.
	ID    string
	Short string // abbreviated ID
	// Author wrote the change.
	Author Person
	// Date is when the revision was recorded in the repository (git/jj commit
	// date, hg's date): the reproducible "when", distinct from any author date.
	// Zero if the VCS reported no timestamp.
	Date time.Time
	// Subject is the message's first line; Body is the remainder.
	Subject string
	Body    string
	// Parents are parent IDs; more than one for a merge.
	Parents []string
}

// ToMap is the Buzz boundary map vcs.commit / vcs.history entries return:
// {id, short, author {name, email}, date, subject, body, parents}. date is
// RFC3339, empty when the VCS reported no timestamp.
func (c Commit) ToMap() map[string]any {
	date := ""
	if !c.Date.IsZero() {
		date = c.Date.Format(time.RFC3339)
	}
	return map[string]any{
		"id":      c.ID,
		"short":   c.Short,
		"author":  map[string]any{"name": c.Author.Name, "email": c.Author.Email},
		"date":    date,
		"subject": c.Subject,
		"body":    c.Body,
		"parents": c.Parents,
	}
}

// CommitAuthor is the boundary mirror of the {name, email} author record a
// vcs.commit / vcs.history result carries. The Buzz `object CommitAuthor` mirror
// is generated from this struct by cmd/magus-utils types; keep them in lockstep.
type CommitAuthor struct {
	Name  string
	Email string
}

// CommitRecord is the boundary mirror of the record vcs.commit / vcs.history
// return: the serializable, every-field-present view of a Commit (Date as an
// RFC3339 string, not time.Time). A magusfile annotates `> Commit` to get
// compile-checked field access on a commit record; the runtime value is the
// matching map (see commitToMap). The Buzz `object Commit` mirror is generated
// from this struct by cmd/magus-utils types (go:generate -type Commit).
type CommitRecord struct {
	ID      string `buzz:"id"`
	Short   string
	Author  CommitAuthor
	Date    string
	Subject string
	Body    string
	Parents []string
}

// VCSMeta holds per-revision metadata for embedding in build artifacts.
type VCSMeta struct {
	ShortHash  string
	Hash       string
	Branch     string
	CommitDate string
	IsDirty    bool
}

// VCSOptions holds explicit VCS configuration; non-zero fields override MAGUS_VCS_* env vars.
type VCSOptions struct {
	Enabled *bool  // nil = check MAGUS_VCS_ENABLED
	Name    string // overrides MAGUS_VCS_NAME
	BaseRef string // overrides MAGUS_VCS_BASE_REF
}

// VCSSource indicates how the active VCS was chosen.
type VCSSource string

const (
	VCSSourceExplicit VCSSource = "explicit"
	VCSSourceAuto     VCSSource = "auto"
	VCSSourceDefault  VCSSource = "default"
	VCSSourceDisabled VCSSource = "disabled"
)

// VCSResolution is the outcome of resolving the active VCS for a workspace.
type VCSResolution struct {
	Name   string // active VCS name, empty when disabled
	Source VCSSource
	Base   string
	VCS    VCSDriver // nil when disabled
}

// DiffCommandHints holds shell commands for inspecting a diff.
type DiffCommandHints struct {
	CLI string
	GUI string
}

// BisectOptions configures a VCSDriver.Bisect call.
type BisectOptions struct {
	Bad        string // commit known bad (default "HEAD")
	Good       string // commit known good; if empty, GoodBefore is used
	GoodBefore time.Time
	// TestCmd is passed to `sh -c` by the bisect runner; it must be operator-trusted.
	TestCmd string
}

// Culprit is the outcome of a successful VCSDriver.Bisect call.
type Culprit struct {
	SHA  string
	Info string // one-line subject, author, and date
}

// ErrVCSUnsupported is returned by operations not supported by a VCSDriver.
var ErrVCSUnsupported = errors.New("vcs: operation not supported by this VCS")

// ErrVCSUnknown is returned by the VCS resolver when an explicit VCS name
// is given but no built-in or registered implementation matches it.
var ErrVCSUnknown = errors.New("vcs: unknown VCS")

// MergeDriverInstaller is an optional capability for VCSDriver implementations
// that can register magus as the merge driver for declared output globs.
type MergeDriverInstaller interface {
	InstallMergeDriver(ctx context.Context, root string, outputGlobs []string) error
	CheckMergeDriver(ctx context.Context, root string) (bool, error)
}

// RemoteReporter is an optional capability for VCSDriver implementations that can
// report the repository's default remote URL (e.g. git's "origin" fetch URL). It
// lets callers derive a forge browse/blob URL for turning a workspace-relative
// source path into a link. Like the other optional capabilities, callers
// type-assert for it and degrade gracefully (no link) when a backend lacks it.
type RemoteReporter interface {
	// RemoteURL returns the default remote URL for the repository containing dir,
	// or "" with ErrVCSUnsupported when there is no remote configured.
	RemoteURL(ctx context.Context, dir string) (string, error)
}

// DefaultBranchReporter is an optional capability (sibling of RemoteReporter) for
// VCSDriver implementations that can report the repository's default branch, e.g.
// "main", independent of whatever branch is currently checked out. Committed
// artifacts (MAGUS.md's forge links) use it so their URLs stay stable no matter which
// feature branch or worktree generated them. Callers type-assert for it and degrade
// gracefully when a backend lacks it.
type DefaultBranchReporter interface {
	// DefaultBranch returns the default branch of the repo containing dir, or ""
	// with ErrVCSUnsupported when it cannot be determined.
	DefaultBranch(ctx context.Context, dir string) (string, error)
}

// CommitChange reduces one commit to who made it, when, and the repo-relative
// paths it touched: the input to churn attribution (no message or diff content).
type CommitChange struct {
	ID     string
	Author string
	Date   time.Time
	Files  []string
}

// ChurnReporter is an optional capability for VCSDriver implementations that can
// report which files recent commits touched, so churn (edit frequency) can be
// attributed to projects. Like MergeDriverInstaller, callers type-assert for it
// and degrade gracefully (skip the heatmap) when a backend lacks it.
type ChurnReporter interface {
	// ChangesByCommit returns up to commits recent non-merge commits, newest
	// first, each reduced to its author, date, and touched repo-relative paths.
	// since, when non-empty, is a backend-native lower bound on the commit date
	// (a git approxidate / RFC3339); commits still caps the result.
	ChangesByCommit(ctx context.Context, dir string, commits int, since string) ([]CommitChange, error)
}

// RevisionExporter is an optional capability for VCSDriver implementations that can
// materialize a revision's tracked files into a directory (a "checkout to a throwaway
// tree" without touching the working copy). Callers type-assert for it and degrade
// gracefully when a backend lacks it - either wrapping ErrVCSUnsupported (like the other
// capabilities) or, for a user-facing command, surfacing a plain message. It powers
// `magus graph diff --rev`, which builds a base knowledge graph from the exported tree.
type RevisionExporter interface {
	// ExportRevision writes the tree of rev (a backend-native revision expression)
	// into dstDir, re-rooted at dir: only dir's own subtree is exported, with paths
	// relative to it, so dstDir mirrors the workspace as of rev.
	ExportRevision(ctx context.Context, dir, rev, dstDir string) error
}
