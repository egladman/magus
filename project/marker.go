package project

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/vcs"
)

// IgnoreDirs lists non-dot directory names skipped by discovery and watch
// (matched at any depth). Dot-directories are skipped separately by IsIgnoreDir
// without being listed here; see there.
//
// SCOPE: this is the PRE-resolution default. Discovery and watch run before any
// spell is known, so they cannot ask a project's spells which dirs are noise; this
// list is the standing answer for the big build/dependency trees that must be
// pruned fast on every walk. Language spells ALSO declare their ecosystem's dirs via
// mgs_listIgnoreDirs (go->vendor, ts->node_modules, ...), which the POST-resolution
// input-hashing walk unions on top, so vendor/node_modules/target appear in both
// places on purpose. Do NOT remove a dependency dir here on the theory that "the spell
// owns it now": the spell only reaches the hash walk, and dropping it here would make
// discovery descend into that tree for every project the spell does not resolve.
//
// The two sets are not nested. This one carries gen, which no spell declares. It leaves
// out the python spell's __pycache__: missing it costs only a little discovery-walk
// time, and adding it would change hash keys for broad-glob projects near one.
//
// These names also prune the cache-key expansion walk (internal/cache.expandSources),
// which is the sharper edge. A target CAN legitimately declare a file inside one of
// these trees (a generated descriptor another project produces is a real input), and a
// walk that skips the directory would drop it from the key while `describe target` went
// on listing it under sources. So an EXACT, wildcard-free declaration is resolved by
// stat instead and reaches the key normally. A PATTERN gets no such exemption, because
// that is the whole point of pruning (a bare **/*.js must not hash node_modules).
// MGS1029 reports one aimed inside a pruned tree rather than letting it match nothing in
// silence. Confirm what actually keyed with `describe target --cache --inputs`.
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

// SkipsWalkDir reports whether a discovery walk skips the directory at path, named name: an
// ignored name, or a second checkout of the repository a project walk would index twice.
func SkipsWalkDir(name, path string) bool {
	return IsIgnoreDir(name) || vcs.IsSecondaryCheckout(path)
}

// CheckoutRoot is the nearest directory at or above dir holding an entry a built-in VCS
// driver claims, or false when none does. A stat error other than not-found counts as a
// marker, so the climb never passes a directory it cannot read.
func CheckoutRoot(dir string) (string, bool) {
	for cur := dir; ; {
		for _, marker := range vcs.Markers() {
			if _, err := os.Stat(filepath.Join(cur, marker)); !errors.Is(err, fs.ErrNotExist) {
				return cur, true
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}
