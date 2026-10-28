package std

import (
	_ "embed"

	figuresrc "github.com/egladman/magus/libs/figure"
)

// The Buzz-implemented half of the standard library. Each source is embedded and
// listed like a Go module; see source_module.go for why a stdlib module would be
// written in Buzz at all, and for the performance trade that says when it should
// not be.

//go:embed lib/lcov.buzz
var lcovSource string

//go:embed lib/modeltiers.buzz
var modelTiersSource string

//go:embed lib/diskwatch.buzz
var diskWatchSource string

//go:embed lib/reclaimdisk.buzz
var reclaimDiskSource string

var sourceBuiltin = []SourceModule{
	{
		Name:   "lcov",
		Doc:    "LCOV coverage reports: the percentage a badge or a floor gate shows, and the line-level merge that keeps it true across multiple test processes.",
		Source: lcovSource,
	},
	{
		Name:   "figure",
		Path:   "magus/figure",
		Doc:    "Architecture figures drawn from graph records: boxes and groups over Dir records, coverage by scope, edges derived from imports and declared calls.",
		Source: figuresrc.Source,
	},
	{
		Name:   "modeltiers",
		Path:   "magus/modeltiers",
		Doc:    "The default model-tier declaration: the ModelTier enum, cheapest first, its default, and the bounds and skill variant of each tier.",
		Source: modelTiersSource,
	},
	{
		// No magus/ path: the reclaim hook imports it, and a hook may import nothing under
		// magus/, since that namespace is where the workspace readers live.
		Name:   "diskwatch",
		Doc:    "The reclaim hook's cheap half: sample free disk space into a per-user store at most once per disk.sample_every, and decide from the last two samples whether to start a reclaim chunk.",
		Source: diskWatchSource,
	},
	{
		Name:   "reclaimdisk",
		Path:   "magus/reclaimdisk",
		Doc:    "Plan and take back the disk a repository's git worktrees, merged agent branches and Go build cache hold: never a dirty, unpushed, leased, busy or locked worktree, and the cache trimmed by entry age, never emptied.",
		Source: reclaimDiskSource,
	},
}
