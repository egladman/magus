package vcs

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/egladman/magus/types"
)

var builtin = []types.VCSDriver{gitVCS{}, hgVCS{}, jjVCS{}}

// IsSecondaryCheckout reports whether dir is a second checkout of a repository
// under any supported VCS (a git linked worktree, an `hg share`, a jj secondary
// workspace). Discovery skips such dirs so a repo's projects and spells are not
// re-indexed and made to shadow the originals (MGS1002). Each backend matches its
// own on-disk signature, so detection favors no single VCS and needs no resolution
// step (the active VCS is not yet known when discovery walks the tree).
func IsSecondaryCheckout(dir string) bool {
	for _, v := range builtin {
		if v.IsSecondaryCheckout(dir) {
			return true
		}
	}
	return false
}

// Resolve picks the active VCS for root: disabled → explicit → auto (claim dir) → default (git).
// Base ref: runtimeBase → opts.BaseRef → MAGUS_VCS_BASE_REF → per-VCS env → built-in default.
func Resolve(_ context.Context, root, runtimeBase string, opts types.VCSOptions) (types.VCSResolution, error) {
	if opts.Enabled != nil && !*opts.Enabled {
		return types.VCSResolution{Source: types.VCSSourceDisabled}, nil
	}
	if opts.Enabled == nil && os.Getenv("MAGUS_VCS_ENABLED") == "false" {
		return types.VCSResolution{Source: types.VCSSourceDisabled}, nil
	}

	name := opts.Name
	if name == "" {
		name = os.Getenv("MAGUS_VCS_NAME")
	}

	var (
		v      types.VCSDriver
		source types.VCSSource
	)

	if name != "" {
		impl, ok := lookupImpl(name)
		if !ok {
			return types.VCSResolution{}, fmt.Errorf("%w: %q", types.ErrVCSUnknown, name)
		}
		v = impl
		source = types.VCSSourceExplicit
	} else {
		for _, e := range builtin {
			if claimsExist(root, e.Claims()) {
				v = e
				name = e.Name()
				source = types.VCSSourceAuto
				break
			}
		}
		if v == nil {
			v = builtin[0]
			name = v.Name()
			source = types.VCSSourceDefault
		}
	}

	globalBaseRef := opts.BaseRef
	if globalBaseRef == "" {
		globalBaseRef = os.Getenv("MAGUS_VCS_BASE_REF")
	}
	perVCSBaseRef := os.Getenv(perVCSEnv(name, "BASE_REF"))

	base := chooseBase(runtimeBase, globalBaseRef, perVCSBaseRef, v.Base())

	return types.VCSResolution{Name: name, Source: source, Base: base, VCS: v}, nil
}

func lookupImpl(name string) (types.VCSDriver, bool) {
	for _, v := range builtin {
		if v.Name() == name {
			return v, true
		}
	}
	return nil, false
}

// InstallableVCSes returns the names of built-in VCS drivers that support
// merge-driver installation.
func InstallableVCSes() []string {
	var names []string
	for _, v := range builtin {
		if _, ok := v.(types.MergeDriverInstaller); ok {
			names = append(names, v.Name())
		}
	}
	return names
}

// Installer returns the merge-driver installer for the named VCS, or (nil, false).
func Installer(name string) (types.MergeDriverInstaller, bool) {
	v, ok := lookupImpl(name)
	if !ok {
		return nil, false
	}
	inst, ok := v.(types.MergeDriverInstaller)
	return inst, ok
}

func chooseBase(runtime, global, perVCS, def string) string {
	if runtime != "" {
		return runtime
	}
	if global != "" {
		return global
	}
	if perVCS != "" {
		return perVCS
	}
	if def != "" {
		return def
	}
	return "origin/main"
}

func perVCSEnv(name, suffix string) string {
	return "MAGUS_VCS_" + strings.ToUpper(name) + "_" + suffix
}

// checkRef rejects a base ref / rev / sha that begins with "-", which a VCS would
// otherwise read as a flag (argument injection) when passed as a standalone token.
func checkRef(ref string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("vcs: refusing ref %q that looks like a flag", ref)
	}
	return nil
}

