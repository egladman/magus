//go:build !wasm

package std

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

//go:generate go run ../cmd/magus-utils bindings -module vcs -lang buzz -out ../internal/interp/bindings/gen/vcs.go

func init() { Register(Vcs) }

// Vcs is the "vcs" host module: version-control queries for the current working tree.
var Vcs = Module{
	Name: "vcs",
	Doc:  "Version-control queries for the current working tree.",
	Methods: []Method{
		{
			Name:    "name",
			Doc:     "VCS short name (e.g. \"git\"). Empty if unresolved, which is how a caller tests for a VCS without catching.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsName,
		},
		{
			Name: "base",
			Doc:  "Resolved base ref for diffs. dir resolves it for the repository holding that directory (relative to the target's cwd) instead of the one holding the cwd, so it names that repository's VCS default. Raises only when dir does not exist.",
			Args: []Arg{
				{Name: "dir", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    VcsBase,
		},
		{
			Name:    "root",
			Doc:     "Absolute path of the repository root.",
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    VcsRoot,
		},
		{
			Name: "changed_files",
			Doc:  "The files changed against the given base (defaults to the base vcs.base resolves for dir), each a Path carrying the repository root as its base. dir reads the repository holding that directory (relative to the target's cwd) instead of the one holding the cwd; a dir that does not exist raises. Empty when no VCS is resolved. Named for what it returns: it answers WHICH files a branch touched, where vcs.dirtyDiff answers WHAT changed inside the working tree.",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
				{Name: "dir", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Path]"}},
			Raises:  true,
			Impl:    VcsChangedFiles,
		},
		{
			Name: "regions",
			Doc:  "The declarations the change against base (defaults to vcs.base) lands in: one {file, side, lines, declaration, driver} per declaration each hunk touches, ordered by path, then side, then line, for the same files vcs.changedFiles lists. side is `old` for lines only the merge base's version has (a deletion) and `new` for the working tree's; lines is the first and last line on that side, 1-based and inclusive; declaration is the enclosing declaration's line as the file's diff driver matched it (`func (m *Magus) run(ctx context.Context) error {`), empty above a file's first declaration; driver is that diff driver (`golang`, `markdown`, `buzz`), empty for a file with none, whose regions then say only which lines changed. It is the footprint `magus job wait` prints. Empty when no VCS is resolved; raises when the backend cannot place regions (only git can) or the diff cannot be computed, since an empty footprint reads as a change that touched nothing.",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[RegionChange]"}},
			Raises:  true,
			Impl:    VcsRegions,
		},
		{
			Name: "ref",
			Doc:  "The movable name pointing at the current revision, or null when none names it: a detached git HEAD, or jj's working copy, which is usually an anonymous change, so null is an ordinary answer there, not a failure. Backend-specific by nature: a git branch, a Mercurial named branch, a Jujutsu bookmark. dir reads the repository holding that directory (relative to the target's cwd) instead of the one holding the cwd. Raises when no VCS is resolved, its metadata cannot be read, or dir does not exist - use vcs.name() to test for a VCS first.",
			Args: []Arg{
				{Name: "dir", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeString, Nullable: true}},
			Raises:  true,
			Impl:    VcsRef,
		},
		{
			Name: "status",
			Doc:  "The working tree's uncommitted state as {clean, files}: clean is true when nothing changed, files are the changed paths (empty when clean). Pass paths to scope it. Each file is a Path carrying the repository root as its base, because a VCS reports paths from the root while a target runs in its project directory. Paths only - a per-entry status code is not portable (jj reports none), so reach for vcs.cmd() when the codes matter.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "Status"}},
			Raises:  true,
			Impl:    VcsStatus,
		},
		{
			Name: "is_dirty",
			Doc:  "True if the working tree has uncommitted changes. Pass paths to scope the check to those files/dirs (relative to the project), e.g. is_dirty([\"MAGUS.md\"]) - the right way to gate generated outputs without shelling out to git or parsing porcelain.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeBool}},
			Raises:  true,
			Impl:    VcsIsDirty,
		},
		{
			Name: "dirty_diff",
			Doc:  "The uncommitted changes to paths, as the active VCS's own unified diff; \"\" when nothing changed or no VCS is resolved. is_dirty answers whether an output moved, this answers how - which is what a drift gate needs when it fires in CI and nobody can look at the tree. Every backend implements it, so a magusfile no longer branches on vcs.name() to print a diff; the bytes are the backend's native format, not a normalized one.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsDirtyDiff,
		},
		{
			Name: "commit",
			Doc:  "Resolve a revision (a VCS-native rev expression; omit for the current revision) to its commit object: {id, short, author {name, email}, date, subject, body, parents, files}. id is the content/revision id (git SHA, hg node, jj commit_id); date is RFC3339 in the committer's own offset, when the revision was recorded. files stays empty here, meaning not asked; vcs.history fills it. Every field is meaningful for every VCS. Raises when no VCS is resolved or the revision cannot be looked up, so a caller never has to sniff a field to find out - use vcs.name() to test for a VCS, and try/catch for a revision that may not exist.",
			Args: []Arg{
				{Name: "rev", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "Commit"}},
			Raises:  true,
			Impl:    VcsCommit,
		},
		{
			Name: "history",
			Doc:  "Up to limit commits reachable from the current revision, newest first, in one VCS call however many there are; each is the object vcs.commit returns, with files set to the repository-relative paths it changed against its first parent (a rename is both paths). limit defaults to 10; 0 means every commit. paths keeps only the commits that changed one of those literal repository-relative paths (a directory keeps what is under it) and narrows each commit's files to them. first_parent follows only the first parent of a merge, the line a branch landed on. An empty list when no VCS is resolved.",
			Args: []Arg{
				{Name: "limit", Type: TypeInt, Optional: true, Default: 10},
				{Name: "paths", Type: TypeStringSlice, Optional: true},
				{Name: "first_parent", Type: TypeBool, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Commit]"}},
			Raises:  true,
			Impl:    VcsHistory,
		},
		{
			Name: "cmd",
			Doc:  "Escape hatch: run the active VCS binary (git/hg/sl/jj) with args, for something no method covers. Same result and raise semantics as magus.cmd and proc.exec - returns {stdout, stderr, code, ok} and raises on a non-zero exit unless opts.allow_failure. opts.dir runs it elsewhere (relative to the target's cwd, unlike proc.exec's positional dir); opts.quiet captures the output without echoing it to the console. This is VCS-AGNOSTIC only in that magus picks the binary; the args are the backend's own, so branch on vcs.name() when they differ. Raises when no VCS is resolved, rather than running nothing and reporting success.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    VcsCmd,
		},
		{
			Name: "tags",
			Doc:  "Repository tags, newest first. Each is an object {name, date, id}: name as written (\"v0.3.0\", no refs/tags/ prefix), date RFC3339 (empty when the VCS reported none), id the revision it resolves to. pattern is a glob over the name (\"v*\"); wildcards stop at \"/\", so \"v*\" selects releases and skips a namespaced tag like backup/x. Omit it to list every tag. Empty when no VCS is resolved or the backend has no tags (jj); a failed query raises rather than reporting \"no tags\". Note a shallow or single-branch clone legitimately fetches no tags, so an empty list still means \"none present here\", not \"none exist\".",
			Args: []Arg{
				{Name: "pattern", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Tag]"}},
			Raises:  true,
			Impl:    VcsTags,
		},
		{
			Name:    "describe",
			Doc:     "Human-readable version string from the nearest tag (git's `describe --tags --always --dirty`: tag, else short hash, with a -dirty suffix for a modified tree). \"\" when no VCS is resolved, or for a backend without a tag-describe concept (jj) - so a magusfile stamps a version without shelling out to git. Pair with vcs.commit().short as a fallback.",
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    VcsDescribe,
		},
	},
}

// vcsState caches the resolved VCS for the current cwd. Re-resolves when cwd
// changes, mirroring the per-registration resolution the hand-written
// binding did before. Package-level state is acceptable here because cwd is
// already process-global (chdirMu in runtime.go serializes mutations).
var (
	vcsMu     sync.Mutex
	vcsCwdKey string
	vcsCached types.VCSDriver
	vcsBase   string
)

func resolveVCS(ctx context.Context) (types.VCSDriver, string) {
	wd, err := EffectiveCwd(ctx)
	if err != nil {
		wd = "."
	}
	v, base, _ := resolveVCSAt(ctx, wd)
	return v, base
}

// resolveVCSAt resolves the driver and base ref for the repository holding wd, sharing
// resolveVCS's one-entry cache.
func resolveVCSAt(ctx context.Context, wd string) (types.VCSDriver, string, error) {
	vcsMu.Lock()
	defer vcsMu.Unlock()
	if wd == vcsCwdKey {
		return vcsCached, vcsBase, nil
	}
	res, err := vcs.Resolve(ctx, wd, "", types.VCSOptions{})
	if err != nil {
		// A resolve failure (transient error, ctx cancellation) is not "no VCS": do
		// not poison the cache for this cwd with it, or every later call in the
		// process would replay this one failure forever.
		return nil, "", err
	}
	vcsCwdKey = wd
	if res.VCS == nil {
		vcsCached, vcsBase = nil, ""
		return nil, "", nil
	}
	vcsCached = res.VCS
	vcsBase = res.Base
	return vcsCached, vcsBase, nil
}

// vcsAt is the driver, base ref and directory a vcs call taking a dir argument reads: the
// repository holding dir, or for "" the one holding the target's cwd. A dir that does not
// exist is an error rather than a fall back to the cwd, which would answer for a different
// checkout than the caller named.
func vcsAt(ctx context.Context, method, dir string) (types.VCSDriver, string, string, error) {
	if dir == "" {
		v, base := resolveVCS(ctx)
		return v, base, vcsDir(ctx), nil
	}
	wd := resolveDir(ctx, dir)
	if err := checkRead(ctx, wd); err != nil {
		return nil, "", "", err
	}
	info, err := os.Stat(wd)
	if err != nil {
		return nil, "", "", fmt.Errorf("vcs.%s: dir %q: %w", method, dir, err)
	}
	if !info.IsDir() {
		return nil, "", "", fmt.Errorf("vcs.%s: dir %q is not a directory", method, dir)
	}
	v, base, err := resolveVCSAt(ctx, wd)
	if err != nil {
		return nil, "", "", fmt.Errorf("vcs.%s: resolve the VCS holding %s: %w", method, wd, err)
	}
	return v, base, wd, nil
}

// VcsName returns the active VCS short name (e.g. "git"), or "" if unresolved.
// Resolution is per call and honors the call's cancellation; resolveVCS caches
// on cwd, so repeated reads cost a mutex rather than a probe.
func VcsName(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	return v.Name(), nil
}

// VcsBase returns the base ref resolved for the repository holding dir, "" meaning the
// target's cwd; it raises only for a dir that does not exist.
func VcsBase(ctx context.Context, dir string) (string, error) {
	_, base, _, err := vcsAt(ctx, "base", dir)
	return base, err
}

// VcsRoot returns the absolute path of the repository root, or "" if unresolved.
func VcsRoot(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	root, err := v.Root(ctx, vcsDir(ctx))
	if err != nil {
		return "", fmt.Errorf("vcs.root: %w", err)
	}
	return root, nil
}

// VcsChangedFiles lists files changed against base, defaulting to the base ref resolved
// for the repository holding dir; dir "" is the target's cwd.
//
// Paths carry the repository root as their base: a VCS reports diff paths from the root
// while a target runs in its project directory.
//
// The probe runs where the driver was resolved (vcsAt). Running it at the PROCESS cwd
// instead would resolve the driver from one directory and run it in another, identical
// only while both sit in the same repository.
func VcsChangedFiles(ctx context.Context, base, dir string) ([]types.Path, error) {
	v, defaultBase, dir, err := vcsAt(ctx, "changedFiles", dir)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	if base == "" {
		base = defaultBase
	}
	files, err := v.ChangedFiles(ctx, dir, base)
	if err != nil {
		return nil, fmt.Errorf("vcs.diff: %w", err)
	}
	root, err := v.Root(ctx, dir)
	if err != nil {
		root = dir
	}
	out := make([]types.Path, 0, len(files))
	for _, f := range files {
		out = append(out, types.Path{Value: f, Base: root})
	}
	return out, nil
}

// VcsRegions refines the files VcsChangedFiles lists into the declarations their changed
// lines land in, through the driver's Regions: the one implementation the job store's
// footprint reads, so a script and `magus job wait` never disagree about a declaration.
func VcsRegions(ctx context.Context, base string) ([]types.RegionChange, error) {
	v, defaultBase := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	if base == "" {
		base = defaultBase
	}
	dir := vcsDir(ctx)
	root, err := v.Root(ctx, dir)
	if err != nil {
		return nil, types.WrapDiagnostic(types.VCSUnavailable, err, "find the %s repository root", v.Name())
	}
	paths, err := v.ChangedFiles(ctx, dir, base)
	if err != nil {
		return nil, fmt.Errorf("vcs.regions: %w", err)
	}
	files := make([]types.FileChange, 0, len(paths))
	for _, p := range paths {
		files = append(files, types.FileChange{Path: p})
	}
	regions, err := v.Regions(ctx, root, base, files)
	if err != nil {
		return nil, fmt.Errorf("vcs.regions: %w", err)
	}
	return regions, nil
}

// vcsDir is the directory every vcs probe runs in: the target's, not the process's.
//
// runBuzz deliberately does NOT os.Chdir (it carries the target's directory on the
// context so projects can execute concurrently without corrupting a shared process cwd),
// so passing "" means the PROCESS cwd, a different place. resolveVCS already picks the
// driver from EffectiveCwd.
//
// Harmless while both sit in the same repository. Not harmless in the server, where the
// process cwd belongs to the server and the context cwd comes from the request.
func vcsDir(ctx context.Context) string {
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		return ""
	}
	return dir
}

// VcsRef returns the movable name at the current revision (a git branch, an hg named
// branch, a jj bookmark) of the repository holding dir, "" meaning the target's cwd, or
// nil when none names it; raises when no VCS or metadata is available, or dir does not
// exist.
func VcsRef(ctx context.Context, dir string) (*string, error) {
	v, _, dir, err := vcsAt(ctx, "ref", dir)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, types.DiagnosticErrorf(types.VCSUnavailable, "no VCS resolved for this workspace; use vcs.name() to test before asking for commit metadata")
	}
	ref, err := v.Ref(ctx, dir)
	if err != nil {
		return nil, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s metadata", v.Name())
	}
	var name *string
	if ref != "" {
		name = &ref
	}
	return name, nil
}

// VcsStatus reports the working tree's uncommitted state as a typed Status.
//
// Handing a magusfile the backend's own status lines (git porcelain, hg status, jj diff
// --name-only) would make every caller reimplement the parsing and know which VCS it is on.
// DirtyFiles answers in paths, so there is nothing here to reimplement.
//
// Paths carry the repository root as their base: a VCS reports from the root while a
// target's cwd is its PROJECT directory, so a bare string was ambiguous exactly when a
// project was not the root.
//
// RAISES on a failed probe rather than reporting clean: a gate that cannot read the tree
// has no answer, and a quiet empty list would let it pass having checked nothing.
func VcsStatus(ctx context.Context, paths []string) (types.Status, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return types.Status{Clean: true}, nil
	}
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
	}
	dirty, err := v.DirtyFiles(ctx, dir, paths)
	if err != nil {
		return types.Status{}, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s status", v.Name())
	}
	root, err := v.Root(ctx, dir)
	if err != nil {
		root = dir
	}
	files := make([]types.Path, 0, len(dirty))
	for _, p := range dirty {
		files = append(files, types.Path{Value: p, Base: root})
	}
	return types.Status{Clean: len(files) == 0, Files: files}, nil
}

