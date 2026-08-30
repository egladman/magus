package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/eventstream"
	"github.com/egladman/magus/types"
)

// eventsCmd implements `magus events`: the subscribe surface third-party
// integrations build against - an editor plugin, a status bar, a notifier.
//
// It is the OUTBOUND half of magus's machine surface and the dual of `magus
// session hook`, which is inbound and returns a verdict. Nothing a subscriber
// does here can change what magus decides; docs/scope.md seals that seam, and
// this command has no reply channel by construction.
//
// It resolves the cache directory WITHOUT loading the workspace
// (magus.ResolveCacheDir), so an editor can attach to a repository whose
// magusfile is mid-edit or outright broken. A subscriber that only comes up when
// the workspace is healthy is useless at exactly the moment someone wants it.
func eventsCmd(ctx context.Context, root string, args []string) error {
	var ef *gen.EventsFlags
	rest, err := cmdParse("events", args, func(fs *flag.FlagSet) {
		ef = gen.BindEvents(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus events [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Stream workspace events as JSONL, one event per line, for an editor plugin")
			fmt.Fprintln(os.Stderr, "or any other integration. Every magus process in the workspace feeds this")
			fmt.Fprintln(os.Stderr, "stream, so a run started in another terminal shows up here.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Event types:")
			for _, t := range types.StreamEventTypes() {
				fmt.Fprintf(os.Stderr, "  %s\n", t)
			}
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "target.output is excluded unless named with --type: it is the one type that")
			fmt.Fprintln(os.Stderr, "scales with build size. Fetch a target's full log by its ref instead:")
			fmt.Fprintln(os.Stderr, "  magus query output <ref>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Examples:")
			fmt.Fprintln(os.Stderr, "  magus events --follow")
			fmt.Fprintln(os.Stderr, "  magus events --follow --type target.result,diagnostic.emitted")
			fmt.Fprintln(os.Stderr, "  magus events --limit 1")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus events: unexpected argument %q (this command takes flags only)", rest[0])
	}

	// The stream IS the format: one event per line, forever, so there is no document to
	// close and nothing json or yaml could wrap. -o was accepted and ignored here, which
	// left `magus events -o json` looking like it had asked for something. --tee still
	// works, because what it mirrors is already JSONL.
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText && opts.Format != outputJSONL {
		return usagef("magus events: -o %s is not supported; the event stream is JSONL only (one event per line) and never closes, so there is no document to render", opts.Format)
	}

	filter, err := parseStreamFilter(ef.Type)
	if err != nil {
		return err
	}

	workspace := resolveRootOrEmpty(root)
	if workspace == "" {
		return fmt.Errorf("magus events: no magus workspace found from this directory; run it inside a workspace, or name one with --root <path>")
	}
	// The contract says every event carries an ABSOLUTE root, because a subscriber
	// watching two workspaces routes on it and a relative path resolves against the
	// subscriber's cwd rather than the producer's. `--root ./ws` would otherwise ship
	// "./ws" verbatim.
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	cacheDir, err := magus.ResolveCacheDir(workspace)
	if err != nil {
		return err
	}

	dst, cleanup, err := outputDst()
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	w := eventstream.NewWriter(dst, filter)
	defer func() { _ = w.Close() }()

	f := eventstream.NewFollower(filepath.Join(cacheDir, cache.RunsDir), workspace)
	// --limit 0 means replay NOTHING, which is what a notifier wants: waking up and
	// announcing yesterday's failure is worse than staying quiet. Negative means
	// replay everything. The library's Replay reads 0 as "no cap", so the mapping
	// happens here rather than changing what that method means to other callers.
	switch {
	case ef.Limit == 0:
		if err := f.Skip(); err != nil {
			return err
		}
	case ef.Limit < 0:
		if err := f.Replay(0, w.Emit); err != nil {
			return err
		}
	default:
		if err := f.Replay(ef.Limit, w.Emit); err != nil {
			return err
		}
	}
	if !ef.Follow {
		return nil
	}

	// No signal handling here: watchInterrupts already cancels this ctx and turns
	// the signal into magus's conventional 128+N exit. Registering a second
	// handler for the same signals would fight it for no gain.
	return f.Follow(ctx, ef.Interval, w.Emit)
}

// parseStreamFilter turns the --type list into a filter, refusing an unknown
// name.
//
// A typo has to be an error rather than an empty match: `--type target.reslut`
// would otherwise present as a workspace where nothing ever happens, which is
// indistinguishable from a broken integration and costs an hour to diagnose.
func parseStreamFilter(csv string) (types.StreamFilter, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return types.StreamFilter{}, nil
	}
	var out types.StreamFilter
	for _, name := range strings.Split(csv, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		t, err := types.ParseStreamEventType(name)
		if err != nil {
			return types.StreamFilter{}, fmt.Errorf("magus events: %w (known types: %s)", err, strings.Join(streamTypeNames(), ", "))
		}
		out.Types = append(out.Types, t)
	}
	return out, nil
}

// streamTypeNames renders the taxonomy for an error message.
func streamTypeNames() []string {
	all := types.StreamEventTypes()
	names := make([]string, len(all))
	for i, t := range all {
		names[i] = string(t)
	}
	return names
}
