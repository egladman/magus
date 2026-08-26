package types

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// ParentRef names the parent of the checked-out commit in this backend's own
	// revision syntax (git HEAD^, hg p1(.), jj @-). It is the fallback base for a
	// ref that builds itself, where Base - that ref's own tip - would compare a
	// commit against itself and report nothing affected.
	ParentRef() string
	// Root, ChangedFiles, and Metadata operate on the repository containing dir. An empty
	// dir uses the process working directory. Passing an explicit dir is required
	// for correctness when work runs concurrently, since the process cwd is global.
	Root(ctx context.Context, dir string) (string, error)
	ChangedFiles(ctx context.Context, dir, base string) ([]string, error)
	Bisect(ctx context.Context, dir string, opts BisectOptions) (Culprit, error)
	DiffCommands(ctx context.Context, dir, base string) (DiffCommandHints, error)
	Metadata(ctx context.Context, dir string) (VCSMeta, error)
	// Dirty reports whether the working tree has uncommitted changes. When paths
	// is non-empty the probe is scoped to those pathspecs (interpreted relative to
	// dir, the same as the VCS's own CLI); empty checks the whole repository. It is
	// the path-scoped counterpart to Metadata's repo-wide IsDirty.
	Dirty(ctx context.Context, dir string, paths []string) (bool, error)
	// DirtyFiles is Dirty with the detail: the changed PATHS, relative to the
	// repository root and using forward slashes, nil when clean. Dirty is defined in
	// terms of this; callers that report *what* changed use these paths.
	//
	// Paths, not the backend's status lines, and the difference is the whole contract.
	// Each backend prints a different shape - git porcelain's two status columns, hg
	// and sl's one, jj's bare `diff --name-only` output, plus git's " -> " for a rename
	// and its C-quoting for unusual bytes - and only the driver knows which it emits.
	// Returning lines pushed that knowledge outward, where it grew THREE parsers that
	// disagreed: one keyed on the backend name, one that guessed the prefix from the
	// line's own bytes, and a Buzz-boundary wrapper delegating to the first. The
	// guessing one read a jj file named "A note.txt" as status "A " plus path
	// "note.txt", so a legitimately-named file silently matched no glob.
	//
	// A per-entry status CODE is deliberately not modeled. It is not portable - jj
	// reports none at all - which is the same reason types.Status carries paths only.
	// Reach for vcs.cmd when the codes matter.
	DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error)
	// DirtyDiff is DirtyFiles with the CONTENT: the working tree's uncommitted changes
	// to those paths, as the backend's own unified diff, empty when nothing changed.
	// Callers that must show WHY a file changed use this; the ones that only need the
	// names use DirtyFiles.
	//
	// Parity here means every backend answers the question, not that the bytes match:
	// git, hg, sl, and jj each emit their native diff header, and no wrapper can reconcile
	// those without lying about what ran. Context width follows the backend's own flag
	// where it has one.
	DirtyDiff(ctx context.Context, dir string, paths []string) (string, error)
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
// for every backend (git, hg, sl, jj); concepts a single VCS lacks (jj's change id,
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
	// ID and Short are the revision identifier and its abbreviation, named to match
	// Commit.ID above rather than for git's word: "hash" is git and hg, while jj has
	// a commit_id and a change_id and no hash in its vocabulary at all. One concept
	// had two names in this file, and Commit.ID is the one that already said so.
	Short string
	ID    string
	// Ref is the movable name pointing at this revision - a git branch, a
	// Mercurial named branch, a Jujutsu bookmark - or "" when there is none
	// (jj's working copy is usually an anonymous change, so empty is ordinary
	// there). Named for the concept rather than for git's word for it, matching
	// the vcs.ref() a magusfile already calls; "branch" is what two of the three
	// backends happen to call theirs, which is not the same as it being the
	// portable name.
	Ref string
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
	// ID is the offending revision, matching Commit.ID and VCSMeta.ID. It was SHA,
	// which is git's alone - hg reports a node, jj a commit id - and bisect is not a
	// git-only operation (hg has its own).
	ID   string
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

