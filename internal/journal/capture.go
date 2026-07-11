package journal

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
)

// eventAttr is the slog attribute key that carries the whole [Event] value through a
// slog.Record. Capture handlers pull it back out with [eventFrom]. Carrying the typed event
// as one attribute keeps the hot output path allocation-light (no field-by-field re-encode);
// the JSON shape a handler writes is still json.Marshal(Event), so the on-disk schema is
// unchanged. A future OpenTelemetry bridge can expand it into per-field attributes there.
const eventAttr = "event"

type (
	loggerKey struct{}
	invKey    struct{}
	stepKey   struct{}
	stepInfo  struct{ project, target string }
)

// WithLogger threads the capture logger for this invocation onto ctx. [Emit] writes to it,
// so the cache and the subprocess layer can record events without a direct dependency on
// the file/broadcaster handlers behind it.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// LoggerFromContext returns the capture logger attached with [WithLogger], or a logger that
// discards (never nil), so [Emit] can be called unconditionally.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return discardLogger
}

// WithInvocationID threads the invocation id stamped onto events emitted under ctx.
func WithInvocationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, invKey{}, id)
}

// InvocationIDFromContext returns the invocation id attached with [WithInvocationID], or "".
func InvocationIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(invKey{}).(string); ok {
		return id
	}
	return ""
}

// WithStep tags ctx with the project/target whose subprocesses are about to run, so the exec
// primitive ([internal/proc/run]) can label the exec events it emits without magus threading
// project/target through every layer by hand. Set once per target step, around the op body.
func WithStep(ctx context.Context, project, target string) context.Context {
	return context.WithValue(ctx, stepKey{}, stepInfo{project: project, target: target})
}

// StepFromContext returns the project/target set by [WithStep] and whether one was set. A
// false ok means we are not inside a captured target step (e.g. an internal probe), so no
// exec event should be emitted.
func StepFromContext(ctx context.Context) (project, target string, ok bool) {
	if s, has := ctx.Value(stepKey{}).(stepInfo); has {
		return s.project, s.target, true
	}
	return "", "", false
}

// Emit records e to the ctx capture logger, stamping the timestamp (if unset) and the
// invocation id from ctx (if unset). A ctx with no capture logger is a no-op, so capture
// sites can call Emit unconditionally.
func Emit(ctx context.Context, e Event) {
	if e.Ts == 0 {
		e.Ts = nowMillis()
	}
	if e.Inv == "" {
		e.Inv = InvocationIDFromContext(ctx)
	}
	LoggerFromContext(ctx).LogAttrs(ctx, slog.LevelInfo, e.Text, slog.Any(eventAttr, e))
}

// EventFromRecord extracts the [Event] a capture record carries, and whether it was present.
// It lets a slog.Handler outside this package (e.g. the daemon's live-run registry) fold the
// same typed events the file and broadcaster handlers consume, without re-parsing JSON.
func EventFromRecord(r slog.Record) (Event, bool) { return eventFrom(r) }

// eventFrom extracts the [Event] a capture record carries, and whether it was present.
func eventFrom(r slog.Record) (Event, bool) {
	var (
		e  Event
		ok bool
	)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == eventAttr {
			if ev, is := a.Value.Any().(Event); is {
				e, ok = ev, true
				return false
			}
		}
		return true
	})
	return e, ok
}

// NewLogger builds the capture logger for one invocation: a slog.Logger fanning every event
// to each handler (the JSONL file, plus any live broadcasters). It is what [WithLogger]
// puts on ctx.
func NewLogger(handlers ...slog.Handler) *slog.Logger {
	return slog.New(fanout(handlers))
}

// discardLogger is the fallback when no capture logger is on ctx: its handler is never
// enabled, so Emit builds nothing.
var discardLogger = slog.New(discardHandler{})

type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }

// fanout is a slog.Handler that delivers each record to every child handler. Capture never
// filters, so it is always enabled; child errors are ignored (capture is best-effort).
type fanout []slog.Handler

func (f fanout) Enabled(context.Context, slog.Level) bool { return true }

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f {
		_ = h.Handle(ctx, r)
	}
	return nil
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make(fanout, len(f))
	for i, h := range f {
		next[i] = h.WithAttrs(attrs)
	}
	return next
}

func (f fanout) WithGroup(name string) slog.Handler {
	next := make(fanout, len(f))
	for i, h := range f {
		next[i] = h.WithGroup(name)
	}
	return next
}

// FileHandler is the capture handler that appends each event as one JSON line (JSONL) to an
// io.Writer - the durable run log. Writes are serialized by a mutex so events from
// concurrent targets stay whole lines. A marshal or write error is dropped: capture is
// best-effort and must never fail a run. Call [FileHandler.Flush] before closing the file.
type FileHandler struct {
	mu sync.Mutex
	w  *bufio.Writer
}

// NewFileHandler returns a FileHandler over w (buffered).
func NewFileHandler(w io.Writer) *FileHandler {
	return &FileHandler{w: bufio.NewWriter(w)}
}

func (h *FileHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *FileHandler) Handle(_ context.Context, r slog.Record) error {
	e, ok := eventFrom(r)
	if !ok {
		return nil
	}
	line, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, _ = h.w.Write(line)
	return h.w.WriteByte('\n')
}

func (h *FileHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *FileHandler) WithGroup(string) slog.Handler      { return h }

// Flush writes any buffered events to the underlying writer.
func (h *FileHandler) Flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.w.Flush()
}
