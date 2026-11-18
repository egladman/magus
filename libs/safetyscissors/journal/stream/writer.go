package stream

import (
	"bufio"
	"io"
	"sync"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// fromJournal maps one journal record onto the stream envelope. It reports
// false for exec, scope and secret records, which no subscriber consumes.
// workspace rides every event: a record names its invocation, not its root.
func fromJournal(workspace string, r journal.Record) (Event, bool) {
	out := Event{Ts: r.Time.UnixMilli(), Workspace: workspace, Inv: r.Invocation}
	if b, ok := r.Started(); ok {
		out.Body = Run{Phase: "started", MagusVersion: b.Version, Command: b.Command.Arguments, Trigger: string(b.Command.Trigger)}
		return out, true
	}
	if b, ok := r.Finished(); ok {
		out.Body = Run{Phase: "finished", Status: string(b.Status)}
		return out, true
	}
	if b, ok := r.Run(); ok {
		status, cached := targetStatus(b.Status)
		out.Body = Target{
			Project:    b.Project,
			Target:     b.Target,
			Status:     status,
			CacheHit:   cached,
			Ref:        b.Ref,
			DurationMs: b.Duration.Milliseconds(),
			Error:      b.Error,
		}
		return out, true
	}
	if b, ok := r.Output(); ok {
		out.Body = Output{Project: b.Project, Target: b.Target, Stream: string(b.Stream), Text: b.Text}
		return out, true
	}
	return Event{}, false
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
func targetStatus(journalStatus journal.Status) (status string, cacheHit bool) {
	switch journalStatus {
	case journal.StatusPass:
		return "ok", false
	case journal.StatusCached:
		return "ok", true
	case journal.StatusPermitted:
		return string(journal.StatusPermitted), false
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
	filter Filter
}

// NewWriter returns a Writer emitting to dst under filter. The caller owns dst
// and must call [Writer.Close] to flush; a Writer that is never closed loses
// whatever is still buffered.
func NewWriter(dst io.Writer, filter Filter) *Writer {
	return &Writer{bw: bufio.NewWriter(dst), filter: filter}
}

// Write writes one event, or does nothing when the filter excludes its type.
// It flushes per event: a subscriber is usually a pipe read live, and a status
// bar that learns of a failed build 4KB late is worse than none.
func (w *Writer) Write(e Event) error {
	if e.Body == nil || !w.filter.Allows(e.Body.EventType()) {
		return nil
	}
	line, err := codec.Marshal(e)
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
