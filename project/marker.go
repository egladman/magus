package project

import "slices"

// IgnoreDirs lists directory names skipped by discovery and watch (matched at any depth).
var IgnoreDirs = []string{
	".git",
	".hg",
	".jj",
	".magus",
	".build",       // Swift / misc build dirs
	"vendor",       // Go vendored deps
	"node_modules", // pnpm/npm
	"target",       // Rust build artifacts
	"gen",          // common generated-source convention
	"starter",      // scaffolding template shipped for `magus self install`, not a real project
}

// IsIgnoreDir reports whether name is a well-known ignore dir.
func IsIgnoreDir(name string) bool {
	return slices.Contains(IgnoreDirs, name)
}
