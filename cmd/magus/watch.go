package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/file/watch"
)

// ignoreFlag accumulates repeated --ignore values; satisfies flag.Value.
type ignoreFlag struct {
	patterns []watch.IgnorePattern
}

func (f *ignoreFlag) String() string {
	parts := make([]string, len(f.patterns))
	for i, p := range f.patterns {
		parts[i] = string(p.Type) + "=" + p.Pattern
	}
	return strings.Join(parts, ",")
}

func (f *ignoreFlag) Set(value string) error {
	p, err := watch.ParsePattern(value)
	if err != nil {
		return err
	}
	f.patterns = append(f.patterns, p)
	return nil
}

// watchCmd implements `magus watch`; output format matches `git diff --name-only` for pipe compatibility.
func watchCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	debounce := fs.Duration("debounce", 200*time.Millisecond, "quiet window before emitting a batch")
	initial := fs.Bool("initial", true, "emit an --all batch on startup before watching")
	null := fs.Bool("null", false, "NUL-separate paths and double-NUL between batches")
	backend := fs.String("backend", "fsnotify", "notification backend: fsnotify or poll")
	var ignores ignoreFlag
	fs.Var(&ignores, "ignore", "ignore pattern; repeatable. Form: type=<glob|regex|literal>,pattern=<value>")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus watch [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Emit changed file paths to stdout. Pair with `magus affected --stdin`:")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  magus watch | magus affected --stdin build")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Ignore examples:")
		fmt.Fprintln(os.Stderr, "  --ignore type=glob,pattern='**/scratch/*'")
		fmt.Fprintln(os.Stderr, "  --ignore type=regex,pattern='\\.tmp$'")
		fmt.Fprintln(os.Stderr, "  --ignore type=literal,pattern='bazel-out/[k8]'")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	var be watch.Backend
	switch *backend {
	case "fsnotify", "":
		be = watch.FsnotifyBackend
	case "poll":
		be = watch.PollBackend
	default:
		return fmt.Errorf("magus watch: unknown backend %q (choose: fsnotify, poll)", *backend)
	}

	// Collect output globs from all registered projects to avoid
	// build → output-write → rebuild loops.
	var outputGlobs []string
	var projectIgnores []watch.IgnorePattern
	for _, p := range ws.All() {
		outputGlobs = append(outputGlobs, p.Outputs...)
		projectIgnores = append(projectIgnores, p.WatchIgnores...)
	}

	// Layered predicate (first match wins, OR semantics):
	//   1. BuiltinIgnore  — VCS metadata, magus cache, editor temps.
	//   2. OutputsIgnore  — per-project Outputs globs (rebuild-loop guard).
	//   3. Config ignores — workspace-wide watch.ignore entries from magus.yaml.
	//   4. Project ignores — magus.WatchIgnore() entries from magusfiles.
	//   5. CLI ignores    — --ignore flags, highest user-supplied tier.
	userPatterns := append([]watch.IgnorePattern{}, rc.watchIgnores...)
	userPatterns = append(userPatterns, projectIgnores...)
	userPatterns = append(userPatterns, ignores.patterns...)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	w, err := watch.New(
		ctx,
		watch.WithRoot(ws.Root()),
		watch.WithIgnore(watch.Compose(
			watch.BuiltinIgnore,
			watch.OutputsIgnore(ws.Root(), outputGlobs),
			watch.IgnorePatterns(ws.Root(), userPatterns),
		)),
		watch.WithDebounce(*debounce),
		watch.WithBackend(be),
	)
	if err != nil {
		return fmt.Errorf("magus watch: %w", err)
	}
	defer func() { _ = w.Close() }()

	sep, batchSep := "\n", "\n"
	if *null {
		sep, batchSep = "\x00", "\x00\x00"
	}

	out := bufio.NewWriter(os.Stdout)

	// writeBatch converts each absolute path from the watcher to a
	// workspace-relative slash-separated path before emitting.
	// Sentinel tokens (e.g. magus.StreamAllSentinel) and any path that
	// escapes the workspace root are passed through verbatim.
	writeBatch := func(paths []string) {
		for _, p := range paths {
			rel := toWorkspaceRel(ws.Root(), p)
			fmt.Fprint(out, rel, sep)
		}
		fmt.Fprint(out, batchSep)
		_ = out.Flush() // critical: stdout is block-buffered when piped
	}

	if *initial {
		// Sentinel consumed by `magus affected --stdin` to trigger a full build.
		writeBatch([]string{magus.StreamAllSentinel})
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-w.Errors():
			slog.ErrorContext(ctx, "watch", slog.String("error", err.Error()))
		case batch, ok := <-w.Events():
			if !ok {
				return nil
			}
			writeBatch(batch.Paths)
		}
	}
}

func toWorkspaceRel(wsRoot, p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(wsRoot, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return filepath.ToSlash(rel)
}
