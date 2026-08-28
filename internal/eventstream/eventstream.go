// Package eventstream maps magus's internal producers onto the single
// [types.StreamEvent] envelope external integrations subscribe to.
//
// It is an ADAPTER layer: the producers it reads keep their own on-disk schemas,
// and nothing here rewrites them.
//
// The run journal is the only source today, and it covers the whole taxonomy. It is
// a stdlib-only leaf, so adapting it costs this package no dependency on the engine.
// The report writer, the attention store and the trail hold facts a subscriber would
// want, but each is a different file in a different directory rather than one bus;
// docs/guides/integrations/editor/design.md prices them.
package eventstream

import (
	"bufio"
	"io"
	"sync"

	"github.com/egladman/magus/internal/journal"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// FromJournal maps one journal event onto the stream envelope, reporting false
// for kinds with no external equivalent.
//
// Skipping is the normal case, not a failure. The journal records things the
// stream deliberately does not carry: KindExec is per-subprocess rather than
// per-target, KindScope is a console header, and KindSecret is governance that
// belongs in the audit trail rather than in an editor. Forwarding them would
// mean inventing a taxonomy type for each, and a type nobody consumes is a
// contract magus would still have to keep.
//
// workspace is stamped on every result because journal events carry an
// invocation id but not a root, and a subscriber watching two repositories
// cannot route an event without one.
func FromJournal(workspace string, e journal.Event) (types.StreamEvent, bool) {
	out := types.StreamEvent{Ts: e.Ts, Workspace: workspace, Inv: e.Inv}
	switch e.Kind {
	case journal.KindStarted:
		b := types.StreamRun{Phase: "started", MagusVersion: e.MagusVersion}
		if e.Command != nil {
			b.Command = e.Command.Arguments
			b.Trigger = e.Command.Trigger
		}
		out.Body = b
	case journal.KindFinished:
		out.Body = types.StreamRun{Phase: "finished", Status: e.Status}
	case journal.KindResult:
		status, cached := targetStatus(e.Status)
		out.Body = types.StreamTarget{
			Project:    e.Project,
			Target:     e.Target,
			Status:     status,
			CacheHit:   cached,
			Ref:        e.Ref,
			DurationMs: e.DurMs,
			// A failed result carries the run error in Text.
			Error: e.Text,
		}
	case journal.KindOutput:
		out.Body = types.StreamOutput{
			Project: e.Project,
			Target:  e.Target,
			Stream:  e.Stream,
			Text:    e.Text,
		}
	default:
		return types.StreamEvent{}, false
	}
	return out, true
}

// targetStatus splits the journal's three-valued status into the envelope's two
// independent axes.
//
// The journal spells a cached replay as a third status alongside pass and fail,
// which conflates "did it succeed" with "did it run". The stream keeps those
// apart because a subscriber asks them separately: a red buffer keys on success,
// a "replayed from cache" badge keys on execution. An unrecognized status maps to
// failed rather than ok, so a status magus grows later cannot present as a
// silent green.
func targetStatus(journalStatus string) (status string, cacheHit bool) {
	switch journalStatus {
	case journal.StatusPass:
		return "ok", false
	case journal.StatusCached:
		return "ok", true
	default:
		return "failed", false
	}
}

// Writer serializes stream events as JSONL onto an [io.Writer], dropping what the
// filter excludes.
//
// It is safe for concurrent use: a run fans out one goroutine per project, so
// events arrive from several at once and an unsynchronized write would interleave
// two JSON objects on one line.
type Writer struct {
	mu     sync.Mutex
	bw     *bufio.Writer
	filter types.StreamFilter
}

// NewWriter returns a Writer emitting to dst under filter. The caller owns dst
// and must call [Writer.Close] to flush; a Writer that is never closed loses
// whatever is still buffered.
func NewWriter(dst io.Writer, filter types.StreamFilter) *Writer {
	return &Writer{bw: bufio.NewWriter(dst), filter: filter}
}

// Emit writes one event, or does nothing when the filter excludes its type.
//
// It flushes per event rather than relying on the buffer filling: a subscriber
// is usually a pipe being read live, and a status bar that learns about a failed
// build once 4KB has accumulated is worse than no status bar.
func (w *Writer) Emit(e types.StreamEvent) error {
	if e.Body == nil || !w.filter.Allows(e.Body.StreamType()) {
		return nil
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.bw.Write(line); err != nil {
		return err
	}
	if err := w.bw.WriteByte('\n'); err != nil {
		return err
	}
	return w.bw.Flush()
}

// Close flushes any buffered line. It does not close the underlying writer,
// which the caller owns.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bw.Flush()
}
