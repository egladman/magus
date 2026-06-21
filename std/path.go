package std

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:generate go run ../cmd/magus-scribe bindings -module path -lang buzz -out ../host/gen/path.go

func init() { Register(Path) }

// Path is the "path" host module: pure path-string math (no filesystem access).
// fs.* already covers dirname/basename/join/glob; these are the remaining
// computations a spell otherwise has to shell out to `realpath`/`readlink -f`
// for — absolute/relative resolution, lexical cleaning, and ~ expansion. Nothing
// here touches disk, so no sandbox check applies; reading/writing the resolved
// path still goes through the sandbox-aware fs.* calls.
var Path = Module{
	Name: "path",
	Doc:  "Pure path-string math: abs, rel, clean, is_abs, expand_user.",
	Methods: []Method{
		{
			Name:    "abs",
			Doc:     "Return the absolute form of path, resolved against the current directory and lexically cleaned.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    PathAbs,
		},
		{
			Name:    "rel",
			Doc:     "Return a relative path from base to target; errors if no relative path exists.",
			Args:    []Arg{{Name: "base", Type: TypeString}, {Name: "target", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    PathRel,
		},
		{
			Name:    "clean",
			Doc:     "Return the shortest lexically-equivalent path (resolves . and .., collapses separators).",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    PathClean,
		},
		{
			Name:    "is_abs",
			Doc:     "Report whether path is absolute.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    PathIsAbs,
		},
		{
			Name:    "expand_user",
			Doc:     "Expand a leading ~ (or ~/...) to the current user's home directory; other paths are returned unchanged.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    PathExpandUser,
		},
	},
}

// PathAbs returns the cleaned absolute form of path (resolved against the cwd).
func PathAbs(_ context.Context, path string) (string, error) {
	abs, err := filepath.Abs(path)
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

// PathClean returns the shortest lexically-equivalent path.
func PathClean(_ context.Context, path string) (string, error) {
	return filepath.Clean(path), nil
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
