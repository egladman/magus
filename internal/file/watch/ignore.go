package watch

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/project"
)

// BuiltinIgnore returns true for paths that should never trigger a
// rebuild: VCS metadata, magus cache, editor temporaries, and common
// output directories. The ignore-dir list is the canonical
// project.IgnoreDirs (single source of truth shared with the project
// discovery walker, so the two can never drift).
func BuiltinIgnore(absPath string) bool {
	base := filepath.Base(absPath)

	// Editor temporaries.
	if strings.HasSuffix(base, ".swp") ||
		strings.HasSuffix(base, ".swo") ||
		strings.HasSuffix(base, "~") ||
		base == ".DS_Store" ||
		base == "4913" { // vim atomic-write sentinel
		return true
	}

	// Magus proc sockets (magus-<PID>-<hex>.sock).
	if strings.HasPrefix(base, "magus-") && strings.HasSuffix(base, ".sock") {
		return true
	}

	// Walk up the path checking each component against the canonical
	// skip-dir list.
	path := filepath.ToSlash(absPath)
	for _, seg := range strings.Split(path, "/") {
		if project.IsIgnoreDir(seg) {
			return true
		}
	}
	return false
}

// RelativeIgnore lifts an ignore predicate so it judges a path by where it sits UNDER root
// rather than by its absolute spelling. A path outside root is ignored: this watcher has no
// business with it either way, and a predicate cannot honestly place it.
//
// It exists because [BuiltinIgnore] skips any dot segment anywhere in the path, which is
// exactly right for a directory inside the tree and wrong for one the tree SITS INSIDE. An
// agent worktree at <repo>/.claude/worktrees/<name> has a dot segment above its root, so
// every file in it matched and the watcher went silent with nothing logged and nothing
// failing. Judge the path from the root down and the question becomes the one the predicate
// was always meant to be asked.
// THE ROOT ITSELF IS NEVER IGNORED, and that case is the whole watcher rather than an edge
// of it: the recursive walk asks about the root first, so ignoring it prunes the tree before
// a single directory is registered and the watcher comes up in microseconds watching
// nothing. Measured while fixing this: a walk over a 33,960-directory worktree "registered"
// in 47us and reported no event ever.
func RelativeIgnore(root string, ignore func(absPath string) bool) func(absPath string) bool {
	return func(absPath string) bool {
		rel, err := filepath.Rel(root, absPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return true
		}
		if rel == "." {
			return false
		}
		return ignore(rel)
	}
}

// Compose returns a predicate that returns true if any of preds returns true.
// An empty Compose always returns false.
func Compose(preds ...func(string) bool) func(string) bool {
	return func(path string) bool {
		for _, p := range preds {
			if p(path) {
				return true
			}
		}
		return false
	}
}

// matchGlob reports whether pattern matches path, supporting ** to match any
// number of path segments (e.g. "dist/**" matches "dist/a/b/c.js").
// It delegates to doublestar.Match which is already used by IgnorePatterns.
func matchGlob(pattern, path string) bool {
	ok, _ := doublestar.Match(pattern, path)
	return ok
}

// OutputsIgnore returns an ignore predicate that skips any path that matches
// one of the provided output glob patterns (relative to wsRoot). Pass the
// project Outputs globs from the workspace to prevent the
// build → output-write → rebuild loop.
func OutputsIgnore(wsRoot string, outputGlobs []string) func(string) bool {
	if len(outputGlobs) == 0 {
		return func(string) bool { return false }
	}
	return func(absPath string) bool {
		rel, err := filepath.Rel(wsRoot, absPath)
		if err != nil {
			return false
		}
		rel = filepath.ToSlash(rel)
		for _, glob := range outputGlobs {
			if matchGlob(glob, rel) {
				return true
			}
			// Also match if absPath is a prefix of the glob's directory, so
			// the directory itself is pruned (avoids descending into it).
			if strings.HasPrefix(rel+"/", filepath.ToSlash(glob)+"/") {
				return true
			}
		}
		return false
	}
}
