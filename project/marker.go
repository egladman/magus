package project

import (
	"slices"
	"strings"
)

// IgnoreDirs lists non-dot directory names skipped by discovery and watch
// (matched at any depth). Dot-directories are skipped separately by IsIgnoreDir
// without being listed here - see there.
//
// SCOPE: this is the PRE-resolution default. Discovery and watch run before any
// spell is known, so they cannot ask a project's spells which dirs are noise; this
// list is the standing answer for the big build/dependency trees that must be
// pruned fast on every walk. Language spells ALSO declare their ecosystem's dirs via
// mgs_listIgnoreDirs (go->vendor, ts->node_modules, ...), which the POST-resolution
// input-hashing walk unions on top - so vendor/node_modules/target appear in both
// places on purpose. Do NOT remove a dependency dir here on the theory that "the spell
// owns it now": the spell only reaches the hash walk, and dropping it here would make
// discovery descend into that tree for every project the spell does not resolve.
// The two sets are not nested (this one carries gen, which no spell declares; the
// python spell declares __pycache__, deliberately left out here - missing it costs
// only a little discovery-walk time, and adding it would change hash keys for broad-
// glob projects that sit near a __pycache__).
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
