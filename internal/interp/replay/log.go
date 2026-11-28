package replay

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/libs/codec"
)

const recordFile = "calls.jsonl"

// Log is a replay log open for writing. It holds the record's one-writer claim
// until Close. It is safe for concurrent use.
type Log struct {
	path string
	lock *flock.Flock

	mu   sync.Mutex
	f    *os.File
	open map[int]Call
	// imports is whether the record holds its imports line or the line saying why not.
	imports bool
}

// OpenLog opens the record of ref under dir for appending, writing h first when the
// record is new. The claim is an OS file lock the kernel drops when its holder dies.
// So a live writer's record is refused, naming that process, and a dead writer's
// record opens to append. Reopened, h must equal the record's header.
func OpenLog(dir, ref string, h Header) (*Log, error) {
	path, err := recordPath(dir, ref)
	if err != nil {
		return nil, err
	}
	if h.Format != "" && h.Format != format {
		return nil, fmt.Errorf("replay: header names format %q, and this store writes %s", h.Format, format)
	}
	h.Format = format
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	lock, err := claim(path + ".lock")
	if err != nil {
		return nil, err
	}
	r, err := openLog(path, h)
	if err != nil {
		return nil, errors.Join(err, lock.Unlock())
	}
	r.lock = lock
	return r, nil
}

func recordPath(dir, ref string) (string, error) {
	if ref == "" || ref != filepath.Base(ref) || ref == "." || ref == ".." {
		return "", fmt.Errorf("replay: ref %q is not one path element", ref)
	}
	return filepath.Join(dir, ref, recordFile), nil
}

// claim takes the writer's lock at path and writes "<pid> <unix seconds>" into it. That
// is the job store's holder record, so a refused writer can name who holds it.
func claim(path string) (*flock.Flock, error) {
	lock := flock.New(path)
	got, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("replay: claim %s: %w", path, err)
	}
	if !got {
		return nil, fmt.Errorf("replay: the record beside %s is being written by %s,"+
			" and one process writes a record at a time, so wait for it to end", path, readHolder(path))
	}
	// In place: a rename would swap the inode the lock is held on.
	holder := fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().Unix())
	if err := os.WriteFile(path, []byte(holder), 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("replay: record the holder of %s: %w", path, err), lock.Unlock())
	}
	return lock, nil
}

func readHolder(path string) string {
	const unnamed = "a process that has not recorded itself"
	raw, err := os.ReadFile(path)
	if err != nil {
		return unnamed
	}
	f := strings.Fields(string(raw))
	if len(f) != 2 {
		return unnamed
	}
	pid, perr := strconv.Atoi(f[0])
	at, aerr := strconv.ParseInt(f[1], 10, 64)
	if perr != nil || aerr != nil {
		return unnamed
	}
	return fmt.Sprintf("pid %d, which took it %s ago", pid, time.Since(time.Unix(at, 0)).Round(time.Second))
}

func openLog(path string, h Header) (*Log, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("replay: %w", err)
	}
	// A dead writer's torn last line would glue onto the next append.
	if end := bytes.LastIndexByte(raw, '\n') + 1; end < len(raw) {
		if err := os.Truncate(path, int64(end)); err != nil {
			return nil, fmt.Errorf("replay: drop the unfinished last line of %s: %w", path, err)
		}
		raw = raw[:end]
	}
	r := &Log{path: path, open: map[int]Call{}}
	fresh := len(bytes.TrimSpace(raw)) == 0
	if !fresh {
		had, calls, finished, err := parse(path, raw)
		if err != nil {
			return nil, err
		}
		if finished {
			return nil, fmt.Errorf("replay: %s is finished: discard it to record again", path)
		}
		if !sameHeader(had, h) {
			return nil, fmt.Errorf("replay: %s was recorded for %s %v, not %s %v", path, had.Script, had.Args, h.Script, h.Args)
		}
		r.imports = had.Imports != nil || had.ImportsUnread != ""
		for _, c := range calls {
			if c.End == callOpen {
				r.open[c.Seq] = c
			}
		}
	}
	r.f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	if fresh {
		if err := r.append(h, true); err != nil {
			return nil, errors.Join(err, r.f.Close())
		}
	}
	return r, nil
}

func sameHeader(a, b Header) bool {
	return a.Format == b.Format && a.Script == b.Script && slices.Equal(a.Args, b.Args)
}

// begin appends e's begin line and syncs it to disk before returning, so a call that
// runs has a line a reboot keeps. e must be open, and its Seq not already open.
func (r *Log) begin(e Call) error {
	if e.End != callOpen {
		return fmt.Errorf("replay: begin %d %s: a begun call is open, not %q", e.Seq, e.Name, e.End)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.open[e.Seq]; ok {
		return fmt.Errorf("replay: begin %d %s: call %d is already open", e.Seq, e.Name, e.Seq)
	}
	if err := r.append(e, true); err != nil {
		return err
	}
	r.open[e.Seq] = e
	return nil
}

// outcome is what an ended call holds beside its end. Result is the encoded value,
// Error the text of a raised call, and Unencodable why Result is empty.
type outcome struct {
	Result, Error, Unencodable string
}

// end appends the end line of the open call seq, returned or raised. The line is not
// synced: the next begin or Close syncs it, and a reboot before then leaves the call open.
func (r *Log) end(seq int, end callEnd, o outcome) error {
	if end != callReturned && end != callRaised {
		return fmt.Errorf("replay: end %d: a call ends returned or raised, not %q", seq, end)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.open[seq]
	if !ok {
		return fmt.Errorf("replay: end %d: no open call has that seq", seq)
	}
	e.End, e.Result, e.Error, e.Unencodable = end, o.Result, o.Error, o.Unencodable
	if err := r.append(e, false); err != nil {
		return err
	}
	delete(r.open, seq)
	return nil
}

// Close syncs the record and releases its claim. A second Close is an error.
func (r *Log) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return fmt.Errorf("replay: %s is already closed", r.path)
	}
	err := errors.Join(r.noteUnreadLocked(), r.f.Sync(), r.f.Close(), r.lock.Unlock())
	r.f = nil
	if err != nil {
		return fmt.Errorf("replay: close %s: %w", r.path, err)
	}
	return nil
}

