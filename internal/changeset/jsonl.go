package changeset

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
)

// The review state is JSON Lines: one object per line, appended when an entry changes. See
// "Everything is a file" in docs/doctrine.md. A line's encoding is its type's own JSON, so the
// store never parses fields.

// Compaction is the one rewrite. It happens when a file holds more than compactMinLines lines
// and more than compactRatio times as many lines as live entries, so a file stays within a
// constant factor of what it says and small files are never rewritten.
const (
	compactMinLines = 64
	compactRatio    = 4
)

// jsonl is an append-only file of T, one JSON object per line. A key names an entry, and the
// last line for a key is the entry; a line that live reports false for ends its key. A reader
// takes the last line per key, so an update is an append and two writers never rewrite each
// other's entries.
type jsonl[T any] struct {
	path string
	perm os.FileMode
	key  func(T) string
	// live is nil when every line is an entry.
	live func(T) bool
}

// read returns every line, in file order. An absent file has none. A last line with no
// newline is an append in flight and is left unread.
func (l jsonl[T]) read() ([]T, error) {
	b, err := os.ReadFile(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	end := bytes.LastIndexByte(b, '\n')
	if end < 0 {
		return nil, nil
	}
	var all []T
	for n, line := range bytes.Split(b[:end], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", l.path, n+1, err)
		}
		all = append(all, v)
	}
	return all, nil
}

// fold is the entries lines say: each key's last line, in the order those lines appear,
// without the keys that ended.
func (l jsonl[T]) fold(all []T) []T {
	last := make(map[string]int, len(all))
	for i, v := range all {
		last[l.key(v)] = i
	}
	var out []T
	for i, v := range all {
		if last[l.key(v)] == i && (l.live == nil || l.live(v)) {
			out = append(out, v)
		}
	}
	return out
}

// load returns the entries.
func (l jsonl[T]) load() ([]T, error) {
	all, err := l.read()
	if err != nil {
		return nil, err
	}
	return l.fold(all), nil
}

// update shows fn the entries and appends the lines it returns, none when it returns none.
// It runs under the file's lock, which the appends do not need and the compaction does: a
// rewrite from an earlier read would drop a line appended after it.
func (l jsonl[T]) update(ctx context.Context, fn func(entries []T) []T) error {
	return file.WithLock(ctx, l.path+".lock", file.LockWait, func() error {
		all, err := l.read()
		if err != nil {
			return err
		}
		add := fn(l.fold(all))
		if len(add) == 0 {
			return nil
		}
		b, err := encodeLines(add)
		if err != nil {
			return err
		}
		if err := l.appendTo(b); err != nil {
			return err
		}
		all = append(all, add...)
		live := l.fold(all)
		if len(all) <= compactMinLines || len(all) <= compactRatio*len(live) {
			return nil
		}
		b, err = encodeLines(live)
		if err != nil {
			return err
		}
		return file.WriteFileAtomic(l.path, b, l.perm)
	})
}

// appendTo adds b, whole lines, with one write so a reader sees none of it or all of it.
func (l jsonl[T]) appendTo(b []byte) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, l.perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func encodeLines[T any](entries []T) ([]byte, error) {
	var buf bytes.Buffer
	for _, v := range entries {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// errLegacyJSON names a JSON document an older magus wrote where this one keeps JSON Lines.
// Reading it would be a dual reader, and ignoring it would drop the marks it holds with no
// word, so it is an error that says how to clear it.
func errLegacyJSON(path string) error {
	return fmt.Errorf("%s is the JSON document of an older magus and is no longer read; move it aside with `mv %s %s.old`", path, path, path)
}

// legacyJSON reports the files among names, beside the review state, that still hold the old
// format.
func (s *Store) legacyJSON(names ...string) error {
	if s.viewedPath == "" {
		return nil
	}
	var errs []error
	for _, name := range names {
		path := filepath.Join(filepath.Dir(s.viewedPath), name)
		if _, err := os.Lstat(path); err == nil {
			errs = append(errs, errLegacyJSON(path))
		}
	}
	return errors.Join(errs...)
}
