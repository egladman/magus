package spellruntime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
)

// Package-manager substitution. A spell whose ops fork a JS package manager
// records ONE bin (pnpm for the typescript spell) because op handlers are
// reduced to static {cmd,args} at resolve time (see recordOp) and cannot read
// the project. The swap therefore happens engine-side at fork time: a spell
// opts in by exporting mgs_getPackageManagerBin (its way of saying "my ops'
// bin IS the project package manager"), and the runner replaces a recorded
// npm/pnpm/yarn/bun bin with the manager the project actually uses.

// PackageManagers is the closed set of substitutable JS package-manager bins.
// The order is presentation order for error messages, not precedence.
var PackageManagers = []string{"npm", "pnpm", "yarn", "bun"}

// KnownPackageManager reports whether bin is one of the substitutable package
// managers. A spell op whose bin is anything else (sh, node) is never touched.
func KnownPackageManager(bin string) bool {
	return slices.Contains(PackageManagers, bin)
}

// lockfilesByManager maps lockfile presence to the manager that wrote it, in
// check order: the first file present in the project dir wins. bun.lock is
// bun's current text lockfile, bun.lockb its legacy binary one.
var lockfilesByManager = []struct{ file, manager string }{
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"bun.lock", "bun"},
	{"bun.lockb", "bun"},
	{"package-lock.json", "npm"},
	{"npm-shrinkwrap.json", "npm"},
}

// ResolvePackageManager returns the package manager the project at dir uses:
// the explicit magus.project "package_manager" option when set (validated at
// load), else the package.json packageManager field (the corepack standard,
// "pnpm@9.1.0" -> pnpm), else the manager whose lockfile is present, else
// fallback (the bin the spell recorded).
//
// Every determinant already feeds the cache key - the magusfile, package.json,
// and each lockfile are hashed project sources - so switching managers by any
// of these routes is a cache miss without this function touching the key.
func ResolvePackageManager(explicit, dir, fallback string) string {
	if explicit != "" {
		return explicit
	}
	if pm := manifestPackageManager(filepath.Join(dir, "package.json")); pm != "" {
		return pm
	}
	for _, l := range lockfilesByManager {
		if _, err := os.Stat(filepath.Join(dir, l.file)); err == nil {
			return l.manager
		}
	}
	return fallback
}

// manifestPackageManager reads the packageManager field from the package.json
// at path, returning the manager name before the "@" version suffix. An
// unreadable file, a malformed document, or a value outside the known set all
// return "" so detection falls through to the lockfile rather than failing the
// run over a field magus does not own.
func manifestPackageManager(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var manifest struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ""
	}
	pm, _, _ := strings.Cut(manifest.PackageManager, "@")
	if !KnownPackageManager(pm) {
		return ""
	}
	return pm
}

// SubstitutePackageManager rewrites a recorded package-manager command for the
// resolved manager pm: the bin is replaced, and because bun spells the exec
// verb as x (run/audit/install are spelled identically across all four), a
// leading "exec" arg is rewritten when pm is bun. No other argv rewriting
// happens. args is never mutated; a rewrite returns a fresh slice.
func SubstitutePackageManager(bin string, args []string, pm string) (string, []string) {
	if pm == "" || pm == bin {
		return bin, args
	}
	if pm == "bun" && len(args) > 0 && args[0] == "exec" {
		args = slices.Clone(args)
		args[0] = "x"
	}
	return pm, args
}
