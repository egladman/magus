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
	// FindCommit looks up a revision (a VCS-native rev expression; empty means
	// the current revision) and returns its normalized Commit.
	FindCommit(ctx context.Context, dir, rev string) (Commit, error)
	// History returns up to limit recent commits, newest first.
	History(ctx context.Context, dir string, limit int) ([]Commit, error)
}

// Person identifies who authored a revision.
type Person struct {
	Name  string
	Email string
}

// Commit is a VCS-agnostic snapshot of one revision. Every field is meaningful
// for every backend (git, hg, jj) — concepts a single VCS lacks (jj's change id,
// git's author/committer split) are deliberately not modeled here; reach for
// vcs.exe() for VCS-specific work.
type Commit struct {
	// ID is the content/revision identifier: git SHA, hg node, jj commit_id.
	ID    string
	Short string // abbreviated ID
	// Author wrote the change.
	Author Person
	// Date is when the revision was recorded in the repository (git/jj commit
	// date, hg's date) — the reproducible "when," distinct from any author date.
	// Zero if the VCS reported no timestamp.
	Date time.Time
	// Subject is the message's first line; Body is the remainder.
	Subject string
	Body    string
	// Parents are parent IDs — more than one for a merge.
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
