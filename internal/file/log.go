package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

// Log is an append-only JSON Lines file of T with no key: every line is an entry, and
// [Log.Compact] keeps the newest Keep of them. A line is T's own JSON, so the log never
// parses fields. Appends and compactions take the lock at Path+".lock", waiting up to
// [LockWait], so no append lands between a compaction's read and its rename.
type Log[T any] struct {
	Path string
	// Perm is the file's mode; zero means 0644.
	Perm os.FileMode
	// Keep is the entries a compaction leaves. Compact fails while it is zero.
	Keep int
	// Select chooses the entries a compaction keeps, in file order, from all of them; nil
	// keeps the newest Keep. It sees only complete lines.
	Select func(all []T, keep int) []T
	// SkipSync appends without a sync, as [codec.WithoutSync] does. A compaction always syncs.
	SkipSync bool
}

// Load returns every entry, oldest first. An absent file has none. A last line with no
// newline is an append in flight and is left unread; a complete line that does not decode
// fails the load and names its line.
func (l Log[T]) Load() ([]T, error) {
	all, _, err := readEntries[T](l.Path)
	return all, err
}

// Append adds entries to the end of the log in one write, creating the file and its
// directory. When the file ends in a line no writer finished, Append rewrites it without
// that line rather than glue its own first line onto it, and the log names the drop. ctx
// bounds only the wait for the lock.
func (l Log[T]) Append(ctx context.Context, entries ...T) error {
	if len(entries) == 0 {
		return nil
	}
	return WithLock(ctx, l.Path+".lock", LockWait, func() error {
		torn, err := endsTorn(l.Path)
		if err != nil {
			return err
		}
		if torn {
			all, partial, err := readEntries[T](l.Path)
			if err != nil {
				return err
			}
			if partial {
				l.warnDropped(ctx)
				return writeEntries(codec.RewriteLines, l.Path, l.Perm, append(all, entries...))
			}
		}
		return writeEntries(l.appendLines, l.Path, l.Perm, entries)
	})
}

// Compact rewrites the log to the entries Select keeps once it holds more than Keep plus
// [codec.CompactSlack] of them, or ends in a line no writer finished, and returns the entries
// the file holds afterwards. A log within its bound is read and left as it is, so a caller
// may compact on any schedule. ctx bounds only the wait for the lock.
func (l Log[T]) Compact(ctx context.Context) ([]T, error) {
	if l.Keep <= 0 {
		return nil, fmt.Errorf("file: log %s keeps %d entries, so it cannot compact: set Keep", l.Path, l.Keep)
	}
	var kept []T
	err := WithLock(ctx, l.Path+".lock", LockWait, func() error {
		all, partial, err := readEntries[T](l.Path)
		if err != nil {
			return err
		}
		if !partial && len(all) <= l.Keep+codec.CompactSlack {
			kept = all
			return nil
		}
		kept = l.selectKept(all)
		if partial {
			l.warnDropped(ctx)
		}
		return writeEntries(codec.RewriteLines, l.Path, l.Perm, kept)
	})
	return kept, err
}

func (l Log[T]) selectKept(all []T) []T {
	if l.Select != nil {
		return l.Select(all, l.Keep)
	}
	if len(all) <= l.Keep {
		return all
	}
	return all[len(all)-l.Keep:]
}

func (l Log[T]) appendLines(path string, lines [][]byte, perm os.FileMode) error {
	if l.SkipSync {
		return codec.AppendLines(path, lines, perm, codec.WithoutSync())
	}
	return codec.AppendLines(path, lines, perm)
}

func (l Log[T]) warnDropped(ctx context.Context) {
	slog.With(attr.Component("magus")).WarnContext(ctx, "log rewritten",
		attr.Error(fmt.Errorf("%s ended in a line no writer finished, so the rewrite dropped it", l.Path)))
}

// endsTorn reports whether the file at path is non-empty and its last byte is not a
// newline. It reads one byte, so an append need not read the log to stay line-aligned.
func endsTorn(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return last[0] != '\n', nil
}

// readEntries decodes each complete line at path as a T and reports whether the file ends in
// a line no writer finished.
func readEntries[T any](path string) ([]T, bool, error) {
	read, err := codec.ReadLines(path)
	if err != nil {
		return nil, false, err
	}
	all := make([]T, 0, len(read.Lines))
	for _, line := range read.Lines {
		var v T
		if err := codec.Unmarshal(line.Text, &v); err != nil {
			return nil, false, StoreDiagnostic(&codec.LineError{Path: path, Line: line.N, Err: err})
		}
		all = append(all, v)
	}
	return all, read.Partial, nil
}

func writeEntries[T any](to func(string, [][]byte, os.FileMode) error, path string, perm os.FileMode, entries []T) error {
	lines := make([][]byte, 0, len(entries))
	for _, v := range entries {
		b, err := codec.Marshal(v)
		if err != nil {
			return err
		}
		lines = append(lines, b)
	}
	if perm == 0 {
		perm = 0o644
	}
	return to(path, lines, perm)
}

// StoreDiagnostic gives a store error from libs/codec its magus code: MGS3049 for a store
// an older magus left beside its replacement, MGS3050 for a line that does not decode. An
// error that already carries a code, and any other error, comes back as it is.
func StoreDiagnostic(err error) error {
	var coded *types.DiagnosticError
	var legacy *codec.LegacyStoreError
	var line *codec.LineError
	switch {
	case errors.As(err, &coded):
		return err
	case errors.As(err, &legacy):
		return types.WrapDiagnostic(types.LegacyStorePresent, err, "%v", err)
	case errors.As(err, &line):
		return types.WrapDiagnostic(types.StoreLineUndecodable, err, "%v", err)
	}
	return err
}

// JSONL is a keyed [codec.JSONL] store whose Load and Update errors carry their magus code,
// as [StoreDiagnostic] gives it. Every magus store on codec.JSONL is one of these, so no
// caller maps the codes itself.
type JSONL[T any] struct{ codec.JSONL[T] }

// Load is [codec.JSONL.Load], its error given a code by [StoreDiagnostic].
func (l JSONL[T]) Load() ([]T, error) {
	entries, err := l.JSONL.Load()
	return entries, StoreDiagnostic(err)
}

// Update is [codec.JSONL.Update], its error given a code by [StoreDiagnostic].
func (l JSONL[T]) Update(ctx context.Context, fn func(entries []T) []T) error {
	return StoreDiagnostic(l.JSONL.Update(ctx, fn))
}
