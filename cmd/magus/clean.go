package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/types"
)

// cleanCmd implements `magus clean [flags] [project...]`.
// It removes files matching each selected project's declared Outputs globs
// (the regenerable build artifacts). With --cache it also invalidates the
// cached build entries for those projects.
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
			fmt.Fprintln(os.Stderr, "never removes. Pass --dry-run (global flag) to preview before trusting one.")
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

	targets, _, err := resolveTargets(m, types.Target{Name: "clean"}, projectArgs, clientCwd(ctx))
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		slog.InfoContext(ctx, "clean: no projects selected")
		return nil
	}

	projects := m.ResolveProjects(targets)

	dryRun := globalCfg.DryRun
	removed, err := m.CleanOutputs(ctx, projects, dryRun)
	if err != nil {
		return fmt.Errorf("clean: %w", err)
	}

	for _, path := range removed {
		if dryRun {
			fmt.Printf("[dry-run] would remove %s\n", path)
		} else {
			fmt.Printf("removed %s\n", path)
		}
	}

	if cf.Cache && !dryRun {
		if err := m.CleanCache(ctx, projects...); err != nil {
			return fmt.Errorf("clean --cache: %w", err)
		}
		slog.InfoContext(ctx, "clean: invalidated cache", slog.Int("projects", len(projects)))
	}

	return nil
}