// DefaultRefReporter is an optional capability (sibling of RemoteReporter) for
// VCSDriver implementations that can report the repository's default branch, e.g.
// "main", independent of whatever branch is currently checked out. Committed
// artifacts (MAGUS.md's forge links) use it so their URLs stay stable no matter which
// feature branch or worktree generated them. Callers type-assert for it and degrade
// gracefully when a backend lacks it.
type DefaultRefReporter interface {
	// DefaultRef returns the repo's primary line of development - git's default
	// branch, hg's "default", jj's trunk() - for the repo containing dir, or ""
	// with ErrVCSUnsupported when it cannot be determined.
	DefaultRef(ctx context.Context, dir string) (string, error)
}

// RevTimeReporter is an optional capability (sibling of RemoteReporter) for VCSDriver
// implementations that can report when a named revision was committed.
//
// Separate from Metadata's CommitDate, which describes the checked-out commit and is
// opaque display text. This one answers it for an ARBITRARY rev and returns a real
// time.Time, because the caller does arithmetic on it: how far behind a comparison base
// has fallen is a number, not a banner string.
type RevTimeReporter interface {
	// RevTime returns the commit date of rev in the repository containing dir.
	//
	// found is false when rev names nothing in this clone, which is an ordinary
	// state rather than an error - a base branch never fetched into a fresh clone
	// resolves to nothing, and a caller reporting staleness has to tell "old" from
	// "not here". err is reserved for a backend that answered something it cannot
	// itself read back.
	RevTime(ctx context.Context, dir, rev string) (t time.Time, found bool, err error)
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

// IgnoredFileReporter is an optional capability (sibling of TrackedFileReporter)
// for VCSDriver implementations that can report which paths the VCS ignores.
//
// Different from TrackedFiles, and the difference is the point: "untracked" lumps
// a file nobody has committed YET together with one nothing should ever commit,
// and only the ignore rules distinguish them. A derived artifact that must be
// reproducible from a clean checkout filters on IGNORED, or it drops work in
// progress.
//
// Callers type-assert and skip the question when a backend lacks it, so treating
// an unknown answer as "not ignored" keeps such a backend behaving as before.
type IgnoredFileReporter interface {
	// IgnoredFiles returns the subset of paths the VCS ignores, as given. Paths are
	// interpreted relative to dir. An empty paths slice returns no results.
	IgnoredFiles(ctx context.Context, dir string, paths []string) ([]string, error)
}

// ChangeStatus is what a commit did to one path. It exists so churn attribution can
// tell a rename from a delete-plus-add: without that distinction a file's history
// splits across every name it ever had, and each fragment ranks as a separate,
// quieter file than the one thing actually being rewritten.
type ChangeStatus string

const (
	ChangeAdded    ChangeStatus = "added"
	ChangeModified ChangeStatus = "modified"
	ChangeDeleted  ChangeStatus = "deleted"
	ChangeRenamed  ChangeStatus = "renamed"
)

// FileChange is one path a commit touched. Path is the name AFTER the commit;
// PrevPath is set only on a rename and carries the name before it, which is the
// edge a reader follows to reassemble a file's lineage.
type FileChange struct {
	Path     string
	PrevPath string
	Status   ChangeStatus
}

// CommitChange reduces one commit to who made it, when, and the repo-relative
// paths it touched: the input to churn attribution (no message or diff content).
type CommitChange struct {
	ID     string
	Author string
	Date   time.Time
	Files  []FileChange
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
	//
	// A backend that cannot detect renames reports them as a delete and an add,
	// which costs lineage but stays correct: PrevPath is simply never set, and
	// FileHotspots then ranks the two names separately rather than wrongly.
	ChangesByCommit(ctx context.Context, dir string, commits int, since string) ([]CommitChange, error)
}

// BranchChange is one other line of work and the repo-relative paths it changes.
type BranchChange struct {
	// Ref is the branch as a reader would name it, with any remote-tracking prefix removed:
	// "feat/audience", not "refs/remotes/origin/feat/audience".
	Ref   string   `json:"ref"`
	Paths []string `json:"paths"`
}

// BranchChangeReporter is an optional capability for VCSDriver implementations that can report
// what OTHER branches are changing, so a reader can be told a file in front of them is also being
// edited elsewhere before the merge conflict tells them.
//
// Callers type-assert for it and degrade gracefully. Degrading here means saying NOTHING rather
// than "no branch competes": a backend that cannot answer and a repository where nothing overlaps
// are different facts, and only one of them is reassuring.
type BranchChangeReporter interface {
	// BranchChanges returns up to limit branches other than the current one, most recently
	// updated first, each with the paths it changes relative to base.
	//
	// Remote-tracking refs only. It answers "what is somebody else doing", and it reads what is
	// already fetched rather than fetching: the answer is as fresh as the reader's last fetch,
	// which a caller has to say out loud rather than implying it is live.
	//
	// limit is the backend's to apply, not the caller's to trim afterwards, so a backend can push
	// it down to the ref listing and never materialise a diff it was going to discard.
	BranchChanges(ctx context.Context, dir, base string, limit int) ([]BranchChange, error)
}

// RangeDiffReporter is an optional capability for VCSDriver implementations that can produce the
// unified diff of a committed revision range, which is the half of review DirtyDiff cannot reach.
//
// Callers type-assert for it and degrade gracefully. Degrading here means REFUSING, not answering
// empty: a working tree with no changes is a real clean answer, but a range magus cannot diff is a
// gap, and rendering a gap as an empty changeset would report a colleague's branch as untouched.
type RangeDiffReporter interface {
	// RangeDiff returns the unified diff from base to head, both backend-native revision names.
	//
	// Symmetric difference, not a two-dot comparison: the answer is what head added since it
	// diverged, never what base gained meanwhile. A reviewer asking about a branch is asking what
	// its author did, and two-dot would charge them for every commit that landed on the base while
	// they were not looking.
	//
	// Reads what the repository already has and never fetches, matching BranchChanges. An
	// unresolvable revision is an error rather than an empty diff, because the two read identically
	// to a caller and only one of them means "nothing changed".
	RangeDiff(ctx context.Context, dir, base, head string) (string, error)
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
// conflicted path inside its own index manipulation, so cost scales with the conflict
// count and a regeneration cannot run there. Deciding every path first, regenerating
// once, then staging inverts that, and is the only way to settle ConflictKindDeleted,
// which a driver is never called for.
//
// Callers type-assert and degrade to "resolve by hand" when a backend lacks it.
//
// Every method takes the repository root and root-relative slash paths: a VCS reports
// conflict paths from the top level but reads pathspecs from the process directory, so
// mixing the two addresses the wrong files instead of failing.
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

// RevisionFileReader is an optional capability for VCSDriver implementations that can read
// ONE file's content at a revision, without materializing a tree.
//
// Sibling of RevisionExporter and deliberately narrower: exporting a whole revision to read
// a single magusfile costs the size of the repository to answer a question about one file.
// `magus vcs resolve` reads the committed magusfile this way when the merge in progress left
// conflict markers in the working copy, which is the only version of it guaranteed to parse.
//
// Callers type-assert and degrade when a backend lacks it, like every other capability
// here - though every backend magus ships does implement it.
type RevisionFileReader interface {
	// ReadFileAt returns the content of a root-relative slash path at rev, EXACTLY as the
	// revision holds it: no trimming, and a trailing newline is part of the file.
	//
	// rev is a backend-native revision expression. Empty means "the committed revision",
	// which each backend spells its own way - HEAD for git, `.` for hg and Sapling, `@`
	// for jj - so a caller that wants the committed side passes "" rather than picking a
	// spelling that is only correct for one of them.
	//
	// A path absent at that revision is an error, not empty content: the caller cannot
	// tell those apart otherwise.
	ReadFileAt(ctx context.Context, root, rev, path string) (string, error)
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

// MergeStarter is an optional capability for VCSDriver implementations that can BEGIN a
// merge against a ref without committing it, and abandon one they began. Callers
// type-assert for it and degrade gracefully when a backend lacks it.
//
// It exists because conflict resolution was reactive-only: ConflictResolver.Conflicts
// reads an operation already in progress, so settling a branch against its base meant the
// caller ran the merge itself first. That put the tricky half - which merge, with what
// flags, and how to back out - in whatever shell script was driving, which is exactly
// where CI has no good place to put it.
type MergeStarter interface {
	// StartMerge begins a merge of ref into the working tree WITHOUT committing it,
	// leaving any conflicts recorded for ConflictResolver.Conflicts to report. A merge
	// that completes cleanly is not an error, and neither is one that conflicts: both
	// leave an operation in progress for the caller to conclude. A merge that could not
	// be started at all (an unknown ref, an operation already underway) is.
	StartMerge(ctx context.Context, root, ref string) error
	// AbortMerge abandons the in-progress merge, restoring the tree to its pre-merge
	// state. Callers start from a clean tree so this cannot discard uncommitted work.
	AbortMerge(ctx context.Context, root string) error
}

// Status is the working tree's uncommitted state: whether it is clean, and which paths
// changed. It replaces the pair of vcs.is_dirty / vcs.dirty_files at the Buzz boundary,
// where the two answered the same question in two shapes - a bool and a list of the
// backend's own status lines, which a caller had to parse differently per VCS.
//
// Files are Paths, not strings, and each carries the repository root as its base: a VCS
// reports paths relative to the root, while a target runs with its cwd set to its PROJECT
// directory. Handing back bare strings made that difference invisible and left every
// caller to rediscover it.
type Status struct {
	// Clean reports whether the tree has no uncommitted changes. It is len(Files) == 0,
	// named so a gate reads as a gate.
	Clean bool
	// Files are the changed paths, empty when Clean. Paths only: a per-entry status code
	// is not portable (jj's diff --name-only reports none at all), and Commit's rule
	// applies - a concept one backend lacks is not modeled here. Reach for vcs.exe() when
	// the codes matter.
	Files []Path
}

// BuzzObject is the Buzz boundary map vcs.status returns: {clean, files}.
func (s Status) BuzzObject() BuzzObject {
	files := make([]any, 0, len(s.Files))
	for _, f := range s.Files {
		files = append(files, f.BuzzObject())
	}
	return BuzzObject{"clean": s.Clean, "files": files}
}

// StatusRecord is the boundary mirror of the object vcs.status returns; the Buzz
// `object Status` mirror is generated from it by cmd/magus-utils types. See
// CommitRecord for why the mirror exists separately from the type it mirrors.
type StatusRecord struct {
	Clean bool
	Files []Path
}

// DriftResult is why a generate gate's declared outputs drifted: not merely THAT they
// did, which a status call already answers, but which of three causes it was. A gate
// fires when its reader is looking at a CI log rather than the tree, so the verdict
// carries the diagnostic code, the sentence to print, its explainer URL, and the files.
//
// It lives in types (not std) for the same reason Commit does: the shape crosses the Buzz
// boundary, so it needs a mirror. The FUNCTION that produces it belongs to the magus
// module rather than vcs - deciding that unchanged inputs plus a dev build means MGS4005
// is magus policy, and vcs only supplies the dirty-file probe underneath it.
type DriftResult struct {
	// Drifted is false with every other field zero when the outputs are clean, so a
	// caller reads the fields unconditionally instead of testing for absent keys.
	Drifted bool
	// Code is the MGS diagnostic: which of the three causes this was.
	Code string
	// Message is the sentence to show, already naming the remedy.
	Message string
	// URL is Code's explainer page.
	URL string
	// Files are the drifted outputs, based at the repository root like Status.Files.
	Files []Path
}

// BuzzObject is the Buzz boundary map magus.diagnoseDrift returns.
func (d DriftResult) BuzzObject() BuzzObject {
	files := make([]any, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, f.BuzzObject())
	}
	return BuzzObject{"drifted": d.Drifted, "code": d.Code, "message": d.Message, "url": d.URL, "files": files}
}

// ClassifyDrift names the cause of a declared output that moved, and the sentence to
// show for it - the one place that fork is decided, so the generate gate and
// `magus vcs add` cannot describe one condition two ways.
//
// The fork is on WHY, not on how bad it is:
//
//   - a declared input moved too (MGS4006): regeneration is expected, and the output
//     belongs in the same commit as the source that moved it;
//   - inputs unchanged, running a DEV build (MGS4005): the committed form is produced by
//     the pinned release, so this is version skew and not the developer's change;
//   - inputs unchanged, running a RELEASE build (MGS4003): same inputs, same generator
//     version, different bytes - a reproducibility bug.
//
// magusVersion is the running binary's version, empty when unknown.
func ClassifyDrift(inputDirty bool, magusVersion string) (DiagnosticCode, string) {
	switch {
	case inputDirty:
		return StaleGeneratedOutput,
			"generated output drifted and a declared input changed; re-run `magus run generate:rw` and commit"
	case IsDevMagusVersion(magusVersion):
		ver := magusVersion
		if ver == "" {
			ver = "unknown"
		}
		return EnvironmentalDrift, fmt.Sprintf("generated output drifted but its declared inputs are unchanged; "+
			"the committed form is produced by the pinned release and you are running a dev build (%s) - "+
			"not your change, do not commit", ver)
	default:
		return NondeterministicOutput,
			"generated output drifted but its declared inputs and the generator version are unchanged - a non-deterministic generator"
	}
}

// SplitExplainedOutputs divides the dirty declared outputs into the ones this change
// accounts for and the ones it does not.
//
// It backs both `magus vcs add` and doctor's generated-drift check. Classifying by
// declared GLOB alone answers "is this path generated" and nothing else, so `vcs add`
// staged whatever bytes sat at an output path while claiming to stage "the generated
// outputs a source change produced" - a causal claim it never checked.
//
// An output is EXPLAINED when some project whose output glob claims it also has a dirty
// declared SOURCE in this change. That is MGS4006's shape. An output with no dirty input
// behind it is the other branch: a different magus version produced the committed form
// (MGS4005), or a generator is not deterministic (MGS4003).
//
// ROLE, not the raw glob sets, decides what counts as a dirty input: a committed
// generated file frequently also matches its project's source globs, and letting that
// count would make every such file explain itself. FileEntry.Role reports "output"
// whenever both match, for exactly this reason.
func SplitExplainedOutputs(files []FileEntry, alsoChangedIn map[string]bool) (explained, unexplained []string) {
	inputMoved := SourceProjects(files)
	// A source change that is already COMMITTED explains its output just as well as a
	// dirty one. Reading only the working tree missed both of the ordinary ways this
	// happens - committing the source and then the generated output, and pulling and then
	// regenerating - so magus reported drift it had itself just accounted for, and the
	// only way past it was to override the check on a legitimate commit. A check people
	// learn to override has stopped working.
	for p := range alsoChangedIn {
		inputMoved[p] = true
	}
	for _, f := range files {
		if f.Role != "output" {
			continue
		}
		if slices.ContainsFunc(f.OutputOf, func(p string) bool { return inputMoved[p] }) {
			explained = append(explained, f.Path)
			continue
		}
		unexplained = append(unexplained, f.Path)
	}
	return explained, unexplained
}

// SourcesChangedSinceBase returns every project whose declared SOURCE changed in THIS
// CHANGE - the branch measured against its base ref, not the working tree.
//
// Base-relative is the whole point, and the working tree cannot substitute for it: on a CI
// runner the checkout is clean, so a dirtiness probe reports nothing and would report every
// author innocent of everything. A source change that is already COMMITTED explains its
// output exactly as well as an uncommitted one.
//
// nil means COULD NOT TELL - no VCS, no base, an unreadable diff - and is not the same
// answer as "nothing changed". A caller deciding whether to blame someone must treat it as
// no evidence rather than as evidence of innocence.
//
// One function for the three callers that ask this (the engine's drift gate, `magus vcs
// add`, and doctor's generated-drift check), because they were three copies of it and a
// staging decision that disagreed with a gate would be invisible until it mattered.
func SourcesChangedSinceBase(ctx context.Context, insp Inspector, res VCSResolution, root string) map[string]bool {
	if insp == nil || res.VCS == nil || res.Base == "" {
		return nil
	}
	paths, err := res.VCS.ChangedFiles(ctx, root, res.Base)
	if err != nil || len(paths) == 0 {
		return nil
	}
	files, err := insp.ClassifyFiles(ctx, paths)
	if err != nil {
		return nil
	}
	return SourceProjects(files)
}

// SourceProjects returns every project whose declared SOURCE glob claims one of these
// paths. It is how both halves of the explained/unexplained question are computed - the
// dirty set and the changed-since-base set - so the two cannot answer it differently.
func SourceProjects(files []FileEntry) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		if f.Role != "source" {
			continue
		}
		for _, p := range f.SourceOf {
			out[p] = true
		}
	}
	return out
}

