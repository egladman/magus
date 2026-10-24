package watch

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// BuiltinIgnore returns true for paths that should never trigger a
// rebuild: VCS metadata, magus cache, editor temporaries, and common
// output directories. The ignore-dir list is the canonical
// project.IgnoreDirs (single source of truth shared with the project
// discovery walker, so the two can never drift).
//
// It judges only the final path element. The watcher's walk prunes a directory this
// ignores, so nothing beneath it is ever watched, which is what applies the rule at any
// depth under the root. Checking every segment of the absolute path instead also judged the
// directories ABOVE the root: a checkout under ~/.cache/magus/checkouts or
// <repo>/.claude/worktrees had a dot segment in every path, and the watcher went silent.
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

	return project.IsIgnoreDir(base)
}

// RelativeIgnore lifts an ignore predicate so it judges a path by where it sits UNDER root
// rather than by its absolute spelling. A path outside root is ignored: this watcher has no
// business with it either way, and a predicate cannot honestly place it.
//
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

// OutputsIgnore returns an ignore predicate that skips any path one of the declared output
// globs (workspace-rooted, relative to wsRoot) claims. Pass every project's outputs to
// prevent the build → output-write → rebuild loop. A file an output's exclusions carve out
// is a source and still fires, and so is the directory holding one: ignoring a directory
// prunes the watch beneath it.
func OutputsIgnore(wsRoot string, outputGlobs []types.Glob) func(string) bool {
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
			if glob.Match(rel) && !slices.ContainsFunc(glob.Except, func(e string) bool { return reachesUnder(e, rel) }) {
				return true
			}
		}
		return false
	}
}

// reachesUnder reports whether pattern can match a path beneath dir: every match starts
// with the pattern's text before its first metacharacter.
func reachesUnder(pattern, dir string) bool {
	lead := pattern
	if i := strings.IndexAny(pattern, `*?[{\`); i >= 0 {
		lead = pattern[:i]
	}
	return strings.HasPrefix(dir+"/", lead) || strings.HasPrefix(lead, dir+"/")
}
