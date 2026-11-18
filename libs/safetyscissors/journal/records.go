package journal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"path/filepath"

	"github.com/egladman/magus/libs/codec"
)

const journalExt = ".jsonl"

// tailWindow bounds the tail [ReadInvocation] reads for the last record. The
// Finished record is the last line, so the window is slack for a long final
// output line.
const tailWindow = 64 << 10

// ValidInvocationID reports whether id can name a journal file: a letter or
// digit, then letters, digits, hyphens and underscores. A reader refuses an id
// that fails it, with an error wrapping fs.ErrNotExist.
//
// A copy of session.ValidFileID's loop: that rule also judges host session ids
// and fold kinds, so it can widen for a host while this one stays the mint's.
func ValidInvocationID(id string) bool {
	if id == "" || !isAlnum(id[0]) {
		return false
	}
	for i := 1; i < len(id); i++ {
		if c := id[i]; !isAlnum(c) && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// openJournal opens the journal dir holds for id, refusing an id
// [ValidInvocationID] does not accept.
func openJournal(dir, id string) (*os.File, string, error) {
	name := id + journalExt
	path := filepath.Join(dir, name)
	if !ValidInvocationID(id) {
		return nil, path, fmt.Errorf("journal: %q is not an invocation id: %w", id, fs.ErrNotExist)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, path, fmt.Errorf("journal: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name)
	if err != nil {
		return nil, path, fmt.Errorf("journal: %w", err)
	}
	return f, path, nil
}

// Records returns the records of the journal dir holds for id, in file order.
// A *codec.LineError is one complete line that does not decode, and the
// records after it still come. Any other error is the read failing, and it is
// the last pair. A line no newline closes is left out.
func Records(ctx context.Context, dir, id string) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		f, path, err := openJournal(dir, id)
		if err != nil {
			yield(Record{}, err)
			return
		}
		defer func() { _ = f.Close() }()
		br := bufio.NewReader(f)
		for n := 1; ; n++ {
			if err := ctx.Err(); err != nil {
				yield(Record{}, err)
				return
			}
			raw, err := br.ReadBytes('\n')
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(Record{}, fmt.Errorf("journal: read %s: %w", path, err))
				return
			}
			if !yieldLine(yield, path, n, raw) {
				return
			}
		}
	}
}

// yieldLine decodes one complete line and yields it, skipping a blank one. It
// reports whether the caller still wants records.
func yieldLine(yield func(Record, error) bool, path string, n int, raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return true
	}
	var r Record
	if err := codec.Unmarshal(raw, &r); err != nil {
		return yield(Record{}, &codec.LineError{Path: path, Line: n, Err: err})
	}
	return yield(r, nil)
}

// RecordsSince reads the journal for id from byte offset to its last complete
// line, under the rule Records states. next is the offset just past those
// records, for the next call. A journal shorter than offset was replaced and
// is read from its start. A *codec.LineError's Line counts from offset.
func RecordsSince(ctx context.Context, dir, id string, offset int64) (records iter.Seq2[Record, error], next int64, err error) {
	if err := ctx.Err(); err != nil {
		return nil, offset, err
	}
	f, path, err := openJournal(dir, id)
	if err != nil {
		return nil, offset, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, offset, fmt.Errorf("journal: %w", err)
	}
	if info.Size() < offset {
		offset = 0
	}
	chunk := make([]byte, info.Size()-offset)
	n, err := f.ReadAt(chunk, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, offset, fmt.Errorf("journal: read %s: %w", path, err)
	}
	end := bytes.LastIndexByte(chunk[:n], '\n')
	if end < 0 {
		return func(func(Record, error) bool) {}, offset, nil
	}
	lines := chunk[:end]
	return func(yield func(Record, error) bool) {
		for i, raw := range bytes.Split(lines, []byte{'\n'}) {
			if err := ctx.Err(); err != nil {
				yield(Record{}, err)
				return
			}
			if !yieldLine(yield, path, i+1, raw) {
				return
			}
		}
	}, offset + int64(end) + 1, nil
}

// ReadInvocation returns the invocation whose journal dir holds for id. It
// reads the journal's first line and its last complete one, never the whole
// file.
func ReadInvocation(ctx context.Context, dir, id string) (Invocation, error) {
	if err := ctx.Err(); err != nil {
		return Invocation{}, err
	}
	f, path, err := openJournal(dir, id)
	if err != nil {
		return Invocation{}, err
	}
	defer func() { _ = f.Close() }()
	inv := Invocation{ID: id}
	head, err := bufio.NewReader(io.LimitReader(f, tailWindow)).ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return inv, nil
		}
		return Invocation{}, fmt.Errorf("journal: read %s: %w", path, err)
	}
	var first Record
	if codec.Unmarshal(bytes.TrimSpace(head), &first) == nil {
		if started, ok := first.Started(); ok {
			inv.Started = first.Time
			inv.Command = started.Command
			inv.Version = started.Version
		}
	}
	last, ok, err := lastRecord(ctx, f, path)
	if err != nil {
		return Invocation{}, err
	}
	if ok {
		inv.Finished = last.Time
		if finished, ok := last.Finished(); ok {
			inv.Status = finished.Status
		}
	}
	return inv, nil
}

// lastRecord is the last complete line in f's final tailWindow bytes that
// decodes. A window that starts mid-file opens mid-line, and scanning back
// reaches the wanted line before that partial one.
func lastRecord(ctx context.Context, f *os.File, path string) (Record, bool, error) {
	info, err := f.Stat()
	if err != nil {
		return Record{}, false, fmt.Errorf("journal: %w", err)
	}
	off := max(info.Size()-tailWindow, 0)
	data := make([]byte, info.Size()-off)
	n, err := f.ReadAt(data, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return Record{}, false, fmt.Errorf("journal: read %s: %w", path, err)
	}
	end := bytes.LastIndexByte(data[:n], '\n')
	if end < 0 {
		return Record{}, false, nil
	}
	lines := bytes.Split(data[:end], []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return Record{}, false, err
		}
		raw := bytes.TrimSpace(lines[i])
		if len(raw) == 0 {
			continue
		}
		var r Record
		if codec.Unmarshal(raw, &r) == nil && r.Kind() != "" {
			return r, true, nil
		}
	}
	return Record{}, false, nil
}