// StagingPlan is what `magus vcs add` decided about a working tree, as a value.
//
// A value rather than a pile of Printlns because the same decision has several
// audiences: the terminal, `-o json`, and (next) a pre-commit hook that needs the
// verdict as an exit status rather than as prose. Every one of those used to mean
// another hand-written rendering of the same four groups, which is how `-o json` came to
// be accepted and silently answer in text.
type StagingPlan struct {
	// Sources and Outputs are what staging claimed: declared sources, and the declared
	// outputs a source change in this same set accounts for.
	Sources []string `json:"sources,omitempty" yaml:"sources,omitempty"`
	Outputs []string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	// Unexplained are declared outputs that moved with no dirty declared input behind
	// them. Skipped unless named explicitly; Code says why they are suspect.
	Unexplained []string `json:"unexplained,omitempty" yaml:"unexplained,omitempty"`
	// Undeclared is what no project claims, and Maintained the subset magus's own core
	// writes outside any target's globs (.gitattributes).
	Undeclared []string `json:"undeclared,omitempty" yaml:"undeclared,omitempty"`
	Maintained []string `json:"maintained,omitempty" yaml:"maintained,omitempty"`
	// Staged is what actually reached the index, empty on a dry run.
	Staged []string `json:"staged" yaml:"staged"`
	// Code, Message and URL classify Unexplained via ClassifyDrift, and are empty when
	// nothing is unexplained.
	Code    string `json:"code,omitempty" yaml:"code,omitempty"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
	URL     string `json:"url,omitempty" yaml:"url,omitempty"`
}

// VCSCheckpoint is the identity of the working state a piece of work was handed:
// what was true in this tree, at this moment, in a form a ledger can record and a
// later reader can act on.
//
// It RESOLVES AND RECORDS; it never MINTS. No tag, no stash, no ref, no file - a
// checkpoint is a pure read, so nothing about it can be lost by not keeping it and
// nothing about the tree changes by taking it. That is what makes it safe to take
// one per delegation, and it is why this is a plain value with no id of its own:
// an identity magus invented would be a fact only magus could confirm.
//
// Two things a reader can do with it. Revision feeds anything that takes a rev
// (`magus graph diff --rev <rev>`). PatchDigest compares against another worker's:
// equal digests mean the two saw the same uncommitted patch, which no revision alone
// can tell you, because a dirty tree's revision is the same one everybody else has.
type VCSCheckpoint struct {
	// Revision is the resolved head revision id (VCSMeta.ID): git SHA, hg node, jj
	// commit_id. Full, never abbreviated - it is meant to be fed back to a VCS.
	Revision string `json:"revision" yaml:"revision"`
	// Branch is the movable name pointing at Revision, or empty where the backend has
	// none. VCSMeta.Ref's value under the name a caller recording a handoff writes;
	// empty is ordinary (a detached HEAD, or jj's usual anonymous change), not an error.
	Branch string `json:"branch,omitempty" yaml:"branch,omitempty"`
	// Dirty reports uncommitted changes, on the vcs package's existing definition
	// (VCSMeta.IsDirty, which for git is a non-empty `status --porcelain` and so counts
	// untracked files too).
	Dirty bool `json:"dirty" yaml:"dirty"`
	// PatchDigest fingerprints the uncommitted patch, empty when the tree is clean.
	//
	// It covers exactly what the backend's DirtyDiff covers, which is TRACKED content
	// against the checked-out revision. Untracked files make Dirty true and leave no
	// mark here, so two trees differing only in untracked files share a digest. Stated
	// rather than papered over: a digest that silently changed with build residue would
	// answer "did we see the same tree" wrong in the commoner direction.
	PatchDigest string `json:"patch_digest,omitempty" yaml:"patch_digest,omitempty"`
	// VCS is the resolved backend name (git, hg, jj), so a reader knows whose revision
	// syntax Revision is written in.
	VCS string `json:"vcs,omitempty" yaml:"vcs,omitempty"`
}

// DriftResultRecord is the boundary mirror cmd/magus-utils types reflects over; see
// CommitRecord for why it is separate from the type it mirrors.
type DriftResultRecord struct {
	Drifted bool
	Code    string
	Message string
	URL     string
	Files   []Path
}
