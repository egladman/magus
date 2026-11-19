package std

import (
	_ "embed"

	diagramsrc "github.com/egladman/magus/libs/diagram"
	diskmod "github.com/egladman/magus/internal/disk"
	jobsrc "github.com/egladman/magus/internal/job"
	resultsrc "github.com/egladman/magus/libs/result"
)

// The Buzz-implemented half of the standard library. Each source is embedded and
// listed like a Go module; see source_module.go for why a stdlib module would be
// written in Buzz at all, and for the performance trade that says when it should
// not be.

//go:embed lib/lcov.buzz
var lcovSource string

var sourceBuiltin = []SourceModule{
	{
		Name:   "lcov",
		Doc:    "LCOV coverage reports: the percentage a badge or a floor gate shows, and the line-level merge that keeps it true across multiple test processes.",
		Source: lcovSource,
	},
	{
		Name:   "diagram",
		Path:   "magus/diagram",
		Doc:    "Architecture diagrams drawn from graph records: boxes and groups over Dir records, coverage by scope, edges derived from imports and declared calls.",
		Source: diagramsrc.Source,
	},
	{
		Name:   "modeltiers",
		Path:   "magus/modeltiers",
		Doc:    "The default model-tier declaration: the ModelTier enum, cheapest first, its default, and the bounds and skill variant of each tier.",
		Source: jobsrc.ModelTiersSource,
	},
	{
		Name:   "diskwatch",
		Path:   "magus/diskwatch",
		Doc:    "The cheap half of disk reclaiming: the per-user store of free-space samples, what a sample asks for, the fence a cache trim keeps, and the lines a person reads.",
		Source: diskmod.DiskWatchSource,
	},
	{
		Name:   "reclaimdisk",
		Path:   "magus/reclaimdisk",
		Doc:    "Plan and take back the disk a repository's git worktrees and merged agent branches hold, never a dirty, unpushed, leased, busy or locked worktree; tick is the one decider every disk trigger calls, and trims only what a spell gives a budget.",
		Source: diskmod.ReclaimDiskSource,
	},
	{
		Name:   resultsrc.ModulePath,
		Doc:    "A value or the Diagnostic that stopped it: Result, the Buzz form of types.Result, with ok, fail, and caught for what a catch hands back.",
		Source: resultsrc.Source,
	},
}