func (r *Log) append(v any, sync bool) error {
	if r.f == nil {
		return fmt.Errorf("replay: %s is closed", r.path)
	}
	b, err := codec.Marshal(v)
	if err != nil {
		return fmt.Errorf("replay: %s: %w", r.path, err)
	}
	if _, err := r.f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("replay: %s: %w", r.path, err)
	}
	if sync {
		if err := r.f.Sync(); err != nil {
			return fmt.Errorf("replay: sync %s: %w", r.path, err)
		}
	}
	return nil
}

// Read returns the record of ref under dir: its header, and its calls in seq order with
// each call's last line winning. A call with no end line reads as open. A last line no
// newline closes is what a crash left, and is not read. A missing record is an error
// that errors.Is matches to [RefusalNoRecord].
func Read(dir, ref string) (Header, []Call, error) {
	path, err := recordPath(dir, ref)
	if err != nil {
		return Header{}, nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Header{}, nil, fmt.Errorf("replay: no record at %s: %w", path, RefusalNoRecord)
	}
	if err != nil {
		return Header{}, nil, fmt.Errorf("replay: %w", err)
	}
	h, calls, _, err := parse(path, raw[:bytes.LastIndexByte(raw, '\n')+1])
	return h, calls, err
}

// footerLine is the last line of a record whose run finished. A call line has no
// "finished" key.
type footerLine struct {
	Finished *bool `json:"finished"`
}

func parse(path string, raw []byte) (Header, []Call, bool, error) {
	var h Header
	byKey := map[int]Call{}
	seen, finished := false, false
	n := 0
	for line := range bytes.SplitSeq(raw, []byte{'\n'}) {
		n++
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !seen {
			seen = true
			if err := codec.Unmarshal(line, &h); err != nil {
				return h, nil, false, &codec.LineError{Path: path, Line: n, Err: err}
			}
			if h.Format != format {
				return h, nil, false, fmt.Errorf("replay: %s:%d: format %q is not %s", path, n, h.Format, format)
			}
			continue
		}
		var f footerLine
		if err := codec.Unmarshal(line, &f); err == nil && f.Finished != nil {
			finished = *f.Finished
			continue
		}
		var imp struct {
			Imports map[string]string `json:"imports"`
			Unread  *string           `json:"imports_unread"`
		}
		if err := codec.Unmarshal(line, &imp); err == nil && (imp.Imports != nil || imp.Unread != nil) {
			h.Imports = imp.Imports
			if imp.Unread != nil {
				h.ImportsUnread = *imp.Unread
			}
			continue
		}
		var c Call
		if err := codec.Unmarshal(line, &c); err != nil {
			return h, nil, false, &codec.LineError{Path: path, Line: n, Err: err}
		}
		byKey[c.Seq] = c
	}
	if !seen {
		return h, nil, false, fmt.Errorf("replay: %s holds no header: %w", path, RefusalNoRecord)
	}
	calls := slices.Collect(maps.Values(byKey))
	slices.SortFunc(calls, func(a, b Call) int { return a.Seq - b.Seq })
	return h, calls, finished, nil
}

// Finish appends the footer that marks the run as having reached its end, and syncs it.
// Call it only for a run that ended on its own. A run its context cut off has not
// finished, and a rerun resumes it.
func (r *Log) Finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.noteUnreadLocked(); err != nil {
		return err
	}
	t := true
	return r.append(footerLine{Finished: &t}, true)
}

// noteImports appends the run's import digests. The next begin or Close syncs the
// line. A record holds one imports line, so a second is an error.
func (r *Log) noteImports(digests map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.imports {
		return fmt.Errorf("replay: %s already holds its imports", r.path)
	}
	if digests == nil {
		digests = map[string]string{}
	}
	if err := r.append(importsLine{Imports: digests}, false); err != nil {
		return err
	}
	r.imports = true
	return nil
}

// noteUnreadLocked says in the record that its imports never arrived, so a rerun
// refuses instead of reading the absence as no imports.
func (r *Log) noteUnreadLocked() error {
	if r.imports || r.f == nil {
		return nil
	}
	if err := r.append(unreadLine{Unread: "the run ended before its entry compiled"}, false); err != nil {
		return err
	}
	r.imports = true
	return nil
}

// Finished reports whether the record of ref under dir holds the footer a finished run
// writes. A missing record is an error that errors.Is matches to [RefusalNoRecord].
func Finished(dir, ref string) (bool, error) {
	path, err := recordPath(dir, ref)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("replay: no record at %s: %w", path, RefusalNoRecord)
	}
	if err != nil {
		return false, fmt.Errorf("replay: %w", err)
	}
	_, _, finished, err := parse(path, raw[:bytes.LastIndexByte(raw, '\n')+1])
	return finished, err
}

// Discard deletes the record of ref under dir. It takes the one-writer claim first, so
// a live writer's record is refused. A missing record is not an error.
func Discard(dir, ref string) error {
	path, err := recordPath(dir, ref)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	lock, err := claim(path + ".lock")
	if err != nil {
		return err
	}
	rmErr := os.Remove(path)
	if errors.Is(rmErr, fs.ErrNotExist) {
		rmErr = nil
	}
	return errors.Join(rmErr, lock.Unlock())
}
