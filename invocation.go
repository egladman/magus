package magus

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/types"
)

// BeginInvocation opens the journal for one `magus` command at
// <cacheDir>/runs/<inv>.jsonl, threads the sinks and the invocation id onto
// ctx, and emits the Started record; extra sinks see every record. The
// cleanup takes the run's error and emits the Finished record before it
// closes the file. An unopened journal still reaches extra, so a run never
// fails on capture.
func (m *Magus) BeginInvocation(ctx context.Context, cmd Command, magusVersion string, extra ...recorder.Sink) (context.Context, func(error)) {
	// Reuse an id already threaded onto ctx (the server mints it in proc.service.run so the
	// adopted call's pool entry can carry its inv and deep-link to this run's live log);
	// otherwise mint a fresh one: the plain CLI path.
	id := recorder.InvocationIDFromContext(ctx)
	if id == "" {
		id = recorder.NewInvocationID()
	}
	ctx = recorder.WithInvocationID(ctx, id)
	// Join this invocation to the ancestry it inherited, so the locks it takes are
	// attributable to it and a descendant can recognize them as its own ancestor's.
	ctx = types.AppendInvocationAncestor(ctx, os.Getpid(), id)

	started := journal.Started{
		Command: journal.Command{Arguments: cmd.Arguments, Cwd: cmd.Cwd, Trigger: journal.Trigger(cmd.Trigger)},
		Version: magusVersion,
	}
	finish := func(ctx context.Context, runErr error) {
		status := journal.StatusPass
		if runErr != nil {
			status = journal.StatusFail
		}
		recorder.Emit(ctx, journal.Finished{Status: status})
	}

	dir := filepath.Join(resolveCacheDir(m.ws.Root, m.cfg), cache.RunsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		ctx = recorder.WithSinks(ctx, extra...)
		recorder.Emit(ctx, started)
		return ctx, func(runErr error) { finish(ctx, runErr) }
	}
	f, err := os.Create(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		ctx = recorder.WithSinks(ctx, extra...)
		recorder.Emit(ctx, started)
		return ctx, func(runErr error) { finish(ctx, runErr) }
	}

	// The file is the durable record; extra sinks see the same records.
	file := recorder.NewFileSink(f)
	ctx = recorder.WithSinks(ctx, append([]recorder.Sink{file.Handle}, extra...)...)
	recorder.Emit(ctx, started)

	return ctx, func(runErr error) {
		finish(ctx, runErr)
		file.Flush()
		_ = f.Close()
		// Cache-yield analysis remains available through `magus doctor`. Do not emit
		// it on every slow invocation: nested/adopted runs can surface the same
		// journal finding twice, and intentional uncached work made the hint noisy.
	}
}

// Command is a magus invocation's lineage: the caller-facing projection of
// [journal.Command]. Field tags match it exactly for JSON wire compat.
type Command struct {
	Arguments []string `json:"arguments,omitempty"` // the full argument vector, subcommand included (e.g. ["run", "build", "api"])
	Cwd       string   `json:"cwd,omitempty"`       // directory the command was invoked in
	Trigger   string   `json:"trigger,omitempty"`   // one of the journal.Trigger* constants
}

// Invocation is one magus command, launch to exit, read back from the journal: the
// caller-facing projection of [journal.Invocation]. See [Magus.InvocationByID].
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
			Trigger:   string(inv.Command.Trigger),
		},
		StartedMs:    unixMilli(inv.Started),
		FinishedMs:   unixMilli(inv.Finished),
		Status:       string(inv.Status),
		MagusVersion: inv.Version,
	}
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// Event is one journal entry (output line, magus log line, or a run's
// result): the caller-facing projection of [journal.Record]. The JSON form is
// the journal line: keys ts, inv and duration_ms (integer milliseconds).
type Event struct {
	Timestamp      int64         `json:"ts"`                        // unix milliseconds
	Invocation     string        `json:"inv,omitempty"`             // invocation id (one per run command)
	Project        string        `json:"project,omitempty"`         // repo-relative project path
	Target         string        `json:"target,omitempty"`          // target name (with charms, as the CLI spells it)
	Kind           string        `json:"kind"`                      // one of the journal.Kind* constants
	Stream         string        `json:"stream,omitempty"`          // stdout|stderr, for output events
	Level          string        `json:"level,omitempty"`           // info|warn|error, for magus events
	Status         string        `json:"status,omitempty"`          // pass|fail|cached, for result events
	Ref            string        `json:"ref,omitempty"`             // target-output ref, for result events
	Duration       time.Duration `json:"-"`                         // wall-clock duration, for result events; the wire form is duration_ms
	Text           string        `json:"text,omitempty"`            // output line or message
	DiagnosticCode string        `json:"diagnostic_code,omitempty"` // MGS code of a failed result that never ran

	// Set only on the started event (Kind==journal.KindStarted): the run's identity.
	Command      *Command `json:"command,omitempty"`
	MagusVersion string   `json:"magus_version,omitempty"`
}

// eventFields is Event without its JSON methods, so eventWire can embed the tagged fields.
type eventFields Event

// eventWire adds the duration_ms conversion to Event's tagged fields.
type eventWire struct {
	eventFields
	DurationMs int64 `json:"duration_ms,omitempty"`
}

// MarshalJSON encodes e in the journal line's form.
func (e Event) MarshalJSON() ([]byte, error) {
	return codec.Marshal(eventWire{eventFields: eventFields(e), DurationMs: e.Duration.Milliseconds()})
}

// UnmarshalJSON decodes a journal line into e.
func (e *Event) UnmarshalJSON(data []byte) error {
	var w eventWire
	if err := codec.Unmarshal(data, &w); err != nil {
		return err
	}
	*e = Event(w.eventFields)
	e.Duration = time.Duration(w.DurationMs) * time.Millisecond
	return nil
}

// newEvent reads r's journal line into the projection, so the two cannot
// disagree on a key.
func newEvent(r journal.Record) (Event, error) {
	line, err := codec.Marshal(r)
	if err != nil {
		return Event{}, err
	}
	var ev Event
	err = codec.Unmarshal(line, &ev)
	return ev, err
}
