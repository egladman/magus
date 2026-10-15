package guard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
)

// The guard's memos in a cache dir are JSON Lines: one object per line, appended when an
// entry changes. See "Everything is a file" in docs/doctrine.md.

// Compaction is the one rewrite: a file holding more than compactMinLines lines and more than
// compactRatio times as many lines as entries is rewritten to its entries, by rename.
const (
	compactMinLines = 64
	compactRatio    = 4
)

// jsonl is an append-only file of T whose last line for a key is that key's entry, and a line
// that live reports false for ends its key. A copy of the one in internal/changeset, which
// keeps it unexported; both belong in internal/file, which holds neither.
type jsonl[T any] struct {
	path string
	perm os.FileMode
	key  func(T) string
	// live is nil when every line is an entry.
	live func(T) bool
}

// load returns the entries, in the order their last lines appear. A last line with no newline
// is an append in flight and is left unread.
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
		if last[l.key(v)] == i && (l.live == nil || l.live(v)) {
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

// refuseOldJSON reports a JSON document an older magus left in cacheDir. It is neither read nor
// skipped without a word: the guard goes on with the defaults, and the error says how to clear
// the file.
func refuseOldJSON(cacheDir, name string) {
	path := filepath.Join(cacheDir, name)
	if _, err := os.Lstat(path); err != nil {
		return
	}
	reportGuardMemo(fmt.Errorf("%s is the JSON document of an older magus and is no longer read; move it aside with `mv %s %s.old`", path, path, path))
}

// reportGuardMemo logs a memo that could not be read. The guard goes on without it, so the log
// is how a person learns it was not used.
func reportGuardMemo(err error) {
	slog.With(attr.Component("magus")).ErrorContext(context.Background(), "guard memo", attr.Error(err))
}
