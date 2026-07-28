package magus

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
)

// BeginInvocation opens the structured journal for one `magus` command (launch to exit). It
// mints an invocation id, opens the union event log (<cacheDir>/runs/<inv>.jsonl) behind a
// capture *slog.Logger, threads that logger + the id onto ctx so every captured event
// (subprocess output + target results) streams into it, and emits the invocation's opening
// lifecycle event: a started event carrying the command lineage (subcommand/args/cwd/trigger) and
// magus version. Folding the identity into the stream this way means both the durable file
// and any live watcher learn which command produced the run from frame one - there is no
// separate metadata file. Extra handlers (e.g. a live SSE broadcaster) fan out from the same
// logger.
//
// The returned cleanup takes the run's error: it emits the closing finished event (overall
// pass/fail outcome, final timing), then flushes and closes the log. Call it as
// `defer func() { end(runErr) }()` so the outcome reflects the final result.
//
// It is best-effort: if the log cannot be opened, the id is still stamped on ctx and the
// lifecycle events still reach any extra handlers, so a run never fails on capture. The
// command/lineage is what the viewer surfaces; see magus.viewer.v1.Invocation.
func (m *Magus) BeginInvocation(ctx context.Context, cmd journal.Command, magusVersion string, extra ...slog.Handler) (context.Context, func(error)) {
	// Reuse an id already threaded onto ctx (the daemon mints it in proc.service.run so the
	// adopted call's pool entry can carry its inv and deep-link to this run's live log);
	// otherwise mint a fresh one - the plain CLI path.
	id := journal.InvocationIDFromContext(ctx)
	if id == "" {
		id = journal.NewInvocationID()
	}
	ctx = journal.WithInvocationID(ctx, id)

	started := journal.Event{Kind: journal.KindStarted, Command: &cmd, MagusVersion: magusVersion}
	finish := func(ctx context.Context, runErr error) {
		status := journal.StatusPass
		if runErr != nil {
			status = journal.StatusFail
		}
		journal.Emit(ctx, journal.Event{Kind: journal.KindFinished, Status: status})
	}

	dir := filepath.Join(resolveCacheDir(m.ws.Root, m.cfg), cache.RunsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		ctx = withCaptureLogger(ctx, extra)
		journal.Emit(ctx, started)
		return ctx, func(runErr error) { finish(ctx, runErr) }
	}
	f, err := os.Create(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		ctx = withCaptureLogger(ctx, extra)
		journal.Emit(ctx, started)
		return ctx, func(runErr error) { finish(ctx, runErr) }
	}

	// The file log is the durable record; any extra handlers stream from the same events.
	fileHandler := journal.NewFileHandler(f)
	ctx = withCaptureLogger(ctx, append([]slog.Handler{fileHandler}, extra...))
	journal.Emit(ctx, started)

	journalPath := filepath.Join(dir, id+".jsonl")
	return ctx, func(runErr error) {
		finish(ctx, runErr)
		fileHandler.Flush()
		_ = f.Close()
		// The journal is closed, so this run's own results count toward the verdict.
		warnStalledTargets(resolveCacheDir(m.ws.Root, m.cfg), journalPath)
	}
}

// slowRunMs is the execution time that makes a cache-yield scan worth doing. If a target
// just spent this long, reading a few hundred small journals is noise beside it; if
// nothing was slow, the scan never happens.
const slowRunMs = 5_000

// warnStalledTargets tells the user when a target they just waited on has never once
// replayed from cache.
//
// This fires where the user already is, deliberately. The same finding is available from
// `magus doctor`, but a diagnostic you have to remember to ask for is one you will not
// see: this exact problem sat in the journals for dozens of runs, costing over an hour,
// while every individual run looked perfectly healthy. Best-effort throughout - a
// diagnostic must never be why a run fails.
func warnStalledTargets(cacheDir, journalPath string) {
	slow := cache.SlowExecutions(journalPath, slowRunMs)
	if len(slow) == 0 {
		return
	}
	stalled := cache.StalledTargets(cacheDir, slow)
	if len(stalled) == 0 {
		return
	}
	// The worst one only. A list of them is what `magus doctor` is for; the point here
	// is to interrupt the habit, not to file a report.
	s := stalled[0]
	interactive.Emit(os.Stderr, fmt.Sprintf(
		"[%s] %s %s has run %d times without ever replaying from cache (%.0fm spent). Its declared "+
			"footprint is probably wider than it reads, so unrelated edits keep changing its key; "+
			"run 'magus doctor' for the full list (see %s)",
		types.TargetNeverReplays, s.Project, s.Target, s.Runs, float64(s.TotalMs)/60000,
		types.CodeURL(types.TargetNeverReplays)))
}

// withCaptureLogger attaches a capture logger fanning to handlers onto ctx (or leaves ctx
// unchanged when there are none - the best-effort path where the durable file could not be
// opened and no live watcher is attached).
func withCaptureLogger(ctx context.Context, handlers []slog.Handler) context.Context {
	if len(handlers) == 0 {
		return ctx
	}
	return journal.WithLogger(ctx, journal.NewLogger(handlers...))
}
