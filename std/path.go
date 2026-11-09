package std

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Path is the "path" host module: pure path-string math (no filesystem access).
// fs.* already covers dirname/basename/join/glob; these are the remaining
// computations a spell otherwise has to shell out to `realpath`/`readlink -f`
// for: absolute/relative resolution, lexical cleaning, and ~ expansion. Nothing
// here touches disk, so no sandbox check applies; reading/writing the resolved
// path still goes through the sandbox-aware fs.* calls.
var Path = Module{
	Name: "path",
	WASM: true,
	Doc:  "Pure path-string math: abs, rel, clean, is_abs, expand_user, and glob matching.",
	Methods: []Method{
		{
			Name:    "abs",
			Doc:     "Return the absolute form of path, resolved against the current directory and lexically cleaned.",
			Args:    []Arg{{Name: "path"}},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    PathAbs,
		},
		{
			Name:    "rel",
			Doc:     "Return a relative path from base to target; errors if no relative path exists.",
			Args:    []Arg{{Name: "base"}, {Name: "target"}},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    PathRel,
		},
		{
			Name:    "clean",
			Doc:     "Return the shortest lexically-equivalent path (resolves . and .., collapses separators).",
			Args:    []Arg{{Name: "path"}},
			Returns: []Ret{{}},
			Impl:    PathClean,
		},
		{
			Name:    "dir",
			Doc:     "Return every element of path but the last, cleaned: \".\" for a bare name, \"/\" for a child of the root.",
			Args:    []Arg{{Name: "path"}},
			Returns: []Ret{{}},
			Impl:    PathDir,
		},
		{
			Name:    "base",
			Doc:     "Return the last element of path, trailing separators removed: \".\" for an empty path.",
			Args:    []Arg{{Name: "path"}},
			Returns: []Ret{{}},
			Impl:    PathBase,
		},
		{
			Name:    "is_abs",
			Doc:     "Report whether path is absolute.",
			Args:    []Arg{{Name: "path"}},
			Returns: []Ret{{}},
			Impl:    PathIsAbs,
		},
		{
			Name: "matches",
			Doc:  "Report whether path matches a doublestar glob (** crosses directory separators, * does not). Purely lexical: unlike fs.glob it touches no filesystem, so it is what filters a list already in hand - the changed files from vcs.changed_files, the entries from archive.list - against the same pattern syntax a target's sources use. Raises on a malformed pattern rather than reporting no match, so a typo is not read as \"nothing changed\".",
			Args: []Arg{
				{Name: "pattern"},
				{Name: "path"},
			},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    PathMatches,
		},
		{
			Name: "matches_any",
			Doc:  "Report whether path matches ANY of the patterns; an empty pattern list is false. The common shape of a declared source or ignore set, which is a list rather than one glob.",
			Args: []Arg{
				{Name: "patterns"},
				{Name: "path"},
			},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    PathMatchAny,
		},
		{
			Name:    "expand_user",
			Doc:     "Expand a leading ~ (or ~/...) to the current user's home directory; other paths are returned unchanged.",
			Args:    []Arg{{Name: "path"}},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    PathExpandUser,
		},
			{
				Name:    "join",
				Doc:     "Join parts with the OS separator and clean the result. Unlike fs.join, which takes its parts as variadic arguments, this takes a list, so a caller holding one needs no spread.",
				Args:    []Arg{{Name: "parts"}},
				Returns: []Ret{{}},
				Impl:    PathJoin,
			},
			{
				Name:    "within",
				Doc:     "Report whether path equals dir or lies under it, comparing the cleaned forms lexically. A sibling that shares dir as a string prefix (\"/a/bc\" against \"/a/b\") is not within.",
				Args:    []Arg{{Name: "dir"}, {Name: "path"}},
				Returns: []Ret{{}},
				Impl:    PathWithin,
			},
	},
}

// PathAbs returns the cleaned absolute form of path, resolved against the
// context working directory (see resolvePath), not the process cwd: a target
// running in another project's directory would otherwise get an answer for the
// wrong place (see vcsDir in vcs.go for the same bug class).
func PathAbs(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(resolvePath(ctx, path))
	if err != nil {
		return "", fmt.Errorf("path.abs %q: %w", path, err)
	}
	return abs, nil
}

// PathRel returns a relative path from base to target.
func PathRel(_ context.Context, base, target string) (string, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("path.rel: %w", err)
	}
	return rel, nil
}

// PathDir returns all but the last element of path, as filepath.Dir does. It is
// pure string math, so a spell, which cannot import fs, can call it.
func PathDir(_ context.Context, path string) (string, error) {
	return filepath.Dir(path), nil
}

// PathBase returns the last element of path, as filepath.Base does.
func PathBase(_ context.Context, path string) (string, error) {
	return filepath.Base(path), nil
}

// PathClean returns the shortest lexically-equivalent path.
func PathClean(_ context.Context, path string) (string, error) {
	return filepath.Clean(path), nil
}

// PathMatches reports whether path matches a doublestar glob.
//
// Slashes are normalized first: doublestar matches on forward slashes, so a
// Windows-shaped path would never match a pattern written the way every
// magusfile writes one.
func PathMatches(_ context.Context, pattern, path string) (bool, error) {
	ok, err := doublestar.Match(pattern, filepath.ToSlash(path))
	if err != nil {
		return false, fmt.Errorf("path.match %q: %w", pattern, err)
	}
	return ok, nil
}

// PathMatchAny reports whether path matches any of the patterns.
func PathMatchAny(ctx context.Context, patterns []string, path string) (bool, error) {
	for _, pattern := range patterns {
		ok, err := PathMatches(ctx, pattern, path)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// PathIsAbs reports whether path is absolute.
func PathIsAbs(_ context.Context, path string) (bool, error) {
	return filepath.IsAbs(path), nil
}

// PathExpandUser expands a leading ~ to the current user's home directory. A
// bare "~" or "~/..." is expanded; "~other" (another user) is left untouched
// since resolving it needs a user lookup the host deliberately doesn't do.
func PathExpandUser(_ context.Context, path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("path.expand_user: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// PathJoin joins parts with the OS separator and cleans the result.
func PathJoin(_ context.Context, parts []string) (string, error) {
	return filepath.Join(parts...), nil
}

// PathWithin reports whether path equals dir or lies under it. A path with
// no relative form to dir, such as an absolute one against a relative one, is
// not within.
func PathWithin(_ context.Context, dir, path string) (bool, error) {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	inside := rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
	return err == nil && inside, nil
}
