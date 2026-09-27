package types

import (
	"context"
	"strings"
)

type magusVersionKey struct{}

// WithMagusVersion carries the running magus binary's display version (main.version,
// linker-injected) on ctx so a host method that needs the release-vs-dev distinction
// (the drift classifier) can read it without importing package main. The CLI stamps it
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

// IsDevMagusVersion reports whether version is a dev/unstamped build rather than a clean
// tagged release. The linker default is "unknown" (the dev sentinel); a git-describe dev
// build past a tag carries a "-g<sha>" suffix (v0.1.0-5-gabc123); a clean release is a
// bare tag (v0.1.0). Committed generated files are, by the compatibility contract,
// produced by the pinned release, so a dev build that finds output drift with unchanged
// inputs is version skew (environmental), not the developer's change.
func IsDevMagusVersion(version string) bool {
	return version == "" || version == "unknown" || strings.Contains(version, "-g")
}

type magusBuildKey struct{}

// MagusBuild is the running binary's build identity as the linker stamped it. A field
// the build did not stamp is "" or "unknown".
type MagusBuild struct {
	// Version is main.version: a release tag, or git describe output for a dev build.
	Version string
	// Commit is the revision the binary was built from.
	Commit string
	// Date is that commit's date, RFC 3339.
	Date string
}

// WithMagusBuild carries the running binary's build identity on ctx, for the writers of
// state that other magus binaries read, which record who wrote it.
func WithMagusBuild(ctx context.Context, b MagusBuild) context.Context {
	return context.WithValue(ctx, magusBuildKey{}, b)
}

// MagusBuildFromContext returns what WithMagusBuild stored, or the zero MagusBuild.
func MagusBuildFromContext(ctx context.Context) MagusBuild {
	b, _ := ctx.Value(magusBuildKey{}).(MagusBuild)
	return b
}