// VcsIsDirty reports whether the working tree has uncommitted changes.
func VcsIsDirty(ctx context.Context, paths []string) (bool, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return false, nil
	}
	// Run the probe in the project's working directory (set via WithCwd for spell
	// targets, the process cwd for magusfile targets) so pathspecs resolve against
	// the project, not wherever the process happens to be.
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
	}
	dirty, err := v.Dirty(ctx, dir, paths)
	if err != nil {
		// RAISE. This is the drift-gate primitive: `is_dirty(["MAGUS.md"])` is how a
		// generate target asks "did my output change?". Reporting false when the probe
		// FAILED answers "clean" to a question that was never actually asked, so the gate
		// passes having checked nothing: the one outcome a gate must never produce
		// silently. No VCS at all is still false above; that is a known state, not a
		// failed probe.
		return false, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s status", v.Name())
	}
	return dirty, nil
}

// VcsDirtyDiff returns the working tree's uncommitted diff for paths. Unlike VcsIsDirty
// this does NOT raise on a failed probe: it is a diagnostic printed beside a failure that
// has already been decided, so a backend that cannot produce a diff must not become the
// reason the build fails. "" reads as "no diff to show".
func VcsDirtyDiff(ctx context.Context, paths []string) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
	}
	diff, err := v.DirtyDiff(ctx, dir, paths)
	if err != nil {
		return "", nil //nolint:nilerr // deliberate: a diagnostic must not become the failure
	}
	return diff, nil
}

