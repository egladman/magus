package magus

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
)

// BeginInvocation opens the structured journal for one `magus` command (launch to exit). It
// mints an invocation id, opens the union event log (<cacheDir>/runs/<inv>.jsonl) behind a
// capture *slog.Logger, threads that logger + the id onto ctx so every captured event
// (subprocess output + target results) streams into it, and emits the invocation's opening
// lifecycle event: a started event carrying the command lineage (verb/args/cwd/trigger) and
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
	id := journal.NewInvocationID()
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

	return ctx, func(runErr error) {
		finish(ctx, runErr)
		fileHandler.Flush()
		_ = f.Close()
	}
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
