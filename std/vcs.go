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
			Doc:  "List files changed against the given base (defaults to vcs.base).",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    VcsDiff,
		},
		{
			Name:    "short_hash",
			Doc:     "Short commit hash. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsShortHash,
		},
		{
			Name:    "hash",
			Doc:     "Full commit hash. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsHash,
		},
		{
			Name:    "branch",
			Doc:     "Current branch. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsBranch,
		},
		{
			Name:    "commit_date",
			Doc:     "Commit date string. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsCommitDate,
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
			Name: "dirty_files",
			Doc:  "The changed entries in the working tree as the backend's own status lines (git porcelain, hg status, jj diff --name-only), or an empty list when clean. Pass paths to scope it, exactly like is_dirty. This is is_dirty with the detail: a gate that has to say WHICH generated files moved asks this instead of shelling out to the VCS and parsing it by hand.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    VcsDirtyFiles,
		},
		{
			Name: "diagnose_drift",
			Doc:  "Diagnose why a generate gate's outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug); files lists the drifted outputs as the backend's status lines, so a gate reports WHICH files moved without shelling out to the VCS. drifted is false with empty fields when the outputs are clean. Composes is_dirty; does not replace it.",
			Args: []Arg{
				{Name: "outputs", Type: TypeStringSlice},
				{Name: "inputs", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    VcsDiagnoseDrift,
		},
		{
			Name:    "metadata",
			Doc:     "Full metadata table: short_hash, hash, branch, commit_date, is_dirty.",
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    VcsMetadata,
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
			Name:    "exe",
			Doc:     "Absolute path to the active VCS executable (git/hg/jj), or \"\" if unresolved. Lets a magusfile run a VCS-agnostic escape-hatch command: os.exec(vcs.exe(), [...]).",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsExe,
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
	root, err := v.Root(ctx, "") // host bindings run in the project cwd
	if err != nil {
		return "", fmt.Errorf("vcs.root: %w", err)
	}
	return root, nil
}

// VcsDiff lists files changed against base, defaulting to the resolved base ref.
func VcsDiff(ctx context.Context, base string) ([]string, error) {
	v, defaultBase := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	if base == "" {
		base = defaultBase
	}
	files, err := v.Diff(ctx, "", base)
	if err != nil {
		return nil, fmt.Errorf("vcs.diff: %w", err)
	}
	return files, nil
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
	meta, err := v.Metadata(ctx, "")
	if err != nil {
		return types.VCSMeta{}, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s metadata", v.Name())
	}
	return meta, nil
}

// VcsShortHash returns the short commit hash; raises when no VCS or metadata is available.
func VcsShortHash(ctx context.Context) (string, error) {
	meta, err := vcsMetadata(ctx)
	if err != nil {
		return "", err
	}
	return meta.ShortHash, nil
}

// VcsHash returns the full commit hash; raises when no VCS or metadata is available.
func VcsHash(ctx context.Context) (string, error) {
	meta, err := vcsMetadata(ctx)
	if err != nil {
		return "", err
	}
	return meta.Hash, nil
}

// VcsBranch returns the current branch; raises when no VCS or metadata is available.
func VcsBranch(ctx context.Context) (string, error) {
	meta, err := vcsMetadata(ctx)
	if err != nil {
		return "", err
	}
	return meta.Branch, nil
}

// VcsCommitDate returns the commit date string; raises when no VCS or metadata is available.
func VcsCommitDate(ctx context.Context) (string, error) {
	meta, err := vcsMetadata(ctx)
	if err != nil {
		return "", err
	}
	return meta.CommitDate, nil
}

// VcsDiagnoseDrift diagnoses a generate gate's drift into a coded diagnostic. Given the
// target's declared output globs and input globs (project-relative) and the fact that the
// tree drifted, it distinguishes the three causes the plan defines:
//
//   - outputs dirty AND a declared input is also dirty -> MGS4006 StaleGeneratedOutput:
//     a source input changed, so regeneration is expected; commit it.
//   - outputs dirty, inputs byte-identical, running a DEV build -> MGS4005 EnvironmentalDrift:
//     the committed form is produced by the pinned release (compat contract), so a dev
//     build's differing output is version/tool skew - not the developer's change.
//   - outputs dirty, inputs byte-identical, running a RELEASE build -> MGS4003
//     NondeterministicOutput: same inputs and generator version, yet output differs - a
//     reproducibility bug.
//
// It RETURNS the classification as a verdict record rather than throwing, so the gate
// owns the response - fail on a clean-tree drift, warn on a mid-edit dirty tree (the
// plan's local-warn / CI-fail split). The record is a plain map:
//
//	{ drifted: bool, code: str, message: str, url: str, files: []str }
//
// drifted is false (and code/message/url empty, files empty) when the outputs are not
// actually dirty. files carries the backend's status lines for the drifted outputs, so a
// gate can say WHICH files moved without shelling out to the VCS itself.
// It composes vcs.isDirty (called on outputs and inputs) rather than replacing it:
// isDirty stays the general "is this path dirty" primitive; diagnoseDrift is the
// drift-specific reading on top of it plus the version signal.
func VcsDiagnoseDrift(ctx context.Context, outputs, inputs []string) (map[string]any, error) {
	// Same keys as the drifted verdict, so a caller can read .files unconditionally
	// rather than discovering the key is absent only on the clean path.
	clean := map[string]any{"drifted": false, "code": "", "message": "", "url": "", "files": []string{}}
	v, _ := resolveVCS(ctx)
	if v == nil {
		return clean, nil
	}
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
	}
	// DirtyFiles, not Dirty: the verdict carries WHICH outputs drifted, and Dirty is
	// defined in terms of this anyway, so naming them costs nothing extra. A gate that
	// reports only "something drifted" sends its reader to reproduce the run just to
	// learn what a status call already knew - and a gate fires precisely when the
	// reader is looking at a CI log rather than the tree.
	dirtyFiles, err := v.DirtyFiles(ctx, dir, outputs)
	if err != nil {
		// Split from the !outDirty case below on purpose: they were one branch, so a
		// failed probe returned the same "clean" verdict as a genuinely clean tree. A
		// drift diagnosis that cannot read the tree has no verdict to give.
		return nil, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s status", v.Name())
	}
	if len(dirtyFiles) == 0 {
		return clean, nil
	}
	inDirty := false
	if len(inputs) > 0 {
		inDirty, _ = v.Dirty(ctx, dir, inputs)
	}

	var code types.DiagnosticCode
	var msg string
	switch {
	case inDirty:
		code = types.StaleGeneratedOutput
		msg = "generated output drifted and a declared input changed; re-run `magus run generate:rw` and commit"
	case types.IsDevMagusVersion(types.MagusVersionFromContext(ctx)):
		ver := types.MagusVersionFromContext(ctx)
		if ver == "" {
			ver = "unknown"
		}
		code = types.EnvironmentalDrift
		msg = fmt.Sprintf("generated output drifted but its declared inputs are unchanged; the committed form is produced by the pinned release and you are running a dev build (%s) - not your change, do not commit", ver)
	default:
		code = types.NondeterministicOutput
		msg = "generated output drifted but its declared inputs and the generator version are unchanged - a non-deterministic generator"
	}
	return map[string]any{
		"drifted": true,
		"code":    string(code),
		"message": msg,
		"url":     types.CodeURL(code),
		"files":   dirtyFiles,
	}, nil
}

// VcsDirtyFiles returns the changed entries as the backend's status lines, the detail
// half of VcsIsDirty. Both resolve paths against the project's working directory and
// both RAISE on a failed probe rather than reporting "clean": a gate that cannot read
// the tree has no answer, and silently returning an empty list would let it pass having
// checked nothing - the one outcome a gate must never produce quietly.
func VcsDirtyFiles(ctx context.Context, paths []string) ([]string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
	}
	files, err := v.DirtyFiles(ctx, dir, paths)
	if err != nil {
		return nil, types.WrapDiagnostic(types.VCSUnavailable, err, "read %s status", v.Name())
	}
	return files, nil
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

// VcsMetadata returns the full metadata map: short_hash, hash, branch, commit_date, is_dirty.
func VcsMetadata(ctx context.Context) (map[string]any, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return map[string]any{
			"short_hash":  "",
			"hash":        "",
			"branch":      "",
			"commit_date": "",
			"is_dirty":    false,
		}, nil
	}
	meta, err := v.Metadata(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("vcs.metadata: %w", err)
	}
	return map[string]any{
		"short_hash":  meta.ShortHash,
		"hash":        meta.Hash,
		"branch":      meta.Branch,
		"commit_date": meta.CommitDate,
		"is_dirty":    meta.IsDirty,
	}, nil
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
	c, err := v.FindCommit(ctx, "", rev) // host bindings run in the project cwd
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
	commits, err := v.History(ctx, "", limit)
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
	out, err := v.Describe(ctx, "") // host bindings run in the project cwd
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
	return v.Tags(ctx, "", pattern) // host bindings run in the project cwd
}

// VcsExe returns the absolute path of the active VCS executable, or "" when
// unresolved or not on PATH.
func VcsExe(ctx context.Context) (string, error) {
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
