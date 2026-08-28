package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/types"
)

// ignoreFlag accumulates repeated --ignore values; satisfies flag.Value.
type ignoreFlag struct {
	patterns []types.IgnorePattern
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
	bindDisplayFlags(fs)
	wf := gen.BindWatch(fs)
	// --ignore stays hand-bound: it is a repeatable custom flag.Value, a shape the
	// registry has no kind for, so it is also absent from the man page.
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
	switch wf.Backend {
	case "fsnotify", "":
		be = watch.FsnotifyBackend
	case "poll":
		be = watch.PollBackend
	default:
		return fmt.Errorf("magus watch: unknown backend %q (choose: fsnotify, poll)", wf.Backend)
	}

	// Collect output globs from all registered projects to avoid
	// build → output-write → rebuild loops.
	//
	// AllOutputs, not the project-wide Outputs alone: a per-target ctx.writesFiles glob and a
	// glob another project writes into this tree both land in the same loop otherwise.
	// The cross-project case closes it fastest - the writer produces the file, watch fires
	// on the owner, the owner's depends_on drags the writer back in, and its cache hit
	// replays the very file that triggered the round.
	var outputGlobs []string
	var projectIgnores []types.IgnorePattern
	for _, p := range ws.All() {
		outputGlobs = append(outputGlobs, p.AllOutputs()...)
		projectIgnores = append(projectIgnores, p.WatchIgnores...)
	}

	// Layered predicate (first match wins, OR semantics):
	//   1. BuiltinIgnore  — VCS metadata, magus cache, editor temps.
	//   2. OutputsIgnore  — per-project Outputs globs (rebuild-loop guard).
	//   3. Config ignores — workspace-wide watch.ignore entries from magus.yaml.
	//   4. Project ignores — magus.WatchIgnore() entries from magusfiles.
	//   5. CLI ignores    — --ignore flags, highest user-supplied tier.
	userPatterns := append([]types.IgnorePattern{}, rc.watchIgnores...)
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
		watch.WithDebounce(wf.Debounce),
		watch.WithBackend(be),
	)
	if err != nil {
		return fmt.Errorf("magus watch: %w", err)
	}
	defer func() { _ = w.Close() }()

	sep, batchSep := "\n", "\n"
	if wf.Null {
		sep, batchSep = "\x00", "\x00\x00"
	}

	out := bufio.NewWriter(os.Stdout)

	// writeBatch converts each absolute path from the watcher to a
	// workspace-relative slash-separated path before emitting.
	// Sentinel tokens (e.g. magus.StreamAllSentinel) and any path that
	// escapes the workspace root are passed through verbatim.
	//
	// The flush is the whole point of the function - stdout is block-buffered when
	// piped, so an unflushed batch never reaches `magus affected --stdin` - which is
	// why its error is returned rather than dropped: a closed pipe downstream ends the
	// watch, it does not become a stream that silently emits nothing.
	writeBatch := func(paths []string) error {
		for _, p := range paths {
			rel := toWorkspaceRel(ws.Root(), p)
			fmt.Fprint(out, rel, sep)
		}
		fmt.Fprint(out, batchSep)
		if err := out.Flush(); err != nil {
			return fmt.Errorf("magus watch: write batch: %w", err)
		}
		return nil
	}

	if wf.Initial {
		// Sentinel consumed by `magus affected --stdin` to trigger a full build.
		if err := writeBatch([]string{magus.StreamAllSentinel}); err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-w.Errors():
			// A watcher error is the watch failing, not a note about it: the backend has
			// stopped seeing some or all of the tree, so continuing prints an empty
			// stream and exits 0 - a build pipeline reads that as "nothing changed".
			return fmt.Errorf("magus watch: %w", err)
		case batch, ok := <-w.Events():
			if !ok {
				return nil
			}
			if err := writeBatch(batch.Paths); err != nil {
				return err
			}
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