// VcsCommit resolves rev (empty = current revision) to its commit object. It
// RAISES when no VCS is resolved and RAISES when the revision can't be looked
// up: a caller uses vcs.name() to test for a VCS first, and try/catch for a
// revision that may not exist.
func VcsCommit(ctx context.Context, rev string) (types.Commit, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return types.Commit{}, types.DiagnosticErrorf(types.VCSUnavailable, "no VCS resolved for this workspace; use vcs.name() to test before looking up a commit")
	}
	c, err := v.FindCommit(ctx, vcsDir(ctx), rev)
	if err != nil {
		which := rev
		if which == "" {
			which = "the current revision"
		}
		return types.Commit{}, types.WrapDiagnostic(types.VCSUnavailable, err, "look up %s in %s", which, v.Name())
	}
	return c, nil
}

// VcsHistory returns the commits the query selects (newest first) as objects, or an
// empty list when no VCS is resolved. It RAISES when the query fails: an empty
// list there would read as "no history" for "could not read history".
func VcsHistory(ctx context.Context, limit int, paths []string, firstParent bool) ([]types.Commit, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	q := types.HistoryQuery{Limit: limit, Paths: paths, FirstParent: firstParent}
	commits, err := v.History(ctx, vcsDir(ctx), q)
	if err != nil {
		return nil, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s history", v.Name())
	}
	return commits, nil
}

