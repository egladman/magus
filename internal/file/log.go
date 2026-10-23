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
	// SkipSync appends without a sync, as [WithoutSync] does. A compaction always syncs.
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
				return writeEntries(RewriteLines, l.Path, l.Perm, append(all, entries...))
			}
		}
		return writeEntries(l.appendLines, l.Path, l.Perm, entries)
	})
}

// Compact rewrites the log to the entries Select keeps once it holds more than Keep plus
// [CompactSlack] of them, or ends in a line no writer finished, and returns the entries
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
		if !partial && len(all) <= l.Keep+CompactSlack {
			kept = all
			return nil
		}
		kept = l.selectKept(all)
		if partial {
			l.warnDropped(ctx)
		}
		return writeEntries(RewriteLines, l.Path, l.Perm, kept)
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
		return AppendLines(path, lines, perm, WithoutSync())
	}
	return AppendLines(path, lines, perm)
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
