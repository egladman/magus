package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/egladman/magus/types"
)

// cleanOutput is the structured view of a clean run. DryRun rides on the model
// because the removed list is otherwise identical either way, and a caller parsing
// json has no other way to tell a preview from a deletion.
type cleanOutput struct {
	DryRun  bool     `json:"dry_run"           yaml:"dry_run"`
	Count   int      `json:"count"             yaml:"count"`
	Removed []string `json:"removed,omitempty" yaml:"removed,omitempty"`
	// CacheInvalidated is the number of projects whose cache entries were dropped
	// by --cache; zero when the flag was not passed. Always emitted: json/v2 reads
	// omitempty as "omit JSON-empty" (null, "", [], {}), so it would not drop a 0
	// anyway, and a caller checking whether --cache took effect wants the field
	// present either way.
	CacheInvalidated int `json:"cache_invalidated" yaml:"cache_invalidated"`
}

// cleanCmd implements `magus clean [flags] [project...]`.
// It removes files matching each selected project's declared Outputs globs
// (the regenerable build artifacts). With --cache it also invalidates the
// cached build entries for those projects.
//
// Pass --dry-run (the global flag) to preview without deleting.
func cleanCmd(ctx context.Context, root string, args []string) error {
	var cacheFlag *bool
	projectArgs, err := cmdParse("clean", args, func(fs *flag.FlagSet) {
		cacheFlag = fs.Bool("cache", false, "Also invalidate magus cache entries for the selected projects")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus clean [flags] [project...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Remove declared Outputs (regenerable build artifacts) for selected projects.")
			fmt.Fprintln(os.Stderr, "With no project args, the cwd project (or all) is selected.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "This removes the files matched by each project's declared Outputs globs")
			fmt.Fprintln(os.Stderr, "- the same files the cache snapshots and replays on cache hits.")
			fmt.Fprintln(os.Stderr, "Use --cache to also drop the magus cache entries, forcing a full rebuild.")
			fmt.Fprintln(os.Stderr, "Use --dry-run (global flag) to preview what would be removed.")
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

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	dryRun := globalCfg.DryRun
	removed, err := m.CleanOutputs(ctx, projects, dryRun)
	if err != nil {
		return fmt.Errorf("clean: %w", err)
	}
	out := cleanOutput{DryRun: dryRun, Count: len(removed), Removed: removed}

	// Cache invalidation happens BEFORE rendering so that every -o format does the
	// same work: the structured arms return directly, and doing this after them
	// would quietly make `clean --cache -o json` skip the invalidation.
	if *cacheFlag && !dryRun {
		if err := m.CleanCache(ctx, projects...); err != nil {
			return fmt.Errorf("clean --cache: %w", err)
		}
		out.CacheInvalidated = len(projects)
		slog.InfoContext(ctx, "clean: invalidated cache", slog.Int("projects", len(projects)))
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, path := range out.Removed {
			fmt.Println(path)
		}
		return nil
	}

	for _, path := range out.Removed {
		if dryRun {
			fmt.Printf("[dry-run] would remove %s\n", path)
		} else {
			fmt.Printf("removed %s\n", path)
		}
	}

	return nil
}
