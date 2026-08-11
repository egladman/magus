package magus

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/cache"
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
	// Join this invocation to the ancestry it inherited, so the locks it takes are
	// attributable to it and a descendant can recognize them as its own ancestor's.
	ctx = types.AppendInvocationAncestor(ctx, os.Getpid(), id)

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
		// Cache-yield analysis remains available through `magus doctor`. Do not emit
		// it on every slow invocation: nested/adopted runs can surface the same
		// journal finding twice, and intentional uncached work made the hint noisy.
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

// Command is a magus invocation's lineage - the caller-facing projection of
// [journal.Command]. Field tags match it exactly for JSON wire compat.
type Command struct {
	Arguments []string `json:"arguments,omitempty"` // the full argument vector, subcommand included (e.g. ["run", "build", "api"])
	Cwd       string   `json:"cwd,omitempty"`       // directory the command was invoked in
	Trigger   string   `json:"trigger,omitempty"`   // one of the journal.Trigger* constants
}

// Invocation is one magus command, launch to exit, read back from the journal - the
// caller-facing projection of [journal.Invocation]. See [Magus.InvocationByID]. Field
// tags match journal.Invocation's exactly for JSON wire compat.
type Invocation struct {
	ID           string  `json:"id"`
	Command      Command `json:"command"`
	StartedMs    int64   `json:"started_ms"`            // unix milliseconds
	FinishedMs   int64   `json:"finished_ms,omitempty"` // unix milliseconds; 0 while running
	Status       string  `json:"status,omitempty"`      // overall outcome (pass|fail), from the finished event
	MagusVersion string  `json:"magus_version,omitempty"`
}

func newInvocation(inv journal.Invocation) Invocation {
	return Invocation{
		ID: inv.ID,
		Command: Command{
			Arguments: inv.Command.Arguments,
			Cwd:       inv.Command.Cwd,
			Trigger:   inv.Command.Trigger,
		},
		StartedMs:    inv.StartedMs,
		FinishedMs:   inv.FinishedMs,
		Status:       inv.Status,
		MagusVersion: inv.MagusVersion,
	}
}

// Event is one journal entry (output line, magus log line, or a run's result) - the
// caller-facing projection of [journal.Event]. See [Magus.InvocationEventsByID]. Field
// tags match journal.Event's exactly for JSON wire compat.
type Event struct {
	Ts      int64  `json:"ts"`                // unix milliseconds
	Inv     string `json:"inv,omitempty"`     // invocation id (one per run command)
	Project string `json:"project,omitempty"` // repo-relative project path
	Target  string `json:"target,omitempty"`  // target name (with charms, as the CLI spells it)
	Kind    string `json:"kind"`              // one of the journal.Kind* constants
	Stream  string `json:"stream,omitempty"`  // stdout|stderr, for output events
	Level   string `json:"level,omitempty"`   // info|warn|error, for magus events
	Status  string `json:"status,omitempty"`  // pass|fail|cached, for result events
	Ref     string `json:"ref,omitempty"`     // target-output ref, for result events
	DurMs   int64  `json:"dur_ms,omitempty"`  // duration in ms, for result events
	Text    string `json:"text,omitempty"`    // output line or message

	// Set only on the started event (Kind==journal.KindStarted): the run's identity.
	Command      *Command `json:"command,omitempty"`
	MagusVersion string   `json:"magus_version,omitempty"`
}

func newEvent(e journal.Event) Event {
	ev := Event{
		Ts: e.Ts, Inv: e.Inv, Project: e.Project, Target: e.Target, Kind: e.Kind,
		Stream: e.Stream, Level: e.Level, Status: e.Status, Ref: e.Ref, DurMs: e.DurMs,
		Text: e.Text, MagusVersion: e.MagusVersion,
	}
	if e.Command != nil {
		ev.Command = &Command{
			Arguments: e.Command.Arguments,
			Cwd:       e.Command.Cwd,
			Trigger:   e.Command.Trigger,
		}
	}
	return ev
}

func newEvents(events []journal.Event) []Event {
	if events == nil {
		return nil
	}
	out := make([]Event, len(events))
	for i, e := range events {
		out[i] = newEvent(e)
	}
	return out
}
