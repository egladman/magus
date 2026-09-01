package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/project"
)

// fixtureDirs are directories whose contents are DATA for a test, not a unit
// anything imports. New ones appear constantly during ordinary test work, and
// advising on each would teach the reader to skip the advisory that matters.
var fixtureDirs = []string{"testdata", "fixtures", "__snapshots__"}

// adviseNewSourceDir fires when a write would create a new source directory, or ""
// when it would not.
//
// A DIRECTORY, not a file, and not a Go package. Every language magus builds draws
// its import boundary at a directory. Go calls the unit a package, Python a package,
// Rust a module, so the directory is the one thing that means "a unit others will
// import" in all of them. Keying on a source extension would have meant
// a hardcoded language list that goes stale the first time magus grows a spell for a
// language nobody added to it.
//
// The gap this closes is a measured one. magus-architecture-review's description already
// covers "deciding where new code belongs", and its own text says to prefer folding
// into an existing mechanism over adding one, but nothing LOADS it at the moment
// the decision is made. Measured: a directory was added for a helper with two
// callers that belonged in an existing one, with the skill installed and never read.
func adviseNewSourceDir(path string) string {
	dir, ok := workspaceRelativeDir(path)
	if !ok {
		return ""
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "" || seg == "." {
			continue
		}
		// Pruned, hidden, and fixture trees: nobody is choosing a boundary there.
		if project.IsIgnoreDir(seg) || slices.Contains(fixtureDirs, seg) {
			return ""
		}
	}
	// Anything already here means the directory is not new: a sibling file, or a
	// subdirectory, which is what a tree of packages like internal/ looks like.
	//
	// The write's own file is NOT excused. This runs before the write, so on creation
	// it does not exist yet; if it does exist, this is an edit to a directory that
	// already had it.
	if entries, err := os.ReadDir(filepath.FromSlash(dir)); err == nil {
		if len(entries) > 0 {
			return ""
		}
	} else if !os.IsNotExist(err) {
		return "" // unreadable: say nothing rather than guess
	}
	return newSourceDirAdvice(dir)
}

// workspaceRelativeDir returns the slash-separated directory of path relative to the
// working directory, and whether it is inside it and not the root itself.
//
// Relative FIRST, then scanned. The host sends an absolute path, and this workspace
// is routinely checked out under a dot-directory (.claude/worktrees/<name>), so
// scanning the absolute form finds `.claude`, calls it hidden, and silently disables
// the rule in the layout the repo's own workflow uses. That is how this shipped inert
// with a green suite: every test passed a relative path.
func workspaceRelativeDir(path string) (string, bool) {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return "", false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	abs := clean
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	rel, err := filepath.Rel(cwd, filepath.Dir(abs))
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	// The workspace root is not a boundary anyone is choosing, and anything above it
	// is not this workspace's business.
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

func newSourceDirAdvice(dir string) string {
	return "magus workspace: `" + dir + "` holds nothing yet, so this write creates a NEW DIRECTORY: whatever your language calls an importable unit (package, module, crate).\n" +
		"Before it exists, check that it has to. Is there an existing directory whose stated purpose already covers this, and would folding it there serve callers better than a new import? A boundary that exists for one helper with two callers is one nobody asked for, and it is far cheaper to not create than to remove later.\n" +
		"Load the magus-architecture-review skill. It answers this from the workspace's own dependency and churn data rather than from taste."
}
