package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/server"
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

	logPath, err := proc.NewDetachedLog(server.StateLogPath("detached"), time.Now())
	if err != nil {
		return fmt.Errorf("--detach: %w", err)
	}
	pid, err := proc.SpawnDetached(proc.WithoutDetachFlag(os.Args[1:]), logPath)
	if err != nil {
		return fmt.Errorf("--detach: %w", err)
	}
	sink.EmitDetach(ctx, pid, logPath)
	return nil
}

// query.go, which this move does not hold, still names these two.
var (
	detachedCommand     = proc.DetachedCommand
	detachedSysProcAttr = proc.DetachedSysProcAttr
)
