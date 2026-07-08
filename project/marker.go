package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// IgnoreDirs lists non-dot directory names skipped by discovery and watch
// (matched at any depth). Dot-directories are skipped separately by IsIgnoreDir
// without being listed here - see there.
var IgnoreDirs = []string{
	"vendor",       // Go vendored deps
	"node_modules", // pnpm/npm
	"target",       // Rust build artifacts
	"gen",          // common generated-source convention: machine-written code, never a discoverable project
}

// IsIgnoreDir reports whether a directory named name is skipped during discovery
// and watch. Any dot-directory is skipped: VCS, editor, and tool metadata (.git,
// .hg, .magus, .idea, .vscode, .claude and its worktree checkouts, ...) never
// holds a discoverable magus project or spell. Other names must be a well-known
// build or dependency directory. There is deliberately no opt-in for a dot-dir
// project today; add a config allowlist if a real workspace ever needs one.
func IsIgnoreDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return slices.Contains(IgnoreDirs, name)
}

// IsNestedWorktree reports whether dir is a linked git worktree: its .git is a
// FILE whose gitdir points under another repo's .git/worktrees/. Such a dir is a
// second checkout of the same repo, so descending into it re-discovers every
// project and spell, which then shadow the originals (MGS1002). Detection is
// structural, so a worktree anywhere (not just under a skipped dot-dir) is caught
// without naming any particular tool. A submodule's gitdir points under
// .git/modules/ instead, so submodules stay discoverable.
func IsNestedWorktree(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false // absent, or a directory (the main checkout) - either way not a linked worktree
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return false
	}
	return strings.Contains(filepath.ToSlash(strings.TrimSpace(rest)), "/.git/worktrees/")
}
