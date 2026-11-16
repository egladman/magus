package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/proc"
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
	sink, closeSink, err := openSink(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { _ = closeSink() }()

	logPath, err := proc.NewDetachedLog(stateLogPath("detached"), time.Now())
	if err != nil {
		return fmt.Errorf("--detach: %w", err)
	}
	pid, err := proc.SpawnDetached(withoutDetachFlag(os.Args[1:]), logPath)
	if err != nil {
		return fmt.Errorf("--detach: %w", err)
	}
	sink.EmitDetach(ctx, pid, logPath)
	return nil
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
		if key, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); strings.HasPrefix(a, "-") && key == flaggen.FlagRunDetach {
			continue
		}
		out = append(out, a)
	}
	return out
}

// The leased callers (broker.go, query.go) still name these; they forward to internal/proc.
var (
	spawnDetached       = proc.SpawnDetached
	detachedCommand     = proc.DetachedCommand
	detachedSysProcAttr = proc.DetachedSysProcAttr
)