// VcsDescribe returns a human-readable version string from the nearest tag (see
// the driver Describe methods), or "" when no VCS is resolved or the backend has
// no describe concept. It RAISES when the query fails: "" is reserved for the
// two no-op cases above, not for a probe that could not run.
func VcsDescribe(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	out, err := v.Describe(ctx, vcsDir(ctx))
	if err != nil {
		return "", types.WrapDiagnostic(types.VCSUnavailable, err, "describe %s revision", v.Name())
	}
	return out, nil
}

// VcsTags returns the repository's tags newest-first, filtered by pattern. An
// empty list when no VCS is resolved; a failed query is returned, not swallowed.
func VcsTags(ctx context.Context, pattern string) ([]types.VCSTag, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	// Errors propagate, deliberately breaking with the metadata accessors above.
	// Those return "" for a failed query because a magusfile reading vcs.ref()
	// outside a repo wants a blank, not an exception. Tags differ: resolveVCS
	// already covers "no VCS", a repository with no tags exits 0 with no output,
	// so a non-nil error here is a real fault: git missing, not a repository, or
	// a malformed pattern. Swallowing it would report "no releases" for "could not
	// read releases", which is exactly the confusion a release page must not make.
	return v.Tags(ctx, vcsDir(ctx), pattern)
}

