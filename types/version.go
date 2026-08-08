package types

import (
	"context"
	"strings"

	"github.com/egladman/magus/spells"
)

type magusVersionKey struct{}

// WithMagusVersion carries the running magus binary's display version (main.version,
// linker-injected) on ctx so a host method that needs the release-vs-dev distinction -
// the drift classifier - can read it without importing package main. The CLI stamps it
// once on the root context at startup; a bare library caller that never stamps it reads
// "" (treated as a dev build, the conservative default).
func WithMagusVersion(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, magusVersionKey{}, version)
}

// MagusVersionFromContext returns the version WithMagusVersion stored, or "" when none
// was set.
func MagusVersionFromContext(ctx context.Context) string {
	v, _ := ctx.Value(magusVersionKey{}).(string)
	return v
}

type toolBoundsKey struct{}

// WithToolBounds carries every project's declared version windows for this run, keyed
// by absolute project dir and then by bin name, so the bounds check at op dispatch can
// intersect a project's policy with what the spell itself declares.
//
// On the context rather than baked into the registered spell, and that is a correctness
// requirement, not a style choice: spells are interned singletons in one process-wide
// registry, and the daemon serves many workspaces from it. A window folded into the
// registry at registration would leak one project's policy into every other one, in
// every other workspace. Stamped per run, beside WithWorkspace, it cannot.
//
// Keyed by DIR, not workspace-wide, because the declaration is per project: `console`
// may require a node line that `docs` does not, and a monorepo where one map governs
// every project can only express the intersection of everyone's constraints.
func WithToolBounds(ctx context.Context, byDir map[string]map[string]spells.VersionBounds) context.Context {
	if len(byDir) == 0 {
		return ctx
	}
	return context.WithValue(ctx, toolBoundsKey{}, byDir)
}

// ToolBoundsFromContext returns the window the project at dir declared for bin, or the
// zero value when it declared none. The zero value intersects to a no-op, so a caller
// never has to distinguish "no bounds" from "not stamped".
func ToolBoundsFromContext(ctx context.Context, dir, bin string) spells.VersionBounds {
	m, _ := ctx.Value(toolBoundsKey{}).(map[string]map[string]spells.VersionBounds)
	return m[dir][bin]
}

// IsDevMagusVersion reports whether version is a dev/unstamped build rather than a clean
// tagged release. The linker default is "unknown" (the dev sentinel); a git-describe dev
// build past a tag carries a "-g<sha>" suffix (v0.1.0-5-gabc123); a clean release is a
// bare tag (v0.1.0). Committed generated files are, by the compatibility contract,
// produced by the pinned release - so a dev build that finds output drift with unchanged
// inputs is version skew (environmental), not the developer's change.
func IsDevMagusVersion(version string) bool {
	return version == "" || version == "unknown" || strings.Contains(version, "-g")
}
