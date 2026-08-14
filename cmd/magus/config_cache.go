package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
)

func configCacheCmd(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("config cache", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage the build cache.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  prune    remove cache entries by age/count (local, or --remote)")
		fmt.Fprintln(os.Stderr, "  export   write the cache to a gzip-tar archive (for CI artifacts)")
		fmt.Fprintln(os.Stderr, "  import   restore the cache from an archive produced by export")
		fmt.Fprintln(os.Stderr, "  key      generate / inspect remote-cache signing keys")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config cache <subcommand> -h` for flags.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return nil
	}
	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "prune":
		return configCachePrune(ctx, root, subArgs)
	case "export":
		return configCacheExport(ctx, root, subArgs)
	case "import":
		return configCacheImport(ctx, root, subArgs)
	case "key":
		return configCacheKey(ctx, root, subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return usagef("magus config cache: unknown subcommand %q", sub)
	}
}

func configCachePrune(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("config cache prune", flag.ContinueOnError)
	bindDisplayFlags(fs)
	pf := gen.BindConfigCachePrune(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache prune [--older-than <duration>] [--keep-last <count>] [--remote] [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Remove cache entries by retention policy. By default prunes the LOCAL cache:")
		fmt.Fprintln(os.Stderr, "entries whose CreatedAt is older than --older-than, then GCs orphaned blobs.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "With --remote, prunes the backend wired via magus.cache.remote(...). The remote")
		fmt.Fprintln(os.Stderr, "sweep also accepts --keep-last N (keep the newest N entries); at least one of")
		fmt.Fprintln(os.Stderr, "--older-than or --keep-last is required, and the two bounds are additive.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Duration examples: 168h (7 days), 24h (1 day), 1h30m")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if pf.Remote {
		if pf.OlderThan <= 0 && pf.KeepLast <= 0 {
			fs.Usage()
			return fmt.Errorf("magus config cache prune: --remote requires --older-than and/or --keep-last")
		}
		m, err := loadMagus(ctx, root)
		if err != nil {
			return err
		}
		if err := m.PruneRemoteCache(ctx, pf.OlderThan, pf.KeepLast, pf.DryRun); err != nil {
			return fmt.Errorf("magus config cache prune --remote: %w", err)
		}
		fmt.Fprintln(os.Stderr, "magus config cache prune: remote prune complete")
		return nil
	}

	if pf.KeepLast > 0 {
		return fmt.Errorf("magus config cache prune: --keep-last is only supported with --remote")
	}
	if pf.OlderThan <= 0 {
		fs.Usage()
		return fmt.Errorf("magus config cache prune: --older-than is required and must be positive")
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-pf.OlderThan)
	n, freed, err := m.PruneCache(ctx, cutoff, pf.DryRun)
	if err != nil {
		return fmt.Errorf("magus config cache prune: %w", err)
	}

	if pf.DryRun {
		fmt.Fprintf(os.Stderr, "magus config cache prune: would remove %d entries (%s)\n", n, fmtBytes(freed))
	} else {
		fmt.Fprintf(os.Stderr, "magus config cache prune: removed %d entries (%s freed)\n", n, fmtBytes(freed))
	}
	return nil
}

func configCacheExport(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("config cache export", flag.ContinueOnError)
	bindDisplayFlags(fs)
	// --to, not --output: the global --output selects a FORMAT on every other
	// command, and one command redefining it to mean a destination path is the
	// kind of inconsistency that makes callers stop trusting the flag set.
	ef := gen.BindConfigCacheExport(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache export [--to <file>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Write the entire build cache to a gzip-compressed tar archive, for")
		fmt.Fprintln(os.Stderr, "persisting across CI runs via artifact upload/download. Writes to stdout")
		fmt.Fprintln(os.Stderr, "when --to is omitted.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	if ef.To == "" {
		if err := m.ExportCache(ctx, os.Stdout); err != nil {
			return fmt.Errorf("config cache export: %w", err)
		}
		return nil
	}

	f, err := os.Create(ef.To)
	if err != nil {
		return fmt.Errorf("config cache export: %w", err)
	}
	if err := m.ExportCache(ctx, f); err != nil {
		_ = f.Close()
		return fmt.Errorf("config cache export: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config cache export: close %s: %w", ef.To, err)
	}
	fmt.Fprintf(os.Stderr, "magus config cache export: wrote %s\n", ef.To)
	return nil
}

func configCacheImport(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("config cache import", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache import [<file>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Restore the build cache from a gzip-compressed tar archive produced by")
		fmt.Fprintln(os.Stderr, "`magus config cache export`. Existing entries are overwritten. Reads")
		fmt.Fprintln(os.Stderr, "stdin when no file is given.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	r := io.Reader(os.Stdin)
	if rest := fs.Args(); len(rest) > 0 {
		f, err := os.Open(rest[0])
		if err != nil {
			return fmt.Errorf("config cache import: %w", err)
		}
		defer f.Close()
		r = f
	}

	if err := m.ImportCache(ctx, r); err != nil {
		return fmt.Errorf("config cache import: %w", err)
	}
	fmt.Fprintln(os.Stderr, "magus config cache import: restored cache")
	return nil
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
