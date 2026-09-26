package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus"
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
		fmt.Fprintln(os.Stderr, "       magus config cache export --toolchain go [--to <file> | --remote] [--used-within <duration>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Write the entire build cache to a gzip-compressed tar archive, for")
		fmt.Fprintln(os.Stderr, "persisting across CI runs via artifact upload/download. Writes to stdout")
		fmt.Fprintln(os.Stderr, "when --to is omitted.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "With --toolchain, export the toolchain's own caches instead (for go, GOCACHE")
		fmt.Fprintln(os.Stderr, "and GOMODCACHE as `go env` reports them), signed with MAGUS_CACHE_SIGNING_KEY,")
		fmt.Fprintln(os.Stderr, "so a magus miss elsewhere recompiles only what changed. --remote stores the")
		fmt.Fprintln(os.Stderr, "bundle in the remote tier under today's key; the day's first bundle stands.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if ef.Toolchain == "" && ef.Remote {
		return usagef("magus config cache export: --remote needs --toolchain; build entries reach the remote tier on their own")
	}
	if ef.Toolchain != "" && ef.Remote && ef.To != "" {
		return usagef("magus config cache export: --remote and --to are exclusive")
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	if ef.Toolchain != "" {
		return configCacheExportToolchain(ctx, m, ef)
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
	imf := gen.BindConfigCacheImport(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache import [<file>]")
		fmt.Fprintln(os.Stderr, "       magus config cache import --toolchain go [<file> | --remote]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Restore the build cache from a gzip-compressed tar archive produced by")
		fmt.Fprintln(os.Stderr, "`magus config cache export`. Existing entries are overwritten. Reads")
		fmt.Fprintln(os.Stderr, "stdin when no file is given.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "With --toolchain, restore a toolchain bundle into this machine's own caches,")
		fmt.Fprintln(os.Stderr, "only once its signature verifies against cache.remote.trusted_keys. --remote")
		fmt.Fprintln(os.Stderr, "takes the newest verified bundle of the last week: one for this module set,")
		fmt.Fprintln(os.Stderr, "else one for this toolchain. A bundle that fails verification is refused and")
		fmt.Fprintln(os.Stderr, "named; finding none leaves builds cold and exits 0.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if imf.Toolchain == "" && imf.Remote {
		return usagef("magus config cache import: --remote needs --toolchain; build entries are read from the remote tier on their own")
	}
	if imf.Remote && len(fs.Args()) > 0 {
		return usagef("magus config cache import: --remote reads no file")
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	if imf.Toolchain != "" && imf.Remote {
		// Each refusal is already logged as cache.toolchain.refused.
		res, err := m.RestoreToolchainCache(ctx, imf.Toolchain)
		if err != nil {
			return fmt.Errorf("config cache import: %w", err)
		}
		if res.Inactive {
			fmt.Fprintln(os.Stderr, "magus config cache import: the remote backend is not active here; builds start cold")
			return nil
		}
		if res.Key == "" {
			fmt.Fprintf(os.Stderr, "magus config cache import: no verified %s toolchain bundle in the remote tier for the last week (%d refused); builds start cold\n", res.Tool, len(res.Refused))
			return nil
		}
		match := "this module set"
		if !res.Exact {
			match = "another module set, same toolchain"
		}
		fmt.Fprintf(os.Stderr, "magus config cache import: restored %s (%s): %d files, %s, %s downloaded, %d already present; %s\n",
			res.Key, match, res.Files, fmtBytes(res.Bytes), fmtBytes(res.Transferred), res.Skipped, strings.Join(res.Dirs, " "))
		return nil
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

	if imf.Toolchain != "" {
		res, err := m.ImportToolchainCache(ctx, imf.Toolchain, r)
		if err != nil {
			return fmt.Errorf("config cache import: refused the %s toolchain bundle: %w", imf.Toolchain, err)
		}
		fmt.Fprintf(os.Stderr, "magus config cache import: restored %d files, %s, %d already present; %s\n",
			res.Files, fmtBytes(res.Bytes), res.Skipped, strings.Join(res.Dirs, " "))
		return nil
	}

	if err := m.ImportCache(ctx, r); err != nil {
		return fmt.Errorf("config cache import: %w", err)
	}
	fmt.Fprintln(os.Stderr, "magus config cache import: restored cache")
	return nil
}

func configCacheExportToolchain(ctx context.Context, m *magus.Magus, ef *gen.ConfigCacheExportFlags) error {
	if ef.Remote {
		res, err := m.SaveToolchainCache(ctx, ef.Toolchain, ef.UsedWithin)
		if err != nil {
			return fmt.Errorf("config cache export: %w", err)
		}
		if res.Present {
			fmt.Fprintf(os.Stderr, "magus config cache export: %s is already stored; nothing to do\n", res.Key)
			return nil
		}
		fmt.Fprintf(os.Stderr, "magus config cache export: stored %s: %d files, %s, %s uploaded, %d unused entries trimmed; %s\n",
			res.Key, res.Files, fmtBytes(res.Bytes), fmtBytes(res.Transferred), res.Skipped, strings.Join(res.Dirs, " "))
		return nil
	}
	w := io.Writer(os.Stdout)
	var f *os.File
	if ef.To != "" {
		var err error
		if f, err = os.Create(ef.To); err != nil {
			return fmt.Errorf("config cache export: %w", err)
		}
		w = f
	}
	res, err := m.ExportToolchainCache(ctx, ef.Toolchain, w, ef.UsedWithin)
	if f != nil {
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		return fmt.Errorf("config cache export: %w", err)
	}
	fmt.Fprintf(os.Stderr, "magus config cache export: wrote %d files, %s, %d unused entries trimmed; %s\n",
		res.Files, fmtBytes(res.Bytes), res.Skipped, strings.Join(res.Dirs, " "))
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
