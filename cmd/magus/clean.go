package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/logattr"
	"github.com/egladman/magus/types"
)

// cleanCmd implements `magus clean [flags] [project...]`.
// It removes files matching each selected project's declared Outputs globs
// (the regenerable build artifacts), keeping any the VCS tracks. With --cache
// it also invalidates the cached build entries for those projects.
//
// Pass --dry-run (the global flag) to preview without deleting.
func cleanCmd(ctx context.Context, root string, args []string) error {
	var cf *gen.CleanFlags
	projectArgs, err := cmdParse("clean", args, func(fs *flag.FlagSet) {
		cf = gen.BindClean(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus clean [flags] [project...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Remove declared Outputs for selected projects.")
			fmt.Fprintln(os.Stderr, "With no project args, the cwd project (or all) is selected.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "This removes the files matched by each project's declared Outputs globs")
			fmt.Fprintln(os.Stderr, "- the same files the cache snapshots and replays on cache hits. Whether")
			fmt.Fprintln(os.Stderr, "each one is regenerable is the declaration's claim, not something clean")
			fmt.Fprintln(os.Stderr, "verifies: a file magus only modifies belongs in ctx.modifiesExistingFiles, which clean")
			fmt.Fprintln(os.Stderr, "never removes. Nor does it remove an output the VCS tracks: that file is")
			fmt.Fprintln(os.Stderr, "committed, and deleting it would dirty the tree.")
			fmt.Fprintln(os.Stderr, "Pass --dry-run (global flag) to preview before trusting one.")
			fmt.Fprintln(os.Stderr, "Use --cache to also drop the magus cache entries, forcing a full rebuild.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	targets, _, err := resolveTargets(ctx, m, types.Target{Name: "clean"}, runSelection{projects: projectArgs, cwd: clientCwd(ctx)})
	if err != nil {
		return err
	}
	dryRun := globalCfg.DryRun
	// Empty lists, never null, so -o json has the same shape whatever was selected.
	report := types.CleanReport{Removed: []string{}, Tracked: []string{}, DryRun: dryRun}
	if len(targets) == 0 {
		logattr.For("clean").InfoContext(ctx, "no projects selected")
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
			logattr.For("clean").InfoContext(ctx, "invalidated cache", slog.Int("projects", len(projects)))
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
		return emitNames(report.Removed)
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
		logattr.For("clean").InfoContext(ctx, "kept outputs the VCS tracks", slog.Int("files", len(report.Tracked)))
	}
	return nil
}
