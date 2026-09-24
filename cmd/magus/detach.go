package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
)

// detachRun starts this invocation again without --detach, as its own session leader
// with its output appended to a fresh log under $XDG_STATE_HOME/magus/detached, and
// returns once it has started. The child is an ordinary run: it takes a broker
// connection for its claims like any other and forwards to a server only if one is
// already up, so detaching neither needs nor starts `magus server`.
func detachRun(ctx context.Context) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	sink, closeSink, err := openSink(opts)
	if err != nil {
		return err
	}
	defer func() { _ = closeSink() }()

	logPath, err := newDetachedLog(time.Now())
	if err != nil {
		return fmt.Errorf("--detach: %w", err)
	}
	pid, err := spawnDetached(withoutDetachFlag(os.Args[1:]), logPath)
	if err != nil {
		return fmt.Errorf("--detach: %w", err)
	}
	sink.EmitDetach(ctx, pid, logPath)
	return nil
}

// newDetachedLog creates an empty log for one detached run and returns its path. The
// name leads with the start time, so a directory listing sorts them.
func newDetachedLog(now time.Time) (string, error) {
	dir := stateLogPath("detached")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, now.Format("20060102T150405")+"-*.log")
	if err != nil {
		return "", fmt.Errorf("create a log in %s: %w", dir, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("create %s: %w", f.Name(), err)
	}
	return filepath.Clean(f.Name()), nil
}

// withoutDetachFlag drops --detach from an argv, in every spelling the flag package
// accepts: -detach, --detach, and either with an inline =value. The detached child runs
// the rest, and a --detach left in would have it detach again, forever.
func withoutDetachFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			// Past the separator the tokens belong to the forwarded tool.
			out = append(out, args[i:]...)
			break
		}
		if key, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); strings.HasPrefix(a, "-") && key == gen.FlagRunDetach {
			continue
		}
		out = append(out, a)
	}
	return out
}
