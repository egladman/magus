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
	// IsSecondaryCheckout reports whether dir is a second checkout of the same
	// repository under this VCS (a git linked worktree, an `hg share`, a jj
	// secondary workspace) rather than the primary. Discovery skips such dirs so a
	// repo's projects and spells are not indexed twice. Matched structurally
	// against the backend's on-disk signature; no process is spawned.
	IsSecondaryCheckout(dir string) bool
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
	// Tags lists the repository's tags, newest first. Tags are shared prior art,
	// not a git import: Mercurial versions them in .hgtags, and Fossil, Bazaar,
	// and Darcs each have the same concept under the same name. A backend that
	// genuinely lacks one returns none rather than faking it.
	//
	// pattern is a path.Match glob over the tag name ("v*"); "" lists every tag.
	// Wildcards stop at "/", so "v*" skips a namespaced tag like backup/x.
	//
	// An empty result means "no tags visible here", which is NOT "never
	// released": a shallow or single-branch clone commonly fetches none, so a
	// caller deciding what shipped must treat empty as unknown.
	Tags(ctx context.Context, dir, pattern string) ([]VCSTag, error)
}

// VCSTag is a VCS-agnostic release marker: a name pinned to a revision. Only the
// facts every tagging backend agrees on are modeled - an annotated tag's tagger
// and message are not, since a lightweight tag has neither. Reach for vcs.exe()
// for backend-specific tag work.
type VCSTag struct {
	// Name is the tag as a user writes it ("v0.3.0", or "libs/gopherbuzz/v0.1.0"
	// for a nested-module tag), without a refs/tags/ prefix.
	Name string
	// Prefix is everything through the final "/" of a nested-module tag
	// ("libs/gopherbuzz/" for "libs/gopherbuzz/v0.1.0"); "" for a root tag with
	// no "/" in its name.
	Prefix string
	// Version is Name's version portion (Name with Prefix stripped) parsed as
	// semver. It is the zero value - test Version.Original == "" - when Name
	// (or its portion after Prefix) is not a semver-shaped tag at all, or when
	// parsing it failed: an annotated tag like "checkpoint" or "release-2026"
	// is a legitimate, non-error case, not a reason to carry a separate
	// IsSemver bool that could disagree with the zero value it's mirroring.
	Version SemverVersion
	// Date is when an annotated tag was created, else when its revision was
	// recorded. Zero if the VCS reported no timestamp.
	Date time.Time
	// ID is the revision identifier the tag resolves to.
	ID string `buzz:"id"`
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
	ID    string `buzz:"id"`
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

// BuzzObject is the Buzz boundary map vcs.commit / vcs.history entries return:
// {id, short, author {name, email}, date, subject, body, parents}. date is
// RFC3339, empty when the VCS reported no timestamp.
func (c Commit) BuzzObject() BuzzObject {
	date := ""
	if !c.Date.IsZero() {
		date = c.Date.Format(time.RFC3339)
	}
	return BuzzObject{
		"id":      c.ID,
		"short":   c.Short,
		"author":  BuzzObject{"name": c.Author.Name, "email": c.Author.Email},
		"date":    date,
		"subject": c.Subject,
		"body":    c.Body,
		"parents": c.Parents,
	}
}

// CommitAuthor is the boundary mirror of the {name, email} author object a
// vcs.commit / vcs.history result carries. The Buzz `object CommitAuthor` mirror
// is generated from this struct by cmd/magus-utils types; keep them in lockstep.
type CommitAuthor struct {
	Name  string
	Email string
}

// CommitRecord is the boundary mirror of the object vcs.commit / vcs.history
// return: the serializable, every-field-present view of a Commit. A magusfile
// annotates `> Commit` to get compile-checked field access on a commit object;
// the runtime value is the matching map (see Commit.BuzzObject), never this
// struct directly - it exists so cmd/magus-utils types has something to
// reflect over. Date stays time.Time, same as Commit.Date: buzzType (in
// cmd/magus-utils/types.go) special-cases time.Time to the Buzz `str` type
// mirroring Commit.BuzzObject's RFC3339 formatting, so the two can share a
// type without the generated mirror changing shape. The Buzz `object Commit`
// mirror is generated from this struct by cmd/magus-utils types
// (go:generate -type Commit).
type CommitRecord struct {
	ID      string `buzz:"id"`
	Short   string
	Author  CommitAuthor
	Date    time.Time
	Subject string
	Body    string
	Parents []string
}

// VCSMeta holds per-revision metadata for embedding in build artifacts.
type VCSMeta struct {
	ShortHash string
	Hash      string
	Branch    string
	// CommitDate stays a string, deliberately not time.Time: each backend
	// formats it with its own native command (git's `log --format=%ci`, hg's
	// `{date|isodate}` template, jj's custom "%Y-%m-%d %H:%M:%S %z") and the
	// formats do not even agree with each other (hg's isodate filter omits
	// seconds; git and jj include them). It is opaque, backend-provided
	// display text meant for a build banner, not a value any caller parses
	// back into a time - forcing one shared layout here would mean discarding
	// or reformatting what the VCS itself chose to report.
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
	// EnsureMergeDriver re-installs only when the registration is missing or the
	// declared globs have moved on, reporting whether it changed anything. Callers
	// run it routinely, so it must be cheap and silent in the steady state.
	EnsureMergeDriver(ctx context.Context, root string, outputGlobs []string) (bool, error)
}

// RefreshHookInstaller is an optional capability (sibling of MergeDriverInstaller) for
// VCSDriver implementations that can install a hook firing on a history-changing event
// (branch switch, merge, rebase) to run command. It shares the managed-section
// convention the merge-driver install uses, so magus has one VCS-integration path, not
// two. Callers type-assert for it and skip gracefully when a backend lacks it (e.g. jj
// has no native hooks). It returns the labels of the hooks it installed, for a notice.
type RefreshHookInstaller interface {
	InstallRefreshHook(ctx context.Context, root, command string) ([]string, error)
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

// TrackedFileReporter is an optional capability (sibling of RemoteReporter) for
// VCSDriver implementations that can report which paths the VCS actually tracks.
//
// "Tracked" is not answerable from Dirty or DirtyFiles, which is why this exists
// separately: an ignored file and a clean tracked file both report nothing dirty, so
// a caller that needs to tell a committed artifact from a build product cannot infer
// it from cleanliness. Callers type-assert for it and skip the question when a
// backend lacks it, rather than guessing - a wrong guess here misclassifies
// generated output as committed, or the reverse.
type TrackedFileReporter interface {
	// TrackedFiles returns the subset of paths that the VCS tracks, as given.
	// Paths are interpreted relative to dir, matching the backend CLI's own pathspec
	// handling. An empty paths slice returns no results rather than every tracked
	// file in the repository.
	TrackedFiles(ctx context.Context, dir string, paths []string) ([]string, error)
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

// ConflictKind classifies why a path is unresolved in an in-progress merge.
type ConflictKind string

const (
	// ConflictKindContent is both sides changing the same file. The VCS has written
	// conflict markers into the working tree.
	ConflictKindContent ConflictKind = "content"
	// ConflictKindDeleted is one side deleting a file the other changed. No content
	// merge is possible, and no VCS invokes a merge driver for it - which is why a
	// driver alone never settles a workspace whose generated files moved.
	ConflictKindDeleted ConflictKind = "deleted"
	// ConflictKindBothDeleted is both sides deleting the file. No content on either
	// side, so recording the removal settles it.
	ConflictKindBothDeleted ConflictKind = "both-deleted"
)

// Conflict is one unresolved path in an in-progress merge, rebase, or cherry-pick.
type Conflict struct {
	// Path is relative to the root passed to Conflicts, using forward slashes.
	Path string
	// Kind is why the path is unresolved, and so what can settle it.
	Kind ConflictKind
}

// ConflictResolver is an optional capability (sibling of MergeDriverInstaller) for
// VCSDriver implementations that can report and settle an in-progress merge's
// unresolved paths in bulk.
//
// A merge driver is the wrong shape for generated files: the VCS invokes one per
// conflicted path, inside its own index manipulation, so cost scales with the conflict
// count and a regeneration cannot run there. Deciding every path first, regenerating
// once, then staging inverts that, and is the only way to settle ConflictKindDeleted,
// which a driver is never called for.
//
// Callers type-assert for it and degrade (tell the user to resolve by hand) when a
// backend lacks it.
//
// Every method takes the repository root and root-relative slash paths. A VCS reports
// conflict paths relative to the top level but reads pathspecs relative to the process
// directory, so mixing the two addresses the wrong files instead of failing.
type ConflictResolver interface {
	// Conflicts returns the unresolved paths of the in-progress operation. No
	// operation in progress is not an error: it returns none.
	Conflicts(ctx context.Context, root string) ([]Conflict, error)
	// KeepIncoming clears the conflict markers by taking the INCOMING side wholesale -
	// git's "theirs", the commit being replayed during a rebase - falling back to the
	// surviving side where the incoming side has none. The side is named here so two
	// backends cannot disagree about which change survives.
	//
	// It marks nothing resolved. A caller regenerates between KeepIncoming and
	// MarkResolved, so the regenerated content is what gets recorded.
	KeepIncoming(ctx context.Context, root string, paths []string) error
	// MarkResolved records paths as resolved with their current working-tree content
	// (git's staging, hg's `resolve --mark`).
	MarkResolved(ctx context.Context, root string, paths []string) error
	// RemoveConflicts resolves paths by deleting them from both the working tree and
	// the recorded state.
	RemoveConflicts(ctx context.Context, root string, paths []string) error
	// IgnoredPaths reports which paths the VCS's ignore RULES cover, tracked or not.
	// Resolution needs it to tell a generated file that is still tracked from one since
	// ignored: both are declared outputs, but only the first survives a
	// ConflictKindDeleted. Paths absent from the result are not ignored.
	IgnoredPaths(ctx context.Context, root string, paths []string) (map[string]bool, error)
}

// ConflictPredictor is an optional capability (sibling of ConflictResolver) for
// VCSDriver implementations that can predict the conflicts merging a base revision
// would produce, WITHOUT touching the working tree or the recorded state.
//
// Hosting services compute a pull request's mergeability with a plain 3-way merge
// and never run a per-clone merge driver, so the first conflict signal a user gets
// is the service's banner - after pushing. Prediction moves that signal to before
// the push, where a resolver can say which conflicts are generated and settle in
// seconds. Callers type-assert for it and degrade when a backend lacks it.
type ConflictPredictor interface {
	// PredictConflicts returns the paths that would be unresolved after merging
	// base into the current revision. A clean predicted merge returns none.
	// ConflictKindBothDeleted never appears: both sides agreeing to delete is not
	// a conflict in a real merge either.
	PredictConflicts(ctx context.Context, root, base string) ([]Conflict, error)
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
