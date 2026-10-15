package review

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

// Compaction is the one rewrite: a file holding more than compactMinLines lines and more than
// compactRatio times as many lines as entries is rewritten to its entries, by rename.
const (
	compactMinLines = 64
	compactRatio    = 4
)

// jsonl is an append-only file of T, one JSON object per line, whose last line for a key is
// that key's entry. A copy of the one in internal/changeset, which keeps it unexported; both
// belong in internal/file, which holds neither.
type jsonl[T any] struct {
	path string
	perm os.FileMode
	key  func(T) string
}

// load returns each key's last line, in the order those lines appear. A last line with no
// newline is an append in flight and is left unread.
func (l jsonl[T]) load() ([]T, error) {
	all, err := l.read()
	if err != nil {
		return nil, err
	}
	return l.fold(all), nil
}

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

func (l jsonl[T]) fold(all []T) []T {
	last := make(map[string]int, len(all))
	for i, v := range all {
		last[l.key(v)] = i
	}
	var out []T
	for i, v := range all {
		if last[l.key(v)] == i {
			out = append(out, v)
		}
	}
	return out
}

// update shows fn the entries and appends the lines it returns, none when it returns none.
// It runs under the file's lock, which the appends do not need and the compaction does.
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
		if err := f.Close(); err != nil {
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
