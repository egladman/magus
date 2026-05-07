package std

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

//go:generate go run ../../cmd/magus-bindings-gen -module vcs -lang lua -out gen/lua/vcs.go
//go:generate go run ../../cmd/magus-bindings-gen -module vcs -lang buzz -out gen/buzz/vcs.go

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
			Doc:     "Short commit hash, or empty on error.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsShortHash,
		},
		{
			Name:    "hash",
			Doc:     "Full commit hash, or empty on error.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsHash,
		},
		{
			Name:    "branch",
			Doc:     "Current branch, or empty on error.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsBranch,
		},
		{
			Name:    "commit_date",
			Doc:     "Commit date string, or empty on error.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsCommitDate,
		},
		{
			Name:    "is_dirty",
			Doc:     "True if the working tree has uncommitted changes.",
			Returns: []Ret{{Type: TypeBool}},
			Impl:    VcsIsDirty,
		},
		{
			Name:    "metadata",
			Doc:     "Full metadata table: short_hash, hash, branch, commit_date, is_dirty.",
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    VcsMetadata,
		},
		{
			Name: "commit",
			Doc:  "Resolve a revision (a VCS-native rev expression; omit for the current revision) to its commit record: {id, short, author {name, email}, date, subject, body, parents}. id is the content/revision id (git SHA, hg node, jj commit_id); date is RFC3339, when the revision was recorded. Every field is meaningful for every VCS. Returns nil when no VCS is resolved or the revision can't be looked up.",
			Args: []Arg{
				{Name: "rev", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    VcsCommit,
		},
		{
			Name: "history",
			Doc:  "Up to limit recent commits, newest first; each is the same record vcs.commit returns. An empty list when no VCS is resolved.",
			Args: []Arg{
				{Name: "limit", Type: TypeInt},
			},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    VcsHistory,
		},
		{
			Name:    "exe",
			Doc:     "Absolute path to the active VCS executable (git/hg/jj), or \"\" if unresolved. Lets a magusfile run a VCS-agnostic escape-hatch command: os.exec(vcs.exe(), [...]).",
			Returns: []Ret{{Type: TypeString}},
			Impl:    VcsExe,
		},
	},
}

// commitToMap renders a Commit as the nested record the magusfile sees. Every
// key is always present and meaningful — no VCS-conditional or nil fields.
func commitToMap(c types.Commit) map[string]any {
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

// vcsState caches the resolved VCS for the current cwd. Re-resolves when cwd
// changes, mirroring the per-registration resolution the hand-written
// binding did before. Package-level state is acceptable here because cwd is
// already process-global (chdirMu in runtime.go serialises mutations).
var (
	vcsMu     sync.Mutex
	vcsCwdKey string
	vcsCached types.VCSDriver
	vcsBase   string
)

func resolveVCS(ctx context.Context) (types.VCSDriver, string) {
	wd, err := os.Getwd()
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
// TODO: Field.Resolver has no ctx parameter; uses context.Background().
func VcsName() (string, error) {
	v, _ := resolveVCS(context.Background())
	if v == nil {
		return "", nil
	}
	return v.Name(), nil
}

// VcsBase returns the resolved base ref used for diffs.
// TODO: Field.Resolver has no ctx parameter; uses context.Background().
func VcsBase() (string, error) {
	_, base := resolveVCS(context.Background())
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

// VcsShortHash returns the short commit hash, or "" when unavailable.
func VcsShortHash(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	meta, err := v.Metadata(ctx, "")
	if err != nil {
		return "", nil //nolint:nilerr // VCS metadata unavailable: callers treat empty as "no VCS info"
	}
	return meta.ShortHash, nil
}

// VcsHash returns the full commit hash, or "" when unavailable.
func VcsHash(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	meta, err := v.Metadata(ctx, "")
	if err != nil {
		return "", nil //nolint:nilerr // VCS metadata unavailable: callers treat empty as "no VCS info"
	}
	return meta.Hash, nil
}

// VcsBranch returns the current branch, or "" when unavailable.
func VcsBranch(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	meta, err := v.Metadata(ctx, "")
	if err != nil {
		return "", nil //nolint:nilerr // VCS metadata unavailable: callers treat empty as "no VCS info"
	}
	return meta.Branch, nil
}

// VcsCommitDate returns the commit date string, or "" when unavailable.
func VcsCommitDate(ctx context.Context) (string, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return "", nil
	}
	meta, err := v.Metadata(ctx, "")
	if err != nil {
		return "", nil //nolint:nilerr // VCS metadata unavailable: callers treat empty as "no VCS info"
	}
	return meta.CommitDate, nil
}

// VcsIsDirty reports whether the working tree has uncommitted changes.
func VcsIsDirty(ctx context.Context) (bool, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return false, nil
	}
	meta, err := v.Metadata(ctx, "")
	if err != nil {
		return false, nil //nolint:nilerr // VCS metadata unavailable: report not-dirty rather than erroring
	}
	return meta.IsDirty, nil
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

// VcsCommit resolves rev (empty = current revision) to its commit record, or nil
// when no VCS is resolved or the revision can't be looked up. Returning nil (a
// null in the magusfile) rather than an empty record gives callers an
// unambiguous "no commit" signal — distinct from a real commit with empty fields.
func VcsCommit(ctx context.Context, rev string) (any, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return nil, nil
	}
	c, err := v.FindCommit(ctx, "", rev) // host bindings run in the project cwd
	if err != nil {
		return nil, nil //nolint:nilerr // unresolved (no commits yet, bad rev): null, matching the metadata accessors' graceful empties
	}
	return commitToMap(c), nil
}

// VcsHistory returns up to limit recent commits (newest first) as records, or an
// empty list when no VCS is resolved or the query fails.
func VcsHistory(ctx context.Context, limit int) (any, error) {
	v, _ := resolveVCS(ctx)
	if v == nil {
		return []any{}, nil
	}
	commits, err := v.History(ctx, "", limit)
	if err != nil {
		return []any{}, nil //nolint:nilerr // unavailable: empty list, matching metadata accessors
	}
	out := make([]any, len(commits))
	for i, c := range commits {
		out[i] = commitToMap(c)
	}
	return out, nil
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
		return "", nil //nolint:nilerr // not on PATH: empty path, caller checks for ""
	}
	return path, nil
}
