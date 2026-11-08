package broker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// admissionBacklog bounds the admissions waiting for the store's writer.
const admissionBacklog = 256

// compactEvery is how many appends pass between compactions of the store.
const compactEvery = 100

// WithAdmissionLog appends each refusal and each seated wait to log, one
// types.MachineAdmission per line. Without it the broker keeps only the
// ring `server status` reads. A log with Keep set is compacted to it.
func WithAdmissionLog(log file.Log[types.MachineAdmission]) Option {
	return func(o *serveOptions) { o.admissions = &log }
}

// admissionWriter appends admissions off the broker's lock: an append waits
// on a file lock, and the broker seats nothing while s.mu is held.
type admissionWriter struct {
	log  file.Log[types.MachineAdmission]
	ch   chan types.MachineAdmission
	done chan struct{}
	warn *slog.Logger

	// mu orders add against close, so nothing sends on a closed channel.
	mu     sync.Mutex
	closed bool
}

// newAdmissionWriter starts the writer for log; nil log is no store, and
// the writer it returns drops every admission.
func newAdmissionWriter(log *file.Log[types.MachineAdmission], warn *slog.Logger) *admissionWriter {
	if log == nil {
		return nil
	}
	w := &admissionWriter{log: *log, ch: make(chan types.MachineAdmission, admissionBacklog), done: make(chan struct{}), warn: warn}
	go w.run()
	return w
}

func (w *admissionWriter) run() {
	defer close(w.done)
	ctx := context.Background()
	w.compact(ctx)
	n := 0
	for a := range w.ch {
		if err := w.log.Append(ctx, a); err != nil {
			w.warn.WarnContext(ctx, "the broker could not record an admission", slog.String("path", w.log.Path), attr.Error(err))
			continue
		}
		if n++; n%compactEvery == 0 {
			w.compact(ctx)
		}
	}
}

func (w *admissionWriter) compact(ctx context.Context) {
	if w.log.Keep <= 0 {
		return
	}
	if _, err := w.log.Compact(ctx); err != nil {
		w.warn.WarnContext(ctx, "the broker could not compact its admissions", slog.String("path", w.log.Path), attr.Error(err))
	}
}

// add queues a for the store. A full backlog drops a and says so, since the
// broker never waits on its own record.
func (w *admissionWriter) add(a types.MachineAdmission) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	select {
	case w.ch <- a:
	default:
		w.warn.WarnContext(context.Background(), "the broker dropped an admission: its store's writer is behind",
			slog.String("path", w.log.Path), slog.String("target", fmt.Sprintf("%s:%s", a.Project, a.Target)),
			slog.String("outcome", string(a.Outcome)))
	}
}

// close stops taking admissions and returns once every queued one is written.
func (w *admissionWriter) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.ch)
	}
	w.mu.Unlock()
	<-w.done
}
