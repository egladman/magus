package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

func cleanUsage(fs *flag.FlagSet) {
	fmt.Fprint(os.Stderr, cleanHelp)
	fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
	printOwnDefaults(fs)
}

const cleanHelp = `Usage: magus clean [flags] [project...]

Remove declared Outputs for selected projects.
With no project args, the cwd project (or all) is selected.

This removes the files matched by each project's declared Outputs globs
- the same files the cache snapshots and replays on cache hits. Whether
each one is regenerable is the declaration's claim, not something clean
verifies: a file magus only modifies belongs in ctx.writesFiles(..., editsExistingSource: true), which clean
never removes. Nor does it remove an output the VCS tracks: that file is
committed, and deleting it would dirty the tree.
Pass --dry-run (global flag) to preview before trusting one.
Use --cache to also drop the magus cache entries, forcing a full rebuild.

`

// cleanCommand implements `magus clean [flags] [project...]`.
// It removes files matching each selected project's declared Outputs globs
// (the regenerable build artifacts), keeping any the VCS tracks. With --cache
// it also invalidates the cached build entries for those projects.
//
// Pass --dry-run (the global flag) to preview without deleting.
func cleanCommand(ctx context.Context, root string, args []string) error {
	var cf *flaggen.CleanFlags
	projectArgs, err := commandParse("clean", args, func(fs *flag.FlagSet) {
		cf = flaggen.BindClean(fs)
		fs.Usage = func() { cleanUsage(fs) }
	})
	if err != nil {
		return err
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	targets, _, err := interactive.ResolveTargets(ctx, m, types.Target{Name: "clean"}, interactive.RunSelection{Projects: projectArgs, Cwd: clientCurrentWorkingDirectory(ctx)})
	if err != nil {
		return err
	}
	dryRun := globalCfg.DryRun
	// Empty lists, never null, so -o json has the same shape whatever was selected.
	report := types.CleanReport{Removed: []string{}, Tracked: []string{}, DryRun: dryRun}
	if len(targets) == 0 {
		slog.With(attr.Component("clean")).InfoContext(ctx, "no projects selected")
	} else {
		projects := m.ResolveProjects(targets)
		cleaned, err := m.CleanOutputs(ctx, projects, dryRun)
		if err != nil {
			return fmt.Errorf("clean: %w", err)
		}
		if cf.Cache && !dryRun {
			if err := m.CleanCache(ctx, projects...); err != nil {
				return fmt.Errorf("clean --cache: %w", err)
			}
			slog.With(attr.Component("clean")).InfoContext(ctx, "invalidated cache", slog.Int("projects", len(projects)))
		}
		report.Removed = append(report.Removed, cleaned.Removed...)
		report.Tracked = append(report.Tracked, cleaned.Tracked...)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, report)
	case outputName:
		return emitNames(os.Stdout, report.Removed)
	case outputText:
	}

	for _, path := range report.Removed {
		if dryRun {
			fmt.Printf("[dry-run] would remove %s\n", path)
		} else {
			fmt.Printf("removed %s\n", path)
		}
	}
	if dryRun {
		for _, path := range report.Tracked {
			fmt.Printf("[dry-run] would keep %s (tracked)\n", path)
		}
	}
	if len(report.Tracked) > 0 {
		slog.With(attr.Component("clean")).InfoContext(ctx, "kept outputs the VCS tracks", slog.Int("files", len(report.Tracked)))
	}
	return nil
}
