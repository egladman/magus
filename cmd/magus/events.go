package main

import (
	eventstypes "github.com/egladman/magus/types/events"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/eventstream"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/cmdline/output"
)

// eventsCommand implements `magus events`: the subscribe command third-party
// integrations build against: an editor plugin, a status bar, a notifier.
//
// It is the OUTBOUND half of magus's machine-facing commands and the dual of `magus
// shell`, which is inbound and returns a verdict. Nothing a subscriber
// does here can change what magus decides; docs/scope.md seals that seam, and
// this command has no reply channel by construction.
//
// It resolves the cache directory WITHOUT loading the workspace
// (magus.ResolveCacheDir), so an editor can attach to a repository whose
// magusfile is mid-edit or outright broken. A subscriber that only comes up when
// the workspace is healthy is useless at exactly the moment someone wants it.
func eventsCommand(ctx context.Context, root string, args []string) error {
	var ef *flaggen.EventsFlags
	rest, err := commandParse("events", args, func(fs *flag.FlagSet) {
		ef = flaggen.BindEvents(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus events [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Stream workspace events as JSONL, one event per line, for an editor plugin")
			fmt.Fprintln(os.Stderr, "or any other integration. Every magus process in the workspace feeds this")
			fmt.Fprintln(os.Stderr, "stream, so a run started in another terminal shows up here.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Event types:")
			for _, t := range eventstypes.EventTypes() {
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
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus events: unexpected argument %q (this command takes flags only)", rest[0])
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	filter, err := parseStreamFilter(ef.Type)
	if err != nil {
		return err
	}

	workspace := resolveRootOrEmpty(root)
	if workspace == "" {
		return fmt.Errorf("magus events: no magus workspace found from this directory, run it inside a workspace or name one with --root <path>")
	}
	// The contract says every event carries an ABSOLUTE root, because a subscriber
	// watching two workspaces routes on it and a relative path resolves against the
	// subscriber's cwd rather than the producer's. `--root ./ws` would otherwise ship
	// "./ws" verbatim.
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	cacheDir, err := magus.ResolveCacheDir(workspace, cliOptions()...)
	if err != nil {
		return err
	}

	destination, cleanup, err := outputDestination(os.Stdout)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	w := eventstream.NewWriter(destination, filter)
	defer func() { _ = w.Close() }()
	emit := w.Emit
	if opts.Format != outputText && opts.Format != outputJSONL {
		emit = eventRecordEmitter(opts, filter)
	}

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
		if err := f.Replay(0, emit); err != nil {
			return err
		}
	default:
		if err := f.Replay(ef.Limit, emit); err != nil {
			return err
		}
	}
	if !ef.Follow {
		return nil
	}

	// No signal handling here: watchInterrupts already cancels this ctx and turns
	// the signal into magus's conventional 128+N exit. Registering a second
	// handler for the same signals would fight it for no gain.
	return f.Follow(ctx, ef.Interval, emit)
}

// eventRecordEmitter renders each event alone as it arrives. The stream never
// closes, so no document could wrap it. A -o name line is the event's type.
func eventRecordEmitter(opts output.Options, filter eventstypes.Filter) func(eventstypes.Event) error {
	var mu sync.Mutex
	return func(e eventstypes.Event) error {
		if e.Body == nil || !filter.Allows(e.Body.StreamType()) {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		return emitRecord(opts, e, []string{string(e.Body.StreamType())})
	}
}

// parseStreamFilter turns the --type list into a filter, refusing an unknown
// name.
//
// A typo has to be an error rather than an empty match: `--type target.reslut`
// would otherwise present as a workspace where nothing ever happens, which is
// indistinguishable from a broken integration and costs an hour to diagnose.
func parseStreamFilter(csv string) (eventstypes.Filter, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return eventstypes.Filter{}, nil
	}
	var out eventstypes.Filter
	for _, name := range strings.Split(csv, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		t, err := eventstypes.ParseStreamEventType(name)
		if err != nil {
			return eventstypes.Filter{}, fmt.Errorf("magus events (known types: %s): %w", strings.Join(streamTypeNames(), ", "), err)
		}
		out.Types = append(out.Types, t)
	}
	return out, nil
}

// streamTypeNames renders the taxonomy for an error message.
func streamTypeNames() []string {
	all := eventstypes.EventTypes()
	names := make([]string, len(all))
	for i, t := range all {
		names[i] = string(t)
	}
	return names
}
