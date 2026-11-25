package main

import (
	"context"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/termkit"
	"io"
	"log/slog"
	"os"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// nextGate is job.NextGate under the name the commands use.
type nextGate struct{ job.NextGate }

func (n nextGate) served(next []types.NextStep) []types.NextStep { return n.Served(next) }

// newNextGate resolves the gate for a command running against root.
func newNextGate(ctx context.Context, root string) nextGate {
	dir, err := magus.ResolveCacheDir(resolveRootOrEmpty(root), workspace.WithLoadedConfig(globalCfg))
	if err != nil {
		return nextGate{job.UnboundNextGate()}
	}
	loc, err := jobPlace(resolveRootOrEmpty(root))
	role, writePaths := job.ActingRole(ctx, loc, err)
	return nextGate{job.NewNextGate(dir, role, writePaths)}
}

// emitConcurrencyNudge prints a finished run's concurrency_profile nudge. -s and disabled
// hints skip it without spending the firing.
func emitConcurrencyNudge(ctx context.Context, sink *magus.Sink, m *magus.Magus, args []string) {
	if global.silent || !interactive.HintsEnabled() {
		return
	}
	// A run the server executes shares its limiter with every client's runs, so a wait
	// for a slot there says nothing about this invocation's profile.
	if _, adopted := magusFromContext(ctx); adopted {
		return
	}
	line := m.ConcurrencyNudge()
	if line == "" {
		return
	}
	if msg, ok := job.ConcurrencyNudge(line, m.CacheDir(), hint.WindowFromTerminal(os.Getenv, os.Getppid(),
		termkit.IsTerminalWriter(os.Stdout, termkit.SystemProbe)), args); ok {
		sink.EmitNotice(ctx, slog.LevelInfo, "", msg)
	}
}

// printNext writes a result's breadcrumbs; -s drops the reasons.
func printNext(w io.Writer, n nextGate, next []types.NextStep) {
	job.PrintNext(w, n.NextGate, next, global.silent)
}
