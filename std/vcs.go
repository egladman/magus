//go:build !wasm

package std

import (
	"context"
	"fmt"
	"os/exec"
	"sync"

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
			Name:    "base",
			Doc:     "Resolved base ref for diffs.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsBase,
		},
		{
			Name:    "root",
			Doc:     "Absolute path of the repository root.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsRoot,
		},
		{
			Name: "changed_files",
			Doc:  "The files changed against the given base (defaults to vcs.base), each a Path carrying the repository root as its base. Empty when no VCS is resolved. Named for what it returns: it answers WHICH files a branch touched, where vcs.dirtyDiff answers WHAT changed inside the working tree.",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Path]"}},
			Impl:    VcsChangedFiles,
		},
		{
			Name:    "ref",
			Doc:     "The movable name pointing at the current revision, or \"\" when there is none. Backend-specific by nature: a git branch, a Mercurial named branch, a Jujutsu bookmark. jj's working copy is usually an anonymous change, so \"\" is an ordinary answer there, not a failure. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsRef,
		},
		{
			Name: "status",
			Doc:  "The working tree's uncommitted state as {clean, files}: clean is true when nothing changed, files are the changed paths (empty when clean). Pass paths to scope it. Each file is a Path carrying the repository root as its base, because a VCS reports paths from the root while a target runs in its project directory. Paths only - a per-entry status code is not portable (jj reports none), so reach for vcs.cmd() when the codes matter.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "Status"}},
			Impl:    VcsStatus,
		},
		{
			Name: "is_dirty",
			Doc:  "True if the working tree has uncommitted changes. Pass paths to scope the check to those files/dirs (relative to the project), e.g. is_dirty([\"MAGUS.md\"]) - the right way to gate generated outputs without shelling out to git or parsing porcelain.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeBool}},
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
			Doc:  "Resolve a revision (a VCS-native rev expression; omit for the current revision) to its commit object: {id, short, author {name, email}, date, subject, body, parents}. id is the content/revision id (git SHA, hg node, jj commit_id); date is RFC3339, when the revision was recorded. Every field is meaningful for every VCS. Raises when no VCS is resolved or the revision cannot be looked up, so a caller never has to sniff a field to find out - use vcs.name() to test for a VCS, and try/catch for a revision that may not exist.",
			Args: []Arg{
				{Name: "rev", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "Commit"}},
			Impl:    VcsCommit,
		},
		{
			Name: "history",
			Doc:  "Up to limit recent commits, newest first; each is the same object vcs.commit returns. limit defaults to 10 when omitted. An empty list when no VCS is resolved.",
			Args: []Arg{
				{Name: "limit", Type: TypeInt, Optional: true, Default: 10},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Commit]"}},
			Impl:    VcsHistory,
		},
		{
			Name: "cmd",
			Doc:  "Escape hatch: run the active VCS binary (git/hg/jj) with args, for something no method covers. Same result and raise semantics as magus.cmd and os.exec - returns {stdout, stderr, code, ok} and raises on a non-zero exit unless opts.allow_failure. opts.dir runs it elsewhere (relative to the target's cwd, unlike os.exec's positional dir); opts.quiet captures the output without echoing it to the console. This is VCS-AGNOSTIC only in that magus picks the binary; the args are the backend's own, so branch on vcs.name() when they differ. Raises when no VCS is resolved, rather than running nothing and reporting success.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Impl:    VcsCmd,
		},
		{
			Name: "tags",
			Doc:  "Repository tags, newest first. Each is an object {name, date, id}: name as written (\"v0.3.0\", no refs/tags/ prefix), date RFC3339 (empty when the VCS reported none), id the revision it resolves to. pattern is a glob over the name (\"v*\"); wildcards stop at \"/\", so \"v*\" selects releases and skips a namespaced tag like backup/x. Omit it to list every tag. Empty when no VCS is resolved or the backend has no tags (jj); a failed query raises rather than reporting \"no tags\". Note a shallow or single-branch clone legitimately fetches no tags, so an empty list still means \"none present here\", not \"none exist\".",
			Args: []Arg{
				{Name: "pattern", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Tag]"}},
			Impl:    VcsTags,
		},
		{
			Name:    "describe",
			Doc:     "Human-readable version string from the nearest tag (git's `describe --tags --always --dirty`: tag, else short hash, with a -dirty suffix for a modified tree). \"\" when no VCS is resolved, or for a backend without a tag-describe concept (jj) - so a magusfile stamps a version without shelling out to git. Pair with vcs.commit().short as a fallback.",
			Returns: []Ret{{Type: TypeString}},
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
	vcsMu.Lock()
	defer vcsMu.Unlock()
	if wd == vcsCwdKey {
		return vcsCached, vcsBase
	}
	res, err := vcs.Resolve(ctx, wd, "", types.VCSOptions{})
	if err != nil {
		// A resolve failure (transient error, ctx cancellation) is not "no VCS" - do
		// not poison the cache for this cwd with it, or every later call in the
		// process would replay this one failure forever.
		return nil, ""
	}
	vcsCwdKey = wd
	if res.VCS == nil {
		vcsCached, vcsBase = nil, ""
		return nil, ""
	}
	vcsCached = res.VCS
	vcsBase = res.Base
	return vcsCached, vcsBase
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

// VcsBase returns the resolved base ref used for diffs (see VcsName).
func VcsBase(ctx context.Context) (string, error) {
	_, base := resolveVCS(ctx)
	return base, nil
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

// VcsChangedFiles lists files changed against base, defaulting to the resolved base ref.
//
// Paths, like vcs.status's, each carrying the repository root as their base. A VCS
// reports diff paths from the root while a target runs in its project directory, so a
// bare string left the caller to supply that fact from memory.
//
// The probe runs at EffectiveCwd, matching resolveVCS and vcs.status. It used to pass an
// empty dir, which means the PROCESS cwd: so magus resolved which VCS to use from the
// target's project directory and then ran the command somewhere else entirely. Identical
// only while both sit in the same repository - and a target's cwd IS its project
// directory, so a nested repository, or a daemon whose process cwd is outside the
// workspace, made the two disagree.
func VcsChangedFiles(ctx context.Context, base string) ([]types.Path, error) {
	v, defaultBase := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	if base == "" {
		base = defaultBase
	}
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
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

// vcsDir is the directory every vcs probe runs in: the target's, not the process's.
//
// These call sites passed "" and explained it as "host bindings run in the project cwd".
// They do not. runBuzz deliberately does NOT os.Chdir - it carries the target's directory
// on the context so projects can execute concurrently without corrupting a shared process
// cwd (internal/interp/runtime.go) - so "" meant the PROCESS cwd, which is a different
// place. resolveVCS already picks the driver from EffectiveCwd, so magus was choosing
// which VCS to use from one directory and then running it in another.
//
// Harmless while both sit in the same repository, which is why it went unnoticed. Not
// harmless in the daemon, where the process cwd belongs to the daemon and the context cwd
// comes from the request (internal/proc/server.go).
func vcsDir(ctx context.Context) string {
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		return ""
	}
	return dir
}

// vcsMetadata resolves the workspace VCS and reads its metadata, RAISING when either step
// fails rather than reporting a zero value.
//
// These accessors used to swallow both failures and return "". That is not how a Buzz
// function reports a problem - upstream declares the error in the signature (`!> errors\X`)
// and the caller writes try/catch - and it is not even unambiguous here, because "" is a
// value a branch name could in principle take. Worse, it pushed the check onto every call
// site: a magusfile that forgot `if (h == "")` silently interpolated an empty commit into a
// version string or an image tag, and nothing surfaced until someone read the artifact.
//
// magus.affected already made this call the other way, for the same reason: an empty answer
// and an unavailable one mean opposite things to whoever is deciding what to build.
func vcsMetadata(ctx context.Context) (types.VCSMeta, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return types.VCSMeta{}, types.DiagnosticErrorf(types.VCSUnavailable, "no VCS resolved for this workspace; use vcs.name() to test before asking for commit metadata")
	}
	meta, err := v.Metadata(ctx, vcsDir(ctx))
	if err != nil {
		return types.VCSMeta{}, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s metadata", v.Name())
	}
	return meta, nil
}

// VcsRef returns the movable name at the current revision (a git branch, an hg named
// branch, a jj bookmark); raises when no VCS or metadata is available.
func VcsRef(ctx context.Context) (string, error) {
	meta, err := vcsMetadata(ctx)
	if err != nil {
		return "", err
	}
	return meta.Ref, nil
}

// VcsStatus reports the working tree's uncommitted state as a typed Status.
//
// It replaces the vcs.dirty_files half of the old pair, which handed a magusfile the
// backend's own status lines - git porcelain, hg status, jj diff --name-only - so a
// caller had to know which VCS it was on to parse them, and every caller reimplemented
// that. statusPaths does it once, here.
//
// Paths carry the repository root as their base. A VCS reports paths from the root, but
// a target runs with its cwd set to its PROJECT directory, so a bare string was silently
// ambiguous exactly when a project was not the root.
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
	lines, err := v.DirtyFiles(ctx, dir, paths)
	if err != nil {
		return types.Status{}, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s status", v.Name())
	}
	root, err := v.Root(ctx, dir)
	if err != nil {
		root = dir
	}
	files := make([]types.Path, 0, len(lines))
	for _, p := range statusPaths(v.Name(), lines) {
		files = append(files, types.Path{Value: p, Base: root})
	}
	return types.Status{Clean: len(files) == 0, Files: files}, nil
}

// statusPaths delegates to vcs.StatusPaths, which lives beside the drivers that produce
// these lines. Kept as a local alias so the call sites in this file stay short.
func statusPaths(vcsName string, lines []string) []string {
	return vcs.StatusPaths(vcsName, lines)
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
		// passes having checked nothing - the one outcome a gate must never produce
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
// up - a caller uses vcs.name() to test for a VCS first, and try/catch for a
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

// VcsHistory returns up to limit recent commits (newest first) as objects, or an
// empty list when no VCS is resolved. It RAISES when the query fails - an empty
// list there would read as "no history" for "could not read history".
func VcsHistory(ctx context.Context, limit int) ([]types.Commit, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	commits, err := v.History(ctx, vcsDir(ctx), limit)
	if err != nil {
		return nil, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s history", v.Name())
	}
	return commits, nil
}

// VcsDescribe returns a human-readable version string from the nearest tag (see
// the driver Describe methods), or "" when no VCS is resolved or the backend has
// no describe concept. It RAISES when the query fails - "" is reserved for the
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
	// so a non-nil error here is a real fault - git missing, not a repository, or
	// a malformed pattern. Swallowing it would report "no releases" for "could not
	// read releases", which is exactly the confusion a release page must not make.
	return v.Tags(ctx, vcsDir(ctx), pattern)
}

// vcsExe returns the absolute path of the active VCS executable, or "" when
// unresolved or not on PATH. Internal: the Buzz surface exposes vcs.cmd, which runs the
// binary, rather than a path for the caller to hand to os.exec themselves.
func vcsExe(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	path, err := exec.LookPath(v.Name())
	if err != nil {
		return "", types.WrapDiagnostic(types.ToolNotOnPath, err, "%s is the resolved VCS but is not on PATH", v.Name())
	}
	return path, nil
}

// VcsCmd runs the active VCS binary with args.
//
// This replaced vcs.exe, which handed back a PATH and left every caller to write
// os.exec(<the vcs binary>, [...]) - two calls, and a silent no-op when the path came back
// empty because no VCS was resolved. Returning an ExecResult also puts the escape hatch
// on the same typed footing as magus.cmd and os.exec instead of a bare string. "exe" was
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
