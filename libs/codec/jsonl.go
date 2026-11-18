package codec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// A store is JSON Lines: one object per line, appended when an entry changes, the last
// line for a key being the entry. See docs/design/principles/everything-is-a-file.md.

// Lines is what [ReadLines] found in a JSON Lines file.
type Lines struct {
	// Lines are the complete lines, blank ones dropped, in file order.
	Lines []Line
	// Partial says the file ends in bytes no newline closes: an append caught mid-write, or
	// the remains of a writer that died in one. A writer appending after them would glue its
	// first line onto them, so it rewrites the file instead.
	Partial bool
	// PartialLine is the 1-based number of the line Partial names, and 0 without one.
	PartialLine int
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
	// raw[:end] ends in a newline, so its last split is the empty start of line n.
	if out.Partial {
		out.PartialLine = n
	}
	return out, nil
}

// AppendLines adds lines to the end of the file at path in one write, so a reader sees each
// of them whole or not at all, and syncs it. It creates the file with perm and its
// directory with the mode [JSONLConfig.Perm] documents.
func AppendLines(path string, lines [][]byte, perm os.FileMode, opts ...AppendOption) error {
	var o appendOptions
	for _, opt := range opts {
		opt(&o)
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode(perm)); err != nil {
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
// old file or the new one. It creates the directory with the mode [JSONLConfig.Perm] documents.
func RewriteLines(path string, lines [][]byte, perm os.FileMode) error {
	return writeAtomic(path, joinLines(lines), perm)
}

func joinLines(lines [][]byte) []byte {
	var b bytes.Buffer
	for _, l := range lines {
		b.Write(l)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// writeAtomic writes data to a temp file beside path, syncs it and renames it over path.
func writeAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode(perm)); err != nil {
		return err
	}
	// Same directory, because rename is atomic only within one filesystem and
	// os.TempDir is routinely another mount.
	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".%s.tmp.*", filepath.Base(path)))
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// dirMode is the mode of a directory holding files of mode perm: searchable by whoever may
// read them, so 0644 files get a 0755 directory and 0600 files a 0700 one. Each permission
// bit outside the nine is dropped.
func dirMode(perm os.FileMode) os.FileMode {
	perm &= os.ModePerm
	return perm | (perm&0o444)>>2
}

// CompactDue reports whether a file of lines lines saying keys entries is due a rewrite to
// one line per entry. Every read decodes every line, so the bound keeps a read within twice
// the entries plus [CompactSlack], while one entry changing alone rewrites the file at most
// once per CompactSlack appends.
func CompactDue(lines, keys int) bool { return lines > 2*keys+CompactSlack }

// CompactSlack is the lines a store may hold past twice its entries.
const CompactSlack = 64

var (
	// ErrLegacyStore matches a [*LegacyStoreError].
	ErrLegacyStore = errors.New("store version conflict")
	// ErrLineUndecodable matches a [*LineError].
	ErrLineUndecodable = errors.New("store line does not decode")
	// ErrUnfinishedLine matches an [*UnfinishedLineError].
	ErrUnfinishedLine = errors.New("store ends in a line no writer finished")
)

// UnfinishedLineError is an Update refused because its rewrite would drop the line no
// writer finished at Path's line Line, and the store has neither a Log nor an OnDrop to
// say so.
type UnfinishedLineError struct {
	Path string
	// Line is the 1-based line number.
	Line int
}

func (e *UnfinishedLineError) Error() string {
	return fmt.Sprintf("%s:%d: %v: dropping it in a rewrite needs a Log or an OnDrop to report it", e.Path, e.Line, ErrUnfinishedLine)
}

func (e *UnfinishedLineError) Unwrap() error { return ErrUnfinishedLine }

// Dropped is the line no writer finished that an Update's rewrite dropped.
type Dropped struct {
	Path string
	// Line is the 1-based line number.
	Line int
}

// LegacyStoreError is the file an older writer kept a store in, still present beside the
// file that replaced it.
type LegacyStoreError struct{ Path string }

func (e *LegacyStoreError) Error() string {
	return fmt.Sprintf("%v: %s was written by an older writer: preserve it and use a compatible build until its active writers stop,"+
		" then reconcile its records before switching stores", ErrLegacyStore, e.Path)
}

func (e *LegacyStoreError) Unwrap() error { return ErrLegacyStore }

// LineError is a complete line of a store at Path that does not decode as its entry.
type LineError struct {
	Path string
	// Line is the 1-based line number.
	Line int
	Err  error
}

func (e *LineError) Error() string { return fmt.Sprintf("%s:%d: %v", e.Path, e.Line, e.Err) }

func (e *LineError) Unwrap() []error { return []error{ErrLineUndecodable, e.Err} }

// RefuseOld preserves a store this build cannot read: it fails with a [*LegacyStoreError]
// while old exists. Moving it aside while an older writer still writes it would create two
// independent accounts of the same state.
func RefuseOld(old string) error {
	_, err := os.Lstat(old)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return &LegacyStoreError{Path: old}
}

// JSONL is an append-only JSON Lines file of T. Key names an entry, and the last line for a
// key is the entry; a line Live reports false for ends its key. A line is T's own JSON, so
// the store never parses fields.
//
// Build one with [NewJSONL]. The zero JSONL was never built, and Load and Update refuse it
// with a [*JSONLConfigError].
type JSONL[T any] struct {
	path   string
	perm   os.FileMode
	key    func(T) string
	live   func(T) bool
	old    string
	log    *slog.Logger
	onDrop func(context.Context, Dropped)
}

// JSONLConfig is what [NewJSONL] builds a [JSONL] from. Path, Perm and Key are required.
type JSONLConfig[T any] struct {
	Path string
	// Perm is the file's mode, and zero is refused. Its directory is created with
	// read and search for whoever may read the file: 0600 files get a 0700 directory.
	Perm os.FileMode
	// Key names an entry.
	Key func(T) string
	// Live reports whether a line is an entry; see [WithLive].
	Live func(T) bool
	// Old is the file an older writer kept this store in; see [WithOld].
	Old string
	// Log receives the warning an Update logs when it drops a line no writer finished.
	Log *slog.Logger
	// OnDrop is told of each line no writer finished that an Update drops. With neither
	// it nor Log set, such an Update fails with an [*UnfinishedLineError] and writes
	// nothing.
	OnDrop func(context.Context, Dropped)

	errs []error
}

// JSONLOption adjusts a [JSONLConfig] before [NewJSONL] validates it. An option given a nil
// or empty value records an [*OptionError] on the config rather than ignoring it, so
// NewJSONL refuses the construction.
type JSONLOption[T any] func(*JSONLConfig[T])

// OptionError is a [JSONLOption] given a value it cannot use.
type OptionError struct {
	Option  string
	Problem string
}

func (e *OptionError) Error() string {
	return fmt.Sprintf("codec: option %s: %s", e.Option, e.Problem)
}

// JSONLConfigError is a [JSONLConfig] field [NewJSONL] cannot build a store from.
type JSONLConfigError struct {
	Field   string
	Problem string
}

func (e *JSONLConfigError) Error() string {
	return fmt.Sprintf("codec: JSONL %s: %s", e.Field, e.Problem)
}

func (c *JSONLConfig[T]) fail(option, problem string) {
	c.errs = append(c.errs, &OptionError{Option: option, Problem: problem})
}

// WithLive sets which lines are entries: a line live reports false for ends its key.
// Without it every line is an entry. A nil function is an *[OptionError].
func WithLive[T any](live func(T) bool) JSONLOption[T] {
	return func(c *JSONLConfig[T]) {
		if live == nil {
			c.fail("WithLive", "nil function")
			return
		}
		c.Live = live
	}
}

// WithOld sets the file an older writer kept this store in. While it exists, Load and
// Update fail with [RefuseOld]'s error. An empty path is an *[OptionError].
func WithOld[T any](old string) JSONLOption[T] {
	return func(c *JSONLConfig[T]) {
		if old == "" {
			c.fail("WithOld", "empty path")
			return
		}
		c.Old = old
	}
}

// WithLog sets the logger told of the line an Update drops. A nil logger is an
// *[OptionError].
func WithLog[T any](l *slog.Logger) JSONLOption[T] {
	return func(c *JSONLConfig[T]) {
		if l == nil {
			c.fail("WithLog", "nil logger")
			return
		}
		c.Log = l
	}
}

// WithOnDrop sets the function told of each line an Update drops. A nil function is an
// *[OptionError].
func WithOnDrop[T any](fn func(context.Context, Dropped)) JSONLOption[T] {
	return func(c *JSONLConfig[T]) {
		if fn == nil {
			c.fail("WithOnDrop", "nil function")
			return
		}
		c.OnDrop = fn
	}
}

// NewJSONL builds a store from cfg and opts. It fails with every problem found, joined: an
// [*OptionError] for each refused option, and a [*JSONLConfigError] for an empty Path, a
// Perm that is zero or holds bits outside [os.ModePerm], and a nil Key.
func NewJSONL[T any](cfg JSONLConfig[T], opts ...JSONLOption[T]) (JSONL[T], error) {
	for _, opt := range opts {
		opt(&cfg)
	}
	errs := cfg.errs
	if cfg.Path == "" {
		errs = append(errs, &JSONLConfigError{Field: "Path", Problem: "empty"})
	}
	if cfg.Perm == 0 || cfg.Perm&^os.ModePerm != 0 {
		errs = append(errs, &JSONLConfigError{Field: "Perm", Problem: fmt.Sprintf("%v is not a nonzero permission mode", cfg.Perm)})
	}
	if cfg.Key == nil {
		errs = append(errs, &JSONLConfigError{Field: "Key", Problem: "nil function"})
	}
	if err := errors.Join(errs...); err != nil {
		return JSONL[T]{}, err
	}
	return JSONL[T]{path: cfg.Path, perm: cfg.Perm, key: cfg.Key, live: cfg.Live, old: cfg.Old, log: cfg.Log, onDrop: cfg.OnDrop}, nil
}

// Path is the file the store keeps its lines in.
func (l JSONL[T]) Path() string { return l.path }

func (l JSONL[T]) built() error {
	if l.key == nil {
		return &JSONLConfigError{Field: "JSONL", Problem: "not built by NewJSONL"}
	}
	return nil
}

// Load returns the entries, in the order their last lines appear. An absent file has none.
// A last line with no newline is an append in flight and is left unread. A complete line
// that does not decode fails the load with a [*LineError].
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
// when it ends in a line no writer finished, which the rewrite drops and Log and OnDrop
// are told of. ctx bounds only the wait for the lock.
func (l JSONL[T]) Update(ctx context.Context, fn func(entries []T) []T) error {
	if err := l.built(); err != nil {
		return err
	}
	// The lock file sits beside the store, so its directory takes the store's mode before
	// WithLock would create it world-searchable.
	if err := os.MkdirAll(filepath.Dir(l.path), dirMode(l.perm)); err != nil {
		return fmt.Errorf("codec: %s: %w", l.path, err)
	}
	return WithLock(ctx, l.path+".lock", LockWait, func() error {
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
		if partial == 0 && !CompactDue(len(all), len(live)) {
			return l.write(func(path string, lines [][]byte, perm os.FileMode) error {
				return AppendLines(path, lines, perm)
			}, add)
		}
		if partial == 0 {
			return l.write(RewriteLines, live)
		}
		if l.log == nil && l.onDrop == nil {
			return &UnfinishedLineError{Path: l.path, Line: partial}
		}
		if err := l.write(RewriteLines, live); err != nil {
			return err
		}
		if l.log != nil {
			l.log.WarnContext(ctx, "store rewritten", slog.String("path", l.path), slog.Int("line", partial),
				slog.String("why", l.path+" ended in a line no writer finished, so the rewrite dropped it"))
		}
		if l.onDrop != nil {
			l.onDrop(ctx, Dropped{Path: l.path, Line: partial})
		}
		return nil
	})
}

// read returns the lines' entries and the number of the line no writer finished, 0 when
// none.
func (l JSONL[T]) read() ([]T, int, error) {
	if err := l.built(); err != nil {
		return nil, 0, err
	}
	if l.old != "" {
		if err := RefuseOld(l.old); err != nil {
			return nil, 0, err
		}
	}
	read, err := ReadLines(l.path)
	if err != nil {
		return nil, 0, err
	}
	all := make([]T, 0, len(read.Lines))
	for _, line := range read.Lines {
		var v T
		if err := Unmarshal(line.Text, &v); err != nil {
			return nil, 0, &LineError{Path: l.path, Line: line.N, Err: err}
		}
		all = append(all, v)
	}
	return all, read.PartialLine, nil
}

// fold is the entries the lines say: each key's last line, in the order those lines appear,
// without the keys that ended.
func (l JSONL[T]) fold(all []T) []T {
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

func (l JSONL[T]) write(to func(string, [][]byte, os.FileMode) error, entries []T) error {
	lines := make([][]byte, 0, len(entries))
	for _, v := range entries {
		b, err := Marshal(v)
		if err != nil {
			return err
		}
		lines = append(lines, b)
	}
	return to(l.path, lines, l.perm)
}
