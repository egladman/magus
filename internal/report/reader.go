package report

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/egladman/magus/internal/json"
)

// Line is one record as another magus stage wrote it.
type Line struct {
	Schema int
	Type   string
	// Raw is the record's JSON object, without its newline.
	Raw []byte
}

// Decode unmarshals the record's fields into v.
func (l Line) Decode(v any) error { return json.Unmarshal(l.Raw, v) }

// recordPrefix opens every line a magus writes as a record (see envelope.appendJSONL).
// Requiring it keeps a JSON line some other program printed from reading as a record.
const recordPrefix = `{"schema":`

// ParseLine reads one record line. A line that is not a record as magus writes one, a
// JSON object opening with its schema and carrying a type, is an error.
func ParseLine(b []byte) (Line, error) {
	b = bytes.TrimSpace(b)
	if !bytes.HasPrefix(b, []byte(recordPrefix)) {
		return Line{}, fmt.Errorf("report: not a record: no leading %s", recordPrefix)
	}
	var head struct {
		Schema int    `json:"schema"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return Line{}, fmt.Errorf("report: not a record: %w", err)
	}
	if head.Type == "" {
		return Line{}, errors.New("report: not a record: no type")
	}
	return Line{Schema: head.Schema, Type: head.Type, Raw: bytes.Clone(b)}, nil
}

// EncodeLine writes l as it was read, so a record passed through a stage reaches the
// next one unchanged.
func (l *LineEncoder) EncodeLine(line Line) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.w.Write(append(bytes.Clone(line.Raw), '\n'))
	return err
}

// Reader reads the record stream a magus stage upstream in a pipe writes. It drains its
// source from the moment it is made, so the writer never blocks on a full pipe whether
// or not anybody asks for a record, and keeps every record for the caller. Safe for
// concurrent use.
type Reader struct {
	mu    sync.Mutex
	cond  *sync.Cond
	lines []Line
	next  int
	eof   bool
	err   error
	done  chan struct{}
}

// NewReader starts draining src. A line that is not a record is written to stray as it
// was read, so nothing else the upstream printed is lost; nil discards it.
func NewReader(src io.Reader, stray io.Writer) *Reader {
	r := &Reader{done: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	go r.drain(src, stray)
	return r
}

func (r *Reader) drain(src io.Reader, stray io.Writer) {
	defer close(r.done)
	br := bufio.NewReader(src)
	for {
		b, err := br.ReadBytes('\n')
		if len(b) > 0 {
			if line, perr := ParseLine(b); perr == nil {
				r.mu.Lock()
				r.lines = append(r.lines, line)
				r.cond.Broadcast()
				r.mu.Unlock()
			} else if stray != nil {
				_, _ = stray.Write(b)
			}
		}
		if err != nil {
			r.mu.Lock()
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				r.err = err
			}
			r.eof = true
			r.cond.Broadcast()
			r.mu.Unlock()
			return
		}
	}
}

// Next returns the next record not yet returned, blocking until one arrives. ok is false
// once the writer has closed the stream and every record has been returned.
func (r *Reader) Next(ctx context.Context) (line Line, ok bool, err error) {
	defer r.lockWaiting(ctx)()
	if err := r.await(ctx); err != nil {
		return Line{}, false, err
	}
	if r.next < len(r.lines) {
		line = r.lines[r.next]
		r.next++
		return line, true, nil
	}
	return Line{}, false, r.err
}

// Peek reports whether Next has a record to return, blocking until one arrives or the
// writer closes the stream.
func (r *Reader) Peek(ctx context.Context) (bool, error) {
	defer r.lockWaiting(ctx)()
	if err := r.await(ctx); err != nil {
		return false, err
	}
	if r.next < len(r.lines) {
		return true, nil
	}
	return false, r.err
}

// lockWaiting locks r so that a wait on its cond also ends with ctx, and returns the
// unlock.
func (r *Reader) lockWaiting(ctx context.Context) func() {
	stop := context.AfterFunc(ctx, func() {
		r.mu.Lock()
		r.cond.Broadcast()
		r.mu.Unlock()
	})
	r.mu.Lock()
	return func() {
		r.mu.Unlock()
		stop()
	}
}

// await waits, holding r.mu, for a record not yet returned or the end of the stream.
func (r *Reader) await(ctx context.Context) error {
	for r.next == len(r.lines) && !r.eof {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.cond.Wait()
	}
	return nil
}

// Rest returns every record not yet returned, once the writer has closed the stream.
func (r *Reader) Rest(ctx context.Context) ([]Line, error) {
	var out []Line
	for {
		line, ok, err := r.Next(ctx)
		if err != nil {
			return out, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, line)
	}
}

// Done is closed once the writer has closed the stream and every line of it is read.
func (r *Reader) Done() <-chan struct{} { return r.done }

// All returns every record the stream carried, including those already returned, once
// the writer has closed it.
func (r *Reader) All(ctx context.Context) ([]Line, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Line(nil), r.lines...), r.err
}
