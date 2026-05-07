package report

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Writer is an async JSONL event sink; safe for concurrent use. A single drain
// goroutine encodes events through a buffered writer. Default policy on a full
// queue is drop+count; use [WithBlockOnFull] for lossless capture.
// I/O errors surface via [Writer.Stats.LastErr]; encoding stops after the first.
type Writer struct {
	f      *os.File
	bw     *bufio.Writer
	ch     chan envelope
	done   chan struct{} // closed by drain when it exits
	quit   chan struct{} // closed by Close to signal drain to stop accepting
	filter *Filter
	block  bool
	flushN int
	flushT time.Duration

	recorded atomic.Uint64
	dropped  atomic.Uint64
	filtered atomic.Uint64
	flushed  atomic.Uint64

	mu      sync.Mutex
	lastErr error

	closeOnce sync.Once
}

// Stats is a best-effort (non-transactional) snapshot of Writer counters and the latest drain error.
type Stats struct {
	Recorded   uint64 // events accepted into the queue
	Dropped    uint64 // events rejected because the queue was full
	Filtered   uint64 // events rejected by the filter before queue send
	Flushed    uint64 // events written to the underlying writer
	LastErr    error  // first drain-goroutine encoding/IO error, if any
	QueueDepth int    // approximate count currently in the channel
}

type writerCfg struct {
	block     bool
	queueSize int
	flushN    int
	flushT    time.Duration
	filter    *Filter
}

func defaultCfg() writerCfg {
	return writerCfg{
		queueSize: 4096,
		flushN:    64,
		flushT:    100 * time.Millisecond,
	}
}

// Option configures a [Writer].
type Option func(*writerCfg)

// WithBlockOnFull makes producers block when the channel is full.
// Default is drop+count.
func WithBlockOnFull() Option { return func(c *writerCfg) { c.block = true } }

// WithQueueSize sets the buffered channel capacity. Default 4096.
func WithQueueSize(n int) Option {
	return func(c *writerCfg) {
		if n > 0 {
			c.queueSize = n
		}
	}
}

// WithFilter installs a parsed Filter; non-admitted events are counted in Stats.Filtered.
func WithFilter(f *Filter) Option { return func(c *writerCfg) { c.filter = f } }

// NewWriter wraps w in a Writer. The caller owns w; Close flushes but does not close w.
func NewWriter(w io.Writer, opts ...Option) *Writer {
	return newWriter(nil, w, opts...)
}

// OpenWriter opens path in append+create mode and returns a Writer.
// The caller must call Close when done to flush and release the file.
func OpenWriter(path string, opts ...Option) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("report: open %q: %w", path, err)
	}
	return newWriter(f, f, opts...), nil
}

func newWriter(f *os.File, dst io.Writer, opts ...Option) *Writer { //nolint:revive // intentional exported/unexported pair, not a typo
	cfg := defaultCfg()
	for _, o := range opts {
		o(&cfg)
	}
	w := &Writer{
		f:      f,
		bw:     bufio.NewWriter(dst),
		ch:     make(chan envelope, cfg.queueSize),
		done:   make(chan struct{}),
		quit:   make(chan struct{}),
		filter: cfg.filter,
		block:  cfg.block,
		flushN: cfg.flushN,
		flushT: cfg.flushT,
	}
	go w.drain()
	return w
}

func (w *Writer) record(e any) error {
	typ := typeOf(e)
	if typ == "" {
		return fmt.Errorf("report: unregistered event type %T", e)
	}
	if w.filter != nil && !w.filter.Admit(typ) {
		w.filtered.Add(1)
		return nil
	}
	env := envelope{Type: typ, Body: e}
	if w.block {
		select {
		case w.ch <- env:
			w.recorded.Add(1)
		case <-w.quit:
			w.dropped.Add(1) // Close was called; drop to avoid blocking
		}
		return nil
	}
	select {
	case w.ch <- env:
		w.recorded.Add(1)
	case <-w.quit:
		w.dropped.Add(1) // Close was called
	default:
		w.dropped.Add(1)
	}
	return nil
}

func (w *Writer) drain() {
	defer close(w.done)

	ticker := time.NewTicker(w.flushT)
	defer ticker.Stop()

	var batch int
	flush := func() {
		if err := w.bw.Flush(); err != nil {
			w.setErr(err)
		}
		batch = 0
	}

	for {
		select {
		case env := <-w.ch:
			if w.hasErr() {
				w.dropped.Add(1)
				continue
			}
			if err := env.writeJSONL(w.bw); err != nil {
				w.setErr(err)
				continue
			}
			w.flushed.Add(1)
			batch++
			if batch >= w.flushN {
				flush()
			}
		case <-w.quit:
			// Drain whatever is already buffered before exiting.
			for {
				select {
				case env := <-w.ch:
					if !w.hasErr() {
						if err := env.writeJSONL(w.bw); err != nil {
							w.setErr(err)
						} else {
							w.flushed.Add(1)
							batch++
						}
					} else {
						w.dropped.Add(1)
					}
				default:
					flush()
					return
				}
			}
		case <-ticker.C:
			if batch > 0 {
				flush()
			}
		}
	}
}

// Close signals the drain goroutine, waits for it, and (for OpenWriter files) closes the file. Idempotent.
func (w *Writer) Close() error {
	w.closeOnce.Do(func() {
		close(w.quit)
		<-w.done
	})
	var errs []error
	if drainErr := w.peekErr(); drainErr != nil {
		errs = append(errs, drainErr)
	}
	if w.f != nil {
		if err := w.f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Stats returns a best-effort snapshot of the writer counters.
func (w *Writer) Stats() Stats {
	w.mu.Lock()
	err := w.lastErr
	w.mu.Unlock()
	return Stats{
		Recorded:   w.recorded.Load(),
		Dropped:    w.dropped.Load(),
		Filtered:   w.filtered.Load(),
		Flushed:    w.flushed.Load(),
		LastErr:    err,
		QueueDepth: len(w.ch),
	}
}

func (w *Writer) setErr(err error) {
	w.mu.Lock()
	first := w.lastErr == nil
	if first {
		w.lastErr = err
	}
	w.mu.Unlock()
	if first {
		slog.Error("report: drain failed", "error", err)
	}
}

func (w *Writer) hasErr() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr != nil
}

func (w *Writer) peekErr() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}
