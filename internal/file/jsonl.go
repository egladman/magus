package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
)

// A store is JSON Lines: one object per line, appended when an entry changes, the last line
// for a key being the entry. See "Everything is a file" in docs/doctrine.md.

// Lines is what [ReadLines] found in a JSON Lines file.
type Lines struct {
	// Lines are the complete lines, blank ones dropped, in file order.
	Lines []Line
	// Partial says the file ends in bytes no newline closes: an append caught mid-write, or
	// the remains of a writer that died in one. A writer appending after them would glue its
	// first line onto them, so it rewrites the file instead.
	Partial bool
}

// Line is one complete line and its 1-based line number.
type Line struct {
	N    int
	Text []byte
}

// ReadLines reads the JSON Lines file at path. An absent file has no lines.
func ReadLines(path string) (Lines, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Lines{}, nil
	}
	if err != nil {
		return Lines{}, err
	}
	end := bytes.LastIndexByte(raw, '\n') + 1
	out := Lines{Partial: len(bytes.TrimSpace(raw[end:])) > 0}
	n := 0
	for line := range bytes.SplitSeq(raw[:end], []byte{'\n'}) {
		n++
		if len(bytes.TrimSpace(line)) > 0 {
			out.Lines = append(out.Lines, Line{N: n, Text: line})
		}
	}
	return out, nil
}

// AppendLines adds lines to the end of the file at path in one write, so a reader sees each
// of them whole or not at all, and syncs it. It creates the file and its directory.
func AppendLines(path string, lines [][]byte, perm os.FileMode, opts ...AppendOption) error {
	var o appendOptions
	for _, opt := range opts {
		opt(&o)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(joinLines(lines))
	if err == nil && !o.skipSync {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// AppendOption changes how [AppendLines] writes.
type AppendOption func(*appendOptions)

type appendOptions struct{ skipSync bool }

// WithoutSync skips the sync: the lines survive the process dying but not the machine. It
// is for a store appended to on a hot path, such as once per RPC, where a sync per append
// costs more than a lost tail; on macOS a sync is an F_FULLFSYNC and takes milliseconds.
func WithoutSync() AppendOption { return func(o *appendOptions) { o.skipSync = true } }

// RewriteLines replaces the file at path with lines through a rename, so a reader sees the
// old file or the new one. It creates the directory.
func RewriteLines(path string, lines [][]byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return WriteFileAtomic(path, joinLines(lines), perm)
}

func joinLines(lines [][]byte) []byte {
	var b bytes.Buffer
	for _, l := range lines {
		b.Write(l)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// CompactDue reports whether a file of lines lines saying keys entries is due a rewrite to
// one line per entry. Every read decodes every line, so the bound keeps a read within twice
// the entries plus [CompactSlack], while one entry changing alone rewrites the file at most
// once per CompactSlack appends.
func CompactDue(lines, keys int) bool { return lines > 2*keys+CompactSlack }

// CompactSlack is the lines a store may hold past twice its entries.
const CompactSlack = 64

// RefuseOld fails when old, a file an older magus kept a store in that this one no longer
// reads, exists. Reading it would be a second reader to keep, and passing it by in silence
// would drop what it holds with no word, so the error names it and how to move it aside.
func RefuseOld(old string) error {
	_, err := os.Lstat(old)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%s is a store an older magus wrote and this one does not read, so move it aside with `mv %s %s.old`", old, old, old)
}

// JSONL is an append-only JSON Lines file of T. Key names an entry, and the last line for a
// key is the entry; a line Live reports false for ends its key. A line is T's own JSON, so
// the store never parses fields.
type JSONL[T any] struct {
	Path string
	// Perm is the file's mode; zero means 0644.
	Perm os.FileMode
	Key  func(T) string
	// Live is nil when every line is an entry.
	Live func(T) bool
	// Old is the file an older magus kept this store in, or "". While it exists, Load and
	// Update fail with [RefuseOld]'s error.
	Old string
}

// Load returns the entries, in the order their last lines appear. An absent file has none.
// A last line with no newline is an append in flight and is left unread.
func (l JSONL[T]) Load() ([]T, error) {
	all, _, err := l.read()
	if err != nil {
		return nil, err
	}
	return l.fold(all), nil
}

// Update shows fn the entries and appends the lines it returns, none when it returns none.
// It runs under the lock at Path+".lock", waiting up to [LockWait]: the appends do not need
// it and the compaction does, since a rewrite from an earlier read would drop a line
// appended after it. The file is rewritten to its entries when [CompactDue] says so, and
// when it ends in a line no writer finished, which the rewrite drops and the log names.
// ctx bounds only the wait for the lock.
func (l JSONL[T]) Update(ctx context.Context, fn func(entries []T) []T) error {
	return WithLock(ctx, l.Path+".lock", LockWait, func() error {
		all, partial, err := l.read()
		if err != nil {
			return err
		}
		add := fn(l.fold(all))
		if len(add) == 0 {
			return nil
		}
		all = append(all, add...)
		live := l.fold(all)
		if !partial && !CompactDue(len(all), len(live)) {
			return l.write(func(path string, lines [][]byte, perm os.FileMode) error {
				return AppendLines(path, lines, perm)
			}, add)
		}
		if partial {
			slog.With(attr.Component("magus")).WarnContext(ctx, "store rewritten",
				attr.Error(fmt.Errorf("%s ended in a line no writer finished, so the rewrite dropped it", l.Path)))
		}
		return l.write(RewriteLines, live)
	})
}

func (l JSONL[T]) read() ([]T, bool, error) {
	if l.Old != "" {
		if err := RefuseOld(l.Old); err != nil {
			return nil, false, err
		}
	}
	read, err := ReadLines(l.Path)
	if err != nil {
		return nil, false, err
	}
	all := make([]T, 0, len(read.Lines))
	for _, line := range read.Lines {
		var v T
		if err := json.Unmarshal(line.Text, &v); err != nil {
			return nil, false, fmt.Errorf("%s:%d: %w", l.Path, line.N, err)
		}
		all = append(all, v)
	}
	return all, read.Partial, nil
}

// fold is the entries the lines say: each key's last line, in the order those lines appear,
// without the keys that ended.
func (l JSONL[T]) fold(all []T) []T {
	last := make(map[string]int, len(all))
	for i, v := range all {
		last[l.Key(v)] = i
	}
	var out []T
	for i, v := range all {
		if last[l.Key(v)] == i && (l.Live == nil || l.Live(v)) {
			out = append(out, v)
		}
	}
	return out
}

func (l JSONL[T]) write(to func(string, [][]byte, os.FileMode) error, entries []T) error {
	lines := make([][]byte, 0, len(entries))
	for _, v := range entries {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		lines = append(lines, b)
	}
	perm := l.Perm
	if perm == 0 {
		perm = 0o644
	}
	return to(l.Path, lines, perm)
}