// vcsExe returns the absolute path of the active VCS executable, or "" when
// unresolved or not on PATH. Internal: the Buzz surface exposes vcs.cmd, which runs the
// binary, rather than a path for the caller to hand to proc.exec themselves.
func vcsExe(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	path, err := run.LookPath(ctx, v.Name())
	if err != nil {
		return "", types.WrapDiagnostic(types.ToolNotOnPath, err, "%s is the resolved VCS but is not on PATH", v.Name())
	}
	return path, nil
}

// VcsCmd runs the active VCS binary with args.
//
// This replaced vcs.exe, which handed back a PATH and left every caller to write
// proc.exec(<the vcs binary>, [...]): two calls, and a silent no-op when the path came back
// empty because no VCS was resolved. Returning an ExecResult also puts the escape hatch
// on the same typed footing as magus.cmd and proc.exec instead of a bare string. "exe" was
// the wrong word besides: it reads as a Windows file extension, and the value is a
// binary on every platform magus runs.
func VcsCmd(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	bin, err := vcsExe(ctx)
	if err != nil {
		return types.ExecResult{}, err
	}
	if bin == "" {
		return types.ExecResult{}, types.DiagnosticErrorf(types.VCSUnavailable,
			"vcs.cmd: no VCS is resolved for this directory, so there is no binary to run")
	}
	return runResult(ctx, bin, args, resolveDir(ctx, optStringDefault(opts, "dir", "")), "vcs.cmd", bin, opts)
}
