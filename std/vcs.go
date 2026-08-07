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
	Fields: []Field{
		{Name: "name", Type: TypeString, Doc: "VCS short name (e.g. \"git\"). Empty if unresolved.", Resolver: VcsName},
		{Name: "base", Type: TypeString, Doc: "Resolved base ref for diffs.", Resolver: VcsBase},
	},
	Methods: []Method{
		{
			Name:    "root",
			Doc:     "Absolute path of the repository root.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsRoot,
		},
		{
			Name: "diff",
			Doc:  "The files changed against the given base (defaults to vcs.base), each a Path carrying the repository root as its base. Empty when no VCS is resolved.",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Path]"}},
			Impl:    VcsDiff,
		},
		{
			Name:    "ref",
			Doc:     "The movable name pointing at the current revision, or \"\" when there is none. Backend-specific by nature: a git branch, a Mercurial named branch, a Jujutsu bookmark. jj's working copy is usually an anonymous change, so \"\" is an ordinary answer there, not a failure. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsRef,
		},
		{
			Name: "status",
			Doc:  "The working tree's uncommitted state as {clean, files}: clean is true when nothing changed, files are the changed paths (empty when clean). Pass paths to scope it. Each file is a Path carrying the repository root as its base, because a VCS reports paths from the root while a target runs in its project directory. Paths only - a per-entry status code is not portable (jj reports none), so reach for vcs.exe() when the codes matter.",
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
			Doc:  "Escape hatch: run the active VCS binary (git/hg/jj) with args, for something no method covers. Mirrors magus.cmd and os.exec - returns {stdout, stderr, code, ok} and raises on a non-zero exit unless opts.allow_failure. opts.dir runs it elsewhere (relative to the target's cwd). This is VCS-AGNOSTIC only in that magus picks the binary; the args are the backend's own, so branch on vcs.name() when they differ. Raises when no VCS is resolved, rather than running nothing and reporting success.",
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
			Doc:     "Human-readable version string from the nearest tag (git's `describe --tags --always --dirty`: tag, else short hash, with a -dirty suffix for a modified tree). \"\" when no VCS is resolved, or for a backend without a tag-describe concept (jj) - so a magusfile stamps a version without shelling out to git. Pair with vcs.shortHash() as a fallback.",
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
	vcsCwdKey = wd
	if err != nil || res.VCS == nil {
		vcsCached, vcsBase = nil, ""
		return nil, ""
	}
	vcsCached = res.VCS
	vcsBase = res.Base
	return vcsCached, vcsBase
}

// VcsName returns the active VCS short name (e.g. "git"), or "" if unresolved.
// The Field is resolved once at module registration; the registration ctx is
// threaded through so resolution honors the run's cancellation rather than a
// detached background context.
func VcsName(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	return v.Name(), nil
}

// VcsBase returns the resolved base ref used for diffs. Resolved once at module
// registration with the registration ctx (see VcsName).
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

// VcsDiff lists files changed against base, defaulting to the resolved base ref.
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
func VcsDiff(ctx context.Context, base string) ([]types.Path, error) {
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
	files, err := v.Diff(ctx, dir, base)
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
	return meta.Branch, nil
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

// VcsCommit resolves rev (empty = current revision) to its commit object. When
// no VCS is resolved or the revision can't be looked up it returns the zero
// types.Commit - an all-empty object (id/date/… are ""), so a caller tests a
// field (e.g. c.date == "") rather than a null.
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
// empty list when no VCS is resolved or the query fails.
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
// no describe concept. A query failure is reported as "" rather than raising,
// matching the metadata accessors - callers treat "" as "no describe" and fall back.
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
	// Those return "" for a failed query because a magusfile reading vcs.branch()
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
// os.exec(vcs.exe(), [...]) - two calls, and a silent no-op when the path came back
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
