package project

import (
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