func claimsExist(root string, claims []string) bool {
	for _, c := range claims {
		if _, err := os.Stat(filepath.Join(root, c)); err == nil {
			return true
		}
	}
	return false
}

// parseTags reads "<name>\t<rfc3339 date>\t<id>" lines, one tag per line, in the
// order the backend emitted them, keeping only names matching pattern ("" keeps
// all). Both git's for-each-ref --format and hg's tag template are configured to
// produce this shape, so the two backends share one parser AND one matcher: git
// could filter refs server-side via for-each-ref's own pattern, but then a glob
// would mean subtly different things per backend, which is worse than the cost of
// matching a handful of strings here.
//
// path.Match is the matcher because its wildcards stop at "/", so "v*" selects
// v0.3.0 while correctly skipping a namespaced tag like backup/pre-reword. A
// malformed pattern is a caller bug and is returned, not silently treated as
// "match nothing". A line missing a name is skipped; an unparsable date is left
// zero rather than dropping the tag, since the name is what callers rely on.
func parseTags(out, pattern string) ([]types.VCSTag, error) {
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	tags := make([]types.VCSTag, 0, len(lines))
	for _, line := range lines {
		name, rest, ok := strings.Cut(line, "\t")
		if !ok || name == "" {
			continue
		}
		if pattern != "" {
			match, err := path.Match(pattern, name)
			if err != nil {
				return nil, fmt.Errorf("vcs: tag pattern %q: %w", pattern, err)
			}
			if !match {
				continue
			}
		}
		when, id, _ := strings.Cut(rest, "\t")
		tag := types.VCSTag{Name: name, ID: id}
		tag.Prefix, tag.Version = splitTagVersion(name)
		if ts, err := time.Parse(time.RFC3339, when); err == nil {
			tag.Date = ts
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// splitTagVersion splits a tag name into its module prefix and parsed
// version: "libs/gopherbuzz/v0.1.0" -> ("libs/gopherbuzz/", 0.1.0), "v0.3.0"
// -> ("", 0.3.0). A name with no "/" has an empty prefix. A version portion
// that fails to parse (an annotated tag like "checkpoint", or a namespaced
// non-release tag the pattern filter let through) is not an error - it
// leaves Version at its zero value. Parses with Masterminds/semver, the same
// library std/semver.go's SemverParse uses; the two can't share a call
// because vcs can't import std (std already imports vcs).
func splitTagVersion(name string) (prefix string, version types.SemverVersion) {
	verPart := name
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		prefix, verPart = name[:i+1], name[i+1:]
	}
	sv, err := semver.NewVersion(verPart)
	if err != nil {
		return prefix, types.SemverVersion{}
	}
	return prefix, types.SemverVersion{
		Major:      int(sv.Major()),
		Minor:      int(sv.Minor()),
		Patch:      int(sv.Patch()),
		Prerelease: sv.Prerelease(),
		Metadata:   sv.Metadata(),
		Original:   sv.Original(),
	}
}

// StatusPaths strips each backend's status prefix from DirtyFiles output, leaving the
// path.
//
//	git  "XY path"      porcelain: two status columns and a space
//	hg   "X path"       one status column and a space
//	jj   "path"         diff --name-only reports no status at all
//
// A git rename reads "R  old -> new"; the new path is the one that exists, so that is
// what is kept. git quotes paths outside ASCII unless core.quotePath is off, which
// gitEnviron disables, so no unquoting is needed here.
//
// It lives here, beside the drivers that PRODUCE those lines, because the format is the
// backend's and the caller should never have to guess it. DirtyFiles returning status
// lines rather than paths is what forces this to exist at all; a second parser in
// cmd/magus sniffs the shape instead of being told it, and is the one still to fold in.
func StatusPaths(vcsName string, lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		p := line
		switch vcsName {
		case "git":
			if len(p) > 3 {
				p = p[3:]
			}
			if _, after, found := strings.Cut(p, " -> "); found {
				p = after
			}
		case "hg":
			if len(p) > 2 {
				p = p[2:]
			}
		}
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
